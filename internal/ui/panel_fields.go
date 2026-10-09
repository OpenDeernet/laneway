package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// The panel's field cursor: tab / shift-tab walk the issue's fields, enter
// edits the selected one with the same editor as its own key. After the
// panel's own fields come the rest of the issue's edit screen (editmeta),
// edited with the transition form's per-kind editors and written at once.
// The pinned fields show (* on one pins or unpins it, shared with the web):
// the panel's own unless unpinned, editmeta ones once pinned, and filled
// rich text always; a More row (enter opens it, for the session) over the
// others.

// panelField is one of the panel's own fields.
type panelField struct {
	id   string                 // its pin (jira.FieldPins), shared with the web; "" always shows
	name string                 // the label its row shows
	edit func(m *Model) tea.Cmd // nil: read-only, a row only while it has a value
}

// panelFields is set in init: its editors render the panel, which reads it.
var panelFields []panelField

func init() {
	panelFields = []panelField{
		{"", "Summary", func(m *Model) tea.Cmd { m.openJiraSummaryInput(); return nil }},
		{"status", "Status", func(m *Model) tea.Cmd { return m.openJiraStatusPicker() }},
		{"priority", "Priority", func(m *Model) tea.Cmd { return m.openJiraPriorityPicker() }},
		{"points", "Points", func(m *Model) tea.Cmd { m.openJiraPointsInput(); return nil }},
		{"assignee", "Assignee", func(m *Model) tea.Cmd { return m.openJiraAssigneePicker() }},
		{"reporter", "Reporter", func(m *Model) tea.Cmd { return m.openJiraReporterPicker() }},
		{"labels", "Labels", func(m *Model) tea.Cmd { m.openJiraLabelsInput(); return nil }},
		{"updated", "Updated", nil},
		{"created", "Created", nil},
		{"resolution", "Resolved", nil},
		{"watches", "Watchers", nil},
		{"votes", "Votes", nil},
		{"timetracking", "Time", nil},
		{"deployed", "Deployed", nil},
	}
}

// panelRow is one stop of the field cursor: one of the panel's own fields,
// an editmeta field, or the More row.
type panelRow struct {
	own  *panelField
	ff   *jiraFormField
	more bool
}

// name is the row's label, as the cursor reports it.
func (r panelRow) name() string {
	switch {
	case r.own != nil:
		return r.own.name
	case r.ff != nil:
		return r.ff.Name
	}
	return moreFieldsName
}

// pinID is the row's pin, "" for one that always shows. A sprint field's
// is "sprint", as the web's own Sprint row.
func (r panelRow) pinID() string {
	switch {
	case r.own != nil:
		return r.own.id
	case r.ff == nil || richField(*r.ff):
		return ""
	case r.ff.Kind == jira.KindSprint:
		return "sprint"
	}
	return r.ff.ID
}

// panelExtraMsg is the shown issue's edit screen fetched.
type panelExtraMsg struct {
	key      string
	fields   []jiraFormField
	facts    jira.Facts
	err      error
	webLinks []jira.WebLink // its remote links; nil when they failed to load
	children []jira.Child   // an epic's child issues; nil for other issues
	desk     bool           // its project is a service desk
	request  *jira.Request  // the issue as the desk's request; nil when none or it failed
}

// fetchPanelExtra loads the shown issue's other editable fields.
func (m *Model) fetchPanelExtra() tea.Cmd {
	if m.jiraIssue == nil || !m.jiraClient.Enabled() {
		return nil
	}
	c, ctx, key, epic := m.jiraClient, m.ctx, m.jiraIssue.Key, m.isEpic(m.jiraIssue)
	return func() tea.Msg {
		_, _ = c.Myself(ctx)     // cached: which comments are yours to edit
		go c.WarmUsers(ctx, key) // @ in a comment answers at once
		links := make(chan []jira.WebLink, 1)
		go func() {
			l, _ := c.WebLinks(ctx, key) // a failure only leaves them out
			links <- l
		}()
		kids := make(chan []jira.Child, 1)
		go func() {
			var k []jira.Child
			if epic {
				k, _ = c.Children(ctx, key) // a failure only leaves them out
			}
			kids <- k
		}()
		var desk bool
		req := make(chan *jira.Request, 1)
		go func() {
			var r *jira.Request
			if desk, _ = c.IsServiceDesk(ctx, issueProject(key)); desk {
				r, _ = c.Request(ctx, key) // a failure only leaves it out
			}
			req <- r
		}()
		metas, values, err := c.EditMeta(ctx, key)
		fields := make([]jiraFormField, len(metas))
		for i, fm := range metas {
			fields[i] = jiraFormField{FieldMeta: fm, required: fm.Required, val: jira.DecodeValue(fm.Kind, values[fm.ID]), raw: values[fm.ID]}
		}
		request := <-req // and desk with it
		return panelExtraMsg{key: key, fields: fields, facts: jira.IssueFacts(values), err: err, webLinks: <-links, children: <-kids, desk: desk, request: request}
	}
}

// earlyExtra draws the shown issue's other fields from the edit screen last
// seen for its project and type (jira.Issue.Screen), till editmeta answers:
// no jump when it does.
func (m *Model) earlyExtra() {
	iss := m.jiraIssue
	if iss == nil || m.panelExtraKey == iss.Key || len(iss.Screen) == 0 {
		return
	}
	fields := make([]jiraFormField, len(iss.Screen))
	for i, fm := range iss.Screen {
		fields[i] = jiraFormField{FieldMeta: fm, required: fm.Required, val: iss.ScreenValues[fm.ID]}
	}
	m.panelExtra, m.panelExtraKey, m.panelExtraEarly = fields, iss.Key, true
}

// handlePanelExtra installs the fields when they are still the shown issue's.
// A failed fetch leaves the panel's own fields only.
func (m Model) handlePanelExtra(msg panelExtraMsg) (tea.Model, tea.Cmd) {
	if m.jiraIssue == nil || m.jiraIssue.Key != msg.key {
		return m, nil
	}
	m.webLinks, m.webLinksKey = msg.webLinks, msg.key
	m.children, m.request = msg.children, msg.request
	if m.desks == nil {
		m.desks = map[string]bool{}
	}
	m.desks[issueProject(msg.key)] = msg.desk
	if msg.err != nil {
		if m.panelExtraEarly { // the remembered screen stays, to read
			for i := range m.panelExtra {
				m.panelExtra[i].ReadOnly = true
			}
			m.panelExtraEarly = false
		}
		m.renderRef()
		return m, nil
	}
	m.panelExtra, m.panelExtraKey, m.panelFacts, m.panelExtraEarly = msg.fields, msg.key, msg.facts, false
	m.renderRef()
	return m, nil
}

// moreFieldsName is the More row's name for the field cursor.
var moreFieldsName = i18n.N("More fields")

// pinned is whether row r shows on every issue rather than under More.
func (m *Model) pinned(r panelRow) bool {
	id := r.pinID()
	return id == "" || jira.Pinned(m.fieldPins, id, r.own != nil)
}

// rowValue is row r's value as text, "" when empty.
func (m *Model) rowValue(r panelRow) string {
	if r.ff != nil {
		return jiraValueText(r.ff.val)
	}
	if r.own == nil {
		return ""
	}
	return m.ownValue(r.own.id)
}

// splitRows is the shown issue's field rows after Summary: those pinned
// and the ones More folds, the panel's own first, then editmeta's once
// they load. A read-only own field is a row only while it has a value.
func (m *Model) splitRows() (top, rest []panelRow) {
	if m.jiraIssue == nil {
		return nil, nil
	}
	add := func(r panelRow) {
		if m.pinned(r) {
			top = append(top, r)
		} else {
			rest = append(rest, r)
		}
	}
	for i := range panelFields[1:] {
		f := &panelFields[i+1]
		if f.edit == nil && m.ownValue(f.id) == "" {
			continue
		}
		add(panelRow{own: f})
	}
	if m.panelExtraKey == m.jiraIssue.Key {
		for i := range m.panelExtra {
			add(panelRow{ff: &m.panelExtra[i]})
		}
	}
	return top, rest
}

// foldedShown is the rows an open More shows: all, or the filled ones
// with ui.empty_fields: hide.
func (m *Model) foldedShown(rest []panelRow) []panelRow {
	if !m.moreFields {
		return nil
	}
	if !m.opts.hideEmpty || m.showEmpty {
		return rest
	}
	var out []panelRow
	for _, r := range rest {
		if m.rowValue(r) != "" {
			out = append(out, r)
		}
	}
	return out
}

// panelRows are the field cursor's stops in the panel's order: Summary,
// the pinned rows, then More and the rows it shows open.
func (m *Model) panelRows() []panelRow {
	if m.jiraIssue == nil {
		return nil
	}
	top, rest := m.splitRows()
	rows := append([]panelRow{{own: &panelFields[0]}}, top...)
	if len(rest) > 0 {
		rows = append(rows, panelRow{more: true})
		rows = append(rows, m.foldedShown(rest)...)
	}
	return rows
}

// extraFields are the editmeta fields the panel shows, in its order.
func (m *Model) extraFields() []jiraFormField {
	var out []jiraFormField
	for _, r := range m.panelRows() {
		if r.ff != nil {
			out = append(out, *r.ff)
		}
	}
	return out
}

// hiddenFields is how many empty fields ui.empty_fields: hide folds away
// in an open More.
func (m *Model) hiddenFields() int {
	if !m.moreFields {
		return 0
	}
	_, rest := m.splitRows()
	return len(rest) - len(m.foldedShown(rest))
}

func (m *Model) panelFieldCount() int {
	return len(m.panelRows())
}

// pinPanelField pins the selected field, or unpins it; it reports whether
// one that can be was selected.
func (m *Model) pinPanelField() bool {
	i := m.panelFieldIdx()
	if i < 0 || m.store == nil {
		return false
	}
	r := m.panelRows()[i]
	id := r.pinID()
	if id == "" {
		return false
	}
	pins, err := jira.SetFieldPin(m.store, id, !m.pinned(r))
	if err != nil {
		m.fail(i18n.Tf("pin not kept: %s", err.Error()))
		return true
	}
	m.fieldPins = pins
	name := i18n.T(r.name())
	if pins[id] {
		m.status = i18n.Tf("%s pinned: shown on every issue", name)
	} else {
		m.status = i18n.Tf("%s unpinned: under %s", name, i18n.T(moreFieldsName))
	}
	// the cursor follows the field to where it moved, else onto More
	rows := m.panelRows()
	j := slices.IndexFunc(rows, func(r panelRow) bool { return r.pinID() == id })
	if j < 0 {
		j = slices.IndexFunc(rows, func(r panelRow) bool { return r.more })
	}
	if j >= 0 {
		m.fieldCursor = j
	}
	m.renderRef()
	return true
}

// panelFieldIdx is the selected field's index (panelFields, then
// extraFields), -1 when none or the cursor belongs to another issue.
func (m *Model) panelFieldIdx() int {
	if m.jiraIssue == nil || m.fieldCursorKey != m.jiraIssue.Key || m.fieldCursor < 0 || m.fieldCursor >= m.panelFieldCount() {
		return -1
	}
	return m.fieldCursor
}

// panelFieldSel is the selected field's name, "" when none.
func (m *Model) panelFieldSel() string {
	if i := m.panelFieldIdx(); i >= 0 {
		return m.panelRows()[i].name()
	}
	return ""
}

// panelFieldRow is the cursor index of the panel's own field name, -1 when
// it has no row.
func (m *Model) panelFieldRow(name string) int {
	return slices.IndexFunc(m.panelRows(), func(r panelRow) bool { return r.own != nil && r.own.name == name })
}

// movePanelField steps the cursor by d; stepping off either end drops it,
// and tab past the last field hands focus to the board.
func (m *Model) movePanelField(d int) {
	i := m.panelFieldIdx()
	switch {
	case i < 0 && d > 0:
		i = 0
	case i < 0:
		m.focus = focusJira
		m.renderJira()
		return
	default:
		i += d
	}
	if i >= m.panelFieldCount() {
		m.clearPanelField()
		m.focus = focusJira
		m.renderJira()
		return
	}
	if i < 0 {
		m.clearPanelField()
		return
	}
	m.fieldCursor, m.fieldCursorKey = i, m.jiraIssue.Key
	m.commentCursorKey = ""
	m.refView.GotoTop() // the fields sit at the top
	m.renderRef()
}

func (m *Model) clearPanelField() {
	m.fieldCursor, m.fieldCursorKey = -1, ""
	m.renderRef()
}

// editPanelField opens the selected field's editor.
func (m *Model) editPanelField() tea.Cmd {
	i := m.panelFieldIdx()
	if i < 0 {
		return nil
	}
	r := m.panelRows()[i]
	switch {
	case r.more:
		m.moreFields = !m.moreFields
		m.renderRef()
		return nil
	case r.own != nil && r.own.edit == nil:
		m.status = i18n.Tf("%s is read-only", i18n.T(r.own.name))
		return nil
	case r.own != nil:
		return r.own.edit(m)
	}
	ff := *r.ff
	if m.panelExtraEarly {
		m.status = i18n.T("a moment: asking Jira what can be edited…")
		return nil
	}
	if ff.ReadOnly {
		m.status = i18n.Tf("Jira lets no one edit %s on %s now", ff.Name, m.jiraIssue.Key)
		return nil
	}
	m.panelEditID = ff.ID
	switch ff.Kind {
	case jira.KindDoc:
		key := m.jiraIssue.Key
		return func() tea.Msg {
			ed, err := jira.EditableDescription(ff.raw)
			return descLoadedMsg{key: key, field: ff.ID, md: ed.Markdown, kept: ed.Kept, base: jira.DocBase(ff.raw), err: err}
		}
	case jira.KindText, jira.KindStrings, jira.KindNumber, jira.KindDate, jira.KindTime, jira.KindIssue:
		hint := ""
		switch ff.Kind {
		case jira.KindDate:
			hint = i18n.T("2006-01-02, today, +3d, fri")
		case jira.KindTime:
			hint = i18n.T("fri 14:00, 2026-10-01 9:30")
		case jira.KindIssue:
			hint = i18n.T("issue key (empty clears)")
		}
		m.openJiraTextInput("field", ff.val.Text, hint, 0)
		m.startFieldInline(i)
		return nil
	}
	if ff.Kind == jira.KindSprint {
		ff.Options = m.sprintOptions()
	}
	cmd := m.openFieldPicker(ff, m.jiraIssue.Key)
	m.jiraPicker.inline = ff.ID
	return cmd
}

// sprintOptions are the board's sprints to pick, and none.
func (m *Model) sprintOptions() []jira.Option {
	opts := []jira.Option{{ID: "", Name: i18n.T("none (backlog)")}}
	for _, v := range m.jiraTab.views {
		if v.kind == jiraViewSprint {
			opts = append(opts, jira.Option{ID: strconv.Itoa(v.sprint), Name: v.name})
		}
	}
	return opts
}

// panelEditField is the extra field being edited, zero when gone.
func (m *Model) panelEditField() jiraFormField {
	for _, ff := range m.extraFields() {
		if ff.ID == m.panelEditID {
			return ff
		}
	}
	return jiraFormField{}
}

// extraField is key's editmeta field id, while the panel shows key.
func (m *Model) extraField(key, id string) (jiraFormField, bool) {
	if id == "" || m.panelExtraKey != key {
		return jiraFormField{}, false
	}
	i := slices.IndexFunc(m.panelExtra, func(ff jiraFormField) bool { return ff.ID == id })
	if i < 0 {
		return jiraFormField{}, false
	}
	return m.panelExtra[i], true
}

// applyPanelExtraText writes the field input's text to the edited field.
func (m Model) applyPanelExtraText(raw string) (tea.Model, tea.Cmd) {
	ff := m.panelEditField()
	if ff.ID == "" {
		m.closeJiraField()
		return m, nil
	}
	if strings.TrimSpace(raw) == strings.TrimSpace(ff.val.Text) {
		m.closeJiraField()
		return m, nil
	}
	ff.val = jira.Value{Text: strings.TrimSpace(raw)}
	if m.refuseEmpty(ff) {
		return m, nil
	}
	cmd, err := m.writePanelExtra(m.jiraFieldKey, ff)
	if err != nil {
		m.fail(ff.Name + ": " + err.Error())
		return m, nil
	}
	m.closeJiraField()
	return m, cmd
}

// pickPanelExtra applies a person or option picked for the edited field and
// writes it.
func (m *Model) pickPanelExtra(key string, kind jiraPickerKind, it jiraPickerItem) tea.Cmd {
	ff := m.panelEditField()
	if ff.ID == "" {
		return nil
	}
	// Copy the slices: the pick must not touch the shown value until Jira
	// has it.
	ff.val.Users = append([]jira.User(nil), ff.val.Users...)
	ff.val.Options = append([]jira.Option(nil), ff.val.Options...)
	pickFieldValue(&ff, kind, it)
	if m.refuseEmpty(ff) {
		return nil
	}
	cmd, err := m.writePanelExtra(key, ff)
	if err != nil {
		m.fail(ff.Name + ": " + err.Error())
	}
	return cmd
}

// refuseEmpty says so on the status line when ff's value would empty a
// field the field configuration requires; nothing is sent and the edit
// stays open.
func (m *Model) refuseEmpty(ff jiraFormField) bool {
	if !ff.required || !ff.val.Clears() {
		return false
	}
	m.fail(i18n.Tf("%s is required", ff.Name))
	return true
}

// writePanelExtra sends ff's value; the reload after shows it.
func (m *Model) writePanelExtra(key string, ff jiraFormField) (tea.Cmd, error) {
	v, _, err := jira.EncodeValue(ff.Kind, ff.val)
	if err != nil {
		return nil, err
	}
	c, ctx, name := m.jiraClient, m.ctx, strings.ToLower(ff.Name)
	m.status = fmt.Sprintf(i18n.T("updating %s %s…"), key, name)
	return jiraMutateCmd(key, name, func() error { return c.SetField(ctx, key, ff.ID, v) }), nil
}
