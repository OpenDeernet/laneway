package demo

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/jira"
)

// demoClient is the app's Jira client on a fresh demo.
func demoClient(t *testing.T) (*jira.Client, *Server) {
	t.Helper()
	s := New(time.Now())
	url, stop, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return jira.New(jira.Config{BaseURL: url, Email: "demo@example.com", APIToken: "demo", Projects: []string{project}}), s
}

// TestDemoBoard: the board, its sprints and a sprint's cards read as from
// Jira, with nothing unanswered.
func TestDemoBoard(t *testing.T) {
	c, s := demoClient(t)
	ctx := context.Background()
	boards, err := c.Boards(ctx, project)
	if err != nil || len(boards) != 1 {
		t.Fatalf("boards = %v, %v", boards, err)
	}
	cfg, err := c.BoardConfiguration(ctx, boards[0].ID)
	if err != nil || len(cfg.Columns) != 4 {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	sprints, err := c.Sprints(ctx, boards[0].ID)
	if err != nil || len(sprints) != 2 || sprints[0].State != "active" {
		t.Fatalf("sprints = %+v, %v", sprints, err)
	}
	cards, _, err := c.SprintIssues(ctx, boards[0].ID, sprints[0].ID, "", pointsField)
	if err != nil || len(cards) != 10 {
		t.Fatalf("sprint cards = %d, %v", len(cards), err)
	}
	mine, _, err := c.SprintIssues(ctx, boards[0].ID, sprints[0].ID, "assignee = currentUser()", pointsField)
	if err != nil || len(mine) != 3 {
		t.Errorf("my sprint cards = %d, %v", len(mine), err)
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unanswered: %v", s.Unhandled)
	}
}

// TestDemoWrites: a move, a comment and a worklog stay, in memory.
func TestDemoWrites(t *testing.T) {
	c, s := demoClient(t)
	ctx := context.Background()
	trs, err := c.Transitions(ctx, "DEMO-5")
	if err != nil || len(trs) == 0 {
		t.Fatalf("transitions = %v, %v", trs, err)
	}
	var toDone string
	for _, tr := range trs {
		if tr.Name == "Done" {
			toDone = tr.ID
		}
	}
	if err := c.TransitionWith(ctx, "DEMO-5", toDone, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.AddComment(ctx, "DEMO-5", "shipped", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.AddWorklog(ctx, "DEMO-5", 1800, time.Now(), "", ""); err != nil {
		t.Fatal(err)
	}
	iss, err := c.Get(ctx, "DEMO-5")
	if err != nil {
		t.Fatal(err)
	}
	if iss.Status != "Done" || len(iss.Comments) != 1 || iss.Comments[0].Body != "shipped" {
		t.Errorf("DEMO-5 = %s, %+v", iss.Status, iss.Comments)
	}
	if wl, err := c.IssueWorklogs(ctx, "DEMO-5"); err != nil || len(wl) != 1 {
		t.Errorf("worklogs = %v, %v", wl, err)
	}
	key, err := c.CreateIssue(ctx, jira.NewIssue{Project: project, Type: "Task", Summary: "Try the demo"})
	if err != nil || s.issues[key] == nil || s.issues[key].summary != "Try the demo" {
		t.Errorf("created %q, %v", key, err)
	}
}

// TestDemoBulkWorklogs: worklogs move to another issue and go, several
// at once.
func TestDemoBulkWorklogs(t *testing.T) {
	c, _ := demoClient(t)
	ctx := context.Background()
	for range 3 {
		if err := c.AddWorklog(ctx, "DEMO-5", 1800, time.Now(), "", ""); err != nil {
			t.Fatal(err)
		}
	}
	wl, err := c.IssueWorklogs(ctx, "DEMO-5")
	if err != nil || len(wl) != 3 {
		t.Fatalf("worklogs = %v, %v", wl, err)
	}
	if err := c.MoveWorklogs(ctx, "DEMO-5", []string{wl[0].ID, wl[1].ID}, "DEMO-6"); err != nil {
		t.Fatal(err)
	}
	moved, _ := c.IssueWorklogs(ctx, "DEMO-6")
	if left, _ := c.IssueWorklogs(ctx, "DEMO-5"); len(left) != 1 || !slices.ContainsFunc(moved, func(w jira.Worklog) bool { return w.ID == wl[0].ID }) {
		t.Errorf("left %v, moved %v", left, moved)
	}
	if err := c.DeleteWorklogs(ctx, "DEMO-6", []string{wl[0].ID, wl[1].ID}); err != nil {
		t.Fatal(err)
	}
	if after, _ := c.IssueWorklogs(ctx, "DEMO-6"); len(after) != len(moved)-2 {
		t.Errorf("after the delete %v", after)
	}
}

func TestDemoJQL(t *testing.T) {
	s := New(time.Now())
	for jql, want := range map[string]int{
		"issuetype = Epic":   3,
		"parent in (DEMO-4)": 3,
		`text ~ "invoice*"`:  4,
		"sprint = 8":         len(s.search("sprint = 8")),
		"statusCategory != Done AND labels = backend":           4,
		`key in (DEMO-1, DEMO-2) OR (assignee = currentUser())`: 7,
	} {
		if got := len(s.search(jql)); got != want || got == 0 {
			t.Errorf("%s: %d issues, want %d", jql, got, want)
		}
	}
	count := func(keep func(*issue) bool) (n int) {
		for _, i := range s.search("") {
			if keep(i) {
				n++
			}
		}
		return n
	}
	for jql, want := range map[string]int{
		"priority = High":                   count(func(i *issue) bool { return i.priority == "High" }),
		`priority in (Highest, "High")`:     count(func(i *issue) bool { return i.priority == "Highest" || i.priority == "High" }),
		"priority not in (Medium)":          count(func(i *issue) bool { return i.priority != "Medium" }),
		"priority > Medium":                 count(func(i *issue) bool { return i.priority == "Highest" || i.priority == "High" }),
		"priority <= Medium":                count(func(i *issue) bool { return i.priority == "Medium" || i.priority == "Low" || i.priority == "Lowest" }),
		`reporter = "Mira Jansen"`:          len(s.search("")),
		"reporter != currentUser()":         len(s.search("")),
		"priority = High AND reporter = me": 0,
	} {
		if got := len(s.search(jql)); got != want {
			t.Errorf("%s: %d issues, want %d", jql, got, want)
		}
	}
	if n := len(s.search("priority = High")); n == 0 || n == len(s.search("")) {
		t.Errorf("priority = High keeps %d of %d", n, len(s.search("")))
	}
}

// TestDemoReleases: V reads the versions with their progress, a release's
// issues by fixVersion, and releasing one sticks.
func TestDemoReleases(t *testing.T) {
	c, s := demoClient(t)
	ctx := context.Background()
	vs, err := c.Versions(ctx, project)
	if err != nil || len(vs) != 3 || vs[0].Name != "2.5" {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
	cur := vs[1]
	if cur.Name != "2.4" || cur.Released || cur.Total != 5 || cur.Done != 0 {
		t.Errorf("2.4 = %+v", cur)
	}
	if got := s.search("fixVersion = " + cur.ID); len(got) != 5 {
		t.Errorf("fixVersion = %s: %d issues, want 5", cur.ID, len(got))
	}
	if err := c.ReleaseVersion(ctx, cur.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if vs, _ = c.Versions(ctx, project); !vs[1].Released {
		t.Errorf("2.4 after releasing: %+v", vs[1])
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unanswered: %v", s.Unhandled)
	}
}

// TestDemoJQLCompletion: Q completes the demo's fields, functions and a
// field's values.
func TestDemoJQLCompletion(t *testing.T) {
	c, _ := demoClient(t)
	ctx := context.Background()
	w, err := c.JQLAutocomplete(ctx)
	if err != nil || !slices.Contains(w.Fields, "assignee") || !slices.Contains(w.Functions, "currentUser()") {
		t.Fatalf("words = %+v, %v", w, err)
	}
	vals, err := c.JQLValues(ctx, "assignee", "pri")
	if err != nil || len(vals) != 1 || vals[0] != "Priya Nair" {
		t.Errorf("assignee values = %v, %v", vals, err)
	}
	if vals, _ = c.JQLValues(ctx, "labels", ""); !slices.Contains(vals, "frontend") {
		t.Errorf("labels = %v", vals)
	}
}

func TestDemoLinks(t *testing.T) {
	c, s := demoClient(t)
	ctx := context.Background()
	types, err := c.LinkTypes(ctx)
	if err != nil || len(types) < 3 {
		t.Fatalf("types = %v, %v", types, err)
	}
	if err := c.LinkIssues(ctx, "Blocks", "DEMO-1", "DEMO-2"); err != nil {
		t.Fatal(err)
	}
	rel := func(key string) (string, string) {
		iss, err := c.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range iss.Links {
			if l.Key == "DEMO-1" || l.Key == "DEMO-2" {
				return l.Rel, l.Key
			}
		}
		return "", ""
	}
	if r, k := rel("DEMO-1"); r != "blocks" || k != "DEMO-2" {
		t.Errorf("DEMO-1: %q %q", r, k)
	}
	if r, k := rel("DEMO-2"); r != "is blocked by" || k != "DEMO-1" {
		t.Errorf("DEMO-2: %q %q", r, k)
	}
	if len(s.Unhandled) != 0 {
		t.Errorf("unhandled %v", s.Unhandled)
	}
	iss, _ := c.Get(ctx, "DEMO-1")
	for _, l := range iss.Links {
		if l.Key == "DEMO-2" {
			if err := c.DeleteLink(ctx, "DEMO-1", l.LinkID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if r, _ := rel("DEMO-1"); r != "" {
		t.Errorf("link kept: %q", r)
	}
}

// TestDemoConfluence: DEMO-4 links a Confluence page on the same site,
// which reads as markdown.
func TestDemoConfluence(t *testing.T) {
	c, s := demoClient(t)
	ctx := context.Background()
	links, err := c.WebLinks(ctx, pageIssue)
	if err != nil || len(links) != 1 || links[0].App != "Confluence" {
		t.Fatalf("links %+v, %v", links, err)
	}
	id := c.PageID(links[0].URL)
	p, err := c.ConfluencePage(ctx, id)
	if err != nil || id != pageID || p.Title != pageTitle || !strings.Contains(p.Markdown, "## Flow") || !strings.HasPrefix(p.Markdown, "- Goal\n- Flow") || !strings.Contains(p.Markdown, "](confluence:"+pageImageID+")") {
		t.Errorf("page %q %+v, %v", id, p, err)
	}
	if b, err := c.PageImage(ctx, pageImageID); err != nil || !strings.HasPrefix(string(b), "\x89PNG") {
		t.Errorf("page image: %d bytes, %v", len(b), err)
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unanswered: %v", s.Unhandled)
	}
}

// TestDemoInbox: others' changes and comments on your issues fill the
// inbox, a mention of you among them.
func TestDemoInbox(t *testing.T) {
	c, _ := demoClient(t)
	ctx := context.Background()
	iss, err := c.InboxIssues(ctx, time.Now().AddDate(0, 0, -7))
	if err != nil || len(iss) < 3 {
		t.Fatalf("inbox issues %d, %v", len(iss), err)
	}
	entries, err := c.IssueInbox(ctx, "DEMO-9", "", time.Now().AddDate(0, 0, -7))
	if err != nil {
		t.Fatal(err)
	}
	var mention, priority bool
	for _, e := range entries {
		mention = mention || e.Mention && e.Who == sam.name
		priority = priority || strings.Contains(e.What, "Highest") && e.Who == mira.name
	}
	if !mention || !priority {
		t.Errorf("DEMO-9's inbox: mention %v, priority %v in %+v", mention, priority, entries)
	}
}

// TestDemoSearchPages: search/jql answers maxResults at a time, as Jira does:
// the palette's find gets its 25, a search pages to every match.
func TestDemoSearchPages(t *testing.T) {
	c, s := demoClient(t)
	s.Bulk(120)
	ctx := context.Background()
	found, err := c.FindIssues(ctx, "filler", 25)
	if err != nil || len(found) != 25 {
		t.Fatalf("find: %d cards, %v; want 25", len(found), err)
	}
	all, err := c.SearchCards(ctx, `text ~ "filler*"`)
	if err != nil || len(all) != 120 {
		t.Fatalf("search: %d cards, %v; want 120", len(all), err)
	}
}
