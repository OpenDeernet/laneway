package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/offline"
	"github.com/cornedor/laneway/internal/store"
	"github.com/cornedor/laneway/internal/ui"
	"github.com/cornedor/laneway/internal/web"
	yaml "gopkg.in/yaml.v3"
)

// laneway web -hosted: the browser app for many people, next to a
// laneway-server. It is a backend for the frontend: each person signs in
// through laneway-server's OAuth (this server is an app an administrator
// registered there, with a secret), their tokens and DPoP key stay here,
// and the browser only gets an HttpOnly session cookie. Each person gets
// the app of their own, on their own directory (config, state, sign-in),
// with what would act as this machine's user switched off (hostedOptions).

const (
	hostedCookie      = "laneway_session"
	hostedIdle        = 12 * time.Hour
	hostedMax         = 7 * 24 * time.Hour
	hostedLoginTTL    = 10 * time.Minute
	hostedAppIdle     = 30 * time.Minute
	hostedSessionFile = "sessions.json"
)

func hostedCmd(args []string, errOut io.Writer) int {
	fs := flag.NewFlagSet("web -hosted", flag.ContinueOnError)
	fs.SetOutput(errOut)
	addr := fs.String("addr", "127.0.0.1:8484", i18n.T("address to listen on"))
	server := fs.String("server", "", i18n.T("the laneway-server people sign in to (its URL)"))
	public := fs.String("url", "", i18n.T("this server's own address as browsers reach it (https://…); its /auth/callback is the app's redirect URI"))
	clientID := fs.String("client-id", "", i18n.T("the app's client id, registered in laneway-server (OAuth apps)"))
	data := fs.String("data", "", i18n.T("directory for each person's config, state and sign-in"))
	cert := fs.String("cert", "", i18n.T("TLS certificate file (PEM); serves https"))
	key := fs.String("key", "", i18n.T("TLS private key file (PEM) for -cert"))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	h, err := newHosted(*server, *public, *data, jira.OAuthClient{ID: *clientID, Secret: os.Getenv("LANEWAY_WEB_CLIENT_SECRET")})
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	h.ctx = ctx
	go h.sweep(ctx)
	// Behind a TLS proxy it listens on loopback; without one it needs TLS itself.
	err = web.Serve(ctx, *addr, true, *cert, *key, h, func(net.Addr) {
		fmt.Fprintln(os.Stderr, i18n.Tf("laneway web for %s on %s", h.server, h.public))
	})
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	return 0
}

// hosted is the gateway: sign-in, sessions, and each person's app.
type hosted struct {
	ctx            context.Context
	server, public string
	host           string // public's host, the only Host accepted
	secure         bool
	client         jira.OAuthClient
	data           string

	mu       sync.Mutex
	sessions map[string]*hostedSession // by the hash of the cookie
	logins   map[string]*hostedLogin   // by state
	apps     map[string]*hostedApp     // by account id
}

type hostedSession struct {
	Account  string    `json:"account"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
}

type hostedLogin struct {
	login *jira.Login
	next  string
	at    time.Time
}

type hostedApp struct {
	srv  *web.Server
	stop context.CancelFunc
	last time.Time
}

func newHosted(server, public, data string, client jira.OAuthClient) (*hosted, error) {
	server, public = strings.TrimRight(strings.TrimSpace(server), "/"), strings.TrimRight(strings.TrimSpace(public), "/")
	if server == "" || public == "" || data == "" || client.ID == "" {
		return nil, errors.New(i18n.T("-hosted needs -server, -url, -data and -client-id, and LANEWAY_WEB_CLIENT_SECRET in the environment"))
	}
	if client.Secret == "" {
		return nil, errors.New(i18n.T("LANEWAY_WEB_CLIENT_SECRET is not set: the app's secret from laneway-server"))
	}
	pu, err := url.Parse(public)
	if err != nil || pu.Host == "" || (pu.Scheme != "https" && pu.Scheme != "http") {
		return nil, fmt.Errorf(i18n.T("-url %q is not an address like https://laneway.example.com"), public)
	}
	if err := os.MkdirAll(filepath.Join(data, "people"), 0o700); err != nil {
		return nil, err
	}
	h := &hosted{server: server, public: public, host: pu.Host, secure: pu.Scheme == "https", client: client, data: data,
		sessions: map[string]*hostedSession{}, logins: map[string]*hostedLogin{}, apps: map[string]*hostedApp{}}
	if raw, err := os.ReadFile(filepath.Join(data, hostedSessionFile)); err == nil {
		json.Unmarshal(raw, &h.sessions)
	}
	return h, nil
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomString() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// saveSessions keeps the sessions (hashed) for a restart; under mu.
func (h *hosted) saveSessions() {
	raw, _ := json.Marshal(h.sessions)
	p := filepath.Join(h.data, hostedSessionFile)
	if os.WriteFile(p+".tmp", raw, 0o600) == nil {
		os.Rename(p+".tmp", p)
	}
}

// accountDir is a person's directory; account ids are laneway-server's.
var accountID = regexp.MustCompile(`^[A-Za-z0-9_:-]{1,128}$`)

func (h *hosted) accountDir(account string) string { return filepath.Join(h.data, "people", account) }

// session is the signed-in account of r, "" for none; it is touched.
func (h *hosted) session(r *http.Request) string {
	c, err := r.Cookie(hostedCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	id := hashSecret(c.Value)
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	if s == nil {
		return ""
	}
	if now.Sub(s.LastSeen) > hostedIdle || now.Sub(s.Created) > hostedMax {
		delete(h.sessions, id)
		h.saveSessions()
		return ""
	}
	if now.Sub(s.LastSeen) > 5*time.Minute {
		s.LastSeen = now
		h.saveSessions()
	}
	return s.Account
}

func (h *hosted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != h.host {
		http.Error(w, i18n.T("unknown host refused"), http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/auth/callback":
		h.callback(w, r)
		return
	case "/auth/logout":
		h.logout(w, r)
		return
	case "/auth/login":
		h.startLogin(w, r, r.URL.Query().Get("next"))
		return
	case "/sw.js":
		// No service worker for many people: it would serve pages from
		// the cache, past the sign-in. This one replaces one a browser
		// still has (from laneway web on the same address, or before).
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		io.WriteString(w, hostedNoWorker)
		return
	}
	account := h.session(r)
	if account == "" {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// The page may come from the browser's cache: it signs in
			// at signedOut (api.js).
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": i18n.T("signed out; reload the page to sign in"), "signedOut": "/auth/login"})
			return
		}
		h.startLogin(w, r, r.URL.RequestURI())
		return
	}
	app, err := h.app(account)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.srv.ServeHTTP(w, r)
}

// startLogin sends the browser to laneway-server's sign-in, to come back
// to /auth/callback and then to next (a path here).
func (h *hosted) startLogin(w http.ResponseWriter, r *http.Request, next string) {
	if r.Method != http.MethodGet {
		http.Error(w, i18n.T("signed out; reload the page to sign in"), http.StatusUnauthorized)
		return
	}
	l, err := jira.BeginLoginAs(h.client, h.server, h.public+"/auth/callback")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	state, _ := url.Parse(l.AuthURL)
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/auth/") || strings.ContainsAny(next, "\\\r\n\t") {
		next = "/"
	}
	h.mu.Lock()
	for k, p := range h.logins {
		if time.Since(p.at) > hostedLoginTTL {
			delete(h.logins, k)
		}
	}
	h.logins[state.Query().Get("state")] = &hostedLogin{login: l, next: next, at: time.Now()}
	h.mu.Unlock()
	http.Redirect(w, r, l.AuthURL, http.StatusFound)
}

// callback finishes a sign-in: the code swapped for tokens bound to a key
// made for this person, kept in their directory, and a session.
func (h *hosted) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	h.mu.Lock()
	p := h.logins[state]
	delete(h.logins, state)
	h.mu.Unlock()
	if p == nil || time.Since(p.at) > hostedLoginTTL {
		http.Error(w, i18n.T("the sign-in took too long or didn't start here; open the page again"), http.StatusBadRequest)
		return
	}
	creds, err := p.login.Finish(r.Context(), r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	// The account id names the directory: an email can change.
	tmp := filepath.Join(h.data, "people", ".signin-"+randomString()[:16]+".json")
	raw, _ := json.Marshal(creds)
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp)
	jc := config.JiraConfig{BaseURL: h.server, SignIn: config.SignInLaneway, CredentialsFile: tmp}
	me, err := jira.New(jira.Config{BaseURL: h.server, SignIn: jc.Credentials(), OAuthClient: h.client}).Myself(r.Context())
	if err != nil || !accountID.MatchString(me.AccountID) {
		http.Error(w, i18n.T("laneway-server didn't say who signed in"), http.StatusBadGateway)
		return
	}
	dir := h.accountDir(me.AccountID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The latest, rotated sign-in from the check above, in its place.
	if raw, err := os.ReadFile(tmp); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "sign-in.json"), raw, 0o600); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); errors.Is(err, os.ErrNotExist) {
		os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("# laneway web keeps your settings here\nui: {}\n"), 0o600)
	}
	secret := randomString()
	now := time.Now()
	h.mu.Lock()
	h.sessions[hashSecret(secret)] = &hostedSession{Account: me.AccountID, Created: now, LastSeen: now}
	h.saveSessions()
	if a := h.apps[me.AccountID]; a != nil { // a new sign-in: start afresh
		a.stop()
		delete(h.apps, me.AccountID)
	}
	h.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: hostedCookie, Value: secret, Path: "/", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode,
		Expires: now.Add(hostedMax)})
	http.Redirect(w, r, p.next, http.StatusSeeOther)
}

// logout ends the session and the person's sign-in on laneway-server.
func (h *hosted) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.public {
		http.Error(w, i18n.T("cross-origin request refused"), http.StatusForbidden)
		return
	}
	account := h.session(r)
	if c, err := r.Cookie(hostedCookie); err == nil {
		h.mu.Lock()
		delete(h.sessions, hashSecret(c.Value))
		h.saveSessions()
		h.mu.Unlock()
	}
	if account != "" {
		h.mu.Lock()
		a := h.apps[account]
		delete(h.apps, account)
		h.mu.Unlock()
		if a != nil {
			a.srv.Client().SignOut(r.Context())
			a.stop()
		}
	}
	http.SetCookie(w, &http.Cookie{Name: hostedCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// app is the person's app, made on first use.
func (h *hosted) app(account string) (*hostedApp, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.apps[account]; a != nil {
		a.last = time.Now()
		return a, nil
	}
	opt, err := h.options(account)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(h.ctx)
	a := &hostedApp{srv: web.New(ctx, opt), stop: cancel, last: time.Now()}
	h.apps[account] = a
	return a, nil
}

// options are the person's app: their directory, laneway-server as the
// one site, and nothing that acts as this machine's user.
func (h *hosted) options(account string) (web.Options, error) {
	dir := h.accountDir(account)
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		return web.Options{}, err
	}
	jc := config.JiraConfig{BaseURL: h.server, SignIn: config.SignInLaneway, CredentialsFile: filepath.Join(dir, "sign-in.json")}
	opt := web.Options{Version: version, Jira: jc, UI: hostedUI(cfg.UI), ConfigPath: cfgPath, AllowedHosts: []string{h.host}, Hosted: true}
	if opt.Store, err = store.Open(filepath.Join(dir, "state.json")); err != nil {
		return opt, err
	}
	jcfg := ui.JiraConfig(jc, opt.UI)
	jcfg.OAuthClient = h.client
	opt.Client = jira.New(jcfg)
	opt.Client.SetQueue(offline.To(opt.Store))
	opt.Client.SetLayouts(opt.Store)
	opt.Restart = func() {
		h.mu.Lock()
		if a := h.apps[account]; a != nil {
			a.stop()
			delete(h.apps, account)
		}
		h.mu.Unlock()
	}
	return opt, nil
}

// sweep stops apps idle for a while and drops ended sessions.
func (h *hosted) sweep(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			h.mu.Lock()
			for k, a := range h.apps {
				if now.Sub(a.last) > hostedAppIdle {
					a.stop()
					delete(h.apps, k)
				}
			}
			changed := false
			for k, s := range h.sessions {
				if now.Sub(s.LastSeen) > hostedIdle || now.Sub(s.Created) > hostedMax {
					delete(h.sessions, k)
					changed = true
				}
			}
			if changed {
				h.saveSessions()
			}
			h.mu.Unlock()
		}
	}
}

// hostedNoWorker takes the place of a service worker: it clears laneway's
// caches, unregisters and reloads the pages it held.
const hostedNoWorker = `self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', e => e.waitUntil((async () => {
  for (const k of await caches.keys()) if (k.startsWith('lw-')) await caches.delete(k);
  await self.registration.unregister();
  for (const c of await self.clients.matchAll({ type: 'window' })) c.navigate(c.url);
})()));
`

// hostedUI is a person's ui: without what acts on this machine: the
// options that run commands, and those that read its files or fetch
// hosts of their choosing (web.HostedOffSettings).
func hostedUI(u config.UIConfig) config.UIConfig {
	raw, err := yaml.Marshal(u)
	if err != nil {
		return config.UIConfig{}
	}
	var m map[string]any
	if yaml.Unmarshal(raw, &m) != nil {
		return config.UIConfig{}
	}
	for k := range m {
		if config.SettingsCommand[k] || web.HostedOffSettings[k] {
			delete(m, k)
		}
	}
	raw, _ = yaml.Marshal(m)
	var out config.UIConfig
	if yaml.Unmarshal(raw, &out) != nil {
		return config.UIConfig{}
	}
	return out
}
