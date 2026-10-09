package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// laneway login and logout: a laneway-server site's own sign-in (setup
// asks for it too). Jira sites use an API token instead.

// loginCmd signs a site in again through its laneway-server, or moves a
// token site on a laneway-server to the sign-in.
func loginCmd(args []string, errOut io.Writer) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfgPath := fs.String("config", "", i18n.T("config file (default ~/.config/laneway/config.yaml)"))
	site := fs.String("site", "", i18n.T("a site from sites: (default jira:)"))
	device := fs.Bool("device", false, i18n.T("sign in with a code on another device instead of this machine's browser"))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, path, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	j, err := cfg.Site(*site)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	ctx := context.Background()
	if !j.LanewaySignIn() && !probeLaneway(ctx, j.BaseURL) {
		fmt.Fprintln(errOut, "laneway: "+i18n.Tf("%s is a Jira site: it signs in with an API token (laneway setup)", j.BaseURL))
		return 1
	}
	p := terminalPrompter(strings.Fields(cfg.UI.Open))
	if err := keepLanewaySignIn(ctx, &j, p, *device); err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	if err := config.SetSite(path, *site, j); err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	return 0
}

// logoutCmd ends a site's laneway sign-in: the server revokes it and it
// is removed here.
func logoutCmd(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfgPath := fs.String("config", "", i18n.T("config file (default ~/.config/laneway/config.yaml)"))
	site := fs.String("site", "", i18n.T("a site from sites: (default jira:)"))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	j, err := cfg.Site(*site)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	if !j.LanewaySignIn() {
		fmt.Fprintln(errOut, "laneway: "+i18n.Tf("%s signs in with an API token; revoke it on the site", j.BaseURL))
		return 1
	}
	err = jira.New(jira.Config{BaseURL: j.BaseURL, SignIn: j.Credentials()}).SignOut(context.Background())
	var re jira.RevokeError
	if errors.As(err, &re) {
		fmt.Fprintln(errOut, "laneway: "+i18n.Tf("signed out of %s here, but the server didn't end the sign-in (%s); end it under signed-in apps there", j.BaseURL, re.Err))
		return 1
	}
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	fmt.Fprintln(out, i18n.Tf("signed out of %s", j.BaseURL))
	return 0
}
