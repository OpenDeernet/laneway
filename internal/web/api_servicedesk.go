package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/cornedor/laneway/internal/jira"
)

// Service desk requests: the panel's Request block (an issue of a desk
// project as its request: type, customer, SLAs) and the desks' queues as
// saved lists. On a site without a desk both answer empty after the one
// cached look at the desk list.

func init() {
	get("/issues/{key}/request", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		project, _, _ := strings.Cut(key, "-")
		desk, err := s.Client().IsServiceDesk(ctx, project)
		if err != nil || !desk {
			return map[string]any{"Desk": false, "Request": nil}, err
		}
		// Desk says its comments are internal notes unless for the customer, even when the request can't be read.
		req, _ := s.Client().Request(ctx, key)
		return map[string]any{"Desk": true, "Request": req}, nil
	})
	get("/servicedesk/queues", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		qs, err := s.Client().DeskQueues(ctx)
		if qs == nil {
			qs = []jira.Queue{}
		}
		return qs, err
	})
}
