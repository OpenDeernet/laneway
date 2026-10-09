package demo

import (
	"sort"
	"strings"
	"time"
)

// One dashboard, as Jira lists it: four gadgets. Jira keeps a gadget's
// settings out of the REST API but for a few (the filter one's config
// here); the laneway flavour computes what each shows.

const demoDashboard = "10000"

func (s *Server) dashboards() map[string]any {
	return map[string]any{"startAt": 0, "maxResults": 100, "total": 1, "isLast": true, "values": []any{map[string]any{
		"id": demoDashboard, "name": "Team dashboard", "isFavourite": true, "view": s.base + "/jira/dashboards/" + demoDashboard,
		"self": s.base + "/rest/api/3/dashboard/" + demoDashboard, "owner": map[string]any{"accountId": me.id, "displayName": me.name},
	}}}
}

var demoGadgets = []struct {
	id       int
	kind     string
	title    string
	row, col int
}{
	{10001, "assigned-to-me-gadget", "Assigned to me", 0, 0},
	{10002, "filter-results-gadget", "Recently updated", 1, 0},
	{10003, "pie-chart-gadget", "Open by status", 0, 1},
	{10004, "created-vs-resolved-issues-chart-gadget", "Created vs resolved", 1, 1},
}

func (s *Server) gadgets() map[string]any {
	out := []any{}
	for _, g := range demoGadgets {
		out = append(out, map[string]any{"id": g.id, "moduleKey": "com.atlassian.jira.gadgets:" + g.kind, "title": g.title, "color": "blue",
			"position": map[string]any{"row": g.row, "column": g.col}})
	}
	return map[string]any{"gadgets": out}
}

// gadgetData is laneway-server's answer for a gadget, from the demo's
// issues.
func (s *Server) gadgetData(id int) (any, bool) {
	row := func(i *issue) map[string]any {
		o := map[string]any{"key": i.key, "summary": i.summary, "status": i.status.name, "statusCategory": i.status.cat, "issuetype": i.typ, "priority": i.priority}
		if i.assignee != nil {
			o["assignee"] = i.assignee.name
		}
		return o
	}
	all := make([]*issue, 0, len(s.issues))
	for _, i := range s.issues {
		all = append(all, i)
	}
	sort.Slice(all, func(a, b int) bool { return all[a].updated.After(all[b].updated) })
	switch id {
	case 10001, 10002:
		rows, n := []any{}, 0
		for _, i := range all {
			if id == 10001 && (i.assignee == nil || i.assignee.id != me.id || i.status.cat == "done") {
				continue
			}
			n++
			if len(rows) < 10 {
				rows = append(rows, row(i))
			}
		}
		jql := "assignee = currentUser() AND resolution = Unresolved ORDER BY priority DESC, updated DESC"
		if id == 10002 {
			jql = "project = DEMO ORDER BY updated DESC"
		}
		return map[string]any{"jql": jql, "total": n, "issues": rows}, true
	case 10003:
		count := map[string]int{}
		open := 0
		for _, i := range all {
			if i.status.cat != "done" {
				count[i.status.name]++
				open++
			}
		}
		names := make([]string, 0, len(count))
		for n := range count {
			names = append(names, n)
		}
		sort.Slice(names, func(a, b int) bool {
			return count[names[a]] > count[names[b]] || count[names[a]] == count[names[b]] && names[a] < names[b]
		})
		buckets := []any{}
		for _, n := range names {
			buckets = append(buckets, map[string]any{"key": strings.ToLower(n), "label": n, "count": count[n], "percent": count[n] * 100 / max(open, 1)})
		}
		return map[string]any{"jql": "project = DEMO AND resolution = Unresolved", "statType": "status", "total": open, "buckets": buckets}, true
	case 10004:
		day := func(t time.Time) string { return t.Format("2006-01-02") }
		points := []any{}
		for d := 13; d >= 0; d-- {
			at := s.now.AddDate(0, 0, -d)
			c, r := 0, 0
			for _, i := range all {
				if day(i.created) == day(at) {
					c++
				}
				if !i.resolved.IsZero() && day(i.resolved) == day(at) {
					r++
				}
			}
			points = append(points, map[string]any{"start": day(at), "created": c, "resolved": r})
		}
		return map[string]any{"jql": "project = DEMO", "period": "daily", "cumulative": false, "points": points}, true
	}
	return nil, false
}
