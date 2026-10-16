package ui

import (
	"charm.land/lipgloss/v2"
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/rules"
)

// jiraTabModel sits on the Jira tab with a loaded scrum board: an active
// sprint with three lanes, then the backlog.
func jiraTabModel(t *testing.T) Model {
	t.Helper()
	m := configuredJiraModel(t, "ABC")
	m.width, m.height = 160, 40
	m.resize()
	out, _ := m.handleJiraBoard(jiraBoardMsg{
		seq: m.jiraTab.seq, project: "ABC",
		boards: []jira.Board{{ID: 1, Name: "ABC board", Type: "scrum"}},
		cfg: &jira.BoardConfig{Columns: []jira.Column{
			{Name: "To do", StatusIDs: []string{"1"}},
			{Name: "In progress", StatusIDs: []string{"3"}},
			{Name: "Done", StatusIDs: []string{"5", "6"}},
		}},
		views: []jiraView{
			{kind: jiraViewSprint, name: "Sprint 1", sprint: 9, lanes: true},
			{kind: jiraViewBacklog, name: "Backlog"},
		},
		quick: []jira.QuickFilter{{ID: 7, Name: "FE", JQL: "labels = frontend"}, {ID: 8, Name: "BE", JQL: "labels = backend"}},
		cards: []jira.Card{
			{Key: "ABC-1", Summary: "First", StatusID: "1", Status: "New", Assignee: "Ada", AssigneeID: "a1"},
			{Key: "ABC-2", Summary: "Second", StatusID: "3", Status: "In progress"},
			{Key: "ABC-3", Summary: "Third", StatusID: "1", Status: "New", Points: "5"},
			{Key: "ABC-4", Summary: "Fourth", StatusID: "6", Status: "Closed"},
		},
		total: 4,
	})
	return out.(Model)
}

func TestJiraTabLanes(t *testing.T) {
	m := jiraTabModel(t)
	if !m.jiraShowsLanes() {
		t.Fatal("active sprint should show as lanes")
	}
	var got []int
	for _, l := range m.jiraTab.lanes {
		got = append(got, len(l.cards))
	}
	if len(got) != 3 || got[0] != 2 || got[1] != 1 || got[2] != 1 {
		t.Fatalf("lane sizes = %v, want [2 1 1]", got)
	}
	view := m.View().Content
	for _, want := range []string{"To do 2", "In progress 1", "ABC-3", "Sprint 1", "Backlog"} {
		if !strings.Contains(view, want) {
			t.Errorf("board lacks %q", want)
		}
	}
}

// TestKanbanIssuesBigBacklog: 2000 backlog issues ranked above 100 in
// progress still leave the 100 on the board; the backlog stays out of the
// fetch, not just the view.
func TestKanbanIssuesBigBacklog(t *testing.T) {
	var jql string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board/7/issue" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		jql = r.URL.Query().Get("jql")
		var issues []string
		if !strings.Contains(jql, "status not in (1, 2)") {
			for i := range 2000 {
				issues = append(issues, fmt.Sprintf(`{"key":"K-%d","fields":{"status":{"id":"1"}}}`, i+1))
			}
		}
		for i := range 100 {
			issues = append(issues, fmt.Sprintf(`{"key":"K-%d","fields":{"status":{"id":"3"}}}`, 3000+i))
		}
		var start int
		fmt.Sscan(r.URL.Query().Get("startAt"), &start)
		page := issues[min(start, len(issues)):min(start+100, len(issues))]
		fmt.Fprintf(w, `{"total":%d,"issues":[%s]}`, len(issues), strings.Join(page, ","))
	}))
	defer srv.Close()
	c := jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	cfg := &jira.BoardConfig{Columns: []jira.Column{{Name: "Backlog", StatusIDs: []string{"1", "2"}}, {Name: "Doing", StatusIDs: []string{"3"}}}}
	cards, total, err := KanbanIssues(context.Background(), c, 7, cfg, 0, "assignee = x", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "((statusCategory != Done OR updated >= -14d) AND (status not in (1, 2))) AND (assignee = x)"; jql != want {
		t.Errorf("jql = %q, want %q", jql, want)
	}
	if len(cards) != 100 || total != 100 || cards[0].Key != "K-3000" {
		t.Errorf("got %d cards of %d, want the 100 in progress", len(cards), total)
	}
}

// TestFilterViewTotal: a filter view the card limit cuts off has Jira's
// count for a total, for the header's "first N of M".
func TestFilterViewTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			_, _ = w.Write([]byte(`{"issues":[{"key":"K-1","fields":{}}],"nextPageToken":"p2"}`))
		case "/rest/api/3/search/approximate-count":
			_, _ = w.Write([]byte(`{"count":2100}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()
	c := jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", CardLimit: 1})
	cards, total, err := fetchJiraView(context.Background(), c, 7, &jira.BoardConfig{}, jiraView{kind: jiraViewFilter, jql: "labels = x"}, "")
	if err != nil || len(cards) != 1 || total != 2100 {
		t.Errorf("%d cards of %d, %v", len(cards), total, err)
	}
}

// TestJiraTabBacklogIsList: a planning view shows as a list whatever the mode.
func TestJiraTabBacklogIsList(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraCards(jiraCardsMsg{seq: m.jiraTab.seq, viewIdx: 1, cards: []jira.Card{
		{Key: "ABC-9", Summary: "Later", StatusID: "1", Status: "New"},
	}, total: 1})
	m = out.(Model)
	if m.jiraShowsLanes() {
		t.Fatal("backlog shows as lanes")
	}
	if m.toggleJiraMode(); !m.jiraTab.wantLanes {
		t.Error("toggle on a list-only view changed the mode")
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-9" {
		t.Errorf("selected %q, want ABC-9", c.Key)
	}
}

func TestJiraTabToggleKeepsSelection(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "right"))
	m = out.(Model)
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-2" {
		t.Fatalf("selected %q, want ABC-2", c.Key)
	}
	out, _ = m.handleJiraKey(keyMsg(t, "t"))
	m = out.(Model)
	if m.jiraShowsLanes() {
		t.Fatal("t did not switch to the list")
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-2" {
		t.Errorf("list selected %q, want ABC-2", c.Key)
	}
}

// TestJiraTabMoveCard: L moves the card a lane right at once, the cursor
// follows it, and a transition is requested.
func TestJiraTabMoveCard(t *testing.T) {
	m := jiraTabModel(t)
	out, cmd := m.handleJiraKey(keyMsg(t, "L"))
	m = out.(Model)
	if cmd == nil {
		t.Fatal("no transition requested")
	}
	if len(m.jiraTab.lanes[1].cards) != 2 {
		t.Fatalf("In progress has %d cards, want 2", len(m.jiraTab.lanes[1].cards))
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-1" || m.jiraTab.lane != 1 {
		t.Errorf("cursor on %q lane %d, want ABC-1 in lane 1", c.Key, m.jiraTab.lane)
	}
}

// TestJiraTabEmptyLanes: alt+e hides lanes the filters leave empty and
// says how many; L still reaches a hidden lane, which shows again; a board
// with no card keeps every lane.
func TestJiraTabEmptyLanes(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cards[1].StatusID = "1" // In progress empties
	m.buildJiraLanes()
	out, _ := m.handleJiraKey(keyMsg(t, "alt+e"))
	m = out.(Model)
	if n := len(m.jiraTab.lanes); n != 2 || m.jiraTab.lanes[1].name != "Done" {
		t.Fatalf("%d lanes, want To do and Done", n)
	}
	if s := ansi.Strip(m.View().Content); !strings.Contains(s, "1 empty hidden") {
		t.Errorf("header does not count the hidden lane:\n%s", s)
	}
	if v, _, _ := m.store.GetMeta(jiraEmptyLanesMeta); v != "hide" {
		t.Errorf("remembered %q, want hide", v)
	}
	m.selectJiraKey("ABC-1")
	out, _ = m.handleJiraKey(keyMsg(t, "L"))
	m = out.(Model)
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-1" || len(m.jiraTab.lanes) != 3 || m.jiraTab.lanes[m.jiraTab.lane].name != "In progress" {
		t.Fatalf("cursor on %q in %d lanes, want ABC-1 in In progress shown again", c.Key, len(m.jiraTab.lanes))
	}
	for i := range m.jiraTab.cards {
		m.jiraTab.cards[i].StatusID = "9" // in no column
	}
	m.buildJiraLanes()
	if n := len(m.jiraTab.lanes); n != 3 {
		t.Errorf("an empty board shows %d lanes, want all 3", n)
	}
	out, _ = m.handleJiraKey(keyMsg(t, "alt+e"))
	if m = out.(Model); m.jiraHidesEmpty() {
		t.Error("alt+e again should show empty lanes")
	}
}

// TestJiraTabDragDrop: dragging a card onto another lane moves it there.
func TestJiraTabDragDrop(t *testing.T) {
	m := jiraTabModel(t)
	laneW := m.jiraTab.laneW
	y := jiraBodyTop + 1 // the first card's key line
	out, _ := m.Update(tea.MouseClickMsg{X: 2, Y: y, Button: tea.MouseLeft})
	m = out.(Model)
	if m.jiraTab.drag.active || m.jiraTab.drag.key != "ABC-1" {
		t.Fatalf("drag = %+v, want ABC-1 armed, not active", m.jiraTab.drag)
	}
	out, _ = m.Update(tea.MouseMotionMsg{X: 3, Y: y, Button: tea.MouseLeft})
	m = out.(Model)
	if m.jiraTab.drag.active {
		t.Fatal("a one-column wiggle started the drag")
	}
	out, _ = m.Update(tea.MouseMotionMsg{X: laneW + 3, Y: y, Button: tea.MouseLeft})
	m = out.(Model)
	if m.jiraTab.drag.over != 1 {
		t.Fatalf("over lane %d, want 1", m.jiraTab.drag.over)
	}
	// The ghost shows in In progress, above ABC-2 (ranked after ABC-1), and the
	// card it left stays faint in To do: ABC-1 is drawn twice.
	lanes := m.jiraTab.lanesOut
	if strings.Count(lanes, "┊ ABC-1") != 2 {
		t.Errorf("want the ghost and the left-behind card, got:\n%s", lanes)
	}
	if g, c := strings.Index(lanes, "┊ First"), strings.Index(lanes, "Second"); g < 0 || c < 0 || g > c {
		t.Errorf("ghost not above ABC-2:\n%s", lanes)
	}
	out, cmd := m.Update(tea.MouseReleaseMsg{X: laneW + 3, Y: y, Button: tea.MouseLeft})
	m = out.(Model)
	if cmd == nil || m.jiraTab.drag.active {
		t.Fatal("drop did not move the card")
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-1" || m.jiraTab.lane != 1 {
		t.Errorf("cursor on %q lane %d, want ABC-1 in In progress", c.Key, m.jiraTab.lane)
	}
}

// TestJiraTabDragRank: dragging a card down its own lane ranks it where it
// is dropped, the ghost showing the spot on the way; a drop into another
// lane lands at the spot too.
func TestJiraTabDragRank(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/agile/1.0/issue/rank" {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.jiraTab.cards = append(m.jiraTab.cards, jira.Card{Key: "ABC-5", Summary: "Fifth", StatusID: "1", Status: "New"})
	m.buildJiraLanes()
	m.renderJira()
	lane := func(l int) string {
		var keys []string
		for _, i := range m.jiraTab.lanes[l].cards {
			keys = append(keys, m.jiraTab.cards[i].Key)
		}
		return strings.Join(keys, " ")
	}
	y := jiraBodyTop + 1
	below := y + 2*m.cardSlot() // under the lane's last card
	out, _ := m.Update(tea.MouseClickMsg{X: 2, Y: y, Button: tea.MouseLeft})
	out, _ = out.(Model).Update(tea.MouseMotionMsg{X: 2, Y: below, Button: tea.MouseLeft})
	m = out.(Model)
	if d := m.jiraTab.drag; !d.active || d.over != 0 || !d.slotOK || d.slot != 2 {
		t.Fatalf("drag = %+v, want lane 0 slot 2", d)
	}
	if g, c := strings.Index(m.jiraTab.lanesOut, "┊ First"), strings.Index(m.jiraTab.lanesOut, "Fifth"); g < 0 || c < 0 || g < c {
		t.Errorf("ghost not below ABC-5:\n%s", m.jiraTab.lanesOut)
	}
	out, cmd := m.Update(tea.MouseReleaseMsg{X: 2, Y: below, Button: tea.MouseLeft})
	m = out.(Model)
	if got := lane(0); got != "ABC-3 ABC-5 ABC-1" || cmd == nil {
		t.Fatalf("lane = %s, cmd %v", got, cmd != nil)
	}
	cmd()
	if body != `{"issues":["ABC-1"],"rankAfterIssue":"ABC-5"}` {
		t.Errorf("rank = %s", body)
	}

	// ABC-3 into In progress, under ABC-2 (clicked elsewhere than before: no
	// double-click).
	out, _ = m.Update(tea.MouseClickMsg{X: 6, Y: y, Button: tea.MouseLeft})
	out, _ = out.(Model).Update(tea.MouseMotionMsg{X: m.jiraTab.laneW + 3, Y: y + m.cardSlot(), Button: tea.MouseLeft})
	m = out.(Model)
	if d := m.jiraTab.drag; d.key != "ABC-3" || d.over != 1 || d.slot != 1 {
		t.Fatalf("drag = %+v, want ABC-3 over lane 1 slot 1", d)
	}
	out, _ = m.Update(tea.MouseReleaseMsg{X: m.jiraTab.laneW + 3, Y: y + m.cardSlot(), Button: tea.MouseLeft})
	m = out.(Model)
	if got := lane(1); got != "ABC-2 ABC-3" {
		t.Errorf("In progress = %s", got)
	}
}

// TestJiraTabDropZones: a lane of several statuses splits into a zone per
// status while dragged over, and the drop lands on the zone's status.
func TestJiraTabDropZones(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.statusNames = map[string]string{"5": "Done", "6": "Closed"}
	laneW := m.jiraTab.laneW
	out, _ := m.Update(tea.MouseClickMsg{X: 2, Y: jiraBodyTop + 1, Button: tea.MouseLeft})
	m = out.(Model)
	zh := jiraZoneH(m.jiraTab.view.Height(), 2)
	y := jiraBodyTop + 1 + zh // inside the second zone
	out, _ = m.Update(tea.MouseMotionMsg{X: 2*laneW + 3, Y: y, Button: tea.MouseLeft})
	m = out.(Model)
	if m.jiraTab.drag.over != 2 || m.jiraTab.drag.zone != 1 {
		t.Fatalf("drag = %+v, want lane 2 zone 1", m.jiraTab.drag)
	}
	for _, want := range []string{"Done", "Closed"} {
		if !strings.Contains(m.jiraTab.lanesOut, want) {
			t.Errorf("zones lack %q", want)
		}
	}
	out, cmd := m.Update(tea.MouseReleaseMsg{X: 2*laneW + 3, Y: y, Button: tea.MouseLeft})
	m = out.(Model)
	if cmd == nil {
		t.Fatal("drop did not move the card")
	}
	if c, _ := m.selectedJiraCard(); c.StatusID != "6" || c.Status != "Closed" {
		t.Errorf("card = %+v, want it in Closed", c)
	}
}

// TestJiraTabKeyMoveAsksStatus: a keyboard move into a lane of several
// statuses asks which one.
func TestJiraTabKeyMoveAsksStatus(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.lane = 1
	m.clampJiraCursor()
	out, _ := m.handleJiraKey(keyMsg(t, "L"))
	m = out.(Model)
	if !m.jiraPicker.active || m.jiraPicker.kind != jiraPickLaneStatus || len(m.jiraPicker.items) != 2 {
		t.Fatalf("picker = %+v, want the two Done statuses", m.jiraPicker)
	}
	m.jiraPicker.idx = 1
	out, cmd := m.applyJiraPick()
	m = out.(Model)
	if c, _ := m.selectedJiraCard(); cmd == nil || c.Key != "ABC-2" || c.StatusID != "6" {
		t.Errorf("card = %+v, want ABC-2 moving to status 6", c)
	}
}

func TestJiraTabOpensIssueInPanel(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "enter"))
	m = out.(Model)
	if !m.refOpen || m.focus != focusRef {
		t.Fatalf("refOpen=%v focus=%v, want the panel focused", m.refOpen, m.focus)
	}
	if r := m.refs[0]; r.kind != refJira || r.jiraKey != "ABC-1" {
		t.Fatalf("ref = %+v, want ABC-1", r)
	}
	if !strings.Contains(m.View().Content, "laneway") {
		t.Error("tab body missing")
	}
	m.closeRef()
	if m.focus != focusJira {
		t.Fatalf("after close focus=%v, want back on the board", m.focus)
	}
}

func TestJiraTabProjectPickerFilters(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.projects = []jira.Project{{Key: "XYZ", Name: "Other"}, {Key: "ABC", Name: "Alpha"}}
	out, _ := m.handleJiraKey(keyMsg(t, "p"))
	m = out.(Model)
	if !m.jiraPicker.active || m.jiraPicker.items[0].id != "ABC" {
		t.Fatalf("picker = %+v, want configured ABC first", m.jiraPicker.items)
	}
	out, _ = m.handleJiraPickerKey(keyMsg(t, "x"))
	m = out.(Model)
	if len(m.jiraPicker.items) != 1 || m.jiraPicker.items[0].id != "XYZ" {
		t.Fatalf("filtered = %+v, want XYZ", m.jiraPicker.items)
	}
	out, cmd := m.handleJiraPickerKey(keyMsg(t, "enter"))
	m = out.(Model)
	if cmd == nil || m.jiraTab.project != "XYZ" || m.jiraPicker.active {
		t.Errorf("project=%q picker=%v, want XYZ loading", m.jiraTab.project, m.jiraPicker.active)
	}
}

func TestJiraFilterJQL(t *testing.T) {
	quick := []jira.QuickFilter{{ID: 7, JQL: "labels = a OR labels = b"}, {ID: 8, JQL: "x = 1"}}
	for _, c := range []struct {
		a    jiraAssignee
		on   map[int]bool
		want string
	}{
		{jiraAssignee{}, nil, ""},
		{jiraAssignee{id: "me"}, nil, "assignee = currentUser()"},
		{jiraAssignee{id: "none"}, map[int]bool{8: true}, "assignee is EMPTY AND (x = 1)"},
		{jiraAssignee{id: "a1"}, map[int]bool{7: true}, `assignee = "a1" AND (labels = a OR labels = b)`},
		{jiraAssignee{id: "me,a1"}, nil, `assignee in (currentUser(), "a1")`},
		{jiraAssignee{id: "a1,none"}, nil, `(assignee = "a1" OR assignee is EMPTY)`},
	} {
		if got := jiraFilterJQL(c.a, quick, c.on); got != c.want {
			t.Errorf("jiraFilterJQL(%+v, %v) = %q, want %q", c.a, c.on, got, c.want)
		}
	}
	if got := andJQL("a OR b", "c"); got != "(a OR b) AND (c)" {
		t.Errorf("andJQL = %q", got)
	}
}

// TestJiraTabQuickFilter: a digit toggles its quick filter and refetches.
func TestJiraTabQuickFilter(t *testing.T) {
	m := jiraTabModel(t)
	out, cmd := m.handleJiraKey(keyMsg(t, "2"))
	m = out.(Model)
	if cmd == nil || !m.jiraTab.quickOn[8] || m.jiraTab.quickOn[7] {
		t.Fatalf("quickOn = %v, want BE on and a refetch", m.jiraTab.quickOn)
	}
	if !strings.Contains(m.View().Content, "2 BE") {
		t.Error("filter line lacks the quick filter")
	}
	out, _ = m.handleJiraKey(keyMsg(t, "0"))
	m = out.(Model)
	if m.jiraTab.jiraFiltered() {
		t.Error("0 left a filter on")
	}
}

// TestJiraTabAssigneeFilter: the picker offers the people on the board, and a
// pick filters by them.
func TestJiraTabAssigneeFilter(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "a"))
	m = out.(Model)
	items := m.jiraPicker.items
	if len(items) != 4 || items[3].id != "a1" || !items[0].current {
		t.Fatalf("items = %+v, want everyone, me, unassigned, Ada", items)
	}
	m.jiraPicker.idx = 3
	out, cmd := m.applyJiraPick()
	m = out.(Model)
	if cmd == nil || m.jiraTab.assignee.id != "a1" || m.jiraTab.assignee.label != "Ada" {
		t.Fatalf("assignee = %+v, want Ada and a refetch", m.jiraTab.assignee)
	}
}

// TestJiraTabAssigneeSeveral: space ticks several people, enter filters by
// all of them, and the picker opens with them ticked; enter without a tick
// still picks the row.
func TestJiraTabAssigneeSeveral(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "a"))
	m = out.(Model)
	for _, i := range []int{1, 3} { // Me, Ada
		m.jiraPicker.idx = i
		out, _ = m.handleJiraPickerKey(keyMsg(t, "space"))
		m = out.(Model)
	}
	if m.jiraPicker.items[0].current || !m.jiraPicker.items[1].current || !m.jiraPicker.items[3].current {
		t.Fatalf("ticks = %+v", m.jiraPicker.items)
	}
	out, cmd := m.handleJiraPickerKey(keyMsg(t, "enter"))
	m = out.(Model)
	if a := m.jiraTab.assignee; cmd == nil || a.id != "me,a1" || a.label != "Me, Ada" {
		t.Fatalf("assignee = %+v, want Me and Ada and a refetch", a)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Me, Ada") {
		t.Error("header lacks both names")
	}
	out, _ = m.handleJiraKey(keyMsg(t, "a"))
	m = out.(Model)
	if it := m.jiraPicker.items; !it[1].current || !it[3].current || it[0].current {
		t.Fatalf("reopened ticks = %+v", it)
	}
	m.jiraPicker.idx = 2
	out, _ = m.handleJiraPickerKey(keyMsg(t, "enter"))
	if a := out.(Model).jiraTab.assignee; a.id != "none" {
		t.Errorf("enter without ticking = %+v, want only the row", a)
	}
	// m stays one toggle: it replaces the set with you.
	m.jiraTab.assignee = jiraAssignee{id: "me,a1", label: "Me, Ada"}
	out, _ = m.handleJiraKey(keyMsg(t, "m"))
	if a := out.(Model).jiraTab.assignee; a.id != "me" {
		t.Errorf("m on a set = %+v, want me", a)
	}
}

// TestJiraTabAssigneeOneHand: several people with the keyboard alone (tab
// ticks the row a typed filter found) or the mouse alone (a click ticks,
// a click on the hint applies).
func TestJiraTabAssigneeOneHand(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "a"))
	m = out.(Model)
	for _, k := range []string{"A", "d", "a", "tab"} {
		out, _ = m.handleJiraPickerKey(keyMsg(t, k))
		m = out.(Model)
	}
	if m.jiraPicker.filter.Value() != "" || m.jiraPicker.checked["a1"] == "" {
		t.Fatalf("tab after typing: filter %q, ticks %v", m.jiraPicker.filter.Value(), m.jiraPicker.checked)
	}
	m.jiraPicker.idx = 1
	out, _ = m.handleJiraPickerKey(keyMsg(t, "tab"))
	out, _ = out.(Model).handleJiraPickerKey(keyMsg(t, "enter"))
	if a := out.(Model).jiraTab.assignee; a.id != "me,a1" {
		t.Fatalf("keyboard: assignee %+v", a)
	}

	m = jiraTabModel(t)
	out, _ = m.handleJiraKey(keyMsg(t, "a"))
	m = out.(Model)
	at := func(text string) (int, int) {
		for y, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			if x := strings.Index(l, text); x >= 0 {
				return len([]rune(l[:x])) + 1, y
			}
		}
		t.Fatalf("%q not on screen", text)
		return 0, 0
	}
	for _, name := range []string{"Me", "Ada"} {
		x, y := at("  " + name)
		out, _ = m.handleClick(tea.MouseClickMsg{X: x + 2, Y: y, Button: tea.MouseLeft})
		m = out.(Model)
	}
	if !m.jiraPicker.active || len(m.jiraPicker.checked) != 2 {
		t.Fatalf("clicks: open %v, ticks %v", m.jiraPicker.active, m.jiraPicker.checked)
	}
	x, y := at("↵ apply")
	out, _ = m.handleClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if a := out.(Model).jiraTab.assignee; a.id != "me,a1" {
		t.Fatalf("mouse: assignee %+v", a)
	}
}

// TestJiraTabSearch: / narrows the board locally, enter keeps the query, esc
// clears it.
func TestJiraTabSearch(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "/"))
	m = out.(Model)
	for _, k := range []string{"t", "h", "i"} {
		out, _ = m.handleKey(keyMsg(t, k))
		m = out.(Model)
	}
	if len(m.jiraTab.order) != 1 || m.jiraTab.cards[m.jiraTab.order[0]].Key != "ABC-3" {
		t.Fatalf("order = %v, want only ABC-3", m.jiraTab.order)
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-3" {
		t.Errorf("selected %q, want ABC-3", c.Key)
	}
	out, _ = m.handleKey(keyMsg(t, "enter"))
	m = out.(Model)
	if m.jiraTab.searching || m.jiraTab.jiraSearchQuery() != "thi" {
		t.Fatalf("after enter searching=%v query=%q", m.jiraTab.searching, m.jiraTab.jiraSearchQuery())
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "/thi ×") {
		t.Error("board lacks the query chip")
	}
	out, _ = m.handleKey(keyMsg(t, "esc"))
	m = out.(Model)
	if m.jiraTab.jiraSearchQuery() != "" || len(m.jiraTab.order) != 4 {
		t.Errorf("after esc query=%q order=%v, want all cards", m.jiraTab.jiraSearchQuery(), m.jiraTab.order)
	}
}

func TestJiraTabSearchNoMatch(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "/"))
	m = out.(Model)
	out, _ = m.handleKey(keyMsg(t, "z"))
	m = out.(Model)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "No card matches /z") || !strings.Contains(view, "esc clears the search") {
		t.Error("empty search lacks its message")
	}
}

// TestEmptyStates: an empty backlog and an empty lane say so.
func TestEmptyStates(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.viewIdx = 1 // the backlog
	m.jiraTab.cards = nil
	m.buildJiraLanes()
	m.renderJira()
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "The backlog is empty") || !strings.Contains(view, "n adds an issue") {
		t.Errorf("empty backlog:\n%s", view)
	}
	m = jiraTabModel(t)
	m.jiraTab.search.SetValue("first")
	m.applyJiraSearch()
	if !strings.Contains(ansi.Strip(m.View().Content), "nothing here") {
		t.Error("an empty lane lacks its placeholder")
	}
}

func TestHelpOverlay(t *testing.T) {
	m := jiraTabModel(t)
	m.width = 400 // every column on one page
	out, _ := m.handleKey(keyMsg(t, "?"))
	m = out.(Model)
	if !m.helpOpen || !strings.Contains(m.View().Content, "story points") {
		t.Fatal("? did not show help")
	}
	hasMouse := func() bool {
		return slices.ContainsFunc(m.helpSections(), func(s struct {
			title string
			rows  []helpRow
		}) bool {
			return s.title == "Mouse"
		})
	}
	if !hasMouse() {
		t.Error("help lacks the mouse")
	}
	m.opts.mouse = false
	if hasMouse() {
		t.Error("help shows the mouse with ui.mouse off")
	}
	m.opts.mouse = true
	out, _ = m.handleKey(keyMsg(t, "j"))
	m = out.(Model)
	if m.helpOpen || m.jiraTab.row != 0 {
		t.Errorf("key after help: open=%v row=%d, want closed and swallowed", m.helpOpen, m.jiraTab.row)
	}
}

func TestJiraTabCopyKey(t *testing.T) {
	m := jiraTabModel(t)
	out, cmd := m.handleKey(keyMsg(t, "y"))
	m = out.(Model)
	if cmd == nil || m.status != "copied ABC-1" {
		t.Errorf("y: cmd=%v status=%q, want copied ABC-1", cmd != nil, m.status)
	}
	out, _ = m.handleKey(keyMsg(t, "Y"))
	m = out.(Model)
	if !strings.HasPrefix(m.status, "copied http") || !strings.HasSuffix(m.status, "/browse/ABC-1") {
		t.Errorf("Y: status=%q, want the browse URL", m.status)
	}
}

func TestJiraTabCopyBranch(t *testing.T) {
	m := jiraTabModel(t)
	c, _ := m.selectedJiraCard()
	out, cmd := m.handleKey(keyMsg(t, "ctrl+y"))
	m = out.(Model)
	if want := "copied ABC-1-" + slugify(c.Summary); cmd == nil || m.status != want {
		t.Errorf("ctrl+y: cmd=%v status=%q, want %q", cmd != nil, m.status, want)
	}
}

func TestJiraGotoKey(t *testing.T) {
	for in, want := range map[string]string{
		"abc-12": "ABC-12", " 42 ": "ABC-42", "XYZ-1": "XYZ-1", "abc": "", "-1": "", "": "",
		"https://x.atlassian.net/browse/XYZ-7":                                              "XYZ-7",
		"https://x.atlassian.net/jira/software/c/projects/ABC/boards/1?selectedIssue=ABC-9": "ABC-9",
	} {
		if got := jiraGotoKey(in, "ABC"); got != want {
			t.Errorf("jiraGotoKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJiraGotoOpensPanel(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "#"))
	m = out.(Model)
	if !m.jiraGotoActive {
		t.Fatal("# did not open the prompt")
	}
	out, _ = m.handleKey(keyMsg(t, "3"))
	m = out.(Model)
	out, cmd := m.handleKey(keyMsg(t, "enter"))
	m = out.(Model)
	if m.jiraGotoActive || !m.refOpen || m.refs[0].jiraKey != "ABC-3" || cmd == nil {
		t.Fatalf("after enter: active=%v open=%v refs=%v", m.jiraGotoActive, m.refOpen, m.refs)
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-3" {
		t.Errorf("board cursor on %q, want ABC-3", c.Key)
	}
}

// TestJiraGotoNotFound: a key off the board is looked up first; one that
// isn't there keeps the prompt, the text and the reason.
func TestJiraGotoNotFound(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "#"))
	m = out.(Model)
	m.jiraGotoInput.SetValue("ABC-99")
	out, cmd := m.handleKey(keyMsg(t, "enter"))
	m = out.(Model)
	if !m.jiraGotoActive || m.refOpen || cmd == nil || m.jiraGotoKey != "ABC-99" {
		t.Fatalf("enter: active %v, panel %v, looking up %q", m.jiraGotoActive, m.refOpen, m.jiraGotoKey)
	}
	out, _ = m.Update(jiraGotoMsg{key: "ABC-99", err: errors.New("jira: ABC-99 not found (or no access)")})
	m = out.(Model)
	if !m.jiraGotoActive || m.refOpen || m.jiraGotoInput.Value() != "ABC-99" || !strings.Contains(m.View().Content, "not found") {
		t.Fatalf("not found: active %v, panel %v, input %q", m.jiraGotoActive, m.refOpen, m.jiraGotoInput.Value())
	}
	out, _ = m.handleKey(keyMsg(t, "enter"))
	out, _ = out.(Model).Update(jiraGotoMsg{key: "ABC-99"})
	if m = out.(Model); m.jiraGotoActive || !m.refOpen || m.refs[0].jiraKey != "ABC-99" {
		t.Errorf("found: active %v, panel %v %v", m.jiraGotoActive, m.refOpen, m.refs)
	}
}

func TestJiraAutoRefresh(t *testing.T) {
	m := jiraTabModel(t)
	seq := m.jiraTab.seq
	out, cmd := m.handleJiraAutoRefresh()
	m = out.(Model)
	if cmd == nil || m.jiraTab.seq != seq {
		t.Fatalf("fresh board: seq %d→%d, want untouched and re-armed", seq, m.jiraTab.seq)
	}
	m.jiraTab.fetched = m.jiraTab.fetched.Add(-m.opts.staleAfter)
	m.helpOpen = true
	if out, _ = m.handleJiraAutoRefresh(); out.(Model).jiraTab.seq != seq {
		t.Error("refreshed under a modal")
	}
	m.helpOpen = false
	// A scrum board reads its sprints first, then the cards.
	out, _ = m.handleJiraAutoRefresh()
	out, _ = out.(Model).handleJiraSprints(jiraSprintsMsg{board: 1, sprints: []jira.Sprint{{ID: 9, Name: "Sprint 1", State: "active"}}})
	if out.(Model).jiraTab.seq == seq {
		t.Error("stale idle board did not refresh")
	}
}

// TestJiraSprintClosedElsewhere: a refresh drops a sprint closed since; the
// board on it shows the first view, one on another view keeps it.
func TestJiraSprintClosedElsewhere(t *testing.T) {
	next := jiraSprintsMsg{board: 1, sprints: []jira.Sprint{{ID: 10, Name: "Sprint 2", State: "active"}, {ID: 11, Name: "Sprint 3", State: "future"}}}
	m := jiraTabModel(t)
	seq := m.jiraTab.seq
	out, _ := m.handleJiraSprints(next)
	m = out.(Model)
	if v, _ := m.jiraCurrentView(); v.sprint != 10 || m.jiraTab.seq == seq || !strings.Contains(m.status, "Sprint 1 has closed") {
		t.Errorf("on the closed sprint: view %q, seq %d→%d, status %q", v.name, seq, m.jiraTab.seq, m.status)
	}
	if len(m.jiraTab.views) != 3 || m.jiraTab.views[2].kind != jiraViewBacklog {
		t.Errorf("views = %v", m.jiraTab.views)
	}

	m = jiraTabModel(t)
	m.jiraTab.viewIdx = 1
	out, _ = m.handleJiraSprints(next)
	m = out.(Model)
	if v, _ := m.jiraCurrentView(); v.kind != jiraViewBacklog || m.status != "" {
		t.Errorf("on the backlog: view %q, status %q", v.name, m.status)
	}
}

func TestJiraPriorityMark(t *testing.T) {
	for p, want := range map[string]string{"Highest": "⇈", "High": "↑", "Medium": "", "": "", "Low": "↓", "Lowest": "⇊"} {
		if got := ansi.Strip(jiraPriorityMark(p)); got != want {
			t.Errorf("jiraPriorityMark(%q) = %q, want %q", p, got, want)
		}
	}
	m := jiraTabModel(t)
	m.jiraTab.cards[0].Priority = "Highest"
	m.buildJiraLanes()
	m.renderJira()
	if !strings.Contains(m.View().Content, "⇈") {
		t.Error("lane card lacks its priority mark")
	}
}

func TestJiraListSort(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.wantLanes = false
	m.jiraTab.cards[1].Priority = "Highest"
	m.jiraTab.cards[3].Priority = "Low"
	m.buildJiraLanes()
	keys := func() string {
		var ks []string
		for _, ci := range m.jiraTab.order {
			ks = append(ks, m.jiraTab.cards[ci].Key)
		}
		return strings.Join(ks, " ")
	}
	rank := keys()
	out, _ := m.handleJiraKey(keyMsg(t, "s"))
	m = out.(Model)
	if got := keys(); m.jiraTab.sort != jiraSortPriority || got != "ABC-2 ABC-1 ABC-3 ABC-4" {
		t.Errorf("priority order = %q", got)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "s sort: priority") {
		t.Error("list lacks the sort chip")
	}
	out, _ = m.handleJiraKey(keyMsg(t, "s"))
	m = out.(Model)
	if got := keys(); !strings.HasPrefix(got, "ABC-3 ") {
		t.Errorf("points order = %q, want ABC-3 (5 pts) first", got)
	}
	for range jiraSortCount - 2 {
		out, _ = m.handleJiraKey(keyMsg(t, "s"))
		m = out.(Model)
	}
	if m.jiraTab.sort != jiraSortRank || keys() != rank {
		t.Errorf("full cycle: sort=%v order=%q, want rank %q", m.jiraTab.sort, keys(), rank)
	}
}

func TestJiraKeySortNumeric(t *testing.T) {
	cards := []jira.Card{{Key: "ABC-10"}, {Key: "ABC-9"}, {Key: "AB-100"}}
	order := []int{0, 1, 2}
	jiraSortKey.apply(order, cards)
	if order[0] != 2 || order[1] != 1 || order[2] != 0 {
		t.Errorf("order = %v, want AB-100 ABC-9 ABC-10", order)
	}
}

func TestJiraMineToggle(t *testing.T) {
	m := jiraTabModel(t)
	out, cmd := m.handleJiraKey(keyMsg(t, "m"))
	m = out.(Model)
	if cmd == nil || m.jiraTab.assignee.id != "me" {
		t.Fatalf("m: assignee = %+v", m.jiraTab.assignee)
	}
	out, _ = m.handleJiraKey(keyMsg(t, "m"))
	if a := out.(Model).jiraTab.assignee; a.id != "" {
		t.Errorf("second m: assignee = %+v, want everyone", a)
	}
}

func TestPanelBackHistory(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.openJiraKey("ABC-1")
	m = out.(Model)
	out, _ = m.openJiraKey("ABC-3")
	m = out.(Model)
	out, _ = m.openJiraKey("ABC-3") // reopening the same issue adds nothing
	m = out.(Model)
	if len(m.refBack) != 1 {
		t.Fatalf("history = %v, want [ABC-1]", m.refBack)
	}
	out, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = out.(Model)
	if cmd == nil || m.currentRef().jiraKey != "ABC-1" || len(m.refBack) != 0 {
		t.Fatalf("back: showing %v, history %v", m.currentRef(), m.refBack)
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-1" {
		t.Errorf("board cursor on %q, want ABC-1", c.Key)
	}
	m.focus = focusRef
	out, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m = out.(Model); !strings.HasPrefix(m.status, "nothing to go back to") {
		t.Errorf("back with no trail: %q", m.status)
	}
	m.closeRef()
	if m.refBack != nil {
		t.Error("closing kept the history")
	}
}

func TestPanelLinks(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := openRefFor(m, "ABC-1")
	m = out.(Model)
	iss := &jira.Issue{Key: "ABC-1", Links: []jira.Link{{Rel: "blocks", Key: "ABC-3", Summary: "Third", Status: "New"}}}
	out, _ = m.handleJiraLoaded(jiraLoadedMsg{gen: m.refGen, key: "ABC-1", issue: iss})
	m = out.(Model)
	if !strings.Contains(ansi.Strip(m.refView.View()), "blocks ABC-3 Third · New") {
		t.Errorf("links section missing:\n%s", ansi.Strip(m.refView.View()))
	}
	out, _ = m.handleKey(keyMsg(t, "L"))
	m = out.(Model)
	if !m.jiraPicker.active || len(m.jiraPicker.items) != 1 {
		t.Fatalf("picker = %+v", m.jiraPicker)
	}
	out, cmd := m.handleKey(keyMsg(t, "enter"))
	m = out.(Model)
	if cmd == nil || m.currentRef().jiraKey != "ABC-3" || len(m.refBack) != 1 {
		t.Errorf("after pick: showing %v, history %v", m.currentRef(), m.refBack)
	}
}

func TestJiraLaneWIPLimit(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cfg.Columns[0].Max = 1
	m.buildJiraLanes()
	m.renderJira()
	if !strings.Contains(ansi.Strip(m.View().Content), "To do 2/1") {
		t.Fatal("lane head lacks its limit")
	}
	if !strings.Contains(m.jiraTab.lanesOut, jiraOverStyle.Underline(true).Render("To do 2/1")) {
		t.Error("over-limit lane not marked")
	}
}

func TestJiraCardShowsParent(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cards[0].ParentKey, m.jiraTab.cards[0].ParentSummary = "ABC-100", "Checkout epic"
	m.buildJiraLanes()
	m.renderJira()
	if !strings.Contains(ansi.Strip(m.View().Content), "⌃ Checkout") {
		t.Error("card lacks its parent")
	}
	m.jiraTab.search.SetValue("checkout")
	m.applyJiraSearch()
	if len(m.jiraTab.order) != 1 {
		t.Errorf("search by parent matched %d cards, want 1", len(m.jiraTab.order))
	}
}

// TestRulesLogBoardChanges: the first fresh load is a baseline; a refresh
// logs what the rules match, a cached copy logs nothing.
func TestRulesLogBoardChanges(t *testing.T) {
	m := jiraTabModel(t)
	m.rulesLog = filepath.Join(t.TempDir(), "rules.log")
	m.rules, _ = rules.Compile([]rules.Rule{{Name: "moved", On: rules.StrList{"status"}, Actions: []rules.Action{{Type: "log"}}}})
	cards := append([]jira.Card(nil), m.jiraTab.cards...)
	if cmd := m.runRules(cards); cmd != nil {
		t.Fatal("baseline fired")
	}
	cards[1].StatusID, cards[1].Status = "5", "Done"
	cached := jiraCardsMsg{seq: m.jiraTab.seq, cached: true, cards: cards}
	if _, cmd := m.handleJiraCards(cached); cmd != nil {
		t.Error("cached cards fired")
	}
	_, cmd := m.handleJiraCards(jiraCardsMsg{seq: m.jiraTab.seq, cards: cards})
	if cmd == nil {
		t.Fatal("refresh did not fire")
	}
	if msg := cmd().(rulesLoggedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	b, _ := os.ReadFile(m.rulesLog)
	if !strings.HasSuffix(string(b), " moved: ABC-2 status In progress → Done\n") {
		t.Errorf("log = %q", b)
	}
}

// TestRuleNotifyAndExec: notify is an OSC 777 without the body's controls;
// exec gets templated args, env and the issue on stdin.
func TestRuleNotifyAndExec(t *testing.T) {
	if got := rules.NotifySeq("laneway", "ABC-2; done\x1b[31m"); got != "\x1b]777;notify;laneway;ABC-2, done[31m\x1b\\" {
		t.Errorf("notify = %q", got)
	}
	m := jiraTabModel(t)
	out := filepath.Join(t.TempDir(), "out")
	m.rules, _ = rules.Compile([]rules.Rule{{Actions: []rules.Action{{Type: "exec",
		Command: []string{"sh", "-c", `printf '%s %s ' "$1" "$LANEWAY_OLD_STATUS" > "$2"; cat >> "$2"`, "_", "{{.Key}}", out}}}}})
	cards := append([]jira.Card(nil), m.jiraTab.cards...)
	m.runRules(cards)
	cards[1].StatusID, cards[1].Status = "5", "Done"
	if msg := m.ruleExec(m.rules.Fire(rules.Diff(m.jiraTab.cards, cards)[0])[0])(); msg != nil {
		t.Fatal(msg)
	}
	b, _ := os.ReadFile(out)
	if !strings.HasPrefix(string(b), `ABC-2 In progress {`) || !strings.Contains(string(b), `"Status":"Done"`) {
		t.Errorf("exec saw %q", b)
	}
	m.rules, _ = rules.Compile([]rules.Rule{{Name: "x", Actions: []rules.Action{{Type: "exec", Command: []string{"false"}}}}})
	if msg, _ := m.ruleExec(m.rules.Fire(rules.Event{Kind: "new", Card: cards[0]})[0])().(rulesLoggedMsg); msg.err == nil {
		t.Error("a failing command reported nothing")
	}
}

// TestRuleHighlight: a highlight rule marks the changed card in lanes and
// list until the card is opened.
func TestRuleHighlight(t *testing.T) {
	m := jiraTabModel(t)
	m.rules, _ = rules.Compile([]rules.Rule{{On: rules.StrList{"status"}, Actions: []rules.Action{{Type: "highlight"}}}})
	cards := append([]jira.Card(nil), m.jiraTab.cards...)
	m.runRules(cards)
	cards[1].StatusID, cards[1].Status = "5", "Done"
	out, _ := m.handleJiraCards(jiraCardsMsg{seq: m.jiraTab.seq, cards: cards})
	m = out.(Model)
	marked := regexp.MustCompile(`● ABC-2`)
	if !marked.MatchString(ansi.Strip(m.View().Content)) {
		t.Fatal("lanes lack the mark")
	}
	m.jiraTab.wantLanes = false
	m.renderJira()
	if !marked.MatchString(ansi.Strip(m.View().Content)) || strings.Contains(ansi.Strip(m.View().Content), "● ABC-1") {
		t.Fatal("list lacks the mark, or marks another card")
	}
	out, _ = m.showJiraKey("ABC-2")
	m = out.(Model)
	m.renderJira()
	if marked.MatchString(ansi.Strip(m.View().Content)) {
		t.Error("opening kept the mark")
	}
}

// TestRuleByMe: with by_me: false, a change you made fires nothing, one a
// teammate made does.
func TestRuleByMe(t *testing.T) {
	author := "me-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/myself":
			_, _ = w.Write([]byte(`{"accountId":"me-1"}`))
		case "/rest/api/3/issue/ABC-2/changelog":
			fmt.Fprintf(w, `{"total":1,"values":[{"author":{"accountId":%q},"items":[{"fieldId":"status"}]}]}`, author)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	for _, c := range []struct {
		author string
		marked bool
	}{{"me-1", false}, {"bob", true}} {
		author = c.author
		m := jiraTabModel(t)
		m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
		no := false
		m.rules, _ = rules.Compile([]rules.Rule{{Match: rules.Match{ByMe: &no}, Actions: []rules.Action{{Type: "highlight"}}}})
		cards := append([]jira.Card(nil), m.jiraTab.cards...)
		m.runRules(cards)
		cards[1].StatusID, cards[1].Status = "5", "Done"
		msg := m.runRules(cards)().(rulesEventsMsg)
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		out, _ := m.handleRulesEvents(msg)
		if _, got := out.(Model).jiraTab.highlights["ABC-2"]; got != c.marked {
			t.Errorf("author %s: marked = %v, want %v", c.author, got, c.marked)
		}
	}
}

// TestRuleWatch: a watch's first search is a baseline, the next fires its
// rules and not the board's, and every poll arms the next.
func TestRuleWatch(t *testing.T) {
	m := jiraTabModel(t)
	m.rules, _ = rules.Compile([]rules.Rule{
		{Name: "board", Actions: []rules.Action{{Type: "highlight", Color: "1"}}},
		{Name: "mine", Watch: "assignee = currentUser()", Actions: []rules.Action{{Type: "highlight", Color: "2"}}},
	})
	cards := []jira.Card{{Key: "XYZ-1", StatusID: "1", Status: "To do"}}
	out, cmd := m.handleRuleWatched(ruleWatchedMsg{jql: "assignee = currentUser()", cards: cards})
	m = out.(Model)
	if cmd == nil || len(m.jiraTab.highlights) != 0 {
		t.Fatalf("baseline: cmd %v, highlights %v", cmd, m.jiraTab.highlights)
	}
	cards = []jira.Card{{Key: "XYZ-1", StatusID: "2", Status: "Done"}}
	out, _ = m.handleRuleWatched(ruleWatchedMsg{jql: "assignee = currentUser()", cards: cards})
	m = out.(Model)
	if got := m.jiraTab.highlights; len(got) != 1 || got["XYZ-1"] != "2" {
		t.Errorf("highlights = %v", got)
	}
	out, cmd = m.handleRuleWatched(ruleWatchedMsg{jql: "assignee = currentUser()", err: errors.New("down")})
	if m = out.(Model); cmd == nil || !strings.Contains(m.status, "down") {
		t.Errorf("failed poll: cmd %v, status %q", cmd, m.status)
	}
}

// TestJiraMoveToSprint: M offers the sprints and the backlog, the current
// one marked; the current is a no-op, the backlog posts the card there.
func TestJiraMoveToSprint(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = r.Method + " " + r.URL.Path + " " + string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	c, _ := m.selectedJiraCard()
	out, _ := m.handleJiraKey(keyMsg(t, "M"))
	m = out.(Model)
	p := m.jiraPicker
	if !p.active || p.kind != jiraPickSprint || p.issueKey != c.Key || len(p.items) != 2 ||
		!p.items[0].current || p.items[1].id != jiraBacklogID {
		t.Fatalf("picker = %+v", p)
	}
	if !strings.Contains(m.View().Content, "Move "+c.Key+" to") {
		t.Error("picker not drawn")
	}
	if _, cmd := m.applyJiraPick(); cmd != nil {
		t.Error("the current sprint should be a no-op")
	}
	m.jiraPicker.idx = 1
	out, cmd := m.applyJiraPick()
	if cmd == nil || out.(Model).jiraPicker.active {
		t.Fatal("expected a move")
	}
	if msg := cmd().(jiraMutatedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	if want := `POST /rest/agile/1.0/backlog/issue {"issues":["` + c.Key + `"]}`; got != want {
		t.Errorf("request = %q, want %q", got, want)
	}
}

// TestJiraSprintLine: a sprint view shows days left or when it starts or
// ended, then its goal on one line; other views show nothing.
func TestJiraSprintLine(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.Local)
	day := 24 * time.Hour
	for _, tc := range []struct {
		v    jiraView
		want string
	}{
		{jiraView{kind: jiraViewSprint, start: now.Add(-day), end: now.Add(4*day + time.Hour), goal: "Ship\nit"}, "5d left · 2 workdays · Ship it"},
		{jiraView{kind: jiraViewSprint, start: now.Add(3 * day)}, "starts Sep 28"},
		{jiraView{kind: jiraViewSprint, end: now.Add(-day)}, "ended Sep 24"},
		{jiraView{kind: jiraViewSprint, goal: "Goal only"}, "Goal only"},
		{jiraView{kind: jiraViewBacklog, goal: "x"}, ""},
	} {
		if got := jiraSprintLine(tc.v, now, nil); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.v, got, tc.want)
		}
	}
	m := jiraTabModel(t)
	m.jiraTab.views[0].end = time.Now().Add(2*day + time.Hour)
	m.jiraTab.views[0].goal = "Launch"
	if !strings.Contains(m.View().Content, "3d left · ") || !strings.Contains(m.View().Content, "Launch") {
		t.Error("header lacks the sprint line")
	}
}

// TestJiraLanePoints: a lane head sums its estimated cards, and shows no
// sum when none is estimated or points are hidden.
func TestJiraLanePoints(t *testing.T) {
	cards := []jira.Card{{Points: "3"}, {Points: ""}, {Points: "0.1"}, {Points: "0.2"}}
	if got, ok := jiraLanePoints(cards, []int{0, 1, 2, 3}); !ok || got != "3.3" {
		t.Errorf("sum = %q %v", got, ok)
	}
	if _, ok := jiraLanePoints(cards, []int{1}); ok {
		t.Error("an unestimated lane has no sum")
	}
	m := jiraTabModel(t)
	if !strings.Contains(m.View().Content, "· 5p") {
		t.Error("lane head lacks its points")
	}
	m.opts.fields.points = false
	m.renderJira()
	if strings.Contains(m.View().Content, "· 5p") {
		t.Error("hidden points still summed")
	}
}

// TestPRMark: a card with an open pull request says so; card_fields can
// leave it out.
func TestPRMark(t *testing.T) {
	c := jira.Card{Key: "ABC-9", Summary: "S", PR: "OPEN"}
	lines := jiraCardLines(c, true, allCardFields)
	if !strings.Contains(ansi.Strip(lines[0]), "ABC-9") || !strings.Contains(ansi.Strip(lines[0]), "PR") {
		t.Errorf("head = %q", ansi.Strip(lines[0]))
	}
	c.PR = "MERGED"
	if got := ansi.Strip(jiraCardLines(c, true, allCardFields)[0]); !strings.Contains(got, "✓PR") {
		t.Errorf("merged head = %q", got)
	}
	f := allCardFields
	f.pr = false
	if got := ansi.Strip(jiraCardLines(c, true, f)[0]); strings.Contains(got, "PR") {
		t.Errorf("pr off: %q", got)
	}
}

// TestDeployMark: a deployed card names its environment, unless left out.
func TestDeployMark(t *testing.T) {
	c := jira.Card{Key: "ABC-9", Summary: "S", Deploy: "production"}
	if got := ansi.Strip(jiraCardLines(c, true, allCardFields)[0]); !strings.Contains(got, "▲ production") {
		t.Errorf("head = %q", got)
	}
	f := allCardFields
	f.deploy = false
	if got := ansi.Strip(jiraCardLines(c, true, f)[0]); strings.Contains(got, "▲") {
		t.Errorf("deploy off: %q", got)
	}
}

func TestSubtaskMark(t *testing.T) {
	c := jira.Card{Key: "ABC-9", Subtasks: 5, SubtasksDone: 2}
	if got := ansi.Strip(jiraCardLines(c, true, allCardFields)[0]); !strings.Contains(got, "☑ 2/5") {
		t.Errorf("head = %q", got)
	}
	if jiraSubtaskMark(jira.Card{}) != "" {
		t.Error("no subtasks, no mark")
	}
}

func TestDueMark(t *testing.T) {
	now := time.Date(2026, 9, 25, 15, 0, 0, 0, time.Local) // a Friday
	day := func(d int) time.Time { return time.Date(2026, 9, 25+d, 0, 0, 0, 0, time.Local) }
	for d, want := range map[int]string{-2: "overdue 2d", 0: "due today", 3: "due mon", 12: "due in 12d"} {
		if got := ansi.Strip(jiraDueMark(jira.Card{Due: day(d)}, now)); got != want {
			t.Errorf("%+d days: %q, want %q", d, got, want)
		}
	}
	if jiraDueMark(jira.Card{Due: day(-2), Done: true}, now) != "" {
		t.Error("a done card is not overdue")
	}
}

func TestAgeMark(t *testing.T) {
	now := time.Now()
	c := jira.Card{InProgress: true, Since: now.Add(-4 * 24 * time.Hour)}
	if got := ansi.Strip(jiraAgeMark(c, now, 5)); got != "4d" {
		t.Errorf("4 days = %q", got)
	}
	if got := jiraAgeMark(c, now, 3); got != jiraOverStyle.Render("4d!") {
		t.Errorf("past stale = %q", got)
	}
	c.InProgress = false
	if jiraAgeMark(c, now, 5) != "" {
		t.Error("only in-progress cards age")
	}
}

// TestUndoMove: u moves the last moved card back; again redoes it.
func TestUndoMove(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "u"))
	if m = out.(Model); !strings.Contains(m.status, "nothing to undo") {
		t.Fatalf("status %q", m.status)
	}
	m.moveJiraCard("ABC-1", 1, "") // To do → In progress
	status := func() string {
		i := slices.IndexFunc(m.jiraTab.cards, func(c jira.Card) bool { return c.Key == "ABC-1" })
		return m.jiraTab.cards[i].StatusID
	}
	if status() != "3" {
		t.Fatalf("after move: %s", status())
	}
	m.moveJiraCard("ABC-1", 2, "") // In progress → Done
	m.moveJiraCard("ABC-3", 1, "")
	for i, want := range []struct{ key, status string }{{"ABC-3", "1"}, {"ABC-1", "3"}, {"ABC-1", "1"}} {
		out, cmd := m.handleJiraKey(keyMsg(t, "u"))
		m = out.(Model)
		c, _ := m.cardOf(want.key)
		if c.StatusID != want.status || cmd == nil {
			t.Fatalf("undo %d: %s in %s, want %s", i+1, want.key, c.StatusID, want.status)
		}
	}
	if len(m.jiraTab.undo) != 0 {
		t.Errorf("undoing pushed steps: %d left", len(m.jiraTab.undo))
	}
	m.jiraTab.undo = []undoStep{{what: "x", back: func(*Model) tea.Cmd { return nil }}}
	m.jiraTab.undoneMove = "ABC-1"
	out, _ = m.Update(jiraMovedMsg{key: "ABC-1", lane: "To do"})
	if m = out.(Model); m.status != "undone: ABC-1 back to To do · 1 more to undo" {
		t.Errorf("undone status %q", m.status)
	}
}

// TestListGroups: sorted by assignee, the list heads each assignee's run
// with a count; a click still lands on the card under it.
func TestListGroups(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "t")) // list
	m = out.(Model)
	for m.jiraTab.sort != jiraSortAssignee {
		out, _ = m.handleJiraKey(keyMsg(t, "s"))
		m = out.(Model)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "── AD Ada · 1") || !strings.Contains(view, "── Unassigned · 3 · 5p") {
		t.Fatalf("headers missing:\n%s", view)
	}
	tt := m.jiraTab
	if tt.lineOf[0] != 1 || tt.lineOf[1] != 3 {
		t.Errorf("lineOf = %v", tt.lineOf)
	}
	if h := m.hitJira(5, jiraBodyTop+1+3); h.line != 1 { // under the column header
		t.Errorf("click on line 3 = card %d, want 1", h.line)
	}
	if h := m.hitJira(5, jiraBodyTop+1+2); h.line != 1 {
		t.Errorf("click on a header = card %d, want its first, 1", h.line)
	}
}

// TestListHeaderSorts: a click on a list column's header sorts by it, a
// second reverses it, a third goes back to rank.
func TestListHeaderSorts(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "t")) // list
	m = out.(Model)
	if head := ansi.Strip(m.View().Content); !strings.Contains(head, "Key") || !strings.Contains(head, "Status") {
		t.Fatalf("no column header:\n%s", head)
	}
	x := -1
	for _, c := range m.jiraTab.headAt {
		if c.by == jiraSortKey {
			x = 1 + c.x0
		}
	}
	if x < 0 {
		t.Fatal("the key header doesn't sort")
	}
	keys := func() string {
		var ks []string
		for _, ci := range m.jiraTab.order {
			ks = append(ks, m.jiraTab.cards[ci].Key)
		}
		return strings.Join(ks, " ")
	}
	rank := keys()
	for _, want := range []struct {
		desc bool
		by   jiraSort
	}{{false, jiraSortKey}, {true, jiraSortKey}, {false, jiraSortRank}} {
		out, _ = m.Update(tea.MouseClickMsg{X: x, Y: jiraBodyTop, Button: tea.MouseLeft})
		m = out.(Model)
		out, _ = m.Update(tea.MouseReleaseMsg{X: x, Y: jiraBodyTop, Button: tea.MouseLeft})
		m = out.(Model)
		if m.jiraTab.sort != want.by || m.jiraTab.desc != want.desc {
			t.Fatalf("sort = %v desc %v, want %v desc %v", m.jiraTab.sort, m.jiraTab.desc, want.by, want.desc)
		}
		if want.desc && !strings.HasPrefix(keys(), "ABC-4") {
			t.Errorf("reversed by key: %s", keys())
		}
	}
	if keys() != rank {
		t.Errorf("back to rank: %s, want %s", keys(), rank)
	}
}

// TestListColumnDrag: a header cell dragged onto another moves its column,
// in the header and the rows, and doesn't sort.
func TestListColumnDrag(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "t")) // list
	m = out.(Model)
	at := func(col string) int {
		for _, c := range m.jiraTab.headCols {
			if c.col == col {
				return 1 + c.x0
			}
		}
		t.Fatalf("no %s column", col)
		return 0
	}
	from, to := at("status"), at("key")
	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{X: from, Y: jiraBodyTop, Button: tea.MouseLeft},
		tea.MouseMotionMsg{X: to, Y: jiraBodyTop, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: to, Y: jiraBodyTop, Button: tea.MouseLeft},
	} {
		out, _ = m.Update(msg)
		m = out.(Model)
	}
	if got := m.listColumnOrder(); got[0] != "status" || got[1] != "key" {
		t.Fatalf("order = %v, want status before key", got)
	}
	if m.jiraTab.sort != jiraSortRank {
		t.Errorf("a drag sorted by %v", m.jiraTab.sort)
	}
	view := ansi.Strip(m.View().Content)
	if h := strings.Index(view, "Status"); h < 0 || h > strings.Index(view, "Key") {
		t.Errorf("header not moved:\n%s", view)
	}
	if r := strings.Index(view, "New"); r < 0 || r > strings.Index(view, "ABC-1") {
		t.Errorf("rows not moved:\n%s", view)
	}
}

// TestListGroupScroll: moving up onto a group's first card shows its header.
func TestListGroupScroll(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "t"))
	m = out.(Model)
	for m.jiraTab.sort != jiraSortAssignee {
		out, _ = m.handleJiraKey(keyMsg(t, "s"))
		m = out.(Model)
	}
	m.jiraTab.view.SetHeight(2)
	m.renderJira()
	m.jiraTab.view.SetYOffset(3)
	m.jiraTab.idx = 1 // first Unassigned card, header on line 2
	m.renderJira()
	if got := m.jiraTab.view.YOffset(); got != 2 {
		t.Errorf("offset = %d, want the header's line 2", got)
	}
}

// TestListEpicGroups: sorted by epic, cards head under their epic, those
// without one last.
func TestListEpicGroups(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cards[1].ParentSummary = "Checkout"
	m.jiraTab.cards[2].ParentSummary = "Checkout"
	m.installJiraCards(m.jiraTab.cards, 4, nil, "")
	out, _ := m.handleJiraKey(keyMsg(t, "t"))
	m = out.(Model)
	for m.jiraTab.sort != jiraSortEpic {
		out, _ = m.handleJiraKey(keyMsg(t, "s"))
		m = out.(Model)
	}
	view := ansi.Strip(m.View().Content)
	a, b := strings.Index(view, "── Checkout · 2"), strings.Index(view, "── No epic · 2")
	if a < 0 || b < 0 || a > b {
		t.Errorf("epic groups:\n%s", view)
	}
}

func TestJiraTabCopyMarkedTable(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cards[0].Summary = "First | pipe"
	for _, k := range []string{"t", "x", "x"} {
		out, _ := m.handleKey(keyMsg(t, k))
		m = out.(Model)
	}
	out, cmd := m.handleKey(keyMsg(t, "y"))
	m = out.(Model)
	if cmd == nil || m.status != "copied 2 rows as a markdown table" {
		t.Fatalf("y with marks: status=%q", m.status)
	}
	got := fmt.Sprint(cmd())
	o := m.jiraTab.order
	first, second, third := m.jiraTab.cards[o[0]].Key, m.jiraTab.cards[o[1]].Key, m.jiraTab.cards[o[2]].Key
	if !strings.Contains(got, "| Key | Summary | Status | Assignee | Points |") || strings.Count(got, "\n") != 4 ||
		strings.Index(got, "/browse/"+first+")") > strings.Index(got, "/browse/"+second+")") || strings.Contains(got, "/browse/"+third+")") {
		t.Errorf("table = %q", got)
	}
	if strings.Contains(got, "ABC-1") && !strings.Contains(got, `First \| pipe`) {
		t.Errorf("pipe not escaped: %q", got)
	}
}

// TestJiraTabPin: * on the board pins the card, shown ★ in lanes and list.
func TestJiraTabPin(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "*"))
	m = out.(Model)
	if !m.pins["ABC-1"] || !strings.Contains(ansi.Strip(m.View().Content), "★ ABC-1") {
		t.Fatalf("lanes: pins=%v status %q", m.pins, m.status)
	}
	out, _ = m.handleKey(keyMsg(t, "t"))
	m = out.(Model)
	if !strings.Contains(m.View().Content, "★") {
		t.Error("list: no ★")
	}
	m.openPalette()
	if got := m.jiraPicker.all[0].label; got != "pinned  ABC-1  First  · New" {
		t.Errorf("palette's first row %q", got)
	}
	m.closeJiraPicker()
	out, _ = m.handleKey(keyMsg(t, "*"))
	m = out.(Model)
	if m.pins["ABC-1"] || strings.Contains(m.View().Content, "★") {
		t.Errorf("unpin: pins=%v", m.pins)
	}
}

// TestJiraSwimlanes: s in lanes mode bands the lanes by assignee, then epic,
// then none; the cursor keeps its band across lanes; a click hits a card.
func TestJiraSwimlanes(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "s"))
	m = out.(Model)
	view := ansi.Strip(m.View().Content)
	ada, un := strings.Index(view, "▾ AD Ada · 1"), strings.Index(view, "▾ Unassigned · 3 · 5p")
	if m.status != "swimlanes by assignee" || ada < 0 || un < ada {
		t.Fatalf("status %q, bands at %d, %d:\n%s", m.status, ada, un, view)
	}
	for _, k := range []string{"j", "l"} {
		out, _ = m.handleKey(keyMsg(t, k))
		m = out.(Model)
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-2" {
		t.Errorf("j then l: on %s, want ABC-2 (Unassigned band, In progress)", c.Key)
	}
	// Ada's band: header on body line 1, then ABC-1's key line.
	out, _ = m.Update(tea.MouseClickMsg{X: 2, Y: jiraBodyTop + 2, Button: tea.MouseLeft})
	m = out.(Model)
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-1" || m.jiraTab.drag.key != "ABC-1" {
		t.Errorf("click: on %s, drag %+v", c.Key, m.jiraTab.drag)
	}
	// Dragged onto In progress: the lane lights up, the drop moves it there.
	out, _ = m.Update(tea.MouseMotionMsg{X: m.jiraTab.laneW + 3, Y: jiraBodyTop + 2, Button: tea.MouseLeft})
	m = out.(Model)
	if !m.jiraTab.drag.active || m.jiraTab.drag.over != 1 || m.jiraTab.drag.zone != -1 {
		t.Fatalf("drag = %+v", m.jiraTab.drag)
	}
	out, cmd := m.Update(tea.MouseReleaseMsg{X: m.jiraTab.laneW + 3, Y: jiraBodyTop + 2, Button: tea.MouseLeft})
	if m = out.(Model); cmd == nil || m.jiraTab.drag.key != "" {
		t.Errorf("drop: cmd %v, drag %+v", cmd != nil, m.jiraTab.drag)
	}
	for _, want := range []string{"swimlanes by epic", "swimlanes by priority", "no swimlanes"} {
		out, _ = m.handleKey(keyMsg(t, "s"))
		if m = out.(Model); m.status != want {
			t.Errorf("status %q, want %q", m.status, want)
		}
	}
	if strings.Contains(m.View().Content, "▾") {
		t.Error("bands left after switching off")
	}
}

// TestJiraSwimlanesRemembered: the board's swimlanes come back on a reload.
func TestJiraSwimlanesRemembered(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "s"))
	m = out.(Model)
	m.jiraTab.swim = jiraSortRank
	tt := m.jiraTab
	out, _ = m.handleJiraBoard(jiraBoardMsg{seq: tt.seq, project: "ABC", boards: tt.boards, cfg: tt.cfg, views: tt.views, cards: tt.cards, total: tt.total})
	if m = out.(Model); m.jiraTab.swim != jiraSortAssignee || !strings.Contains(ansi.Strip(m.View().Content), "▾ AD Ada") {
		t.Errorf("swim after reload = %v", m.jiraTab.swim)
	}
}

// TestJiraSwimlaneDropAssigns: a card dropped into another assignee's band
// is assigned to them, and shows there at once.
func TestJiraSwimlaneDropAssigns(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	out, _ := m.handleKey(keyMsg(t, "s"))
	m = out.(Model)
	// Unassigned band: header on body line 6, ABC-3's key line on 7.
	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{X: 2, Y: jiraBodyTop + 7, Button: tea.MouseLeft},
		tea.MouseMotionMsg{X: 2, Y: jiraBodyTop + 3, Button: tea.MouseLeft},
	} {
		out, _ = m.Update(msg)
		m = out.(Model)
	}
	if m.jiraTab.drag.key != "ABC-3" || !m.jiraTab.drag.bandOK {
		t.Fatalf("drag = %+v", m.jiraTab.drag)
	}
	out, cmd := m.Update(tea.MouseReleaseMsg{X: 2, Y: jiraBodyTop + 3, Button: tea.MouseLeft})
	m = out.(Model)
	if cmd == nil || !strings.Contains(ansi.Strip(m.View().Content), "▾ AD Ada · 2") {
		t.Fatalf("drop: cmd %v\n%s", cmd != nil, ansi.Strip(m.View().Content))
	}
	cmd()
	if len(got) != 1 || got[0] != `PUT /rest/api/3/issue/ABC-3/assignee {"accountId":"a1"}` {
		t.Errorf("requests = %q", got)
	}
	// u gives it back to nobody; u again has nothing left to take back.
	out, cmd = m.handleKey(keyMsg(t, "u"))
	m = out.(Model)
	if cmd == nil || !strings.Contains(ansi.Strip(m.View().Content), "▾ Unassigned · 3") {
		t.Fatalf("undo: cmd %v status %q\n%s", cmd != nil, m.status, ansi.Strip(m.View().Content))
	}
	cmd()
	if len(got) != 2 || got[1] != `PUT /rest/api/3/issue/ABC-3/assignee {"accountId":null}` {
		t.Errorf("undo requests = %q", got)
	}
	out, _ = m.handleKey(keyMsg(t, "u"))
	if m = out.(Model); !strings.Contains(m.status, "nothing to undo") {
		t.Errorf("u again: status %q", m.status)
	}
}

// TestJiraSwimlaneFold: z folds the cursor's band to its header and moves
// the cursor on; j/k skip it; Z unfolds all.
func TestJiraSwimlaneFold(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "z"))
	if m = out.(Model); !strings.Contains(m.status, "fold needs swimlanes") {
		t.Errorf("z without swimlanes: %q", m.status)
	}
	for _, k := range []string{"s", "z"} {
		out, _ = m.handleKey(keyMsg(t, k))
		m = out.(Model)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "▸ AD Ada · 1") || strings.Contains(view, "First") {
		t.Fatalf("folded view:\n%s", view)
	}
	if c, _ := m.selectedJiraCard(); c.Key != "ABC-3" {
		t.Errorf("after fold on %s, want ABC-3", c.Key)
	}
	out, _ = m.handleKey(keyMsg(t, "k"))
	if m = out.(Model); m.jiraTab.row != 1 {
		t.Errorf("k went into the fold: row %d", m.jiraTab.row)
	}
	out, _ = m.handleKey(keyMsg(t, "Z"))
	if m = out.(Model); !strings.Contains(ansi.Strip(m.View().Content), "First") {
		t.Error("Z should unfold")
	}
}

// TestJiraBandJump: } { step a swimlane at a time, into the nearest lane
// when the cursor's has none of the band.
func TestJiraBandJump(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "}"))
	if m = out.(Model); !strings.Contains(m.status, "bands need swimlanes") {
		t.Errorf("} without swimlanes: %q", m.status)
	}
	for _, step := range []struct{ key, want string }{
		{"s", "ABC-1"}, {"}", "ABC-3"}, {"}", "ABC-3"}, {"{", "ABC-1"}, {"l", "ABC-2"}, {"{", "ABC-1"},
	} {
		out, _ = m.handleKey(keyMsg(t, step.key))
		m = out.(Model)
		if c, _ := m.selectedJiraCard(); c.Key != step.want {
			t.Fatalf("%s: on %s, want %s", step.key, c.Key, step.want)
		}
	}
	// The list, by assignee: a group at a time.
	for _, k := range []string{"t", "s", "s", "s", "g"} {
		out, _ = m.handleKey(keyMsg(t, k))
		m = out.(Model)
	}
	first, _ := m.selectedJiraCard()
	out, _ = m.handleKey(keyMsg(t, "}"))
	m = out.(Model)
	if c, _ := m.selectedJiraCard(); m.jiraTab.sort != jiraSortAssignee || c.Assignee == first.Assignee {
		t.Errorf("list }: sort %v, from %s to %s", m.jiraTab.sort, first.Key, c.Key)
	}
	out, _ = m.handleKey(keyMsg(t, "{"))
	if m = out.(Model); m.jiraTab.idx != 0 {
		t.Errorf("list {: idx %d, want 0", m.jiraTab.idx)
	}
}

// TestPanelDeployed: the panel names the environment the board's card was
// deployed to.
func TestPanelDeployed(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cards[0].Deploy = "production"
	out, _ := m.openJiraCard()
	m = out.(Model)
	out, _ = m.handleJiraLoaded(jiraLoadedMsg{gen: m.refGen, key: "ABC-1", issue: &jira.Issue{Key: "ABC-1", Summary: "First"}})
	m = out.(Model)
	if got := m.renderJiraIssue(m.jiraIssue, 60); !strings.Contains(ansi.Strip(got), "production") {
		t.Errorf("panel lacks the deployment:\n%s", ansi.Strip(got))
	}
}

// TestPanelTrail: an issue opened from the panel keeps the one before as a
// strip above it; a click on the strip goes back. The board starts afresh.
func TestPanelTrail(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.openJiraCard()
	m = out.(Model)
	out, _ = m.handleJiraLoaded(jiraLoadedMsg{gen: m.refGen, key: "ABC-1", issue: &jira.Issue{Key: "ABC-1", Summary: "First", Status: "New"}})
	m = out.(Model)
	out, _ = m.openJiraKey("ABC-7") // a linked issue
	m = out.(Model)
	out, _ = m.handleJiraLoaded(jiraLoadedMsg{gen: m.refGen, key: "ABC-7", issue: &jira.Issue{Key: "ABC-7", Summary: "Linked"}})
	m = out.(Model)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "↰ ABC-1  New  First") {
		t.Fatalf("no trail strip:\n%s", view)
	}
	y := slices.IndexFunc(strings.Split(view, "\n"), func(l string) bool { return strings.Contains(l, "↰ ABC-1") })
	listW, _ := m.jiraListWidth(m.width)
	out, _ = m.Update(tea.MouseClickMsg{X: listW + 4, Y: y, Button: tea.MouseLeft})
	if m = out.(Model); len(m.refBack) != 0 || m.currentRef().jiraKey != "ABC-1" {
		t.Errorf("click on the strip: back %v, showing %s", m.refBack, m.currentRef().jiraKey)
	}
	out, _ = m.openJiraKey("ABC-7")
	m = out.(Model)
	out, _ = m.openJiraCard()
	if m = out.(Model); len(m.refBack) != 0 {
		t.Errorf("opening from the board kept the trail: %v", m.refBack)
	}
}

// TestPanelClicks: a click selects a field, a second edits it; a click on a
// linked issue opens it, the first joining the trail.
func TestPanelClicks(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.openJiraCard()
	m = out.(Model)
	out, _ = m.handleJiraLoaded(jiraLoadedMsg{gen: m.refGen, key: "ABC-1", issue: &jira.Issue{
		Key: "ABC-1", Summary: "First", Status: "New", Priority: "High",
		Description: "A description long enough to wrap in the panel, " + strings.Repeat("and then some more words ", 8),
		Links:       []jira.Link{{Rel: "blocks", Key: "ABC-9", Summary: "Later"}},
	}})
	m = out.(Model)
	rowOf := func(s string) int {
		return slices.IndexFunc(strings.Split(ansi.Strip(m.View().Content), "\n"), func(l string) bool { return strings.Contains(l, s) })
	}
	listW, _ := m.jiraListWidth(m.width)
	click := func(y int) tea.Cmd {
		t.Helper()
		out, cmd := m.Update(tea.MouseClickMsg{X: listW + 4, Y: y, Button: tea.MouseLeft})
		m = out.(Model)
		m.lastClick.at = time.Time{} // no double-click
		return cmd
	}
	click(rowOf("Priority"))
	if m.panelFieldIdx() != 2 || m.focus != focusRef {
		t.Fatalf("click on Priority: field %d", m.panelFieldIdx())
	}
	click(rowOf("Priority"))
	if !m.jiraPicker.active {
		t.Error("a second click should edit the field")
	}
	m.closeJiraPicker()
	click(rowOf("blocks ABC-9"))
	if m.currentRef().jiraKey != "ABC-9" || len(m.refBack) != 1 {
		t.Errorf("link click: showing %s, trail %v", m.currentRef().jiraKey, m.refBack)
	}
}

// TestLaneCardWidth: a card's lines are exactly the lane's width, however
// long the summary: longer is cut, shorter padded.
func TestLaneCardWidth(t *testing.T) {
	m := jiraTabModel(t)
	for _, sum := range []string{"x", strings.Repeat("a long summary 🚀 ", 20)} {
		c := jira.Card{Key: "ABC-9", Summary: sum, Assignee: "Ada", ParentSummary: strings.Repeat("Epic ", 30)}
		for _, sel := range []bool{false, true} {
			for i, l := range m.jiraLaneCard(c, sel, 30) {
				if w := lipgloss.Width(l); w != 30 {
					t.Errorf("summary %d runes, sel %v: line %d is %d wide", len(sum), sel, i, w)
				}
			}
		}
	}
	t.Cleanup(func() { applyTheme(defaultTheme()) })
	for _, shaded := range []bool{false, true} {
		if shaded {
			out, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{0x20, 0x20, 0x28, 0xff}})
			m = out.(Model)
		}
		rows := strings.Split(m.jiraTab.lanesOut, "\n")
		want := lipgloss.Width(rows[0]) // the lanes' widths, what integer division leaves
		for _, l := range rows {
			if w := lipgloss.Width(l); w != want || w > m.jiraTab.view.Width() {
				t.Errorf("shaded %v: lanes row %d wide, want %d: %q", shaded, w, want, ansi.Strip(l))
			}
		}
	}
}

// TestJiraListSelectedPlain: the selected list row drops its dim and
// accent colours for the selection's own, so status, points and PR read.
func TestJiraListSelectedPlain(t *testing.T) {
	m := jiraTabModel(t)
	c := jira.Card{Key: "ABC-9", Summary: "Pay", Status: "Review", Points: "3", PR: "OPEN"}
	dim := ansiOpenSeq(jiraDimStyle)
	if dim == "" {
		t.Skip("no colours in this environment")
	}
	if row := m.jiraListRow(c, false, listCols{width: 80, key: 6, status: 8}); !strings.Contains(row, dim) {
		t.Fatal("unselected row lost its dim status")
	}
	row := m.jiraListRow(c, true, listCols{width: 80, key: 6, status: 8})
	if strings.Contains(row, dim) || strings.Contains(row, ansiOpenSeq(jiraViewActive)) {
		t.Errorf("selected row keeps other colours: %q", row)
	}
	if plain := ansi.Strip(row); !strings.Contains(plain, "Review") || !strings.Contains(plain, "PR Pay") {
		t.Errorf("selected row = %q", plain)
	}
}

func TestJiraSprintBar(t *testing.T) {
	cards := []jira.Card{{Done: true}, {}, {}, {Done: true}}
	if got := ansi.Strip(jiraSprintBar(cards)); got != "▰▰▰▰▰▱▱▱▱▱ 2/4" {
		t.Errorf("issues = %q", got)
	}
	cards = []jira.Card{{Done: true, Points: "3"}, {Points: "5"}, {Points: "2"}}
	if got := ansi.Strip(jiraSprintBar(cards)); got != "▰▰▰▱▱▱▱▱▱▱ 3/10p" {
		t.Errorf("points = %q", got)
	}
	if jiraSprintBar(nil) != "" {
		t.Error("bar without cards")
	}
	m := jiraTabModel(t) // a sprint view, ABC-4 done
	if !strings.Contains(ansi.Strip(m.View().Content), "▱ 0/5p") {
		t.Errorf("header lacks the bar:\n%s", strings.Split(ansi.Strip(m.View().Content), "\n")[jiraBodyTop-2])
	}
}

func TestWorkdaysLeft(t *testing.T) {
	fri := time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC)
	wed := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if n := workdaysLeft(fri, wed, nil); n != 3 { // fri, mon, tue
		t.Errorf("mon–fri = %d", n)
	}
	sunThu := []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday}
	if n := workdaysLeft(fri, wed, sunThu); n != 3 { // sun, mon, tue
		t.Errorf("sun–thu = %d", n)
	}
}

// TestLaneCategory: a lane's mark follows its cards' status category, else
// its place.
func TestLaneCategory(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cards[1].InProgress = true // ABC-2, In progress
	m.jiraTab.cards[3].Done = true       // ABC-4, Closed
	for l, want := range []string{"new", "indeterminate", "done"} {
		if got := m.laneCategory(l); got != want {
			t.Errorf("lane %d = %s, want %s", l, got, want)
		}
	}
	m.jiraTab.cards = nil
	if got := m.laneCategory(2); got != "done" {
		t.Errorf("empty last lane = %s", got)
	}
	m = jiraTabModel(t)
	if !strings.Contains(ansi.Strip(m.View().Content), "▍ To do") {
		t.Error("lane head lacks its mark")
	}
}

// TestOfflineKeepsBoard: a failed fetch leaves the cards shown and says so
// in the header until a fetch succeeds.
func TestOfflineKeepsBoard(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraCards(jiraCardsMsg{seq: m.jiraTab.seq, err: errors.New("dial tcp: no route to host")})
	m = out.(Model)
	view := ansi.Strip(m.View().Content)
	if len(m.jiraTab.cards) != 4 || !strings.Contains(view, "offline · showing the cached board") || !strings.Contains(view, "First") {
		t.Fatalf("cards %d:\n%s", len(m.jiraTab.cards), view)
	}
	out, _ = m.handleJiraCards(jiraCardsMsg{seq: m.jiraTab.seq, cards: m.jiraTab.cards, total: 4})
	if m = out.(Model); m.jiraTab.offline != "" {
		t.Error("a good fetch left the banner")
	}
}

// TestCompactCards: c draws lane cards on one line with no gap, and a click
// maps to the card on that line.
func TestCompactCards(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "c"))
	m = out.(Model)
	if !m.jiraTab.compact {
		t.Fatal("c did not switch")
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	var first, third string
	for i, l := range lines {
		if strings.Contains(l, "ABC-1") && strings.Contains(l, "First") {
			first, third = l, lines[i+1]
		}
	}
	if first == "" || !strings.Contains(third, "ABC-3") {
		t.Fatalf("to do lane not one card a line:\n%s", strings.Join(lines, "\n"))
	}
	if h := m.hitJira(2, jiraBodyTop+2); h.idx != 0 || h.line != 1 { // the second card in To do
		t.Errorf("hit = %+v", h)
	}
	out, _ = m.handleKey(keyMsg(t, "c"))
	if m = out.(Model); m.jiraTab.compact {
		t.Error("c again should switch back")
	}
}

// TestCardColors: the board's colours draw as a ribbon on cards and rows,
// by type here; off leaves them out.
func TestCardColors(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cards[0].Type = "Bug"
	out, _ := m.handleCardColors(cardColorsMsg{board: m.jiraBoardID(), colors: jira.CardColors{By: "issuetype", Colors: []jira.CardColor{{Value: "bug", Color: "#ff5630"}}}})
	m = out.(Model)
	if m.cardColor(m.jiraTab.cards[0]) != "#ff5630" || m.cardColor(m.jiraTab.cards[1]) != "" {
		t.Fatal("colour by type")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "▌ABC-1") {
		t.Errorf("no ribbon on the card:\n%s", ansi.Strip(m.View().Content))
	}
	m.opts.cardColors = "off"
	if m.cardRibbon(m.jiraTab.cards[0]) != "" {
		t.Error("off still draws")
	}
	m.opts.cardColors = "ribbon"
	out, _ = m.handleCardColors(cardColorsMsg{board: m.jiraBoardID(), colors: jira.CardColors{By: "custom"}, keys: map[string]string{"ABC-2": "#00ff00"}})
	if m = out.(Model); m.cardColor(m.jiraTab.cards[1]) != "#00ff00" {
		t.Error("custom colour by key")
	}
}

// TestBoardRank: J ranks the card after the next one in its lane, which
// swaps them on screen; K on the first card says so.
func TestBoardRank(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = r.Method + " " + r.URL.Path + " " + string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	out, _ := m.handleKey(keyStr("K"))
	if m = out.(Model); m.status != "ABC-1 is ranked first already" {
		t.Errorf("K on the first card: %q", m.status)
	}
	out, cmd := m.handleKey(keyStr("J"))
	m = out.(Model)
	lane := m.jiraTab.lanes[0].cards
	if m.jiraTab.cards[lane[0]].Key != "ABC-3" || m.selectedJiraKey() != "ABC-1" {
		t.Fatalf("lane = %v, selected %s", lane, m.selectedJiraKey())
	}
	out, _ = m.Update(cmd())
	if m = out.(Model); m.status != "ranked ABC-1" {
		t.Errorf("status %q", m.status)
	}
	if body != `PUT /rest/agile/1.0/issue/rank {"issues":["ABC-1"],"rankAfterIssue":"ABC-3"}` {
		t.Errorf("rank = %s", body)
	}

	m.jiraTab.swim = jiraSortAssignee
	out, _ = m.handleKey(keyStr("J"))
	if m = out.(Model); !strings.Contains(m.status, "swimlanes off") {
		t.Errorf("with swimlanes: %q", m.status)
	}
}

// TestBoardRankEnds: alt+j and alt+k rank a card to the bottom and top of
// its lane; u ranks it back beside its old neighbour.
func TestBoardRankEnds(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.jiraTab.cards = append(m.jiraTab.cards, jira.Card{Key: "ABC-5", Summary: "Fifth", StatusID: "1", Status: "New"})
	m.buildJiraLanes()
	lane := func() (keys []string) {
		for _, i := range m.jiraTab.lanes[0].cards {
			keys = append(keys, m.jiraTab.cards[i].Key)
		}
		return keys
	}

	out, cmd := m.handleKey(tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	m = out.(Model)
	if got := strings.Join(lane(), " "); got != "ABC-3 ABC-5 ABC-1" || m.selectedJiraKey() != "ABC-1" {
		t.Fatalf("lane = %s, selected %s", got, m.selectedJiraKey())
	}
	cmd()
	if body != `{"issues":["ABC-1"],"rankAfterIssue":"ABC-5"}` {
		t.Errorf("to the bottom: %s", body)
	}
	out, _ = m.handleKey(tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	if m = out.(Model); m.status != "ABC-1 is ranked last already" {
		t.Errorf("at the bottom: %q", m.status)
	}

	out, cmd = m.handleKey(keyStr("u"))
	m = out.(Model)
	cmd()
	if body != `{"issues":["ABC-1"],"rankBeforeIssue":"ABC-3"}` {
		t.Errorf("undo: %s", body)
	}

	out, cmd = m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt})
	m = out.(Model)
	cmd()
	if got := strings.Join(lane(), " "); got != "ABC-1 ABC-3 ABC-5" || body != `{"issues":["ABC-1"],"rankBeforeIssue":"ABC-3"}` {
		t.Errorf("to the top: lane %s, rank %s", got, body)
	}
}

// TestListDragRank: in the backlog's list sorted by rank, a card dragged up shows
// where it would land and is ranked there on the drop.
func TestListDragRank(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.jiraTab.viewIdx = 1 // the backlog
	m.buildJiraLanes()
	m.renderJira()
	tt := m.jiraTab
	first, last := tt.cards[tt.order[0]].Key, tt.cards[tt.order[len(tt.order)-1]].Key
	yOf := func(i int) int { return jiraBodyTop + 1 + tt.lineOf[i] - tt.view.YOffset() }
	yLast, yFirst := yOf(len(tt.order)-1), yOf(0)
	out, _ := m.Update(tea.MouseClickMsg{X: 3, Y: yLast, Button: tea.MouseLeft})
	m = out.(Model)
	out, _ = m.Update(tea.MouseMotionMsg{X: 3, Y: yFirst, Button: tea.MouseLeft})
	m = out.(Model)
	if !strings.Contains(ansi.Strip(m.jiraTab.view.View()), "┊ "+last) {
		t.Fatalf("no ghost:\n%s", ansi.Strip(m.jiraTab.view.View()))
	}
	out, cmd := m.Update(tea.MouseReleaseMsg{X: 3, Y: yFirst, Button: tea.MouseLeft})
	m = out.(Model)
	if cmd == nil {
		t.Fatal("the drop should rank")
	}
	cmd()
	if got := m.jiraTab.cards[m.jiraTab.order[0]].Key; got != last {
		t.Errorf("first is %s, want %s", got, last)
	}
	if want := `{"issues":["` + last + `"],"rankBeforeIssue":"` + first + `"}`; body != want {
		t.Errorf("rank %s, want %s", body, want)
	}
}

// TestPickersFilter: the board and sprint pickers take a filter as the
// project picker does.
func TestPickersFilter(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.boards = append(m.jiraTab.boards, jira.Board{ID: 2, Name: "Ops kanban", Type: "kanban"})
	out, _ := m.handleKey(keyStr("b"))
	for _, r := range "ops" {
		out, _ = out.(Model).handleKey(keyStr(string(r)))
	}
	m = out.(Model)
	if len(m.jiraPicker.items) != 1 || !strings.Contains(m.jiraPicker.items[0].label, "Ops kanban") {
		t.Errorf("board items = %+v", m.jiraPicker.items)
	}
	m.closeJiraPicker()
	m.openJiraSprintPicker()
	if !m.jiraPicker.filterable {
		t.Error("the sprint picker should filter")
	}
}

// TestQuickFiltersFailed: quick filters that didn't load say so, and that
// the filters left on aren't applied.
func TestQuickFiltersFailed(t *testing.T) {
	m := jiraTabModel(t)
	t0 := m.jiraTab
	out, _ := m.handleJiraBoard(jiraBoardMsg{seq: t0.seq, project: "ABC", boards: t0.boards, cfg: t0.cfg, views: t0.views,
		quickErr: errors.New("timeout"), quickOn: map[int]bool{7: true}, cards: t0.cards, total: len(t0.cards)})
	if m = out.(Model); !strings.HasPrefix(m.statusErr, "quick filters: timeout · the board shows unfiltered") {
		t.Errorf("status %q", m.statusErr)
	}
}

// TestBoardKeysWithoutBoard: b, a and m say why nothing happened.
func TestBoardKeysWithoutBoard(t *testing.T) {
	m := configuredJiraModel(t, "ABC")
	m.jiraTab.project, m.jiraTab.loading = "ABC", true
	m.openJiraAssigneeFilter()
	if m.status != "the board is still loading" {
		t.Errorf("a while loading: %q", m.status)
	}
	m.jiraTab.loading = false
	m.openJiraBoardPicker()
	if !strings.HasPrefix(m.status, "ABC has no boards") {
		t.Errorf("b without boards: %q", m.status)
	}
	out, _ := m.handleJiraKey(keyStr("m"))
	if m = out.(Model); !strings.HasPrefix(m.status, "no board loaded") {
		t.Errorf("m without a board: %q", m.status)
	}
}

// TestAssigneeFilterListsBoardPeople: the filter offers the people with
// cards on the view, not the whole project; narrowed to one person, it
// fetches the view without that filter (quick filters kept), and the late
// rows keep the filter typed meanwhile.
func TestAssigneeFilterListsBoardPeople(t *testing.T) {
	m := jiraTabModel(t)
	if m.openJiraAssigneeFilter() != nil {
		t.Error("unfiltered: the board's cards should be enough")
	}
	if !pickerHasLabel(m, "Ada") || len(m.jiraPicker.items) != 4 {
		t.Fatalf("unfiltered: %+v", m.jiraPicker.items)
	}
	m.closeJiraPicker()

	var gotJQL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/agile/1.0/board/1/sprint/9/issue" {
			gotJQL = r.URL.Query().Get("jql")
			_, _ = w.Write([]byte(`{"total":2,"issues":[{"key":"ABC-1","fields":{"assignee":{"accountId":"a1","displayName":"Ada"}}},{"key":"ABC-5","fields":{"assignee":{"accountId":"b2","displayName":"Bob"}}}]}`))
		}
	}))
	defer srv.Close()
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.jiraTab.assignee = jiraAssignee{id: "me", label: "Me"}
	m.jiraTab.quickOn = map[int]bool{7: true}
	cmd := m.openJiraAssigneeFilter()
	if pickerHasLabel(m, "Bob") {
		t.Fatalf("before the load: %+v", m.jiraPicker.items)
	}
	u, _ := m.handleJiraPickerKey(keyStr("b"))
	m = u.(Model)
	out, _ := m.handleJiraPickerLoaded(cmd().(jiraPickerLoadedMsg))
	m = out.(Model)
	if strings.Contains(gotJQL, "currentUser") || !strings.Contains(gotJQL, "frontend") {
		t.Errorf("refetch jql %q", gotJQL)
	}
	if !pickerHasLabel(m, "Bob") || m.jiraPicker.items[m.jiraPicker.idx].id != "b2" {
		t.Errorf("after the load, filtered to b: %+v at %d", m.jiraPicker.items, m.jiraPicker.idx)
	}
}

// TestCardsReloadDuringBoardLoad: a reload after an edit while another
// board loads leaves that load current, so its reply isn't dropped.
func TestCardsReloadDuringBoardLoad(t *testing.T) {
	m := jiraTabModel(t)
	if m.refreshJiraAfterEdit() == nil {
		t.Fatal("no reload on a loaded board")
	}
	m.jiraTab.loading = false
	m.loadJiraBoard("ABC", 2, "", false)
	seq := m.jiraTab.seq
	if m.refreshJiraAfterEdit() != nil || m.jiraTab.seq != seq {
		t.Errorf("reload superseded the board load: seq %d → %d", seq, m.jiraTab.seq)
	}
}

// TestPanelHintLeavesWithPanel: the panel's key hint is gone from the status
// line once esc closes it.
func TestPanelHintLeavesWithPanel(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "enter"))
	m = out.(Model)
	if !m.refOpen || !strings.Contains(m.status, "esc closes") {
		t.Fatalf("enter: open %v, status %q", m.refOpen, m.status)
	}
	out, _ = m.handleKey(keyMsg(t, "esc"))
	m = out.(Model)
	if m.refOpen || m.status != "" {
		t.Errorf("esc: open %v, status %q", m.refOpen, m.status)
	}
}

// TestTimeMachineFromList: ctrl+t in the list replays the board in lanes;
// esc goes back to the list.
func TestTimeMachineFromList(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "t"))
	m = out.(Model)
	if m.jiraShowsLanes() {
		t.Fatal("t did not switch to the list")
	}
	out, cmd := m.handleJiraKey(keyMsg(t, "ctrl+t"))
	m = out.(Model)
	if cmd == nil || !m.jiraShowsLanes() || m.jiraTab.past == nil {
		t.Fatalf("ctrl+t in the list: lanes %v, past %v, status %q", m.jiraShowsLanes(), m.jiraTab.past != nil, m.status)
	}
	m.closeTimeMachine()
	if m.jiraShowsLanes() {
		t.Error("leaving the time machine kept lanes")
	}
}

// TestListColumnsFrom: ui.list_columns' columns go first, the rest follow
// in the default order; an unknown one warns.
func TestListColumnsFrom(t *testing.T) {
	got, warn := listColumnsFrom([]string{"Summary", "key", "bogus", "key"})
	if want := []string{"summary", "key", "type", "priority", "status", "points", "assignee", "marks"}; !slices.Equal(got, want) || len(warn) != 1 {
		t.Errorf("= %v %v, want %v and one warning", got, warn, want)
	}
}
