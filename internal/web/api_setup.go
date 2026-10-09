package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
)

// Setup is the first start: no site to talk to yet. With Options.Setup set
// the server serves only the setup screen (GET /api/session, POST
// /api/setup); every other API answers 503.
type Setup struct {
	// Name is the site being set up ("" is jira:); Prefill its URL and email
	// when the config has them, but no token.
	Name    string
	Prefill SetupForm
	// ConfigPath is the file the site is written to.
	ConfigPath string
	// Keyring is whether a system keyring can keep the token; EnvToken that
	// JIRA_API_TOKEN is set, so the token may be left empty.
	Keyring, EnvToken bool
	// Save signs in with f and writes the site (Demo: the generated board
	// instead); the server is then replaced by the app on that site. It
	// answers whose login it is.
	Save func(ctx context.Context, f SetupForm) (string, error)
	// Probe says whether site is a laneway-server, which signs in through
	// itself; Laneway starts that sign-in (a code to allow on its /device
	// page). Save with SetupForm.Laneway then answers ErrPending until the
	// person allowed it. Nil: token sign-in only.
	Probe   func(ctx context.Context, site string) bool
	Laneway func(ctx context.Context, site string) (LanewayCode, error)
}

// LanewayCode is a laneway sign-in waiting for the person: the code to
// check and where to allow it.
type LanewayCode struct {
	Code, URI, Complete string
}

// ErrPending is Save's answer while a laneway sign-in waits.
var ErrPending = errors.New("waiting for the sign-in")

// SiteAdder adds a Jira site to the config, as `laneway setup` does with a
// site set up already: Save signs in, writes it under name ("" names it
// after its address) and the server comes back on it. It answers whose
// login it is and the name.
type SiteAdder struct {
	Keyring, EnvToken bool
	Save              func(ctx context.Context, name string, f SetupForm) (who, site string, err error)
}

// SetupForm is what the setup screen sends. Laneway is a laneway-server
// sign-in Laneway started, instead of email and token.
type SetupForm struct {
	Site, Email, Token                string
	Keyring, Demo, Autostart, Laneway bool
}

// FieldError is a refusal of one form field ("site", "email", "token").
type FieldError struct{ Field, Msg string }

func (e FieldError) Error() string { return e.Msg }

// setupOnly is the gate in setup mode: the setup screen's own calls pass.
func setupOnly(s *Server, w http.ResponseWriter, r *http.Request) bool {
	if s.opt.Setup == nil || r.URL.Path == "/api/session" || r.URL.Path == "/api/i18n.js" || r.URL.Path == "/api/setup" || r.URL.Path == "/api/setup/probe" || r.URL.Path == "/api/setup/laneway" || r.URL.Path == "/api/autostart" {
		return false
	}
	writeErr(w, httpError{http.StatusServiceUnavailable, i18n.T("laneway is not connected to Jira yet")})
	return true
}

func init() {
	post("/sites", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		a := s.sites.base.AddSite
		if a == nil || s.opt.Demo {
			return nil, httpError{http.StatusNotImplemented, i18n.T("adding a site needs the config file")}
		}
		f, err := Body[struct {
			SetupForm
			Name string
		}](r)
		if err != nil {
			return nil, err
		}
		f.Demo, f.Autostart = false, false
		who, site, err := a.Save(ctx, f.Name, f.SetupForm)
		if err != nil {
			return nil, err
		}
		return map[string]any{"who": who, "site": site}, nil
	})
	post("/setup", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		if s.opt.Setup == nil {
			return nil, httpError{http.StatusConflict, i18n.T("already set up")}
		}
		f, err := Body[SetupForm](r)
		if err != nil {
			return nil, err
		}
		who, err := s.opt.Setup.Save(ctx, f)
		if errors.Is(err, ErrPending) {
			return map[string]any{"pending": true}, nil
		}
		if err != nil {
			return nil, err
		}
		// Saved: installed only now, and quickly, before this server is swapped for the app.
		var startErr string
		if f.Autostart && !f.Demo {
			if err := s.setAutostart(true); err != nil {
				startErr = err.Error()
			}
		}
		return map[string]any{"who": who, "configPath": s.opt.Setup.ConfigPath, "autostartError": startErr}, nil
	})
}

func init() {
	post("/setup/probe", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		st := s.opt.Setup
		if st == nil {
			return nil, httpError{http.StatusConflict, i18n.T("already set up")}
		}
		f, err := Body[SetupForm](r)
		if err != nil {
			return nil, err
		}
		return map[string]any{"laneway": st.Probe != nil && st.Probe(ctx, f.Site)}, nil
	})
	post("/setup/laneway", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		st := s.opt.Setup
		if st == nil || st.Laneway == nil {
			return nil, httpError{http.StatusConflict, i18n.T("already set up")}
		}
		f, err := Body[SetupForm](r)
		if err != nil {
			return nil, err
		}
		c, err := st.Laneway(ctx, f.Site)
		if err != nil {
			return nil, err
		}
		return map[string]any{"code": c.Code, "uri": c.URI, "complete": c.Complete}, nil
	})
}

// setupSession is /api/session in setup mode.
func setupSession(s *Server) map[string]any {
	st := s.opt.Setup
	return map[string]any{
		"version": s.opt.Version,
		"setup": map[string]any{
			"name": st.Name, "site": st.Prefill.Site, "email": st.Prefill.Email,
			"configPath": st.ConfigPath, "keyring": st.Keyring, "envToken": st.EnvToken,
			"tokenURL": config.TokenURL, "autostart": s.autostartInfo(),
		},
	}
}

// addSiteInfo is what the add site form needs, nil when it can't add one.
func addSiteInfo(s *Server) map[string]any {
	a := s.sites.base.AddSite
	if a == nil || s.opt.Demo {
		return nil
	}
	return map[string]any{"keyring": a.Keyring, "envToken": a.EnvToken, "tokenURL": config.TokenURL, "configPath": s.opt.ConfigPath}
}
