package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/rules"
)

// rulesWatch polls the rules' watches until interrupted, like the TUI does,
// running log, notify and exec. highlight needs a board and is skipped.
func rulesWatch(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("rules watch", flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfgPath := fs.String("config", "", i18n.T("config file"))
	site := fs.String("site", "", i18n.T("Jira site from the config's sites: (default jira:)"))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := loadSite(*cfgPath, *site)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	set, warn := rules.Compile(cfg.Rules)
	for _, w := range warn {
		fmt.Fprintln(errOut, i18n.T("skipped:"), w)
	}
	watches := set.Watches()
	if len(watches) == 0 {
		fmt.Fprintln(errOut, "laneway:", i18n.T("no rule has a watch:"))
		return 1
	}
	if err := cfg.Jira.Check(siteName(*site)); err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	state, err := config.SiteStatePath(*site)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	timeout, err := cfg.Jira.RequestTimeout()
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	c := jira.New(jira.Config{BaseURL: cfg.Jira.BaseURL, Email: cfg.Jira.Email, APIToken: cfg.Jira.APIToken, SignIn: cfg.Jira.Credentials(),
		StoryPointsField: cfg.Jira.StoryPointsField, CardLimit: cfg.UI.CardLimit, Timeout: timeout})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	st, _ := os.Stdout.Stat()
	w := &rules.Watcher{C: c, Set: set, Log: filepath.Join(filepath.Dir(state), "rules.log"), Out: out}
	if st != nil && out == io.Writer(os.Stdout) && st.Mode()&os.ModeCharDevice != 0 {
		w.Notify = func(f rules.Firing) { w.Print(rules.NotifySeq(f.Title, f.Text)) }
	}
	for _, wt := range watches {
		fmt.Fprintf(out, i18n.T("watching %s every %s")+"\n", wt.JQL, wt.Every)
	}
	w.Run(ctx)
	return 0
}
