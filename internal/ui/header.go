package ui

import (
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/safeterm"
)

// The board's header rows as segments: what each draws and what a click on
// it does, so drawing and the mouse agree.

// headSeg is one run of a header row.
type headSeg struct {
	s     string // as drawn
	kind  string // a click: "view", "quick", "term", "chart", "assignee", "esc", "key", "branch", "" nothing
	i     int    // the view, quick filter, term or chart
	press string // the key a "key" click presses
}

func plainSeg(s string) headSeg { return headSeg{s: s} }

// keySeg presses b's first key; pressSeg presses k.
func keySeg(s string, b key.Binding) headSeg {
	if len(b.Keys()) == 0 {
		return plainSeg(s)
	}
	return pressSeg(s, firstKey(b))
}

func pressSeg(s, k string) headSeg { return headSeg{s: s, kind: "key", press: k} }

func joinSegs(segs []headSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.s)
	}
	return b.String()
}

// jiraTitleSegs is the title row: project, board, freshness, timer, inbox,
// agents and the main keys, each a click on its key.
func (m *Model) jiraTitleSegs() []headSeg {
	t, k := m.jiraTab, m.keys
	dim := refDimStyle.Render
	segs := []headSeg{plainSeg(titleStyle.Render("laneway"))} // the app's name, on Jira and laneway-server alike
	if t.project != "" {
		segs = append(segs, plainSeg(" "), keySeg(titleStyle.Render(t.project), k.Project))
	}
	if t.board < len(t.boards) {
		segs = append(segs, plainSeg(dim("  ")), keySeg(dim(t.boards[t.board].Name), k.Board))
	}
	switch {
	case t.loading:
		segs = append(segs, plainSeg(dim(i18n.Tf("  refreshing…%s", loadingFor(t.loadingSince)))))
	case !t.fetched.IsZero():
		segs = append(segs, plainSeg(dim("  ")), keySeg(dim(i18n.Tf("updated %s", age(t.fetched))), k.Refresh))
		if t.total > len(t.cards) {
			segs = append(segs, plainSeg(dim(i18n.Tf("  ·  first %d of %d", len(t.cards), t.total))))
		}
	}
	if tl := m.timerLabel(); tl != "" {
		segs = append(segs, plainSeg(dim("  ·  ")), keySeg(dim(tl), k.Timer))
	}
	if b := m.queueBadge(); b != "" {
		segs = append(segs, plainSeg(dim("  ·  ")+jiraOverStyle.Render(b)))
	}
	if b := m.inboxBadge(); b != "" {
		segs = append(segs, plainSeg(dim("  ·  ")), keySeg(dim(b+" "+helpKey(k.Inbox)), k.Inbox))
	}
	if b := m.agentsBadge(); b != "" {
		segs = append(segs, plainSeg(dim("  ·  ")), keySeg(dim(b+" "+helpKey(k.Agents)), k.Agents))
	}
	if m.branchKey != "" {
		segs = append(segs, plainSeg(dim("  ·  ")), headSeg{s: dim("⎇ " + m.branchKey), kind: "branch"})
	}
	if m.newRelease != "" {
		segs = append(segs, plainSeg(dim("  ·  ")+jiraOverStyle.Render("↑ "+m.newRelease)))
	}
	segs = append(segs, plainSeg(dim("  ·  ")))
	for i, h := range []struct {
		b    key.Binding
		what string
	}{{k.Help, i18n.T("help")}, {k.Project, i18n.T("project")}, {k.Board, i18n.T("board")}} {
		if i > 0 {
			segs = append(segs, plainSeg(dim("  ")))
		}
		segs = append(segs, keySeg(dim(helpKey(h.b)+" "+h.what), h.b))
	}
	segs = append(segs, plainSeg(dim("  ")), keySeg(dim(helpKey(k.PrevView)), k.PrevView), plainSeg(dim(" ")),
		keySeg(dim(helpKey(k.NextView)), k.NextView), plainSeg(dim(i18n.T(" view"))))
	for _, h := range []struct {
		b    key.Binding
		what string
	}{{k.ToggleMode, i18n.T("lanes/list")}, {k.OpenChannel, i18n.T("open")}, {k.OpenAttach, i18n.T("browser")}, {k.Refresh, i18n.T("refresh")}} {
		segs = append(segs, plainSeg(dim("  ")), keySeg(dim(helpKey(h.b)+" "+h.what), h.b))
	}
	if m.jiraShowsLanes() {
		segs = append(segs, plainSeg(dim(i18n.Tf("  %s/%s move", helpKey(k.MoveCardLeft), helpKey(k.MoveCardRight)))))
	}
	return segs
}

// jiraViewSegs is the views row: the offline notice (retries), the views,
// the sprint's bar (charts), its days and goal (a click shows it whole) and
// which lanes show. The sprint's part keeps its room; views that don't fit
// give way (‹ the one before the first shown, › the one after the last).
func (m *Model) jiraViewSegs() []headSeg {
	t := m.jiraTab
	var head, tail []headSeg
	if t.offline != "" {
		head = append(head, keySeg(jiraOverStyle.Render(i18n.Tf("offline · showing the cached board · %s retries", helpKey(m.keys.Refresh))), m.keys.Refresh), plainSeg("    "))
	}
	if v, ok := m.jiraCurrentView(); ok {
		// A closed sprint's bar would count what is done now; its line says what was.
		if bar := jiraSprintBar(t.cards); v.kind == jiraViewSprint && v.closed.IsZero() && bar != "" {
			tail = append(tail, plainSeg("    "), keySeg(bar, m.keys.Charts))
		}
		if s := jiraSprintLine(v, time.Now(), m.opts.workdays); s != "" {
			tail = append(tail, headSeg{s: jiraDimStyle.Render("    " + s), kind: "goal"})
		}
		if !v.closed.IsZero() {
			tail = append(tail, plainSeg(jiraDimStyle.Render("    "+closedSprintLine(t.cards))))
		}
	}
	if n := len(t.lanes); m.jiraShowsLanes() && n > 0 {
		if vis, _ := jiraLaneLayout(t.view.Width(), n); vis < n {
			tail = append(tail, plainSeg(jiraDimStyle.Render(i18n.Tf("    lanes %d–%d of %d", t.firstLane+1, t.firstLane+vis, n))))
		}
		if h := len(t.cols) - n; h > 0 {
			tail = append(tail, keySeg(jiraDimStyle.Render(i18n.Tf("    %d empty hidden", h)), m.keys.EmptyLanes))
		}
		if t.layout != "" && t.layout == m.jiraLayoutName() {
			s := i18n.Tf("    lanes: %s", t.layout)
			if t.layoutHidden > 0 {
				s = i18n.Tf("    lanes: %s, %d hidden", t.layout, t.layoutHidden)
			}
			tail = append(tail, keySeg(jiraDimStyle.Render(s), m.keys.LaneLayout))
		}
	}
	room := t.viewsW - ansi.StringWidth(joinSegs(head)) - ansi.StringWidth(joinSegs(tail))
	first, last := jiraViewsFit(t.views, t.viewIdx, room)
	segs := head
	if first > 0 {
		segs = append(segs, headSeg{s: jiraDimStyle.Render("‹"), kind: "view", i: first - 1}, plainSeg(jiraDimStyle.Render(jiraViewSep)))
	}
	for i := first; i < last; i++ {
		if i > first {
			segs = append(segs, plainSeg(jiraDimStyle.Render(jiraViewSep)))
		}
		style := jiraDimStyle
		if i == t.viewIdx {
			style = jiraViewActive
		}
		segs = append(segs, headSeg{s: style.Render(t.views[i].name), kind: "view", i: i})
	}
	if last < len(t.views) {
		segs = append(segs, plainSeg(jiraDimStyle.Render(jiraViewSep)), headSeg{s: jiraDimStyle.Render("›"), kind: "view", i: last})
	}
	return append(segs, tail...)
}

// jiraFilterSegs is the filters row: the search (its terms remove one, esc
// all), the assignee, the quick filters, and the clear, sort and search keys.
func (m *Model) jiraFilterSegs() []headSeg {
	t := m.jiraTab
	dim := jiraDimStyle.Render
	chip := func(on bool, s string) string {
		if on {
			return jiraViewActive.Render(s)
		}
		return dim(s)
	}
	var segs []headSeg
	if t.past != nil {
		segs = append(segs, plainSeg(jiraViewActive.Render(t.past.label(time.Now()))), plainSeg("  "))
	}
	switch {
	case t.searching:
		listW, _ := m.jiraListWidth(m.width)
		t.search.SetWidth(min(40, max(listW/3, 10)))
		segs = append(segs, plainSeg(t.search.View()), plainSeg("  "))
	case t.jiraSearchQuery() != "":
		segs = append(segs, plainSeg(dim("/")))
		for i, w := range jiraQueryWords(t.search.Value()) {
			if i > 0 {
				segs = append(segs, plainSeg(" "))
			}
			segs = append(segs, headSeg{s: chip(true, w+" ×"), kind: "term", i: i})
		}
		segs = append(segs, plainSeg(" "), headSeg{s: dim("esc"), kind: "esc"}, plainSeg("  "))
	}
	who := i18n.T("everyone")
	if t.assignee.id != "" {
		who = t.assignee.short()
	}
	segs = append(segs, plainSeg(dim(i18n.Tf("%s assignee (%s me): ", helpKey(m.keys.Assignee), helpKey(m.keys.Mine)))),
		headSeg{s: chip(t.assignee.id != "", who), kind: "assignee"})
	for i, q := range t.quick {
		if i == 9 {
			break
		}
		segs = append(segs, plainSeg("  "), headSeg{s: chip(t.quickOn[q.ID], strconv.Itoa(i+1)+" "+q.Name), kind: "quick", i: i})
	}
	if t.jiraFiltered() {
		segs = append(segs, plainSeg(dim("  ·  ")), keySeg(dim(i18n.Tf("%s clears", helpKey(m.keys.ClearFilters))), m.keys.ClearFilters))
	}
	if t.sort != jiraSortRank && !m.jiraShowsLanes() {
		segs = append(segs, plainSeg(dim("  ·  ")), keySeg(dim(i18n.Tf("%s sort: ", helpKey(m.keys.Sort)))+chip(true, t.sort.String()), m.keys.Sort))
	}
	if !t.searching {
		segs = append(segs, plainSeg(dim("  ·  ")))
		if t.jiraSearchQuery() == "" {
			segs = append(segs, keySeg(dim(i18n.Tf("%s search", helpKey(m.keys.Search))), m.keys.Search), plainSeg(dim("  ")))
		}
		segs = append(segs, keySeg(dim(i18n.Tf("%s filter", helpKey(m.keys.FilterBuilder))), m.keys.FilterBuilder))
	}
	return segs
}

// headerHit is the header segment at x, y; its kind is "" for nothing.
func (m *Model) headerHit(x, y int) headSeg {
	s, _, _ := m.headerSpan(x, y)
	return s
}

// headerSpan is headerHit and the columns the segment covers.
func (m *Model) headerSpan(x, y int) (headSeg, int, int) {
	t := m.jiraTab
	other := t.roadmap != nil || t.plan != nil || t.charts != nil || t.week != nil || t.standup != nil || t.inbox != nil || t.agentsView != nil || t.mrs != nil
	var segs []headSeg
	switch {
	case y == 0 && !other:
		segs = m.jiraTitleSegs()
	case y == jiraBodyTop-2 && t.roadmap != nil:
		segs = m.roadmapSegs()
	case y == jiraBodyTop-2 && t.charts != nil:
		segs = m.chartsSegs()
	case y == jiraBodyTop-2 && (t.week != nil || t.standup != nil || t.inbox != nil || t.agentsView != nil || t.mrs != nil):
		return headSeg{}, 0, 0
	case y == jiraBodyTop-2 && t.plan != nil:
		segs = m.planSegs()
	case y == jiraBodyTop-2:
		segs = m.jiraViewSegs()
	case y == jiraBodyTop-1 && !other:
		if t.searching {
			return headSeg{}, 0, 0
		}
		segs = m.jiraFilterSegs()
	}
	return segSpan(segs, x, 1) // after the box's left border
}

// clickHeader acts on a header hit; ok false when x, y is none.
func (m Model) clickHeader(x, y int) (tea.Model, tea.Cmd, bool) {
	return m.runSeg(m.headerHit(x, y))
}

// runSeg does what a click on segment h does; ok false when nothing.
func (m Model) runSeg(h headSeg) (tea.Model, tea.Cmd, bool) {
	switch h.kind {
	case "view":
		return m, m.cycleJiraView(h.i - m.jiraTab.viewIdx), true
	case "quick":
		return m, m.toggleJiraQuick(h.i), true
	case "term":
		m.removeSearchTerm(h.i)
		return m, nil, true
	case "chart":
		m.jiraTab.charts.tab = h.i
		return m, nil, true
	case "goal":
		if v, ok := m.jiraCurrentView(); ok && strings.TrimSpace(v.goal) != "" {
			m.status = i18n.Tf("goal: %s", safeterm.Line(strings.Join(strings.Fields(v.goal), " ")))
		}
		return m, nil, true
	case "esc":
		m.clearJiraSearch()
		return m, nil, true
	case "branch":
		out, cmd := m.openBranchIssue()
		return out, cmd, true
	case "assignee":
		h = keySeg("", m.keys.Assignee)
	case "key":
	default:
		return m, nil, false
	}
	if h.press == "" {
		return m, nil, true
	}
	out, cmd := m.handleJiraKey(keyPress(h.press))
	return out, cmd, true
}

// segAt is the segment of segs at column x, the row starting at column at.
func segAt(segs []headSeg, x, at int) headSeg {
	s, _, _ := segSpan(segs, x, at)
	return s
}

// segSpan is segAt and the columns the segment covers.
func segSpan(segs []headSeg, x, at int) (headSeg, int, int) {
	for _, s := range segs {
		w := ansi.StringWidth(s.s)
		if x >= at && x < at+w {
			return s, at, at + w
		}
		at += w
	}
	return headSeg{}, 0, 0
}

// labelAt is which of labels, drawn " · " apart from column 0, is under
// col, and its columns; -1 between them.
func labelAt(labels []string, col int) (i, x0, x1 int) {
	at := 0
	for i, l := range labels {
		w := ansi.StringWidth(l)
		if col >= at && col < at+w {
			return i, at, at + w
		}
		at += w + 3
	}
	return -1, 0, 0
}

// hint is a key shown as label that a click presses; a pair has two keys
// before one what ("← → scroll").
type hint struct{ label, press, label2, press2, what string }

func keyHint(b key.Binding, what string) hint {
	return hint{label: helpKey(b), press: firstKey(b), what: what}
}

func firstKey(b key.Binding) string {
	if len(b.Keys()) == 0 {
		return ""
	}
	return b.Keys()[0]
}

// hintSegs lays hints out two cells apart, each key pressing itself.
func hintSegs(hints ...hint) []headSeg {
	dim := jiraDimStyle.Render
	var segs []headSeg
	for _, h := range hints {
		segs = append(segs, plainSeg(dim("  ")), pressSeg(dim(h.label), h.press))
		if h.label2 != "" {
			segs = append(segs, plainSeg(dim(" ")), pressSeg(dim(h.label2), h.press2))
		}
		segs = append(segs, pressSeg(dim(" "+h.what), h.press))
	}
	return segs
}

// roadmapSegs is the roadmap's view line: its keys press as hints.
func (m *Model) roadmapSegs() []headSeg {
	r := m.jiraTab.roadmap
	dim := jiraDimStyle.Render
	s := jiraViewActive.Render(i18n.T("Roadmap")) + dim(i18n.Tf("  %d epics · %s per column", len(r.epics), roadmapZoomName(roadmapZooms[r.zoom])))
	if n := len(r.groups); n > 0 {
		s += dim(i18n.Tf(" · %d parents", n))
	}
	segs := []headSeg{plainSeg(s)}
	switch {
	case r.finding:
		segs = append(segs, plainSeg("  "+r.find.View()))
	case r.filter != "":
		segs = append(segs, plainSeg(dim(i18n.Tf("  /%s (esc clears)", r.filter))))
	}
	switch {
	case r.loading:
		segs = append(segs, plainSeg(dim(i18n.T("  ·  loading…"))))
	case !r.fetched.IsZero():
		segs = append(segs, plainSeg(dim("  ·  ")), keySeg(dim(i18n.Tf("updated %s", age(r.fetched))), m.keys.Refresh))
	}
	k := m.keys
	segs = append(segs, plainSeg(dim("  ·")))
	segs = append(segs, hintSegs(hint{"←", "left", "→", "right", i18n.T("scroll")},
		hint{helpKey(k.ZoomIn), firstKey(k.ZoomIn), helpKey(k.ZoomOut), firstKey(k.ZoomOut), i18n.T("zoom")},
		keyHint(k.Today, i18n.T("today")), keyHint(k.RoadmapFold, i18n.T("children")))...)
	segs = append(segs, plainSeg(dim(i18n.Tf("  %s/%s move  %s %s end  %s grip an end",
		helpKey(k.MoveCardLeft), helpKey(k.MoveCardRight), helpKey(k.EndEarlier), helpKey(k.EndLater), helpKey(k.RoadmapGrip)))))
	return append(segs, hintSegs(keyHint(k.OpenChannel, i18n.T("open")), keyHint(k.CopyKey, i18n.T("copy")), hint{label: "esc", press: "esc", what: i18n.T("board")})...)
}

// chartsSegs is the charts' view line: a chart's name shows it.
func (m *Model) chartsSegs() []headSeg {
	ch := m.jiraTab.charts
	var segs []headSeg
	for n, i := range ch.chartTabsShown() {
		if n > 0 {
			segs = append(segs, plainSeg(jiraDimStyle.Render(chartTabSep)))
		}
		style := jiraDimStyle
		if i == ch.tab {
			style = jiraViewActive
		}
		segs = append(segs, headSeg{s: style.Render(i18n.T(chartTabNames[i])), kind: "chart", i: i})
	}
	if ch.loading {
		segs = append(segs, plainSeg(jiraDimStyle.Render(i18n.T("  ·  loading…"))))
	}
	segs = append(segs, plainSeg(jiraDimStyle.Render("  ·")))
	return append(segs, hintSegs(hint{label: "tab", press: "tab", what: i18n.T("switch")}, keyHint(m.keys.ChartDone, i18n.T("done")), keyHint(m.keys.ChartCompare, i18n.T("compare")),
		keyHint(m.keys.Refresh, i18n.T("refresh")), keyHint(m.keys.CopyKey, i18n.T("copy")), hint{label: "esc", press: "esc", what: i18n.T("board")})...)
}

// planSegs is planning's view line: the sprint's name steps to the next.
func (m *Model) planSegs() []headSeg {
	p := m.jiraTab.plan
	dim := jiraDimStyle.Render
	k := m.keys
	segs := []headSeg{plainSeg(jiraViewActive.Render(i18n.T("Planning")) + dim(i18n.T("  backlog → "))), keySeg(dim(p.sprints[p.target].name), k.NextView)}
	switch {
	case p.finding:
		segs = append(segs, plainSeg("  "+p.find.View()))
	case p.filter != "":
		segs = append(segs, plainSeg(dim(i18n.Tf("  /%s (esc clears)", p.filter))))
	}
	if p.loading {
		segs = append(segs, plainSeg(dim(i18n.T("  ·  loading…"))))
	}
	segs = append(segs, plainSeg(dim("  ·")))
	segs = append(segs, hintSegs(hint{"←", "left", "→", "right", i18n.T("side")},
		hint{helpKey(k.PrevView), firstKey(k.PrevView), helpKey(k.NextView), firstKey(k.NextView), i18n.T("sprint")})...)
	if !p.finding {
		segs = append(segs, plainSeg(dim("  ")), keySeg(dim(i18n.Tf("%s filter", helpKey(k.FilterBuilder))), k.FilterBuilder))
	}
	segs = append(segs, plainSeg(dim(i18n.Tf("  %s/space move across  %s %s rank  %s goal  %s rename  %s new  %s start/end  %s %s complete",
		helpKey(k.MoveSprint), helpKey(k.RankUp), helpKey(k.RankDown), helpKey(k.PlanGoal), helpKey(k.PlanRename), helpKey(k.PlanNew),
		helpKey(k.PlanStart), helpKey(k.PlanComplete), helpKey(k.PlanComplete)))))
	return append(segs, hintSegs(keyHint(k.OpenChannel, i18n.T("open")), hint{label: "esc", press: "esc", what: i18n.T("board")})...)
}
