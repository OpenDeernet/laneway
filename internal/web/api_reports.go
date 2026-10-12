package web

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cornedor/laneway/internal/calendar"
	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// Planning, reports and roadmap. Aggregated where a view would otherwise
// need several round trips.

func init() {
	get("/plan/{board}", planBundle)
	post("/plan/move", planMove)
	post("/plan/rank", planRank)
	post("/plan/sprints", planCreateSprint)
	post("/plan/sprints/{id}/start", planStartSprint)
	post("/plan/sprints/{id}/close", planCloseSprint)
	post("/plan/sprints/{id}/update", planUpdateSprint)

	get("/reports/sprint/{board}", reportSprint)
	get("/reports/velocity/{board}", reportVelocity)
	get("/reports/retro/{board}", reportRetro)
	get("/reports/cycle/{project}", reportCycle)
	get("/reports/versions/{project}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().Versions(ctx, r.PathValue("project"))
	})
	post("/reports/versions/{id}/release", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ Day string }](r)
		if err != nil {
			return nil, err
		}
		day := time.Now()
		if b.Day != "" {
			if day, err = time.ParseInLocation(time.DateOnly, b.Day, time.Local); err != nil {
				return nil, badRequest(i18n.T("bad day"))
			}
		}
		return nil, s.Client().ReleaseVersion(ctx, r.PathValue("id"), day)
	})

	get("/roadmap/{project}", roadmap)
	post("/roadmap/{key}/dates", roadmapDates)
}

type planSprint struct {
	jira.Sprint
	Cards []jira.Card
	Total int
	// Mine is your capacity less your meetings in ui.calendar, with
	// ui.capacity for you and the sprint's dates set.
	Mine *planMine `json:",omitempty"`
}

// planMine is your capacity in a sprint: Capacity points after Hours of
// meetings.
type planMine struct {
	Name     string
	Capacity float64
	Hours    float64
}

// planBundle is the planning view in one call: the board's open and future
// sprints with their cards, and the backlog, narrowed by ?jql= (the quick
// filters on); and the quick filters.
func planBundle(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := boardID(r)
	if err != nil {
		return nil, err
	}
	c := s.Client()
	cfg, err := c.BoardConfiguration(ctx, id)
	if err != nil {
		return nil, err
	}
	sprints, err := c.Sprints(ctx, id)
	if err != nil {
		return nil, err
	}
	pf, jql := cfg.PointsField, Q(r, "jql")
	out := make([]planSprint, len(sprints))
	errs := make([]error, len(sprints)+1)
	var backlog []jira.Card
	var btotal int
	var wg sync.WaitGroup
	for i, sp := range sprints {
		out[i].Sprint = sp
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].Cards, out[i].Total, errs[i] = c.SprintIssues(ctx, id, sp.ID, jql, pf)
		}()
	}
	var quick []jira.QuickFilter
	wg.Add(2)
	go func() {
		defer wg.Done()
		backlog, btotal, errs[len(sprints)] = c.BacklogIssues(ctx, id, jql, pf)
	}()
	go func() { defer wg.Done(); quick = boardQuick(ctx, s, id) }()
	// Your meetings over every dated sprint, read once.
	var me jira.User
	var meetings []calendar.Meeting
	var calErr error
	ui := s.UIConfig()
	var from, to time.Time
	for _, sp := range sprints {
		if !sp.Start.IsZero() && sp.End.After(sp.Start) {
			if from.IsZero() || sp.Start.Before(from) {
				from = sp.Start
			}
			to = maxT(to, sp.End)
		}
	}
	if cal := strings.TrimSpace(ui.Calendar); cal != "" && len(ui.Capacity) > 0 && !from.IsZero() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if me, calErr = c.Myself(ctx); calErr == nil {
				meetings, calErr = calendar.Read(ctx, cal, from, to)
			}
		}()
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	for i, sp := range sprints {
		if calErr != nil || me.DisplayName == "" || sp.Start.IsZero() || !sp.End.After(sp.Start) {
			continue
		}
		if left, busy, ok := calendar.Yours(ui.Capacity, me.DisplayName, meetings, sp.Start, sp.End, workdays(s)); ok {
			out[i].Mine = &planMine{Name: me.DisplayName, Capacity: left, Hours: busy.Hours()}
		}
	}
	res := map[string]any{
		"Sprints": out, "Backlog": map[string]any{"Cards": backlog, "Total": btotal},
		"PointsField": pf, "Columns": cfg.Columns, "QuickFilters": quick,
	}
	if calErr != nil {
		res["Calendar"] = calErr.Error()
	}
	return res, nil
}

func maxT(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// planMove puts keys in a sprint, or in the backlog when Sprint is 0.
func planMove(ctx context.Context, s *Server, r *http.Request) (any, error) {
	b, err := Body[struct {
		Keys   []string
		Sprint int
	}](r)
	if err != nil {
		return nil, err
	}
	if len(b.Keys) == 0 {
		return nil, badRequest(i18n.T("no issues"))
	}
	for _, k := range b.Keys {
		if !jira.ValidKey(k) {
			return nil, badRequest(i18n.T("bad issue key"))
		}
	}
	if b.Sprint == 0 {
		return nil, s.Client().MoveToBacklog(ctx, b.Keys...)
	}
	return nil, s.Client().MoveToSprint(ctx, b.Sprint, b.Keys...)
}

// planRank puts Keys, in their order, just before (After false) or after Other.
func planRank(ctx context.Context, s *Server, r *http.Request) (any, error) {
	b, err := Body[struct {
		Keys  []string
		Other string
		After bool
	}](r)
	if err != nil {
		return nil, err
	}
	if len(b.Keys) == 0 {
		return nil, badRequest(i18n.T("no issues"))
	}
	for _, k := range b.Keys {
		if !jira.ValidKey(k) {
			return nil, badRequest(i18n.T("bad issue key"))
		}
	}
	if !jira.ValidKey(b.Other) {
		return nil, badRequest(i18n.T("bad issue key"))
	}
	return nil, s.Client().RankKeys(ctx, b.Keys, b.Other, b.After)
}

func planCreateSprint(ctx context.Context, s *Server, r *http.Request) (any, error) {
	b, err := Body[struct {
		Board int
		Name  string
	}](r)
	if err != nil {
		return nil, err
	}
	if b.Name == "" || b.Board == 0 {
		return nil, badRequest(i18n.T("a sprint needs a board and a name"))
	}
	return nil, s.Client().CreateSprint(ctx, b.Board, b.Name)
}

func sprintID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return 0, badRequest(i18n.T("bad sprint id"))
	}
	return id, nil
}

// planStartSprint starts a sprint now; End is a day ("2006-01-02"), ending at 17:00.
func planStartSprint(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := sprintID(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[struct{ End string }](r)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	end, err := endOfDay(b.End)
	if err != nil || !end.After(now) {
		return nil, badRequest(i18n.T("the end must be a day after today"))
	}
	return nil, s.Client().StartSprint(ctx, id, now, end)
}

func endOfDay(day string) (time.Time, error) {
	d, err := time.ParseInLocation(time.DateOnly, day, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 17, 0, 0, 0, time.Local), nil
}

// planUpdateSprint renames, moves the end and/or sets the goal (Goal nil leaves it).
func planUpdateSprint(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := sprintID(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[struct {
		Name, End string
		Goal      *string
	}](r)
	if err != nil {
		return nil, err
	}
	var end time.Time
	if b.End != "" {
		if end, err = endOfDay(b.End); err != nil {
			return nil, badRequest(i18n.T("bad end day"))
		}
	}
	c := s.Client()
	if b.Name != "" || !end.IsZero() {
		if err := c.UpdateSprint(ctx, id, b.Name, end); err != nil {
			return nil, err
		}
	}
	if b.Goal != nil {
		return nil, c.SetSprintGoal(ctx, id, *b.Goal)
	}
	return nil, nil
}

// planCloseSprint moves the sprint's unfinished issues (not in the board's
// last column) to sprint MoveTo, or the backlog when 0, then closes it.
func planCloseSprint(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := sprintID(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[struct{ Board, MoveTo int }](r)
	if err != nil {
		return nil, err
	}
	c := s.Client()
	cfg, err := c.BoardConfiguration(ctx, b.Board)
	if err != nil {
		return nil, err
	}
	var done []string
	if n := len(cfg.Columns); n > 0 {
		done = cfg.Columns[n-1].StatusIDs
	}
	moved := 0
	for round := 0; round < 20; round++ {
		cards, _, err := c.SprintIssues(ctx, b.Board, id, "", cfg.PointsField)
		if err != nil {
			return nil, err
		}
		var open []string
		for _, cd := range cards {
			if !slices.Contains(done, cd.StatusID) && !cd.Done {
				open = append(open, cd.Key)
			}
		}
		if len(open) == 0 {
			break
		}
		if b.MoveTo != 0 {
			err = c.MoveToSprint(ctx, b.MoveTo, open...)
		} else {
			err = c.MoveToBacklog(ctx, open...)
		}
		if err != nil {
			return nil, err
		}
		moved += len(open)
	}
	if err := c.CloseSprint(ctx, id); err != nil {
		return nil, err
	}
	return map[string]int{"Moved": moved}, nil
}

// reportSprint is what the burndown, burnup and flow charts draw: the sprint
// (?sprint=, else the active one, else the last closed), its issues and the
// board's columns.
func reportSprint(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := boardID(r)
	if err != nil {
		return nil, err
	}
	c := s.Client()
	cfg, err := c.BoardConfiguration(ctx, id)
	if err != nil {
		return nil, err
	}
	all, err := c.Sprints(ctx, id)
	if err != nil {
		return nil, err
	}
	closed, _ := c.ClosedSprints(ctx, id)
	all = append(all, closed...)
	var sp *jira.Sprint
	if q := Q(r, "sprint"); q != "" {
		n, _ := strconv.Atoi(q)
		if i := slices.IndexFunc(all, func(x jira.Sprint) bool { return x.ID == n }); i >= 0 {
			sp = &all[i]
		}
	} else if i := slices.IndexFunc(all, func(x jira.Sprint) bool { return x.State == "active" }); i >= 0 {
		sp = &all[i]
	} else if len(closed) > 0 {
		sp = &closed[0]
	}
	out := map[string]any{"Sprints": all, "Columns": cfg.Columns, "PointsField": cfg.PointsField}
	if sp == nil {
		return out, nil
	}
	issues, err := c.SprintBurn(ctx, sp.ID, cfg.PointsField)
	if err != nil {
		return nil, err
	}
	out["Sprint"], out["Issues"] = sp, issues
	return out, nil
}

func sprintCount(r *http.Request, def int) int {
	if n, err := strconv.Atoi(Q(r, "n")); err == nil && n > 0 && n <= 50 {
		return n
	}
	return def
}

// velocitySprints is ui.velocity_sprints, 8 when unset or out of range.
func velocitySprints(ui config.UIConfig) int {
	if n := ui.VelocitySprints; n >= 1 && n <= 50 {
		return n
	}
	return 8
}

func boardPoints(ctx context.Context, s *Server, board int) string {
	if cfg, err := s.Client().BoardConfiguration(ctx, board); err == nil {
		return cfg.PointsField
	}
	return ""
}

func reportVelocity(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := boardID(r)
	if err != nil {
		return nil, err
	}
	n := sprintCount(r, velocitySprints(s.UIConfig()))
	return s.Client().Velocity(ctx, id, n, boardPoints(ctx, s, id))
}

func reportRetro(ctx context.Context, s *Server, r *http.Request) (any, error) {
	id, err := boardID(r)
	if err != nil {
		return nil, err
	}
	done, compare, err := reportLines(ctx, s, r, id)
	if err != nil {
		return nil, err
	}
	rs, err := s.Client().Retro(ctx, id, sprintCount(r, 2), boardPoints(ctx, s, id), done, compare)
	if err != nil {
		return nil, err
	}
	// A retro lists keys; a reader wants summaries.
	var keys []string
	for _, sp := range rs {
		for _, list := range [][]string{sp.Done, sp.Carried, sp.Added, sp.Back, sp.Between} {
			for _, k := range list {
				if !slices.Contains(keys, k) && jira.ValidKey(k) {
					keys = append(keys, k)
				}
			}
		}
	}
	cards := map[string]jira.Card{}
	for chunk := range slices.Chunk(keys, 100) {
		found, err := s.Client().SearchCards(ctx, "key in ("+strings.Join(chunk, ",")+")")
		if err != nil {
			break
		}
		for _, cd := range found {
			cards[cd.Key] = cd
		}
	}
	return map[string]any{"Sprints": rs, "Cards": cards}, nil
}

func reportCycle(ctx context.Context, s *Server, r *http.Request) (any, error) {
	weeks := 12
	if n, err := strconv.Atoi(Q(r, "weeks")); err == nil && n > 0 && n <= 52 {
		weeks = n
	}
	board, _ := strconv.Atoi(Q(r, "board"))
	done, compare, err := reportLines(ctx, s, r, board)
	if err != nil {
		return nil, err
	}
	return s.Client().CycleTimes(ctx, r.PathValue("project"), weeks, done, compare)
}

// reportLines are the lines a chart counts by: ?done= (a column of the
// board; none for Jira's resolution) and ?compare=, a second one or none.
func reportLines(ctx context.Context, s *Server, r *http.Request, board int) (done, compare *jira.Line, err error) {
	dn, cmp := Q(r, "done"), Q(r, "compare")
	if dn == "" && cmp == "" || board == 0 {
		return nil, nil, nil
	}
	cfg, err := s.Client().BoardConfiguration(ctx, board)
	if err != nil {
		return nil, nil, err
	}
	first := s.UIConfig().ReportBackwards == "first"
	return jira.NewLine(cfg.Columns, dn, first), jira.NewLine(cfg.Columns, cmp, first), nil
}

func roadmap(ctx context.Context, s *Server, r *http.Request) (any, error) {
	typ, days := s.UIConfig().RoadmapEpicType, s.UIConfig().RoadmapDoneDays
	if typ == "" {
		typ = "Epic"
	}
	if days <= 0 {
		days = 90
	}
	c := s.Client()
	epics, err := c.Roadmap(ctx, r.PathValue("project"), typ, days)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Epics": epics, "CanSetStart": c.CanSetStart(ctx)}, nil
}

// roadmapDates sets an epic's dates ("2006-01-02", "" clears).
func roadmapDates(ctx context.Context, s *Server, r *http.Request) (any, error) {
	b, err := Body[struct{ Start, End string }](r)
	if err != nil {
		return nil, err
	}
	key := r.PathValue("key")
	if !jira.ValidKey(key) {
		return nil, badRequest(i18n.T("bad issue key"))
	}
	parse := func(v string) (time.Time, error) {
		if v == "" {
			return time.Time{}, nil
		}
		return time.ParseInLocation(time.DateOnly, v, time.Local)
	}
	start, err1 := parse(b.Start)
	end, err2 := parse(b.End)
	if err1 != nil || err2 != nil {
		return nil, badRequest(i18n.T("bad date"))
	}
	return nil, s.Client().SetDates(ctx, key, start, end)
}
