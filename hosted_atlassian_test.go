package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cornedor/laneway/internal/jira"
)

const testSealKey = "0123456789abcdef0123456789abcdef"

// fakeAtlassian is auth.atlassian.com and api.atlassian.com in one: a
// confidential app's code and rotating refresh tokens, the sites a grant
// takes in, and Jira behind /ex/jira/{cloudId} with a bearer token.
type fakeAtlassian struct {
	*httptest.Server
	mu     sync.Mutex
	who    string
	site   string   // the site a consent takes in
	picks  []string // sites the next consents take in, before site
	grants map[string]map[string]bool
	codes  map[string]string
	access map[string]string // token -> account
	n      int
	paths  []string // the API paths asked
}

func newFakeAtlassian(t *testing.T) *fakeAtlassian {
	f := &fakeAtlassian{who: "5b10a2844c20165700ede21g", site: "https://acme.atlassian.net", codes: map[string]string{}, access: map[string]string{}, grants: map[string]map[string]bool{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		bearer := func() string { return f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] }
		switch {
		case r.URL.Path == "/authorize":
			q := r.URL.Query()
			if q.Get("client_id") != "webapp" || q.Get("audience") != "api.atlassian.com" || q.Get("response_type") != "code" || !strings.Contains(q.Get("scope"), "offline_access") {
				http.Error(w, "bad", 400)
				return
			}
			pick := f.site
			if len(f.picks) > 0 {
				pick, f.picks = f.picks[0], f.picks[1:]
			}
			if f.grants[f.who] == nil {
				f.grants[f.who] = map[string]bool{}
			}
			f.grants[f.who][pick] = true
			f.n++
			code := fmt.Sprintf("c%d", f.n)
			f.codes[code] = f.who
			back, _ := url.Parse(q.Get("redirect_uri"))
			back.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
			http.Redirect(w, r, back.String(), http.StatusFound)
		case r.URL.Path == "/oauth/token":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if r.Header.Get("Content-Type") != "application/json" || b["client_id"] != "webapp" || b["client_secret"] != "s3cret" {
				w.WriteHeader(401)
				json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
				return
			}
			who := ""
			switch b["grant_type"] {
			case "authorization_code":
				who = f.codes[b["code"]]
				delete(f.codes, b["code"])
			case "refresh_token":
				who, _, _ = strings.Cut(strings.TrimPrefix(b["refresh_token"], "r-"), "-")
			}
			if who == "" {
				w.WriteHeader(403)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			f.n++
			a := fmt.Sprintf("a%d", f.n)
			f.access[a] = who
			json.NewEncoder(w).Encode(map[string]any{"access_token": a, "token_type": "Bearer", "expires_in": 3600, "refresh_token": fmt.Sprintf("r-%s-%d", who, f.n)})
		case r.URL.Path == "/oauth/token/accessible-resources" && bearer() != "":
			ids := map[string]string{"https://acme.atlassian.net": "11111111-2222-3333-4444-555555555555", "https://wiki.atlassian.net": "22222222-2222-3333-4444-555555555555",
				"https://other.atlassian.net": "33333333-2222-3333-4444-555555555555"}
			var out []map[string]any
			for site := range f.grants[bearer()] {
				out = append(out, map[string]any{"id": ids[site], "url": site})
			}
			json.NewEncoder(w).Encode(out)
		case r.URL.Path == "/me" && bearer() != "":
			json.NewEncoder(w).Encode(map[string]string{"account_id": bearer()})
		case strings.HasPrefix(r.URL.Path, "/ex/jira/11111111-2222-3333-4444-555555555555/") && bearer() != "":
			p := strings.TrimPrefix(r.URL.Path, "/ex/jira/11111111-2222-3333-4444-555555555555")
			f.paths = append(f.paths, p)
			w.Header().Set("Content-Type", "application/json")
			if p == "/rest/api/3/myself" {
				json.NewEncoder(w).Encode(map[string]any{"accountId": bearer(), "displayName": "Person"})
				return
			}
			w.Write([]byte(`{}`))
		case strings.HasPrefix(r.URL.Path, "/ex/confluence/") && bearer() != "":
			f.paths = append(f.paths, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"7","title":"Page"}`))
		default:
			w.WriteHeader(401)
		}
	}))
	t.Cleanup(f.Close)
	auth, api := jira.AtlassianAuth, jira.AtlassianAPI
	jira.AtlassianAuth, jira.AtlassianAPI = f.URL, f.URL
	t.Cleanup(func() { jira.AtlassianAuth, jira.AtlassianAPI = auth, api })
	return f
}

func newAtlassianGateway(t *testing.T, wiki string) (*fakeAtlassian, *hosted, string) {
	f := newFakeAtlassian(t)
	gw := httptest.NewUnstartedServer(nil)
	h, err := newHostedAtlassian("https://acme.atlassian.net", wiki, "http://"+gw.Listener.Addr().String(), t.TempDir(), jira.AtlassianScopes, testSealKey,
		jira.OAuthClient{ID: "webapp", Secret: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	h.ctx = t.Context()
	gw.Config.Handler = h
	gw.Start()
	t.Cleanup(gw.Close)
	return f, h, gw.URL
}

func TestHostedAtlassian(t *testing.T) {
	f, h, gw := newAtlassianGateway(t, "")
	ada := browserFor()
	res, err := ada.Get(gw + "/board/ABC/1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/board/ABC/1" {
		t.Fatalf("after sign-in at %s", res.Request.URL)
	}
	res, err = ada.Get(gw + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(raw), f.who) {
		t.Errorf("session %d: %s", res.StatusCode, raw)
	}
	// Jira was asked through Atlassian's API for the site.
	f.mu.Lock()
	asked := strings.Join(f.paths, " ")
	f.mu.Unlock()
	if !strings.Contains(asked, "/rest/api/3/myself") {
		t.Errorf("asked %s", asked)
	}
	// The sign-in is kept sealed: no token readable, only this server's
	// key opens it.
	p := filepath.Join(h.data, "people", f.who, "sign-in.json")
	sealed, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("r-")) || bytes.Contains(sealed, []byte("refresh")) {
		t.Errorf("sign-in kept in the clear: %s", sealed)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("sign-in mode %v", fi.Mode())
	}
	// Another person's file put in place doesn't open.
	st, _ := h.atlassianStore("someone-else")
	os.MkdirAll(h.accountDir("someone-else"), 0o700)
	os.WriteFile(filepath.Join(h.accountDir("someone-else"), "sign-in.json"), sealed, 0o600)
	if raw, _ := st.Load(); len(raw) != 0 {
		t.Error("a sign-in opened for another account")
	}

	logout(t, ada, gw)
	if raw, _ := os.ReadFile(p); len(raw) != 0 {
		t.Error("sign-in kept after logout")
	}
}

// A grant that leaves the site out is refused.
func TestHostedAtlassianOtherSite(t *testing.T) {
	f, _, gw := newAtlassianGateway(t, "")
	f.site = "https://other.atlassian.net"
	res, err := browserFor().Get(gw + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("a grant without the site: %d", res.StatusCode)
	}
}

func TestHostedAtlassianFlags(t *testing.T) {
	c := jira.OAuthClient{ID: "a", Secret: "b"}
	for name, try := range map[string]func() error{
		"no seal key": func() error {
			_, err := newHostedAtlassian("https://acme.atlassian.net", "", "https://x.example", t.TempDir(), jira.AtlassianScopes, "short", c)
			return err
		},
		"http site": func() error {
			_, err := newHostedAtlassian("http://acme.atlassian.net", "", "https://x.example", t.TempDir(), jira.AtlassianScopes, testSealKey, c)
			return err
		},
		"site path": func() error {
			_, err := newHostedAtlassian("https://acme.atlassian.net/jira", "", "https://x.example", t.TempDir(), jira.AtlassianScopes, testSealKey, c)
			return err
		},
		"no offline": func() error {
			_, err := newHostedAtlassian("https://acme.atlassian.net", "", "https://x.example", t.TempDir(), "read:jira-work", testSealKey, c)
			return err
		},
		"no secret": func() error {
			_, err := newHostedAtlassian("https://acme.atlassian.net", "", "https://x.example", t.TempDir(), jira.AtlassianScopes, testSealKey, jira.OAuthClient{ID: "a"})
			return err
		},
	} {
		if try() == nil {
			t.Errorf("%s: started", name)
		}
	}
}

// Confluence on another site: a second consent takes it in, and its API
// is asked by that site's cloud id.
func TestHostedAtlassianConfluenceSite(t *testing.T) {
	f, h, gw := newAtlassianGateway(t, "https://wiki.atlassian.net")
	f.picks = []string{"https://acme.atlassian.net", "https://wiki.atlassian.net"}
	res, err := browserFor().Get(gw + "/board/ABC/1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/board/ABC/1" {
		t.Fatalf("after sign-in at %s", res.Request.URL)
	}
	opt, err := h.options(f.who)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opt.Client.ConfluencePage(t.Context(), "7"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	asked := strings.Join(f.paths, " ")
	f.mu.Unlock()
	if !strings.Contains(asked, "/ex/confluence/22222222-2222-3333-4444-555555555555/wiki/api/v2/pages/7") {
		t.Errorf("asked %s", asked)
	}
	if id := opt.Client.PageID("https://wiki.atlassian.net/wiki/spaces/X/pages/9/T"); id != "9" {
		t.Errorf("a page on the wiki site: %q", id)
	}
}

// A person who leaves Confluence's site out twice still gets Jira.
func TestHostedAtlassianConfluenceLeftOut(t *testing.T) {
	f, _, gw := newAtlassianGateway(t, "https://wiki.atlassian.net")
	res, err := browserFor().Get(gw + "/board/ABC/1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/board/ABC/1" || res.StatusCode != 200 {
		t.Errorf("after sign-in at %s %d", res.Request.URL, res.StatusCode)
	}
	f.mu.Lock()
	n := len(f.codes)
	f.mu.Unlock()
	if n != 0 {
		t.Errorf("%d codes left", n)
	}
}
