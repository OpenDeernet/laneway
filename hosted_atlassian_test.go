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
	"time"

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
	// reported are the account ids of each personal data report; closed
	// and updated what the next answers say of them.
	reported        [][]string
	closed, updated []string
	cycle           string
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
		case r.URL.Path == "/app/report-accounts/" && bearer() != "":
			var b struct {
				Accounts []jira.AccountReport `json:"accounts"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			var ids []string
			for _, a := range b.Accounts {
				ids = append(ids, a.AccountID)
			}
			f.reported = append(f.reported, ids)
			if f.cycle != "" {
				w.Header().Set("Cycle-Period", f.cycle)
			}
			var out []map[string]string
			for _, id := range f.closed {
				out = append(out, map[string]string{"accountId": id, "status": "closed"})
			}
			for _, id := range f.updated {
				out = append(out, map[string]string{"accountId": id, "status": "updated"})
			}
			if len(out) == 0 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"accounts": out})
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

// Personal data reporting: the accounts kept are reported once a cycle; a
// closed one is erased, the others stay.
func TestHostedAtlassianReport(t *testing.T) {
	f, h, gw := newAtlassianGateway(t, "")
	ada := browserFor()
	if res, err := ada.Get(gw + "/"); err == nil {
		res.Body.Close()
	}
	f.mu.Lock()
	ada1, bob := f.who, "bob-0001"
	f.who = bob
	f.mu.Unlock()
	if res, err := browserFor().Get(gw + "/"); err == nil {
		res.Body.Close()
	}
	now := time.Now()
	h.reportIfDue(t.Context(), now)
	f.mu.Lock()
	if len(f.reported) != 1 || len(f.reported[0]) != 2 {
		t.Fatalf("reported %v", f.reported)
	}
	f.closed = []string{ada1, "not-kept"}
	f.cycle = "3600"
	f.mu.Unlock()
	// Not due yet: a week.
	h.reportIfDue(t.Context(), now.Add(time.Hour))
	f.mu.Lock()
	if len(f.reported) != 1 {
		t.Errorf("reported before due: %v", f.reported)
	}
	f.mu.Unlock()
	h.reportIfDue(t.Context(), now.Add(8*24*time.Hour))
	if _, err := os.Stat(h.accountDir(ada1)); !os.IsNotExist(err) {
		t.Errorf("a closed account kept: %v", err)
	}
	if _, err := os.Stat(h.accountDir(bob)); err != nil {
		t.Errorf("bob's directory: %v", err)
	}
	h.mu.Lock()
	for _, s := range h.sessions {
		if s.Account == ada1 {
			t.Error("a closed account's session kept")
		}
	}
	h.mu.Unlock()
	// The answer's Cycle-Period sets the next one.
	var st reportState
	raw, _ := os.ReadFile(filepath.Join(h.data, reportFile))
	json.Unmarshal(raw, &st)
	if d := st.Next.Sub(now.Add(8 * 24 * time.Hour)); d != time.Hour {
		t.Errorf("next report in %s, want 1h", d)
	}
}
