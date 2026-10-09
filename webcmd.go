package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cornedor/laneway/internal/autostart"
	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/offline"
	"github.com/cornedor/laneway/internal/store"
	"github.com/cornedor/laneway/internal/ui"
	"github.com/cornedor/laneway/internal/web"
)

// webCmd is `laneway web` (alias serve): the board in a browser.
func webCmd(args []string, cfgPath, site string, errOut io.Writer) int {
	if len(args) > 0 && (args[0] == "-hosted" || args[0] == "--hosted") {
		return hostedCmd(args[1:], errOut)
	}
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(errOut)
	addr := fs.String("addr", "127.0.0.1:8484", i18n.T("address to listen on"))
	remote := fs.Bool("remote", false, i18n.T("allow a non-loopback address; needs -cert, -key and the printed ?token= URL. WARNING: grants shell access via ui.actions and ui.llm to whoever holds the token"))
	cert := fs.String("cert", "", i18n.T("TLS certificate file (PEM); serves https"))
	key := fs.String("key", "", i18n.T("TLS private key file (PEM) for -cert"))
	noOpen := fs.Bool("no-open", false, i18n.T("do not open the browser"))
	demoFlag := fs.Bool("demo", false, i18n.T("serve a generated project instead of Jira"))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if (*cert == "") != (*key == "") {
		fmt.Fprintln(errOut, i18n.T("laneway: -cert and -key go together"))
		return 2
	}
	if err := runWeb(cfgPath, site, *addr, *cert, *key, *remote, !*noOpen, *demoFlag); err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	return 0
}

func runWeb(cfgPath, site, addr, cert, key string, remote, open, demoMode bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	token := ""
	if remote {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		token = hex.EncodeToString(b)
	}
	// The setup screen ends by serving the app in its place, on the same
	// address: the browser reloads into it.
	for {
		next, bound, err := serveWeb(ctx, cfgPath, site, addr, cert, key, token, remote, open, demoMode)
		if errors.Is(err, syscall.EADDRINUSE) && runningWeb(addr) {
			// Started at login, most likely: open that one.
			url := "http://" + addr
			fmt.Fprintln(os.Stderr, i18n.Tf("laneway web already runs on %s", url))
			if open {
				openBrowser(url)
			}
			return nil
		}
		if err != nil || next == webQuit || ctx.Err() != nil {
			return err
		}
		open = false
		if _, port, _ := net.SplitHostPort(addr); port == "0" {
			addr = bound // back on the port it got
		}
		if next != webRestart {
			demoMode = next == webDemo
		}
		if next == webAdded {
			site = "" // the added one, the last site now
		}
	}
}

// How a web server ended: stopped, set up (to the site, or the demo), a
// site added or restarted.
const (
	webQuit = iota
	webSetUp
	webDemo
	webAdded   // a site was added: start again on it
	webRestart // asked from the settings: start again as it was
)

func serveWeb(parent context.Context, cfgPath, site, addr, cert, key, token string, remote, open, demoMode bool) (end int, bound string, err error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	end = webQuit
	opt := web.Options{Version: version, Demo: demoMode, Token: token, UpgradeCmd: upgradeCmd()}
	if demoMode {
		baseURL, stopDemo, err := newDemo().Start()
		if err != nil {
			return end, bound, err
		}
		defer stopDemo()
		dir, err := os.MkdirTemp("", "laneway-web-demo-")
		if err != nil {
			return end, bound, err
		}
		defer os.RemoveAll(dir)
		if opt.Store, err = store.Open(filepath.Join(dir, "state.json")); err != nil {
			return end, bound, err
		}
		jc := config.JiraConfig{BaseURL: baseURL, Email: "demo@example.com", APIToken: "demo", Projects: []string{"DEMO"}}
		opt.Site, opt.Jira = "demo", jc
		opt.GitLab = demoGitLab(baseURL)
		opt.ConfigPath = filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(opt.ConfigPath, []byte("# demo config\n"), 0o600); err != nil {
			return end, bound, err
		}
		var uiCfg config.UIConfig
		if cfgPath != "" { // as in the TUI's demo: the config's ui: and rules: apply, never its sites
			cfg, _, err := config.Load(cfgPath)
			if err != nil {
				return end, bound, err
			}
			uiCfg, opt.Rules, opt.RulesTest = cfg.UI, cfg.Rules, cfg.RulesTest
		}
		opt.UI = uiCfg
		opt.Client = webClient(jc, uiCfg)
		opt.Client.SetQueue(offline.To(opt.Store))
		opt.Client.SetLayouts(opt.Store)
	} else {
		cfg, loaded, err := config.Load(cfgPath)
		if err != nil && !errors.Is(err, config.ErrNoConfig) {
			return end, bound, err
		}
		opt.ConfigPath = loaded
		if site == "" {
			site = config.LastSite(cfg.SiteNames())
		}
		if st, ok := webSetup(cfg, loaded, site, err != nil); ok {
			st.Save = func(ctx context.Context, f web.SetupForm) (string, error) {
				who, err := webSetupSave(ctx, loaded, site, f)
				if err == nil {
					end = webSetUp
					if f.Demo {
						end = webDemo
					}
					time.AfterFunc(300*time.Millisecond, cancel) // after the answer is sent
				}
				return who, err
			}
			st.Probe = func(ctx context.Context, site string) bool {
				base, err := config.BaseURL(site)
				return err == nil && probeLaneway(ctx, base)
			}
			st.Laneway = webLaneway.start
			opt.Setup = st
		} else {
			if opt, err = webSite(cfg, site, opt); err != nil {
				return end, bound, err
			}
			opt.Open = func(other string) (web.Options, error) { return webSite(cfg, other, web.Options{ConfigPath: loaded}) }
			opt.AddSite = &web.SiteAdder{Keyring: keyringAvailable(), EnvToken: os.Getenv("JIRA_API_TOKEN") != "",
				Save: func(ctx context.Context, name string, f web.SetupForm) (string, string, error) {
					who, name, err := webAddSite(ctx, cfg, loaded, name, f)
					if err == nil {
						end = webAdded
						time.AfterFunc(300*time.Millisecond, cancel) // after the answer is sent
					}
					return who, name, err
				}}
			opt.Sites = cfg.SiteNames()
			opt.DefaultName = cfg.Jira.Name
		}
	}
	if opt.Setup == nil {
		opt.Restart = func() {
			end = webRestart
			time.AfterFunc(300*time.Millisecond, cancel) // after the answer is sent
		}
	}
	if !demoMode && !remote && cert == "" { // -remote's token changes each start: not for a login service
		opt.Autostart = webAutostart(cfgPath, addr)
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		opt.AllowedHosts = append(opt.AllowedHosts, allowedHosts(host)...)
	}
	srv := web.New(ctx, opt)
	err = web.Serve(ctx, addr, remote, cert, key, srv, func(a net.Addr) {
		bound = a.String()
		url := "http://" + a.String()
		if cert != "" {
			url = "https://" + a.String()
		}
		if opt.Token != "" {
			url += "/?token=" + opt.Token
		}
		switch {
		case opt.Setup != nil:
			fmt.Fprintln(os.Stderr, i18n.Tf("laneway web on %s · connect it to Jira there", url))
		default:
			fmt.Fprintln(os.Stderr, i18n.Tf("laneway web on %s", url))
		}
		if open {
			openBrowser(url)
		}
	})
	return end, bound, err
}

// runningWeb is whether a laneway web answers on addr.
func runningWeb(addr string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + addr + "/api/session")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&v) == nil && v.Version != ""
}

// webAutostart is the login service for this laneway web: this binary (by
// its PATH name when that is the same file, so an upgrade keeps working),
// the same address and -config, and this shell's PATH for the tools it runs.
func webAutostart(cfgPath, addr string) *web.Autostart {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if p, err := exec.LookPath("laneway"); err == nil && sameFile(p, exe) {
		exe = p
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	argv := []string{exe, "web", "-no-open", "-addr", addr}
	if cfgPath != "" {
		if abs, err := filepath.Abs(cfgPath); err == nil {
			argv = append(argv, "-config", abs)
		}
	}
	return &web.Autostart{Sys: autostart.Default(), Argv: argv, Path: os.Getenv("PATH")}
}

func sameFile(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

// webSetup is the setup screen for a start without a site to use: no
// config (noConfig), no jira: and no site picked, or a site without its
// email or token (filled in but those).
func webSetup(cfg config.Config, path, site string, noConfig bool) (*web.Setup, bool) {
	st := &web.Setup{Name: site, ConfigPath: path, EnvToken: os.Getenv("JIRA_API_TOKEN") != ""}
	st.Keyring = keyringAvailable()
	if noConfig || site == "" && strings.TrimSpace(cfg.Jira.BaseURL) == "" {
		return st, true
	}
	j, err := cfg.Site(site)
	if err != nil || j.Check(siteName(site)) == nil {
		return nil, false // an unknown -site says so as before
	}
	st.Prefill = web.SetupForm{Site: j.BaseURL, Email: j.Email}
	return st, true
}

// webAddSite writes another site from the site picker's form: a free name
// ("" names it after its address) and an address no site has yet.
func webAddSite(ctx context.Context, cfg config.Config, path, name string, f web.SetupForm) (who, site string, err error) {
	base, err := config.BaseURL(f.Site)
	if err != nil {
		return "", "", web.FieldError{Field: "site", Msg: sentence(err.Error())}
	}
	for _, n := range cfg.SiteNames() {
		if j, err := cfg.Site(n); err == nil && strings.TrimRight(j.BaseURL, "/") == strings.TrimRight(base, "/") {
			return "", "", web.FieldError{Field: "site", Msg: i18n.Tf("%s is the site %s already.", base, siteName(n))}
		}
	}
	name = cmp.Or(strings.TrimSpace(name), config.SiteName(base))
	switch {
	case !config.ValidSiteName(name):
		return "", "", web.FieldError{Field: "name", Msg: i18n.T("Lower-case letters, digits, - and _ only.")}
	case slices.Contains(cfg.SiteNames(), name):
		return "", "", web.FieldError{Field: "name", Msg: i18n.Tf("There is a site %s already.", name)}
	}
	if who, err = webSetupSave(ctx, path, name, f); err != nil {
		return "", "", err
	}
	_ = config.SetLastSite(name)
	return who, name, nil
}

// keyringAvailable is whether keyringFor finds a keyring tool.
func keyringAvailable() bool {
	return keyringFor("https://example.atlassian.net", "you@example.com") != nil
}

// sentence capitalises msg and ends it with a full stop.
func sentence(msg string) string {
	if msg == "" {
		return msg
	}
	return strings.ToUpper(msg[:1]) + strings.TrimSuffix(msg[1:], ".") + "."
}

// webSetupSave is the setup screen's Connect: as laneway setup, it signs in
// to check the site, then writes it to the config at path as site ("" is
// jira:), the token in the keyring when asked and one is there.
func webSetupSave(ctx context.Context, path, site string, f web.SetupForm) (string, error) {
	if f.Demo {
		return "", nil
	}
	base, err := config.BaseURL(f.Site)
	if err != nil {
		return "", web.FieldError{Field: "site", Msg: sentence(err.Error())}
	}
	if f.Laneway {
		creds, err := webLaneway.result(base)
		if err != nil {
			return "", err
		}
		j := config.JiraConfig{BaseURL: base, SignIn: config.SignInLaneway, Email: creds.Account}
		raw, _ := json.Marshal(creds)
		if err := j.Credentials().Save(raw); err != nil {
			return "", err
		}
		if err := config.SetSite(path, site, j); err != nil {
			return "", err
		}
		return creds.Account, nil
	}
	j := config.JiraConfig{BaseURL: base, Email: strings.TrimSpace(f.Email), APIToken: strings.TrimSpace(f.Token)}
	if !strings.Contains(j.Email, "@") {
		return "", web.FieldError{Field: "email", Msg: i18n.T("Type the email address you sign in to Jira with.")}
	}
	check := j
	if strings.HasPrefix(base, "https://") && strings.HasSuffix(strings.ToLower(base), ".atlassian.net") {
		// JIRA_API_TOKEN is an Atlassian Cloud token: whoever can reach this
		// form must not get it sent to a host of their choosing.
		check.APIToken = cmp.Or(j.APIToken, os.Getenv("JIRA_API_TOKEN"))
	}
	if check.APIToken == "" {
		return "", web.FieldError{Field: "token", Msg: i18n.T("Paste the API token.")}
	}
	who, err := signIn(ctx, check)
	switch {
	case err == nil:
	case errors.Is(err, jira.ErrUnauthorized):
		return "", web.FieldError{Field: "token", Msg: i18n.T("Jira did not accept this email and token. Check the email is the one you sign in with, and copy the token again.")}
	case errors.Is(err, jira.ErrNotFound):
		return "", web.FieldError{Field: "site", Msg: i18n.Tf("%s answers, but not as Jira Cloud. Check the address.", base)}
	default:
		return "", web.FieldError{Field: "site", Msg: i18n.Tf("Could not reach %s: %s", base, err.Error())}
	}
	if f.Keyring && j.APIToken != "" {
		if k := keyringFor(j.BaseURL, j.Email); k != nil && k.put(j.APIToken) == nil {
			j.APIToken, j.APITokenCmd = "", k.lookup
		}
	}
	if err := config.SetSite(path, site, j); err != nil {
		return "", err
	}
	return who, nil
}

// allowedHosts are the Host headers a browser may use for a server bound to
// host: the host itself, or for a wildcard bind this machine's addresses.
func allowedHosts(host string) []string {
	if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
		return []string{host}
	}
	var out []string
	if h, err := os.Hostname(); err == nil {
		out = append(out, h)
	}
	if as, err := net.InterfaceAddrs(); err == nil {
		for _, a := range as {
			if ipn, ok := a.(*net.IPNet); ok {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

func openBrowser(url string) {
	name := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name = "rundll32"
		url = "url.dll,FileProtocolHandler " + url
	}
	_ = exec.Command(name, url).Start()
}

// webSite opens a configured site for the server: its client and state.
func webSite(cfg config.Config, site string, opt web.Options) (web.Options, error) {
	jc, err := cfg.Site(site)
	if err != nil {
		return opt, err
	}
	if err := jc.Check(siteName(site)); err != nil {
		return opt, fmt.Errorf("%w\n%s", err, i18n.T("`laneway setup` asks for them and checks they work"))
	}
	path, err := config.SiteStatePath(site)
	if err != nil {
		return opt, err
	}
	if opt.Store, err = store.Open(path); err != nil {
		return opt, err
	}
	opt.Site, opt.Jira, opt.UI = site, jc, cfg.UI
	opt.Rules, opt.RulesTest = cfg.Rules, cfg.RulesTest
	opt.Client = webClient(jc, cfg.UI)
	opt.GitLab, _ = gitlabSites(cfg)
	opt.GitLabRepos = gitlabRepos(cfg)
	opt.Client.SetQueue(offline.To(opt.Store))
	opt.Client.SetLayouts(opt.Store)
	return opt, nil
}

func webClient(jc config.JiraConfig, uiCfg config.UIConfig) *jira.Client {
	return jira.New(ui.JiraConfig(jc, uiCfg))
}

// deviceLogin is jira's; tests swap it.
var deviceLogin = jira.DeviceLogin

// webLaneway holds the setup screen's laneway sign-ins under way, by site.
var webLaneway = &lanewaySignIns{pending: map[string]*lanewaySignIn{}}

type lanewaySignIns struct {
	mu      sync.Mutex
	pending map[string]*lanewaySignIn
}

type lanewaySignIn struct {
	done  chan struct{}
	creds jira.Credentials
	err   error
}

// start begins a sign-in to site with a code the person allows at its
// /device page (in another tab, or on another device for a remote
// laneway web); Save with Laneway picks it up.
func (l *lanewaySignIns) start(ctx context.Context, site string) (web.LanewayCode, error) {
	base, err := config.BaseURL(site)
	if err != nil {
		return web.LanewayCode{}, web.FieldError{Field: "site", Msg: sentence(err.Error())}
	}
	shown := make(chan web.LanewayCode, 1)
	p := &lanewaySignIn{done: make(chan struct{})}
	l.mu.Lock()
	l.pending[base] = p
	l.mu.Unlock()
	go func() {
		defer close(p.done)
		ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
		defer cancel()
		p.creds, p.err = deviceLogin(ctx, base, func(code, uri, complete string) {
			shown <- web.LanewayCode{Code: code, URI: uri, Complete: complete}
		})
	}()
	select {
	case c := <-shown:
		return c, nil
	case <-p.done:
		return web.LanewayCode{}, web.FieldError{Field: "site", Msg: sentence(p.err.Error())}
	case <-ctx.Done():
		return web.LanewayCode{}, ctx.Err()
	}
}

// result is site's sign-in once allowed; web.ErrPending while it waits.
func (l *lanewaySignIns) result(base string) (jira.Credentials, error) {
	l.mu.Lock()
	p := l.pending[base]
	l.mu.Unlock()
	if p == nil {
		return jira.Credentials{}, web.FieldError{Field: "site", Msg: i18n.T("Start the sign-in first.")}
	}
	select {
	case <-p.done:
	default:
		return jira.Credentials{}, web.ErrPending
	}
	l.mu.Lock()
	delete(l.pending, base)
	l.mu.Unlock()
	if p.err != nil {
		return jira.Credentials{}, web.FieldError{Field: "site", Msg: sentence(p.err.Error())}
	}
	return p.creds, nil
}
