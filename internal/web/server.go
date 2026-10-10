// Package web serves laneway in a browser: a JSON API over internal/jira and
// an embedded, build-free frontend (static/).
//
// Every API file registers its routes from init() with handle/get/post, so
// feature areas live in their own api_*.go files.
package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/forge/gitlab"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/rules"
	"github.com/cornedor/laneway/internal/store"
)

//go:embed static
var staticFS embed.FS

// Options configure a Server.
type Options struct {
	Client *jira.Client
	Jira   config.JiraConfig
	UI     config.UIConfig
	// ConfigPath is the config file the settings write to; empty: read-only.
	ConfigPath string
	Store      *store.Store
	Site       string
	Sites      []string
	// DefaultName labels the default site (jira: name:) in the header.
	DefaultName string
	Demo        bool
	Version     string
	Rules       []rules.Rule
	RulesTest   config.RulesTest
	// Open builds the options of another site (nil: no switching).
	Open func(site string) (Options, error)
	// AllowedHosts are extra Host headers accepted besides loopback names: a
	// "host:port" matches exactly, a bare "host" on any port. Needed for -remote.
	AllowedHosts []string
	// Token, when set, must be exchanged (?token=) for a cookie before any
	// other request is served. Set with -remote.
	Token string
	// Setup, when set, makes this a first-start server: only the setup
	// screen, no Jira yet.
	Setup *Setup
	// Autostart, when set, offers starting laneway web at login.
	Autostart *Autostart
	// AddSite, when set, lets the site picker add a Jira site.
	AddSite *SiteAdder
	// Restart, when set, serves the app again with the config read anew.
	Restart func()
	// UpgradeCmd updates this binary ("" when unknown: the release page).
	UpgradeCmd string
	// Hosted is laneway web for many people (hosted.go): only the routes
	// that act through the person's own Jira client serve.
	Hosted bool
	// SignedOut, when set, is told the site's sign-in ended (hosted: the
	// gateway drops the person's sessions); the browser is sent to sign in.
	SignedOut func()
	// GitLab are the GitLab instances (nil: none, as in the demo), and
	// GitLabRepos their projects' checkouts by path (gitlab: repos:).
	GitLab      *gitlab.Sites
	GitLabRepos map[string]string
}

// Server is the HTTP handler: API under /api, assets everywhere else.
type Server struct {
	opt   Options
	mux   *http.ServeMux
	ctx   context.Context
	sites *siteSet
}

// Client is the Jira client; handlers take it from the request's Server.
func (s *Server) Client() *jira.Client { return s.opt.Client }

// Options returns the server's options.
func (s *Server) Options() Options { return s.opt }

type route struct {
	pattern string
	h       func(s *Server, w http.ResponseWriter, r *http.Request)
}

var (
	routesMu sync.Mutex
	routes   []route
)

// handle registers a raw handler under a Go 1.22 mux pattern ("GET /api/x/{id}").
func handle(pattern string, h func(s *Server, w http.ResponseWriter, r *http.Request)) {
	routesMu.Lock()
	routes = append(routes, route{pattern, h})
	routesMu.Unlock()
}

// Handler is a JSON endpoint: the result is encoded, an error becomes a
// JSON {error} with a fitting status.
type Handler func(ctx context.Context, s *Server, r *http.Request) (any, error)

// get registers a GET endpoint at /api+path.
func get(path string, h Handler) { api("GET "+path, h) }

// post registers a POST endpoint at /api+path; the body is read with Body.
func post(path string, h Handler) { api("POST "+path, h) }

// put, del: likewise.
func put(path string, h Handler) { api("PUT "+path, h) }
func del(path string, h Handler) { api("DELETE "+path, h) }

func api(pattern string, h Handler) {
	method, path, _ := strings.Cut(pattern, " ")
	handle(method+" /api"+path, func(s *Server, w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		v, err := h(ctx, s, r)
		if errors.Is(err, jira.ErrSignedOut) && s.opt.SignedOut != nil {
			s.opt.SignedOut()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": i18n.T("signed out; reload the page to sign in"), "signedOut": "/auth/login"})
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, r, v)
	})
}

// Body decodes the request's JSON body into T.
func Body[T any](r *http.Request) (T, error) {
	var v T
	err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&v)
	if err != nil && !errors.Is(err, io.EOF) {
		return v, badRequest(i18n.Tf("bad JSON: %s", err.Error()))
	}
	return v, nil
}

// Q returns a query parameter.
func Q(r *http.Request, name string) string { return r.URL.Query().Get(name) }

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func badRequest(msg string) error { return httpError{http.StatusBadRequest, msg} }

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	var he httpError
	var re *jira.RequestError
	var fe FieldError
	var se signInErr
	switch {
	case errors.As(err, &se):
		code = se.code
	case errors.As(err, &fe):
		code = http.StatusBadRequest
	case errors.As(err, &he):
		code = he.code
	case errors.Is(err, jira.ErrQueued):
		code = http.StatusAccepted // kept for later: the browser counts it as sent
	case errors.Is(err, jira.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, jira.ErrDocChanged):
		code = http.StatusConflict
	case jira.TimedOut(err): // it may have landed: a comment posted again checks first (lib/comment.js)
		code = http.StatusGatewayTimeout
	case errors.As(err, &re):
		code = http.StatusBadGateway
	}
	body := map[string]any{"error": err.Error()}
	if re != nil && len(re.Fields) > 0 {
		body["fields"] = re.Fields // Jira's reasons by field id, for the form to show under each
	}
	if fe.Field != "" {
		body["fields"] = map[string]string{fe.Field: fe.Msg}
	}
	if se.Host != "" {
		body["signin"] = se.SignIn // how to sign in to GitLab there
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// writeJSON encodes v, gzipped when the client takes it and the body is big.
func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	if v == nil {
		v = map[string]bool{"ok": true}
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	emptySlices(&buf)
	if b := rewriteAvatars(buf.Bytes()); len(b) != buf.Len() || !bytes.Equal(b, buf.Bytes()) {
		buf.Reset()
		buf.Write(b)
	}
	if buf.Len() > 1024 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write(buf.Bytes())
		_ = gz.Close()
		return
	}
	_, _ = w.Write(buf.Bytes())
}

// sliceKeys are response fields that are Go slices: a nil one encodes as null,
// which the UI would trip over. Keep in step with lib/api.js.
var sliceKeys = []string{
	"Links", "Attachments", "Comments", "Labels", "Watches", "Watchers", "Transitions", "Worklogs",
	"Sprints", "Issues", "Columns", "Statuses", "Subtasks", "Kids", "Mentions", "Threads", "Histories",
	"FixVersions", "Components", "Warnings", "Options", "AllowedValues", "Entries", "Results", "Changes",
	"Activity", "Agents", "Views", "Groups", "Filters", "QuickFilters", "Branches", "Commits",
	"PullRequests", "Builds", "Deployments", "BlockedBy", "References", "Choices", "Moves", "Items", "Rules", "Replies",
}

var nullKeys = func() [][2][]byte {
	out := make([][2][]byte, len(sliceKeys))
	for i, k := range sliceKeys {
		out[i] = [2][]byte{[]byte(`"` + k + `":null`), []byte(`"` + k + `":[]`)}
	}
	return out
}()

// emptySlices rewrites null to [] for the known slice fields. One scan for
// ":null" first, so bodies without nulls cost almost nothing.
func emptySlices(buf *bytes.Buffer) {
	b := buf.Bytes()
	if !bytes.Contains(b, []byte(":null")) {
		return
	}
	for _, kv := range nullKeys {
		if bytes.Contains(b, kv[0]) {
			b = bytes.ReplaceAll(b, kv[0], kv[1])
		}
	}
	buf.Reset()
	buf.Write(b)
}

// New builds the handler.
func New(ctx context.Context, opt Options) *Server {
	s := &Server{opt: opt, mux: http.NewServeMux(), ctx: ctx, sites: newSiteSet(opt)}
	routesMu.Lock()
	for _, rt := range routes {
		h, pattern := rt.h, rt.pattern
		s.mux.HandleFunc(rt.pattern, func(w http.ResponseWriter, r *http.Request) {
			if ss := s.at(r); !hostedGate(ss, pattern, w) && !setupOnly(ss, w, r) && !demoGate(ss, w, r) {
				h(ss, w, r)
			}
		})
	}
	routesMu.Unlock()
	if opt.Setup == nil {
		s.sites.rulesOf(ctx, opt) // starts the rule watches
	}
	sub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("/", assets(sub))
	return s
}

// hostAllowed: Host must be a loopback name or one the server was told about,
// which keeps a DNS-rebound page from talking to the API.
func (s *Server) hostAllowed(host string) bool {
	h := strings.ToLower(host)
	name := h
	if n, _, err := net.SplitHostPort(h); err == nil {
		name = n
	}
	name = strings.Trim(name, "[]")
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return true
	}
	if ip := net.ParseIP(name); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, a := range s.opt.AllowedHosts {
		a = strings.ToLower(a)
		if _, _, err := net.SplitHostPort(a); a == h || err != nil && strings.Trim(a, "[]") == name {
			return true
		}
	}
	return false
}

const tokenCookie = "laneway_token"

// tokenOK exchanges a launch token in the URL for a cookie (and redirects to
// the clean URL), or accepts the cookie. It reports whether to go on.
func (s *Server) tokenOK(w http.ResponseWriter, r *http.Request) bool {
	want := []byte(s.opt.Token)
	if t := r.URL.Query().Get("token"); t != "" && subtle.ConstantTimeCompare([]byte(t), want) == 1 {
		http.SetCookie(w, &http.Cookie{Name: tokenCookie, Value: s.opt.Token, Path: "/", HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode})
		q := r.URL.Query()
		q.Del("token")
		u := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
		http.Redirect(w, r, u.String(), http.StatusSeeOther)
		return false
	}
	if c, err := r.Cookie(tokenCookie); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), want) == 1 {
		return true
	}
	http.Error(w, i18n.T("open the URL with ?token= that laneway printed at start"), http.StatusUnauthorized)
	return false
}

// secureRequest is whether cookies may carry Secure: over TLS (directly or
// by a proxy), or to a loopback host, which browsers treat as secure. Only
// plain http to a remote host gets none, where the browser would drop it.
func secureRequest(r *http.Request) bool {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	h := r.Host
	if name, _, err := net.SplitHostPort(h); err == nil {
		h = name
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ServeHTTP guards against other sites driving the API from the browser: the
// Host must be ours (DNS rebinding), a state-changing request must come from
// this origin, and with a Token every request needs its cookie.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r.Host) {
		http.Error(w, i18n.T("unknown host refused"), http.StatusForbidden)
		return
	}
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead
	if !safe || strings.HasPrefix(r.URL.Path, "/api/") {
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host != r.Host {
				http.Error(w, i18n.T("cross-origin request refused"), http.StatusForbidden)
				return
			}
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if f := r.Header.Get("Sec-Fetch-Site"); f != "" && f != "same-origin" && f != "none" {
			http.Error(w, i18n.T("cross-site request refused"), http.StatusForbidden)
			return
		}
	}
	if s.opt.Token != "" && !s.tokenOK(w, r) {
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	s.mux.ServeHTTP(w, r)
}

// asset is one embedded file, read and compressed once at startup.
type asset struct {
	plain, gz   []byte
	etag, ctype string
}

// assets serves the embedded frontend from memory: the request path is only
// a lookup key into what was walked at startup, never written back. embed.FS
// has no modtime, so each file gets an ETag from its content hash and reloads
// answer 304.
func assets(root fs.FS) http.Handler {
	types := map[string]string{".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml", ".json": "application/json", ".webmanifest": "application/manifest+json", ".html": "text/html; charset=utf-8"}
	files := map[string]*asset{}
	_ = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		a := &asset{plain: b, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, ctype: types[path.Ext(p)]}
		if compressible("/" + p) {
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			_, _ = zw.Write(b)
			_ = zw.Close()
			a.gz = buf.Bytes()
		}
		files["/"+p] = a
		return nil
	})
	files["/"] = files["/index.html"]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := files[r.URL.Path]
		if !ok && appPath(r.URL.Path) {
			a, ok = files["/"], true
		}
		if !ok || a == nil || r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if a.ctype != "" {
			h.Set("Content-Type", a.ctype)
		}
		body, etag := a.plain, a.etag
		if a.gz != nil {
			h.Set("Vary", "Accept-Encoding")
			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				body, etag = a.gz, etag[:len(etag)-1]+`-gz"`
				h.Set("Content-Encoding", "gzip")
			}
		}
		h.Set("Etag", etag)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	})
}

// appPath is a page of the app (/board/ABC/1, /issue/ABC-1): the shell, its router takes it from there.
// Files have an extension; /api answers itself or not at all.
func appPath(p string) bool {
	return p != "/api" && !strings.HasPrefix(p, "/api/") && path.Ext(p) == ""
}

func compressible(p string) bool {
	for _, e := range []string{".js", ".css", ".html", ".svg", ".json", "/"} {
		if strings.HasSuffix(p, e) {
			return true
		}
	}
	return false
}

// Serve listens on addr and serves until ctx ends, over TLS when certFile
// and keyFile are given. A wildcard or public address is refused unless
// allowRemote, and then needs TLS: the API acts as you on Jira and runs
// your commands, so its token must not cross the network in plain text.
func Serve(ctx context.Context, addr string, allowRemote bool, certFile, keyFile string, h http.Handler, ready func(net.Addr)) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		if !allowRemote {
			return fmt.Errorf(i18n.T("%s is not a loopback address: the web UI acts as you on Jira; pass -remote to allow it"), addr)
		}
		if certFile == "" {
			return fmt.Errorf(i18n.T("%s is not a loopback address: -remote needs -cert and -key, or an SSH tunnel to a loopback address"), addr)
		}
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return err
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if ready != nil {
		ready(ln.Addr())
	}
	serve := srv.Serve
	if srv.TLSConfig != nil {
		serve = func(ln net.Listener) error { return srv.ServeTLS(ln, "", "") }
	}
	if err := serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
