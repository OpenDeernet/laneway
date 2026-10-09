package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/jira"
)

// fakeLanewayServer signs people in as a confidential app: the code and
// refresh token only with the app's secret and a DPoP proof; the API only
// with a DPoP access token. who is the account the next sign-in is for.
type fakeLanewayServer struct {
	*httptest.Server
	mu      sync.Mutex
	who     string
	codes   map[string]string // code -> account
	access  map[string]string // token -> account
	revoked []string
	n       int
}

func newFakeLanewayServer(t *testing.T) *fakeLanewayServer {
	f := &fakeLanewayServer{who: "acc1", codes: map[string]string{}, access: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/oauth/authorize":
			q := r.URL.Query()
			if q.Get("client_id") != "webapp" || q.Get("dpop_jkt") == "" || q.Get("code_challenge") == "" {
				http.Error(w, "bad", 400)
				return
			}
			f.n++
			code := fmt.Sprintf("c%d", f.n)
			f.codes[code] = f.who
			back, _ := url.Parse(q.Get("redirect_uri"))
			back.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
			http.Redirect(w, r, back.String(), http.StatusFound)
		case "/oauth/token":
			r.ParseForm()
			if r.PostForm.Get("client_id") != "webapp" || r.PostForm.Get("client_secret") != "s3cret" || r.Header.Get("DPoP") == "" {
				w.WriteHeader(401)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
				return
			}
			who := ""
			switch r.PostForm.Get("grant_type") {
			case "authorization_code":
				who = f.codes[r.PostForm.Get("code")]
				delete(f.codes, r.PostForm.Get("code"))
			case "refresh_token":
				who, _, _ = strings.Cut(strings.TrimPrefix(r.PostForm.Get("refresh_token"), "r-"), "-")
			}
			if who == "" {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			f.n++
			a := fmt.Sprintf("a-%s-%d", who, f.n)
			f.access[a] = who
			json.NewEncoder(w).Encode(map[string]any{"access_token": a, "token_type": "DPoP", "expires_in": 900, "refresh_token": fmt.Sprintf("r-%s-%d", who, f.n)})
		case "/oauth/revoke":
			r.ParseForm()
			f.revoked = append(f.revoked, r.PostForm.Get("token"))
		default:
			tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "DPoP ")
			who := f.access[tok]
			if !ok || who == "" || r.Header.Get("DPoP") == "" {
				w.WriteHeader(401)
				return
			}
			if r.URL.Path == "/rest/api/3/myself" {
				json.NewEncoder(w).Encode(map[string]any{"accountId": who, "displayName": "Person " + who, "emailAddress": who + "@x.test"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// browserFor is a browser that follows redirects across the gateway and
// the fake laneway, keeping each one's cookies.
func browserFor() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func TestHostedGateway(t *testing.T) {
	lw := newFakeLanewayServer(t)
	data := t.TempDir()
	gw := httptest.NewUnstartedServer(nil)
	h, err := newHosted(lw.URL, "http://"+gw.Listener.Addr().String(), data, jira.OAuthClient{ID: "webapp", Secret: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	h.ctx = t.Context()
	gw.Config.Handler = h
	gw.Start()
	defer gw.Close()

	if _, err := newHosted(lw.URL, gw.URL, data, jira.OAuthClient{ID: "webapp"}); err == nil {
		t.Error("a gateway without the client secret started")
	}

	// Signed out: the API says so; a page goes through laneway's sign-in and back.
	anon := &http.Client{}
	if res, _ := anon.Get(gw.URL + "/api/session"); res.StatusCode != 401 {
		t.Errorf("API signed out: %d", res.StatusCode)
	} else {
		// A page from the browser's cache learns where to sign in.
		var b map[string]string
		json.NewDecoder(res.Body).Decode(&b)
		res.Body.Close()
		if b["signedOut"] != "/auth/login" {
			t.Errorf("signed-out answer %v", b)
		}
	}
	// The app's files and service worker come without a session; pages
	// don't, and the worker leaves /auth/ to the server.
	if res, _ := anon.Get(gw.URL + "/sw.js"); res.StatusCode != 200 {
		t.Errorf("sw.js signed out: %d", res.StatusCode)
	} else {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if !strings.Contains(string(b), "'/auth/'") {
			t.Errorf("the worker doesn't leave /auth/ alone")
		}
	}
	if res, _ := anon.Get(gw.URL + "/js/app.js"); res.StatusCode != 200 {
		t.Errorf("app.js signed out: %d", res.StatusCode)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if res, _ := noRedirect.Get(gw.URL + "/board/ABC/1"); res.StatusCode != http.StatusFound {
		t.Errorf("a page signed out: %d, want the sign-in", res.StatusCode)
	}
	via := browserFor()
	if res, err := via.Get(gw.URL + "/auth/login?next=" + url.QueryEscape("/board/ABC/2?x=1")); err != nil || res.Request.URL.RequestURI() != "/board/ABC/2?x=1" {
		t.Errorf("sign-in through /auth/login landed at %v %v", res.Request.URL, err)
	}
	if res, _ := browserFor().Get(gw.URL + "/auth/login?next=" + url.QueryEscape("//evil.example/")); res.Request.URL.Host != gu(gw.URL) {
		t.Errorf("an outside next was followed: %v", res.Request.URL)
	}
	ada := browserFor()
	res, err := ada.Get(gw.URL + "/board/ABC/1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/board/ABC/1" {
		t.Errorf("after sign-in at %s, want /board/ABC/1", res.Request.URL)
	}
	session := func(c *http.Client) map[string]any {
		t.Helper()
		res, err := c.Get(gw.URL + "/api/session")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var s map[string]any
		raw, _ := io.ReadAll(res.Body)
		json.Unmarshal(raw, &s)
		if res.StatusCode != 200 {
			t.Fatalf("session %d: %s", res.StatusCode, raw)
		}
		return s
	}
	s := session(ada)
	if s["hosted"] != true || s["me"].(map[string]any)["AccountID"] != "acc1" {
		t.Errorf("ada's session: %v", s)
	}
	// Her sign-in lives in her own directory, only she reads it; sessions
	// are kept hashed.
	fi, err := os.Stat(filepath.Join(data, "people", "acc1", "sign-in.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("sign-in file: %v %v", fi, err)
	}
	var cookie string
	gu, _ := url.Parse(gw.URL)
	for _, c := range ada.Jar.Cookies(gu) {
		if c.Name == hostedCookie {
			cookie = c.Value
		}
	}
	raw, _ := os.ReadFile(filepath.Join(data, hostedSessionFile))
	if cookie == "" || strings.Contains(string(raw), cookie) || !strings.Contains(string(raw), "acc1") {
		t.Errorf("sessions file %s (cookie %q)", raw, cookie)
	}

	// Another person: their own app and account.
	lw.mu.Lock()
	lw.who = "acc2"
	lw.mu.Unlock()
	bob := browserFor()
	res, _ = bob.Get(gw.URL + "/")
	res.Body.Close()
	if s := session(bob); s["me"].(map[string]any)["AccountID"] != "acc2" {
		t.Errorf("bob's session: %v", s)
	}
	if s := session(ada); s["me"].(map[string]any)["AccountID"] != "acc1" {
		t.Errorf("ada's session after bob signed in: %v", s)
	}
	// Nothing in it acts as this machine.
	req, _ := http.NewRequest("POST", gw.URL+"/api/actions/x/run", strings.NewReader("{}"))
	if res, _ := ada.Do(req); res.StatusCode != 403 {
		t.Errorf("an action on the hosted app: %d", res.StatusCode)
	}

	// A callback that didn't start here, a foreign Host.
	if res, _ := anon.Get(gw.URL + "/auth/callback?code=c1&state=nope"); res.StatusCode != 400 {
		t.Errorf("stray callback: %d", res.StatusCode)
	}
	req, _ = http.NewRequest("GET", gw.URL+"/api/session", nil)
	req.Host = "evil.example"
	if res, _ := ada.Do(req); res.StatusCode != 403 {
		t.Errorf("foreign Host: %d", res.StatusCode)
	}

	// Signing out revokes on laneway and ends the session.
	req, _ = http.NewRequest("POST", gw.URL+"/auth/logout", nil)
	req.Header.Set("Origin", gw.URL)
	noFollow := *ada
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if res, _ := noFollow.Do(req); res.StatusCode != http.StatusSeeOther {
		t.Errorf("logout: %d", res.StatusCode)
	}
	if len(lw.revoked) != 1 || !strings.HasPrefix(lw.revoked[0], "r-acc1-") {
		t.Errorf("revoked %v", lw.revoked)
	}
	if res, _ := (&http.Client{Jar: ada.Jar, CheckRedirect: noFollow.CheckRedirect}).Get(gw.URL + "/api/session"); res.StatusCode != 401 {
		t.Errorf("session after logout: %d", res.StatusCode)
	}
	if raw, _ := os.ReadFile(filepath.Join(data, "people", "acc1", "sign-in.json")); len(raw) != 0 {
		t.Errorf("sign-in kept after logout: %s", raw)
	}
}

// gu is a URL's host.
func gu(raw string) string { u, _ := url.Parse(raw); return u.Host }

func TestHostedUI(t *testing.T) {
	u := hostedUI(config.UIConfig{LLM: "rm -rf ~", Calendar: "/etc/passwd", StaleDays: 4})
	if u.LLM != "" || u.Calendar != "" || u.StaleDays != 4 {
		t.Errorf("hosted ui %+v", u)
	}
}
