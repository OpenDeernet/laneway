package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// Dashboards: the person's dashboards and their gadgets, each drawn from
// what laneway-server computes for it; against Jira the gadgets that list
// a filter's issues, the others a link to the dashboard on the site.

func init() {
	get("/dashboards", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().Dashboards(ctx)
	})
	get("/dashboards/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id := r.PathValue("id")
		if _, err := strconv.Atoi(id); err != nil {
			return nil, badRequest(i18n.T("bad dashboard id"))
		}
		gs, err := s.Client().Gadgets(ctx, id)
		if err != nil {
			return nil, err
		}
		type gadget struct {
			jira.Gadget
			Kind string
		}
		out := make([]gadget, len(gs))
		for i, g := range gs {
			out[i] = gadget{g, g.Kind()}
		}
		return map[string]any{"id": id, "gadgets": out}, nil
	})
	get("/dashboards/{id}/gadgets/{gid}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id := r.PathValue("id")
		gid, err := strconv.Atoi(r.PathValue("gid"))
		if _, e := strconv.Atoi(id); e != nil || err != nil {
			return nil, badRequest(i18n.T("bad gadget id"))
		}
		gs, err := s.Client().Gadgets(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, g := range gs {
			if g.ID != gid {
				continue
			}
			data, err := s.Client().GadgetData(ctx, id, g)
			if errors.Is(err, jira.ErrGadgetElsewhere) {
				return map[string]any{"elsewhere": true}, nil
			}
			if err != nil {
				return nil, err
			}
			return json.RawMessage(data), nil
		}
		return nil, httpError{http.StatusNotFound, i18n.T("no such gadget")}
	})
}
