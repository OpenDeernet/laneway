package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// Settings > Jira site > API tokens and apps: on laneway-server the
// account's own tokens and the apps it signed in with, to revoke, and a
// token made; on Jira only the way to Atlassian's token page.

func init() {
	get("/site/tokens", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		c := s.Client()
		is, err := c.Laneway(ctx)
		if err != nil {
			return nil, err
		}
		if !is {
			return map[string]any{"Laneway": false, "TokenURL": config.TokenURL}, nil
		}
		toks, err := c.APITokens(ctx)
		if err != nil {
			return nil, err
		}
		apps, err := c.AppGrants(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"Laneway": true, "Page": c.TokenPage(), "MakesTokens": c.MakesTokens(), "Tokens": toks, "Apps": apps}, nil
	})
	// A token made: its secret, shown once; Page instead when this sign-in
	// (an app's) can't make one.
	post("/site/tokens", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct {
			Label string
			Days  int
		}](r)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(b.Label) == "" {
			return nil, FieldError{"label", i18n.T("give it a label")}
		}
		if b.Days < 0 {
			return nil, FieldError{"days", i18n.T("days is 1 or more, or empty for the site's default")}
		}
		tok, secret, err := s.Client().CreateAPIToken(ctx, strings.TrimSpace(b.Label), b.Days, nil)
		if errors.Is(err, jira.ErrTokenPage) {
			return map[string]any{"Page": s.Client().TokenPage()}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"Token": tok, "Secret": secret}, nil
	})
	del("/site/tokens/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return nil, s.Client().RevokeAPIToken(ctx, r.PathValue("id"))
	})
	del("/site/tokens", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return nil, s.Client().RevokeAPITokens(ctx)
	})
	del("/site/apps/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return nil, s.Client().RevokeAppGrant(ctx, r.PathValue("id"))
	})
}
