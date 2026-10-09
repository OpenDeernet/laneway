package web

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/demo"
	"github.com/cornedor/laneway/internal/jira"
)

// The account menu's server links: none on Jira; on laneway-server the
// sign-in page, and the admin only for whoever administers something.
func TestAccount(t *testing.T) {
	for _, tc := range []struct {
		name           string
		laneway, admin bool
	}{{"jira", false, false}, {"member", true, false}, {"admin", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/rest/api/3/serverInfo":
					if tc.laneway {
						fmt.Fprint(w, `{"lanewayServerVersion":"1"}`)
					} else {
						fmt.Fprint(w, `{"deploymentType":"Cloud"}`)
					}
				case "/rest/api/3/mypermissions":
					fmt.Fprintf(w, `{"permissions":{"ADMINISTER":{"havePermission":false},"ADMINISTER_PROJECTS":{"havePermission":%t}}}`, tc.admin)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(site.Close)
			ts := tokensServer(t, site.URL)
			var got struct {
				Laneway               bool
				SignInPage, AdminPage string
			}
			if code := issueCall(t, "GET", ts.URL+"/api/account", nil, &got); code != 200 || got.Laneway != tc.laneway {
				t.Fatalf("%d %+v", code, got)
			}
			if want := map[bool]string{true: site.URL + "/admin#passkeys"}[tc.laneway]; got.SignInPage != want {
				t.Errorf("sign-in page %q, want %q", got.SignInPage, want)
			}
			if want := map[bool]string{true: site.URL + "/admin"}[tc.admin]; got.AdminPage != want {
				t.Errorf("admin page %q, want %q", got.AdminPage, want)
			}
		})
	}
}

type meInfo struct {
	Laneway, Uploaded       bool
	ProfilePage, SignInPage string
	TimeZone, Locale        string
	Me                      jira.Me
	Notifications           *jira.Notifications
	Sessions                []jira.Session
}

// Settings > Account on laneway-server: time zone, mail language and mail
// changed, an avatar uploaded and removed, every session ended.
func TestMe(t *testing.T) {
	d := demo.New(time.Now())
	d.Laneway()
	base, stop, err := d.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ts := tokensServer(t, base)
	me := func() meInfo {
		t.Helper()
		var m meInfo
		if code := issueCall(t, "GET", ts.URL+"/api/me", nil, &m); code != 200 {
			t.Fatalf("GET /api/me: %d", code)
		}
		return m
	}
	m := me()
	if !m.Laneway || m.TimeZone != "UTC" || m.Locale != "en_US" || m.Notifications == nil || !m.Notifications.Email || len(m.Sessions) != 2 || m.Uploaded || m.Me.AvatarURL != "" {
		t.Fatalf("%+v", m)
	}
	if code := issueCall(t, "PUT", ts.URL+"/api/me/timezone", map[string]string{"Value": "Mars/Olympus"}, nil); code != 400 {
		t.Errorf("bad time zone: %d", code)
	}
	for path, v := range map[string]any{"/api/me/timezone": map[string]string{"Value": "Europe/Amsterdam"}, "/api/me/locale": map[string]string{"Value": "nl_NL"},
		"/api/me/notifications": jira.Notifications{Email: true, Mode: "daily"}} {
		if code := issueCall(t, "PUT", ts.URL+path, v, nil); code != 200 {
			t.Errorf("PUT %s: %d", path, code)
		}
	}
	if m := me(); m.TimeZone != "Europe/Amsterdam" || m.Locale != "nl_NL" || m.Notifications.Mode != "daily" {
		t.Errorf("after changes: %+v", m)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	f, _ := mw.CreateFormFile("file", "me.png")
	f.Write([]byte("\x89PNG\r\n\x1a\n not really"))
	mw.Close()
	res, err := http.Post(ts.URL+"/api/me/avatar", mw.FormDataContentType(), &buf)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("upload: %v %v", err, res.Status)
	}
	res.Body.Close()
	if m := me(); !m.Uploaded || !strings.HasPrefix(m.Me.AvatarURL, "/api/avatar/") {
		t.Errorf("after upload: %+v", m)
	}
	var after jira.Me
	if code := issueCall(t, "DELETE", ts.URL+"/api/me/avatar", nil, &after); code != 200 || after.AvatarURL != "" {
		t.Errorf("remove: %d %+v", code, after)
	}

	if code := issueCall(t, "DELETE", ts.URL+"/api/me/sessions", nil, nil); code != 200 {
		t.Errorf("end sessions: %d", code)
	}
	if m := me(); len(m.Sessions) != 0 || m.Uploaded {
		t.Errorf("after sign out everywhere: %+v", m)
	}
}

// On Jira: no settings of its own, only where Jira keeps your profile.
func TestMeOnJira(t *testing.T) {
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	var m meInfo
	if code := issueCall(t, "GET", tokensServer(t, base).URL+"/api/me", nil, &m); code != 200 || m.Laneway || !strings.HasPrefix(m.ProfilePage, "https://id.atlassian.com/") {
		t.Fatalf("%d %+v", code, m)
	}
}

// A sign-in the server keeps from mail and sessions (an app's scopes):
// both null, and sessions not even asked.
func TestMeRefused(t *testing.T) {
	var sessions atomic.Bool
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/serverInfo":
			fmt.Fprint(w, `{"lanewayServerVersion":"1"}`)
		case "/rest/api/3/myself":
			fmt.Fprint(w, `{"accountId":"a1","displayName":"Ada"}`)
		case "/rest/api/3/mypreferences":
			fmt.Fprint(w, "UTC")
		case "/rest/api/3/mypreferences/locale":
			fmt.Fprint(w, `{"locale":"en_US"}`)
		case "/rest/api/3/universal_avatar/type/user/owner/a1":
			fmt.Fprint(w, `{"system":[],"custom":[]}`)
		case "/rest/laneway/1/myself/sessions":
			sessions.Store(true)
			fallthrough
		default:
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"errorMessages":["Unauthorized; scope does not match"]}`)
		}
	}))
	t.Cleanup(site.Close)
	var m meInfo
	if code := issueCall(t, "GET", tokensServer(t, site.URL).URL+"/api/me", nil, &m); code != 200 || !m.Laneway || m.Notifications != nil || m.Sessions != nil {
		t.Fatalf("%d %+v", code, m)
	}
	if sessions.Load() {
		t.Error("sessions asked after mail was refused")
	}
}
