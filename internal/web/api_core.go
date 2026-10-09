package web

import (
	"cmp"
	"context"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/lanes"
	"github.com/cornedor/laneway/internal/ui"
)

func init() {
	get("/session", session)
	get("/projects", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().ListProjects(ctx)
	})
	get("/projects/{project}/boards", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().Boards(ctx, r.PathValue("project"))
	})
	get("/boards/{board}", boardBundle)
	post("/boards/{board}/arrange", arrangeBoard)
	get("/boards/{board}/cards", boardCards)
	get("/issues/{key}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key := r.PathValue("key")
		if !jira.ValidKey(key) {
			return nil, badRequest(i18n.T("bad issue key"))
		}
		if Q(r, "fresh") != "" {
			s.Client().Invalidate(key)
		}
		return s.Client().Get(ctx, key)
	})
	get("/issues/{key}/transitions", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().Transitions(ctx, r.PathValue("key"))
	})
	post("/issues/{key}/transition", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ ID string }](r)
		if err != nil {
			return nil, err
		}
		key := r.PathValue("key")
		err = s.Client().DoTransition(ctx, key, b.ID)
		s.Client().Invalidate(key)
		return nil, err
	})
	post("/issues/{key}/rank", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct {
			Other string
			After bool
		}](r)
		if err != nil {
			return nil, err
		}
		return nil, s.Client().Rank(ctx, r.PathValue("key"), b.Other, b.After)
	})
	get("/prefs", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		out := map[string]string{}
		for k, v := range s.opt.Store.Prefixed("web:") {
			out[strings.TrimPrefix(k, "web:")] = v
		}
		return out, nil
	})
	put("/prefs/{name}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ Value string }](r)
		if err != nil {
			return nil, err
		}
		return nil, s.opt.Store.SetMeta("web:"+r.PathValue("name"), b.Value)
	})
}

// session is what the frontend needs at start: who, where, defaults.
func session(ctx context.Context, s *Server, r *http.Request) (any, error) {
	if s.opt.Setup != nil {
		return setupSession(s), nil
	}
	o := s.opt
	me, _ := s.Client().Myself(ctx)
	return map[string]any{
		"site": o.Site, "sites": o.Sites, "defaultName": o.DefaultName, "demo": o.Demo, "version": o.Version,
		"baseURL": s.Client().BaseURL(), "me": me,
		"projects": s.projects(), "ui": s.UIConfig(), "autostart": s.autostartInfo(), "pins": fieldPins(s),
		"repos":   slices.Sorted(maps.Keys(o.Jira.Repos)), // projects with a checkout: draft PR, removing a worktree
		"addSite": addSiteInfo(s), "home": homeInfo(s), "hosted": o.Hosted,
	}, nil
}

func boardID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(r.PathValue("board"))
	if err != nil {
		return 0, badRequest(i18n.T("bad board id"))
	}
	return id, nil
}

// boardQuick is the board's quick filters: ui.quick_filters, then the
// board's own unless ui.board_quick_filters is off.
func boardQuick(ctx context.Context, s *Server, id int) []jira.QuickFilter {
	quick := localQuick(s.UIConfig().QuickFilters)
	if !strings.EqualFold(strings.TrimSpace(s.UIConfig().BoardQuickFilters), "off") {
		qf, _ := s.Client().QuickFilters(ctx, id)
		quick = append(quick, qf...)
	}
	return quick
}

// boardBundle is everything a board view needs besides its cards: columns,
// quick filters, sprints (open and future; closed with ?closed=1).
func boardBundle(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := boardID(r)
	if err != nil {
		return nil, err
	}
	c := s.Client()
	cfg, err := c.BoardConfiguration(ctx, id)
	if err != nil {
		return nil, err
	}
	names, _ := c.StatusNames(ctx)
	out := map[string]any{"id": id, "config": cfg, "layouts": boardLayouts(s.UIConfig().LaneLayouts, lanes.Site(c.BaseURL()), id, cfg.Columns, names)}
	out["quickFilters"] = boardQuick(ctx, s, id)
	if sp, err := c.Sprints(ctx, id); err == nil {
		out["sprints"] = sp
	}
	if Q(r, "closed") != "" {
		if sp, err := c.ClosedSprints(ctx, id); err == nil {
			out["closedSprints"] = sp
		}
	}
	if names != nil {
		out["statusNames"] = names
	}
	return out, nil
}

// layoutLane is a lane of a lane layout shaped as a board column, so the
// board draws it as one, with the columns stacked in it.
type layoutLane struct {
	Name      string
	StatusIDs []string
	Max       int
	Sections  []lanes.Section
}

// boardLayout is a ui.lane_layouts entry arranged over a board's columns,
// with the statuses it hides.
type boardLayout struct {
	Name   string
	Lanes  []layoutLane
	Hidden []string
}

// boardLayouts are the lane layouts fitting board of site, arranged as the
// terminal does (internal/lanes).
func boardLayouts(ls []config.LaneLayout, site string, board int, cols []jira.Column, names map[string]string) []boardLayout {
	out := []boardLayout{}
	for _, l := range lanes.Fitting(ls, site, board, cols) {
		arranged, hidden := lanes.Arrange(l, cols, names)
		b := boardLayout{Name: l.Name, Hidden: append([]string{}, hidden...)}
		for _, a := range arranged {
			b.Lanes = append(b.Lanes, layoutLane{Name: a.Name, StatusIDs: a.StatusIDs(), Max: a.Max, Sections: a.Sections})
		}
		out = append(out, b)
	}
	return out
}

// arrangeBoard is a lane layout over the board's columns, for the
// settings' lane editor: the body's Layout (as ui.lane_layouts has it), with
// the editor's Draft of it applied when given, and this site's when it has
// none. It answers the columns, status names, the layout to write, its draft
// over the board, and whether it fits the board.
func arrangeBoard(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := boardID(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[struct {
		Layout config.LaneLayout
		Draft  *lanes.Draft
	}](r)
	if err != nil {
		return nil, err
	}
	c := s.Client()
	cfg, err := c.BoardConfiguration(ctx, id)
	if err != nil {
		return nil, err
	}
	l, site := b.Layout, lanes.Site(c.BaseURL())
	if b.Draft != nil {
		l = b.Draft.Apply(l, cfg.Columns)
		l.Site = cmp.Or(l.Site, site)
	}
	names, _ := c.StatusNames(ctx)
	return map[string]any{"Columns": cfg.Columns, "StatusNames": names, "Layout": l, "Draft": lanes.NewDraft(l, cfg.Columns),
		"Fits": lanes.Fits(l, site, id, cfg.Columns)}, nil
}

// localQuick are ui.quick_filters, shown before every board's own, as the
// TUI's (ids -1, -2, …); one without a name or JQL is left out.
func localQuick(qs []config.QuickFilter) []jira.QuickFilter {
	out := []jira.QuickFilter{}
	for _, q := range qs {
		if strings.TrimSpace(q.Name) != "" && strings.TrimSpace(q.JQL) != "" {
			out = append(out, jira.QuickFilter{ID: -1 - len(out), Name: q.Name, JQL: q.JQL})
		}
	}
	return out
}

// boardCards: ?sprint=ID → that sprint, ?backlog=1 → backlog, ?kanban=1 →
// a kanban board as Jira shows it (old done work and its backlog column
// left out), else the board; ?jql= narrows. The response is {cards, total}.
func boardCards(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := boardID(r)
	if err != nil {
		return nil, err
	}
	c, jql, pf := s.Client(), Q(r, "jql"), Q(r, "points")
	var cfg *jira.BoardConfig
	if pf == "" || Q(r, "kanban") != "" {
		if bc, e := c.BoardConfiguration(ctx, id); e == nil {
			cfg = bc
			pf = cmp.Or(pf, bc.PointsField)
		}
	}
	var cards []jira.Card
	var total int
	switch {
	case Q(r, "sprint") != "":
		sp, e := strconv.Atoi(Q(r, "sprint"))
		if e != nil {
			return nil, badRequest(i18n.T("bad sprint id"))
		}
		cards, total, err = c.SprintIssues(ctx, id, sp, jql, pf)
	case Q(r, "backlog") != "":
		cards, total, err = c.BacklogIssues(ctx, id, jql, pf)
	case Q(r, "kanban") != "":
		cards, total, err = ui.KanbanIssues(ctx, c, id, cfg, s.UIConfig().KanbanDoneDays, jql, pf)
	default:
		cards, total, err = c.BoardIssues(ctx, id, jql, pf)
	}
	if err != nil {
		return nil, err
	}
	observeRules(s, r, cards)
	return map[string]any{"cards": cards, "total": total}, nil
}
