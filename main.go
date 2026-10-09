// laneway is a terminal board for Jira: a project's board as swim
// lanes or a list, with the selected issue in a panel on the right.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/demo"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/index"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/store"
	"github.com/cornedor/laneway/internal/ui"
)

// version is set by the release build.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, i18n.T("print the version and exit"))
	cfgPath := flag.String("config", "", i18n.T("config file (default ~/.config/laneway/config.yaml, then jiratui's and matterbox's)"))
	site := flag.String("site", "", i18n.T("Jira site from the config's sites: (default the one last picked with @, else jira:)"))
	demoFlag := flag.Bool("demo", false, i18n.T("try laneway on a generated project, without Jira; writes are kept in memory (-config: its ui: only)"))
	flag.Parse()
	version = buildVersion(version)
	if *showVersion {
		fmt.Println("laneway", version)
		return
	}
	if *demoFlag {
		if err := runDemo(*cfgPath); err != nil {
			fmt.Fprintln(os.Stderr, "laneway:", err)
			os.Exit(1)
		}
		return
	}
	if flag.NArg() > 0 {
		os.Exit(subcommand(flag.Args(), *cfgPath, *site, os.Stdout, os.Stderr))
	}
	if err := run(*cfgPath, *site); err != nil {
		fmt.Fprintln(os.Stderr, "laneway:", err)
		os.Exit(1)
	}
}

// subcommand runs a command without the board (list, view, create, move,
// rules, setup, prompt, hook, index, token or apps), handing on the global
// -config and -site given before it.
func subcommand(args []string, cfgPath, site string, out, errOut io.Writer) int {
	var global []string
	if cfgPath != "" {
		global = append(global, "-config", cfgPath)
	}
	switch args[0] {
	case "rules":
		if len(args) > 1 && site != "" {
			global = append(global, "-site", site)
		}
		if len(args) > 1 {
			return rulesCmd(append([]string{args[1]}, append(global, args[2:]...)...), out, errOut)
		}
		return rulesCmd(nil, out, errOut)
	case "setup":
		if site != "" {
			global = append(global, "-site", site)
		}
		return setupCmd(append(global, args[1:]...), errOut)
	case "login":
		if site != "" {
			global = append(global, "-site", site)
		}
		return loginCmd(append(global, args[1:]...), errOut)
	case "logout":
		if site != "" {
			global = append(global, "-site", site)
		}
		return logoutCmd(append(global, args[1:]...), out, errOut)
	case "prompt":
		if site != "" {
			global = append(global, "-site", site)
		}
		return promptCmd(append(global, args[1:]...), out, errOut)
	case "hook":
		if len(args) < 2 {
			return hookCmd(nil, out, errOut)
		}
		if site != "" {
			global = append(global, "-site", site)
		}
		return hookCmd(append([]string{args[1]}, append(global, args[2:]...)...), out, errOut)
	case "index":
		return indexCmd(args[1:], site, out, errOut)
	case "mr":
		return mrCmd(args[1:], out, errOut)
	case "token", "apps":
		if site != "" {
			global = append(global, "-site", site)
		}
		if len(args) < 2 {
			return tokenCmd(args[0] == "apps", nil, out, errOut)
		}
		return tokenCmd(args[0] == "apps", append([]string{args[1]}, append(global, args[2:]...)...), out, errOut)
	case "web", "serve":
		return webCmd(args[1:], cfgPath, site, errOut)
	case "completion":
		return completionCmd(args[1:], out, errOut)
	case "__complete":
		return completeCmd(args[1:], out)
	case "list", "view", "create", "move":
		if site != "" {
			global = append(global, "-site", site)
		}
		return cliCmd(args[0], append(global, args[1:]...), out, errOut)
	}
	fmt.Fprintf(errOut, i18n.T("laneway: unknown command %q (list, view, create, move, web, rules, setup, login, logout, prompt, hook, index, token, apps, completion)")+"\n", args[0])
	return 2
}

func run(cfgPath, site string) error {
	// Without -site it starts on the site last picked with @.
	remembered := site == ""
	// The config is read again for each run: @ ends the app with another
	// site picked, or to add one, and it starts again there.
	for {
		cfg, path, err := config.Load(cfgPath)
		if remembered && err == nil {
			site, remembered = config.LastSite(cfg.SiteNames()), false
		}
		first := errors.Is(err, config.ErrNoConfig) || err == nil && site == "" && strings.TrimSpace(cfg.Jira.BaseURL) == ""
		if first && interactive() {
			// A first start: ask for the site instead of explaining YAML.
			if site, err = setup(context.Background(), cfgPath, terminalPrompter(strings.Fields(cfg.UI.Open)), signIn); errors.Is(err, errTryDemo) {
				return runDemo(cfgPath)
			} else if err != nil {
				return err
			}
			fmt.Println()
			continue
		}
		if err != nil {
			return err
		}
		next, end, err := runSite(cfg, path, site)
		switch {
		case err != nil || end == endQuit:
			return err
		case end == endAddSite:
			added, err := setup(context.Background(), path, terminalPrompter(strings.Fields(cfg.UI.Open)), signIn)
			if err == nil {
				site = added
				_ = config.SetLastSite(site)
			} else if !errors.Is(err, errCancelled) {
				fmt.Fprintln(os.Stderr, "laneway:", err)
				fmt.Fprint(os.Stderr, i18n.T("enter goes back to the board… "))
				fmt.Scanln()
			}
		default:
			site = next
			_ = config.SetLastSite(site) // a convenience: failing it only forgets the pick
		}
	}
}

// How the app ended: quit, to switch site, or to add one.
const (
	endQuit = iota
	endSwitch
	endAddSite
)

// runSite runs the app on site, and says how it ended: the site it was
// left for, or to add one.
func runSite(cfg config.Config, cfgPath, site string) (string, int, error) {
	jc, err := cfg.Site(site)
	if err != nil {
		return "", endQuit, err
	}
	if err := jc.Check(siteName(site)); err != nil {
		return "", endQuit, fmt.Errorf("%w\n%s", err, i18n.T("`laneway setup` asks for them and checks they work"))
	}
	path, err := config.SiteStatePath(site)
	if err != nil {
		return "", endQuit, err
	}
	st, err := store.Open(path)
	if err != nil {
		return "", endQuit, err
	}
	// The index is a mirror: without it the app still runs.
	var ix *index.Index
	if p, err := index.Path(site); err == nil {
		if ix, err = index.Open(p); err != nil {
			fmt.Fprintln(os.Stderr, "laneway: index:", err)
		}
	}
	defer ix.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rulesLog := filepath.Join(filepath.Dir(path), "rules.log")
	gl, glWarn := gitlabSites(cfg)
	m := ui.New(ctx, jc, cfg.UI, cfg.Rules, rulesLog, st).WithSites(cfg.SiteNames(), site, cfg.Jira.Name).WithConfigPath(cfgPath).WithWarnings(append(cfg.Warnings, glWarn...)).WithIndex(ix).WithVersion(version, upgradeCmd()).WithGitLab(gl).WithGitLabRepos(gitlabRepos(cfg)).
		WithSiteClients(func(other string) (*jira.Client, error) {
			j, err := cfg.Site(other)
			if err != nil || j.Check(siteName(other)) != nil {
				return nil, err
			}
			timeout, _ := j.RequestTimeout()
			return jira.New(jira.Config{BaseURL: j.BaseURL, Email: j.Email, APIToken: j.APIToken, SignIn: j.Credentials(), Timeout: timeout}), nil
		})
	final, err := newProgram(m).Run()
	fm, ok := final.(ui.Model)
	if ok {
		fmt.Fprint(os.Stdout, fm.ReleaseImages()+fm.ReleasePointer())
	}
	if err != nil || !ok {
		return "", endQuit, err
	}
	if fm.AddSite() {
		return "", endAddSite, nil
	}
	if next, ok := fm.NextSite(); ok {
		return next, endSwitch, nil
	}
	return "", endQuit, nil
}

// runDemo runs the app on a generated project served in-process, with a
// throwaway state: nothing of yours is read or written.
// runDemo runs the board on a generated project; with cfgPath its ui:
// settings apply (the docs' screenshots use that), never its sites.
func runDemo(cfgPath string) error {
	var uiCfg config.UIConfig
	if cfgPath != "" {
		cfg, _, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		uiCfg = cfg.UI
	}
	uiCfg.UpdateCheck = "off"
	baseURL, stop, err := newDemo().Start()
	if err != nil {
		return err
	}
	defer stop()
	dir, err := os.MkdirTemp("", "laneway-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	st, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jc := config.JiraConfig{BaseURL: baseURL, Email: "demo@example.com", APIToken: "demo", Projects: []string{"DEMO"}}
	m := ui.New(ctx, jc, uiCfg, nil, filepath.Join(dir, "rules.log"), st).WithVersion(version, "").WithDemo().WithGitLab(demoGitLab(baseURL))
	final, err := newProgram(m).Run()
	if fm, ok := final.(ui.Model); ok {
		fmt.Fprint(os.Stdout, fm.ReleasePointer())
	}
	return err
}

// newDemo is the demo Jira. LANEWAY_DEMO_UNHANDLED names a file it adds
// each request it can't answer to, for the e2e tests; LANEWAY_DEMO_BULK a
// number of issues more, for timing a big board; LANEWAY_DEMO_FLAVOUR=laneway
// makes it laneway-server instead of Jira Cloud.
func newDemo() *demo.Server {
	s := demo.New(time.Now())
	if os.Getenv("LANEWAY_DEMO_FLAVOUR") == "laneway" {
		s.Laneway()
	}
	if n, err := strconv.Atoi(os.Getenv("LANEWAY_DEMO_BULK")); err == nil {
		s.Bulk(n)
	}
	if path := os.Getenv("LANEWAY_DEMO_UNHANDLED"); path != "" {
		s.OnUnhandled = func(req string) {
			if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintln(f, req)
				f.Close()
			}
		}
	}
	return s
}

// newProgram runs m with wheel bursts folded into one message a frame.
func newProgram(m tea.Model) *tea.Program {
	var p *tea.Program
	p = tea.NewProgram(m, tea.WithFilter(ui.WheelFilter(func(msg tea.Msg) { p.Send(msg) })))
	return p
}

func siteName(site string) string {
	if site == "" {
		return "jira"
	}
	return "sites." + site
}

// buildVersion is the release build's version, else the module version a
// go install records; "dev" for a local build (its VCS-stamped version
// too: only a module download has a sum).
func buildVersion(v string) string {
	if v != "dev" {
		return v
	}
	if bi, ok := debug.ReadBuildInfo(); ok && goInstalled(bi) {
		return bi.Main.Version
	}
	return v
}

func goInstalled(bi *debug.BuildInfo) bool {
	return bi.Main.Sum != "" && bi.Main.Version != "" && bi.Main.Version != "(devel)"
}

// upgradeCmd is the command that updates this binary, from where it was
// installed; "" when unknown.
func upgradeCmd() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, _ = filepath.EvalSymlinks(exe)
	if strings.Contains(exe, "/Caskroom/") || strings.Contains(exe, "/homebrew/") || strings.Contains(exe, "/linuxbrew/") {
		return "brew upgrade laneway"
	}
	if bi, ok := debug.ReadBuildInfo(); ok && goInstalled(bi) {
		return "go install github.com/cornedor/laneway@latest"
	}
	return ""
}
