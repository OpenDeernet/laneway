package jira

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Signing in to a laneway-server: in the browser, which comes back to a
// loopback address (RFC 8252), or with a code typed on another device
// (RFC 8628) when there is no browser here. Either way the tokens are
// bound to a key made for this sign-in.

// Login is a browser sign-in under way: send the person to AuthURL, then
// Finish with what the browser brought back to Redirect.
type Login struct {
	AuthURL  string
	Redirect string
	base     string
	state    string
	verifier string
	key      *ecdsa.PrivateKey
}

// BeginLogin starts a browser sign-in to base that comes back to redirect
// (a loopback address).
func BeginLogin(base, redirect string) (*Login, error) {
	key, err := newDPoPKey()
	if err != nil {
		return nil, err
	}
	rnd := func() string {
		b := make([]byte, 32)
		rand.Read(b)
		return b64(b)
	}
	l := &Login{Redirect: redirect, base: strings.TrimRight(base, "/"), state: rnd(), verifier: rnd(), key: key}
	sum := sha256.Sum256([]byte(l.verifier))
	q := url.Values{"client_id": {ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {Scopes}, "state": {l.state},
		"code_challenge": {b64(sum[:])}, "code_challenge_method": {"S256"}, "dpop_jkt": {thumbprint(key)}}
	l.AuthURL = l.base + "/oauth/authorize?" + q.Encode()
	return l, nil
}

// Finish swaps what came back to Redirect (its query) for a sign-in.
func (l *Login) Finish(ctx context.Context, back url.Values) (Credentials, error) {
	if subtle.ConstantTimeCompare([]byte(back.Get("state")), []byte(l.state)) != 1 {
		return Credentials{}, errors.New("laneway: the sign-in didn't come back as it left; try again")
	}
	if e := back.Get("error"); e != "" {
		return Credentials{}, fmt.Errorf("laneway: sign-in refused: %s", strings.TrimSpace(e+" "+back.Get("error_description")))
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	a, err := tokenCall(ctx, hc, l.base, l.key, url.Values{"grant_type": {"authorization_code"}, "client_id": {ClientID}, "code": {back.Get("code")},
		"redirect_uri": {l.Redirect}, "code_verifier": {l.verifier}})
	if err != nil {
		return Credentials{}, err
	}
	return signedIn(ctx, hc, l.base, l.key, a)
}

// signedIn checks a token answer and makes the sign-in of it, with the
// account it is for.
func signedIn(ctx context.Context, hc *http.Client, base string, key *ecdsa.PrivateKey, a tokenAnswer) (Credentials, error) {
	if a.Error != "" {
		return Credentials{}, fmt.Errorf("laneway: sign-in refused: %s", strings.TrimSpace(a.Error+" "+a.Desc))
	}
	if a.Type != "DPoP" || a.Refresh == "" {
		return Credentials{}, errors.New("laneway: the server didn't bind the sign-in to this app's key")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/rest/api/3/myself", nil)
	p, err := dpopProof(key, http.MethodGet, req.URL.String(), a.Access)
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Authorization", "DPoP "+a.Access)
	req.Header.Set("DPoP", p)
	var me struct {
		Email string `json:"emailAddress"`
	}
	if resp, err := hc.Do(req); err == nil {
		json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me)
		resp.Body.Close()
	}
	return credentialsOf(key, a.Refresh, me.Email)
}

// LoopbackLogin signs in to base through the browser: open shows the
// person the sign-in page; it comes back to a listener on 127.0.0.1.
func LoopbackLogin(ctx context.Context, base string, open func(url string) error) (Credentials, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Credentials{}, err
	}
	defer ln.Close()
	l, err := BeginLogin(base, fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port))
	if err != nil {
		return Credentials{}, err
	}
	got := make(chan url.Values, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		msg := "Signed in to laneway. You can close this tab."
		if e := r.URL.Query().Get("error"); e != "" {
			msg = "The sign-in didn't work: " + e
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>laneway</title><p>%s</p>", html.EscapeString(msg))
		select {
		case got <- r.URL.Query():
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()
	if err := open(l.AuthURL); err != nil {
		return Credentials{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	select {
	case back := <-got:
		return l.Finish(ctx, back)
	case <-ctx.Done():
		return Credentials{}, errors.New("laneway: no sign-in came back within 5 minutes")
	}
}

// devicePollMin is the least wait between polls (RFC 8628's default);
// tests shorten it.
var devicePollMin = 5 * time.Second

// DeviceLogin signs in to base with a code the person types at the
// server's /device page on any device; show tells them the code and where.
func DeviceLogin(ctx context.Context, base string, show func(code, uri, complete string)) (Credentials, error) {
	base = strings.TrimRight(base, "/")
	key, err := newDPoPKey()
	if err != nil {
		return Credentials{}, err
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/oauth/device/code",
		strings.NewReader(url.Values{"client_id": {ClientID}, "scope": {Scopes}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	p, err := dpopProof(key, http.MethodPost, base+"/oauth/device/code", "")
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("DPoP", p)
	resp, err := hc.Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("call laneway: %w", err)
	}
	var dev struct {
		Device   string `json:"device_code"`
		User     string `json:"user_code"`
		URI      string `json:"verification_uri"`
		Complete string `json:"verification_uri_complete"`
		Expires  int    `json:"expires_in"`
		Interval int    `json:"interval"`
		Error    string `json:"error"`
		Desc     string `json:"error_description"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&dev)
	resp.Body.Close()
	if dev.Device == "" {
		return Credentials{}, fmt.Errorf("laneway: no device sign-in: %s", strings.TrimSpace(dev.Error+" "+dev.Desc))
	}
	show(dev.User, dev.URI, dev.Complete)
	wait := max(time.Duration(dev.Interval)*time.Second, devicePollMin)
	end := time.Now().Add(time.Duration(dev.Expires) * time.Second)
	for time.Now().Before(end) {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return Credentials{}, ctx.Err()
		case <-t.C:
		}
		a, err := tokenCall(ctx, hc, base, key, url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {ClientID},
			"device_code": {dev.Device}})
		if err != nil {
			return Credentials{}, err
		}
		switch a.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			wait += 5 * time.Second
			continue
		}
		return signedIn(ctx, hc, base, key, a)
	}
	return Credentials{}, errors.New("laneway: the code expired; sign in again")
}

// SignOut ends the client's laneway sign-in: the server revokes it and
// the stored one is cleared. Not signed in: nothing to do.
func (c *Client) SignOut(ctx context.Context) error {
	if c.oauth == nil {
		return nil
	}
	o := c.oauth
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.key == nil {
		o.load()
	}
	if o.refresh != "" {
		a, err := oauthPost(ctx, o.http, o.base+"/oauth/revoke", nil, url.Values{"client_id": {ClientID}, "token": {o.refresh}})
		if err != nil {
			return err
		}
		if a.Error != "" {
			return fmt.Errorf("laneway: signing out: %s %s", a.Error, a.Desc)
		}
	}
	o.key, o.refresh, o.access, o.account = nil, "", "", ""
	return o.store.Save(nil)
}

// ProbeLaneway reports whether base is a laneway-server that signs the
// laneway app in: its OAuth metadata (RFC 8414) names it as issuer and
// offers the device grant. Jira answers 404 there.
func ProbeLaneway(ctx context.Context, base string) bool {
	base = strings.TrimRight(base, "/")
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var md struct {
		Issuer string   `json:"issuer"`
		Grants []string `json:"grant_types_supported"`
		DPoP   []string `json:"dpop_signing_alg_values_supported"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&md) != nil {
		return false
	}
	return strings.TrimRight(md.Issuer, "/") == base && len(md.DPoP) > 0
}
