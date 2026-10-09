package web

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
)

// Hosted (Options.Hosted) is laneway web for many people, behind the
// gateway in webcmd's hosted.go: each person's app runs on this machine,
// so nothing in it may act as this machine's user. hosted-routes.txt says
// which routes serve then; any other answers 403.

//go:embed hosted-routes.txt
var hostedRoutesTxt string

// hostedOn are the routes a hosted app serves.
var hostedOn = func() map[string]bool {
	on := map[string]bool{}
	for _, l := range strings.Split(hostedRoutesTxt, "\n") {
		if p, ok := strings.CutPrefix(l, "on  "); ok {
			on[strings.TrimSpace(p)] = true
		}
	}
	return on
}()

// hostedGate refuses a route a hosted app doesn't serve.
func hostedGate(s *Server, pattern string, w http.ResponseWriter) bool {
	if !s.opt.Hosted || hostedOn[pattern] {
		return false
	}
	writeErr(w, httpError{http.StatusForbidden, i18n.T("not on a laneway web shared by many people")})
	return true
}

// hostedSetting reports whether a hosted app may write ui.<name>: not the
// ones that run commands or read files and hosts of a person's choosing.
func hostedSetting(name string) bool {
	return !config.SettingsCommand[name] && !HostedOffSettings[name]
}

// HostedOffSettings are more ui: options a hosted app neither writes nor
// reads: calendar is a file, directory or URL fetched by this machine;
// download_dir is a directory on it.
var HostedOffSettings = map[string]bool{"calendar": true, "download_dir": true}
