package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// The account menu (lib/account.js): on laneway-server the account's own
// sign-in page, and its admin for whoever administers the site or a
// project. Asked when the menu opens, not at start.
//
// Settings > Account (views/settings_account.js): on laneway-server your
// avatar, how it mails you, your time zone and mail language, and your
// sessions (GET /api/me); on Jira only where Jira keeps your profile.

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
	get("/me", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		c := s.Client()
		if is, _ := c.Laneway(ctx); !is {
			return map[string]any{"Laneway": false, "ProfilePage": c.ProfilePage(ctx)}, nil
		}
		me, err := c.Me(ctx)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"Laneway": true, "Me": me, "SignInPage": c.TokenPage()}
		out["Uploaded"], _ = c.MyAvatarUploaded(ctx)
		out["TimeZone"], _ = c.MyTimeZone(ctx)
		out["Locale"], _ = c.MyLocale(ctx)
		// Mail and sessions an app's sign-in may not reach yet: null, and
		// the row stays away. Both are refused alike: one probe.
		if n, err := c.Notifications(ctx); err == nil {
			out["Notifications"] = n
			if ss, err := c.Sessions(ctx); err == nil {
				out["Sessions"] = ss
			}
		} else if !refused(err) {
			return nil, err
		}
		return out, nil
	})
	put("/me/timezone", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ Value string }](r)
		if err != nil {
			return nil, err
		}
		if _, err := time.LoadLocation(b.Value); err != nil || b.Value == "" {
			return nil, FieldError{"timezone", i18n.T("not a time zone")}
		}
		return nil, s.Client().SetMyTimeZone(ctx, b.Value)
	})
	put("/me/locale", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ Value string }](r)
		if err != nil {
			return nil, err
		}
		return nil, s.Client().SetMyLocale(ctx, b.Value)
	})
	put("/me/notifications", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[jira.Notifications](r)
		if err != nil {
			return nil, err
		}
		return nil, s.Client().SetNotifications(ctx, b)
	})
	// Signs you out everywhere: hosted, or signed in as an app, this
	// laneway too.
	del("/me/sessions", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return nil, s.Client().EndSessions(ctx)
	})
	// POST multipart, field "file": your new picture; answers you as /myself
	// says now (its AvatarURL the new one).
	post("/me/avatar", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		r.Body = http.MaxBytesReader(nil, r.Body, 2<<20)
		f, hdr, err := r.FormFile("file")
		if err != nil {
			return nil, badRequest(i18n.Tf("need a file: %s", err.Error()))
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		typ := hdr.Header.Get("Content-Type")
		if typ == "" {
			typ = http.DetectContentType(data)
		}
		if err := s.Client().SetMyAvatar(ctx, data, typ); err != nil {
			return nil, err
		}
		return s.Client().Me(ctx)
	})
	del("/me/avatar", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		if err := s.Client().RemoveMyAvatar(ctx); err != nil {
			return nil, err
		}
		return s.Client().Me(ctx)
	})
}

// refused is the server turning this sign-in away from a call (an app's
// scopes don't reach it), not the call failing.
func refused(err error) bool {
	return errors.Is(err, jira.ErrUnauthorized) || errors.Is(err, jira.ErrForbidden)
}
