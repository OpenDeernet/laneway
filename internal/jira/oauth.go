package jira

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Sign-in to laneway-server through OAuth (its built-in client "laneway")
// instead of an API token: 15-minute access tokens and a rotating refresh
// token, all bound to a key only this client holds (DPoP, RFC 9449), so a
// token that leaks is of no use. Jira sites keep email and API token.

// ClientID is the laneway app's client id on every laneway-server.
const ClientID = "laneway"

// Scopes are what the app asks for: what a person does with it.
const Scopes = "read:jira-work write:jira-work read:jira-user manage:jira-project read:servicedesk-request write:servicedesk-request read:confluence-content.all write:confluence-content read:me manage:me offline_access"

// OAuthClient is who the app signs in as: the built-in public client
// (the zero value), or an app an administrator registered with a secret
// (laneway web serving many people, which keeps the secret).
type OAuthClient struct {
	ID, Secret string
}

func (c OAuthClient) id() string {
	if c.ID == "" {
		return ClientID
	}
	return c.ID
}

// form adds the client's credentials to a token or revocation request.
func (c OAuthClient) form(v url.Values) url.Values {
	v.Set("client_id", c.id())
	if c.Secret != "" {
		v.Set("client_secret", c.Secret)
	}
	return v
}

// ErrSignedOut means the sign-in ended (revoked, expired, or the key is
// gone): sign in again with `laneway login`.
var ErrSignedOut = errors.New("jira: signed out of laneway · sign in again with laneway login")

// CredentialStore keeps a site's sign-in (Credentials as JSON) between
// runs: the OS keyring, or a file only its owner reads.
type CredentialStore interface {
	Load() ([]byte, error)
	Save([]byte) error
}

// Credentials are a sign-in: the DPoP key and the refresh token, and the
// access token last issued, which the app's other processes (the TUI,
// laneway web, scripts) use too rather than each refreshing.
type Credentials struct {
	Key     string    `json:"key"`     // the P-256 private key, PKCS#8, base64
	Refresh string    `json:"refresh"` // rotates at each use
	Account string    `json:"account,omitempty"`
	Access  string    `json:"access,omitempty"`
	Expires time.Time `json:"expires,omitzero"`
}

// Locker is a CredentialStore that other processes respect a lock on:
// a refresh token used twice ends the sign-in, so only one process
// refreshes at a time.
type Locker interface {
	Lock() (unlock func(), err error)
}

// oauthSession holds a site's sign-in while the client runs. The access
// token lives only here, in memory.
type oauthSession struct {
	base   string
	client OAuthClient
	store  CredentialStore
	http   *http.Client

	mu      sync.Mutex
	key     *ecdsa.PrivateKey
	refresh string
	account string
	access  string
	expires time.Time
	// turnedAway is the access token the server refused, not to be taken
	// from the store again.
	turnedAway string
}

// newDPoPKey makes a key for a new sign-in.
func newDPoPKey() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }

func (c Credentials) parseKey() (*ecdsa.PrivateKey, error) {
	der, err := base64.StdEncoding.DecodeString(c.Key)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok || ek.Curve != elliptic.P256() {
		return nil, errors.New("not a P-256 key")
	}
	return ek, nil
}

func credentialsOf(key *ecdsa.PrivateKey, refresh, account string) (Credentials, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Key: base64.StdEncoding.EncodeToString(der), Refresh: refresh, Account: account}, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// jwk is key's public half as a JWK; thumbprint is its RFC 7638 hash.
func jwk(key *ecdsa.PrivateKey) map[string]string {
	pad := func(n *big.Int) string { return b64(n.FillBytes(make([]byte, 32))) }
	return map[string]string{"kty": "EC", "crv": "P-256", "x": pad(key.X), "y": pad(key.Y)}
}

func thumbprint(key *ecdsa.PrivateKey) string {
	k := jwk(key)
	// The members in lexical order, no spaces (RFC 7638 3.2).
	h := sha256.Sum256([]byte(`{"crv":"` + k["crv"] + `","kty":"` + k["kty"] + `","x":"` + k["x"] + `","y":"` + k["y"] + `"}`))
	return b64(h[:])
}

// dpopProof is a proof (ES256 JWT) for one request: method, the URL
// without query, and the access token it carries ("" for none).
func dpopProof(key *ecdsa.PrivateKey, method, target, access string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	u.RawQuery, u.Fragment = "", ""
	jti := make([]byte, 16)
	rand.Read(jti)
	claims := map[string]any{"jti": b64(jti), "htm": method, "htu": u.String(), "iat": serverNow(u.Host).Unix()}
	if access != "" {
		h := sha256.Sum256([]byte(access))
		claims["ath"] = b64(h[:])
	}
	hdr, _ := json.Marshal(map[string]any{"typ": "dpop+jwt", "alg": "ES256", "jwk": jwk(key)})
	body, _ := json.Marshal(claims)
	input := b64(hdr) + "." + b64(body)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return "", err
	}
	sig := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + b64(sig), nil
}

// tokenAnswer is /oauth/token's answer.
type tokenAnswer struct {
	Access    string `json:"access_token"`
	Type      string `json:"token_type"`
	ExpiresIn int    `json:"expires_in"`
	Refresh   string `json:"refresh_token"`
	Error     string `json:"error"`
	Desc      string `json:"error_description"`
}

// tokenCall posts form to base's token endpoint with a proof of key.
func tokenCall(ctx context.Context, hc *http.Client, base string, key *ecdsa.PrivateKey, form url.Values) (tokenAnswer, error) {
	return oauthPost(ctx, hc, base+"/oauth/token", key, form)
}

func oauthPost(ctx context.Context, hc *http.Client, target string, key *ecdsa.PrivateKey, form url.Values) (tokenAnswer, error) {
	var a tokenAnswer
	code, raw, err := formPost(ctx, hc, target, key, form)
	if err != nil {
		return a, err
	}
	if len(raw) > 0 && json.Unmarshal(raw, &a) != nil && code == http.StatusOK {
		return a, fmt.Errorf("laneway: unreadable answer from %s", target)
	}
	if code != http.StatusOK && a.Error == "" {
		a.Error = http.StatusText(code)
	}
	return a, nil
}

// formPost posts form to target with a proof of key (nil: none). A proof
// refused while this machine's clock is off goes once more with the
// server's time.
func formPost(ctx context.Context, hc *http.Client, target string, key *ecdsa.PrivateKey, form url.Values) (int, []byte, error) {
	for try := 0; ; try++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		if key != nil {
			p, err := dpopProof(key, http.MethodPost, target, "")
			if err != nil {
				return 0, nil, err
			}
			req.Header.Set("DPoP", p)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return 0, nil, fmt.Errorf("call laneway: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(raw, &e)
		if learnClock(resp) && key != nil && e.Error == "invalid_dpop_proof" && try == 0 {
			continue
		}
		return resp.StatusCode, raw, nil
	}
}

// clockSkew is how far each laneway-server's clock (by host) is ahead of
// this machine's, from the Date of its answers: a proof's iat must be
// within a minute of the server's time.
var clockSkew sync.Map

// learnClock keeps the skew resp's Date shows; it reports whether it
// changed. Under 5 seconds counts as none: Date has whole seconds.
func learnClock(resp *http.Response) bool {
	date, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil || resp.Request == nil {
		return false
	}
	d := time.Until(date)
	if d.Abs() < 5*time.Second {
		d = 0
	}
	old, _ := clockSkew.Swap(resp.Request.URL.Host, d)
	was, _ := old.(time.Duration)
	return was != d
}

// serverNow is the time on host's clock as far as it is known.
func serverNow(host string) time.Time {
	d, _ := clockSkew.Load(host)
	skew, _ := d.(time.Duration)
	return time.Now().Add(skew)
}

// token is a valid access token, refreshed when it ends within a minute,
// and the key to prove it with.
func (o *oauthSession) token(ctx context.Context) (string, *ecdsa.PrivateKey, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.access != "" && time.Until(o.expires) > time.Minute {
		return o.access, o.key, nil
	}
	if l, ok := o.store.(Locker); ok {
		unlock, err := l.Lock()
		if err != nil {
			return "", nil, err
		}
		defer unlock()
	}
	// What another process kept meanwhile: its refresh token is the one
	// to use, its access token may do.
	if err := o.load(); err != nil {
		return "", nil, err
	}
	if o.access != "" && time.Until(o.expires) > time.Minute {
		return o.access, o.key, nil
	}
	a, err := tokenCall(ctx, o.http, o.base, o.key, o.client.form(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {o.refresh}}))
	if err != nil {
		return "", nil, err
	}
	switch {
	case a.Error == "invalid_grant":
		o.access, o.refresh = "", ""
		return "", nil, ErrSignedOut
	case a.Error == "invalid_dpop_proof":
		// Any proof it refuses, also one from a clock far off: the sign-in
		// may well hold.
		return "", nil, fmt.Errorf("laneway: the server refused this app's proof of its key (%s); check this machine's clock", strings.TrimSpace(a.Desc))
	case a.Error != "":
		return "", nil, fmt.Errorf("laneway: refreshing the sign-in: %s %s", a.Error, a.Desc)
	case a.Type != "DPoP" || a.Refresh == "":
		return "", nil, errors.New("laneway: the server didn't bind the sign-in to this app's key")
	}
	// The new refresh token first: the old one now revokes the grant.
	creds, err := credentialsOf(o.key, a.Refresh, o.account)
	if err != nil {
		return "", nil, err
	}
	creds.Access, creds.Expires = a.Access, time.Now().Add(time.Duration(a.ExpiresIn)*time.Second)
	raw, _ := json.Marshal(creds)
	if err := o.store.Save(raw); err != nil {
		return "", nil, fmt.Errorf("laneway: keeping the sign-in: %w", err)
	}
	o.refresh, o.access, o.expires = a.Refresh, creds.Access, creds.Expires
	return o.access, o.key, nil
}

// load reads the stored sign-in.
func (o *oauthSession) load() error {
	raw, err := o.store.Load()
	if err != nil || len(raw) == 0 {
		return ErrSignedOut
	}
	var c Credentials
	if json.Unmarshal(raw, &c) != nil || c.Refresh == "" {
		return ErrSignedOut
	}
	k, err := c.parseKey()
	if err != nil {
		return ErrSignedOut
	}
	o.key, o.refresh, o.account = k, c.Refresh, c.Account
	if c.Access != o.access && c.Access != o.turnedAway {
		o.access, o.expires = c.Access, c.Expires
	}
	return nil
}

// expire drops the access token the server turned away, so the next
// request refreshes.
func (o *oauthSession) expire(access string) {
	o.mu.Lock()
	if o.access == access {
		o.access, o.turnedAway = "", access
	}
	o.mu.Unlock()
}

// authorize sets req's credentials: Basic for Jira, DPoP with a fresh
// proof for a laneway sign-in. It returns the access token sent ("" for
// Basic).
func (c *Client) authorize(req *http.Request) (string, error) {
	if c.atl != nil {
		return c.atl.authorize(req)
	}
	if c.oauth == nil {
		req.Header.Set("Authorization", c.auth)
		return "", nil
	}
	tok, key, err := c.oauth.token(req.Context())
	if err != nil {
		return "", err
	}
	p, err := dpopProof(key, req.Method, req.URL.String(), tok)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "DPoP "+tok)
	req.Header.Set("DPoP", p)
	return tok, nil
}

// apiURL is where the request for path (/rest/…, /wiki/…) goes: the
// site, or Atlassian's API for it.
func (c *Client) apiURL(path string) string {
	if c.atl != nil {
		return c.atl.apiURL(path)
	}
	return c.baseURL + path
}

// expire drops the access token the server turned away.
func (c *Client) expire(access string) {
	if c.atl != nil {
		c.atl.expire(access)
	} else if c.oauth != nil {
		c.oauth.expire(access)
	}
}

// MakesTokens says whether this sign-in can make API tokens: an app's
// (OAuth) can't, the server's own page can (TokenPage).
func (c *Client) MakesTokens() bool { return c.oauth == nil }

// SignedInAs is the account a laneway sign-in is for ("" for API-token
// sites).
func (c *Client) SignedInAs() string {
	if c.oauth == nil {
		return ""
	}
	c.oauth.mu.Lock()
	defer c.oauth.mu.Unlock()
	if c.oauth.key == nil {
		c.oauth.load()
	}
	return c.oauth.account
}
