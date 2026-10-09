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
const Scopes = "read:jira-work write:jira-work read:jira-user manage:jira-project read:servicedesk-request write:servicedesk-request read:me offline_access"

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
	base  string
	store CredentialStore
	http  *http.Client

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
	claims := map[string]any{"jti": b64(jti), "htm": method, "htu": u.String(), "iat": time.Now().Unix()}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return a, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if key != nil {
		p, err := dpopProof(key, http.MethodPost, target, "")
		if err != nil {
			return a, err
		}
		req.Header.Set("DPoP", p)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return a, fmt.Errorf("call laneway: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if len(raw) > 0 && json.Unmarshal(raw, &a) != nil && resp.StatusCode == http.StatusOK {
		return a, fmt.Errorf("laneway: unreadable answer from %s", target)
	}
	if resp.StatusCode != http.StatusOK && a.Error == "" {
		a.Error = http.StatusText(resp.StatusCode)
	}
	return a, nil
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
	a, err := tokenCall(ctx, o.http, o.base, o.key, url.Values{"grant_type": {"refresh_token"}, "client_id": {ClientID}, "refresh_token": {o.refresh}})
	if err != nil {
		return "", nil, err
	}
	switch {
	case a.Error == "invalid_grant" || a.Error == "invalid_dpop_proof":
		o.access, o.refresh = "", ""
		return "", nil, ErrSignedOut
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
