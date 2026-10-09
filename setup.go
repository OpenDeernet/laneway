package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/opener"
)

// errCancelled is setup ended with ctrl+d.
var errCancelled = errors.New("setup cancelled")

// prompter asks setup's questions: lines from in, a token through secret
// (not echoed on a terminal).
type prompter struct {
	in     *bufio.Reader
	out    io.Writer
	secret func() (string, error)
	open   func(url string) error
}

// ask prints label, with def in brackets when there is one, and returns
// the answer, def for an empty one.
func (p prompter) ask(label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(p.out, "%s: ", label)
	}
	line, err := p.in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		fmt.Fprintln(p.out)
		return "", errCancelled
	}
	return cmp.Or(strings.TrimSpace(line), def), nil
}

// terminalPrompter reads the terminal; the token is read without echo.
func terminalPrompter(openCmd []string) prompter {
	in := bufio.NewReader(os.Stdin)
	p := prompter{in: in, out: os.Stdout, open: func(u string) error { return opener.Open(openCmd, u) }}
	p.secret = func() (string, error) {
		if !term.IsTerminal(os.Stdin.Fd()) {
			line, err := in.ReadString('\n')
			if err != nil && line == "" {
				return "", errCancelled
			}
			return strings.TrimSpace(line), nil
		}
		b, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Fprintln(os.Stdout)
		if err != nil {
			return "", errCancelled
		}
		return strings.TrimSpace(string(b)), nil
	}
	return p
}

// signIn checks a site's login, and says whose it is.
func signIn(ctx context.Context, j config.JiraConfig) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	u, err := jira.New(jira.Config{BaseURL: j.BaseURL, Email: j.Email, APIToken: j.APIToken}).Myself(ctx)
	return u.DisplayName, err
}

// probeLaneway and lanewayLogin are jira's; tests swap them.
var (
	probeLaneway = jira.ProbeLaneway
	lanewayLogin = func(ctx context.Context, base string, p prompter, device bool) (jira.Credentials, error) {
		// Over SSH the browser here isn't the person's: a code they type
		// on their own device instead.
		if device || os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
			return jira.DeviceLogin(ctx, base, func(code, uri, complete string) {
				fmt.Fprintln(p.out, "  "+i18n.Tf("open %s on any device and enter %s", uri, code))
				fmt.Fprint(p.out, "  "+i18n.T("waiting… "))
			})
		}
		return jira.LoopbackLogin(ctx, base, func(u string) error {
			fmt.Fprintln(p.out, "  "+i18n.T("signing in in your browser; if it didn't open, open this:"))
			fmt.Fprintln(p.out, "  "+u)
			fmt.Fprint(p.out, "  "+i18n.T("waiting… "))
			p.open(u)
			return nil
		})
	}
)

// keepLanewaySignIn signs j in through its laneway-server and keeps the
// sign-in: j becomes a sign_in: laneway site for the account.
func keepLanewaySignIn(ctx context.Context, j *config.JiraConfig, p prompter, device bool) error {
	creds, err := lanewayLogin(ctx, j.BaseURL, p, device)
	if err != nil {
		fmt.Fprintln(p.out)
		return err
	}
	j.SignIn, j.Email, j.APIToken, j.APITokenCmd = config.SignInLaneway, creds.Account, "", nil
	raw, _ := json.Marshal(creds)
	if err := j.Credentials().Save(raw); err != nil {
		return fmt.Errorf(i18n.T("keeping the sign-in: %w"), err)
	}
	fmt.Fprintln(p.out, "✓ "+i18n.Tf("signed in as %s", creds.Account))
	return nil
}

// errTryDemo is "demo" typed at a first start's site prompt: laneway -demo
// instead.
var errTryDemo = errors.New("try the demo")

// setup asks for a Jira site, signs in to check it and writes it to the
// config at cfgPath (the default when ""): jira: when that has no site
// yet, else sites.<name>. A site whose base_url is already there gets the
// new email and token. It returns the site's name ("" is jira:).
func setup(ctx context.Context, cfgPath string, p prompter, verify func(context.Context, config.JiraConfig) (string, error)) (string, error) {
	return setupSite(ctx, cfgPath, "", p, verify)
}

// setupSite is setup with site's URL filled in, so enter signs that site in
// again (a new token); "" asks for any.
func setupSite(ctx context.Context, cfgPath, site string, p prompter, verify func(context.Context, config.JiraConfig) (string, error)) (string, error) {
	cfg, path, err := config.Load(cfgPath)
	if err != nil && !errors.Is(err, config.ErrNoConfig) {
		return "", err
	}
	var j config.JiraConfig
	if site != "" {
		known, ok := cfg.Sites[site]
		if !ok {
			return "", fmt.Errorf(i18n.T("no site %q in sites:; laneway setup adds one"), site)
		}
		j.BaseURL = known.BaseURL
	}
	if filepath.Base(filepath.Dir(path)) == "matterbox" {
		return "", fmt.Errorf(i18n.T("the config in use is matterbox's (%s), whose sites laneway doesn't read; copy its jira: to a laneway config (-config) first"), path)
	}
	first := errors.Is(err, config.ErrNoConfig) || cfg.Jira.BaseURL == "" && len(cfg.Sites) == 0
	if first {
		fmt.Fprintln(p.out, i18n.T("Connect laneway to a Jira site, or type demo to try it on a generated board first. ctrl+d cancels."))
	} else {
		fmt.Fprintln(p.out, i18n.T("Connect laneway to a Jira site. ctrl+d cancels."))
	}
	fmt.Fprintln(p.out)
	envToken := os.Getenv("JIRA_API_TOKEN")
	name, found, typedTok := "", false, ""
	for {
		typed, err := p.ask(i18n.T("Jira site, its name (acme) or URL"), j.BaseURL)
		if err != nil {
			return "", err
		}
		if first && strings.EqualFold(strings.TrimSpace(typed), "demo") {
			return "", errTryDemo
		}
		if j.BaseURL, err = config.BaseURL(typed); err != nil {
			fmt.Fprintln(p.out, "  "+err.Error())
			continue
		}
		fmt.Fprintln(p.out, "  "+j.BaseURL)
		name, found = cfg.SiteFor(j.BaseURL)
		known, _ := cfg.Site(name)
		if found {
			fmt.Fprintf(p.out, "  "+i18n.T("already set up as %s; this replaces its email and token")+"\n", siteName(name))
		} else {
			known = cfg.Jira // the same person, most likely
		}
		if probeLaneway(ctx, j.BaseURL) {
			yes, err := p.ask(i18n.T("It's a laneway-server: sign in through it (the browser, or a code on another device)? [Y/n, n: email and API token]"), "")
			if err != nil {
				return "", err
			}
			if yes == "" || strings.HasPrefix(strings.ToLower(yes), "y") || strings.HasPrefix(strings.ToLower(yes), "j") {
				if err := keepLanewaySignIn(ctx, &j, p, false); err != nil {
					fmt.Fprintln(p.out, "  ✗ "+err.Error())
					fmt.Fprintln(p.out, "  "+i18n.T("Try again; enter keeps what you typed."))
					continue
				}
				break
			}
		}
		if j.Email, err = p.ask(i18n.T("Email"), cmp.Or(j.Email, known.Email)); err != nil {
			return "", err
		}
		// JIRA_API_TOKEN stands in for jira:'s token only.
		useEnv := envToken != "" && name == "" && (found || cfg.Jira.BaseURL == "")
		tok, err := askToken(p, typedTok, useEnv)
		if err != nil {
			return "", err
		}
		typedTok = tok
		check := j
		check.APIToken = cmp.Or(tok, envToken)
		fmt.Fprint(p.out, "  "+i18n.T("signing in… "))
		who, err := verify(ctx, check)
		if err != nil {
			fmt.Fprintln(p.out, "✗ "+err.Error())
			fmt.Fprintln(p.out, "  "+i18n.T("Try again; enter keeps what you typed."))
			continue
		}
		fmt.Fprintln(p.out, "✓ "+i18n.Tf("signed in as %s", who))
		j.APIToken = tok
		break
	}
	if j.APIToken != "" {
		if k := keyringFor(j.BaseURL, j.Email); k != nil {
			if yes, err := p.ask(i18n.T("Keep the token in the system keyring instead of the config file? [Y/n]"), ""); err != nil {
				return "", err
			} else if yes == "" || strings.HasPrefix(strings.ToLower(yes), "y") || strings.HasPrefix(strings.ToLower(yes), "j") {
				if err := k.put(j.APIToken); err != nil {
					fmt.Fprintln(p.out, "  "+i18n.Tf("the keyring refused it (%s); keeping it in the file", err.Error()))
				} else {
					j.APIToken, j.APITokenCmd = "", k.lookup
					fmt.Fprintln(p.out, "  "+i18n.Tf("in the keyring; the config runs %s for it", strings.Join(k.lookup, " ")))
				}
			}
		}
	}
	if !found && cfg.Jira.BaseURL != "" {
		for {
			typed, err := p.ask(i18n.T("A name for it, to pick it by"), config.SiteName(j.BaseURL))
			if err != nil {
				return "", err
			}
			_, taken := cfg.Sites[typed]
			switch {
			case !config.ValidSiteName(typed):
				fmt.Fprintln(p.out, "  "+i18n.T("lower-case letters, digits, - and _ only"))
			case taken:
				fmt.Fprintf(p.out, "  "+i18n.T("there is a site %s already")+"\n", typed)
			default:
				name = typed
			}
			if name != "" {
				break
			}
		}
	}
	if err := config.SetSite(path, name, j); err != nil {
		return "", err
	}
	fmt.Fprintf(p.out, "\n"+i18n.T("Saved as %s in %s.")+"\n", siteName(name), path)
	if name != "" {
		fmt.Fprintf(p.out, i18n.T("Switch to it with @ in the app, or start on it with laneway -site %s.")+"\n", name)
	}
	return name, nil
}

// askToken reads the API token; an empty answer opens the page that makes
// one and asks again. keep is a token typed before (enter keeps it);
// useEnv lets enter take JIRA_API_TOKEN.
func askToken(p prompter, keep string, useEnv bool) (string, error) {
	opened := false
	for {
		switch {
		case keep != "":
			fmt.Fprint(p.out, i18n.T("API token [enter keeps the one typed]: "))
		case useEnv:
			fmt.Fprint(p.out, i18n.T("API token [enter uses JIRA_API_TOKEN]: "))
		case opened:
			fmt.Fprint(p.out, i18n.T("API token: "))
		default:
			fmt.Fprintln(p.out, i18n.Tf("API token: make one at %s", config.TokenURL))
			fmt.Fprint(p.out, "  "+i18n.T("paste it, or press enter to open that page: "))
		}
		tok, err := p.secret()
		if err != nil {
			return "", err
		}
		if tok != "" || keep != "" || useEnv {
			return cmp.Or(tok, keep), nil
		}
		if !opened {
			opened = true
			if err := p.open(config.TokenURL); err != nil {
				fmt.Fprintln(p.out, "  "+i18n.Tf("can't open a browser here; the page is %s", config.TokenURL))
			}
		}
	}
}

// setupCmd is `laneway setup`.
func setupCmd(args []string, errOut io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfgPath := fs.String("config", "", i18n.T("config file to write (default ~/.config/laneway/config.yaml)"))
	site := fs.String("site", "", i18n.T("a site from sites: to sign in again"))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, _ := config.Load(*cfgPath)
	name, err := setupSite(context.Background(), *cfgPath, *site, terminalPrompter(strings.Fields(cfg.UI.Open)), signIn)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	if name == "" {
		fmt.Fprintln(os.Stdout, i18n.T("Start it with laneway."))
	}
	return 0
}

// interactive is whether setup can ask: both ends a terminal.
func interactive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}
