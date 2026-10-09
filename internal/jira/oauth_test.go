package jira

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore is a CredentialStore in memory.
type memStore struct {
	mu  sync.Mutex
	raw []byte
}

func (m *memStore) Load() ([]byte, error) { m.mu.Lock(); defer m.mu.Unlock(); return m.raw, nil }
func (m *memStore) Save(b []byte) error   { m.mu.Lock(); defer m.mu.Unlock(); m.raw = b; return nil }

// fakeLaneway is the least of laneway-server's OAuth with DPoP: it checks
// every proof (signature, htm, htu, ath, key binding) as the server does.
type fakeLaneway struct {
	*httptest.Server
	t         *testing.T
	mu        sync.Mutex
	codes     map[string]string // code -> jkt (dpop_jkt)
	access    map[string]string // access token -> jkt
	refresh   map[string]string // refresh token -> jkt
	devices   map[string]string // device code -> "pending" or "allowed"
	jtis      map[string]bool
	n         int
	refreshs  int
	revoked   []string
	clock     time.Duration // how far its clock is ahead
	noRevoke  bool          // the revoke call fails
	badProofs bool          // every proof is refused
}

func newFakeLaneway(t *testing.T) *fakeLaneway {
	f := &fakeLaneway{t: t, codes: map[string]string{}, access: map[string]string{}, refresh: map[string]string{}, devices: map[string]string{}, jtis: map[string]bool{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// proofKey checks r's DPoP proof for the request (and access, if not
// "") and returns the key's thumbprint, "" when it doesn't hold.
func (f *fakeLaneway) proofKey(r *http.Request, access string) string {
	if f.badProofs {
		return ""
	}
	parts := strings.Split(r.Header.Get("DPoP"), ".")
	if len(parts) != 3 {
		return ""
	}
	dec := func(s string, v any) bool {
		b, err := base64.RawURLEncoding.DecodeString(s)
		return err == nil && json.Unmarshal(b, v) == nil
	}
	var hdr struct {
		Typ, Alg string
		JWK      map[string]string
	}
	var c struct {
		JTI, HTM, HTU, ATH string
		IAT                int64
	}
	if !dec(parts[0], &hdr) || !dec(parts[1], &c) || hdr.Typ != "dpop+jwt" || hdr.Alg != "ES256" {
		return ""
	}
	xb, _ := base64.RawURLEncoding.DecodeString(hdr.JWK["x"])
	yb, _ := base64.RawURLEncoding.DecodeString(hdr.JWK["y"])
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return ""
	}
	if c.HTM != r.Method || c.HTU != f.URL+r.URL.Path || time.Now().Add(f.clock).Sub(time.Unix(c.IAT, 0)).Abs() > time.Minute || f.jtis[c.JTI] {
		return ""
	}
	f.jtis[c.JTI] = true
	if access != "" {
		h := sha256.Sum256([]byte(access))
		if c.ATH != base64.RawURLEncoding.EncodeToString(h[:]) {
			return ""
		}
	}
	k := &ecdsa.PrivateKey{PublicKey: *pub}
	return thumbprint(k)
}

func (f *fakeLaneway) issue(w http.ResponseWriter, jkt string) {
	f.n++
	a, rt := fmt.Sprintf("lwo_%d", f.n), fmt.Sprintf("lwr_%d", f.n)
	f.access[a], f.refresh[rt] = jkt, jkt
	json.NewEncoder(w).Encode(map[string]any{"access_token": a, "token_type": "DPoP", "expires_in": 900, "refresh_token": rt})
}

func (f *fakeLaneway) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Date", time.Now().Add(f.clock).UTC().Format(http.TimeFormat))
	fail := func(code string) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	switch r.URL.Path {
	case "/oauth/authorize":
		// The person signs in and allows it at once.
		q := r.URL.Query()
		if q.Get("client_id") != ClientID || q.Get("code_challenge_method") != "S256" || q.Get("dpop_jkt") == "" {
			http.Error(w, "bad request", 400)
			return
		}
		f.n++
		code := fmt.Sprintf("lwc_%d", f.n)
		f.codes[code] = q.Get("dpop_jkt")
		back, _ := url.Parse(q.Get("redirect_uri"))
		bq := back.Query()
		bq.Set("code", code)
		bq.Set("state", q.Get("state"))
		back.RawQuery = bq.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	case "/oauth/token":
		r.ParseForm()
		jkt := f.proofKey(r, "")
		if jkt == "" {
			fail("invalid_dpop_proof")
			return
		}
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			if want, ok := f.codes[r.PostForm.Get("code")]; !ok || want != jkt || r.PostForm.Get("code_verifier") == "" {
				fail("invalid_grant")
				return
			}
			delete(f.codes, r.PostForm.Get("code"))
		case "refresh_token":
			if want, ok := f.refresh[r.PostForm.Get("refresh_token")]; !ok || want != jkt {
				fail("invalid_grant")
				return
			}
			delete(f.refresh, r.PostForm.Get("refresh_token")) // rotates
			f.refreshs++
		case "urn:ietf:params:oauth:grant-type:device_code":
			switch f.devices[r.PostForm.Get("device_code")] {
			case "pending":
				f.devices[r.PostForm.Get("device_code")] = "allowed" // allowed while it waited
				fail("authorization_pending")
				return
			case "allowed":
			default:
				fail("invalid_grant")
				return
			}
		default:
			fail("unsupported_grant_type")
			return
		}
		f.issue(w, jkt)
	case "/oauth/device/code":
		if f.proofKey(r, "") == "" {
			fail("invalid_dpop_proof")
			return
		}
		f.devices["lwd_1"] = "pending"
		json.NewEncoder(w).Encode(map[string]any{"device_code": "lwd_1", "user_code": "BCDF-GHJK", "verification_uri": f.URL + "/device",
			"verification_uri_complete": f.URL + "/device?user_code=BCDF-GHJK", "expires_in": 600, "interval": 0})
	case "/oauth/revoke":
		r.ParseForm()
		if f.noRevoke {
			w.WriteHeader(503)
			return
		}
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		delete(f.refresh, r.PostForm.Get("token"))
	default:
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "DPoP ")
		jkt, bound := f.access[tok]
		if !ok || !bound || f.proofKey(r, tok) != jkt {
			w.Header().Set("WWW-Authenticate", `DPoP error="invalid_token"`)
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"accountId": "a1", "displayName": "Dev", "emailAddress": "dev@x.test"})
	}
}

func TestLoopbackLoginAndDPoPRequests(t *testing.T) {
	f := newFakeLaneway(t)
	ctx := context.Background()
	// The "browser" follows the sign-in page straight back to the listener.
	creds, err := LoopbackLogin(ctx, f.URL, func(u string) error {
		go func() {
			resp, err := http.Get(u)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if creds.Account != "dev@x.test" || creds.Refresh == "" || creds.Key == "" {
		t.Fatalf("credentials: %+v", creds)
	}
	st := &memStore{}
	raw, _ := json.Marshal(creds)
	st.Save(raw)

	c := New(Config{BaseURL: f.URL, SignIn: st})
	if !c.Enabled() || c.SignedInAs() != "dev@x.test" {
		t.Fatalf("enabled %v as %q", c.Enabled(), c.SignedInAs())
	}
	u, err := c.Myself(ctx)
	if err != nil || u.AccountID != "a1" {
		t.Fatalf("myself: %v %v", u, err)
	}
	// The refresh rotated and the new token was kept.
	var kept Credentials
	json.Unmarshal(st.raw, &kept)
	if kept.Refresh == creds.Refresh || f.refreshs != 1 {
		t.Errorf("refresh token not rotated and kept: %q (refreshes %d)", kept.Refresh, f.refreshs)
	}

	// A token the server turned away is refreshed and the request sent again.
	f.mu.Lock()
	f.access = map[string]string{}
	f.mu.Unlock()
	c.mu.Lock()
	c.myself = nil
	c.mu.Unlock()
	if _, err := c.Myself(ctx); err != nil {
		t.Fatalf("after the access token was dropped: %v", err)
	}
	if f.refreshs != 2 {
		t.Errorf("refreshes %d, want 2", f.refreshs)
	}

	// Signing out revokes and clears.
	if err := c.SignOut(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 1 || len(st.raw) != 0 {
		t.Errorf("revoked %v, store %q", f.revoked, st.raw)
	}
	c2 := New(Config{BaseURL: f.URL, SignIn: st})
	if _, err := c2.Myself(ctx); err == nil || !strings.Contains(err.Error(), "laneway login") {
		t.Errorf("after sign-out: %v", err)
	}
}

func TestDeviceLogin(t *testing.T) {
	f := newFakeLaneway(t)
	shortPolls(t)
	var shown string
	creds, err := DeviceLogin(context.Background(), f.URL, func(code, uri, complete string) { shown = code + " " + uri })
	if err != nil {
		t.Fatal(err)
	}
	if shown != "BCDF-GHJK "+f.URL+"/device" || creds.Account != "dev@x.test" {
		t.Errorf("shown %q, credentials %+v", shown, creds)
	}
}

func TestDPoPProofShape(t *testing.T) {
	key, _ := newDPoPKey()
	p, err := dpopProof(key, "GET", "https://lw.test/rest/api/3/myself?expand=x#y", "lwo_1")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(p, ".")
	var c map[string]any
	b, _ := base64.RawURLEncoding.DecodeString(parts[1])
	json.Unmarshal(b, &c)
	if c["htu"] != "https://lw.test/rest/api/3/myself" || c["htm"] != "GET" || c["ath"] == nil || c["jti"] == "" {
		t.Errorf("claims %v", c)
	}
	if len(thumbprint(key)) != 43 {
		t.Errorf("thumbprint %q", thumbprint(key))
	}
}

// lockStore is a memStore other "processes" respect a lock on.
type lockStore struct {
	memStore
	lock sync.Mutex
}

func (l *lockStore) Lock() (func(), error) { l.lock.Lock(); return l.lock.Unlock, nil }

// TestSignInSharedByProcesses: the app's processes share one sign-in: one
// uses the access token another got, and a refresh takes the newest
// refresh token, so none is ever sent twice (which ends the sign-in).
func TestSignInSharedByProcesses(t *testing.T) {
	f := newFakeLaneway(t)
	shortPolls(t)
	ctx := context.Background()
	creds, err := DeviceLogin(ctx, f.URL, func(string, string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	st := &lockStore{}
	raw, _ := json.Marshal(creds)
	st.Save(raw)
	tui, web := New(Config{BaseURL: f.URL, SignIn: st}), New(Config{BaseURL: f.URL, SignIn: st})
	if _, err := tui.Myself(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := web.Myself(ctx); err != nil {
		t.Fatal(err)
	}
	if f.refreshs != 1 {
		t.Errorf("refreshes %d: the second process should use the first one's access token", f.refreshs)
	}
	// The access token ends: tui refreshes, then web, each with the newest.
	for i, c := range []*Client{tui, web} {
		var kept Credentials
		json.Unmarshal(st.raw, &kept)
		kept.Expires = time.Now()
		raw, _ := json.Marshal(kept)
		st.Save(raw)
		c.oauth.mu.Lock()
		c.oauth.expires = time.Now()
		c.oauth.mu.Unlock()
		c.mu.Lock()
		c.myself = nil
		c.mu.Unlock()
		if _, err := c.Myself(ctx); err != nil {
			t.Fatalf("process %d after the access token ended: %v", i, err)
		}
	}
	if f.refreshs != 3 {
		t.Errorf("refreshes %d, want 3", f.refreshs)
	}
}

func shortPolls(t *testing.T) {
	old := devicePollMin
	devicePollMin = 10 * time.Millisecond
	t.Cleanup(func() { devicePollMin = old })
}

// signedInClient is a client on a device sign-in to f, and its store.
func signedInClient(t *testing.T, f *fakeLaneway) (*Client, *memStore) {
	t.Helper()
	shortPolls(t)
	creds, err := DeviceLogin(context.Background(), f.URL, func(string, string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	st := &memStore{}
	raw, _ := json.Marshal(creds)
	st.Save(raw)
	return New(Config{BaseURL: f.URL, SignIn: st}), st
}

// A clock minutes off: proofs carry the server's time, from its Date.
func TestDPoPClockSkew(t *testing.T) {
	f := newFakeLaneway(t)
	f.clock = 10 * time.Minute
	c, _ := signedInClient(t, f)
	if _, err := c.Myself(context.Background()); err != nil {
		t.Fatalf("with the server's clock 10 minutes ahead: %v", err)
	}
	f.mu.Lock()
	f.clock = -10 * time.Minute
	f.mu.Unlock()
	c.oauth.expire(c.oauth.access)
	c.mu.Lock()
	c.myself = nil
	c.mu.Unlock()
	if _, err := c.Myself(context.Background()); err != nil {
		t.Fatalf("after the server's clock moved: %v", err)
	}
}

// A proof the server refuses isn't a sign-in that ended.
func TestInvalidProofIsNotSignedOut(t *testing.T) {
	f := newFakeLaneway(t)
	c, st := signedInClient(t, f)
	f.mu.Lock()
	f.badProofs = true
	f.mu.Unlock()
	_, err := c.Myself(context.Background())
	if err == nil || errors.Is(err, ErrSignedOut) {
		t.Fatalf("a refused proof: %v", err)
	}
	var kept Credentials
	json.Unmarshal(st.raw, &kept)
	if kept.Refresh == "" {
		t.Error("the sign-in was dropped")
	}
	f.mu.Lock()
	f.badProofs = false
	f.mu.Unlock()
	c.mu.Lock()
	c.myself = nil
	c.mu.Unlock()
	if _, err := c.Myself(context.Background()); err != nil {
		t.Errorf("once proofs pass again: %v", err)
	}
}

// A stray request to the loopback listener doesn't end the sign-in.
func TestLoopbackLoginIgnoresStrayCallback(t *testing.T) {
	f := newFakeLaneway(t)
	_, err := LoopbackLogin(context.Background(), f.URL, func(u string) error {
		au, _ := url.Parse(u)
		redirect := au.Query().Get("redirect_uri")
		go func() {
			if resp, err := http.Get(redirect + "?code=evil&state=nope"); err == nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusBadRequest {
					t.Errorf("stray callback: %d", resp.StatusCode)
				}
			}
			if resp, err := http.Get(u); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("after a stray callback: %v", err)
	}
}

// Signing out clears the sign-in here also when the server can't be told.
func TestSignOutWhenRevokeFails(t *testing.T) {
	f := newFakeLaneway(t)
	c, st := signedInClient(t, f)
	f.mu.Lock()
	f.noRevoke = true
	f.mu.Unlock()
	err := c.SignOut(context.Background())
	var re RevokeError
	if !errors.As(err, &re) {
		t.Errorf("sign-out with the revoke failing: %v", err)
	}
	if len(st.raw) != 0 {
		t.Errorf("sign-in kept: %s", st.raw)
	}
}
