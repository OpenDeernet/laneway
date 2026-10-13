package jira

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// atlassianFake is the token endpoint and Jira behind /ex/jira/cid: a
// refresh token works once.
type atlassianFake struct {
	*httptest.Server
	mu      sync.Mutex
	live    map[string]bool // refresh tokens
	access  map[string]bool
	n       int
	headers []string // Authorization of each API request
}

func newAtlassianFake(t *testing.T) *atlassianFake {
	f := &atlassianFake{live: map[string]bool{"r0": true}, access: map[string]bool{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/oauth/token" {
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["grant_type"] != "refresh_token" || !f.live[b["refresh_token"]] || b["client_secret"] != "s" {
				w.WriteHeader(403)
				w.Write([]byte(`{"error":"invalid_grant","error_description":"Unknown or invalid refresh token."}`))
				return
			}
			delete(f.live, b["refresh_token"])
			f.n++
			a, rt := "a"+string(rune('0'+f.n)), "r"+string(rune('0'+f.n))
			f.live[rt], f.access[a] = true, true
			json.NewEncoder(w).Encode(map[string]any{"access_token": a, "refresh_token": rt, "expires_in": 3600})
			return
		}
		f.headers = append(f.headers, r.Header.Get("Authorization"))
		if !f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"accountId":"me"}`))
	}))
	t.Cleanup(f.Close)
	auth, api := AtlassianAuth, AtlassianAPI
	AtlassianAuth, AtlassianAPI = f.URL, f.URL
	t.Cleanup(func() { AtlassianAuth, AtlassianAPI = auth, api })
	return f
}

func atlassianClient(st CredentialStore) *Client {
	return New(Config{BaseURL: "https://acme.atlassian.net", Atlassian: st, CloudID: "cid", OAuthClient: OAuthClient{ID: "c", Secret: "s"}})
}

func TestAtlassianRefreshRotates(t *testing.T) {
	f := newAtlassianFake(t)
	st := &memStore{raw: []byte(`{"refresh":"r0","cloudId":"cid","account":"me"}`)}
	c := atlassianClient(st)
	if _, err := c.Myself(t.Context()); err != nil {
		t.Fatal(err)
	}
	var kept AtlassianCredentials
	json.Unmarshal(st.raw, &kept)
	if kept.Refresh != "r1" || kept.Access != "a1" || kept.CloudID != "cid" {
		t.Errorf("kept %s", st.raw)
	}
	// A token the API turns away is refreshed once, with the new refresh
	// token.
	f.mu.Lock()
	delete(f.access, "a1")
	f.mu.Unlock()
	c.Invalidate("")
	if err := c.do(t.Context(), http.MethodGet, "/rest/api/3/myself", "me", nil, nil); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(st.raw, &kept)
	if kept.Refresh != "r2" {
		t.Errorf("after a turned-away token: %s", st.raw)
	}
	if c.BrowseURL("A-1") != "https://acme.atlassian.net/browse/A-1" {
		t.Errorf("browse %s", c.BrowseURL("A-1"))
	}
}

func TestAtlassianGrantEnded(t *testing.T) {
	newAtlassianFake(t)
	st := &memStore{raw: []byte(`{"refresh":"gone","cloudId":"cid"}`)}
	if _, err := atlassianClient(st).Myself(t.Context()); !errors.Is(err, ErrSignedOut) {
		t.Errorf("an ended grant: %v", err)
	}
	if len(st.raw) != 0 {
		t.Errorf("an ended sign-in kept: %s", st.raw)
	}
}

// The bearer token goes to the site's API roots and nowhere else.
func TestAtlassianTokenStaysHome(t *testing.T) {
	f := newAtlassianFake(t)
	st := &memStore{raw: []byte(`{"refresh":"r0","cloudId":"cid","access":"a9","expires":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `"}`)}
	o := atlassianClient(st).atl
	for _, target := range []string{
		f.URL + "/ex/jira/cid/rest/api/3/myself",
		f.URL + "/ex/confluence/cid/wiki/api/v2/pages/1",
	} {
		u, _ := url.Parse(target)
		if !o.allowed(u) {
			t.Errorf("refused %s", target)
		}
	}
	for _, target := range []string{
		f.URL + "/ex/jira/other/rest/api/3/myself",
		f.URL + "/ex/jira/cidx/rest/api/3/myself",
		f.URL + "/ex/jira/cid/../other/rest",
		f.URL + "/ex/jira/cid/%2e%2e/other/rest",
		f.URL + "/me",
		"https://acme.atlassian.net/rest/api/3/myself",
		"https://evil.example/ex/jira/cid/rest",
		strings.Replace(f.URL, "://", "://u:p@", 1) + "/ex/jira/cid/rest",
	} {
		u, _ := url.Parse(target)
		if o.allowed(u) {
			t.Errorf("token allowed to %s", target)
		}
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if _, err := o.authorize(req); err == nil || req.Header.Get("Authorization") != "" {
			t.Errorf("token set for %s", target)
		}
	}
}

func TestAtlassianLoginSite(t *testing.T) {
	l := BeginAtlassianLogin(OAuthClient{ID: "c", Secret: "s"}, "https://ACME.atlassian.net/", "", "https://gw.example/auth/callback", AtlassianScopes)
	u, _ := url.Parse(l.AuthURL)
	q := u.Query()
	if q.Get("state") != l.State || len(l.State) < 32 || q.Get("prompt") != "consent" || q.Get("audience") != "api.atlassian.com" || strings.Contains(l.AuthURL, "client_secret") {
		t.Errorf("auth url %s", l.AuthURL)
	}
	if _, err := l.Finish(t.Context(), url.Values{"state": {"other"}, "code": {"x"}}); err == nil {
		t.Error("a foreign state was taken")
	}
}
