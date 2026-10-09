package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// laneway token and laneway apps: on laneway-server, your API tokens and
// the apps you signed in with. Jira keeps its tokens on Atlassian's page.

var tokenUsage = i18n.N(`usage: laneway token list
       laneway token create [-days N] LABEL
       laneway token revoke ID
       laneway token revoke-all

Your API tokens on a laneway-server site: list them, make one (its secret
is printed once), revoke one or all. -days is how long it lives (default
the site's). -config and -site go after token.`)

var appsUsage = i18n.N(`usage: laneway apps list
       laneway apps revoke ID

The apps you signed in to a laneway-server site with; revoke signs one
out. -config and -site go after apps.`)

// tokenCmd runs laneway token (apps false) or laneway apps.
func tokenCmd(apps bool, args []string, out, errOut io.Writer) int {
	usage, name := tokenUsage, "token"
	if apps {
		usage, name = appsUsage, "apps"
	}
	verbs := []string{"list", "create", "revoke", "revoke-all"}
	if apps {
		verbs = []string{"list", "revoke"}
	}
	if len(args) == 0 || !slices.Contains(verbs, args[0]) {
		fmt.Fprintln(errOut, i18n.T(usage))
		return 2
	}
	verb := args[0]
	fs := flag.NewFlagSet(name+" "+verb, flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() { fmt.Fprintln(errOut, i18n.T(usage)) }
	cfgPath := fs.String("config", "", i18n.T("config file"))
	site := fs.String("site", "", i18n.T("Jira site from the config's sites:"))
	days := fs.Int("days", 0, i18n.T("create: the days it lives (default the site's)"))
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) > 0 { // flags after the label or id too
		if err := fs.Parse(rest[1:]); err != nil {
			return 2
		}
		rest = append([]string{rest[0]}, fs.Args()...)
	}
	want := map[string]int{"list": 0, "create": 1, "revoke": 1, "revoke-all": 0}[verb]
	if len(rest) != want || verb == "create" && strings.TrimSpace(rest[0]) == "" {
		fs.Usage()
		return 2
	}
	c, err := siteClient(*cfgPath, *site)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch {
	case apps && verb == "list":
		err = appsList(ctx, c, out)
	case apps:
		if err = c.RevokeAppGrant(ctx, rest[0]); err == nil {
			fmt.Fprintln(out, i18n.Tf("revoked %s", rest[0]))
		}
	case verb == "list":
		err = tokenList(ctx, c, out)
	case verb == "create":
		var tok jira.APIToken
		var secret string
		if tok, secret, err = c.CreateAPIToken(ctx, strings.TrimSpace(rest[0]), *days, nil); err == nil {
			fmt.Fprintln(out, secret)
			fmt.Fprintln(errOut, i18n.Tf("%s: copy it now, it shows only once; it expires %s", tok.Label, dateOf(tok.Expires)))
		}
	case verb == "revoke":
		if err = c.RevokeAPIToken(ctx, rest[0]); err == nil {
			fmt.Fprintln(out, i18n.Tf("revoked %s", rest[0]))
		}
	default:
		if err = c.RevokeAPITokens(ctx); err == nil {
			fmt.Fprintln(out, i18n.T("revoked every token"))
		}
	}
	switch {
	case errors.Is(err, jira.ErrNotLaneway):
		fmt.Fprintln(errOut, "laneway:", i18n.Tf("the site is Jira: its tokens are on %s", config.TokenURL))
		return 1
	case errors.Is(err, jira.ErrTokenPage):
		fmt.Fprintln(errOut, "laneway:", i18n.Tf("this sign-in can't make tokens; make one on %s", c.TokenPage()))
		return 1
	case err != nil:
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	return 0
}

func tokenList(ctx context.Context, c *jira.Client, out io.Writer) error {
	toks, err := c.APITokens(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, t := range toks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Label, scopeList(t.Scopes), i18n.Tf("expires %s", dateOf(t.Expires)), usedOn(t.LastUsed))
	}
	return tw.Flush()
}

func appsList(ctx context.Context, c *jira.Client, out io.Writer) error {
	gs, err := c.AppGrants(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, g := range gs {
		name := g.App
		if g.Current {
			name += " " + i18n.T("(this app)")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", g.ID, name, scopeList(g.Scopes), i18n.Tf("expires %s", dateOf(g.Expires)), usedOn(g.LastUsed))
	}
	return tw.Flush()
}

func scopeList(s []string) string {
	if len(s) == 0 {
		return i18n.T("everything")
	}
	return strings.Join(s, ",")
}

func dateOf(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format(time.DateOnly)
}

func usedOn(t time.Time) string {
	if t.IsZero() {
		return i18n.T("never used")
	}
	return i18n.Tf("used %s", dateOf(t))
}
