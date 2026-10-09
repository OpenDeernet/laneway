package ui

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// jiraAssigneeDebounce delays the assignable-user search after a keystroke so
// rapid typing coalesces into one request (mirrors mentionDebounce).
const jiraAssigneeDebounce = 200 * time.Millisecond

// Editable Jira fields. While the panel has focus (see jira.go) the keys
// s / p / a open a modal list picker for Status / Priority / Assignee, and P
// opens a numeric input for Story points. The pickers and the input are fully
// modal — they own every keystroke while open (dispatched in update.go before
// the focus-based routing) and overlay the screen (view.go), mirroring the
// reaction picker. A confirmed change writes to Jira (internal/jira), then the
// panel refetches the issue so authoritative — and any cascading — values show.

// jiraPickerKind selects which field a picker edits and which fetch/mutation
// backs it.
type jiraPickerKind int

const (
	jiraPickStatus jiraPickerKind = iota
	jiraPickPriority
	jiraPickAssignee
	// jiraPickReplyTarget lists the issue's comments so the user can choose
	// which one to reply to. Unlike the others it triggers no mutation — picking
	// a row opens the reply composer (see applyJiraPick / jira_comment.go).
	jiraPickReplyTarget
	// jiraPickProject and jiraPickBoard switch the Jira tab's board
	// (jiratab.go). The project list filters locally.
	jiraPickProject
	jiraPickBoard
	// jiraPickBoardAssignee filters the Jira tab's board by assignee; it
	// filters locally too.
	jiraPickBoardAssignee
	// jiraPickStandupPeople ticks who takes part in the standup
	// (standup_screen.go), filtered locally.
	jiraPickStandupPeople
	// jiraPickFormUser and jiraPickFormOption fill a row of the transition
	// form (jira_transition.go): a person searched server-side like the
	// assignee, or one of the field's options, filtered locally.
	jiraPickFormUser
	jiraPickFormOption
	// jiraPickLaneStatus picks which of a lane's statuses a keyboard move
	// lands on (jiratab.go).
	jiraPickLaneStatus
	// jiraPickLink lists the issue's parent, links and subtasks; picking one
	// shows it in the panel.
	jiraPickLink
	// jiraPickSprint moves the selected card to a sprint or the backlog
	// (jira_sprint.go).
	jiraPickSprint
	// jiraPickPalette is the command palette (palette.go).
	jiraPickPalette
	// jiraPickMessages is the status line's kept messages (messages.go).
	jiraPickMessages
	// jiraPickBulk asks what to change on the marked cards (bulk.go).
	jiraPickBulk
	// jiraPickTimesheet lists today's worklogs (worklog.go).
	jiraPickTimesheet
	// jiraPickIssueActions and jiraPickLinkType are the panel's A menu and
	// its link types (issue_actions.go).
	jiraPickIssueActions
	jiraPickLinkType
	// jiraPickClosedSprint lists the board's closed sprints (closed_sprint.go).
	jiraPickClosedSprint
	// jiraPickSite switches the Jira site (sites.go).
	jiraPickSite
	// jiraPickHistory is the panel issue's history (history.go).
	jiraPickHistory
	// jiraPickDev lists the issue's pull requests and branches (devinfo.go).
	jiraPickDev
	// jiraPickJob lists a merge request's pipeline jobs (joblog.go).
	jiraPickJob
	// jiraPickAttachment picks an attachment to download (issue_actions.go),
	// jiraPickDeleteAttachment one to delete.
	jiraPickAttachment
	jiraPickDeleteAttachment
	// jiraPickUnlink picks an issue link to remove (issue_actions.go).
	jiraPickUnlink
	// jiraPickEditComment picks one of your comments to edit (description.go),
	// jiraPickDeleteComment one to delete.
	jiraPickEditComment
	jiraPickDeleteComment
	// jiraPickChangeType picks the issue's new type (issue_actions.go).
	jiraPickChangeType
	// jiraPickMoveProject and jiraPickMoveType pick where the issue moves
	// to (issue_actions.go).
	jiraPickMoveProject
	jiraPickMoveType
	// jiraPickDeleteIssue confirms deleting the issue (issue_actions.go).
	jiraPickDeleteIssue
	// jiraPickReporter picks the issue's reporter among the people who can
	// see it, searched server-side like the assignee.
	jiraPickReporter
	// jiraPickWatchers lists the issue's watchers and finds people to add,
	// searched server-side like the assignee (issue_actions.go).
	jiraPickWatchers
	// jiraPickReleases lists the project's versions (releases.go).
	jiraPickReleases
	// jiraPickStatusTime is the issue's time in each status (status_time.go).
	jiraPickStatusTime
	// jiraPickActionOutput is an action's output (actions.go).
	jiraPickActionOutput
	// jiraPickDeps is the issue's dependency tree (deps.go).
	jiraPickDeps
	// jiraPickQueue lists the offline writes (queue.go).
	jiraPickQueue
	// jiraPickAsk picks what to ask ui.llm about the issue (llm.go).
	jiraPickAsk
	// jiraPickHome is the start screen (home.go).
	jiraPickHome
	// jiraPickMREdit is what e changes on a merge request, jiraPickMRPeople
	// its reviewers or assignees, jiraPickMRLabels its labels and
	// jiraPickMRMerge how M merges it (mr_edit.go).
	jiraPickMREdit
	jiraPickMRPeople
	jiraPickMRLabels
	jiraPickMRMerge
	// jiraPickChartDone is the column the charts count done from,
	// jiraPickChartCompare where a second line goes (chart_lines.go).
	jiraPickChartDone
	jiraPickChartCompare
)

// jiraPickerItem is one selectable row. id is the value handed to the mutation
// (transition id / priority id / accountId; "" = unassign). current marks the
// issue's present value with a ✓.
type jiraPickerItem struct {
	id      string
	label   string
	current bool
	value   string // what an edit of the row starts from (a worklog's "1h fix")
	focus   bool   // the cursor starts here rather than on the current row
	search  string // more words the filter matches, never shown
	hint    string // dim at a dropdown row's right: its key, ▸ for a submenu
}

// jiraPickerState is the modal list picker reused for the three list-style
// fields. The assignee picker is filterable: a textinput drives a debounced
// server-side search of assignable users (a large project has more than the
// default page); status and priority are short fixed lists with 1-9
// accelerators. fetchSeq is bumped on every (re)query so a late response from
// an earlier query is discarded — same idea as mentionState.fetchSeq.
type jiraPickerState struct {
	active      bool
	kind        jiraPickerKind
	issueKey    string
	title       string
	gen         int // drops a stale async load (picker reopened/closed since)
	loading     bool
	err         error
	items       []jiraPickerItem
	idx         int  // cursor into the item list
	filterable  bool // assignee: has a search box backed by the server
	filter      textinput.Model
	fetchSeq    int              // discards a search response from a superseded query
	curAssignee string           // accountId of the issue's assignee, to mark ✓ across re-queries
	all         []jiraPickerItem // a locally filtered picker's full list
	bulk        []string         // the marked keys a pick applies to, none for one issue
	// day is the timesheet's day; pendingDelete the entry a first d picked
	// (worklog.go).
	text          string // the list as text, for its copy (the timesheet)
	day           time.Time
	pendingDelete string
	// found are the palette's search hits, shown after its own rows: remote
	// (Jira's) then indexed (the index's) not already listed.
	found, remote, indexed []jiraPickerItem
	// inline is the panel row the list drops under (a panel field name or
	// an extra field's id), "" for the modal.
	inline string
	// checked are the rows space ticked in a picker that takes several
	// (the board's assignee filter), id to label; nil for one pick. ticked
	// is whether space was used: enter then applies the ticks, else the row.
	checked map[string]string
	ticked  bool
	// at is where a dropdown's corner goes (dropdown.go), nil for the modal;
	// top its first row shown.
	at  *point
	top int
}

// jiraPickerLoadedMsg carries the fetched option list for an open picker. gen +
// kind + seq drop a result the user has since closed, replaced, or re-queried.
type jiraPickerLoadedMsg struct {
	gen   int
	seq   int
	kind  jiraPickerKind
	items []jiraPickerItem
	err   error
	// projects is the project picker's fetch, kept for the next opening.
	projects []jira.Project
	// sprints is the closed sprint picker's fetch (closed_sprint.go).
	sprints []jira.Sprint
	title   string // replaces the picker's title when set
	text    string // the list as text, for its copy (the timesheet)
	// single makes a multi picker take one row (a GitLab that takes one
	// reviewer); its current one is marked.
	single bool
}

// jiraAssigneeDebounceMsg fires after the debounce window to run the pending
// assignee search for seq (ignored if a newer keystroke has superseded it).
type jiraAssigneeDebounceMsg struct{ seq int }

// jiraMutatedMsg carries the result of a field write. On success the panel
// reloads the issue (the client already invalidated its cache).
type jiraMutatedMsg struct {
	key   string
	field string
	err   error
	text  string // a comment's text, kept when its post fails
	// maybe is the comment as posted when the post may have landed (it
	// timed out); found is a comment that had.
	maybe json.RawMessage
	found bool
}

// unsentComment is a comment whose post failed, on key; maybe is it as
// posted when the post may have landed all the same.
type unsentComment struct {
	key, text string
	maybe     json.RawMessage
}

// startJiraPicker resets the picker to a fresh loading state for the current
// issue and returns the new generation so the caller's fetch can be matched
// against it.
func (m *Model) startJiraPicker(kind jiraPickerKind, title string, filterable bool) int {
	gen := m.jiraPicker.gen + 1
	issueKey := ""
	if m.jiraIssue != nil {
		issueKey = m.jiraIssue.Key
	}
	m.jiraPicker = jiraPickerState{
		active:     true,
		kind:       kind,
		issueKey:   issueKey,
		title:      title,
		gen:        gen,
		loading:    true,
		filterable: filterable,
		fetchSeq:   1,
	}
	if filterable {
		ti := textinput.New()
		ti.Prompt = "❯ "
		ti.Placeholder = i18n.T("filter…")
		ti.SetWidth(40)
		ti.Focus()
		m.jiraPicker.filter = ti
	}
	return gen
}

// openJiraStatusPicker loads the issue's workflow transitions (the only status
// changes Jira accepts) into the picker.
func (m *Model) openJiraStatusPicker() tea.Cmd {
	gen := m.startJiraPicker(jiraPickStatus, i18n.Tf("Set status — %s", m.jiraIssue.Key), false)
	m.jiraPicker.inline = "Status"
	seq := m.jiraPicker.fetchSeq
	key, cur := m.jiraIssue.Key, m.jiraIssue.Status
	client, ctx := m.jiraClient, m.ctx
	return func() tea.Msg {
		opts, err := client.Transitions(ctx, key)
		if err != nil {
			return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: jiraPickStatus, err: err}
		}
		items := make([]jiraPickerItem, 0, len(opts))
		for _, o := range opts {
			items = append(items, jiraPickerItem{id: o.ID, label: o.Name, current: o.Name == cur})
		}
		return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: jiraPickStatus, items: items}
	}
}

// openJiraPriorityPicker loads the instance priority list into the picker.
func (m *Model) openJiraPriorityPicker() tea.Cmd {
	gen := m.startJiraPicker(jiraPickPriority, i18n.Tf("Set priority — %s", m.jiraIssue.Key), false)
	m.jiraPicker.inline = "Priority"
	seq := m.jiraPicker.fetchSeq
	curID := m.jiraIssue.PriorityID
	client, ctx := m.jiraClient, m.ctx
	return func() tea.Msg {
		opts, err := client.Priorities(ctx)
		if err != nil {
			return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: jiraPickPriority, err: err}
		}
		items := make([]jiraPickerItem, 0, len(opts))
		for _, o := range opts {
			items = append(items, jiraPickerItem{id: o.ID, label: o.Name, current: o.ID == curID})
		}
		return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: jiraPickPriority, items: items}
	}
}

// openJiraAssigneePicker opens the filterable assignee picker and runs the
// initial (empty-query) search. Typing re-runs it server-side (see
// handleJiraPickerKey / jiraAssigneeDebounceMsg).
func (m *Model) openJiraAssigneePicker() tea.Cmd {
	gen := m.startJiraPicker(jiraPickAssignee, i18n.Tf("Set assignee — %s", m.jiraIssue.Key), true)
	m.jiraPicker.curAssignee = m.jiraIssue.AssigneeAccountID
	m.jiraPicker.inline = "Assignee"
	return m.fetchAssignees(gen, m.jiraPicker.fetchSeq, m.jiraIssue.Key, "")
}

// openJiraReporterPicker opens the reporter picker under its row.
func (m *Model) openJiraReporterPicker() tea.Cmd {
	gen := m.startJiraPicker(jiraPickReporter, i18n.Tf("Set reporter — %s", m.jiraIssue.Key), true)
	m.jiraPicker.issueKey = m.jiraIssue.Key
	m.jiraPicker.curAssignee = m.jiraIssue.ReporterAccountID
	m.jiraPicker.inline = "Reporter"
	return m.fetchReporters(gen, m.jiraPicker.fetchSeq, m.jiraIssue.Key, "")
}

// fetchReporters lists the people who can see key matching query, the
// reporter marked.
func (m *Model) fetchReporters(gen, seq int, key, query string) tea.Cmd {
	c, ctx, cur := m.jiraClient, m.ctx, m.jiraPicker.curAssignee
	return func() tea.Msg {
		users, err := c.ViewUsers(ctx, key, query)
		items := make([]jiraPickerItem, 0, len(users))
		for _, u := range users {
			items = append(items, jiraPickerItem{id: u.AccountID, label: u.DisplayName, current: u.AccountID == cur})
		}
		return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: jiraPickReporter, items: items, err: err}
	}
}

// fetchAssignees searches assignable users for query and builds the picker
// rows. With an empty query (the default view) it prepends Unassigned and
// "Assign to me"; while searching it shows server matches only.
func (m Model) fetchAssignees(gen, seq int, key, query string) tea.Cmd {
	client, ctx := m.jiraClient, m.ctx
	curID, kind := m.jiraPicker.curAssignee, m.jiraPicker.kind
	return func() tea.Msg {
		users, err := client.AssignableUsers(ctx, key, query)
		if err != nil {
			return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: kind, err: err}
		}
		var items []jiraPickerItem
		meID := ""
		if strings.TrimSpace(query) == "" {
			items = append(items, jiraPickerItem{id: "", label: i18n.T("Unassigned"), current: curID == ""})
			// "Assign to me" is a convenience pinned near the top; a Myself
			// failure just omits it (and the dedup below is skipped).
			if me, meErr := client.Myself(ctx); meErr == nil && me.AccountID != "" {
				meID = me.AccountID
				items = append(items, jiraPickerItem{
					id:      me.AccountID,
					label:   i18n.Tf("Assign to me (%s)", me.DisplayName),
					current: me.AccountID == curID,
					focus:   me.AccountID != curID, // a enter takes it
				})
			}
		}
		for _, u := range users {
			if meID != "" && u.AccountID == meID {
				continue // already shown as "Assign to me"
			}
			items = append(items, jiraPickerItem{id: u.AccountID, label: u.DisplayName, current: u.AccountID == curID})
		}
		return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: kind, items: items}
	}
}

// openJiraReplyPicker lists the issue's comments (newest first) so the user can
// pick which one to reply to. The comments are already loaded with the issue,
// so the picker opens populated — there's no fetch. Each row's id is the index
// into m.jiraIssue.Comments, so applyJiraPick can recover the chosen comment.
func (m *Model) openJiraReplyPicker() {
	m.startJiraPicker(jiraPickReplyTarget, i18n.Tf("Reply to comment — %s", m.jiraIssue.Key), false)
	items := make([]jiraPickerItem, 0, len(m.jiraIssue.Comments))
	for i := len(m.jiraIssue.Comments) - 1; i >= 0; i-- {
		items = append(items, jiraPickerItem{id: strconv.Itoa(i), label: commentPickerLabel(m.jiraIssue.Comments[i])})
	}
	m.jiraPicker.loading = false
	m.jiraPicker.items = items
	m.jiraPicker.idx = 0
}

// commentPickerLabel is a one-line "Author: snippet" used in the reply picker.
func commentPickerLabel(c jira.Comment) string {
	author := c.Author
	if author == "" {
		author = i18n.T("Unknown")
	}
	snippet := strings.Join(strings.Fields(c.Body), " ")
	const max = 60
	if r := []rune(snippet); len(r) > max {
		snippet = string(r[:max]) + "…"
	}
	if snippet == "" {
		return author
	}
	return author + ": " + snippet
}

// openJiraPointsInput shows the numeric story-points input seeded with the
// current value (empty when unset).
func (m *Model) openJiraPointsInput() {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = i18n.T("number (empty clears)")
	ti.CharLimit = 12
	ti.SetWidth(24)
	ti.SetValue(m.jiraIssue.StoryPoints)
	ti.CursorEnd()
	ti.Focus()
	m.jiraFieldInput = ti
	m.jiraFieldActive = true
	m.jiraFieldName = "points"
	m.jiraFieldKey = m.jiraIssue.Key
	m.startFieldInline(m.panelFieldRow("Points"))
}

// openJiraSummaryInput shows the summary input seeded with the current one.
func (m *Model) openJiraSummaryInput() {
	m.openJiraTextInput("summary", m.jiraIssue.Summary, "", 255) // Jira's summary limit
	m.startFieldInline(m.panelFieldRow("Summary"))
}

// openJiraLabelsInput shows the labels, space separated: a label has no
// spaces.
func (m *Model) openJiraLabelsInput() {
	m.openJiraTextInput("labels", strings.Join(m.jiraIssue.Labels, " "), i18n.T("space separated (empty clears)"), 0)
	m.startFieldInline(m.panelFieldRow("Labels"))
}

// openJiraTextInput opens the wide field input for field, seeded with value.
func (m *Model) openJiraTextInput(field, value, placeholder string, limit int) {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = placeholder
	ti.CharLimit = limit
	ti.SetWidth(max(min(m.width-16, 72), 16))
	ti.SetValue(value)
	ti.CursorEnd()
	ti.Focus()
	m.jiraFieldInput = ti
	m.jiraFieldActive = true
	m.jiraFieldName = field
	m.jiraFieldKey = m.jiraIssue.Key
}

// handleJiraPickerLoaded installs a finished option fetch and parks the cursor
// on the current value (when present). Stale results are dropped.
func (m Model) handleJiraPickerLoaded(msg jiraPickerLoadedMsg) (tea.Model, tea.Cmd) {
	if !m.jiraPicker.active || msg.gen != m.jiraPicker.gen || msg.kind != m.jiraPicker.kind || msg.seq != m.jiraPicker.fetchSeq {
		return m, nil
	}
	if msg.projects != nil {
		m.jiraTab.projects = msg.projects
	}
	if msg.kind == jiraPickClosedSprint {
		m.jiraTab.closedSprints = msg.sprints
	}
	m.jiraPicker.err = msg.err
	if msg.title != "" {
		m.jiraPicker.title = msg.title
	}
	m.jiraPicker.text = msg.text
	if msg.kind == jiraPickBoardAssignee && !m.jiraPicker.loading {
		// more people for a picker already showing: keep the filter typed
		// and the row chosen
		chosen := ""
		if i := m.jiraPicker.idx; i < len(m.jiraPicker.items) {
			chosen = m.jiraPicker.items[i].id
		}
		m.jiraPicker.all = msg.items
		m.markChecked()
		m.filterJiraPicker()
		m.jiraPicker.idx = max(0, slices.IndexFunc(m.jiraPicker.items, func(it jiraPickerItem) bool { return it.id == chosen }))
		return m, nil
	}
	if msg.single {
		m.jiraPicker.checked = nil
	}
	m.setJiraPickerItems(msg.items)
	m.markChecked()
	return m, nil
}

// emptyText is what an empty picker says: a filter matched nothing, or the
// list itself is empty.
func (p *jiraPickerState) emptyText() string {
	switch {
	case p.filterable && strings.TrimSpace(p.filter.Value()) != "":
		return i18n.T("no matches")
	case p.kind == jiraPickStatus:
		return i18n.T("no transitions from here (the workflow, or your permissions)")
	}
	return i18n.T("nothing here")
}

// setJiraPickerItems fills an open picker and parks the cursor on the current
// value (when present).
func (m *Model) setJiraPickerItems(items []jiraPickerItem) {
	m.jiraPicker.loading = false
	m.jiraPicker.items = items
	m.jiraPicker.all = items
	m.jiraPicker.idx = 0
	if i := slices.IndexFunc(items, func(it jiraPickerItem) bool { return it.focus }); i >= 0 {
		m.jiraPicker.idx = i
		return
	}
	for i, it := range items {
		if it.current {
			m.jiraPicker.idx = i
			break
		}
	}
}

// markChecked shows a multi picker's ticks as its rows' ✓; the "" row
// (everyone) has it while none are ticked.
func (m *Model) markChecked() {
	p := &m.jiraPicker
	if p.checked == nil {
		return
	}
	for _, l := range [][]jiraPickerItem{p.all, p.items} {
		for i := range l {
			_, on := p.checked[l[i].id]
			l[i].current = on || (l[i].id == "" && len(p.checked) == 0)
		}
	}
}

// toggleChecked ticks or unticks the row under the cursor; the "" row
// clears every tick.
func (m *Model) toggleChecked() {
	p := &m.jiraPicker
	if p.idx >= len(p.items) {
		return
	}
	it := p.items[p.idx]
	switch _, on := p.checked[it.id]; {
	case it.id == "":
		clear(p.checked)
	case on:
		delete(p.checked, it.id)
	default:
		p.checked[it.id] = it.label
	}
	p.ticked = true
	m.markChecked()
}

// checkedPick is a multi picker's ticks as one comma-joined id and label,
// in the list's order (ticks not listed, as before the people load, last).
func (p *jiraPickerState) checkedPick() (id, label string) {
	var ids, labels []string
	for _, it := range p.all {
		if _, ok := p.checked[it.id]; ok && it.id != "" {
			ids, labels = append(ids, it.id), append(labels, it.label)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(p.checked)) {
		if !slices.Contains(ids, k) {
			ids, labels = append(ids, k), append(labels, p.checked[k])
		}
	}
	return strings.Join(ids, ","), strings.Join(labels, ", ")
}

// filterJiraPicker narrows a locally filtered picker to rows containing
// every word of the filter.
func (m *Model) filterJiraPicker() {
	terms := strings.Fields(strings.ToLower(m.jiraPicker.filter.Value()))
	m.jiraPicker.items = nil
	for _, it := range m.jiraPicker.all {
		label := strings.ToLower(it.label + " " + it.search)
		if !slices.ContainsFunc(terms, func(t string) bool { return !strings.Contains(label, t) }) {
			m.jiraPicker.items = append(m.jiraPicker.items, it)
		}
	}
	m.jiraPicker.items = append(m.jiraPicker.items, m.jiraPicker.found...)
	m.jiraPicker.idx = 0
}

// closeJiraPicker tears the picker down, preserving gen so a late load is
// ignored.
func (m *Model) closeJiraPicker() {
	m.jiraPicker = jiraPickerState{gen: m.jiraPicker.gen}
	m.dropdown = nil
}

// closeJiraField tears the field input down.
func (m *Model) closeJiraField() {
	m.labels = labelSuggest{seq: m.labels.seq + 1}
	m.jiraFieldActive = false
	m.jiraFieldName = ""
	m.jiraFieldKey = ""
	m.jiraFieldAsked = ""
	m.jiraFieldInput = textinput.Model{}
	m.renderRef()
}

// fieldInline is whether the field input edits the shown issue's panel row
// in place rather than as a modal.
func (m *Model) fieldInline() bool {
	if !m.jiraFieldActive || !m.refOpen || m.jiraIssue == nil || m.jiraFieldKey != m.jiraIssue.Key {
		return false
	}
	switch m.jiraFieldName {
	case "summary", "labels", "points":
		return true
	case "field":
		return m.panelEditField().ID != ""
	}
	return false
}

// fieldInlineOn is whether the inline input sits on the panel's own field
// name ("Summary", "Points", "Labels") or extra field id.
func (m *Model) fieldInlineOn(name string) bool {
	if !m.fieldInline() {
		return false
	}
	if m.jiraFieldName == "field" {
		return m.panelEditID == name
	}
	return strings.EqualFold(m.jiraFieldName, name)
}

// startFieldInline draws the just opened input in its row and scrolls the
// row into view.
func (m *Model) startFieldInline(row int) {
	if !m.fieldInline() {
		return
	}
	m.renderRef()
	if row < 0 || row >= len(m.panelFieldLine) {
		return
	}
	if y := m.panelFieldLine[row]; y < m.refView.YOffset() || y >= m.refView.YOffset()+m.refView.Height() {
		m.refView.SetYOffset(max(y-1, 0))
	}
}

// handleJiraPickerKey owns every keystroke while the list picker is open.
// Arrow/ctrl-nav move the cursor; for the fixed lists 1-9 jump+apply; enter
// applies; esc cancels. For the assignee picker, other keys feed the filter.
func (m Model) handleJiraPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		if !m.backToMenu() {
			m.closeJiraPicker()
		}
		return m, nil
	case "enter":
		return m.applyJiraPick()
	case "space", "tab":
		// space ticks while nothing is typed (names have spaces), tab
		// always, clearing what was typed for the next name.
		if p := &m.jiraPicker; p.checked != nil && (msg.String() == "tab" || p.filter.Value() == "") {
			m.toggleChecked()
			if p.filter.Value() != "" {
				p.filter.SetValue("")
				m.filterJiraPicker()
				p.idx = max(0, slices.IndexFunc(p.items, func(it jiraPickerItem) bool { return it.current && it.id != "" }))
			}
			return m, nil
		}
	case "left":
		if !m.jiraPicker.filterable && m.backToMenu() {
			return m, nil
		}
	case "right":
		if p := &m.jiraPicker; m.dropdown != nil && p.kind == jiraPickBulk && p.idx < len(p.items) && p.items[p.idx].hint == submenuHint {
			return m.applyJiraPick()
		}
	}

	if m.jiraPicker.kind == jiraPickTimesheet && key.Matches(msg, m.keys.Timesheet) {
		day := m.jiraPicker.day
		m.closeJiraPicker()
		return m, m.openWeek(day)
	}
	if p := &m.jiraPicker; copyRowKinds[p.kind] && key.Matches(msg, m.keys.CopyBranch) && p.idx < len(p.items) {
		it := p.items[p.idx]
		text := cmp.Or(it.value, it.id) // dev info: the branch, else the link
		if p.kind != jiraPickDev {
			text = strings.TrimSpace(it.label) // the row as read
		}
		if text == "" {
			m.status = i18n.T("nothing to copy on this row")
			return m, nil
		}
		m.status = i18n.Tf("copied %s", text)
		return m, tea.SetClipboard(text)
	}
	if m.jiraPicker.filterable {
		// Arrows, ctrl+p/ctrl+n and pgup/pgdown navigate so letters stay
		// available for typing.
		if i, ok := m.keys.listNav(msg, m.jiraPicker.idx, len(m.jiraPicker.items), m.pickerWin(m.bodyH()), true); ok {
			m.jiraPicker.idx = i
			return m, nil
		}
		before := m.jiraPicker.filter.Value()
		var cmd tea.Cmd
		m.jiraPicker.filter, cmd = m.jiraPicker.filter.Update(msg)
		if m.jiraPicker.filter.Value() == before {
			return m, cmd
		}
		if k := m.jiraPicker.kind; k == jiraPickProject || k == jiraPickBoardAssignee || k == jiraPickStandupPeople || k == jiraPickFormOption || k == jiraPickPalette || k == jiraPickMessages || k == jiraPickHistory || k == jiraPickDev || k == jiraPickJob || k == jiraPickAttachment || k == jiraPickDeleteAttachment || k == jiraPickUnlink || k == jiraPickMoveProject ||
			k == jiraPickLink || k == jiraPickLinkType || k == jiraPickClosedSprint || k == jiraPickSprint || k == jiraPickBoard || k == jiraPickReleases || k == jiraPickDeps ||
			k == jiraPickMRPeople || k == jiraPickMRLabels {
			m.jiraPicker.found = nil
			m.filterJiraPicker()
			if k == jiraPickPalette {
				return m, tea.Batch(cmd, m.schedulePaletteSearch())
			}
			return m, cmd
		}
		// Query changed: schedule a debounced server search. fetchSeq drops any
		// earlier in-flight search. The existing rows stay on screen meanwhile.
		m.jiraPicker.fetchSeq++
		seq := m.jiraPicker.fetchSeq
		debounce := tea.Tick(jiraAssigneeDebounce, func(time.Time) tea.Msg {
			return jiraAssigneeDebounceMsg{seq: seq}
		})
		return m, tea.Batch(cmd, debounce)
	}

	if i, ok := m.keys.listNav(msg, m.jiraPicker.idx, len(m.jiraPicker.items), m.pickerWin(m.bodyH()), false); ok {
		m.jiraPicker.idx = i
		return m, nil
	}
	if m.jiraPicker.kind == jiraPickTimesheet {
		if cmd, ok := m.timesheetKey(msg.String()); ok {
			return m, cmd
		}
	}
	// Digit accelerators 1..9 over the (short) fixed list.
	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		idx := int(s[0] - '1')
		if idx < len(m.jiraPicker.items) {
			m.jiraPicker.idx = idx
			return m.applyJiraPick()
		}
	}
	return m, nil
}

// copyRowKinds are the pickers whose rows ctrl+y copies.
var copyRowKinds = map[jiraPickerKind]bool{jiraPickDev: true, jiraPickHistory: true}

// handleJiraAssigneeDebounce runs the pending assignee search once the debounce
// window elapses, unless a newer keystroke has superseded it.
func (m Model) handleJiraAssigneeDebounce(msg jiraAssigneeDebounceMsg) (tea.Model, tea.Cmd) {
	k := m.jiraPicker.kind
	if !m.jiraPicker.active || (k != jiraPickAssignee && k != jiraPickFormUser && k != jiraPickWatchers && k != jiraPickReporter) || msg.seq != m.jiraPicker.fetchSeq {
		return m, nil
	}
	if k == jiraPickReporter {
		return m, m.fetchReporters(m.jiraPicker.gen, msg.seq, m.jiraPicker.issueKey, m.jiraPicker.filter.Value())
	}
	if k == jiraPickWatchers {
		return m, m.fetchWatchers(m.jiraPicker.gen, msg.seq, m.jiraPicker.issueKey, m.jiraPicker.filter.Value())
	}
	return m, m.fetchAssignees(m.jiraPicker.gen, msg.seq, m.jiraPicker.issueKey, m.jiraPicker.filter.Value())
}

// jiraPickerMove nudges the cursor within the item list, clamped to bounds. The
// render windows around it (see renderJiraPicker), so no scroll state is tracked
// here.
func (m *Model) jiraPickerMove(delta int) {
	n := len(m.jiraPicker.items)
	if n == 0 {
		m.jiraPicker.idx = 0
		return
	}
	idx := m.jiraPicker.idx + delta
	if idx < 0 {
		idx = 0
	}
	if idx > n-1 {
		idx = n - 1
	}
	m.jiraPicker.idx = idx
}

// handleJiraFieldKey owns every keystroke while the field input is open.
func (m Model) handleJiraFieldKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.jiraFieldName == "worklog" && m.worklogFromTimer {
		if out, cmd, ok := m.timerStopKey(msg); ok {
			return out, cmd
		}
	}
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.closeJiraField()
		return m, nil
	case "enter":
		return m.applyJiraField()
	case "tab":
		if m.jiraFieldName == "upload" {
			done, names := completePath(m.jiraFieldInput.Value())
			m.jiraFieldInput.SetValue(done)
			m.jiraFieldInput.CursorEnd()
			if len(names) > 1 {
				m.status = strings.Join(names[:min(len(names), 8)], "  ")
			}
			return m, nil
		}
	}
	if m.labelKey(msg) || m.linkKey(msg) {
		return m, nil
	}
	before := m.jiraFieldInput.Value()
	var cmd tea.Cmd
	m.jiraFieldInput, cmd = m.jiraFieldInput.Update(msg)
	if m.jiraFieldInput.Value() != before {
		cmd = tea.Batch(cmd, m.suggestLabels(), m.searchLinkTarget())
	}
	if m.fieldInline() {
		m.renderRef()
	}
	return m, cmd
}

// applyJiraPick closes the picker and fires the mutation for the highlighted
// row.
func (m Model) applyJiraPick() (tea.Model, tea.Cmd) {
	items := m.jiraPicker.items
	if m.jiraPicker.idx < 0 || m.jiraPicker.idx >= len(items) {
		return m, nil
	}
	it := items[m.jiraPicker.idx]
	kind := m.jiraPicker.kind

	// The reply-target picker mutates nothing: the chosen row opens the reply
	// composer prefilled from the loaded comment (id is its index).
	if kind == jiraPickReplyTarget {
		m.closeJiraPicker()
		idx, err := strconv.Atoi(it.id)
		if err != nil || m.jiraIssue == nil || idx < 0 || idx >= len(m.jiraIssue.Comments) {
			return m, nil
		}
		m.openJiraReply(m.jiraIssue.Comments[idx])
		return m, nil
	}

	if kind == jiraPickProject || kind == jiraPickBoard {
		m.closeJiraPicker()
		return m, m.pickJiraBoard(kind, it.id)
	}
	if bulk := m.jiraPicker.bulk; len(bulk) > 0 {
		if len(bulk) > 1 && m.jiraPicker.pendingDelete != it.id { // confirmed by a second enter on it
			m.jiraPicker.pendingDelete = it.id
			m.status = bulkAsk(kind, it.label, len(bulk))
			return m, nil
		}
		m.closeJiraPicker()
		return m, m.applyBulkPick(kind, bulk, it)
	}
	if kind == jiraPickSite {
		m.closeJiraPicker()
		return m.pickSite(it.id)
	}
	if kind == jiraPickIssueActions {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		return m, m.applyIssueAction(key, it.id)
	}
	if kind == jiraPickAsk {
		m.closeJiraPicker()
		return m, m.askLLM(it.id)
	}
	if kind == jiraPickClosedSprint {
		m.closeJiraPicker()
		return m, m.openClosedSprint(it.id)
	}
	if kind == jiraPickLinkType {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		m.openLinkTarget(key, it)
		return m, nil
	}
	if kind == jiraPickEditComment {
		m.closeJiraPicker()
		i, err := strconv.Atoi(it.id)
		if err != nil {
			return m, nil
		}
		return m, m.editComment(i)
	}
	if kind == jiraPickDeleteComment {
		i, err := strconv.Atoi(it.id)
		if err != nil || m.jiraIssue == nil || i >= len(m.jiraIssue.Comments) {
			return m, nil
		}
		if m.jiraPicker.pendingDelete != it.id { // confirmed by a second enter on it
			m.jiraPicker.pendingDelete = it.id
			m.status = i18n.Tf("enter again deletes the comment %s", strings.TrimSpace(it.label))
			return m, nil
		}
		m.closeJiraPicker()
		return m, m.deleteComment(i)
	}
	if kind == jiraPickReporter {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		if it.current {
			return m, nil
		}
		c, ctx := m.jiraClient, m.ctx
		m.status = i18n.Tf("setting %s's reporter to %s…", key, it.label)
		return m, jiraMutateCmd(key, "reporter", func() error { return c.SetReporter(ctx, key, it.id) })
	}
	if kind == jiraPickWatchers {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		c, ctx, watch := m.jiraClient, m.ctx, !it.current
		field := "watcher added"
		if !watch {
			field = "watcher removed"
		}
		return m, jiraMutateCmd(key, field, func() error { return c.SetWatcher(ctx, key, it.id, watch) })
	}
	if kind == jiraPickDeleteIssue {
		if m.jiraPicker.pendingDelete != it.id { // confirmed by a second enter on it
			m.jiraPicker.pendingDelete = it.id
			m.status = i18n.Tf("enter again deletes %s, for good", it.id)
			return m, nil
		}
		m.closeJiraPicker()
		return m, m.deleteIssue(it.id, it.value == "subtasks")
	}
	if kind == jiraPickMoveProject {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		return m, m.openMoveTypes(key, it.id)
	}
	if kind == jiraPickMoveType {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		return m, m.moveIssue(key, it)
	}
	if kind == jiraPickChangeType {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		c, ctx := m.jiraClient, m.ctx
		m.status = i18n.Tf("changing %s to %s…", key, it.value)
		return m, jiraMutateCmd(key, "type", func() error { return c.SetIssueType(ctx, key, it.id) })
	}
	if kind == jiraPickUnlink {
		if m.jiraPicker.pendingDelete != it.id { // a removal is confirmed by a second enter on it
			m.jiraPicker.pendingDelete = it.id
			m.status = i18n.Tf("enter again removes the link %s", it.label)
			return m, nil
		}
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		return m, m.unlinkJira(key, it)
	}
	if kind == jiraPickDeleteAttachment {
		if m.jiraPicker.pendingDelete != it.id { // confirmed by a second enter on it
			m.jiraPicker.pendingDelete = it.id
			m.status = i18n.Tf("enter again deletes %s", it.value)
			return m, nil
		}
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		c, ctx := m.jiraClient, m.ctx
		m.status = i18n.T("deleting the attachment…")
		return m, jiraMutateCmd(key, "attachments", func() error { return c.DeleteAttachment(ctx, key, it.id) })
	}
	if kind == jiraPickAttachment {
		m.closeJiraPicker()
		return m, m.downloadAttachment(it.id)
	}
	if kind == jiraPickMREdit || kind == jiraPickMRPeople || kind == jiraPickMRLabels || kind == jiraPickMRMerge {
		return m.applyMRPick(kind, it)
	}
	if kind == jiraPickJob {
		id, _ := strconv.Atoi(it.id)
		m.closeJiraPicker()
		if id == 0 || m.mr == nil {
			return m, nil
		}
		return m, m.openJobLog(m.mr.c, m.mr.ref.Repo, id, it.value)
	}
	if kind == jiraPickDev {
		if it.id == "" {
			m.status = i18n.T("no link for this row")
			return m, nil
		}
		m.closeJiraPicker()
		if cmd, ok := m.openMRLink(it.id, it.label); ok {
			m.focus = focusRef
			return m, cmd
		}
		m.status = i18n.Tf("opening %s…", it.id)
		return m, m.openOpenable(openable{name: it.label, url: it.id})
	}
	if kind == jiraPickHistory {
		m.closeJiraPicker() // on to the panel's history, changes in full
		m.focus = focusRef
		return m, m.switchActivity(activityHistory)
	}
	if rest, ok := strings.CutPrefix(it.id, proposalID); ok && kind == jiraPickTimesheet {
		key, at, _ := strings.Cut(rest, "/")
		sec, _ := strconv.ParseInt(at, 10, 64)
		m.closeJiraPicker()
		m.openWorklogInput(key, it.value+" ", time.Unix(sec, 0))
		return m, nil
	}
	if kind == jiraPickTimesheet {
		key, _, _ := strings.Cut(it.id, "/") // a worklog row is key/id
		if key == "" {
			m.status = i18n.T("no issue on this row") // a day's heading: the list stays
			return m, nil
		}
		m.closeJiraPicker()
		return m.openJiraKey(key)
	}
	if kind == jiraPickReleases {
		return m.applyRelease(it)
	}
	if kind == jiraPickHome {
		return m.applyHomePick(it)
	}
	if kind == jiraPickChartDone || kind == jiraPickChartCompare {
		m.applyChartLine(kind == jiraPickChartCompare, it.id)
		return m, m.loadCharts()
	}
	if kind == jiraPickQueue {
		return m.applyQueuePick(it)
	}
	if kind == jiraPickDeps {
		if it.id == "" {
			return m, nil
		}
		m.closeJiraPicker()
		return m.openJiraKey(it.id)
	}
	if kind == jiraPickStatusTime || kind == jiraPickActionOutput {
		m.closeJiraPicker()
		return m, nil
	}
	if kind == jiraPickBulk {
		d, row := m.dropdown, m.jiraPicker.idx
		var box string
		var top, left int
		if d != nil {
			box = m.renderDropdown(m.bodyH())
			top, left = m.dropdownPlace(box)
		}
		m.closeJiraPicker()
		if k, ok := strings.CutPrefix(it.id, "key:"); ok { // a card menu's key: pressed on the card
			m.quickKey = ""
			return m.handleJiraKey(keyPress(k))
		}
		cmd := m.applyBulkMenu(it.id)
		if d != nil {
			m.openSubmenu(d, box, top, left, row)
		}
		return m, cmd
	}
	if kind == jiraPickPalette {
		m.closeJiraPicker()
		return m.applyPalette(it.id)
	}
	if kind == jiraPickMessages {
		m.closeJiraPicker()
		return m.applyMessage(it)
	}
	if kind == jiraPickStandupPeople {
		id := it.id
		if m.jiraPicker.ticked {
			id, _ = m.jiraPicker.checkedPick()
		}
		m.closeJiraPicker()
		m.setStandupPeople(strings.Split(id, ","))
		return m, nil
	}
	if kind == jiraPickBoardAssignee {
		id, label := it.id, it.label
		if m.jiraPicker.ticked {
			id, label = m.jiraPicker.checkedPick()
		}
		m.closeJiraPicker()
		return m, m.setJiraAssignee(id, label)
	}
	if kind == jiraPickFormUser || kind == jiraPickFormOption {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		if m.jiraForm == nil {
			return m, m.pickPanelExtra(key, kind, it)
		}
		m.pickJiraFormValue(kind, it)
		if f := m.jiraForm; f.idx < len(f.fields) && f.fields[f.idx].ID == createTypeField {
			return m, m.syncCreateType()
		}
		return m, nil
	}
	if kind == jiraPickLaneStatus {
		m.closeJiraPicker()
		return m, m.pickJiraLaneStatus(it.id)
	}
	if kind == jiraPickLink && it.value == "web" {
		m.closeJiraPicker()
		if id := m.jiraClient.PageID(it.id); id != "" {
			return m, m.openPage(id, strings.TrimPrefix(it.label, "page "))
		}
		m.status = i18n.Tf("opening %s…", it.id)
		return m, m.openOpenable(openable{name: it.label, url: it.id})
	}
	if kind == jiraPickLink {
		m.closeJiraPicker()
		m.selectJiraKey(it.id)
		m.renderJira()
		return m.openJiraKey(it.id)
	}
	if kind == jiraPickSprint {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		return m, m.moveJiraToSprint([]string{key}, it)
	}
	if kind == jiraPickStatus {
		key := m.jiraPicker.issueKey
		m.closeJiraPicker()
		return m, m.startJiraMoveFromPanel(key, it.id, it.label)
	}

	key := m.jiraPicker.issueKey
	m.closeJiraPicker()

	client, ctx := m.jiraClient, m.ctx
	var field string
	var run func() error
	switch kind {
	case jiraPickStatus:
		field = "status"
		run = func() error { return client.DoTransition(ctx, key, it.id) }
	case jiraPickPriority:
		field = "priority"
		run = func() error { return client.SetPriority(ctx, key, it.id) }
	case jiraPickAssignee:
		field = "assignee"
		run = func() error { return client.SetAssignee(ctx, key, it.id) }
	default:
		return m, nil
	}
	m.undoPanelPick(kind, key)
	m.status = i18n.Tf("updating %s %s…", key, field)
	return m, jiraMutateCmd(key, field, run)
}

// applyJiraField closes the input and fires the field's write. An empty
// summary is refused: Jira requires one.
func (m Model) applyJiraField() (tea.Model, tea.Cmd) {
	key, field := m.jiraFieldKey, m.jiraFieldName
	raw := m.jiraFieldInput.Value()
	client, ctx := m.jiraClient, m.ctx
	run := func() error { return client.SetStoryPoints(ctx, key, raw) }
	if field == "summary" {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			m.status = i18n.T("a summary can't be empty")
			return m, nil
		}
		if m.jiraIssue != nil && m.jiraIssue.Key == key && raw == m.jiraIssue.Summary {
			m.closeJiraField()
			return m, nil
		}
		run = func() error { return client.SetSummary(ctx, key, raw) }
	}
	if field == "mr-title" || field == "mr-target" {
		return m.applyMRText(field, raw)
	}
	if field == "plan-rename" || field == "plan-end" {
		return m.applyPlanSprint(field, raw)
	}
	if field == "plan-goal" {
		return m.applyPlanGoal(raw)
	}
	if field == "lane-rename" {
		return m.applyLaneRename(raw)
	}
	if field == "jql-filter" {
		return m.applySaveFilter(raw)
	}
	if field == "plan-new" {
		return m.applyPlanNew(raw)
	}
	if field == "week-add" {
		return m.applyWeekAdd(raw)
	}
	if field == "worklog-move" {
		return m.applyWorklogMove(raw)
	}
	if field == "plan-start" {
		return m.applyPlanStart(raw)
	}
	if field == "upload" {
		return m.applyUpload(raw)
	}
	if field == "link" {
		return m.applyLink(raw)
	}
	if field == "weblink" {
		return m.applyWebLink(raw)
	}
	if field == "estimate" {
		return m.applyEstimate(raw)
	}
	if field == "agent-prompt" {
		return m.applyAgentPrompt(raw)
	}
	if field == "find" {
		m.applyPanelFind(raw)
		return m, nil
	}
	if field == "worklog" {
		return m.applyWorklog(raw)
	}
	if strings.HasPrefix(field, "bulk-") {
		return m.applyBulkField(field, raw)
	}
	if field == "field" {
		return m.applyPanelExtraText(raw)
	}
	if field == "labels" {
		labels := strings.Fields(raw)
		if m.jiraIssue != nil && m.jiraIssue.Key == key && slices.Equal(labels, m.jiraIssue.Labels) {
			m.closeJiraField()
			return m, nil
		}
		run = func() error { return client.SetLabels(ctx, key, labels) }
	}
	m.undoPanelField(field, key)
	m.closeJiraField()
	m.status = i18n.Tf("updating %s %s…", key, field)
	return m, jiraMutateCmd(key, field, run)
}

// mutatedStatus says a write went through in words that fit what it
// wrote: "commented on ABC-1", "ABC-1 priority updated".
func mutatedStatus(key, field string) string {
	switch field {
	case "comment":
		return i18n.Tf("commented on %s", key)
	case "worklog":
		return i18n.Tf("logged work on %s", key)
	case "flagged":
		return i18n.Tf("flagged %s", key)
	case "flag cleared":
		return i18n.Tf("cleared the flag on %s", key)
	case "comment deleted":
		return i18n.Tf("deleted a comment on %s", key)
	case "watcher added":
		return i18n.Tf("watcher added on %s", key)
	case "watcher removed":
		return i18n.Tf("watcher removed on %s", key)
	case "links", "attachments":
		return i18n.Tf("%s %s changed", key, field)
	case "release":
		return i18n.Tf("%s released", key)
	}
	return i18n.Tf("%s %s updated", key, field)
}

// jiraMutateCmd runs a field write in the background and reports the result.
func jiraMutateCmd(key, field string, run func() error) tea.Cmd {
	return func() tea.Msg {
		return jiraMutatedMsg{key: key, field: field, err: run()}
	}
}

// handleJiraMutated reports the write outcome and reloads the issue on success
// so the panel shows the authoritative (and any cascading) values.
func (m Model) handleJiraMutated(msg jiraMutatedMsg) (tea.Model, tea.Cmd) {
	if errors.Is(msg.err, jira.ErrQueued) {
		m.queued = len(readQueue(m.store))
		m.status = i18n.Tf("offline: %s %s kept for later · %s", msg.key, msg.field, m.queueBadge())
		return m, nil
	}
	if msg.err != nil {
		m.fail(i18n.Tf("%s %s update failed: %v", msg.key, msg.field, msg.err))
		if msg.text != "" {
			m.unsent = unsentComment{key: msg.key, text: msg.text, maybe: msg.maybe}
			if msg.maybe != nil {
				m.fail(i18n.Tf("%s comment may have posted: %v · %s brings it back, posting it looks first", msg.key, msg.err, helpKey(m.keys.JiraComment)))
			} else {
				m.fail(i18n.Tf("%s comment not posted: %v · %s brings it back", msg.key, msg.err, helpKey(m.keys.JiraComment)))
			}
		}
		return m, nil
	}
	m.status = mutatedStatus(msg.key, msg.field)
	if msg.found {
		m.status = i18n.Tf("your comment was on %s already: not posted again", msg.key)
	}
	m.noteRefine(m.status)
	if d, ok := m.draft(commentDraft(msg.key)); ok && msg.field == "comment" && strings.TrimSpace(d.Text) == msg.text {
		m.dropDraft(commentDraft(msg.key)) // posted
	}
	board := m.refreshJiraAfterEdit()
	if r := m.currentRef(); r != nil && r.jiraKey == msg.key {
		return m, tea.Batch(m.loadCurrentRef(), board)
	}
	return m, board
}

// renderJiraPicker draws the modal list picker. Layout mirrors
// renderReactionPicker (rounded border, centred title, ✓ on the current value,
// cursor row in focusedColor, footer hint); a long list is windowed around the
// selection the same way renderSwitcherCommands does, so it never overflows
// maxH (the body area height passed by view.go).
// pickerWin is how many rows the picker lists within maxH: the box's
// chrome is the title + blank + two scroll markers + blank + hint (6) plus
// the border + padding (4), plus the filter input when present.
func (m *Model) pickerWin(maxH int) int {
	win := maxH - 9 - lipgloss.Height(m.pickerTitle())
	if m.jiraPicker.filterable {
		win--
	}
	return max(win, 3)
}

// pickerWindow is the rows [start, end) shown of win, the selection kept
// roughly centred and clamped to the ends.
func (m *Model) pickerWindow(win int) (start, end int) {
	n := len(m.jiraPicker.items)
	if n > win {
		start = min(max(m.jiraPicker.idx-win/2, 0), n-win)
	}
	return start, min(start+win, n)
}

// pickerRowAt is the picker item on screen row y, -1 for none; outside
// is true when y, x fall outside the box.
func (m *Model) pickerRowAt(x, y int) (idx int, outside bool) {
	if m.jiraPicker.at != nil {
		return m.dropdownRowAt(x, y)
	}
	bodyH := m.bodyH()
	top, _, inside := m.overlayAt(m.renderJiraPicker(bodyH), x, y)
	if !inside {
		return -1, true
	}
	p := &m.jiraPicker
	if p.loading || p.err != nil || len(p.items) == 0 {
		return -1, false
	}
	start, end := m.pickerWindow(m.pickerWin(bodyH))
	first := top + 2 + lipgloss.Height(m.pickerTitle()) + 1 // border, padding, title, blank
	if p.filterable {
		first++
	}
	if start > 0 || p.filterable {
		first++ // the ↑ marker's line
	}
	if i := start + y - first; y >= first && i < end {
		return i, false
	}
	if p.checked != nil && y == top+lipgloss.Height(m.renderJiraPicker(bodyH))-3 { // the hint, above padding and border
		return pickerApplyRow, false
	}
	return -1, false
}

// pickerApplyRow is pickerRowAt's answer for a multi picker's hint line,
// whose click applies the ticks.
const pickerApplyRow = -2

// pickerInline is whether the picker drops under its panel row rather than
// drawing as a modal.
func (m *Model) pickerInline() bool {
	p := &m.jiraPicker
	return p.inline != "" && m.refOpen && m.jiraIssue != nil && p.issueKey == m.jiraIssue.Key && m.pickerOnTop()
}

// pickerInlineOn is whether the inline list drops under the row name.
func (m *Model) pickerInlineOn(name string) bool {
	return m.pickerInline() && m.jiraPicker.inline == name
}

// inlinePickerRows is how many of its rows the inline list shows.
const inlinePickerRows = 8

// renderInlinePicker writes the list under its row, indented to the values'
// column; pickerLine records where its first row lands, for clicks.
func (m *Model) renderInlinePicker(b *strings.Builder, indent, width int) {
	p := &m.jiraPicker
	pad := strings.Repeat(" ", indent)
	inner := max(width-indent, 8)
	if p.filterable {
		p.filter.SetWidth(max(inner-lipgloss.Width(p.filter.Prompt)-1, 4))
		b.WriteString(pad + p.filter.View() + "\n")
	}
	switch {
	case p.loading:
		b.WriteString(pad + refDimStyle.Render(i18n.T("loading…")) + "\n")
	case p.err != nil:
		b.WriteString(pad + refErrStyle.Render(ansi.Truncate(p.err.Error(), inner, "…")) + "\n")
	case len(p.items) == 0:
		b.WriteString(pad + refDimStyle.Render(ansi.Truncate(p.emptyText(), inner, "…")) + "\n")
	default:
		start, end := m.pickerWindow(inlinePickerRows)
		if start > 0 {
			b.WriteString(pad + refDimStyle.Render(i18n.Tf("  ↑ %d more", start)) + "\n")
		}
		m.pickerLine, m.pickerStart = strings.Count(b.String(), "\n"), start
		cursorStyle := lipgloss.NewStyle().Foreground(focusedColor).Bold(true)
		for i := start; i < end; i++ {
			it := p.items[i]
			marker := " "
			if it.current {
				marker = "✓"
			}
			text := ansi.Truncate(marker+" "+it.label, inner-2, "…")
			if i == p.idx {
				b.WriteString(pad + cursorStyle.Render("▸ "+text) + "\n")
			} else {
				b.WriteString(pad + "  " + text + "\n")
			}
		}
		if end < len(p.items) {
			b.WriteString(pad + refDimStyle.Render(i18n.Tf("  ↓ %d more", len(p.items)-end)) + "\n")
		}
	}
	hint := i18n.T("↵ apply · esc cancel")
	if p.checked != nil {
		hint = i18n.T("space/tab/click ticks · [ ↵ apply ] · esc cancel")
	}
	if p.filterable {
		hint = i18n.Tf("type to filter · %s", hint)
	}
	b.WriteString(pad + refDimStyle.Render(ansi.Truncate(hint, inner, "…")) + "\n")
}

// showInlinePicker scrolls the panel so the inline list's rows and hint are
// in view, its field row too when there is room.
func (m *Model) showInlinePicker() {
	if !m.pickerInline() || m.pickerLine < 0 {
		return
	}
	start, end := m.pickerWindow(inlinePickerRows)
	last := m.pickerLine + end - start // the hint, below a ↓ line or the last row
	if end < len(m.jiraPicker.items) {
		last++
	}
	top, h := m.refView.YOffset(), m.refView.Height()
	if last >= top+h {
		m.refView.SetYOffset(min(last-h+1, max(m.pickerLine-2, 0)))
	}
}

// clickInlinePicker picks the row clicked; a click anywhere else cancels.
func (m Model) clickInlinePicker(x, y int) (tea.Model, tea.Cmd) {
	if listW, _ := m.jiraListWidth(m.width); x > listW && m.pickerLine >= 0 {
		start, end := m.pickerWindow(inlinePickerRows)
		if i := m.pickerStart + m.panelLineAt(y) - m.pickerLine; m.pickerStart == start && i >= start && i < end {
			m.jiraPicker.idx = i
			return m.handleJiraPickerKey(keyPress("enter"))
		}
	}
	return m.handleJiraPickerKey(keyPress("esc"))
}

// pickerInner is the picker's text width: one whatever the rows, so async
// results don't resize the box.
func (m *Model) pickerInner() int {
	outerW := max(min(pickerMaxWidth, m.width-4), 32)
	return max(outerW-8, 1)
}

// pickerTitle is the picker's title, wrapped to its width.
func (m *Model) pickerTitle() string {
	return lipgloss.NewStyle().Width(m.pickerInner()).Align(lipgloss.Center).Bold(true).Render(m.jiraPicker.title)
}

func (m *Model) renderJiraPicker(maxH int) string {
	if !m.jiraPicker.active {
		return ""
	}
	inner := m.pickerInner()
	parts := []string{m.pickerTitle()}
	if m.jiraPicker.filterable {
		m.jiraPicker.filter.SetWidth(max(inner-4, 8))
		parts = append(parts, m.jiraPicker.filter.View())
	}

	// A picker with a search box keeps one size while results come and go:
	// its list always takes win rows and both scroll markers' lines.
	fixed := m.jiraPicker.filterable
	win := m.pickerWin(maxH)
	listed := len(parts)
	switch {
	case m.jiraPicker.loading:
		parts = append(parts, "", refDimStyle.Render(i18n.T("loading…")))
	case m.jiraPicker.err != nil:
		parts = append(parts, "", refErrStyle.Render(ansi.Truncate(m.jiraPicker.err.Error(), inner, "…")))
	default:
		vis := m.jiraPicker.items
		if len(vis) == 0 {
			parts = append(parts, "", refDimStyle.Render(ansi.Truncate(m.jiraPicker.emptyText(), inner, "…")))
			break
		}
		start, end := m.pickerWindow(win)

		cursorStyle := lipgloss.NewStyle().Foreground(focusedColor).Bold(true)
		rows := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			it := vis[i]
			prefix := ""
			if !m.jiraPicker.filterable {
				accel := " "
				if i < 9 {
					accel = fmt.Sprintf("%d", i+1)
				}
				prefix = "[" + accel + "] "
			}
			marker := " "
			if it.current {
				marker = "✓"
			}
			text := ansi.Truncate(fmt.Sprintf("%s%s %s", prefix, marker, it.label), inner-2, "…")
			if i == m.jiraPicker.idx {
				rows = append(rows, cursorStyle.Render("▸ "+text))
			} else {
				rows = append(rows, "  "+text)
			}
		}

		// Scroll markers, shown only when there's something off-screen (win is
		// sized assuming both can appear, so the popup stays within maxH).
		parts = append(parts, "")
		if start > 0 {
			parts = append(parts, refDimStyle.Render(i18n.Tf("  ↑ %d more", start)))
		} else if fixed {
			parts = append(parts, "")
		}
		parts = append(parts, strings.Join(rows, "\n"))
		if end < len(vis) {
			parts = append(parts, refDimStyle.Render(i18n.Tf("  ↓ %d more", len(vis)-end)))
		}
	}
	if fixed { // pad the list to its full height: blank, marker, win rows, marker
		n := 0
		for _, p := range parts[listed:] {
			n += strings.Count(p, "\n") + 1
		}
		for ; n < win+3; n++ {
			parts = append(parts, "")
		}
	}

	hintTxt := i18n.T("↑/↓ move · ↵ apply · esc cancel")
	if m.jiraPicker.checked != nil {
		hintTxt = i18n.T("↑/↓ move · space/tab/click ticks · [ ↵ apply ] · esc cancel")
	}
	if m.jiraPicker.kind == jiraPickJob {
		hintTxt = i18n.T("↑/↓ move · ↵ its log · esc close")
	}
	if m.jiraPicker.kind == jiraPickDev {
		hintTxt = i18n.Tf("↵ open: GitLab here, the rest in the browser · %s branch · esc close", helpKey(m.keys.CopyBranch))
	}
	if m.jiraPicker.filterable {
		hintTxt = i18n.Tf("type to filter · %s", hintTxt)
	}
	parts = append(parts, "", lipgloss.NewStyle().Width(inner).Align(lipgloss.Center).Foreground(dimColor).Italic(true).Render(hintTxt))

	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(focusedColor).Padding(1, 3).Width(inner + 8).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// renderJiraFieldInput draws the story points or summary modal.
func (m *Model) renderJiraFieldInput() string {
	if !m.jiraFieldActive {
		return ""
	}
	title, hint, outerW := i18n.T("Set story points"), i18n.T("↵ save · empty clears · esc cancel"), 40
	switch m.jiraFieldName {
	case "summary":
		title, hint, outerW = i18n.T("Edit summary"), i18n.T("↵ save · esc cancel"), m.jiraFieldInput.Width()+12
	case "labels":
		title, hint, outerW = i18n.T("Edit labels"), i18n.T("↵ save · empty clears · esc cancel"), m.jiraFieldInput.Width()+12
	case "mr-title":
		title, hint, outerW = i18n.Tf("Title of %s", m.jiraFieldKey), i18n.T("↵ save · esc cancel"), m.jiraFieldInput.Width()+12
	case "mr-target":
		title, hint, outerW = i18n.Tf("Target branch of %s", m.jiraFieldKey), i18n.T("↵ save · esc cancel"), m.jiraFieldInput.Width()+12
	case "plan-rename":
		title, hint, outerW = i18n.T("Rename the sprint"), i18n.T("↵ save · esc cancel"), m.jiraFieldInput.Width()+12
	case "plan-end":
		title, hint, outerW = i18n.Tf("End %s on", m.jiraFieldKey), i18n.T("↵ save · esc cancel"), m.jiraFieldInput.Width()+12
	case "plan-goal":
		title, hint, outerW = i18n.Tf("Goal of %s", m.jiraFieldKey), i18n.T("↵ save · esc cancel"), m.jiraFieldInput.Width()+12
	case "lane-rename":
		title, hint, outerW = i18n.T("Rename the lane"), i18n.T("↵ save · empty: its first column's · esc cancel"), m.jiraFieldInput.Width()+12
	case "jql-filter":
		title, hint, outerW = i18n.T("Save as a Jira filter"), i18n.T("↵ save, starred · esc cancel"), m.jiraFieldInput.Width()+12
	case "week-add":
		title, hint, outerW = i18n.T("Add an issue"), i18n.T("↵ add its row · esc cancel"), m.jiraFieldInput.Width()+12
	case "worklog-move":
		title, hint, outerW = i18n.Tf("Move %s to", m.jiraFieldKey), i18n.T("↵ move · esc cancel"), m.jiraFieldInput.Width()+12
	case "plan-new":
		title, hint, outerW = i18n.T("New sprint"), i18n.T("↵ create · esc cancel"), m.jiraFieldInput.Width()+12
	case "plan-start":
		title, hint, outerW = i18n.Tf("Start %s today", m.jiraFieldKey), i18n.T("↵ start · esc cancel"), m.jiraFieldInput.Width()+12
	case "upload":
		title, hint, outerW = i18n.T("Upload a file"), i18n.T("tab complete · ↵ upload · esc cancel"), m.jiraFieldInput.Width()+12
	case "link":
		title, hint, outerW = i18n.Tf("Link %s", m.jiraLinkChoice.label), i18n.T("↑↓ pick · ↵ link · esc cancel"), m.jiraFieldInput.Width()+12
	case "weblink":
		title, hint, outerW = i18n.Tf("Web link on %s", m.jiraFieldKey), i18n.T("↵ add · esc cancel"), m.jiraFieldInput.Width()+12
	case "estimate":
		title, hint, outerW = i18n.Tf("Original estimate of %s", m.jiraFieldKey), i18n.T("↵ save · esc cancel"), m.jiraFieldInput.Width()+12
	case "agent-prompt":
		title, hint, outerW = i18n.Tf("Prompt for %s's agent", m.jiraFieldKey), i18n.T("↵ send · esc cancel"), m.jiraFieldInput.Width()+12
	case "find":
		title, hint, outerW = i18n.Tf("Find in %s", m.jiraFieldKey), i18n.T("↵ find · n / N next / previous · esc cancel"), m.jiraFieldInput.Width()+12
	case "worklog":
		title, hint, outerW = i18n.T("Log work"), i18n.T("↵ log · esc cancel"), m.jiraFieldInput.Width()+12
		switch {
		case m.worklogFromTimer && m.timerNext != "":
			hint = i18n.Tf("↵ log, then time %s · ctrl+t move the timer there · ctrl+d drop · esc keep timing", m.timerNext)
		case m.worklogFromTimer:
			hint = i18n.T("↵ log · ctrl+d drop the timer · esc keep timing")
		}
	case "bulk-labels":
		title, hint, outerW = i18n.T("Edit labels"), i18n.T("↵ save · esc cancel"), m.jiraFieldInput.Width()+12
	case "bulk-points":
		title = i18n.T("Set story points")
	case "field":
		title, hint, outerW = i18n.Tf("Edit %s", m.panelEditField().Name), i18n.T("↵ save · empty clears · esc cancel"), m.jiraFieldInput.Width()+12
	}
	if outerW > m.width-4 {
		outerW = m.width - 4
	}
	if outerW < 24 {
		outerW = 24
	}
	inner := outerW - 8
	if inner < 1 {
		inner = 1
	}
	header := lipgloss.NewStyle().Width(inner).Align(lipgloss.Center).Bold(true).Render(i18n.Tf("%s — %s", title, m.jiraFieldKey))
	hint = lipgloss.NewStyle().Width(inner).Align(lipgloss.Center).Foreground(dimColor).Italic(true).Render(hint)
	rows := append([]string{header, "", m.jiraFieldInput.View()}, m.labelLines(2)...)
	rows = append(rows, m.linkLines(inner)...)
	body := lipgloss.JoinVertical(lipgloss.Left, append(rows, "", hint)...)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(focusedColor).Padding(1, 3).Render(body)
}
