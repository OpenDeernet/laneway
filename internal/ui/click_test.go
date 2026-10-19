package ui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/jira"
)

// viewLineOf is the screen row of the first line containing s, -1 for none.
func viewLineOf(m Model, s string) int {
	for i, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(l, s) {
			return i
		}
	}
	return -1
}

func click(m Model, x, y int) Model {
	out, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return out.(Model)
}

// TestClickSwimlaneHeader: a click on a band's header folds it, again
// unfolds it.
func TestClickSwimlaneHeader(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "s"))
	m = out.(Model)
	y := viewLineOf(m, "▾ AD Ada")
	if y < 0 {
		t.Fatalf("no Ada band:\n%s", ansi.Strip(m.View().Content))
	}
	if m = click(m, 5, y); viewLineOf(m, "▸ AD Ada · 1") < 0 || viewLineOf(m, "First") >= 0 {
		t.Fatalf("click did not fold:\n%s", ansi.Strip(m.View().Content))
	}
	if c, _ := m.selectedJiraCard(); c.Key == "ABC-1" {
		t.Error("the cursor stayed in the fold")
	}
	if m = click(m, 5, viewLineOf(m, "▸ AD Ada")); viewLineOf(m, "First") < 0 {
		t.Error("click again should unfold")
	}
}

// TestClickAboveBoard: a click on the header's blank space leaves the
// cursor's lane alone.
func TestClickAboveBoard(t *testing.T) {
	m := jiraTabModel(t)
	lane := m.jiraTab.lane
	if m = click(m, m.jiraTab.laneW+3, 1); m.jiraTab.lane != lane {
		t.Errorf("lane %d, want %d", m.jiraTab.lane, lane)
	}
}

// TestClickClosesHelp: any click closes the help overlay.
func TestClickClosesHelp(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "?"))
	if m = click(out.(Model), 3, 3); m.helpOpen {
		t.Error("help still open")
	}
}

// TestClickChartTab: a click on a chart's name in the view line shows it.
func TestClickChartTab(t *testing.T) {
	m := chartsModel(t)
	line := strings.Split(ansi.Strip(m.View().Content), "\n")[jiraBodyTop-2]
	x := ansi.StringWidth(line[:strings.Index(line, "Flow")])
	if m = click(m, x+1, jiraBodyTop-2); m.jiraTab.charts.tab != 2 {
		t.Errorf("tab %d, want flow", m.jiraTab.charts.tab)
	}
	if m = click(m, 0, jiraBodyTop-2); m.jiraTab.charts.tab != 2 {
		t.Error("a click on the border switched")
	}
}

// clickText clicks the first screen cell showing s, failing when none does.
func clickText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for y, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if i := strings.Index(l, s); i >= 0 {
			return click(m, ansi.StringWidth(l[:i]), y)
		}
	}
	t.Fatalf("no %q on screen:\n%s", s, ansi.Strip(m.View().Content))
	return m
}

// TestClickSettings: a click selects a row, another on it edits, outside
// cancels the edit, then closes.
func TestClickSettings(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, ","))
	m = out.(Model)
	i := 5 // custom_fields, a text row
	name := m.settings.rows[i].name
	for j, r := range m.settings.rows {
		if j != i && strings.Contains(r.name, name) {
			t.Fatalf("%s is not a unique name", name)
		}
	}
	if m = clickText(t, m, name); m.settings.idx != i || m.settings.input != nil {
		t.Fatalf("idx %d, want %d, not editing", m.settings.idx, i)
	}
	if m = clickText(t, m, name); m.settings.input == nil {
		t.Fatal("a click on the selected row should edit it")
	}
	if m = click(m, 0, 0); m.settings == nil || m.settings.input != nil {
		t.Fatal("outside should cancel the edit only")
	}
	if m = click(m, 0, 0); m.settings != nil {
		t.Error("outside should close")
	}
}

// TestClickFilterBuilder: a field's click moves on to compare, a value's
// adds the term.
func TestClickFilterBuilder(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "F"))
	m = out.(Model)
	if m = clickText(t, m, "Assignee"); m.builderPick(0).id != "assignee" || m.filterBuilder.col != 1 {
		t.Fatalf("field %q col %d", m.builderPick(0).id, m.filterBuilder.col)
	}
	if m = clickText(t, m, "Ada · 1"); m.jiraTab.search.Value() != "assignee:Ada" {
		t.Errorf("query %q", m.jiraTab.search.Value())
	}
	if m = click(m, 0, 0); m.filterBuilder != nil {
		t.Error("outside should close")
	}
}

// TestClickJQL: a click on a completion takes it; outside cancels.
func TestClickJQL(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "Q"))
	m = out.(Model)
	m.jql.input.SetValue("")
	m.jql.sugg = []string{"assignee = currentUser()", "status = Done"}
	if m = clickText(t, m, "status = Done"); m.jql.input.Value() != "status = Done" {
		t.Errorf("input %q", m.jql.input.Value())
	}
	if m = click(m, 0, 0); m.jql != nil {
		t.Error("outside should cancel")
	}
}

// TestWheelOverlay: the wheel moves the settings cursor, not the board.
func TestWheelOverlay(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, ","))
	out, _ = out.(Model).Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown})
	if m = out.(Model); m.settings.idx != 1 || m.jiraTab.row != 0 {
		t.Errorf("idx %d, board row %d", m.settings.idx, m.jiraTab.row)
	}
}

// TestClickImageView: the right half steps on, the left back, the wheel
// too; a click below the image leaves the viewer.
func TestClickImageView(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraIssue = &jira.Issue{Key: "ABC-1", Attachments: []jira.Attachment{{ID: "10"}, {ID: "11"}}}
	m.images = &panelImages{on: true, maxRows: 16, byAtt: map[string]*panelImage{
		"10": {state: imgReady, id: 1, pxW: 100, pxH: 100},
		"11": {state: imgReady, id: 2, pxW: 100, pxH: 100},
	}}
	m.openImageView()
	mid := m.bodyH() / 2
	if m = click(m, m.width-5, mid); m.imageViewIdx != 1 {
		t.Fatalf("right half: image %d", m.imageViewIdx)
	}
	if m = click(m, 5, mid+1); m.imageViewIdx != 0 {
		t.Fatalf("left half: image %d", m.imageViewIdx)
	}
	out, _ := m.Update(tea.MouseWheelMsg{X: 5, Y: mid, Button: tea.MouseWheelDown})
	if m = out.(Model); m.imageViewIdx != 1 {
		t.Fatalf("wheel: image %d", m.imageViewIdx)
	}
	if m = click(m, 5, m.bodyH()-1); m.imageView {
		t.Error("a click below the image should go back")
	}
}

// TestClickOutsideInputs: outside the go-to and create boxes a click
// cancels them; inside it keeps them.
func TestClickOutsideInputs(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "#"))
	m = out.(Model)
	if !m.jiraGotoActive {
		t.Fatal("# did not open go-to")
	}
	if m = clickText(t, m, "Go to issue"); !m.jiraGotoActive {
		t.Fatal("a click inside closed it")
	}
	if m = click(m, 0, 0); m.jiraGotoActive {
		t.Error("outside should cancel go-to")
	}
}

// TestClickHeaderKeys: the header's key hints, names and chips run their
// key.
func TestClickHeaderKeys(t *testing.T) {
	m := jiraTabModel(t)
	if m = clickText(t, m, "? help"); !m.helpOpen {
		t.Fatal("help chip")
	}
	m.helpOpen = false
	lanes := m.jiraShowsLanes()
	if m = clickText(t, m, "lanes/list"); m.jiraShowsLanes() == lanes {
		t.Error("lanes/list chip did not switch")
	}
	if m = clickText(t, m, "/ search"); !m.jiraTab.searching {
		t.Fatal("search chip")
	}
	out, _ := m.handleKey(keyMsg(t, "f"))
	out, _ = out.(Model).handleKey(keyMsg(t, "enter"))
	if m = clickText(t, out.(Model), "esc"); m.jiraTab.jiraSearchQuery() != "" {
		t.Error("esc chip left the query")
	}
	m.jiraTab.offline = "down"
	m.renderJira()
	if m = clickText(t, m, "offline"); !m.jiraTab.loading {
		t.Error("offline notice did not retry")
	}
}

// TestClickHeaderTimer: the running timer's label stops it into the log
// input.
func TestClickHeaderTimer(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "T"))
	if m = out.(Model); m.timerLabel() == "" {
		t.Fatal("T started no timer")
	}
	if m = clickText(t, m, "⏱"); !m.worklogFromTimer {
		t.Error("a click on the timer should open its log")
	}
}

// TestClickRoadmapFold: a click on an epic's ▸ folds its children out,
// again in; a click on a view line hint presses its key.
func TestClickRoadmapFold(t *testing.T) {
	m := roadmapModel(t)
	y := viewLineOf(m, "▸ ABC-10")
	if y < 0 {
		t.Fatalf("no fold mark:\n%s", ansi.Strip(m.View().Content))
	}
	if m = click(m, 1, y); viewLineOf(m, "ABC-12 Pay") < 0 {
		t.Fatal("click did not fold out")
	}
	if m = click(m, 1, y); viewLineOf(m, "ABC-12 Pay") >= 0 {
		t.Error("click again should fold in")
	}
	zoom := m.jiraTab.roadmap.zoom
	if m = clickText(t, m, "+ - zoom"); m.jiraTab.roadmap.zoom == zoom {
		t.Error("+ hint did not zoom")
	}
	if m = clickText(t, m, "space children"); viewLineOf(m, "ABC-12 Pay") < 0 {
		t.Error("space hint did not fold out")
	}
}

// TestClickPlanSprint: planning's sprint name steps to the next sprint.
func TestClickPlanSprint(t *testing.T) {
	m := planModel(t, &[]string{})
	p := m.jiraTab.plan
	p.sprints = append(p.sprints, p.sprints[0])
	p.sprints[len(p.sprints)-1].name = "Sprint 9"
	m.renderJira()
	target := p.target
	if m = clickText(t, m, p.sprints[target].name); m.jiraTab.plan.target == target {
		t.Error("the name did not step")
	}
}

// TestPickerWrappedTitle: a title wrapped over lines on a narrow terminal
// still maps a click to the row under it.
func TestPickerWrappedTitle(t *testing.T) {
	m := jiraTabModel(t)
	m.openPalette()
	m.jiraPicker.title = strings.Repeat("a long title ", 8)
	m.width = 60
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	want := m.jiraPicker.items[2].label
	y := -1
	for i, l := range lines {
		if strings.Contains(l, want) {
			y = i
			break
		}
	}
	if i, _ := m.pickerRowAt(30, y); i != 2 {
		t.Errorf("row at y %d = %d, want 2", y, i)
	}
}

// TestClickPlanHead: a click on a side's head focuses that side.
func TestClickPlanHead(t *testing.T) {
	m := planModel(t, &[]string{})
	m.jiraTab.plan.side = 0
	if m = click(m, m.width-10, jiraBodyTop); m.jiraTab.plan.side != 1 {
		t.Error("the sprint's head did not take the focus")
	}
}

// panelModel is the board with ABC-1 loaded in the panel: a link, an image
// ready to draw.
func panelModel(t *testing.T) Model {
	t.Helper()
	m := jiraTabModel(t)
	m.images = &panelImages{on: true, maxRows: 16, byAtt: map[string]*panelImage{}}
	iss := &jira.Issue{Key: "ABC-1", Summary: "s", Description: "Words\n\n![shot.png](attachment:10)",
		Attachments: []jira.Attachment{{ID: "10", Filename: "shot.png", MimeType: "image/png"}},
		Links:       []jira.Link{{Rel: "blocks", Key: "ABC-2", Summary: "Second"}}}
	out, _ := openRefFor(m, "ABC-1")
	out, _ = out.(Model).handleJiraLoaded(jiraLoadedMsg{gen: out.(Model).refGen, key: "ABC-1", issue: iss})
	m = out.(Model)
	e := m.images.byAtt["10"]
	out, _ = m.handleImageLoaded(imageLoadedMsg{att: "10", id: e.id, pxW: 40, pxH: 40, cols: 4, rows: 2, seq: "SEQ"})
	return out.(Model)
}

// TestClickPanelExtras: the hint line's keys, the Links head, a double-click
// on the Description head and an inline image each do theirs.
func TestClickPanelExtras(t *testing.T) {
	m := panelModel(t)
	if m = clickText(t, m, "c comment"); !m.jiraCommentActive {
		t.Fatal("hint did not open the composer")
	}
	m.jiraCommentActive = false
	if m = clickText(t, m, "Links (1)"); !m.jiraPicker.active {
		t.Fatal("Links head did not open the link picker")
	}
	m.jiraPicker.active = false
	m.renderRef()
	if m = clickText(t, m, "Description"); strings.Contains(m.status, "description") {
		t.Fatal("one click should not edit")
	}
	if m = clickText(t, m, "Description"); !strings.Contains(m.status, "loading ABC-1 description") {
		t.Fatalf("a double-click should edit the description: %q", m.status)
	}
	if m = clickText(t, m, "\U0010EEEE"); !m.imageView {
		t.Error("a click on the image should show it full size")
	}
}

// TestWheelPanelWhileEditing: with a comment composed in the panel the
// wheel scrolls the panel and leaves the board alone.
func TestWheelPanelWhileEditing(t *testing.T) {
	m := panelModel(t)
	m.jiraIssue.Description = strings.Repeat("line\n\n", 60)
	m.renderRef()
	m.focus = focusRef
	out, _ := m.Update(keyMsg(t, "c"))
	if m = out.(Model); !m.jiraCommentActive || !m.commentInline() {
		t.Fatal("c did not compose in the panel")
	}
	listW, _ := m.jiraListWidth(m.width)
	top := m.refView.YOffset()
	out, _ = m.Update(tea.MouseWheelMsg{X: listW + 5, Y: 5, Button: tea.MouseWheelUp})
	if m = out.(Model); top == 0 || m.refView.YOffset() != top-3 {
		t.Errorf("the panel did not scroll: %d → %d", top, m.refView.YOffset())
	}
	row := m.jiraTab.row
	out, _ = m.Update(tea.MouseWheelMsg{X: 3, Y: 6, Button: tea.MouseWheelDown})
	if m = out.(Model); m.jiraTab.row != row || !m.jiraCommentActive {
		t.Error("the board moved under the edit")
	}
}

// TestMouseOff: ui.mouse off leaves the mouse to the terminal; a bad value
// warns and keeps it on.
func TestMouseOff(t *testing.T) {
	m := jiraTabModel(t)
	if m.View().MouseMode != tea.MouseModeAllMotion {
		t.Fatal("the mouse is on by default")
	}
	o, _ := optionsFrom(config.UIConfig{Mouse: "off"})
	m.opts = o
	if m.View().MouseMode != tea.MouseModeNone {
		t.Error("off still captures the mouse")
	}
	if o, warn := optionsFrom(config.UIConfig{Mouse: "maybe"}); !o.mouse || len(warn) == 0 {
		t.Error("a bad value should warn and keep the mouse")
	}
}

// TestClickEmptyState: an empty board's hint runs its key.
func TestClickEmptyState(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.search.SetValue("zzz")
	m.applyJiraSearch()
	if m = clickText(t, m, "builds a filter"); m.filterBuilder == nil {
		t.Fatal("the hint did not open the builder")
	}
	m.filterBuilder = nil
	if m = clickText(t, m, "esc clears the search"); m.jiraTab.jiraSearchQuery() != "" {
		t.Error("the hint did not clear the search")
	}
}

// TestClickListGroupHeader: a click on a list group's header selects its
// first card.
func TestClickListGroupHeader(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "t"))
	m = out.(Model)
	for m.jiraTab.sort != jiraSortAssignee {
		out, _ = m.handleJiraKey(keyMsg(t, "s"))
		m = out.(Model)
	}
	y := viewLineOf(m, "Ada · 1")
	if y < 0 {
		t.Fatalf("no Ada group:\n%s", ansi.Strip(m.View().Content))
	}
	if m = click(m, 5, y); m.jiraTab.cards[m.jiraTab.order[m.jiraTab.idx]].Key != "ABC-1" {
		t.Errorf("selected %s", m.jiraTab.cards[m.jiraTab.order[m.jiraTab.idx]].Key)
	}
}

// TestPanelScrollbar: a click on the panel's right border jumps there, a
// drag follows, esc puts it back.
func TestPanelScrollbar(t *testing.T) {
	m := panelModel(t)
	m.jiraIssue.Description = strings.Repeat("line\n\n", 80)
	m.renderRef()
	h := m.refView.Height()
	if m = click(m, m.width-1, h); m.refView.YOffset() == 0 || !m.panelScrolling {
		t.Fatalf("a click at the bottom: offset %d", m.refView.YOffset())
	}
	bottom := m.refView.YOffset()
	out, _ := m.Update(tea.MouseMotionMsg{X: m.width - 1, Y: 1, Button: tea.MouseLeft})
	if m = out.(Model); m.refView.YOffset() != 0 {
		t.Errorf("drag to the top: offset %d", m.refView.YOffset())
	}
	out, _ = m.handleKey(keyMsg(t, "esc"))
	if m = out.(Model); m.panelScrolling || m.refView.YOffset() != 0 || bottom == 0 {
		t.Errorf("esc: offset %d, still scrolling %v", m.refView.YOffset(), m.panelScrolling)
	}
}

// TestHelpPages: on a narrow screen help pages its columns; → shows the
// next, another key closes.
func TestHelpPages(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out, _ = out.(Model).handleKey(keyMsg(t, "?"))
	m = out.(Model)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "page 1/") || strings.Contains(view, "story points") {
		t.Fatalf("first page:\n%s", view)
	}
	for m.helpOpen && !strings.Contains(ansi.Strip(m.View().Content), "story points") {
		out, _ = m.handleKey(keyMsg(t, "right"))
		m = out.(Model)
	}
	if !m.helpOpen {
		t.Fatal("→ closed help before the panel's keys")
	}
	for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if ansi.StringWidth(l) > 100 {
			t.Fatalf("a line wider than the screen: %q", l)
		}
	}
	out, _ = m.handleKey(keyMsg(t, "x"))
	if m = out.(Model); m.helpOpen || m.helpPage != 0 {
		t.Error("another key should close and reset")
	}
}

// TestHelpFromPanel: ? in the panel opens help at the panel's keys.
func TestHelpFromPanel(t *testing.T) {
	m := panelModel(t)
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out, _ = out.(Model).handleRefKey(keyMsg(t, "?"))
	if m = out.(Model); !strings.Contains(ansi.Strip(m.View().Content), "story points") {
		t.Errorf("help from the panel opened on page %d", m.helpPage)
	}
}

// TestOverlaysFitWidth: on an 80-column screen no overlay is wider than it.
func TestOverlaysFitWidth(t *testing.T) {
	for _, open := range []string{",", "F", "?", ":", "#"} {
		m := jiraTabModel(t).WithConfigPath("/home/someone/.config/laneway/a-rather-long-config-name.yaml")
		out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		out, _ = out.(Model).handleKey(keyMsg(t, open))
		m = out.(Model)
		for page := range 4 {
			m.helpPage = page // the help's every page
			for _, l := range strings.Split(m.renderOverlay(m.bodyH()), "\n") {
				if w := ansi.StringWidth(l); w > 80 {
					t.Errorf("%s page %d: a line %d wide", open, page, w)
					break
				}
			}
		}
	}
}

// TestCommentKeepsText: esc asks once when you wrote something; a failed
// post keeps the text for c to bring back.
func TestCommentKeepsText(t *testing.T) {
	m := panelModel(t)
	press := func(keys ...string) {
		t.Helper()
		for _, k := range keys {
			out, _ := m.handleKey(keyMsg(t, k))
			m = out.(Model)
		}
	}
	m.focus = focusRef
	press("c", "h", "i", "esc")
	if !m.jiraCommentActive || !strings.Contains(m.status, "esc again discards") {
		t.Fatalf("esc with text: active %v, %q", m.jiraCommentActive, m.status)
	}
	press("esc")
	if m.jiraCommentActive {
		t.Fatal("esc again should discard")
	}
	press("c", "esc")
	if m.jiraCommentActive {
		t.Fatal("esc on an empty comment should close at once")
	}
	out, _ := m.handleJiraMutated(jiraMutatedMsg{key: "ABC-1", field: "comment", err: fmt.Errorf("boom"), text: "my words"})
	if m = out.(Model); !strings.Contains(m.status, "c brings it back") {
		t.Fatalf("status %q", m.status)
	}
	press("c")
	if m.jiraCommentInput.Value() != "my words" {
		t.Errorf("composer holds %q", m.jiraCommentInput.Value())
	}
}

// TestCommentTimedOut: a post Jira answers too late may have landed; c
// brings it back, and posting it again finds it there instead of posting
// a second copy.
func TestCommentTimedOut(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/myself":
			fmt.Fprint(w, `{"accountId":"me1"}`)
		case r.Method == http.MethodPost:
			posts.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done() // stored, but answered too late
		case r.URL.Path == "/rest/api/3/issue/ABC-1/comment":
			fmt.Fprint(w, `{"comments":[{"id":"2","author":{"accountId":"me1"},
				"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"my words"}]}]}}]}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()
	m := panelModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", Timeout: 50 * time.Millisecond})
	m.focus = focusRef
	post := func() jiraMutatedMsg {
		t.Helper()
		m.jiraCommentInput.SetValue("my words")
		out, cmd := m.handleKey(keyMsg(t, "ctrl+s"))
		msg, ok := findMsg[jiraMutatedMsg](cmd)
		if !ok {
			t.Fatal("no post")
		}
		out, _ = out.(Model).handleJiraMutated(msg)
		m = out.(Model)
		return msg
	}
	out, _ := m.handleKey(keyMsg(t, "c"))
	m = out.(Model)
	if post(); !strings.Contains(m.status, "may have posted") {
		t.Fatalf("status %q", m.status)
	}
	out, _ = m.handleKey(keyMsg(t, "c"))
	m = out.(Model)
	if msg := post(); !msg.found || posts.Load() != 1 || !strings.Contains(m.status, "already") {
		t.Errorf("again: found %v, %d posts, %q", msg.found, posts.Load(), m.status)
	}
}

// TestQuitGuard: ctrl+c with a comment you wrote asks once; again quits.
func TestQuitGuard(t *testing.T) {
	m := panelModel(t)
	m.focus = focusRef
	for _, k := range []string{"c", "h", "i"} {
		out, _ := m.handleKey(keyMsg(t, k))
		m = out.(Model)
	}
	out, cmd := m.handleKey(keyPress("ctrl+c"))
	if m = out.(Model); cmd != nil || !strings.Contains(m.status, "your comment is unsent") {
		t.Fatalf("first ctrl+c: %q", m.status)
	}
	_, cmd = m.handleKey(keyPress("ctrl+c"))
	if cmd == nil {
		t.Fatal("second ctrl+c should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("second ctrl+c: not a quit")
	}
	if _, cmd = jiraTabModel(t).handleKey(keyMsg(t, "q")); cmd == nil {
		t.Error("q with nothing unsent should quit at once")
	}
}

// TestMessages: the status line's messages are kept, newest first in the
// palette's messages list, enter copies one; several startup warnings
// show as a count.
func TestMessages(t *testing.T) {
	m := jiraTabModel(t)
	m.startupStatus([]string{"ui.a: bad", "ui.b: worse"})
	if !strings.Contains(m.status, "2 config warnings") {
		t.Fatalf("status %q", m.status)
	}
	m.status = "a long error that the status line cuts"
	out, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = out.(Model)
	m.openPalette()
	i := slices.IndexFunc(m.jiraPicker.items, func(it jiraPickerItem) bool { return it.id == "m:" })
	if i < 0 {
		t.Fatal("no messages row in the palette")
	}
	m.jiraPicker.idx = i
	out, _ = m.handleJiraPickerKey(keyPress("enter"))
	m = out.(Model)
	if m.jiraPicker.kind != jiraPickMessages || len(m.jiraPicker.items) != 3 ||
		!strings.Contains(m.jiraPicker.items[0].label, "a long error") || !strings.Contains(m.jiraPicker.items[2].label, "ui.a: bad") {
		t.Fatalf("messages = %+v", m.jiraPicker.items)
	}
	out, cmd := m.handleJiraPickerKey(keyPress("enter"))
	if m = out.(Model); cmd == nil || m.status != "copied the message" {
		t.Errorf("enter: %q", m.status)
	}
}

// TestQClosesScreens: q on the roadmap, planning or charts closes it as
// esc does, rather than quitting.
func TestQClosesScreens(t *testing.T) {
	for name, m := range map[string]Model{"roadmap": roadmapModel(t), "planning": planModel(t, &[]string{}), "charts": chartsModel(t)} {
		out, cmd := m.handleJiraKey(keyMsg(t, "q"))
		m = out.(Model)
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Errorf("%s: q quit the app", name)
			}
		}
		if tab := m.jiraTab; tab.roadmap != nil || tab.plan != nil || tab.charts != nil {
			t.Errorf("%s: q left it open", name)
		}
	}
}

// TestMutatedStatus: a write's success reads as what it did.
func TestMutatedStatus(t *testing.T) {
	for field, want := range map[string]string{
		"comment": "commented on ABC-1", "worklog": "logged work on ABC-1", "flagged": "flagged ABC-1",
		"flag cleared": "cleared the flag on ABC-1", "links": "ABC-1 links changed", "priority": "ABC-1 priority updated",
	} {
		if got := mutatedStatus("ABC-1", field); got != want {
			t.Errorf("%s: %q, want %q", field, got, want)
		}
	}
}

// TestDownloadDir: ui.download_dir wins, ~ is home, else XDG, else
// ~/Downloads.
func TestDownloadDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("XDG_DOWNLOAD_DIR", "/xdg")
	for in, want := range map[string]string{"~/jira": filepath.Join(home, "jira"), "/tmp/x": "/tmp/x", "": "/xdg"} {
		if got := downloadDir(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	t.Setenv("XDG_DOWNLOAD_DIR", "")
	if got := downloadDir(""); got != filepath.Join(home, "Downloads") {
		t.Errorf("default %q", got)
	}
}

// TestHelpPerScreen: ? on the roadmap, planning or charts opens help at
// that screen's keys.
func TestHelpPerScreen(t *testing.T) {
	for title, m := range map[string]Model{"Roadmap": roadmapModel(t), "Planning": planModel(t, &[]string{}), "Charts": chartsModel(t)} {
		out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		out, _ = out.(Model).handleJiraKey(keyMsg(t, "?"))
		m = out.(Model)
		found := false
		for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			if strings.Contains(l, " "+title+" ") || strings.HasSuffix(strings.TrimRight(l, " │"), title) {
				found = true
			}
		}
		if !m.helpOpen || !found {
			t.Errorf("%s: help at page %d lacks its section", title, m.helpPage)
		}
	}
}

// TestFailStatus: an error shows in the error colour until the next
// message, and is marked in the log.
func TestFailStatus(t *testing.T) {
	m := jiraTabModel(t)
	m.fail("download: boom")
	if !m.statusIsErr() {
		t.Fatal("fail is not an error")
	}
	out, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = out.(Model)
	m.status = "saved"
	if m.statusIsErr() {
		t.Error("a later message is still an error")
	}
	out, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = out.(Model)
	m.openMessages()
	if !strings.Contains(m.jiraPicker.items[1].label, "✗ download: boom") || strings.Contains(m.jiraPicker.items[0].label, "✗") {
		t.Errorf("log = %+v", m.jiraPicker.items)
	}
}

// TestOpenDownload: a saved attachment gets a palette row that opens it.
func TestOpenDownload(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraDownloaded(jiraDownloadedMsg{path: "/tmp/shot.png"})
	if m = out.(Model); !strings.Contains(m.status, "open download") {
		t.Fatalf("status %q", m.status)
	}
	m.openPalette()
	if !slices.ContainsFunc(m.jiraPicker.items, func(it jiraPickerItem) bool { return it.label == "open download  shot.png" }) {
		t.Error("no open download row")
	}
	if _, cmd := m.applyPalette("d:"); cmd == nil {
		t.Error("the row opens nothing")
	}
}

// TestDoubleClickOption: ui.double_click sets the window; out of range warns.
func TestDoubleClickOption(t *testing.T) {
	if o, warn := optionsFrom(config.UIConfig{DoubleClick: "800ms"}); o.doubleClick != 800*time.Millisecond || len(warn) != 0 {
		t.Errorf("800ms: %v %v", o.doubleClick, warn)
	}
	if o, warn := optionsFrom(config.UIConfig{DoubleClick: "5s"}); o.doubleClick != 400*time.Millisecond || len(warn) == 0 {
		t.Errorf("5s: %v %v", o.doubleClick, warn)
	}
}

// TestSilentKeys: with no card, card keys say so; H past the first lane
// says why nothing moved, and L in list mode moves the card a column.
func TestSilentKeys(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "H"))
	if m = out.(Model); !strings.Contains(m.status, "first lane already") {
		t.Errorf("H in the first lane: %q", m.status)
	}
	out, _ = m.handleJiraKey(keyMsg(t, "t"))
	out, _ = out.(Model).handleJiraKey(keyMsg(t, "L"))
	if m = out.(Model); !strings.Contains(m.status, "moving ABC-1 → In progress") {
		t.Errorf("L in list mode: %q", m.status)
	}
	m.jiraTab.search.SetValue("zzz")
	m.applyJiraSearch()
	for _, k := range []string{"enter", "o", "y", "x", "e"} {
		m.status = ""
		out, _ = m.handleJiraKey(keyMsg(t, k))
		if m = out.(Model); m.status != "no card selected" {
			t.Errorf("%s with no card: %q", k, m.status)
		}
	}
}

// TestErrorState: a failed load wraps its error to the board and offers
// what to do, each a click.
func TestErrorState(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	m.jiraTab.err = "jira server 400: " + strings.Repeat("a long reason ", 12)
	m.renderJira()
	view := ansi.Strip(m.View().Content)
	for _, l := range strings.Split(view, "\n") {
		if ansi.StringWidth(l) > 80 {
			t.Fatalf("a line past the screen: %q", l)
		}
	}
	if !strings.Contains(view, "r retries · p picks a project") {
		t.Fatalf("no hints:\n%s", view)
	}
	if m = clickText(t, m, "picks a project"); !m.jiraPicker.active {
		t.Error("the project hint opens no picker")
	}
}

// TestListNav: pickers, settings and the builder page and jump alike;
// while typing only keys that type nothing move the cursor.
func TestListNav(t *testing.T) {
	m := jiraTabModel(t)
	m.openPalette()
	out, _ := m.handleJiraPickerKey(keyPress("pgdown"))
	if m = out.(Model); m.jiraPicker.idx == 0 {
		t.Error("pgdown in the palette did not move")
	}
	out, _ = m.handleJiraPickerKey(keyMsg(t, "G"))
	if m = out.(Model); m.jiraPicker.filter.Value() != "G" {
		t.Error("G in a searchable picker should type")
	}
	m.closeJiraPicker()
	out, _ = m.handleKey(keyMsg(t, ","))
	out, _ = out.(Model).handleKey(keyMsg(t, "G"))
	if m = out.(Model); m.settings.idx != len(m.settings.rows)-1 {
		t.Errorf("G in settings: %d", m.settings.idx)
	}
	out, _ = m.handleKey(keyPress("pgup"))
	if m = out.(Model); m.settings.idx == len(m.settings.rows)-1 {
		t.Error("pgup in settings did not move")
	}
}

// TestNarrowBoxes: at 60 columns the create box, the builder, settings
// while editing and a searchable picker fit the screen.
func TestNarrowBoxes(t *testing.T) {
	setups := map[string]func(m *Model){
		"create": func(m *Model) {
			m.openCreateForm(createSpec{in: jira.NewIssue{Project: "ABC"}, types: []string{"Task"}})
		},
		"builder":  func(m *Model) { m.openFilterBuilder(false) },
		"settings": func(m *Model) { m.openSettings(); m.settings.idx = 3; m.editSetting() },
		"palette":  func(m *Model) { m.openPalette() },
	}
	for name, setup := range setups {
		m := jiraTabModel(t)
		out, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
		m = out.(Model)
		setup(&m)
		for _, l := range strings.Split(m.renderOverlay(m.bodyH()), "\n") {
			if w := ansi.StringWidth(l); w > 60 {
				t.Errorf("%s: a line %d wide", name, w)
				break
			}
		}
	}
}

// TestLanesOffScreen: with lanes past the screen the outer heads say how
// many.
func TestLanesOffScreen(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	m = out.(Model)
	n := len(m.jiraTab.lanes)
	vis, _ := jiraLaneLayout(m.jiraTab.view.Width(), n)
	if vis >= n {
		t.Skipf("all %d lanes fit", n)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), fmt.Sprintf(" %d›", n-vis)) {
		t.Errorf("no count of the lanes to the right:\n%s", ansi.Strip(m.View().Content))
	}
	out, _ = m.handleJiraKey(keyMsg(t, "l"))
	for m = out.(Model); m.jiraTab.firstLane == 0; m = out.(Model) {
		out, _ = m.handleJiraKey(keyMsg(t, "l"))
	}
	if !strings.Contains(ansi.Strip(m.View().Content), fmt.Sprintf("‹%d ", m.jiraTab.firstLane)) {
		t.Errorf("no count of the lanes to the left:\n%s", ansi.Strip(m.View().Content))
	}
}

// TestJQLEnterPast: enter on an empty JQL box runs the highlighted past
// search.
func TestJQLEnterPast(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "Q"))
	m = out.(Model)
	m.jql.input.SetValue("")
	m.jql.sugg, m.jql.idx = []string{"project = A", "project = B"}, 1
	out, cmd := m.handleJQLKey(keyPress("enter"))
	m = out.(Model)
	if v := m.jiraTab.views[len(m.jiraTab.views)-1]; cmd == nil || v.jql != "project = B" {
		t.Errorf("ran %q", v.jql)
	}
}

// TestCreateSummary: a blank summary keeps the form and says so; in a
// sprint view the title names the sprint.
func TestCreateSummary(t *testing.T) {
	m := jiraTabModel(t)
	m.openCreateForm(createSpec{in: jira.NewIssue{Project: "ABC"}, types: []string{"Task"}})
	m.jiraForm.fields[1].val = jira.Value{Text: "   "}
	if cmd := m.submitJiraForm(); cmd != nil || m.jiraForm == nil || !strings.Contains(m.jiraForm.err, "Summary") {
		t.Errorf("blank: sent %v, %q", cmd != nil, m.jiraForm.err)
	}
	for i, v := range m.jiraTab.views {
		if v.kind == jiraViewSprint {
			m.jiraTab.viewIdx = i
			if !strings.Contains(m.jiraCreateTitle(), "→ "+v.name) {
				t.Errorf("title %q", m.jiraCreateTitle())
			}
			return
		}
	}
	t.Skip("no sprint view")
}

// TestLoadingElapsed: a load past 2s shows how long it has taken, and its
// tick stops once it is done.
func TestLoadingElapsed(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.loading, m.jiraTab.loadingSince = true, time.Now().Add(-5*time.Second)
	if !strings.Contains(ansi.Strip(m.View().Content), "refreshing… 5s") {
		t.Errorf("no elapsed time:\n%s", ansi.Strip(m.View().Content))
	}
	if _, cmd := m.handleLoadingTick(); cmd == nil {
		t.Error("the tick stopped while loading")
	}
	m.jiraTab.loading = false
	if _, cmd := m.handleLoadingTick(); cmd != nil {
		t.Error("the tick runs on after the load")
	}
	if loadingFor(time.Now()) != "" {
		t.Error("a fresh load shows a time")
	}
}

// TestListRowTail: a long summary gives way so the row's tail (assignee,
// due) stays on screen.
func TestListRowTail(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.cards[0].Summary = strings.Repeat("very long summary ", 20)
	m.jiraTab.cards[0].Due = time.Now().Add(-48 * time.Hour)
	out, _ := m.handleKey(keyMsg(t, "t"))
	m = out.(Model)
	m.jiraTab.rows = nil
	m.renderJira()
	var row string
	for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(l, "ABC-1 ") {
			row = l
		}
	}
	if !strings.Contains(row, "Ada") || !strings.Contains(row, "…") {
		t.Errorf("row lost its tail: %q", row)
	}
}

// TestOverLimitMark: a lane past its limit says so with ! too.
func TestOverLimitMark(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraTab.lanes[0].max = 1
	m.renderJira()
	if !strings.Contains(ansi.Strip(m.View().Content), "/1!") {
		t.Errorf("no over-limit mark:\n%s", ansi.Strip(m.View().Content))
	}
}

// TestPlainIcons: ui.icons plain draws issue types as letters.
func TestPlainIcons(t *testing.T) {
	t.Cleanup(func() { plainIcons = false })
	o, warn := optionsFrom(config.UIConfig{Icons: "plain"})
	if !o.plainIcons || len(warn) != 0 {
		t.Fatalf("plain: %v %v", o.plainIcons, warn)
	}
	plainIcons = true
	if got := ansi.Strip(jiraTypeIcon("", "Bug")); got != "B" {
		t.Errorf("bug = %q", got)
	}
	if _, warn := optionsFrom(config.UIConfig{Icons: "emoji"}); len(warn) == 0 {
		t.Error("a bad value should warn")
	}
}

// TestPanelFacts: the panel shows when the issue was made and resolved,
// who watches and votes, and its time tracking.
func TestPanelFacts(t *testing.T) {
	m := panelModel(t)
	out, _ := m.handlePanelExtra(panelExtraMsg{key: "ABC-1", facts: jira.Facts{
		Created: time.Now().Add(-3 * 24 * time.Hour), Resolution: "Done", Watchers: 4, Watching: true,
		Spent: 3 * 3600, Estimate: 8 * 3600, Left: 5 * 3600,
	}})
	m = out.(Model)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Created", "3d ago", "Resolved", "Done", "Watchers", "4 (you)", "3h logged · 5h left of 8h"} {
		if !strings.Contains(view, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	if strings.Contains(view, "Votes") {
		t.Error("no votes should show no row")
	}
}

// TestPanelRowWrap: a long value wraps under its value column, not the
// label.
func TestPanelRowWrap(t *testing.T) {
	m := panelModel(t)
	var labels []string
	for i := range 30 {
		labels = append(labels, fmt.Sprintf("label-%02d", i))
	}
	m.jiraIssue.Labels = labels
	m.renderRef()
	lines := strings.Split(ansi.Strip(m.refView.GetContent()), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "Labels:") {
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], strings.Repeat(" ", 10)) || lines[i+1][10] == ' ' {
				t.Errorf("continuation %q", lines[i+1])
			}
			return
		}
	}
	t.Fatal("no Labels row")
}

// TestSortByDates: the list sorts by updated and created (newest first)
// and due (soonest first, none last).
func TestSortByDates(t *testing.T) {
	now := time.Now()
	cards := []jira.Card{
		{Key: "A-1", Updated: now.Add(-time.Hour), Created: now.Add(-48 * time.Hour)},
		{Key: "A-2", Updated: now, Created: now.Add(-72 * time.Hour), Due: now.Add(48 * time.Hour)},
		{Key: "A-3", Updated: now.Add(-2 * time.Hour), Created: now, Due: now.Add(24 * time.Hour)},
	}
	for s, want := range map[jiraSort][]string{jiraSortUpdated: {"A-2", "A-1", "A-3"}, jiraSortCreated: {"A-3", "A-1", "A-2"}, jiraSortDue: {"A-3", "A-2", "A-1"}} {
		order := []int{0, 1, 2}
		s.apply(order, cards)
		var got []string
		for _, i := range order {
			got = append(got, cards[i].Key)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", s, got, want)
		}
	}
}

// TestWorklogDay: a leading day logs work on that day; weekdays look back.
func TestWorklogDay(t *testing.T) {
	now := time.Date(2026, 9, 25, 15, 0, 0, 0, time.Local) // a Friday
	for in, want := range map[string]string{
		"yesterday 2h": "2026-09-24", "fri 1h": "2026-09-25", "mon 1h": "2026-09-21", "2026-09-10 3h": "2026-09-10", "-2d 1h": "2026-09-23",
	} {
		d, rest, ok := worklogDay(in, now)
		if !ok || d.Format(time.DateOnly) != want || !strings.HasSuffix(rest, "h") {
			t.Errorf("%q: %v %q %v, want %s", in, d, rest, ok, want)
		}
	}
	if _, rest, ok := worklogDay("2h fix", now); ok || rest != "2h fix" {
		t.Errorf("a plain time: %q %v", rest, ok)
	}
}

// TestHideEmptyFields: ui.empty_fields hide folds empty fields behind a
// row; a click on it shows them.
func TestHideEmptyFields(t *testing.T) {
	m := panelModel(t)
	m.opts.hideEmpty, m.moreFields = true, true // in an open More
	full := jiraFormField{FieldMeta: jira.FieldMeta{ID: "c1", Name: "Team", Kind: jira.KindText}, val: jira.Value{Text: "Web"}}
	empty := jiraFormField{FieldMeta: jira.FieldMeta{ID: "c2", Name: "Sprint goal note", Kind: jira.KindText}}
	out, _ := m.handlePanelExtra(panelExtraMsg{key: "ABC-1", fields: []jiraFormField{full, empty}})
	m = out.(Model)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Team") || strings.Contains(view, "Sprint goal note") || !strings.Contains(view, "1 empty fields") {
		t.Fatalf("folded:\n%s", view)
	}
	if m = clickText(t, m, "1 empty fields"); !strings.Contains(ansi.Strip(m.View().Content), "Sprint goal note") {
		t.Error("the click did not show them")
	}
}

// TestStartWrites: with ui.start_assigns and ui.start_status, start work
// assigns you and moves the issue; a move with a screen is left to s.
func TestStartWrites(t *testing.T) {
	var writes []string
	screen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/myself":
			io.WriteString(w, `{"accountId":"me-1","displayName":"Me"}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/transitions"):
			fmt.Fprintf(w, `{"transitions":[{"id":"21","name":"Start","hasScreen":%v,"to":{"id":"3","name":"In Progress"}}]}`, screen)
		default:
			b, _ := io.ReadAll(r.Body)
			writes = append(writes, r.Method+" "+r.URL.Path+" "+string(b))
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.opts.startAssigns, m.opts.startStatus = true, "in progress"
	msg := m.startWrites("ABC-1")().(startWritesMsg)
	if msg.err != nil || len(writes) != 2 || !strings.Contains(writes[0], "assignee") || !strings.Contains(writes[1], `"id":"21"`) {
		t.Fatalf("writes %q, err %v", writes, msg.err)
	}
	out, _ := m.handleStartWrites(msg)
	if m = out.(Model); m.status != "ABC-1 assigned to you, moved to In Progress" {
		t.Errorf("status %q", m.status)
	}
	writes, screen = nil, true
	m.opts.startAssigns = false
	if msg = m.startWrites("ABC-1")().(startWritesMsg); msg.err == nil || len(writes) != 0 {
		t.Errorf("a screen: writes %q, err %v", writes, msg.err)
	}
	m.opts.startStatus = ""
	if m.startWrites("ABC-1") != nil {
		t.Error("with neither set it writes")
	}
}

// TestWorklogEditMovesDay: editing an entry with a day first moves it
// there, from ui.workday_start.
func TestWorklogEditMovesDay(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
		}
		io.WriteString(w, `{"worklogs":[],"issues":[]}`)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.opts, _ = optionsFrom(config.UIConfig{WorkdayStart: "08:30"})
	m.openWorklogInput("ABC-1", "", time.Time{})
	m.worklogEdit, m.worklogEditDay = "77", time.Now()
	_, cmd := m.applyWorklog("yesterday 1h fixed it")
	cmd()
	y := time.Now().AddDate(0, 0, -1)
	if !strings.Contains(body, `"started":"`+y.Format("2006-01-02")+`T08:30`) || !strings.Contains(body, `"timeSpentSeconds":3600`) {
		t.Errorf("PUT body %s", body)
	}
}

// TestRuleActed: a rule's Jira action says what it did, or why not, by
// rule and issue.
func TestRuleActed(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleRuleActed(ruleActedMsg{rule: "Stale review", key: "ABC-1", done: "→ Done"})
	if m = out.(Model); m.status != `rule "Stale review": ABC-1 → Done` {
		t.Errorf("done: %q", m.status)
	}
	out, _ = m.handleRuleActed(ruleActedMsg{rule: "Stale review", key: "ABC-1", err: fmt.Errorf("no move")})
	if m = out.(Model); !m.statusIsErr() || m.status != `rule "Stale review": ABC-1: no move` {
		t.Errorf("failed: %q", m.status)
	}
}

// TestQuickEditTitle: a quick edit's input names its issue.
func TestQuickEditTitle(t *testing.T) {
	m := jiraTabModel(t)
	m.quickKey = "ABC-1"
	m.openBulkInput("labels", "labels")
	if m.jiraFieldKey != "ABC-1" {
		t.Errorf("title key %q", m.jiraFieldKey)
	}
}

// TestScreenErrors: a failed roadmap load wraps its error with what to do;
// an empty roadmap says how to add an epic and pick the type.
func TestScreenErrors(t *testing.T) {
	m := roadmapModel(t)
	m.jiraTab.roadmap.err = "jira server 400: " + strings.Repeat("reason ", 40)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "r retries · esc back to the board") {
		t.Errorf("no hints:\n%s", view)
	}
	m.jiraTab.roadmap.err, m.jiraTab.roadmap.epics = "", nil
	if view = ansi.Strip(m.View().Content); !strings.Contains(view, "No open epics in ABC") || !strings.Contains(view, "ui.roadmap_epic_type") {
		t.Errorf("empty roadmap:\n%s", view)
	}
}

// TestPlanFeedback: x says how many are marked, y copies the sprint, an
// empty side says how to fill it.
func TestPlanFeedback(t *testing.T) {
	m := planModel(t, &[]string{})
	p := m.jiraTab.plan
	p.side = 0
	out, _ := m.handleJiraKey(keyMsg(t, "x"))
	if m = out.(Model); len(p.sides[0]) > 0 && !strings.Contains(m.status, "1 marked") {
		t.Errorf("x: %q", m.status)
	}
	out, cmd := m.handleJiraKey(keyMsg(t, "y"))
	if m = out.(Model); cmd == nil || !strings.Contains(m.status, "as a markdown table") {
		t.Errorf("y: %q", m.status)
	}
	p.sides[1] = []jira.Card{}
	if !strings.Contains(ansi.Strip(m.View().Content), "moves cards here") {
		t.Errorf("empty side:\n%s", ansi.Strip(m.View().Content))
	}
}

// TestHistoryEnter: enter in H goes on to the panel's History tab.
func TestHistoryEnter(t *testing.T) {
	m := panelModel(t)
	m.startJiraPicker(jiraPickHistory, "History — ABC-1", true)
	m.setJiraPickerItems([]jiraPickerItem{{id: "0", label: "status: To Do → Done"}})
	out, _ := m.applyJiraPick()
	if m = out.(Model); m.jiraPicker.active || m.activityTab != activityHistory || m.focus != focusRef {
		t.Errorf("active %v, tab %d, focus %v", m.jiraPicker.active, m.activityTab, m.focus)
	}
}

// TestDevInfoCopy: ctrl+y in D copies a row's branch; enter on a row
// without a link says so.
func TestDevInfoCopy(t *testing.T) {
	m := panelModel(t)
	m.startJiraPicker(jiraPickDev, "Development — ABC-1", true)
	m.setJiraPickerItems([]jiraPickerItem{{id: "https://git/pr/1", label: "OPEN  Fix  (issue/ABC-1-fix)", value: "issue/ABC-1-fix"}, {label: "commit abc"}})
	out, cmd := m.handleJiraPickerKey(keyPress("ctrl+y"))
	if m = out.(Model); cmd == nil || m.status != "copied issue/ABC-1-fix" {
		t.Errorf("ctrl+y: %q", m.status)
	}
	m.jiraPicker.idx = 1
	out, _ = m.applyJiraPick()
	if m = out.(Model); !m.jiraPicker.active || m.status != "no link for this row" {
		t.Errorf("enter without a link: %q", m.status)
	}
}

// TestBurnIssues: a sprint without points burns down by issue count.
func TestBurnIssues(t *testing.T) {
	start := time.Now().AddDate(0, 0, -3)
	v := jiraView{name: "Sprint 1", start: start, end: start.AddDate(0, 0, 14)}
	issues := []jira.BurnIssue{{Resolved: start.Add(time.Hour)}, {}, {}}
	got := ansi.Strip(renderBurndown(v, issues, chartLines{}, time.Now(), 80, 20))
	if !strings.Contains(got, "2 of 3 issues left") {
		t.Errorf("burndown:\n%s", got)
	}
	if got := ansi.Strip(renderBurnup(v, issues, chartLines{}, time.Now(), 80, 20)); !strings.Contains(got, "1 of 3 issues done") {
		t.Errorf("burnup:\n%s", got)
	}
}

// TestChartsNoSprint: tab with no active sprint says why nothing changes.
func TestChartsNoSprint(t *testing.T) {
	m := chartsModel(t)
	m.jiraTab.charts.sprint, m.jiraTab.charts.tab = nil, chartVelocity
	out, _ := m.handleJiraKey(keyMsg(t, "tab"))
	if m = out.(Model); m.status != "no active sprint: velocity only" {
		t.Errorf("status %q", m.status)
	}
}

// TestChartsHalfLoaded: a failed velocity leaves the burndown showing, its
// tab alone saying what failed.
func TestChartsHalfLoaded(t *testing.T) {
	m := chartsModel(t)
	ch := m.jiraTab.charts
	burn := ch.burn
	ch.seq++
	out, _ := m.handleCharts(chartsMsg{seq: ch.seq, burn: burn, velErr: fmt.Errorf("velocity down")})
	m = out.(Model)
	ch.tab = chartBurndown
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "velocity down") || !strings.Contains(view, "left") {
		t.Errorf("burndown:\n%s", view)
	}
	ch.tab = chartVelocity
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "velocity down") {
		t.Errorf("velocity tab lacks its error:\n%s", view)
	}
}

// TestChartTableIssues: an unpointed sprint's burndown table counts issues.
func TestChartTableIssues(t *testing.T) {
	m := chartsModel(t)
	ch := m.jiraTab.charts
	ch.tab = chartBurndown
	for i := range ch.burn {
		ch.burn[i].Points = 0
	}
	if got := m.chartTable(time.Now()); !strings.Contains(got, "Issues left") {
		t.Errorf("table:\n%s", got)
	}
}

// TestPlanHalfLoaded: a failed sprint side shows its error beside the
// backlog, and moves wait for a retry.
func TestPlanHalfLoaded(t *testing.T) {
	m := planModel(t, &[]string{})
	p := m.jiraTab.plan
	p.seq++
	out, _ := m.handlePlan(planMsg{seq: p.seq, left: []jira.Card{{Key: "ABC-1", Summary: "First"}}, errs: [2]error{nil, fmt.Errorf("sprint down")}})
	m = out.(Model)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "sprint down") || !strings.Contains(view, "ABC-1") {
		t.Fatalf("half loaded:\n%s", view)
	}
	p.side = 0
	out, cmd := m.handleJiraKey(keyMsg(t, "space"))
	if m = out.(Model); cmd != nil || !strings.Contains(m.status, "retries before moving") {
		t.Errorf("move: %q", m.status)
	}
}

// TestReviewFixes: the builder keeps a field picked through its filter; a
// paste reaches go-to; messages copies the row picked though the log
// shifted; a held site switch doesn't fire on a later quit.
func TestReviewFixes(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleKey(keyMsg(t, "F"))
	m = out.(Model)
	for _, r := range "repo" {
		out, _ = m.handleKey(keyMsg(t, string(r)))
		m = out.(Model)
	}
	out, _ = m.handleKey(keyPress("enter"))
	if m = out.(Model); m.builderPick(0).id != "reporter" {
		t.Errorf("builder field %q after the filter", m.builderPick(0).id)
	}
	m.filterBuilder = nil

	out, _ = m.handleKey(keyMsg(t, "#"))
	out, _ = out.(Model).Update(tea.PasteMsg{Content: "https://x.atlassian.net/browse/ABC-9"})
	if m = out.(Model); !strings.Contains(m.jiraGotoInput.Value(), "ABC-9") {
		t.Errorf("go-to after a paste: %q", m.jiraGotoInput.Value())
	}
	m.jiraGotoActive = false

	out, _ = m.applyMessage(jiraPickerItem{id: "0", value: "the one picked"})
	if m = out.(Model); m.status != "copied the message" {
		t.Errorf("messages: %q", m.status)
	}

	m.site = ""
	m.quitAsked = false
	var mm Model
	out, _ = m.pickSite("club")
	mm = out.(Model)
	if _, switched := mm.NextSite(); !switched {
		t.Error("with nothing unsent the pick should switch")
	}
}

// TestPanelHintsRebound: the panel's hint line shows and presses the keys
// as bound.
func TestPanelHintsRebound(t *testing.T) {
	m := panelModel(t)
	m.keys.applyKeys(map[string]config.KeyList{"comment": {"K"}})
	m.renderRef()
	if m = clickText(t, m, "K comment"); !m.jiraCommentActive {
		t.Error("the rebound comment hint did not open the composer")
	}
}

// TestCopyRow: ctrl+y in the history copies the row as read.
func TestCopyRow(t *testing.T) {
	m := jiraTabModel(t)
	m.startJiraPicker(jiraPickHistory, "History", true)
	m.setJiraPickerItems([]jiraPickerItem{{id: "ABC-1", label: "  @ 2h  Ada  ABC-1 First — commented"}})
	out, cmd := m.handleJiraPickerKey(keyPress("ctrl+y"))
	if m = out.(Model); cmd == nil || m.status != "copied @ 2h  Ada  ABC-1 First — commented" {
		t.Errorf("status %q", m.status)
	}
}

// TestClickDescriptionEdit: one click on the Description heading's hint
// edits it; with none, the heading still shows, to add one. The text under
// it keeps its own clicks.
func TestClickDescriptionEdit(t *testing.T) {
	m := panelModel(t)
	x, y := screenAt(t, m, "E edit")
	if got := hovered(m, x, y); got != "E edit" {
		t.Errorf("hover on the hint = %q", got)
	}
	if m := clickText(t, m, "E edit"); !strings.Contains(m.status, "loading ABC-1 description") {
		t.Fatalf("a click on the hint should edit: %q", m.status)
	}
	if m := clickText(t, m, "Words"); strings.Contains(m.status, "description") {
		t.Error("a click on the text should not edit")
	}
	m.jiraIssue.Description = ""
	m.renderRef()
	if m := clickText(t, m, "E add one"); !strings.Contains(m.status, "loading ABC-1 description") {
		t.Fatalf("with none, the hint should add one: %q", m.status)
	}
}

// TestPanelEpicChildren: an epic's panel lists its children open ones first
// under a progress count; L offers them, a click opens one.
func TestPanelEpicChildren(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := openRefFor(m, "ABC-1")
	out, _ = out.(Model).handleJiraLoaded(jiraLoadedMsg{gen: out.(Model).refGen, key: "ABC-1", issue: &jira.Issue{Key: "ABC-1", Summary: "s", Type: "Epic"}})
	out, _ = out.(Model).handlePanelExtra(panelExtraMsg{key: "ABC-1", children: []jira.Child{
		{Key: "ABC-2", Summary: "Shipped", Status: "Done", Done: true},
		{Key: "ABC-3", Summary: "Pending", Status: "Open", Assignee: "Ann"},
	}})
	m = out.(Model)
	body := strings.Join(m.panelPlain, "\n")
	head := strings.Index(body, "Children (1/2 done)")
	open, done := strings.Index(body, "ABC-3 Pending · Open · Ann"), strings.Index(body, "ABC-2 Shipped")
	if head < 0 || open < head || done < open {
		t.Fatalf("panel:\n%s", body)
	}
	m.openJiraLinkPicker()
	if !slices.ContainsFunc(m.jiraPicker.items, func(it jiraPickerItem) bool { return it.id == "ABC-3" }) {
		t.Errorf("picker = %+v", m.jiraPicker.items)
	}
	m.jiraPicker.active = false
	m.renderRef()
	if m = clickText(t, m, "ABC-3 Pending"); m.currentRef().jiraKey != "ABC-3" {
		t.Errorf("click opened %q", m.currentRef().jiraKey)
	}
}

// TestPinnedAndMoreFields: pinned fields and filled rich text show, the
// rest fold under More (enter on it opens them); * on a field pins it in
// the state file; a read-only one says so instead of editing.
func TestPinnedAndMoreFields(t *testing.T) {
	m := panelModel(t)
	tester := jiraFormField{FieldMeta: jira.FieldMeta{ID: "c1", Name: "Tester", Kind: jira.KindText}, val: jira.Value{Text: "Ada"}}
	legacy := jiraFormField{FieldMeta: jira.FieldMeta{ID: "c2", Name: "Legacy ref", Kind: jira.KindText, ReadOnly: true}, val: jira.Value{Text: "X-1"}}
	notes := jiraFormField{FieldMeta: jira.FieldMeta{ID: "c3", Name: "Test notes", Kind: jira.KindDoc}, val: jira.Value{Text: "check **it**"}}
	out, _ := m.handlePanelExtra(panelExtraMsg{key: "ABC-1", fields: []jiraFormField{tester, legacy, notes}})
	m = out.(Model)
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Tester") || strings.Contains(view, "Legacy ref") || !strings.Contains(view, "More fields") || !strings.Contains(view, "Test notes") {
		t.Fatalf("folded:\n%s", view)
	}
	// the cursor: past the own fields, Test notes (filled rich text), then More
	if rowNamed(m, moreFieldsName) != rowNamed(m, "Test notes")+1 {
		t.Fatalf("rows: %d, %d", rowNamed(m, moreFieldsName), rowNamed(m, "Test notes"))
	}
	m.fieldCursor, m.fieldCursorKey = rowNamed(m, moreFieldsName), "ABC-1"
	out, _ = m.handleRefKey(keyMsg(t, "enter"))
	m = out.(Model)
	if !strings.Contains(ansi.Strip(m.View().Content), "Tester") {
		t.Fatal("enter on More did not open it")
	}
	m.fieldCursor = rowNamed(m, "Legacy ref")
	if out, _ = m.handleRefKey(keyMsg(t, "enter")); !strings.Contains(out.(Model).status, "no one edit") {
		t.Errorf("read-only: %q", out.(Model).status)
	}
	m.fieldCursor = rowNamed(m, "Tester")
	out, _ = m.handleRefKey(keyMsg(t, "*"))
	m = out.(Model)
	if got := jira.FieldPins(m.store); len(got) != 1 || !got["c1"] || m.panelFieldSel() != "Tester" {
		t.Fatalf("pins %v, cursor on %q", got, m.panelFieldSel())
	}
	m.moreFields = false
	m.renderRef()
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Tester:") || strings.Contains(view, "Legacy ref") {
		t.Errorf("pinned folded:\n%s", view)
	}
}

// TestEarlyExtraFields: the remembered screen draws with the issue, before
// editmeta; enter waits for it; editmeta then takes over.
func TestEarlyExtraFields(t *testing.T) {
	m := panelModel(t)
	m.fieldPins = map[string]bool{"c1": true}
	m.jiraIssue.Screen = []jira.FieldMeta{{ID: "c1", Name: "Tester", Kind: jira.KindText}}
	m.jiraIssue.ScreenValues = map[string]jira.Value{"c1": {Text: "Ada"}}
	m.panelExtraKey = ""
	m.earlyExtra()
	m.renderRef()
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Tester:") || !strings.Contains(view, "Ada") {
		t.Fatalf("early:\n%s", view)
	}
	m.fieldCursor, m.fieldCursorKey = rowNamed(m, "Tester"), "ABC-1"
	if out, _ := m.handleRefKey(keyMsg(t, "enter")); !strings.Contains(out.(Model).status, "a moment") {
		t.Errorf("enter before editmeta: %q", out.(Model).status)
	}
	out, _ := m.handlePanelExtra(panelExtraMsg{key: "ABC-1", fields: []jiraFormField{{FieldMeta: jira.FieldMeta{ID: "c1", Name: "Tester", Kind: jira.KindText}, val: jira.Value{Text: "Ada"}}}})
	if m = out.(Model); m.panelExtraEarly {
		t.Error("editmeta did not take over")
	}
}
