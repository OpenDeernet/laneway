package web

import (
	"context"
	"net/http"
)

// The account menu (lib/account.js): on laneway-server the account's own
// sign-in page, and its admin for whoever administers the site or a
// project. Asked when the menu opens, not at start.

func init() {
	get("/account", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		c := s.Client()
		if is, _ := c.Laneway(ctx); !is { // unknown (offline): no server links
			return map[string]any{"Laneway": false}, nil
		}
		out := map[string]any{"Laneway": true, "SignInPage": c.TokenPage()}
		if admin, _ := c.Administers(ctx); admin {
			out["AdminPage"] = c.AdminPage()
		}
		return out, nil
	})
}
