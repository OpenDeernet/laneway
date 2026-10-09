package ui

import (
	"context"
	"fmt"
	"hash/fnv"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/index"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/lanes"
	"github.com/cornedor/laneway/internal/safeterm"
	"github.com/cornedor/laneway/internal/viewport"
)

// The board: one board of one project, as swim lanes or a list, with the
// selected issue opening in the reference panel on the right.

// jiraMetaPrefix keys the tab's remembered choices in the store's meta table:
// project, board:<project>, mode, assignee and quick:<board>.
const jiraMetaPrefix = "jira_tab:"

// Card geometry in the lane view: three lines and a gap, or compact (c)
// one line and none.
const (
	jiraCardH    = 3
	jiraCardSlot = jiraCardH + 1
	jiraLaneMinW = 22
)

// cardH and cardSlot are a lane card's lines, and with its gap.
func (m *Model) cardH() int {
	if m.jiraTab.compact {
		return 1
	}
	return jiraCardH
}

func (m *Model) cardSlot() int {
	if m.jiraTab.compact {
		return 1
	}
	return jiraCardSlot
}

// cardLines are a lane card's lines: three, or one when compact (key and
// marks, the avatar, the summary).
func (m *Model) cardLines(c jira.Card, styled bool) []string {
	lines := cardLinesFor(c, styled, m.opts.fields, m.opts.layout, m.cardLook(c))
	if tm := m.timerMark(c.Key); tm != "" {
		lines[0] += " " + tm
	}
	if !m.jiraTab.compact {
		return lines
	}
	chip := ""
	if m.opts.fields.avatar && c.Assignee != "" {
		chip = jiraInitials(c.Assignee) + " "
		if styled {
			chip = jiraAvatar(c.Assignee) + " "
		}
	}
	return []string{joinSides(lines[0]) + " " + chip + c.Summary}
}

// cardLook is what ui.card_styles make of c, against the query env of the
// last render.
func (m *Model) cardLook(c jira.Card) cardLook {
	if len(m.opts.cardStyles) == 0 {
		return cardLook{}
	}
	env := m.jiraTab.styleEnv
	if env.now.IsZero() {
		env.now = time.Now()
	}
	return cardLookOf(c, m.opts.cardStyles, env)
}

const jiraCompactMeta = jiraMetaPrefix + "compact"

// toggleCompact switches lane cards between three lines and one,
// remembered.
func (m *Model) toggleCompact() {
	t := m.jiraTab
	t.compact = !t.compact
	if m.store != nil {
		if t.compact {
			_ = m.store.SetMeta(jiraCompactMeta, "1")
		} else {
			_ = m.store.DeleteMeta(jiraCompactMeta)
		}
	}
	m.status = i18n.T("cards: full")
	if t.compact {
		m.status = i18n.T("cards: one line")
	}
	m.renderJira()
}

const jiraEmptyLanesMeta = jiraMetaPrefix + "empty_lanes"

// jiraHidesEmpty is whether the board hides its empty lanes: as last
// toggled, else ui.empty_lanes.
func (m *Model) jiraHidesEmpty() bool {
	if v := m.jiraTab.emptyLanes; v != "" {
		return v == "hide"
	}
	return m.opts.hideLanes
}

// toggleEmptyLanes hides the board's empty lanes or shows them again,
// remembered.
func (m *Model) toggleEmptyLanes() {
	t := m.jiraTab
	keep := m.selectedJiraKey()
	if m.jiraHidesEmpty() {
		t.emptyLanes = "show"
	} else {
		t.emptyLanes = "hide"
	}
	if m.store != nil {
		_ = m.store.SetMeta(jiraEmptyLanesMeta, t.emptyLanes)
	}
	m.buildJiraLanes()
	m.selectJiraKey(keep)
	m.status = i18n.T("empty lanes: shown")
	if t.emptyLanes == "hide" {
		m.status = i18n.T("empty lanes: hidden")
		if n := len(t.cols) - len(t.lanes); n > 0 {
			m.status = i18n.Tf("empty lanes: %d hidden", n)
		}
	}
	m.renderJira()
}

// jiraLayoutKey remembers a board's lane layout by name.
func jiraLayoutKey(board int) string {
	return jiraMetaPrefix + "layout:" + strconv.Itoa(board)
}

// jiraSite is the Jira the board is on, as a lane layout's Site names it.
func (m *Model) jiraSite() string { return lanes.Site(m.jiraClient.BaseURL()) }

// jiraLayout is the board's chosen lane layout while it still fits.
func (m *Model) jiraLayout() (config.LaneLayout, bool) {
	t := m.jiraTab
	if t.layout == "" || t.cfg == nil {
		return config.LaneLayout{}, false
	}
	fit := lanes.Fitting(m.opts.laneLayouts, m.jiraSite(), m.jiraBoardID(), t.cfg.Columns)
	i := slices.IndexFunc(fit, func(l config.LaneLayout) bool { return l.Name == t.layout })
	if i < 0 {
		return config.LaneLayout{}, false
	}
	return fit[i], true
}

// jiraLayoutName is the lane layout the board shows, "" for its columns.
func (m *Model) jiraLayoutName() string {
	l, _ := m.jiraLayout()
	return l.Name
}

// cycleJiraLayout switches the board to its next fitting lane layout, after
// the last back to its own columns, remembered per board.
func (m *Model) cycleJiraLayout() {
	t := m.jiraTab
	if t.cfg == nil {
		return
	}
	fit := lanes.Fitting(m.opts.laneLayouts, m.jiraSite(), m.jiraBoardID(), t.cfg.Columns)
	if len(fit) == 0 {
		m.status = i18n.T("no lane layout fits this board: add one to ui.lane_layouts")
		return
	}
	next := fit[0].Name
	if cur, ok := m.jiraLayout(); ok {
		i := slices.IndexFunc(fit, func(l config.LaneLayout) bool { return l.Name == cur.Name })
		next = ""
		if i+1 < len(fit) {
			next = fit[i+1].Name
		}
	}
	keep := m.selectedJiraKey()
	t.layout = next
	if m.store != nil {
		_ = m.store.SetMeta(jiraLayoutKey(m.jiraBoardID()), next)
	}
	t.laneTop = nil
	m.buildJiraLanes()
	m.selectJiraKey(keep)
	m.status = i18n.T("lanes: the board's columns")
	if next != "" {
		m.status = i18n.Tf("lanes: %s", next)
	}
	m.renderJira()
}

// jiraBodyTop is the screen row of the first body line: the title row, its
// rule, the view selector and the filter line.
const jiraBodyTop = 4

type jiraViewKind int

const (
	jiraViewBoard jiraViewKind = iota // a kanban board
	jiraViewSprint
	jiraViewBacklog
	jiraViewJQL // a ui.views entry: the board's issues narrowed by jql
	// jiraViewFilter is a starred Jira filter (or a service desk queue):
	// its own search, any board.
	jiraViewFilter
)

// jiraView is one issue list of a board: a sprint, the kanban board, the
// backlog, or a configured JQL view. lanes is whether it may show as swim lanes; planning lists
// (backlog, future sprints) are list-only.
type jiraView struct {
	kind   jiraViewKind
	name   string
	sprint int
	jql    string
	lanes  bool
	// A sprint's dates (zero until planned) and goal.
	start, end time.Time
	goal       string
	// doneDays is how long a kanban board shows done work (0: the default).
	doneDays int
	// closed is when a closed sprint closed (closed_sprint.go); zero for a
	// view that is not one.
	closed time.Time
}

// jiraLane is a board column and the cards (indexes into cards) in it.
type jiraLane struct {
	name      string
	statusIDs []string
	cards     []int
	max       int // the column's WIP limit, 0 for none
	col       int // its index in cols
	// sections are a layout lane's columns, stacked; nil for a lane of one.
	sections []jiraSection
}

// jiraSection is a column stacked in a lane: its cards are the lane's
// cards[first:first+n].
type jiraSection struct {
	name      string
	statusIDs []string
	first, n  int
}

// jiraAssignee is the board's assignee filter: id "" for everyone, else
// one or more (comma-joined, as the state file keeps it) of "me", "none"
// for unassigned and accountIds; label is their names, ", "-joined.
type jiraAssignee struct {
	id    string
	label string
}

func (a jiraAssignee) ids() []string {
	if a.id == "" {
		return nil
	}
	return strings.Split(a.id, ",")
}

// short is the label for the header: past two names, the first and how
// many more.
func (a jiraAssignee) short() string {
	if names := strings.Split(a.label, ", "); len(names) > 2 {
		return fmt.Sprintf("%s +%d", names[0], len(names)-1)
	}
	return a.label
}

// jiraFilterJQL is the JQL the filters narrow a view by: the assignee and
// every quick filter that is on, all of which must hold (as on Jira's board).
func jiraFilterJQL(a jiraAssignee, quick []jira.QuickFilter, on map[int]bool) string {
	var parts, in []string
	none := false
	for _, id := range a.ids() {
		switch id {
		case "me":
			in = append(in, "currentUser()")
		case "none":
			none = true
		default:
			in = append(in, fmt.Sprintf("%q", id))
		}
	}
	var who string
	switch {
	case len(in) == 1:
		who = "assignee = " + in[0]
	case len(in) > 1:
		who = "assignee in (" + strings.Join(in, ", ") + ")"
	}
	switch {
	case none && who != "":
		parts = append(parts, "("+who+" OR assignee is EMPTY)")
	case none:
		parts = append(parts, "assignee is EMPTY")
	case who != "":
		parts = append(parts, who)
	}
	for _, q := range quick {
		if on[q.ID] {
			parts = append(parts, "("+q.JQL+")")
		}
	}
	return strings.Join(parts, " AND ")
}

// withLocalQuick puts the config's presets (negative ids) before the
// board's quick filters, replacing any already there.
func withLocalQuick(board, local []jira.QuickFilter) []jira.QuickFilter {
	out := append([]jira.QuickFilter(nil), local...)
	for _, q := range board {
		if q.ID >= 0 {
			out = append(out, q)
		}
	}
	return out
}

// andJQL joins two JQL clauses, either of which may be empty.
// andOrderedJQL is andJQL for a query that may end in ORDER BY, which
// stays last.
func andOrderedJQL(q, b string) string {
	where, order := q, ""
	if i := strings.LastIndex(strings.ToUpper(q), "ORDER BY"); i >= 0 {
		where, order = strings.TrimSpace(q[:i]), " "+q[i:]
	}
	if where == "" {
		return strings.TrimSpace(b + order)
	}
	return andJQL(where, b) + order
}

func andJQL(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return "(" + a + ") AND (" + b + ")"
}

// jiraDrag is a card being dragged between lanes with the mouse. A click arms
// it (key set); it goes active once the pointer moves past a small threshold.
type jiraDrag struct {
	active bool
	key    string
	x, y   int // where the click armed it
	from   int
	over   int
	zone   int // the status zone under the pointer, in a lane of several
	// slot is where in the lane under the pointer the card would land,
	// counted among its other cards; ok over a lane of one status, with
	// the swimlanes off.
	slot   int
	slotOK bool
	// band is a card of the swimlane under the pointer, ok when over one.
	band   jira.Card
	bandOK bool
}

type jiraTabState struct {
	styleEnv jiraQueryEnv   // what ui.card_styles queries compare against, from the last render
	view     viewport.Model // the list mode's rows
	project  string
	projects []jira.Project // the picker's list, fetched once
	boards   []jira.Board
	board    int // index into boards
	cfg      *jira.BoardConfig
	views    []jiraView
	// rulesSeen is each board, view and filter's last fresh cards, for rules.
	rulesSeen map[string][]jira.Card
	// highlights are cards a rule marked, by key: the colour, until opened.
	highlights map[string]string
	// undo is the session's changes to take back, the latest last: moves,
	// band drops, field, bulk, sprint and comment changes (edit_undo.go).
	undo []undoStep
	// undoing is a way back running: the change it makes is not a new step.
	undoing bool
	// undoneMove is the card an undo is moving back, for its status.
	undoneMove string
	// marked are the cards a bulk edit applies to (bulk.go), by key, as they
	// were when marked: the way back for those another view has unloaded.
	marked    map[string]jira.Card
	viewIdx   int
	wantLanes bool // the user's mode; a list-only view overrides it
	modeRead  bool // wantLanes and the assignee were restored from the store

	assignee jiraAssignee
	quick    []jira.QuickFilter
	quickOn  map[int]bool // quick filter id → on
	// statusNames names status ids, for a lane's drop zones.
	statusNames map[string]string
	// pendingMove is a keyboard move into a lane of several statuses,
	// waiting on the status picker.
	pendingMove struct {
		key  string
		lane int
	}

	cards []jira.Card
	total int
	lanes []jiraLane
	// cols are every column, lanes the ones shown: all, or with empty
	// lanes hidden (emptyLanes "hide", else ui.empty_lanes) those holding
	// a card. Moves address cols, so H and L reach a hidden lane.
	cols       []jiraLane
	emptyLanes string
	// arrange is the arrange mode's cursor (lanearrange.go), nil when off.
	arrange *jiraArrange
	// secFold are the stacked lanes' folded sections (secFoldKey).
	secFold map[string]bool
	// layout is the board's ui.lane_layouts entry by name, "" for its own
	// columns; layoutHidden counts the cards in the columns it hides.
	layout       string
	layoutHidden int
	// laneAt is each shown lane's body lines' card rows, -1 for a section
	// header or a gap, as the last render drew them.
	laneAt [][]int
	order  []int // the list mode's row order, indexes into cards
	// rows caches the list mode's unselected rows, per order, for rowsFor;
	// buildJiraLanes drops it.
	rows    []string
	rowsFor listCols
	// lineOf is each order entry's line in the list, which group headers
	// (sorted by assignee or priority) push down.
	lineOf []int

	idx       int   // list cursor, into order
	lane, row int   // lane cursor
	laneTop   []int // per lane, the first card on screen
	firstLane int   // the first lane on screen
	laneW     int   // a lane's width in the last render
	lanesOut  string
	// empty is an empty board's hint line, for the mouse; row -1 for none.
	empty emptyHint
	// confetti is the lane head celebrating a card into done (delight.go).
	confetti confetti
	// past replays the board on an earlier day, nil for now (timemachine.go).
	past *timeMachine
	// pastFromList is a time machine opened from the list: leaving it goes
	// back there.
	pastFromList bool
	// closedSprints is the board's closed sprints, as the closed sprint
	// picker last fetched them; closedFrom the view a closed one returns to.
	closedSprints []jira.Sprint
	closedFrom    int

	sort jiraSort // the list's order; lanes keep the board's rank
	desc bool     // the list's order reversed
	// sorts is each view's sort by name, so one view's s (or my work's
	// grouping) stays with it.
	sorts map[string]viewSort
	// listHead is the list's column header line, headAt its sortable cells
	// and headCols its columns; headDrag a press on it.
	listHead string
	headAt   []headCell
	headCols []headCell
	headDrag headDrag
	// swim groups the lanes into swimlanes by assignee, epic or priority (jiraSortRank
	// for none); swimTop is its first line on screen, swimAt each body
	// line's card row per lane (-1 for none), for the mouse.
	swim    jiraSort
	swimTop int
	swimAt  [][]int
	// swimBand is each body line's band, as one of its cards; swimHead
	// the band's name on its header line, "" elsewhere.
	swimBand []jira.Card
	swimHead []string
	// swimFold are the folded bands, by name, until the swimlanes change.
	swimFold map[string]bool

	// search narrows the cards locally; searching while it has the keyboard.
	search    textinput.Model
	searching bool
	// colors are the board's card colours (colorsBoard's), colorKeys the
	// issues a custom colour's JQL took.
	colors      jira.CardColors
	colorKeys   map[string]string
	colorsBoard int
	// compact draws lane cards on one line (c), remembered.
	compact bool
	// offline is why the last fetch failed while cached cards stay shown,
	// "" once one succeeds.
	offline string
	// viewsW is the views row's width, as last drawn (for clicks).
	viewsW int
	// fullAt and fullKey are when and for which view and filters the cards
	// were last fetched whole; an idle refresh within ui.full_refresh of it
	// fetches only what changed (loadJiraDelta).
	fullAt  time.Time
	fullKey string
	// roadmap and plan show in place of the cards while non-nil
	// (roadmap.go, planning.go).
	roadmap *roadmapState
	plan    *planState
	charts  *chartsState  // charts.go
	week    *weekState    // week.go
	standup *standupState // standup_screen.go
	inbox   *inboxScreen  // inbox.go
	// agentsView is the agents screen (agents_screen.go).
	agentsView *agentsScreen
	mrs        *mrScreen // mr_screen.go
	// planSeq and chartsSeq outlive a close, so a reply for a view since
	// closed never matches the one reopened.
	planSeq, chartsSeq int

	drag    jiraDrag
	loading bool
	// loadingSince is when the load under way started, for how long it takes.
	loadingSince time.Time
	err          string
	seq          int
	boardSeq     int // the seq of the last whole-board load
	fresh        int // the last seq the network answered, which a cached copy can't undo
	fetched      time.Time
}

// newJiraTabState is held by pointer, like the GitLab tab's.
func newJiraTabState() *jiraTabState {
	return &jiraTabState{view: viewport.New(), wantLanes: true}
}

// jiraBoardMsg carries a full board load: project, boards, columns, views and
// the selected view's cards. Stale responses (seq behind) are dropped.
type jiraBoardMsg struct {
	seq      int
	cached   bool // from the store, the network copy still coming
	project  string
	boards   []jira.Board
	board    int
	cfg      *jira.BoardConfig
	views    []jiraView
	viewIdx  int
	lanes    *bool // the stored mode, on the first load
	quick    []jira.QuickFilter
	quickErr error // the quick filters didn't load: the board shows without them
	quickOn  map[int]bool
	assignee jiraAssignee
	// statusNames names status ids, for the drop zones.
	statusNames map[string]string
	cards       []jira.Card
	total       int
	err         error
}

// jiraCardsMsg carries one view's cards, after a view switch or a move.
type jiraCardsMsg struct {
	seq    int
	cached bool
	delta  bool     // only the cards updated since the last fetch
	gone   []string // with delta: loaded cards that left the view
	// full is a delta that couldn't tell what left: fetch the whole view.
	full    bool
	viewIdx int
	cards   []jira.Card
	total   int
	err     error
	// note says the cards came from the index, offline (indexJQL).
	note string
}

// jiraMovedMsg reports a card's transition to another lane.
type jiraMovedMsg struct {
	key  string
	lane string
	err  error
}

// enterJiraTab focuses the board and refetches when it is missing or older
// than the stale_after option.
func (m *Model) enterJiraTab() tea.Cmd {
	m.focus = focusJira
	t := m.jiraTab
	if t.loading || (t.cfg != nil && time.Since(t.fetched) < m.opts.staleAfter) {
		return nil
	}
	// Nothing on screen yet: show the stored copy while the fresh one loads.
	return m.loadJiraBoard(t.project, m.jiraBoardID(), m.jiraViewName(), t.cfg == nil)
}

func (m *Model) jiraBoardID() int {
	t := m.jiraTab
	if t.board >= 0 && t.board < len(t.boards) {
		return t.boards[t.board].ID
	}
	return 0
}

func (m *Model) jiraViewName() string {
	if v, ok := m.jiraCurrentView(); ok {
		return v.name
	}
	return ""
}

func (m *Model) jiraCurrentView() (jiraView, bool) {
	t := m.jiraTab
	if t.viewIdx >= 0 && t.viewIdx < len(t.views) {
		return t.views[t.viewIdx], true
	}
	return jiraView{}, false
}

// jiraShowsLanes reports whether the board shows as swim lanes right now.
func (m *Model) jiraShowsLanes() bool {
	v, ok := m.jiraCurrentView()
	return ok && v.lanes && (m.jiraTab.wantLanes || !v.closed.IsZero())
}

// loadJiraBoard fetches a board from scratch. An empty project falls back to
// the remembered one, then the first configured, then the first visible;
// board 0 to the remembered board of the project, then its first; view keeps
// a view by name across a refresh, and an empty one opens the board's last.
// fromCache shows the stored copy of the board first.
func (m *Model) loadJiraBoard(project string, boardID int, view string, fromCache bool) tea.Cmd {
	t := m.jiraTab
	t.seq++
	t.boardSeq = t.seq
	t.loading, t.loadingSince = true, time.Now()
	t.err = ""
	m.renderJira()
	seq, ctx, st, c := t.seq, m.ctx, m.store, m.jiraClient
	configured := m.jiraProjects
	readMode := !t.modeRead
	assignee, quickOn, quickBoard, local, localViews := t.assignee, t.quickOn, m.jiraBoardID(), m.opts.quick, append(slices.Clone(m.opts.views), m.savedJQLViews()...)
	withSaved, withQuick, doneDays, remember := m.opts.savedFilters, m.opts.boardQuick, m.opts.kanbanDoneDays, m.opts.rememberFilters
	var cached tea.Cmd
	if fromCache {
		cached = jiraBoardFromCache(st, seq, project, boardID, view, configured, readMode, remember)
	}
	return tea.Batch(cached, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, c.Scaled(60*time.Second))
		defer cancel()
		msg := jiraBoardMsg{seq: seq}
		if readMode {
			msg.lanes = storedJiraMode(st)
			if v, ok, _ := st.GetMeta(jiraMetaPrefix + "assignee"); ok && remember {
				id, label, _ := strings.Cut(v, "\t")
				assignee = jiraAssignee{id: id, label: label}
			}
		}
		project, boardID = resolveJiraBoard(st, project, boardID, configured)
		if project == "" {
			ps, err := c.ListProjects(ctx)
			if err != nil {
				msg.err = err
				return msg
			}
			if len(ps) == 0 {
				msg.err = fmt.Errorf("%s", i18n.T("jira: no projects visible"))
				return msg
			}
			project = ps[0].Key
		}
		msg.project = project
		boards, err := c.Boards(ctx, project)
		if err != nil {
			msg.err = err
			return msg
		}
		if len(boards) == 0 {
			msg.err = fmt.Errorf("%s", i18n.Tf("jira: %s has no boards", project))
			return msg
		}
		msg.boards = boards
		for i, b := range boards {
			if b.ID == boardID {
				msg.board = i
			}
		}
		board := boards[msg.board]
		// What the board is made of, fetched side by side; only the columns
		// are essential.
		var (
			cfg                *jira.BoardConfig
			sprints            []jira.Sprint
			cfgErr, sprintsErr error
			wg                 sync.WaitGroup
		)
		var saved []jira.QuickFilter
		var queues []jira.Queue
		wg.Add(5)
		go func() {
			defer wg.Done()
			if withSaved {
				saved, _ = c.FavouriteFilters(ctx) // views are extras: a failure just leaves them out
				queues, _ = c.DeskQueues(ctx)
			}
		}()
		go func() { defer wg.Done(); cfg, cfgErr = c.BoardConfiguration(ctx, board.ID) }()
		go func() {
			defer wg.Done()
			if board.Type == "scrum" {
				sprints, sprintsErr = c.Sprints(ctx, board.ID)
			}
		}()
		go func() {
			defer wg.Done()
			if withQuick {
				msg.quick, msg.quickErr = c.QuickFilters(ctx, board.ID)
			}
		}()
		go func() { defer wg.Done(); msg.statusNames, _ = c.StatusNames(ctx) }()
		wg.Wait()
		if err := firstErr(cfgErr, sprintsErr); err != nil {
			msg.err = err
			return msg
		}
		msg.cfg = cfg
		if board.Type == "scrum" {
			for _, s := range sprints {
				msg.views = append(msg.views, jiraView{kind: jiraViewSprint, name: s.Name, sprint: s.ID, lanes: s.State == "active",
					start: s.Start, end: s.End, goal: s.Goal})
			}
			msg.views = append(msg.views, jiraView{kind: jiraViewBacklog, name: i18n.T("Backlog")})
		} else {
			msg.views = append(msg.views, jiraView{kind: jiraViewBoard, name: "Board", lanes: true, doneDays: doneDays})
			if kanbanBacklog(cfg) >= 0 {
				msg.views = append(msg.views, jiraView{kind: jiraViewBacklog, name: i18n.T("Backlog")})
			}
		}
		msg.views = append(msg.views, localViews...)
		for _, f := range saved {
			msg.views = append(msg.views, jiraView{kind: jiraViewFilter, name: f.Name, jql: f.JQL})
		}
		for _, q := range queues {
			msg.views = append(msg.views, jiraView{kind: jiraViewFilter, name: q.Label(), jql: q.JQL})
		}
		if view == "" {
			view, _, _ = st.GetMeta(jiraViewKey(board.ID))
		}
		for i, v := range msg.views {
			if v.name == view {
				msg.viewIdx = i
			}
		}
		if board.ID != quickBoard {
			quickOn = map[int]bool{}
			if v, ok, _ := st.GetMeta(jiraMetaPrefix + "quick:" + strconv.Itoa(board.ID)); ok && remember {
				for _, f := range strings.Split(v, ",") {
					if id, err := strconv.Atoi(f); err == nil {
						quickOn[id] = true
					}
				}
			}
		}
		msg.assignee, msg.quickOn = assignee, quickOn
		msg.quick = withLocalQuick(msg.quick, local)
		filter := jiraFilterJQL(assignee, msg.quick, quickOn)
		msg.cards, msg.total, msg.err = fetchJiraView(ctx, c, board.ID, cfg, msg.views[msg.viewIdx], filter)
		_ = st.SetMeta(jiraMetaPrefix+"project", project)
		_ = st.SetMeta(jiraMetaPrefix+"board:"+project, strconv.Itoa(board.ID))
		if msg.err == nil {
			saveJiraCache(st, board.ID, msg.views[msg.viewIdx].name, cacheOf(msg, filter))
		}
		return msg
	})
}

// kanbanBacklog is the index of a kanban board's backlog column (Jira names it
// "Backlog" when the board has one), or -1.
func kanbanBacklog(cfg *jira.BoardConfig) int {
	if cfg != nil && len(cfg.Columns) > 0 && strings.EqualFold(cfg.Columns[0].Name, "backlog") {
		return 0
	}
	return -1
}

// jiraKanbanJQL hides what Jira's own kanban board hides: work done more than
// days (two weeks by default) ago.
func jiraKanbanJQL(days int) string {
	if days < 1 || days > 365 {
		days = defaultKanbanDoneDays
	}
	return fmt.Sprintf("statusCategory != Done OR updated >= -%dd", days)
}

const defaultKanbanDoneDays = 14

// KanbanIssues is a kanban board as Jira shows it, narrowed by filter: work
// done more than days ago and its backlog column left out. The backlog goes
// in the JQL, so the card limit counts only cards that show.
func KanbanIssues(ctx context.Context, c *jira.Client, board int, cfg *jira.BoardConfig, days int, filter, pointsField string) ([]jira.Card, int, error) {
	q := jiraKanbanJQL(days)
	if i := kanbanBacklog(cfg); i >= 0 && len(cfg.Columns[i].StatusIDs) > 0 {
		q = andJQL(q, "status not in ("+strings.Join(cfg.Columns[i].StatusIDs, ", ")+")")
	}
	return c.BoardIssues(ctx, board, andJQL(q, filter), pointsField)
}

func fetchJiraView(ctx context.Context, c *jira.Client, board int, cfg *jira.BoardConfig, v jiraView, filter string) ([]jira.Card, int, error) {
	switch v.kind {
	case jiraViewSprint:
		return c.SprintIssues(ctx, board, v.sprint, filter, cfg.PointsField)
	case jiraViewBacklog:
		return c.BacklogIssues(ctx, board, filter, cfg.PointsField)
	case jiraViewJQL:
		return c.BoardIssues(ctx, board, andOrderedJQL(v.jql, filter), cfg.PointsField)
	case jiraViewFilter:
		return c.SearchCardsTotal(ctx, andOrderedJQL(v.jql, filter))
	}
	return KanbanIssues(ctx, c, board, cfg, v.doneDays, filter, cfg.PointsField)
}

// indexJQL answers q from the index for a search Jira can't take offline,
// saying so and what of q it left out.
func indexJQL(ctx context.Context, c *jira.Client, ix *index.Index, q string, msg jiraCardsMsg) jiraCardsMsg {
	me, _ := c.Myself(ctx) // cached from before; offline without it, currentUser() drops
	hits, dropped, err := ix.JQL(q, me.AccountID, indexJQLHits)
	if err != nil {
		msg.err = err
		return msg
	}
	for _, h := range hits {
		msg.cards = append(msg.cards, h.Card)
	}
	msg.total = len(msg.cards)
	msg.note = i18n.Tn(len(msg.cards), "offline: %d issue from the index", "offline: %d issues from the index", len(msg.cards))
	if len(dropped) > 0 {
		msg.note += i18n.Tf(" · left out %s", strings.Join(dropped, ", "))
	}
	return msg
}

// indexJQLHits bounds an offline search's answer.
const indexJQLHits = 500

// loadJiraCards refetches the cards of view idx on the loaded board.
// fromCache shows the stored copy first, when it was narrowed by the same
// filters; a refetch after a change leaves it out, as it predates the change.
// While a whole board loads it does nothing: that load brings fresh cards,
// and superseding it would bring back the old board.
func (m *Model) loadJiraCards(idx int, fromCache bool) tea.Cmd {
	t := m.jiraTab
	if t.cfg == nil || idx < 0 || idx >= len(t.views) || (t.loading && t.boardSeq == t.seq) {
		return nil
	}
	t.seq++
	t.loading, t.loadingSince = true, time.Now()
	t.err = ""
	seq, ctx, c, board, cfg, v, ix := t.seq, m.ctx, m.jiraClient, m.jiraBoardID(), t.cfg, t.views[idx], m.index
	filter := jiraFilterJQL(t.assignee, t.quick, t.quickOn)
	st := m.store
	base := cacheOf(jiraBoardMsg{project: t.project, boards: t.boards, board: t.board, cfg: cfg, views: t.views,
		viewIdx: idx, quick: t.quick, quickOn: t.quickOn, assignee: t.assignee, statusNames: t.statusNames}, filter)
	var cached tea.Cmd
	if fromCache {
		cached = func() tea.Msg {
			if c, ok := readJiraCache(st, board, v.name); ok && c.Filter == filter {
				return jiraCardsMsg{seq: seq, cached: true, viewIdx: idx, cards: c.Cards, total: c.Total}
			}
			return nil
		}
	}
	return tea.Batch(cached, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, c.Scaled(60*time.Second))
		defer cancel()
		cards, total, err := fetchJiraView(ctx, c, board, cfg, v, filter)
		if err == nil {
			base.Cards, base.Total = cards, total
			saveJiraCache(st, board, v.name, base)
		}
		if v.kind == jiraViewFilter && jira.Offline(err) && ix != nil {
			return indexJQL(ctx, c, ix, andOrderedJQL(v.jql, filter), jiraCardsMsg{seq: seq, viewIdx: idx})
		}
		return jiraCardsMsg{seq: seq, viewIdx: idx, cards: cards, total: total, err: err}
	})
}

func (m Model) handleJiraBoard(msg jiraBoardMsg) (tea.Model, tea.Cmd) {
	t := m.jiraTab
	if msg.seq != t.seq || (msg.cached && t.fresh == msg.seq) {
		return m, nil
	}
	if !msg.cached {
		t.loading, t.fresh = false, msg.seq
	}
	if msg.lanes != nil {
		t.wantLanes = *msg.lanes
	}
	t.modeRead = true
	if msg.project != "" {
		t.project = msg.project
	}
	if msg.err != nil && msg.cfg == nil && t.cfg != nil && len(t.cards) > 0 && t.project == msg.project {
		// Offline, or Jira down: keep the cached board, say so.
		t.offline = msg.err.Error()
		m.fail(i18n.Tf("offline: %s", msg.err.Error()))
		m.renderJira()
		return m, nil
	}
	if msg.err != nil && msg.cfg == nil {
		t.err = msg.err.Error()
		t.boards, t.cfg, t.views, t.cards = msg.boards, nil, nil, nil
		m.buildJiraLanes()
		m.renderJira()
		return m, nil
	}
	keep := m.selectedJiraKey()
	t.boards, t.board, t.cfg, t.views, t.viewIdx = msg.boards, msg.board, msg.cfg, msg.views, msg.viewIdx
	t.quick, t.quickOn, t.assignee = withLocalQuick(msg.quick, m.opts.quick), msg.quickOn, msg.assignee
	t.swim = m.readJiraSwim()
	t.layout = ""
	if m.store != nil {
		t.layout, _, _ = m.store.GetMeta(jiraLayoutKey(m.jiraBoardID()))
	}
	t.secFold = m.readJiraSecFold()
	if msg.statusNames != nil {
		t.statusNames = msg.statusNames
	}
	m.installJiraCards(msg.cards, msg.total, msg.err, keep)
	if msg.cached || msg.err != nil {
		return m, nil
	}
	if msg.quickErr != nil {
		warn := i18n.Tf("quick filters: %s", msg.quickErr.Error())
		if len(msg.quickOn) > 0 {
			warn += i18n.Tf(" · the board shows unfiltered, %s retries", helpKey(m.keys.Refresh))
		}
		m.fail(warn)
	}
	t.fullAt, t.fullKey = time.Now(), m.jiraFetchKey(t.viewIdx)
	return m, tea.Batch(m.runRules(msg.cards), m.startHome())
}

func (m Model) handleJiraCards(msg jiraCardsMsg) (tea.Model, tea.Cmd) {
	t := m.jiraTab
	if msg.seq != t.seq || (msg.cached && t.fresh == msg.seq) {
		return m, nil
	}
	if !msg.cached {
		t.loading, t.fresh = false, msg.seq
	}
	keep := m.selectedJiraKey()
	if msg.delta {
		if msg.err != nil {
			t.offline = msg.err.Error()
			return m, nil
		}
		t.offline = ""
		if msg.full {
			return m, m.loadJiraCards(msg.viewIdx, false)
		}
		prev := t.cards
		merged, added := mergeCards(prev, msg.cards)
		if len(msg.gone) > 0 {
			merged = slices.DeleteFunc(merged, func(cd jira.Card) bool { return slices.Contains(msg.gone, cd.Key) })
			added -= len(msg.gone)
		}
		m.installJiraCards(merged, max(t.total+added, len(merged)), nil, keep)
		if m.inClosedSprint() {
			return m, nil
		}
		return m, m.runRules(merged)
	}
	if !msg.cached && msg.err == nil {
		t.fullAt, t.fullKey = time.Now(), m.jiraFetchKey(msg.viewIdx)
	}
	if msg.viewIdx != t.viewIdx && msg.viewIdx < len(t.views) {
		vs := t.sorts[t.views[msg.viewIdx].name]
		t.sort, t.desc = vs.by, vs.desc
	}
	t.viewIdx = msg.viewIdx
	m.installJiraCards(msg.cards, msg.total, msg.err, keep)
	if msg.note != "" {
		m.status = msg.note
	}
	if msg.cached || msg.err != nil {
		return m, nil
	}
	if v, _ := m.jiraCurrentView(); !v.closed.IsZero() {
		if t.past != nil && t.past.at.Equal(v.closed) {
			return m, nil // a refresh: the replay stays
		}
		return m, m.openTimeMachineAt(v.closed)
	}
	return m, m.runRules(msg.cards)
}

// setViewSort keeps sort as view i's.
func (m *Model) setViewSort(i int, sort viewSort) {
	t := m.jiraTab
	if i < 0 || i >= len(t.views) {
		return
	}
	if t.sorts == nil {
		t.sorts = map[string]viewSort{}
	}
	t.sorts[t.views[i].name] = sort
}

// sortJiraList orders the list by sort, keeping the selected card.
func (m *Model) sortJiraList(sort viewSort) {
	t := m.jiraTab
	keep := m.selectedJiraKey()
	t.sort, t.desc = sort.by, sort.desc
	m.setViewSort(t.viewIdx, sort)
	m.buildJiraLanes()
	m.selectJiraKey(keep)
	m.renderJira()
	m.status = i18n.Tf("sorted by %s", t.sort.String())
	if t.desc {
		m.status = i18n.Tf("sorted by %s, reversed", t.sort.String())
	}
}

// installJiraCards shows a fetched card list, keeping the selection on the
// card with key keep when it is still there.
func (m *Model) installJiraCards(cards []jira.Card, total int, err error, keep string) {
	t := m.jiraTab
	switch {
	case err != nil && len(cards) == 0 && len(t.cards) > 0:
		// Offline, or Jira down: the cards already shown stay.
		t.offline = err.Error()
		m.fail(i18n.Tf("offline: %s", err.Error()))
		m.renderJira()
		return
	case err != nil:
		t.err = err.Error()
	default:
		t.offline = ""
	}
	m.dropClosedSprint()
	t.cards, t.total = cards, total
	t.fetched = time.Now()
	m.buildJiraLanes()
	m.selectJiraKey(keep)
	m.renderJira()
}

// buildJiraLanes sorts the cards into the board's columns (dropping cards
// whose status no column shows) and derives the list order: column by column
// on a board, rank order on a list-only view.
func (m *Model) buildJiraLanes() {
	t := m.jiraTab
	t.lanes, t.cols = nil, nil
	t.order = t.order[:0]
	t.rows = nil
	v, ok := m.jiraCurrentView()
	if t.cfg == nil || !ok {
		return
	}
	q, env := jiraParseQuery(t.jiraSearchQuery()), m.jiraQueryEnv()
	board := m.jiraBoard()
	ls, hidden := lanes.Board(board), []string(nil)
	if l, ok := m.jiraLayout(); ok {
		ls, hidden = lanes.Arrange(l, board, t.statusNames)
	}
	col := map[string]int{}
	for _, l := range ls {
		lane := jiraLane{name: l.Name, statusIDs: l.StatusIDs(), max: l.Max, col: len(t.cols)}
		for _, id := range lane.statusIDs {
			col[id] = len(t.cols)
		}
		if len(l.Sections) > 1 {
			for _, s := range l.Sections {
				lane.sections = append(lane.sections, jiraSection{name: s.Name, statusIDs: s.StatusIDs})
			}
		}
		t.cols = append(t.cols, lane)
	}
	t.layoutHidden = 0
	var asOf time.Time
	if t.past != nil && !t.past.loading {
		asOf = t.past.asOf(time.Now())
	}
	for i, cd := range t.cards {
		if !jiraCardMatches(cd, q, env) {
			continue
		}
		if !asOf.IsZero() {
			var ok bool
			if cd, ok = t.past.card(cd, asOf); !ok {
				continue
			}
		}
		if l, ok := col[cd.StatusID]; ok {
			t.cols[l].cards = append(t.cols[l].cards, i)
		} else if slices.Contains(hidden, cd.StatusID) {
			t.layoutHidden++
		}
	}
	for i := range t.cols {
		t.cols[i].stack(t.cards)
	}
	t.lanes = t.cols
	if m.jiraHidesEmpty() && slices.ContainsFunc(t.cols, func(l jiraLane) bool { return len(l.cards) > 0 }) {
		t.lanes = slices.DeleteFunc(slices.Clone(t.cols), func(l jiraLane) bool { return len(l.cards) == 0 })
	}
	if v.lanes {
		for _, l := range t.lanes {
			t.swim.apply(l.cards, t.cards)
			t.order = append(t.order, l.cards...)
		}
	} else {
		for i, cd := range t.cards {
			if jiraCardMatches(cd, q, env) {
				t.order = append(t.order, i) // a planning list shows every card
			}
		}
	}
	if t.desc {
		t.sort.applyDesc(t.order, t.cards)
	} else {
		t.sort.apply(t.order, t.cards)
	}
	if len(t.laneTop) != len(t.lanes) {
		t.laneTop = make([]int, len(t.lanes))
	}
	m.clampJiraCursor()
}

// stack orders a stacked lane's cards by section, rank kept within each,
// and counts them into its sections.
func (l *jiraLane) stack(cards []jira.Card) {
	if len(l.sections) == 0 {
		return
	}
	sec := func(ci int) int {
		return slices.IndexFunc(l.sections, func(s jiraSection) bool { return slices.Contains(s.statusIDs, cards[ci].StatusID) })
	}
	slices.SortStableFunc(l.cards, func(a, b int) int { return sec(a) - sec(b) })
	at := 0
	for i := range l.sections {
		l.sections[i].first, l.sections[i].n = at, 0
		for at < len(l.cards) && sec(l.cards[at]) == i {
			l.sections[i].n++
			at++
		}
	}
}

// headAt is the section whose header a stacked lane draws before its card
// r, the first of it; -1 for none. An empty section draws none.
func (l jiraLane) headAt(r int) int {
	return slices.IndexFunc(l.sections, func(s jiraSection) bool { return s.first == r && s.n > 0 })
}

// sectionOf is the index of the section holding card r, -1 for none.
func (l jiraLane) sectionOf(r int) int {
	return slices.IndexFunc(l.sections, func(s jiraSection) bool { return r >= s.first && r < s.first+s.n })
}

// secFoldKey names a stacked lane's section for the folds.
func secFoldKey(lane, section string) string { return lane + "\x1f" + section }

// jiraSecFoldKey remembers a board's folded sections.
func jiraSecFoldKey(board int) string {
	return jiraMetaPrefix + "secfold:" + strconv.Itoa(board)
}

// readJiraSecFold is the current board's folded sections.
func (m *Model) readJiraSecFold() map[string]bool {
	out := map[string]bool{}
	if m.store == nil {
		return out
	}
	v, _, _ := m.store.GetMeta(jiraSecFoldKey(m.jiraBoardID()))
	for _, k := range strings.Split(v, "\n") {
		if k != "" {
			out[k] = true
		}
	}
	return out
}

// toggleJiraSection folds section s of lane l to its header, or unfolds
// it, remembered per board.
func (m *Model) toggleJiraSection(l, s int) {
	t := m.jiraTab
	if l < 0 || l >= len(t.lanes) || s < 0 || s >= len(t.lanes[l].sections) {
		return
	}
	lane := t.lanes[l]
	k := secFoldKey(lane.name, lane.sections[s].name)
	if t.secFold == nil {
		t.secFold = map[string]bool{}
	}
	if t.secFold[k] {
		delete(t.secFold, k)
		m.status = i18n.Tf("unfolded %s", lane.sections[s].name)
	} else {
		t.secFold[k] = true
		m.status = i18n.Tf("folded %s · %s unfolds all", lane.sections[s].name, helpKey(m.keys.UnfoldAll))
	}
	m.saveJiraSecFold()
	m.skipJiraFolded(1)
	m.renderJira()
}

func (m *Model) saveJiraSecFold() {
	if m.store != nil {
		_ = m.store.SetMeta(jiraSecFoldKey(m.jiraBoardID()), strings.Join(slices.Sorted(maps.Keys(m.jiraTab.secFold)), "\n"))
	}
}

func (m *Model) clampJiraCursor() {
	t := m.jiraTab
	t.idx = min(max(t.idx, 0), max(len(t.order)-1, 0))
	t.lane = min(max(t.lane, 0), max(len(t.lanes)-1, 0))
	if t.lane < len(t.lanes) {
		t.row = min(max(t.row, 0), max(len(t.lanes[t.lane].cards)-1, 0))
	} else {
		t.row = 0
	}
}

// selectedJiraCard is the card under the cursor of the current mode.
func (m *Model) selectedJiraCard() (jira.Card, bool) {
	t := m.jiraTab
	if m.jiraShowsLanes() {
		if t.lane < len(t.lanes) && t.row < len(t.lanes[t.lane].cards) {
			return t.cards[t.lanes[t.lane].cards[t.row]], true
		}
		return jira.Card{}, false
	}
	if t.idx < len(t.order) {
		return t.cards[t.order[t.idx]], true
	}
	return jira.Card{}, false
}

func (m *Model) selectedJiraKey() string {
	c, _ := m.selectedJiraCard()
	return c.Key
}

// selectJiraKey puts both cursors on the card with key, when present.
func (m *Model) selectJiraKey(key string) {
	if key == "" {
		return
	}
	t := m.jiraTab
	for i, ci := range t.order {
		if t.cards[ci].Key == key {
			t.idx = i
		}
	}
	for l, lane := range t.lanes {
		for r, ci := range lane.cards {
			if t.cards[ci].Key == key {
				t.lane, t.row = l, r
			}
		}
	}
}

func (m Model) handleJiraKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.jiraTab
	if key.Matches(msg, m.keys.Palette) {
		m.openPalette()
		return m, nil
	}
	if t.arrange != nil {
		return m.handleJiraArrangeKey(msg)
	}
	if t.roadmap != nil {
		return m.handleRoadmapKey(msg)
	}
	if t.plan != nil {
		return m.handlePlanKey(msg)
	}
	if t.charts != nil {
		return m.handleChartsKey(msg)
	}
	if t.week != nil {
		return m.handleWeekKey(msg)
	}
	if t.standup != nil {
		return m.handleStandupKey(msg)
	}
	if t.inbox != nil {
		return m.handleInboxKey(msg)
	}
	if t.agentsView != nil {
		return m.handleAgentsKey(msg)
	}
	if t.mrs != nil {
		return m.handleMRsKey(msg)
	}
	if t.past != nil {
		return m.handleTimeMachineKey(msg)
	}
	if i, ok := m.actionForKey(msg.String(), false); ok {
		return m, m.runAction(i, false)
	}
	lanes := m.jiraShowsLanes()
	if m.needsCard(msg) {
		if _, ok := m.selectedJiraCard(); !ok {
			m.status = i18n.T("no card selected")
			return m, nil
		}
	}
	switch {
	case msg.String() == "ctrl+c", key.Matches(msg, m.keys.Quit):
		return m.quit()
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.InputUp):
		m.moveJiraCursor(-1)
	case key.Matches(msg, m.keys.Down), key.Matches(msg, m.keys.InputDown):
		m.moveJiraCursor(1)
	case lanes && key.Matches(msg, m.keys.Left):
		m.moveJiraLane(-1)
	case lanes && key.Matches(msg, m.keys.Right):
		m.moveJiraLane(1)
	case key.Matches(msg, m.keys.MoveCardLeft):
		return m, m.moveJiraCardBy(-1)
	case key.Matches(msg, m.keys.MoveCardRight):
		return m, m.moveJiraCardBy(1)
	case key.Matches(msg, m.keys.RankUp):
		return m, m.rankJiraCard(-1)
	case key.Matches(msg, m.keys.RankDown):
		return m, m.rankJiraCard(1)
	case key.Matches(msg, m.keys.RankTop):
		return m, m.rankJiraCard(-jiraRankEnd)
	case key.Matches(msg, m.keys.RankBottom):
		return m, m.rankJiraCard(jiraRankEnd)
	case key.Matches(msg, m.keys.Home):
		t.idx, t.row = 0, 0
		m.renderJira()
	case key.Matches(msg, m.keys.End):
		t.idx, t.row = len(t.order), 1<<30
		m.clampJiraCursor()
		m.renderJira()
	case key.Matches(msg, m.keys.PageUp):
		m.moveJiraCursor(-max(t.view.Height()/2, 1))
	case key.Matches(msg, m.keys.PageDown):
		m.moveJiraCursor(max(t.view.Height()/2, 1))
	case key.Matches(msg, m.keys.NextBand):
		m.jumpJiraBand(1)
	case key.Matches(msg, m.keys.PrevBand):
		m.jumpJiraBand(-1)
	case key.Matches(msg, m.keys.OpenChannel), key.Matches(msg, m.keys.OpenRef):
		return m.openJiraCard()
	case key.Matches(msg, m.keys.OpenAttach):
		if c, ok := m.selectedJiraCard(); ok {
			url := m.jiraClient.BrowseURL(c.Key)
			m.status = i18n.Tf("opening %s…", url)
			return m, m.openOpenable(openable{name: c.Key, url: url})
		}
	case key.Matches(msg, m.keys.Refresh):
		return m, m.loadJiraBoard(t.project, m.jiraBoardID(), m.jiraViewName(), false)
	case key.Matches(msg, m.keys.Project):
		return m, m.openJiraProjectPicker()
	case key.Matches(msg, m.keys.Board):
		m.openJiraBoardPicker()
	case key.Matches(msg, m.keys.MoveSprint):
		m.openJiraSprintPicker()
	case key.Matches(msg, m.keys.NextView):
		return m, m.cycleJiraView(1)
	case key.Matches(msg, m.keys.PrevView):
		return m, m.cycleJiraView(-1)
	case key.Matches(msg, m.keys.ToggleMode):
		return m, m.toggleJiraMode()
	case key.Matches(msg, m.keys.Roadmap):
		return m, m.openRoadmap()
	case key.Matches(msg, m.keys.Plan):
		return m, m.openPlanning()
	case key.Matches(msg, m.keys.Charts):
		return m, m.openCharts()
	case key.Matches(msg, m.keys.TimeMachine):
		return m, m.openTimeMachine()
	case key.Matches(msg, m.keys.ClosedSprint):
		return m, m.openClosedSprintPicker()
	case key.Matches(msg, m.keys.Timer):
		return m, m.toggleTimer(m.selectedJiraKey())
	case key.Matches(msg, m.keys.Timesheet):
		return m, m.openTimesheet()
	case key.Matches(msg, m.keys.Inbox):
		return m, m.openInbox()
	case key.Matches(msg, m.keys.Agents):
		return m, m.openAgents()
	case key.Matches(msg, m.keys.MergeRequests):
		return m, m.openMRs()
	case key.Matches(msg, m.keys.StartScreen):
		return m, m.openHome()
	case key.Matches(msg, m.keys.Standup):
		return m, m.openStandup()
	case key.Matches(msg, m.keys.Releases):
		return m, m.openReleases()
	case key.Matches(msg, m.keys.JQL):
		return m, m.openJQL()
	case key.Matches(msg, m.keys.Site):
		m.openSitePicker()
	case key.Matches(msg, m.keys.Undo):
		return m, m.undoJiraMove()
	case key.Matches(msg, m.keys.Repeat):
		return m, m.repeatOnSelected()
	case key.Matches(msg, m.keys.Refine):
		return m, m.startRefine()
	case key.Matches(msg, m.keys.Pin):
		if c, ok := m.selectedJiraCard(); ok {
			m.togglePin(c.Key, c.Summary)
		}
	case key.Matches(msg, m.keys.Fold):
		m.foldJiraSwimlane()
	case key.Matches(msg, m.keys.UnfoldAll):
		t.swimFold, t.secFold = nil, nil
		m.saveJiraSecFold()
		m.renderJira()
	case key.Matches(msg, m.keys.Mark):
		m.toggleJiraMark()
	case key.Matches(msg, m.keys.MarkAll):
		m.toggleJiraMarkAll()
	case key.Matches(msg, m.keys.Bulk):
		m.openBulkMenu()
	case key.Matches(msg, m.keys.Assignee):
		return m, m.openJiraAssigneeFilter()
	case key.Matches(msg, m.keys.Mine):
		if t.cfg == nil {
			m.status = m.noBoardYet()
			break
		}
		if t.assignee.id == "me" {
			return m, m.setJiraAssignee("", "")
		}
		return m, m.setJiraAssignee("me", i18n.T("Me"))
	case key.Matches(msg, m.keys.ClearFilters):
		return m, m.clearJiraFilters()
	case key.Matches(msg, m.keys.Search):
		m.startJiraSearch()
	case key.Matches(msg, m.keys.FilterBuilder):
		m.openFilterBuilder(false)
	case key.Matches(msg, m.keys.QuickEdit):
		m.openQuickEdit()
	case key.Matches(msg, m.keys.MyWork):
		return m, m.openMyWork()
	case key.Matches(msg, m.keys.EmptyLanes) && m.jiraShowsLanes():
		m.toggleEmptyLanes()
		return m, nil
	case key.Matches(msg, m.keys.LaneLayout) && m.jiraShowsLanes():
		m.cycleJiraLayout()
		return m, nil
	case key.Matches(msg, m.keys.ArrangeLanes) && m.jiraShowsLanes():
		m.openJiraArrange()
		return m, nil
	case key.Matches(msg, m.keys.Compact):
		m.toggleCompact()
	case key.Matches(msg, m.keys.PanelWider):
		m.stepPanel(1)
	case key.Matches(msg, m.keys.PanelNarrower):
		m.stepPanel(-1)
	case key.Matches(msg, m.keys.Help):
		m.helpOpen = true
	case key.Matches(msg, m.keys.Settings):
		return m, m.openSettings()
	case key.Matches(msg, m.keys.Sort):
		if lanes {
			keep := m.selectedJiraKey()
			switch t.swim {
			case jiraSortRank:
				t.swim = jiraSortAssignee
			case jiraSortAssignee:
				t.swim = jiraSortEpic
			case jiraSortEpic:
				t.swim = jiraSortPriority
			default:
				t.swim = jiraSortRank
			}
			t.swimFold = nil
			m.buildJiraLanes()
			m.selectJiraKey(keep)
			m.renderJira()
			m.status = i18n.Tf("swimlanes by %s", t.swim.String())
			if t.swim == jiraSortRank {
				m.status = i18n.T("no swimlanes")
			}
			if m.store != nil {
				_ = m.store.SetMeta(jiraSwimKey(m.jiraBoardID()), t.swim.String())
			}
			break
		}
		m.sortJiraList(viewSort{by: (t.sort + 1) % jiraSortCount})
	case key.Matches(msg, m.keys.Goto):
		m.openJiraGoto()
	case key.Matches(msg, m.keys.Create):
		return m, m.openJiraCreate()
	case key.Matches(msg, m.keys.CopyKey) && !lanes && len(t.marked) > 0:
		return m, m.copyJiraTable()
	case key.Matches(msg, m.keys.CopyKey), key.Matches(msg, m.keys.CopyURL):
		return m, m.copyJira(m.selectedJiraKey(), key.Matches(msg, m.keys.CopyURL))
	case key.Matches(msg, m.keys.CopyBranch):
		if c, ok := m.selectedJiraCard(); ok {
			return m, m.copyBranch(c.Key, c.Type, c.Summary)
		}
	case msg.String() == "esc" && t.jiraSearchQuery() != "":
		m.clearJiraSearch()
	case msg.String() == "esc" && len(t.marked) > 0:
		m.clearJiraMarks()
		m.status = i18n.T("marks cleared")
	case len(msg.String()) == 1 && msg.String() >= "1" && msg.String() <= "9":
		return m, m.toggleJiraQuick(int(msg.String()[0] - '1'))
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.ShiftTab):
		if m.refOpen {
			m.focus = focusRef
			m.renderJira()
		}
	}
	return m, nil
}

// moveJiraCursor moves down (or up) the list, or the current lane.
func (m *Model) moveJiraCursor(delta int) {
	t := m.jiraTab
	if m.jiraShowsLanes() {
		t.row += delta
	} else {
		t.idx += delta
	}
	m.clampJiraCursor()
	m.skipJiraFolded(delta)
	m.renderJira()
}

// jiraFolded is whether the lane cursor's row r of lane l is in a folded
// swimlane, or a folded section of a stacked lane.
func (m *Model) jiraFolded(l, r int) bool {
	t := m.jiraTab
	if l >= len(t.lanes) || r >= len(t.lanes[l].cards) {
		return false
	}
	if lane := t.lanes[l]; t.swim == jiraSortRank && len(t.secFold) > 0 {
		s := lane.sectionOf(r)
		return s >= 0 && t.secFold[secFoldKey(lane.name, lane.sections[s].name)]
	}
	if len(t.swimFold) == 0 || t.swim == jiraSortRank {
		return false
	}
	g, _ := jiraGroupOf(t.swim, t.cards[t.lanes[l].cards[r]])
	return t.swimFold[g]
}

// skipJiraFolded moves the lane cursor off a folded swimlane: on in the
// direction of dir, else back the other way; it stays when every card is.
func (m *Model) skipJiraFolded(dir int) {
	t := m.jiraTab
	if !m.jiraShowsLanes() || !m.jiraFolded(t.lane, t.row) {
		return
	}
	step := 1
	if dir < 0 {
		step = -1
	}
	for _, s := range []int{step, -step} {
		for r := t.row + s; r >= 0 && r < len(t.lanes[t.lane].cards); r += s {
			if !m.jiraFolded(t.lane, r) {
				t.row = r
				return
			}
		}
	}
}

// toggleJiraSwimlane folds band g to its header, or unfolds it (a click on
// its header).
func (m *Model) toggleJiraSwimlane(g string) {
	t := m.jiraTab
	if t.swimFold == nil {
		t.swimFold = map[string]bool{}
	}
	if t.swimFold[g] {
		delete(t.swimFold, g)
	} else {
		t.swimFold[g] = true
	}
	m.skipJiraFolded(1)
	m.renderJira()
}

// foldJiraSwimlane folds the cursor's swimlane to its header.
func (m *Model) foldJiraSwimlane() {
	t := m.jiraTab
	c, ok := m.selectedJiraCard()
	if t.swim == jiraSortRank && m.jiraShowsLanes() && t.lane < len(t.lanes) && len(t.lanes[t.lane].sections) > 0 {
		if s := t.lanes[t.lane].sectionOf(t.row); s >= 0 && ok {
			m.toggleJiraSection(t.lane, s)
		}
		return
	}
	if t.swim == jiraSortRank || !m.jiraShowsLanes() {
		m.status = i18n.Tf("fold needs swimlanes (%s in lanes) or a stacked lane", helpKey(m.keys.Sort))
		return
	}
	if !ok {
		return
	}
	g, _ := jiraGroupOf(t.swim, c)
	if t.swimFold == nil {
		t.swimFold = map[string]bool{}
	}
	t.swimFold[g] = true
	m.skipJiraFolded(1)
	m.renderJira()
	m.status = i18n.Tf("folded %s · %s unfolds all", g, helpKey(m.keys.UnfoldAll))
}

// jiraSwimGroups are the swimlanes in order, with a card of each.
func (m *Model) jiraSwimGroups() ([]string, map[string]jira.Card) {
	t := m.jiraTab
	var groups []string
	rep := map[string]jira.Card{}
	for _, l := range t.lanes {
		for _, ci := range l.cards {
			if g, _ := jiraGroupOf(t.swim, t.cards[ci]); rep[g].Key == "" {
				rep[g] = t.cards[ci]
				groups = append(groups, g)
			}
		}
	}
	cmp := t.swim.cmp()
	slices.SortStableFunc(groups, func(a, b string) int { return cmp(rep[a], rep[b]) })
	return groups, rep
}

// jumpJiraBand moves the cursor to the first card of the next swimlane (dir
// 1) or the previous one, skipping folded ones; in the list, of the next
// group. A swimlane with no card in the cursor's lane takes the nearest lane
// with one.
func (m *Model) jumpJiraBand(dir int) {
	t := m.jiraTab
	c, ok := m.selectedJiraCard()
	if !m.jiraShowsLanes() {
		g, grouped := jiraGroupOf(t.sort, c)
		if !grouped {
			m.status = i18n.Tf("bands need swimlanes or a grouped list (%s)", helpKey(m.keys.Sort))
			return
		}
		groupAt := func(i int) string { g, _ := jiraGroupOf(t.sort, t.cards[t.order[i]]); return g }
		i := t.idx
		for i >= 0 && i < len(t.order) && groupAt(i) == g {
			i += dir
		}
		if !ok || i < 0 || i >= len(t.order) {
			return
		}
		for g = groupAt(i); i > 0 && groupAt(i-1) == g; i-- {
		}
		t.idx = i
		m.renderJira()
		return
	}
	if t.swim == jiraSortRank {
		m.status = i18n.Tf("bands need swimlanes or a grouped list (%s)", helpKey(m.keys.Sort))
		return
	}
	if !ok {
		return
	}
	groups, _ := m.jiraSwimGroups()
	g, _ := jiraGroupOf(t.swim, c)
	for i := slices.Index(groups, g) + dir; i >= 0 && i < len(groups); i += dir {
		if !t.swimFold[groups[i]] && m.selectJiraSwimlane(groups[i]) {
			m.renderJira()
			return
		}
	}
}

// selectJiraSwimlane puts the cursor on band g's first card in the cursor's
// lane, else in the nearest lane holding one; false when none does.
func (m *Model) selectJiraSwimlane(g string) bool {
	t := m.jiraTab
	for d := 0; d < 2*len(t.lanes); d++ {
		l := t.lane + (d+1)/2*(1-2*(d%2))
		if l < 0 || l >= len(t.lanes) {
			continue
		}
		if r := slices.IndexFunc(t.lanes[l].cards, func(ci int) bool {
			cg, _ := jiraGroupOf(t.swim, t.cards[ci])
			return cg == g
		}); r >= 0 {
			t.lane, t.row = l, r
			return true
		}
	}
	return false
}

// moveJiraLane moves the cursor to the next lane (or previous), keeping its
// height as near as the lane allows (its swimlane, when there are).
func (m *Model) moveJiraLane(delta int) {
	t := m.jiraTab
	c, ok := m.selectedJiraCard()
	t.lane += delta
	m.clampJiraCursor()
	if ok && t.swim != jiraSortRank && t.lane < len(t.lanes) {
		// Swimlanes: stay in the card's band, on its first card there.
		g, _ := jiraGroupOf(t.swim, c)
		if r := slices.IndexFunc(t.lanes[t.lane].cards, func(ci int) bool {
			cg, _ := jiraGroupOf(t.swim, t.cards[ci])
			return cg == g
		}); r >= 0 {
			t.row = r
		}
	}
	m.skipJiraFolded(1)
	m.renderJira()
}

func (m *Model) cycleJiraView(delta int) tea.Cmd {
	t := m.jiraTab
	n := len(t.views)
	if n < 2 {
		return nil
	}
	idx := ((t.viewIdx+delta)%n + n) % n
	return m.loadJiraCards(idx, true)
}

// toggleJiraMode flips lanes and list, remembering the choice. A list-only
// view stays a list.
func (m *Model) toggleJiraMode() tea.Cmd {
	t := m.jiraTab
	if v, ok := m.jiraCurrentView(); ok && !v.lanes {
		m.status = i18n.Tf("%s is a list", v.name)
		return nil
	}
	keep := m.selectedJiraKey()
	t.wantLanes = !t.wantLanes
	m.selectJiraKey(keep)
	m.renderJira()
	mode := "list"
	if t.wantLanes {
		mode = "lanes"
	}
	st := m.store
	return func() tea.Msg {
		_ = st.SetMeta(jiraMetaPrefix+"mode", mode)
		return nil
	}
}

// jiraSwimKey remembers a board's swimlanes (assignee, epic, priority or rank).
func jiraSwimKey(board int) string {
	return jiraMetaPrefix + "swim:" + strconv.Itoa(board)
}

// readJiraSwim is the current board's remembered swimlanes, none by default.
func (m *Model) readJiraSwim() jiraSort {
	if m.store == nil {
		return jiraSortRank
	}
	switch v, _, _ := m.store.GetMeta(jiraSwimKey(m.jiraBoardID())); v {
	case jiraSortAssignee.String():
		return jiraSortAssignee
	case jiraSortEpic.String():
		return jiraSortEpic
	case jiraSortPriority.String():
		return jiraSortPriority
	}
	return jiraSortRank
}

// openJiraCard shows the selected issue in the reference panel.
func (m Model) openJiraCard() (tea.Model, tea.Cmd) {
	c, ok := m.selectedJiraCard()
	if !ok {
		return m, nil
	}
	m.refBack = nil // the board is its own way back
	m.sizeRefView()
	return m.showJiraKey(c.Key)
}

// openJiraKey shows the issue key in the reference panel, the issue it
// replaces joining the trail for backspace.
func (m Model) openJiraKey(key string) (tea.Model, tea.Cmd) {
	if r := m.currentRef(); r != nil && r.jiraKey != key {
		c := refCrumb{key: r.jiraKey}
		if iss := m.jiraIssue; iss != nil && iss.Key == r.jiraKey {
			c.summary, c.status = iss.Summary, iss.Status
		}
		m.refBack = append(m.refBack, c)
		m.sizeRefView()
	}
	return m.showJiraKey(key)
}

// showJiraKey shows key in the panel without touching the history.
func (m Model) showJiraKey(key string) (tea.Model, tea.Cmd) {
	if _, ok := m.jiraTab.highlights[key]; ok {
		delete(m.jiraTab.highlights, key)
		m.jiraTab.rows = nil
	}
	if key != m.agentTermKey {
		m.closeAgentPanel()
	}
	refs := []reference{{kind: refJira, jiraKey: key}}
	m.refOpen = true
	m.refs = refs
	m.refIdx = 0
	m.focus = focusRef
	m.setPanelHint(m.refStatusHint(refs[0], 1))
	m.resize()
	cmd := m.loadCurrentRef()
	return m, cmd
}

// needsCard is whether key msg acts on the selected card alone: with no
// card selected (and none marked, a timer not running) it says so rather
// than doing nothing.
func (m *Model) needsCard(msg tea.KeyPressMsg) bool {
	k := m.keys
	if len(m.jiraTab.marked) > 0 {
		return false
	}
	for _, b := range []key.Binding{k.OpenChannel, k.OpenAttach, k.Pin, k.CopyBranch, k.CopyKey, k.CopyURL, k.Mark, k.QuickEdit, k.MoveSprint, k.MoveCardLeft, k.MoveCardRight, k.RankUp, k.RankDown, k.RankTop, k.RankBottom} {
		if key.Matches(msg, b) {
			return true
		}
	}
	return key.Matches(msg, k.Timer) && m.timer.key == ""
}

// jiraRankedMsg is a board rank answered.
type jiraRankedMsg struct {
	key string
	err error
}

// jiraRankEnd, as rankJiraCard's d, ranks to the top (-) or bottom (+).
const jiraRankEnd = 1 << 30

// rankJiraCard ranks the selected card d places up (-) or down (+): in its
// lane, or in a list in rank order.
func (m *Model) rankJiraCard(d int) tea.Cmd {
	t := m.jiraTab
	var shown []int // the card indexes in the order the cursor walks
	var at int
	switch {
	case m.jiraShowsLanes() && t.swim != jiraSortRank:
		m.status = i18n.Tf("ranking needs the swimlanes off (%s)", helpKey(m.keys.Sort))
		return nil
	case m.jiraShowsLanes():
		if t.lane < len(t.lanes) {
			shown, at = t.lanes[t.lane].cards, t.row
		}
	case t.sort != jiraSortRank:
		m.status = i18n.Tf("ranking needs the list sorted by rank (%s)", helpKey(m.keys.Sort))
		return nil
	default:
		shown, at = t.order, t.idx
	}
	if at >= len(shown) {
		return nil
	}
	to := min(max(at+d, 0), len(shown)-1)
	switch key := t.cards[shown[at]].Key; {
	case to == at && d < 0:
		m.status = i18n.Tf("%s is ranked first already", key)
		return nil
	case to == at:
		m.status = i18n.Tf("%s is ranked last already", key)
		return nil
	}
	return m.rankJiraCardTo(shown, at, to)
}

// rankJiraCardTo ranks card shown[at] into place to of shown: before the
// card there going up, after it going down. The card moves on screen at
// once; u ranks it back beside its old neighbour; a failed rank refetches
// the board.
func (m *Model) rankJiraCardTo(shown []int, at, to int) tea.Cmd {
	t := m.jiraTab
	c, other, after := t.cards[shown[at]], t.cards[shown[to]], to > at
	client := m.jiraClient
	if at+1 < len(shown) {
		next := t.cards[shown[at+1]].Key
		m.recordUndo(i18n.Tf("%s's rank", c.Key), func(ctx context.Context) error { return client.Rank(ctx, c.Key, next, false) })
	} else if at > 0 {
		prev := t.cards[shown[at-1]].Key
		m.recordUndo(i18n.Tf("%s's rank", c.Key), func(ctx context.Context) error { return client.Rank(ctx, c.Key, prev, true) })
	}
	i := shown[at]
	t.cards = slices.Delete(t.cards, i, i+1)
	j := slices.IndexFunc(t.cards, func(cd jira.Card) bool { return cd.Key == other.Key })
	if after {
		j++
	}
	t.cards = slices.Insert(t.cards, j, c)
	m.buildJiraLanes()
	m.selectJiraKey(c.Key)
	m.renderJira()
	m.status = i18n.Tf("ranking %s…", c.Key)
	ctx := m.ctx
	return func() tea.Msg {
		return jiraRankedMsg{key: c.Key, err: client.Rank(ctx, c.Key, other.Key, after)}
	}
}

func (m Model) handleJiraRanked(msg jiraRankedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.fail(i18n.Tf("rank %s: %s", msg.key, msg.err.Error()))
		return m, m.loadJiraCards(m.jiraTab.viewIdx, false)
	}
	m.status = i18n.Tf("ranked %s", msg.key)
	return m, nil
}

// moveJiraCardBy moves the selected card delta lanes over. A lane of several
// statuses asks which one first.
func (m *Model) moveJiraCardBy(delta int) tea.Cmd {
	t := m.jiraTab
	c, ok := m.selectedJiraCard()
	if !ok {
		return nil
	}
	// The stops H and L step through: each lane, a stacked lane's sections
	// one by one.
	type stop struct {
		lane int
		ids  []string
	}
	var stops []stop
	for i, l := range t.cols {
		if len(l.sections) == 0 {
			stops = append(stops, stop{i, l.statusIDs})
		}
		for _, sec := range l.sections {
			stops = append(stops, stop{i, sec.statusIDs})
		}
	}
	from := slices.IndexFunc(stops, func(s stop) bool { return slices.Contains(s.ids, c.StatusID) })
	if m.jiraShowsLanes() && t.lane < len(t.lanes) && (from < 0 || stops[from].lane != t.lanes[t.lane].col) {
		from = slices.IndexFunc(stops, func(s stop) bool { return s.lane == t.lanes[t.lane].col })
	}
	to := from + delta
	switch {
	case from < 0:
		m.status = i18n.Tf("%s's status is in no column of this board", c.Key)
		return nil
	case to < 0:
		m.status = i18n.Tf("%s is in the first lane already", c.Key)
		return nil
	case to >= len(stops):
		m.status = i18n.Tf("%s is in the last lane already", c.Key)
		return nil
	}
	if ids := stops[to].ids; len(ids) > 1 {
		m.openJiraLaneStatusPicker(c, stops[to].lane, ids)
		return nil
	} else if len(t.cols[stops[to].lane].sections) > 0 {
		return m.moveJiraCard(c.Key, stops[to].lane, ids[0])
	}
	return m.moveJiraCard(c.Key, stops[to].lane, "")
}

// jiraStatusName names a status id, falling back to the id.
func (m *Model) jiraStatusName(id string) string {
	if n := m.jiraTab.statusNames[id]; n != "" {
		return n
	}
	return id
}

// openJiraLaneStatusPicker asks which of ids, statuses of column to, card
// goes to.
func (m *Model) openJiraLaneStatusPicker(c jira.Card, to int, ids []string) {
	t := m.jiraTab
	lane := t.cols[to]
	m.startJiraPicker(jiraPickLaneStatus, c.Key+" → "+lane.name, false)
	items := make([]jiraPickerItem, len(ids))
	for i, id := range ids {
		items[i] = jiraPickerItem{id: id, label: m.jiraStatusName(id), current: id == c.StatusID}
	}
	m.setJiraPickerItems(items)
	t.pendingMove.key, t.pendingMove.lane = c.Key, to
}

// pickJiraLaneStatus moves the card waiting on the status picker.
func (m *Model) pickJiraLaneStatus(statusID string) tea.Cmd {
	pm := m.jiraTab.pendingMove
	return m.moveJiraCard(pm.key, pm.lane, statusID)
}

// moveJiraCard moves the card to column to (an index into cols): to status
// statusID, or with "" to whichever of the lane's statuses the workflow
// reaches first. The card moves on screen at once, the cursor with it; the move itself may need the
// transition form (jira_transition.go), and the board refetches once Jira
// answers.
func (m *Model) moveJiraCard(key string, to int, statusID string) tea.Cmd {
	t := m.jiraTab
	if to < 0 || to >= len(t.cols) {
		return nil
	}
	lane := t.cols[to]
	ci := slices.IndexFunc(t.cards, func(c jira.Card) bool { return c.Key == key })
	if ci < 0 || len(lane.statusIDs) == 0 {
		return nil
	}
	cur := t.cards[ci].StatusID
	if statusID == cur || (statusID == "" && slices.Contains(lane.statusIDs, cur)) {
		return nil
	}
	if t.undoing {
		t.undoneMove = key
	}
	m.pushUndo(i18n.Tf("%s back to %s", key, m.jiraStatusName(cur)), func(m *Model) tea.Cmd {
		to := slices.IndexFunc(m.jiraTab.cols, func(l jiraLane) bool { return slices.Contains(l.statusIDs, cur) })
		if to < 0 {
			m.status = i18n.Tf("%s: its old status is not on this board", key)
			return nil
		}
		return m.moveJiraCard(key, to, cur)
	})
	m.setRepeat(i18n.Tf("move to %s", lane.name), func(m *Model, key string) tea.Cmd { return m.moveJiraCard(key, to, statusID) })
	target, name := lane.statusIDs[0], lane.name
	want := func(tm jira.TransitionMeta) bool { return slices.Contains(lane.statusIDs, tm.ToID) }
	if statusID != "" {
		target, name = statusID, m.jiraStatusName(statusID)
		want = func(tm jira.TransitionMeta) bool { return tm.ToID == statusID }
	}
	cat := m.laneCategory(to) // before the card lands, which would tell its old one
	if statusID != "" {
		cat = m.statusCategory(statusID, cat)
	}
	t.cards[ci].StatusID = target
	t.cards[ci].Status = name
	t.cards[ci].Done, t.cards[ci].InProgress = cat == "done", cat == "indeterminate"
	m.buildJiraLanes()
	m.selectJiraKey(key)
	m.renderJira()
	m.status = i18n.Tf("moving %s → %s…", key, name)
	return m.prepareJiraMove(key, name, jiraFromBoard, want)
}

// undoJiraMove takes back the latest change still on the stack; u again
// takes back the one before.
func (m *Model) undoJiraMove() tea.Cmd {
	t := m.jiraTab
	if len(t.undo) == 0 {
		m.status = i18n.T("nothing to undo")
		return nil
	}
	step := t.undo[len(t.undo)-1]
	t.undo = t.undo[:len(t.undo)-1]
	t.undoing = true
	cmd := step.back(m)
	t.undoing = false
	return cmd
}

func (m Model) handleJiraMoved(msg jiraMovedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.fail(i18n.Tf("%s: move failed: %v", msg.key, msg.err))
	} else if t := m.jiraTab; t.undoneMove == msg.key {
		t.undoneMove = ""
		m.status = i18n.Tf("undone: %s back to %s%s", msg.key, msg.lane, t.undoMore())
	} else {
		m.status = fmt.Sprintf("%s → %s", msg.key, msg.lane)
	}
	return m, tea.Batch(m.loadJiraCards(m.jiraTab.viewIdx, false), m.celebrateMove(msg.key))
}

// refreshJiraAfterEdit refetches the board after the panel changed an issue on
// it, so a new status or assignee shows on its card.
func (m *Model) refreshJiraAfterEdit() tea.Cmd {
	if m.jiraTab.cfg == nil {
		return nil
	}
	return m.loadJiraCards(m.jiraTab.viewIdx, false)
}

// openJiraProjectPicker lists the projects, configured ones first, in a
// filterable picker.
func (m *Model) openJiraProjectPicker() tea.Cmd {
	gen := m.startJiraPicker(jiraPickProject, i18n.T("Project"), true)
	t := m.jiraTab
	cur, configured := t.project, m.jiraProjects
	if t.projects != nil {
		m.setJiraPickerItems(jiraProjectItems(t.projects, configured, cur))
		return nil
	}
	seq, c, ctx := m.jiraPicker.fetchSeq, m.jiraClient, m.ctx
	return func() tea.Msg {
		ps, err := c.ListProjects(ctx)
		return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: jiraPickProject, projects: ps,
			items: jiraProjectItems(ps, configured, cur), err: err}
	}
}

func jiraProjectItems(ps []jira.Project, configured []string, cur string) []jiraPickerItem {
	var first, rest []jiraPickerItem
	for _, p := range ps {
		it := jiraPickerItem{id: p.Key, label: p.Key + "  " + p.Name, current: p.Key == cur}
		if slices.Contains(configured, p.Key) {
			first = append(first, it)
		} else {
			rest = append(rest, it)
		}
	}
	return append(first, rest...)
}

// noBoardYet says why a key that needs the board did nothing.
func (m *Model) noBoardYet() string {
	if m.jiraTab.loading {
		return i18n.T("the board is still loading")
	}
	return i18n.Tf("no board loaded · %s retries", helpKey(m.keys.Refresh))
}

// openJiraBoardPicker lists the project's boards.
func (m *Model) openJiraBoardPicker() {
	t := m.jiraTab
	if len(t.boards) == 0 {
		switch {
		case t.loading:
			m.status = i18n.T("the boards are still loading")
		case t.project != "":
			m.status = i18n.Tf("%s has no boards · %s picks another project", t.project, helpKey(m.keys.Project))
		default:
			m.status = i18n.Tf("%s picks a project first", helpKey(m.keys.Project))
		}
		return
	}
	m.startJiraPicker(jiraPickBoard, i18n.Tf("Board — %s", t.project), true)
	items := make([]jiraPickerItem, len(t.boards))
	for i, b := range t.boards {
		items[i] = jiraPickerItem{id: strconv.Itoa(b.ID), label: b.Name + "  " + b.Type, current: i == t.board}
	}
	m.setJiraPickerItems(items)
}

// pickJiraBoard loads a board picked from the project or board picker.
func (m *Model) pickJiraBoard(kind jiraPickerKind, id string) tea.Cmd {
	t := m.jiraTab
	if kind == jiraPickProject {
		if id == t.project {
			return nil
		}
		t.project = id
		t.boards, t.cfg, t.views, t.cards = nil, nil, nil, nil
		t.quick, t.quickOn = nil, nil
		t.idx, t.lane, t.row, t.firstLane = 0, 0, 0, 0
		m.buildJiraLanes()
		return m.loadJiraBoard(id, 0, "", true)
	}
	board, _ := strconv.Atoi(id)
	if board == m.jiraBoardID() {
		return nil
	}
	t.idx, t.lane, t.row, t.firstLane = 0, 0, 0, 0
	t.quick, t.quickOn = nil, nil
	return m.loadJiraBoard(t.project, board, "", true)
}

// openJiraAssigneeFilter offers everyone, you, unassigned and the people
// with cards on the view, in a filterable picker where space ticks several.
// While the filter narrows the view, the view is fetched again without it,
// so the others stay on offer.
func (m *Model) openJiraAssigneeFilter() tea.Cmd {
	t := m.jiraTab
	if t.cfg == nil {
		m.status = m.noBoardYet()
		return nil
	}
	gen := m.startJiraPicker(jiraPickBoardAssignee, i18n.T("Assignee"), true)
	cur := t.assignee.id
	m.jiraPicker.checked = map[string]string{}
	seen := cardAssignees(t.cards)
	ids, labels := t.assignee.ids(), strings.Split(t.assignee.label, ", ")
	for i, id := range ids {
		m.jiraPicker.checked[id] = id
		if len(labels) == len(ids) { // a name with ", " in it: the rows name them
			m.jiraPicker.checked[id] = labels[i]
		}
		if id != "me" && id != "none" { // ticked, so it can be unticked
			seen[id] = m.jiraPicker.checked[id]
		}
	}
	m.setJiraPickerItems(boardAssigneeItems(seen, cur))
	if cur == "" || t.viewIdx < 0 || t.viewIdx >= len(t.views) {
		return nil
	}
	c, ctx, board, cfg, v, seq := m.jiraClient, m.ctx, m.jiraBoardID(), t.cfg, t.views[t.viewIdx], m.jiraPicker.fetchSeq
	filter := jiraFilterJQL(jiraAssignee{}, t.quick, t.quickOn)
	return func() tea.Msg {
		cards, _, err := fetchJiraView(ctx, c, board, cfg, v, filter)
		if err != nil {
			return nil
		}
		maps.Copy(seen, cardAssignees(cards))
		return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: jiraPickBoardAssignee, items: boardAssigneeItems(seen, cur)}
	}
}

// cardAssignees are the cards' assignees, accountId to name.
func cardAssignees(cards []jira.Card) map[string]string {
	names := map[string]string{}
	for _, c := range cards {
		if c.AssigneeID != "" {
			names[c.AssigneeID] = c.Assignee
		}
	}
	return names
}

// boardAssigneeItems are the assignee filter's rows: everyone, you,
// unassigned, then people by name, cur's (comma-joined) ids marked.
func boardAssigneeItems(people map[string]string, cur string) []jiraPickerItem {
	var rows []jiraPickerItem
	for id, name := range people {
		rows = append(rows, jiraPickerItem{id: id, label: name})
	}
	slices.SortFunc(rows, func(a, b jiraPickerItem) int { return strings.Compare(a.label, b.label) })
	items := append([]jiraPickerItem{{id: "", label: i18n.T("Everyone")}, {id: "me", label: i18n.T("Me")}, {id: "none", label: i18n.T("Unassigned")}}, rows...)
	for i := range items {
		items[i].current = items[i].id == cur || (items[i].id != "" && slices.Contains(strings.Split(cur, ","), items[i].id))
	}
	return items
}

// setJiraAssignee applies an assignee filter picked from the picker and
// remembers it (ui.remember_filters).
func (m *Model) setJiraAssignee(id, label string) tea.Cmd {
	t := m.jiraTab
	if id == "" {
		label = ""
	}
	t.assignee = jiraAssignee{id: id, label: label}
	st := m.store
	if !m.opts.rememberFilters {
		return m.loadJiraCards(t.viewIdx, true)
	}
	save := func() tea.Msg {
		_ = st.SetMeta(jiraMetaPrefix+"assignee", id+"\t"+label)
		return nil
	}
	return tea.Batch(m.loadJiraCards(t.viewIdx, true), save)
}

// toggleJiraQuick flips the board's quick filter i (0-based) and remembers
// the board's set.
func (m *Model) toggleJiraQuick(i int) tea.Cmd {
	t := m.jiraTab
	if i < 0 || i >= len(t.quick) {
		return nil
	}
	if t.quickOn == nil {
		t.quickOn = map[int]bool{}
	}
	id := t.quick[i].ID
	t.quickOn[id] = !t.quickOn[id]
	return tea.Batch(m.loadJiraCards(t.viewIdx, true), m.saveJiraQuick())
}

// clearJiraFilters turns every filter off.
func (m *Model) clearJiraFilters() tea.Cmd {
	t := m.jiraTab
	if t.assignee.id == "" && len(t.quickOn) == 0 {
		return nil
	}
	t.quickOn = map[int]bool{}
	return tea.Batch(m.setJiraAssignee("", ""), m.saveJiraQuick())
}

func (m *Model) saveJiraQuick() tea.Cmd {
	t := m.jiraTab
	if !m.opts.rememberFilters {
		return nil
	}
	var ids []string
	for _, q := range t.quick {
		if t.quickOn[q.ID] {
			ids = append(ids, strconv.Itoa(q.ID))
		}
	}
	st, key := m.store, jiraMetaPrefix+"quick:"+strconv.Itoa(m.jiraBoardID())
	return func() tea.Msg {
		_ = st.SetMeta(key, strings.Join(ids, ","))
		return nil
	}
}

// jiraFiltered reports whether any filter is on.
func (t *jiraTabState) jiraFiltered() bool {
	return jiraFilterJQL(t.assignee, t.quick, t.quickOn) != ""
}

// jiraListWidth is the board pane's outer width: the whole body, or what the
// reference panel leaves of it.
func (m *Model) jiraListWidth(width int) (list, ref int) {
	if !m.refOpen {
		return width, 0
	}
	ref = splitRightPane(width, m.opts.panelPct)
	return width - ref, ref
}

func (m *Model) sizeJiraView(width, height int) {
	list, _ := m.jiraListWidth(width)
	m.jiraTab.view.SetWidth(max(list-2, 10))
	m.jiraTab.view.SetHeight(max(height-4, 1)) // title row, rule, views, filters
}

var jiraLaneStyle = lipgloss.NewStyle().Bold(true)

// Themed board styles, set by applyTheme. jiraOverStyle marks a lane past
// its WIP limit.
var jiraKeyStyle, jiraDimStyle, jiraOverStyle, jiraDropStyle, jiraViewActive, jiraGhostStyle, jiraPinStyle lipgloss.Style

// jiraTypeIcons are the glyph, plain letter and theme colour per issue
// type kind (jira.TypeKind). The colours follow Jira's: red type_bug, green
// type_story, purple type_epic, blue type_other, orange type_alert.
var jiraTypeIcons = map[string][3]string{
	"task":        {"\uf14a", "•", "type_other"},
	"bug":         {"\uf188", "B", "type_bug"},
	"incident":    {"\uf21e", "•", "type_bug"},
	"document":    {"\uf15c", "•", "type_epic"},
	"epic":        {"\uf0e7", "E", "type_epic"},
	"problem":     {"\uf06a", "!", "type_alert"},
	"design":      {"\uf040", "•", "type_other"},
	"improvement": {"\uf062", "↑", "type_story"},
	"add":         {"\uf0fe", "+", "type_story"},
	"remove":      {"\uf146", "-", "type_story"},
	"checklist":   {"\uf0ae", "•", "type_epic"},
	"money":       {"\uf0d6", "•", "type_story"},
	"story":       {"\uf02e", "S", "type_story"},
	"subtask":     {"\uf0da", "↳", "type_subtask"},
	"question":    {"\uf059", "?", "type_alert"},
	"code":        {"\uf121", "•", "type_other"},
	"idea":        {"\uf0eb", "•", "type_alert"},
	"security":    {"\uf023", "•", "type_alert"},
	"mail":        {"\uf0e0", "•", "type_other"},
	"folder":      {"\uf07b", "•", "type_story"},
	"work":        {"\uf0b1", "•", "type_alert"},
	"event":       {"\uf271", "•", "type_story"},
	"devices":     {"\uf109", "•", "type_other"},
	"building":    {"\uf1ad", "•", "type_story"},
	"add-user":    {"\uf234", "•", "type_other"},
	"user-check":  {"\uf007", "•", "type_alert"},
	"user":        {"\uf2bd", "•", "type_other"},
	"monitor":     {"\uf108", "•", "type_story"},
}

// jiraTypeIcon is a nerd-font glyph per issue type, like the GitLab tab's:
// by kind (jira.TypeKind), else by name.
func jiraTypeIcon(kind, name string) string {
	if kind == "" {
		kind = strings.ToLower(name)
	}
	if kind == "sub-task" {
		kind = "subtask"
	}
	t, ok := jiraTypeIcons[kind]
	if !ok {
		t = jiraTypeIcons["task"]
	}
	glyph := t[0]
	if plainIcons {
		glyph = t[1]
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(curTheme[t[2]])).Render(glyph)
}

// plainIcons draws issue types as letters, for terminals without a Nerd
// Font (ui.icons: plain). Set by New.
var plainIcons bool

// jiraPriorityMark marks a card's priority, "" for medium or none: the
// default needs no ink.
// jiraAgeMark is how long an in-progress card has been so: "4d", in the
// over limit colour past stale days; "" for others and under a day.
func jiraAgeMark(c jira.Card, now time.Time, stale int) string {
	if !c.InProgress || c.Since.IsZero() {
		return ""
	}
	days := int(now.Sub(c.Since).Hours() / 24)
	switch {
	case days < 1:
		return ""
	case stale > 0 && days > stale:
		return jiraOverStyle.Render(fmt.Sprintf("%dd!", days)) // ! says it without colour too
	}
	return jiraDimStyle.Render(fmt.Sprintf("%dd", days))
}

// jiraDueMark is an open card's due date against today: "due fri" within
// a week, "due in 12d" later, "overdue 2d" (over limit colour) past it.
func jiraDueMark(c jira.Card, now time.Time) string {
	if c.Due.IsZero() || c.Done {
		return ""
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	due := time.Date(c.Due.Year(), c.Due.Month(), c.Due.Day(), 0, 0, 0, 0, time.Local)
	days := int(math.Round(due.Sub(today).Hours() / 24))
	switch {
	case days < 0:
		return jiraOverStyle.Render(i18n.Tf("overdue %dd", -days))
	case days == 0:
		return jiraOverStyle.Render(i18n.T("due today"))
	case days < 7:
		return jiraDimStyle.Render(i18n.Tf("due %s", strings.ToLower(due.Format("Mon"))))
	}
	return jiraDimStyle.Render(i18n.Tf("due in %dd", days))
}

// jiraSubtaskMark is "☑ 2/5" for a card with subtasks, "" without; all
// done shows in the done colour.
func jiraSubtaskMark(c jira.Card) string {
	if c.Subtasks == 0 {
		return ""
	}
	s := fmt.Sprintf("☑ %d/%d", c.SubtasksDone, c.Subtasks)
	if c.SubtasksDone == c.Subtasks {
		return roadmapDoneStyle.Render(s)
	}
	return jiraDimStyle.Render(s)
}

// jiraPRMark is a card's pull request sign: PR while one is open, a dim
// ✓PR once merged, nothing otherwise.
func jiraPRMark(state string) string {
	switch state {
	case "OPEN":
		return jiraViewActive.Render("PR")
	case "MERGED":
		return jiraDimStyle.Render("✓PR")
	}
	return ""
}

// jiraDeployMark is a card's top deployment environment, "▲ production".
func jiraDeployMark(env string) string {
	if env == "" {
		return ""
	}
	return jiraDimStyle.Render("▲ " + env)
}

func jiraPriorityMark(p string) string {
	st := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	switch strings.ToLower(p) {
	case "highest", "blocker", "critical":
		return st(curTheme["priority_highest"]).Render("⇈")
	case "high", "major":
		return st(curTheme["priority_high"]).Render("↑")
	case "low", "minor":
		return st(curTheme["priority_low"]).Render("↓")
	case "lowest", "trivial":
		return st(curTheme["priority_lowest"]).Render("⇊")
	}
	return ""
}

// renderJira rebuilds the body: the list viewport's content, or the cached
// lane render, with the cursor scrolled into view.
func (m *Model) renderJira() {
	t := m.jiraTab
	if len(m.opts.cardStyles) > 0 {
		t.styleEnv = m.jiraQueryEnv()
	}
	w, h := t.view.Width(), t.view.Height()
	var msg string
	t.empty = emptyHint{row: -1}
	dim := refDimStyle.Render
	switch {
	case t.err != "":
		msg, t.empty = jiraErrorState(t.err, w, h, keySeg(dim(i18n.Tf("%s retries", helpKey(m.keys.Refresh))), m.keys.Refresh), plainSeg(dim(" · ")),
			keySeg(dim(i18n.Tf("%s picks a project", helpKey(m.keys.Project))), m.keys.Project), plainSeg(dim(" · ")),
			keySeg(dim(i18n.Tf("%s runs JQL", helpKey(m.keys.JQL))), m.keys.JQL))
	case t.cfg == nil:
		what := i18n.T("loading")
		if t.project != "" {
			what = i18n.Tf("loading %s", t.project)
		}
		msg, _ = jiraEmptyState(what+"…"+loadingFor(t.loadingSince), w, h)
	case len(t.order) == 0 && t.jiraSearchQuery() != "":
		msg, t.empty = jiraEmptyState(i18n.Tf("No card matches /%s", t.search.Value()), w, h,
			headSeg{s: dim(i18n.T("esc clears the search")), kind: "esc"}, plainSeg(dim(" · ")), keySeg(dim(i18n.Tf("%s builds a filter", helpKey(m.keys.FilterBuilder))), m.keys.FilterBuilder))
	case len(t.cards) == 0 && t.jiraFiltered():
		msg, t.empty = jiraEmptyState(i18n.T("No card matches the filters"), w, h, keySeg(dim(i18n.Tf("%s clears them", helpKey(m.keys.ClearFilters))), m.keys.ClearFilters))
	case len(t.order) == 0:
		title := i18n.T("No issues here")
		hint := []headSeg{keySeg(dim(i18n.Tf("%s refreshes", helpKey(m.keys.Refresh))), m.keys.Refresh), plainSeg(dim(" · ")), keySeg(dim(i18n.Tf("%s adds one", helpKey(m.keys.Create))), m.keys.Create)}
		if v, ok := m.jiraCurrentView(); ok {
			switch v.kind {
			case jiraViewBacklog:
				title, hint = i18n.T("The backlog is empty"), []headSeg{keySeg(dim(i18n.Tf("%s adds an issue", helpKey(m.keys.Create))), m.keys.Create)}
			case jiraViewSprint:
				title, hint = i18n.T("Nothing in this sprint yet"), []headSeg{keySeg(dim(i18n.Tf("%s plans it from the backlog", helpKey(m.keys.Plan))), m.keys.Plan)}
			}
		}
		msg, t.empty = jiraEmptyState(title, w, h, hint...)
	}
	if msg != "" {
		t.view.SetContent(msg)
		t.lanesOut = msg
		return
	}
	if t.arrange != nil && m.jiraShowsLanes() {
		t.lanesOut = m.renderJiraArrange(w, h)
		return
	}
	if m.jiraShowsLanes() {
		t.lanesOut = m.renderJiraLanes(w, h)
		return
	}
	cols := listCols{width: w}
	for _, ci := range t.order {
		c := t.cards[ci]
		cols.key = max(cols.key, len(c.Key))
		cols.status = max(cols.status, visualWidth(c.Status))
		cols.who = max(cols.who, visualWidth(m.jiraListWho(c)))
		cols.marks = max(cols.marks, visualWidth(m.jiraListMarks(c)))
	}
	cols.status, cols.who = min(cols.status, 20), min(cols.who, 24)
	t.listHead, t.headAt, t.headCols = m.jiraListHeader(cols)
	h-- // the header sits over the viewport's top line
	if t.rowsFor != cols || len(t.rows) != len(t.order) {
		t.rows = make([]string, len(t.order))
		for i, ci := range t.order {
			t.rows[i] = m.jiraListRow(t.cards[ci], false, cols)
			if i%2 == 1 { // zebra: every other row faintly shaded
				t.rows[i] = shade(t.rows[i], w)
			}
		}
		t.rowsFor = cols
	}
	var lines []string
	t.lineOf = t.lineOf[:0]
	group := ""
	for i, at := range t.jiraListOrder() {
		ci := t.order[at]
		if g, ok := jiraGroupOf(t.sort, t.cards[ci]); ok && (i == 0 || g != group) {
			group = g
			lines = append(lines, m.jiraGroupHeader(g, i))
		}
		t.lineOf = append(t.lineOf, len(lines))
		row := t.rows[at]
		switch c := t.cards[ci]; {
		case t.drag.active && c.Key == t.drag.key: // being dragged: faint, where it would land
			row = jiraGhostStyle.Render(ansi.Truncate("┊ "+c.Key+" "+c.Summary, w, "…"))
		case at == t.idx:
			row = m.jiraListRow(c, true, cols)
		}
		lines = append(lines, row)
	}
	// One line past the end, as the header covers one: the last row scrolls
	// into sight.
	lines = append(lines, "")
	t.view.SetContentLinesWidth(lines, w)
	top := t.view.YOffset()
	r := 0
	if t.idx < len(t.lineOf) {
		r = t.lineOf[t.idx]
	}
	head := r // a group's first card brings its header into view
	if t.idx < len(t.lineOf) && r > 0 && (t.idx == 0 || t.lineOf[t.idx-1] != r-1) {
		head = r - 1
	}
	switch {
	case t.drag.active: // the pointer scrolls it
	case head < top:
		t.view.SetYOffset(head)
	case r >= top+h:
		t.view.SetYOffset(r - h + 1)
	}
}

// jiraListBody is the list under its header: the viewport less its last
// line.
func (m *Model) jiraListBody() string {
	t := m.jiraTab
	lines := strings.Split(t.view.View(), "\n")
	return strings.Join(append([]string{t.listHead}, lines[:max(len(lines)-1, 0)]...), "\n")
}

// jiraGroupOf is the group a card heads under in list mode: its assignee,
// priority or epic when the list is sorted by that; ok false for others.
func jiraGroupOf(s jiraSort, c jira.Card) (string, bool) {
	switch s {
	case jiraSortAssignee:
		if c.Assignee == "" {
			return i18n.T("Unassigned"), true
		}
		return c.Assignee, true
	case jiraSortPriority:
		if c.Priority == "" {
			return i18n.T("No priority"), true
		}
		return c.Priority, true
	case jiraSortEpic:
		if c.ParentSummary == "" {
			return i18n.T("No epic"), true
		}
		return c.ParentSummary, true
	case jiraSortStatus:
		return c.Status, true
	}
	return "", false
}

// jiraGroupHeader is the header line of the group starting at order index
// from: its name, card count and points.
func (m *Model) jiraGroupHeader(g string, from int) string {
	t := m.jiraTab
	n, pts := 0, 0.0
	for _, ci := range t.order[from:] {
		if cg, _ := jiraGroupOf(t.sort, t.cards[ci]); cg != g {
			break
		}
		n++
		if f, err := strconv.ParseFloat(t.cards[ci].Points, 64); err == nil {
			pts += f
		}
	}
	s := fmt.Sprintf("%s · %d", g, n)
	if pts > 0 {
		s += " · " + strconv.FormatFloat(pts, 'f', -1, 64) + "p"
	}
	return jiraViewActive.Render("── ") + m.jiraGroupAvatar(t.sort, g) + jiraViewActive.Render(s)
}

// jiraGroupAvatar is the chip before a group by assignee's name, "" for
// other groupings and the unassigned.
func (m *Model) jiraGroupAvatar(by jiraSort, g string) string {
	if by != jiraSortAssignee || !m.opts.fields.avatar || g == i18n.T("Unassigned") {
		return ""
	}
	return jiraAvatar(g) + " "
}

// listCols are the list's width and its columns' widths, the widest of
// the rows shown.
type listCols struct {
	width, key, status int
	who, marks         int // the assignee and the marks after it (deploy, subtasks, due, age)
}

// jiraListWho is a list row's assignee, its avatar first; "" when not shown.
func (m *Model) jiraListWho(c jira.Card) string {
	f := m.opts.fields
	switch {
	case !f.assignee || c.Assignee == "":
		return ""
	case f.avatar:
		return jiraAvatar(c.Assignee) + jiraDimStyle.Render(" "+c.Assignee)
	}
	return jiraDimStyle.Render(c.Assignee)
}

// jiraListMarks are a list row's marks after the assignee.
func (m *Model) jiraListMarks(c jira.Card) string {
	f := m.opts.fields
	var marks []string
	if d := jiraDeployMark(c.Deploy); f.deploy && d != "" {
		marks = append(marks, d)
	}
	if st := jiraSubtaskMark(c); f.subtasks && st != "" {
		marks = append(marks, st)
	}
	if d := jiraDueMark(c, time.Now()); f.due && d != "" {
		marks = append(marks, d)
	}
	if a := jiraAgeMark(c, time.Now(), m.opts.staleDays); f.age && a != "" {
		marks = append(marks, a)
	}
	return strings.Join(marks, " ")
}

func (m *Model) jiraListRow(c jira.Card, selected bool, cols listCols) string {
	width, keyW := cols.width, cols.key
	status := ansi.Truncate(c.Status, cols.status, "…")
	status += strings.Repeat(" ", max(cols.status-visualWidth(status), 0))
	pts := "    "
	if c.Points != "" {
		pts = fmt.Sprintf("%4s", c.Points+"p")
	}
	f := m.opts.fields
	// The summary, its parent and custom fields give way on a long row; the
	// tail (assignee, deploy, subtasks, due, age) is what a row is scanned
	// for, in columns at the right edge.
	lk := m.cardLook(c)
	title := c.Summary
	if lk.bold {
		title = lipgloss.NewStyle().Bold(true).Render(title)
	}
	if f.parent && c.ParentSummary != "" {
		title += jiraDimStyle.Render(" · ⌃ " + c.ParentSummary)
	}
	for _, v := range jiraExtraValues(c) {
		title += jiraDimStyle.Render(" · " + v)
	}
	if pr := jiraPRMark(c.PR); f.pr && pr != "" {
		title = pr + " " + title
	}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", max(w-visualWidth(s), 0)) }
	row := "  "
	if r := m.cardRibbon(c); r != "" && !selected {
		row = r + " "
	}
	if col := cardStyleColour(lk.edge); col != nil && !selected {
		row = lipgloss.NewStyle().Foreground(col).Render("▌") + " "
	}
	if hl := m.jiraHighlight(c.Key); hl != "" {
		row = hl + " "
	}
	if f.flagged && c.Flagged {
		title = jiraOverStyle.Render("⚑") + " " + title
	}
	if m.pins[c.Key] {
		title = jiraPinStyle.Render("★") + " " + title
	}
	if tm := m.timerMark(c.Key); tm != "" {
		title = tm + " " + title
	}
	if a := m.agentMark(c.Key); a != "" {
		title = a + " " + title
	}
	for _, col := range m.jiraListLayout(cols) {
		var s string
		switch col.id {
		case "key":
			s = jiraKeyStyle.Render(fmt.Sprintf("%-*s", keyW, c.Key))
		case "type":
			s = jiraTypeIcon(c.TypeKind, c.Type)
		case "priority":
			s = jiraPriorityMark(c.Priority)
		case "status":
			s = jiraDimStyle.Render(status)
		case "points":
			s = jiraDimStyle.Render(pts)
		case "summary":
			s = ansi.Truncate(title, col.w, "…")
		case "assignee":
			s = ansi.Truncate(m.jiraListWho(c), col.w, "…")
		case "marks":
			s = m.jiraListMarks(c)
		}
		row += pad(s, col.w) + strings.Repeat(" ", col.gap)
	}
	row = ansi.Truncate(row, width-1, "…")
	if selected {
		// Plain selection colours, as the selected card: dim status, points
		// and marks sank into the selection background.
		row = stripKeepImages(row)
	} else {
		if lk.fade {
			row = jiraDimStyle.Faint(true).Render(stripKeepImages(row))
		}
		if tint, ok := cardTintStyle(lk.tint); ok {
			row = paintRow(tint, row, width)
		}
	}
	return m.jiraSelect(row, selected, width)
}

// jiraHighlight is the mark of a card a rule highlighted, "" for none.
func (m *Model) jiraHighlight(key string) string {
	if mk := m.jiraMark(key); mk != "" {
		return mk
	}
	c, ok := m.jiraTab.highlights[key]
	if !ok {
		return ""
	}
	if c == "" {
		c = curTheme["highlight"]
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("●")
}

// jiraSelect paints a selected row across width: bright while the board has
// the keys, quiet while the panel does.
func (m *Model) jiraSelect(row string, selected bool, width int) string {
	if !selected {
		return row
	}
	st := diffTreeSelStyle
	if m.focus == focusJira {
		st = selectedRow
	}
	row = keepBG(row, ansiOpenSeq(st))
	if pad := width - visualWidth(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	return st.Render(row)
}

// jiraLanePoints sums the story points of a lane's cards; false when none
// is estimated.
func jiraLanePoints(cards []jira.Card, lane []int) (string, bool) {
	sum, any := 0.0, false
	for _, ci := range lane {
		if f, err := strconv.ParseFloat(cards[ci].Points, 64); err == nil {
			sum, any = sum+f, true
		}
	}
	return strconv.FormatFloat(math.Round(sum*100)/100, 'f', -1, 64), any
}

// jiraLaneLayout is how many lanes fit in width, and how wide each is.
func jiraLaneLayout(width, n int) (visible, laneW int) {
	if n == 0 {
		return 0, width
	}
	visible = min(n, max(width/jiraLaneMinW, 1))
	return visible, width / visible
}

// jiraCardLines is a card's three lines as f picks them: key, marks and
// points; summary; assignee, epic and custom fields. styled false leaves
// them plain, for the drag ghost.
func jiraCardLines(c jira.Card, styled bool, f cardFields) []string {
	return cardLinesFor(c, styled, f, nil, cardLook{})
}

// cardLinesFor is a card's three lines: as l places them and lk styles
// them, or as f picks them when l is nil.
func cardLinesFor(c jira.Card, styled bool, f cardFields, l *cardLayout, lk cardLook) []string {
	if l == nil {
		l = defaultCardLayout(f)
	}
	render := func() []string { return layoutCardLines(c, l, styled, f.stale, lk) }
	if !styled {
		return render()
	}
	now := time.Now().Truncate(time.Minute)
	if !now.Equal(cardLinesAt) {
		clear(cardLinesMemo)
		cardLinesAt = now
	}
	k := cardLinesKey{c, f, plainIcons, l, strings.Join(lk.hidden, ","), lk.bold}
	lines, ok := cardLinesMemo[k]
	if !ok {
		lines = render()
		cardLinesMemo[k] = lines
	}
	return slices.Clone(lines)
}

// cardLinesMemo caches styled card lines: styling is most of a lane frame.
// It is cleared by the minute (due and age marks), a theme and an avatar
// arriving; renders run on one goroutine.
var (
	cardLinesMemo = map[cardLinesKey][]string{}
	cardLinesAt   time.Time
)

type cardLinesKey struct {
	c      jira.Card
	f      cardFields
	plain  bool
	layout *cardLayout
	hidden string
	bold   bool
}

// jiraExtra is a card's ui.custom_fields as name → value, names
// lower-cased.
func jiraExtra(c jira.Card) map[string]string {
	if c.Extra == "" {
		return nil
	}
	out := map[string]string{}
	for _, kv := range strings.Split(c.Extra, jira.ExtraSep) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[strings.ToLower(k)] = v
		}
	}
	return out
}

// jiraExtraValues are a card's custom field values in config order.
func jiraExtraValues(c jira.Card) []string {
	if c.Extra == "" {
		return nil
	}
	var out []string
	for _, kv := range strings.Split(c.Extra, jira.ExtraSep) {
		_, v, _ := strings.Cut(kv, "=")
		out = append(out, v)
	}
	return out
}

// avatarColours are the chips' backgrounds, picked per person by name; the
// initials on them are white whatever the terminal's palette.
var avatarColours = []string{"24", "29", "95", "130", "61", "66", "131", "98"}

// avatars caches jiraAvatar by name; renders run on one goroutine.
var avatars = map[string]string{}

// jiraAvatar is a person's initials on a colour of their own, the same on
// every card and every run.
func jiraAvatar(name string) string {
	if p, ok := avatarPlace[name]; ok {
		return p
	}
	if a, ok := avatars[name]; ok {
		return a
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	bg := avatarColours[h.Sum32()%uint32(len(avatarColours))]
	a := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color(bg)).Bold(true).Render(jiraInitials(name))
	if monoTheme {
		a = lipgloss.NewStyle().Reverse(true).Render(jiraInitials(name))
	}
	avatars[name] = a
	return a
}

// jiraInitials is two letters for a name: first and last word's initials,
// or a single word's first two letters.
func jiraInitials(name string) string {
	words := strings.Fields(name)
	if len(words) == 0 {
		return "??"
	}
	first := []rune(words[0])
	if len(words) == 1 {
		if len(first) == 1 {
			return strings.ToUpper(string(first)) + " "
		}
		return strings.ToUpper(string(first[:2]))
	}
	return strings.ToUpper(string(first[:1]) + string([]rune(words[len(words)-1])[:1]))
}

// jiraDropZones splits a lane body into one drop zone per status while a
// card is dragged over it, the zone under the pointer lit and the card's own
// status marked, as Jira's board does.
func (m *Model) jiraDropZones(lane jiraLane, ghost, width, height int) []string {
	t := m.jiraTab
	cur := ""
	if ghost >= 0 {
		cur = t.cards[ghost].StatusID
	}
	zh := jiraZoneH(height+1, len(lane.statusIDs))
	var out []string
	for i, id := range lane.statusIDs {
		name := m.jiraStatusName(id)
		if id == cur {
			name = i18n.Tf("%s (now)", name)
		}
		style := jiraGhostStyle
		if i == t.drag.zone {
			style = jiraDropStyle
		}
		for r := 0; r < zh; r++ {
			var line string
			switch {
			case r == zh-1 && zh > 1:
				line = "" // a gap between zones
				out = append(out, line)
				continue
			case r == (zh-1)/2:
				line = " " + ansi.Truncate(name, width-2, "…")
			}
			line += strings.Repeat(" ", max(width-visualWidth(line), 0))
			if i != t.drag.zone {
				line = "┊" + line[min(len(line), 1):]
			}
			out = append(out, style.Render(line))
		}
	}
	return out
}

// renderJiraLanes draws the visible lanes side by side: a header with the
// column name and count, then the cards, each lane scrolled to keep the cursor
// on screen. A lane a card is being dragged over lights its header.
func (m *Model) renderJiraLanes(width, height int) string {
	t := m.jiraTab
	visible, laneW := jiraLaneLayout(width, len(t.lanes))
	t.laneW = laneW
	if t.lane < t.firstLane {
		t.firstLane = t.lane
	}
	if t.lane >= t.firstLane+visible {
		t.firstLane = t.lane - visible + 1
	}
	t.firstLane = min(max(t.firstLane, 0), max(len(t.lanes)-visible, 0))
	if t.swim != jiraSortRank {
		return m.renderJiraSwimlanes(visible, laneW, height)
	}
	if t.lane < len(t.laneTop) && t.lane < len(t.lanes) {
		top := &t.laneTop[t.lane]
		if t.row < *top {
			*top = t.row
		}
		// Down until the cursor's card fits, section headers and all.
		for *top < t.row && m.jiraLaneSpan(t.lane, *top, t.row) > height-1 {
			*top++
		}
	}
	ghost := -1 // the dragged card, as an index into cards
	if t.drag.active {
		ghost = slices.IndexFunc(t.cards, func(c jira.Card) bool { return c.Key == t.drag.key })
	}
	cols := make([][]string, 0, visible)
	t.laneAt = t.laneAt[:0]
	for l := t.firstLane; l < t.firstLane+visible && l < len(t.lanes); l++ {
		inner := laneW - 1
		lane := t.lanes[l]
		head := m.jiraLaneHead(l, inner)
		col := []string{head}
		var at []int // the body lines' card rows
		if t.drag.active && l == t.drag.over && len(lane.statusIDs) > 1 {
			cols = append(cols, append(col, m.jiraDropZones(lane, ghost, inner, height-1)...))
			t.laneAt = append(t.laneAt, nil)
			continue
		}
		// While a card is dragged over this lane, its ghost sits where the card
		// will land: lanes keep the view's rank order, so after every card of
		// the lane ranked above it.
		type slot struct {
			ci    int
			ghost bool
		}
		slots := make([]slot, 0, len(lane.cards)+1)
		for _, ci := range lane.cards {
			slots = append(slots, slot{ci: ci})
		}
		top := 0
		if l < len(t.laneTop) {
			top = min(t.laneTop[l], max(len(lane.cards)-1, 0))
		}
		if g := ghost; g >= 0 && l == t.drag.over && (l != t.drag.from || t.drag.slotOK) {
			at := 0
			if t.drag.slotOK {
				// The ghost sits where the drop ranks it, its old place
				// closed up.
				slots = slices.DeleteFunc(slots, func(s slot) bool { return s.ci == g })
				at = min(t.drag.slot, len(slots))
			} else {
				for at < len(lane.cards) && lane.cards[at] < g {
					at++
				}
			}
			slots = slices.Insert(slots, at, slot{ci: g, ghost: true})
			top = min(top, at)
			if fit := max((height-1)/m.cardSlot(), 1); at >= top+fit {
				top = at - fit + 1
			}
		}
		gap := m.cardSlot() > m.cardH()
		for r := top; r < len(slots) && len(col)+m.cardH() <= height; r++ {
			folded := m.jiraFolded(l, r)
			if s := lane.headAt(r); s >= 0 {
				if !folded && len(col)+1+m.cardH() > height {
					break // no header without its card
				}
				col = append(col, m.jiraSectionHead(lane.sections[s], inner, folded))
				at = append(at, -2-s)
			}
			if folded {
				continue
			}
			c := t.cards[slots[r].ci]
			if slots[r].ghost || (slots[r].ci == ghost && l == t.drag.from && t.drag.over != l) {
				// The ghost, and the card it left behind: plain text, faint.
				for _, line := range m.cardLines(c, false) {
					col = append(col, jiraGhostStyle.Render(ansi.Truncate("┊ "+line, inner, "…")))
					at = append(at, -1)
				}
				if gap {
					col = append(col, "")
					at = append(at, -1)
				}
				continue
			}
			row := slices.Index(lane.cards, slots[r].ci)
			sel := l == t.lane && t.row < len(lane.cards) && lane.cards[t.row] == slots[r].ci
			lines := m.jiraLaneCard(c, sel, inner)
			col = append(col, lines...)
			for range lines {
				at = append(at, row)
			}
			if gap {
				col = append(col, "")
				at = append(at, -1)
			}
		}
		if len(slots) == 0 {
			col = append(col, jiraGhostStyle.Render(i18n.T(" nothing here")))
		}
		cols = append(cols, col)
		t.laneAt = append(t.laneAt, at)
	}
	sep := shade(jiraDimStyle.Render("│"), 1)
	lines := make([]string, height)
	for y := range lines {
		var b strings.Builder
		for i, col := range cols {
			cell := ""
			if y < len(col) {
				cell = col[y]
			}
			b.WriteString(canvasCell(cell, laneW-1))
			if i < len(cols)-1 {
				b.WriteString(sep)
			}
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}

// jiraLaneSpan is how many lines lane's cards top to row take, the
// section headers among them counted.
func (m *Model) jiraLaneSpan(l, top, row int) int {
	lane, n := m.jiraTab.lanes[l], 0
	for r := top; r <= row; r++ {
		if lane.headAt(r) >= 0 {
			n++
		}
		if !m.jiraFolded(l, r) {
			n += m.cardSlot()
		}
	}
	return n
}

// jiraSectionHead is a stacked lane's line over a column's cards: its name
// and count on a rule, a ▸ when folded.
func (m *Model) jiraSectionHead(s jiraSection, inner int, folded bool) string {
	mark := "─ "
	if folded {
		mark = "▸ "
	}
	label := mark + ansi.Truncate(s.name, max(inner-8, 1), "…") + " " + strconv.Itoa(s.n) + " "
	return jiraDimStyle.Render(label + strings.Repeat("─", max(inner-1-visualWidth(label), 0)))
}

// screenErrHints are what a screen over the board offers when its load
// failed: retry, or back to the board.
func (m *Model) screenErrHints() []headSeg {
	dim := refDimStyle.Render
	return []headSeg{plainSeg(dim(i18n.Tf("%s retries · esc back to the board", helpKey(m.keys.Refresh))))}
}

// jiraErrorState is a failed load: the error wrapped to the width, centred
// a third of the way down, and what to do about it.
func jiraErrorState(err string, w, h int, hint ...headSeg) (string, emptyHint) {
	var b strings.Builder
	top := max(h/3, 0)
	b.WriteString(strings.Repeat("\n", top))
	lines := strings.Split(lipgloss.NewStyle().Width(max(min(w-4, 80), 10)).Render(err), "\n")
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		b.WriteString(strings.Repeat(" ", max((w-visualWidth(l))/2, 0)) + refErrStyle.Render(l) + "\n")
	}
	s := ansi.Truncate(joinSegs(hint), max(w, 1), "…")
	left := max((w-visualWidth(s))/2, 0)
	b.WriteString(strings.Repeat(" ", left) + s)
	return b.String(), emptyHint{row: top + len(lines), left: left, segs: hint}
}

// emptyHint is where an empty board's hint line is: its body row, its
// first column and its segments (each a click on its key).
type emptyHint struct {
	row, left int
	segs      []headSeg
}

// jiraEmptyState is what an empty board shows: a title and what to do
// about it, centred a third of the way down.
func jiraEmptyState(title string, w, h int, hint ...headSeg) (string, emptyHint) {
	pad := func(s string) int { return max((w-visualWidth(s))/2, 0) }
	t := ansi.Truncate(titleStyle.Render(title), max(w, 1), "…")
	s := ansi.Truncate(joinSegs(hint), max(w, 1), "…")
	top := max(h/3, 0)
	return strings.Repeat("\n", top) + strings.Repeat(" ", pad(t)) + t + "\n" + strings.Repeat(" ", pad(s)) + s,
		emptyHint{row: top + 1, left: pad(s), segs: hint}
}

// canvasCell is a lane cell w wide: a card's line (full width already) on
// the terminal's background, anything else on the shaded canvas around
// the cards.
func canvasCell(cell string, w int) string {
	pad := w - visualWidth(cell)
	switch {
	case pad <= 0:
		return cell
	case shadeOn:
		return shade(cell, w)
	}
	return cell + strings.Repeat(" ", pad)
}

// jiraLaneCard is a card's lines in a lane, inner wide.
func (m *Model) jiraLaneCard(c jira.Card, sel bool, inner int) []string {
	lines := m.cardLines(c, true)
	if m.pins[c.Key] {
		lines[0] = jiraPinStyle.Render("★") + " " + lines[0]
	}
	if a := m.agentMark(c.Key); a != "" {
		lines[0] = a + " " + lines[0]
	}
	if hl := m.jiraHighlight(c.Key); hl != "" {
		lines[0] = hl + " " + lines[0]
	}
	lk := m.cardLook(c)
	ribbon := m.cardRibbon(c)
	if col := cardStyleColour(lk.edge); col != nil {
		ribbon = lipgloss.NewStyle().Foreground(col).Render("▌")
	}
	if ribbon != "" {
		inner--
	}
	tint, tinted := cardTintStyle(lk.tint)
	for i, line := range lines {
		line = ansi.Truncate(cardSides(line, inner), inner, "…")
		if lk.fade && !sel {
			line = jiraDimStyle.Faint(true).Render(stripKeepImages(line))
		}
		switch {
		case sel: // plain: dim marks vanish on the selection colour
			lines[i] = ribbon + m.jiraSelect(stripKeepImages(line), true, inner)
		case tinted:
			lines[i] = ribbon + paintRow(tint, line, inner)
		default: // full width, on the terminal's own background
			lines[i] = ribbon + line + strings.Repeat(" ", max(inner-visualWidth(line), 0))
		}
	}
	return lines
}

// cardRibbon is the card's colour from the board's settings as a thin bar,
// "" with ui.card_colors off or no colour for it.
func (m *Model) cardRibbon(c jira.Card) string {
	col := m.cardColor(c)
	if col == "" || m.opts.cardColors != "ribbon" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Render("▌")
}

// cardColor is the colour the board gives card c, "" for none.
func (m *Model) cardColor(c jira.Card) string {
	t := m.jiraTab
	if t.colorsBoard != m.jiraBoardID() {
		return ""
	}
	if t.colors.By == "custom" {
		return t.colorKeys[c.Key]
	}
	var have []string
	switch t.colors.By {
	case "priority":
		have = []string{c.Priority}
	case "issuetype":
		have = []string{c.Type}
	case "assignee":
		have = []string{c.Assignee, c.AssigneeID}
	}
	for _, col := range t.colors.Colors {
		for _, h := range have {
			if h != "" && strings.EqualFold(h, col.Value) {
				return col.Color
			}
		}
	}
	return ""
}

// cardColorsMsg is a board's card colours read, with the keys a custom
// colour's JQL takes.
type cardColorsMsg struct {
	board  int
	colors jira.CardColors
	keys   map[string]string
	err    error
}

// fetchCardColors reads the current board's card colours once a session
// (custom ones again with each load, their JQL judging the issues).
func (m *Model) fetchCardColors() tea.Cmd {
	t := m.jiraTab
	board := m.jiraBoardID()
	if m.opts.cardColors == "off" || board == 0 || t.cfg == nil || (t.colorsBoard == board && t.colors.By != "custom") {
		return nil
	}
	c, ctx, project := m.jiraClient, m.ctx, t.project
	return func() tea.Msg {
		cc, err := c.CardColors(ctx, board)
		if err != nil || cc.By != "custom" {
			return cardColorsMsg{board: board, colors: cc, err: err}
		}
		scope := ""
		if project != "" {
			scope = fmt.Sprintf("project = %q", project)
		}
		keys, err := c.CardColorKeys(ctx, cc, scope)
		return cardColorsMsg{board: board, colors: cc, keys: keys, err: err}
	}
}

func (m Model) handleCardColors(msg cardColorsMsg) (tea.Model, tea.Cmd) {
	t := m.jiraTab
	if msg.board != m.jiraBoardID() {
		return m, nil
	}
	t.colorsBoard, t.colors, t.colorKeys = msg.board, msg.colors, msg.keys
	if msg.err != nil { // an instance without the edit model: no colours
		t.colors = jira.CardColors{}
	}
	t.rows = nil
	m.renderJira()
	return m, nil
}

// renderJiraSwimlanes draws the lanes cut into swimlanes: a band per
// assignee (or epic, or priority) across every lane, headed by its name, its cards side
// by side. The bands scroll together, keeping the cursor's card in view.
func (m *Model) renderJiraSwimlanes(visible, laneW, height int) string {
	t := m.jiraTab
	inner := laneW - 1
	sep := shade(jiraDimStyle.Render("│"), 1)
	shown := t.lanes[t.firstLane:min(t.firstLane+visible, len(t.lanes))]
	row := func(cells []string) string {
		var b strings.Builder
		for i, cell := range cells {
			b.WriteString(canvasCell(cell, inner))
			if i < len(cells)-1 {
				b.WriteString(sep)
			}
		}
		return b.String()
	}
	totalW := len(shown)*laneW - 1
	heads := make([]string, len(shown))
	for i := range shown {
		heads[i] = m.jiraLaneHead(t.firstLane+i, inner)
	}
	// Each lane is sorted by group, so a group is a run in every lane;
	// next[i] is where lane i's current run starts.
	groups, rep := m.jiraSwimGroups()
	// Lay the body out first, then draw only the lines on screen: each line
	// is a band's header, a card row's line y (its first line at start), or
	// a gap.
	type swimLine struct {
		head  string // the band's name on its header line
		count int
		pts   string // its points, "" when none are estimated
		at    []int  // the row's card row per shown lane, -1 for none
		y     int    // the card line, -1 for a header or gap
		start int    // the row's first line
	}
	var body []swimLine
	t.swimAt, t.swimBand, t.swimHead = t.swimAt[:0], t.swimBand[:0], t.swimHead[:0]
	blank := make([]int, len(shown))
	for i := range blank {
		blank[i] = -1
	}
	next := make([]int, len(shown))
	selLine := 0
	for _, g := range groups {
		runs := make([][]int, len(shown)) // per shown lane, its rows in g
		n, cards := 0, 0
		for i, l := range shown {
			for next[i] < len(l.cards) {
				if cg, _ := jiraGroupOf(t.swim, t.cards[l.cards[next[i]]]); cg != g {
					break
				}
				runs[i] = append(runs[i], next[i])
				next[i]++
			}
			n, cards = max(n, len(runs[i])), cards+len(runs[i])
		}
		if cards == 0 {
			continue
		}
		var idx []int
		for i, run := range runs {
			for _, r := range run {
				idx = append(idx, shown[i].cards[r])
			}
		}
		pts, ok := jiraLanePoints(t.cards, idx)
		if !ok || !m.opts.fields.points {
			pts = ""
		}
		body = append(body, swimLine{head: g, count: cards, pts: pts, y: -1})
		t.swimAt, t.swimBand, t.swimHead = append(t.swimAt, blank), append(t.swimBand, rep[g]), append(t.swimHead, g)
		if t.swimFold[g] {
			continue
		}
		for r := range n {
			at := make([]int, len(shown))
			for i := range shown {
				at[i] = -1
				if r < len(runs[i]) {
					at[i] = runs[i][r]
					if t.firstLane+i == t.lane && at[i] == t.row {
						selLine = len(body)
					}
				}
			}
			start := len(body)
			for y := range m.cardH() {
				body = append(body, swimLine{at: at, y: y, start: start})
				t.swimAt, t.swimBand, t.swimHead = append(t.swimAt, at), append(t.swimBand, rep[g]), append(t.swimHead, "")
			}
			if m.cardSlot() > m.cardH() {
				body = append(body, swimLine{y: -1})
				t.swimAt, t.swimBand, t.swimHead = append(t.swimAt, blank), append(t.swimBand, rep[g]), append(t.swimHead, "")
			}
		}
	}
	h := max(height-1, 1)
	if selLine < t.swimTop+1 {
		t.swimTop = max(selLine-1, 0) // keep the band's header in view too
	}
	if selLine+m.cardH() > t.swimTop+h {
		t.swimTop = selLine + m.cardH() - h
	}
	t.swimTop = min(t.swimTop, max(len(body)-h, 0))
	cells := map[int][][]string{} // a row's drawn cards, by its first line
	lines := []string{row(heads)}
	for y := t.swimTop; y < len(body) && len(lines) < height; y++ {
		bl := body[y]
		switch {
		case bl.head != "":
			sign := "▾ "
			if t.swimFold[bl.head] {
				sign = "▸ "
			}
			n := fmt.Sprintf(" · %d", bl.count)
			if bl.pts != "" {
				n += " · " + bl.pts + "p"
			}
			lines = append(lines, shade(jiraViewActive.Render(sign)+m.jiraGroupAvatar(t.swim, bl.head)+jiraViewActive.Render(bl.head)+jiraDimStyle.Render(n), totalW))
			continue
		case bl.y < 0:
			lines = append(lines, row(make([]string, len(shown))))
			continue
		}
		cs, ok := cells[bl.start]
		if !ok {
			cs = make([][]string, len(shown))
			for i, ri := range bl.at {
				if ri < 0 {
					continue
				}
				c := t.cards[shown[i].cards[ri]]
				cs[i] = m.jiraLaneCard(c, t.firstLane+i == t.lane && ri == t.row, inner)
				if t.drag.active && c.Key == t.drag.key { // being dragged: faint where it was
					for y, line := range m.cardLines(c, false) {
						cs[i][y] = jiraGhostStyle.Render(ansi.Truncate("┊ "+line, inner, "…"))
					}
				}
			}
			cells[bl.start] = cs
		}
		line := make([]string, len(shown))
		for i := range shown {
			if cs[i] != nil {
				line[i] = cs[i][bl.y]
			}
		}
		lines = append(lines, row(line))
	}
	for len(lines) < height {
		lines = append(lines, row(make([]string, len(shown))))
	}
	return strings.Join(lines, "\n")
}

// jiraLaneHead is lane l's heading: name, count (of its WIP limit) and
// points, lit for the cursor lane, a drop target or a limit broken.
func (m *Model) jiraLaneHead(l, inner int) string {
	t := m.jiraTab
	lane := t.lanes[l]
	count := strconv.Itoa(len(lane.cards))
	// Filtered counts undercount the column, so only a full board judges it.
	over := lane.max > 0 && len(lane.cards) > lane.max && !t.jiraFiltered() && t.jiraSearchQuery() == ""
	if lane.max > 0 {
		count += "/" + strconv.Itoa(lane.max)
	}
	if over {
		count += "!" // over its limit, without colour too
	}
	if pts, ok := jiraLanePoints(t.cards, lane.cards); ok && m.opts.fields.points {
		count += " · " + pts + "p"
	}
	// The outer lanes on screen say how many more lie past them.
	before, after := "", ""
	if vis, _ := jiraLaneLayout(t.view.Width(), len(t.lanes)); vis < len(t.lanes) {
		if l == t.firstLane && l > 0 {
			before = fmt.Sprintf("‹%d ", l)
		}
		if rest := len(t.lanes) - t.firstLane - vis; l == t.firstLane+vis-1 && rest > 0 {
			after = fmt.Sprintf(" %d›", rest)
		}
	}
	head := ansi.Truncate(lane.name+" "+count, max(inner-2-visualWidth(before+after), 1), "…")
	if t.confetti.on(lane.name) {
		after = confettiStyle.Render(" "+t.confetti.line(inner-3-visualWidth(before+head+after))) + after
	}
	switch {
	case over && !(t.drag.active && l == t.drag.over):
		head = jiraOverStyle.Underline(l == t.lane).Render(head)
	case t.drag.active && l == t.drag.over:
		head = jiraDropStyle.Render(head + strings.Repeat(" ", max(inner-visualWidth(head), 0)))
	case l == t.lane:
		head = jiraViewActive.Render(head)
	default:
		head = jiraLaneStyle.Render(head)
	}
	return laneMark[m.laneCategory(lane.col)].Render("▍") + " " + jiraDimStyle.Render(before) + head + jiraDimStyle.Render(after)
}

// statusCategory is status id's category as a loaded card in it shows,
// else def.
func (m *Model) statusCategory(id, def string) string {
	for _, c := range m.jiraTab.cards {
		if c.StatusID != id {
			continue
		}
		switch {
		case c.Done:
			return "done"
		case c.InProgress:
			return "indeterminate"
		}
		return "new"
	}
	return def
}

// laneMark colours a lane head's mark by its status category, as the
// panel's lozenges do. Set by applyTheme.
var laneMark map[string]lipgloss.Style

// laneCategory is column l's (an index into cols) status category (new,
// indeterminate, done): its statuses' as the loaded cards show them, else
// by place, first to do and last done.
func (m *Model) laneCategory(l int) string {
	t := m.jiraTab
	for _, id := range t.cols[l].statusIDs {
		for _, c := range t.cards {
			if c.StatusID != id {
				continue
			}
			switch {
			case c.Done:
				return "done"
			case c.InProgress:
				return "indeterminate"
			}
			return "new"
		}
	}
	switch l {
	case 0:
		return "new"
	case len(t.cols) - 1:
		return "done"
	}
	return "indeterminate"
}

// jiraViewSep parts the header's views.
const jiraViewSep = "  │  "

// jiraGoalMax is how much of a sprint's goal the views row shows.
const jiraGoalMax = 32

// jiraViewsFit is the views the row shows in width, [first, last): from
// far enough on to show the active one, then on while they fit. A "‹" or
// "›" and its separator stand for the ones left out.
func jiraViewsFit(views []jiraView, active, width int) (first, last int) {
	if active < 0 || active >= len(views) {
		return 0, len(views)
	}
	sep := ansi.StringWidth(jiraViewSep)
	more := func(first, last int) int { // the ‹ and › the range needs
		n := 0
		if first > 0 {
			n += 1 + sep
		}
		if last < len(views) {
			n += 1 + sep
		}
		return n
	}
	w := ansi.StringWidth(views[active].name)
	first, last = active, active+1
	for first > 0 && w+sep+ansi.StringWidth(views[first-1].name)+more(first-1, last) <= width {
		first--
		w += sep + ansi.StringWidth(views[first].name)
	}
	for last < len(views) && w+sep+ansi.StringWidth(views[last].name)+more(first, last+1) <= width {
		w += sep + ansi.StringWidth(views[last].name)
		last++
	}
	return first, last
}

// renderJiraPane draws the tab body: the board pane, plus the reference panel
// on the right while one is open.
func (m *Model) renderJiraPane(height, width int) string {
	t := m.jiraTab
	listW, refW := m.jiraListWidth(width)
	innerH := max(height-1, 1)
	boxW := listW
	if refW > 0 {
		boxW++ // its right border is cut below; the panel's left border divides
	}

	head := ansi.Truncate(joinSegs(m.jiraTitleSegs()), max(boxW-2, 1), "…")
	rule := refDimStyle.Render(strings.Repeat("─", max(boxW-2, 1)))

	t.viewsW = max(boxW-3, 1) // a cell for the truncation's …
	viewLine := joinSegs(m.jiraViewSegs())
	viewLine = ansi.Truncate(viewLine, max(boxW-2, 1), "…")
	filterLine := ansi.Truncate(m.jiraFilterLine(), max(boxW-2, 1), "…")

	body := t.view.View()
	switch {
	case t.roadmap != nil:
		viewLine = ansi.Truncate(m.roadmapLine(), max(boxW-2, 1), "…")
		filterLine = ""
		body = m.renderRoadmap(t.view.Width(), t.view.Height())
	case t.charts != nil:
		viewLine = ansi.Truncate(m.chartsLine(), max(boxW-2, 1), "…")
		filterLine = ""
		body = m.renderCharts(t.view.Width(), t.view.Height())
	case t.week != nil:
		viewLine = ansi.Truncate(m.weekLine(), max(boxW-2, 1), "…")
		filterLine = ""
		body = m.renderWeek(t.view.Width(), t.view.Height())
	case t.standup != nil:
		viewLine = ansi.Truncate(m.standupViewLine(), max(boxW-2, 1), "…")
		filterLine = m.standupStrip(time.Now(), max(boxW-2, 1))
		body = m.renderStandup(t.view.Width(), t.view.Height())
	case t.inbox != nil:
		viewLine = ansi.Truncate(m.inboxViewLine(), max(boxW-2, 1), "…")
		filterLine = ""
		body = m.renderInbox(t.view.Width(), t.view.Height())
	case t.agentsView != nil:
		viewLine = ansi.Truncate(m.agentsViewLine(), max(boxW-2, 1), "…")
		filterLine = ""
		body = m.renderAgentsScreen(t.view.Width(), t.view.Height())
	case t.mrs != nil:
		viewLine = ansi.Truncate(m.mrsViewLine(), max(boxW-2, 1), "…")
		filterLine = ""
		body = m.renderMRs(t.view.Width(), t.view.Height())
	case t.plan != nil:
		viewLine = ansi.Truncate(m.planLine(), max(boxW-2, 1), "…")
		filterLine = ""
		body = m.renderPlan(t.view.Width(), t.view.Height())
	case m.jiraShowsLanes() || t.cfg == nil || len(t.order) == 0:
		body = t.lanesOut
	default:
		body = m.jiraListBody()
	}
	rows := []string{head, rule, viewLine, filterLine, body}
	borderColor := dimColor
	if m.focus == focusJira {
		borderColor = focusedColor
	}
	content := strings.Join(rows, "\n")
	var box string
	ok := false
	if refW == 0 {
		box, ok = renderPaneBox(content, boxW, innerH, borderColor)
	} else {
		box, ok = renderPanelBox(content, listW, innerH, borderColor)
	}
	cut := !ok && refW > 0
	if !ok {
		box = lipgloss.NewStyle().Border(border).UnsetBorderTop().
			Width(boxW).Height(innerH).BorderForeground(borderColor).Render(content)
	}
	list := joinRuleRows(box, 1)
	if refW == 0 {
		return list
	}
	if cut {
		lines := strings.Split(list, "\n")
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, listW, "")
		}
		list = strings.Join(lines, "\n")
	}
	ref := m.renderRefPane(height, refW)
	if pane, ok := joinBeside(list, ref); ok {
		return pane
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, list, ref)
}

// jiraSprintBar is the sprint's progress as a thin bar and a count: done
// points of all when any card has points, else done issues; "" with none.
func jiraSprintBar(cards []jira.Card) string {
	var done, total float64
	pointed := slices.ContainsFunc(cards, func(c jira.Card) bool { return c.Points != "" })
	for _, c := range cards {
		n := 1.0
		if pointed {
			n, _ = strconv.ParseFloat(c.Points, 64)
		}
		total += n
		if c.Done {
			done += n
		}
	}
	if total == 0 {
		return ""
	}
	const cells = 10
	full := int(math.Round(done / total * cells))
	unit := ""
	if pointed {
		unit = "p"
	}
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
	return roadmapDoneStyle.Render(strings.Repeat("▰", full)) + jiraDimStyle.Render(strings.Repeat("▱", cells-full)+" "+num(done)+"/"+num(total)+unit)
}

// workdaysLeft counts the workdays from now's day up to, not including,
// end's day (ui.workdays, Monday to Friday by default).
func workdaysLeft(now, end time.Time, workdays []time.Weekday) int {
	if len(workdays) == 0 {
		workdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	}
	end = end.In(now.Location())
	last := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, now.Location())
	n := 0
	for d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()); d.Before(last); d = d.AddDate(0, 0, 1) {
		if slices.Contains(workdays, d.Weekday()) {
			n++
		}
	}
	return n
}

// jiraSprintLine is a sprint view's time and goal: "5d left · 3 workdays ·
// Ship it".
func jiraSprintLine(v jiraView, now time.Time, workdays []time.Weekday) string {
	if v.kind != jiraViewSprint {
		return ""
	}
	var parts []string
	days := func(t time.Time) int { return int(math.Ceil(t.Sub(now).Hours() / 24)) }
	switch {
	case !v.closed.IsZero():
		parts = append(parts, i18n.Tf("closed %s", v.closed.Local().Format("Jan 2")))
	case !v.start.IsZero() && v.start.After(now):
		parts = append(parts, i18n.Tf("starts %s", v.start.Local().Format("Jan 2")))
	case !v.end.IsZero() && days(v.end) > 0:
		left := i18n.Tf("%dd left", days(v.end))
		if n := workdaysLeft(now, v.end, workdays); n != 1 {
			left += i18n.Tf(" · %d workdays", n)
		} else {
			left += i18n.T(" · 1 workday")
		}
		parts = append(parts, left)
	case !v.end.IsZero():
		parts = append(parts, i18n.Tf("ended %s", v.end.Local().Format("Jan 2")))
	}
	if g := safeterm.Line(strings.Join(strings.Fields(v.goal), " ")); g != "" {
		parts = append(parts, ansi.Truncate(g, jiraGoalMax, "…")) // a click shows it whole
	}
	return strings.Join(parts, " · ")
}

// jiraFilterLine shows the filters and their keys, the ones that are on lit.
func (m *Model) jiraFilterLine() string { return joinSegs(m.jiraFilterSegs()) }

// hitJira maps a screen cell on the board to a card: idx is the lane (-1 in
// list mode and above the board) and line the card's row in it, -1 over no
// card; band is the swimlane whose header it is on.
func (m *Model) hitJira(x, y int) hit {
	t := m.jiraTab
	line := y - jiraBodyTop
	if line < 0 || t.arrange != nil {
		return hit{zone: hitJira, idx: -1, line: -1}
	}
	if !m.jiraShowsLanes() {
		if line == 0 { // the column header
			h := hit{zone: hitJira, idx: -1, line: -1}
			for _, c := range t.headCols {
				if x-1 >= c.x0 && x-1 < c.x1 {
					h.headCol = c.col
				}
			}
			for _, c := range t.headAt {
				if x-1 >= c.x0 && x-1 < c.x1 {
					h.sortBy = int(c.by) + 1
				}
			}
			return h
		}
		if line--; line >= 0 && line < t.view.Height()-1 {
			if i := slices.Index(t.lineOf, t.view.YOffset()+line); i >= 0 {
				return hit{zone: hitJira, idx: -1, line: i}
			}
			if i := slices.Index(t.lineOf, t.view.YOffset()+line+1); i >= 0 { // a group's header: its first card
				return hit{zone: hitJira, idx: -1, line: i}
			}
		}
		return hit{zone: hitJira, idx: -1, line: -1}
	}
	visible, laneW := jiraLaneLayout(t.view.Width(), len(t.lanes))
	col := (x - 1) / max(laneW, 1)
	if x < 1 || col >= visible || t.firstLane+col >= len(t.lanes) {
		return hit{zone: hitJira, idx: -1, line: -1}
	}
	lane := t.firstLane + col
	h := hit{zone: hitJira, idx: lane, line: -1}
	if t.swim != jiraSortRank {
		if y := line - 1 + t.swimTop; line >= 1 && y < len(t.swimAt) && col < len(t.swimAt[y]) {
			h.line, h.band = t.swimAt[y][col], t.swimHead[y]
		}
		return h
	}
	if line >= 1 && col < len(t.laneAt) && t.laneAt[col] != nil {
		if line-1 < len(t.laneAt[col]) {
			h.line = t.laneAt[col][line-1]
		}
		if h.line <= -2 { // a section's header
			h.section, h.line = -1-h.line, -1
		}
	} else if line >= 1 {
		r := (line-1)/m.cardSlot() + t.laneTop[lane]
		if (line-1)%m.cardSlot() < m.cardH() && r < len(t.lanes[lane].cards) {
			h.line = r
		}
	}
	return h
}

// clickJira selects the clicked card and arms a drag on it; a double-click
// opens it in the panel.
func (m Model) clickJira(h hit, x, y, count int) (tea.Model, tea.Cmd) {
	t := m.jiraTab
	onMark := count == 1 && h.line >= 0 && len(m.agents) > 0 && isAgentGlyph(m.frameCell(x, y))
	m.focus = focusJira
	if h.band != "" {
		m.toggleJiraSwimlane(h.band)
		return m, nil
	}
	if h.headCol != "" { // a click sorts on the release, a drag moves the column
		t.headDrag = headDrag{col: h.headCol, sortBy: h.sortBy, x: x}
		return m, nil
	}
	if h.section > 0 && h.idx >= 0 {
		t.lane = h.idx
		m.toggleJiraSection(h.idx, h.section-1)
		return m, nil
	}
	if h.line < 0 {
		if h.idx >= 0 {
			t.lane = h.idx
			m.clampJiraCursor()
		}
		m.renderJira()
		return m, nil
	}
	onMark = onMark || t.past != nil // replayed cards don't drag
	if h.idx < 0 {
		t.idx = h.line
		if c, ok := m.selectedJiraCard(); ok && count == 1 && !onMark {
			t.drag = jiraDrag{key: c.Key, x: x, y: y, from: -1, over: -1}
		}
	} else {
		t.lane, t.row = h.idx, h.line
		if c, ok := m.selectedJiraCard(); ok && count == 1 && !onMark {
			t.drag = jiraDrag{key: c.Key, x: x, y: y, from: h.idx, over: h.idx}
		}
	}
	if c, ok := m.selectedJiraCard(); ok && onMark && len(m.agents[c.Key]) > 0 && !(m.jiraTab.isMarked(c.Key) && m.frameCell(x, y) == "✓") {
		m.renderJira()
		return m, m.attachAgent(c.Key, m.agents[c.Key][0].PaneID)
	}
	m.renderJira()
	if count == 2 {
		return m.openJiraCard()
	}
	return m, nil
}

// dragJira follows a dragged card: the lane under the pointer lights up, and
// the pointer at the pane's edge scrolls hidden lanes into view.
func (m Model) dragJira(x, y int) (tea.Model, tea.Cmd) {
	t := m.jiraTab
	if !t.drag.active {
		if x-t.drag.x < 2 && t.drag.x-x < 2 && y == t.drag.y {
			return m, nil
		}
		t.drag.active = true
	}
	if !m.jiraShowsLanes() {
		return m.dragJiraList(y)
	}
	visible, _ := jiraLaneLayout(t.view.Width(), len(t.lanes))
	listW, _ := m.jiraListWidth(m.width)
	over, scrolled := t.drag.over, true
	switch {
	case x >= listW-2 && t.firstLane+visible < len(t.lanes):
		t.firstLane++
		over = t.firstLane + visible - 1
		t.lane = over // renderJiraLanes keeps the cursor lane on screen
	case x <= 1 && t.firstLane > 0:
		t.firstLane--
		over = t.firstLane
		t.lane = over
	default:
		scrolled = false
		if h := m.hitJira(x, y); h.idx >= 0 {
			over = h.idx
		}
	}
	zone := 0
	if t.swim != jiraSortRank {
		zone = -1 // swimlanes draw no status zones: the lane's first status
	} else if over >= 0 && over < len(t.lanes) {
		if n := len(t.lanes[over].statusIDs); n > 1 {
			zone = min(max((y-jiraBodyTop-1)/jiraZoneH(t.view.Height(), n), 0), n-1)
		}
	}
	if t.swim != jiraSortRank {
		if l := y - jiraBodyTop - 1 + t.swimTop; y-jiraBodyTop >= 1 && l < len(t.swimBand) {
			t.drag.band, t.drag.bandOK = t.swimBand[l], true
		}
	}
	slot, slotOK := m.jiraDropSlot(over, y)
	if over != t.drag.over || zone != t.drag.zone || slot != t.drag.slot || slotOK != t.drag.slotOK || scrolled {
		t.drag.over, t.drag.zone, t.drag.slot, t.drag.slotOK = over, zone, slot, slotOK
		m.renderJira()
	}
	return m, nil
}

// dragJiraList follows a card dragged in list mode: sorted by rank, the
// row under the pointer is where it would be ranked.
func (m Model) dragJiraList(y int) (tea.Model, tea.Cmd) {
	t := m.jiraTab
	if t.sort != jiraSortRank {
		m.status = i18n.Tf("ranking needs the list sorted by rank (%s)", helpKey(m.keys.Sort))
		return m, nil
	}
	switch line := y - jiraBodyTop; {
	case line < 1: // over the column header too
		t.view.ScrollUp(1)
	case line >= t.view.Height():
		t.view.ScrollDown(1)
	}
	slot := t.drag.slot
	if h := m.hitJira(1, min(max(y, jiraBodyTop+1), jiraBodyTop+t.view.Height()-1)); h.line >= 0 {
		slot = h.line
	}
	if slot != t.drag.slot || !t.drag.slotOK {
		t.drag.slot, t.drag.slotOK = slot, true
		m.status = i18n.Tf("drop to rank %s here", t.drag.key)
		m.renderJira()
	}
	return m, nil
}

// jiraListOrder is the list's order with a card dragged in it shown at its
// slot, as positions into t.order.
func (t *jiraTabState) jiraListOrder() []int {
	pos := make([]int, len(t.order))
	for i := range pos {
		pos[i] = i
	}
	d := t.drag
	if !d.active || !d.slotOK || d.from != -1 {
		return pos
	}
	at := slices.IndexFunc(t.order, func(ci int) bool { return t.cards[ci].Key == d.key })
	if at < 0 || d.slot == at {
		return pos
	}
	return slices.Insert(slices.Delete(pos, at, at+1), min(d.slot, len(pos)-1), at)
}

// jiraLaneOthers is lane l's cards but the dragged one.
func (m *Model) jiraLaneOthers(l int) []int {
	t := m.jiraTab
	return slices.DeleteFunc(slices.Clone(t.lanes[l].cards), func(ci int) bool { return t.cards[ci].Key == t.drag.key })
}

// jiraDropSlot is where among lane over's other cards a drop at row y
// lands: the gap between cards nearest the pointer. ok only where a drop
// can rank: a lane of one status, the swimlanes off.
func (m *Model) jiraDropSlot(over, y int) (int, bool) {
	t := m.jiraTab
	if t.swim != jiraSortRank || over < 0 || over >= len(t.lanes) || len(t.lanes[over].statusIDs) != 1 {
		return 0, false
	}
	n := len(m.jiraLaneOthers(over))
	top := 0
	if over < len(t.laneTop) {
		top = min(t.laneTop[over], max(n-1, 0))
	}
	line := y - jiraBodyTop - 1
	return min(max(top+(line+m.cardSlot()/2)/m.cardSlot(), 0), n), true
}

// dropJira ends a drag, moving the card when it landed on another lane, or on
// another status of a lane of several.
func (m Model) dropJira() (tea.Model, tea.Cmd) {
	t := m.jiraTab
	d := t.drag
	t.drag = jiraDrag{}
	if !d.active {
		return m, nil
	}
	if d.from == -1 { // list mode
		m.status = ""
		at := slices.IndexFunc(t.order, func(ci int) bool { return t.cards[ci].Key == d.key })
		if d.slotOK && at >= 0 && d.slot != at && d.slot < len(t.order) {
			return m, m.rankJiraCardTo(t.order, at, d.slot)
		}
		m.renderJira()
		return m, nil
	}
	status, to := "", -1
	if d.over >= 0 && d.over < len(t.lanes) {
		to = t.lanes[d.over].col
		if ids := t.lanes[d.over].statusIDs; len(ids) > 1 && d.zone >= 0 && d.zone < len(ids) {
			status = ids[d.zone]
		}
	}
	band := m.jiraBandMove(d)
	if d.over == d.from && status == "" {
		if d.slotOK {
			if cmd := m.rankJiraDrop(d); cmd != nil {
				return m, tea.Batch(cmd, band)
			}
		}
		m.renderJira()
		return m, band
	}
	if cmd := m.moveJiraCard(d.key, to, status); cmd != nil {
		if d.slotOK {
			cmd = tea.Batch(cmd, m.rankJiraDrop(d))
		}
		if band != nil {
			t.joinUndo() // one drop, one undo
		}
		return m, tea.Batch(cmd, band)
	}
	if band != nil {
		return m, band
	}
	m.selectJiraKey(d.key)
	m.renderJira()
	return m, nil
}

// rankJiraDrop ranks a dropped card into its slot in the lane it landed
// in; nil when that is where it was.
func (m *Model) rankJiraDrop(d jiraDrag) tea.Cmd {
	t := m.jiraTab
	if d.over < 0 || d.over >= len(t.lanes) {
		return nil
	}
	shown := t.lanes[d.over].cards
	at := slices.IndexFunc(shown, func(ci int) bool { return t.cards[ci].Key == d.key })
	if at < 0 || len(shown) < 2 {
		return nil
	}
	// Counted among the lane's other cards, slot is the place the card
	// takes in the whole lane too.
	to := min(d.slot, len(shown)-1)
	if to == at {
		return nil
	}
	return m.rankJiraCardTo(shown, at, to)
}

// jiraBandMove is the write a drop into another swimlane makes, as on
// Jira's board: the band's assignee (or epic) for the card. The card moves
// band at once; nil when it stays in its own, or the bands are priorities.
func (m *Model) jiraBandMove(d jiraDrag) tea.Cmd {
	if !d.bandOK {
		return nil
	}
	return m.jiraSetBand(d.key, m.jiraTab.swim, d.band)
}

// jiraBandUndo is a band drop to revert: the card's values before it.
// jiraSetBand gives card key band b's assignee (or epic, by swim), locally
// at once and in Jira; nil when it already has it.
func (m *Model) jiraSetBand(key string, swim jiraSort, b jira.Card) tea.Cmd {
	t := m.jiraTab
	ci := slices.IndexFunc(t.cards, func(c jira.Card) bool { return c.Key == key })
	if ci < 0 {
		return nil
	}
	c := &t.cards[ci]
	prev := *c
	client, ctx := m.jiraClient, m.ctx
	var field, fieldName string
	var run func() error
	switch {
	case swim == jiraSortAssignee && b.AssigneeID != c.AssigneeID:
		c.Assignee, c.AssigneeID = b.Assignee, b.AssigneeID
		field, fieldName, run = "assignee", i18n.T("assignee"), func() error { return client.SetAssignee(ctx, key, b.AssigneeID) }
	case swim == jiraSortEpic && b.ParentKey != c.ParentKey:
		c.ParentKey, c.ParentSummary = b.ParentKey, b.ParentSummary
		var v any // no epic: cleared
		if b.ParentKey != "" {
			v = map[string]string{"key": b.ParentKey}
		}
		field, fieldName, run = "parent", i18n.T("parent"), func() error { return client.SetField(ctx, key, "parent", v) }
	default:
		return nil
	}
	m.pushUndo(i18n.Tf("%s back to its %s", key, fieldName), func(m *Model) tea.Cmd { return m.jiraSetBand(key, swim, prev) })
	m.buildJiraLanes()
	m.selectJiraKey(key)
	m.renderJira()
	m.status = i18n.Tf("updating %s %s…", key, fieldName)
	return jiraMutateCmd(key, field, run)
}

// jiraZoneH is the height of one status drop zone in a lane body of height
// rows split n ways.
func jiraZoneH(height, n int) int {
	return max((height-1)/max(n, 1), 1)
}

func (m *Model) jiraDragging() bool {
	return m.jiraTab.drag.key != ""
}

// jiraAutoRefreshMsg is the auto-refresh tick.
type jiraAutoRefreshMsg struct{}

// jiraAutoRefreshTick arms the next auto-refresh, none when it is off.
func (m *Model) jiraAutoRefreshTick() tea.Cmd {
	if m.opts.autoRefresh <= 0 {
		return nil
	}
	return tea.Tick(m.opts.autoRefresh, func(time.Time) tea.Msg { return jiraAutoRefreshMsg{} })
}

// handleJiraAutoRefresh refetches the view unless the user is mid-action or
// the board is fresh; the next tick is always armed.
func (m Model) handleJiraAutoRefresh() (tea.Model, tea.Cmd) {
	t := m.jiraTab
	if m.modalOpen() || t.loading || t.searching || m.jiraDragging() || t.cfg == nil || time.Since(t.fetched) < m.opts.staleAfter {
		return m, m.jiraAutoRefreshTick()
	}
	return m, tea.Batch(m.loadJiraDelta(), m.jiraAutoRefreshTick())
}

// jiraFetchKey names a view and the current filters, which a delta must share
// with the last whole fetch.
func (m *Model) jiraFetchKey(idx int) string {
	t := m.jiraTab
	return strconv.Itoa(idx) + "|" + jiraFilterJQL(t.assignee, t.quick, t.quickOn)
}

// loadJiraDelta fetches the current view's cards updated since the last
// fetch, when a whole fetch of the same view is recent; else all of them.
func (m *Model) loadJiraDelta() tea.Cmd {
	t := m.jiraTab
	idx := t.viewIdx
	if len(t.cards) == 0 || t.fullKey != m.jiraFetchKey(idx) || time.Since(t.fullAt) >= m.opts.fullRefresh || idx >= len(t.views) {
		return m.loadJiraCards(idx, false)
	}
	t.seq++
	t.loading, t.loadingSince = true, time.Now()
	// A couple of minutes' overlap covers clock skew and in-flight edits.
	mins := int(time.Since(t.fetched).Minutes()) + 2
	filter := andJQL(jiraFilterJQL(t.assignee, t.quick, t.quickOn), fmt.Sprintf("updated >= -%dm", mins))
	seq, ctx, c, board, cfg, v := t.seq, m.ctx, m.jiraClient, m.jiraBoardID(), t.cfg, t.views[idx]
	var loaded []string
	if len(t.cards) <= deltaGoneMax {
		for _, cd := range t.cards {
			loaded = append(loaded, cd.Key)
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, c.Scaled(60*time.Second))
		defer cancel()
		cards, _, err := fetchJiraView(ctx, c, board, cfg, v, filter)
		msg := jiraCardsMsg{seq: seq, delta: true, viewIdx: idx, cards: cards, err: err}
		if err == nil && len(loaded) > 0 {
			// Loaded cards updated since but not in the view's answer have
			// left it (another sprint, a filtered-out status).
			// Jira refuses the search when a key was deleted, and answers a
			// card moved to another project by its new key: either needs
			// the whole view.
			jql := fmt.Sprintf("key in (%s) AND updated >= -%dm", strings.Join(loaded, ","), mins)
			changed, err := c.SearchCards(ctx, jql)
			if err != nil {
				msg.full = true
				return msg
			}
			in := map[string]bool{}
			for _, cd := range cards {
				in[cd.Key] = true
			}
			for _, cd := range changed {
				switch {
				case !slices.Contains(loaded, cd.Key):
					msg.full = true
					return msg
				case !in[cd.Key]:
					msg.gone = append(msg.gone, cd.Key)
				}
			}
		}
		return msg
	}
}

// deltaGoneMax caps the cards a partial refresh checks for having left the
// view; bigger boards wait for the whole fetch.
const deltaGoneMax = 300

// mergeCards replaces cards by key with their changed copies and adds the
// ones new to the view at the end; it returns how many were added.
func mergeCards(cards, changed []jira.Card) ([]jira.Card, int) {
	at := make(map[string]int, len(cards))
	for i, c := range cards {
		at[c.Key] = i
	}
	out := slices.Clone(cards)
	added := 0
	for _, c := range changed {
		if i, ok := at[c.Key]; ok {
			out[i] = c
			continue
		}
		out = append(out, c)
		added++
	}
	return out, added
}

// loadingTickMsg redraws a load's elapsed time.
type loadingTickMsg struct{}

func loadingTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return loadingTickMsg{} })
}

// handleLoadingTick redraws while a load is under way, then stops.
func (m Model) handleLoadingTick() (tea.Model, tea.Cmd) {
	if !m.jiraTab.loading {
		return m, nil
	}
	if m.jiraTab.cfg == nil {
		m.renderJira()
	}
	return m, loadingTick()
}

// loadingFor is " 5s" once a load has taken 2s, else "".
func loadingFor(since time.Time) string {
	if d := time.Since(since); !since.IsZero() && d >= 2*time.Second {
		return fmt.Sprintf(" %ds", int(d.Seconds()))
	}
	return ""
}
