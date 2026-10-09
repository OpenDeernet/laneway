package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/jira"
)

// TestPanelFieldCursor: tab walks the fields, shift+tab steps back, esc drops
// the cursor before closing the panel, tab past the last goes to the board.
func TestPanelFieldCursor(t *testing.T) {
	m := loadedJiraModel(t)
	step := func(k string) {
		t.Helper()
		out, _ := m.handleRefKey(keyMsg(t, k))
		m = out.(Model)
	}
	step("tab")
	if got := m.panelFieldSel(); got != "Summary" {
		t.Fatalf("first tab: %q", got)
	}
	step("tab")
	step("tab")
	if got := m.panelFieldSel(); got != "Priority" {
		t.Fatalf("third tab: %q", got)
	}
	step("shift+tab")
	if got := m.panelFieldSel(); got != "Status" {
		t.Fatalf("shift+tab: %q", got)
	}
	step("esc")
	if m.panelFieldSel() != "" || !m.refOpen {
		t.Fatalf("esc: sel %q, open %v", m.panelFieldSel(), m.refOpen)
	}
	for range m.panelFieldCount() {
		step("tab")
	}
	if m.panelFieldSel() != "Labels" || m.focus != focusRef {
		t.Fatalf("on last: sel %q, focus %v", m.panelFieldSel(), m.focus)
	}
	step("tab")
	if m.panelFieldSel() != "" || m.focus != focusJira {
		t.Fatalf("past last: sel %q, focus %v", m.panelFieldSel(), m.focus)
	}
}

// TestPinOwnFields: * unpins one of the panel's own fields, under More
// with the cursor on it; a read-only one with a value is a stop to pin,
// enter on it says so; * on Summary pins the issue instead.
func TestPinOwnFields(t *testing.T) {
	m := loadedJiraModel(t)
	m.jiraIssue.Updated = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m.renderRef()
	m.fieldCursor, m.fieldCursorKey = rowNamed(m, "Priority"), "ABC-1"
	out, _ := m.handleRefKey(keyMsg(t, "*"))
	m = out.(Model)
	if on, ok := jira.FieldPins(m.store)["priority"]; on || !ok || m.panelFieldSel() != moreFieldsName {
		t.Fatalf("pins %v, cursor on %q", jira.FieldPins(m.store), m.panelFieldSel())
	}
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "Priority:") || !strings.Contains(view, "More fields:") {
		t.Fatalf("unpinned:\n%s", view)
	}
	m.moreFields = true
	if rowNamed(m, "Priority") != rowNamed(m, moreFieldsName)+1 {
		t.Fatalf("Priority at %d, More at %d", rowNamed(m, "Priority"), rowNamed(m, moreFieldsName))
	}
	m.fieldCursor = rowNamed(m, "Updated")
	if out, _ = m.handleRefKey(keyMsg(t, "enter")); !strings.Contains(out.(Model).status, "read-only") {
		t.Errorf("enter on Updated: %q", out.(Model).status)
	}
	out, _ = m.handleRefKey(keyMsg(t, "*"))
	m = out.(Model)
	if on, ok := m.fieldPins["updated"]; on || !ok || m.panelFieldSel() != "Updated" || rowNamed(m, "Updated") < rowNamed(m, moreFieldsName) {
		t.Fatalf("pins %v, cursor on %q", m.fieldPins, m.panelFieldSel())
	}
	m.fieldCursor = 0
	out, _ = m.handleRefKey(keyMsg(t, "*"))
	if m = out.(Model); !m.pins["ABC-1"] {
		t.Error("* on Summary did not pin the issue")
	}
}

// TestPanelFieldEnter: enter opens the selected field's editor.
func TestPanelFieldEnter(t *testing.T) {
	m := loadedJiraModel(t)
	for range 4 { // Summary Status Priority Points
		out, _ := m.handleRefKey(keyMsg(t, "tab"))
		m = out.(Model)
	}
	if !strings.Contains(m.View().Content, "Points:") {
		t.Fatal("points row not drawn")
	}
	out, _ := m.handleRefKey(keyMsg(t, "enter"))
	m = out.(Model)
	if !m.jiraFieldActive || m.jiraFieldName != "points" || m.jiraFieldInput.Value() != "5" {
		t.Fatalf("input: active %v, field %q, value %q", m.jiraFieldActive, m.jiraFieldName, m.jiraFieldInput.Value())
	}

	m = loadedJiraModel(t)
	out, _ = m.handleRefKey(keyMsg(t, "tab"))
	out, _ = out.(Model).handleRefKey(keyMsg(t, "tab"))
	out, cmd := out.(Model).handleRefKey(keyMsg(t, "enter"))
	if got := out.(Model); !got.jiraPicker.active || got.jiraPicker.kind != jiraPickStatus || cmd == nil {
		t.Fatalf("status picker not opened: %+v", got.jiraPicker)
	}
}

// TestPanelFieldOtherIssue: the cursor belongs to the issue it was set on.
func TestPanelFieldOtherIssue(t *testing.T) {
	m := loadedJiraModel(t)
	out, _ := m.handleRefKey(keyMsg(t, "tab"))
	m = out.(Model)
	m.fieldCursorKey = "ABC-2"
	if m.panelFieldSel() != "" {
		t.Fatal("cursor should not carry to another issue")
	}
}

// rowNamed is the field cursor index of the panel row named name, -1 when
// none.
func rowNamed(m Model, name string) int {
	return slices.IndexFunc(m.panelRows(), func(r panelRow) bool { return r.name() == name })
}

// withExtra gives the loaded issue two editmeta fields, writes going to a
// fake Jira whose request bodies land in bodies.
func withExtra(t *testing.T, bodies *[]string) Model {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, r.Method+" "+r.URL.Path+" "+string(b))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	m := loadedJiraModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	out, _ := m.handlePanelExtra(panelExtraMsg{key: "ABC-1", fields: []jiraFormField{
		{FieldMeta: jira.FieldMeta{ID: "customfield_3", Name: "Team", Kind: jira.KindOption,
			Options: []jira.Option{{ID: "7", Name: "Core"}, {ID: "8", Name: "Web"}}},
			val: jira.Value{Options: []jira.Option{{ID: "7", Name: "Core"}}}},
		{FieldMeta: jira.FieldMeta{ID: "customfield_5", Name: "Ticket ref", Kind: jira.KindText}},
	}})
	m = out.(Model)
	m.moreFields = true // unpinned: under More, opened
	m.renderRef()
	return m
}

// TestPanelExtraFields: editmeta fields show after the panel's own, the
// cursor walks onto them, and a pick writes the field.
func TestPanelExtraFields(t *testing.T) {
	var bodies []string
	m := withExtra(t, &bodies)
	view := m.View().Content
	if !strings.Contains(view, "Team:") || !strings.Contains(view, "Core") || !strings.Contains(view, "Ticket ref:") {
		t.Fatal("extra fields not drawn")
	}
	for range rowNamed(m, "Team") + 1 {
		out, _ := m.handleRefKey(keyMsg(t, "tab"))
		m = out.(Model)
	}
	if got := m.panelFieldSel(); got != "Team" {
		t.Fatalf("cursor on %q", got)
	}
	out, _ := m.handleRefKey(keyMsg(t, "enter"))
	m = out.(Model)
	if !m.jiraPicker.active || m.jiraPicker.kind != jiraPickFormOption || len(m.jiraPicker.items) != 2 {
		t.Fatalf("option picker: %+v", m.jiraPicker)
	}
	m.jiraPicker.idx = 1
	out, cmd := m.applyJiraPick()
	m = out.(Model)
	if cmd == nil || m.jiraPicker.active {
		t.Fatal("expected a write")
	}
	if msg := cmd().(jiraMutatedMsg); msg.err != nil || msg.field != "team" {
		t.Fatalf("mutation: %+v", msg)
	}
	if len(bodies) != 1 || bodies[0] != `PUT /rest/api/3/issue/ABC-1 {"fields":{"customfield_3":{"id":"8"}}}` {
		t.Errorf("requests = %q", bodies)
	}
	if m.extraFields()[0].val.Options[0].ID != "7" {
		t.Error("shown value changed before Jira had it")
	}
}

// TestPanelExtraText: a text field opens the field input; enter writes it,
// unchanged closes without a write.
func TestPanelExtraText(t *testing.T) {
	var bodies []string
	m := withExtra(t, &bodies)
	m.fieldCursor, m.fieldCursorKey = rowNamed(m, "Ticket ref"), "ABC-1"
	out, _ := m.handleRefKey(keyMsg(t, "enter"))
	m = out.(Model)
	if !m.jiraFieldActive || m.jiraFieldName != "field" || strings.Contains(m.View().Content, "Edit Ticket ref") || !regexp.MustCompile(`Ticket ref: +❯`).MatchString(ansi.Strip(m.View().Content)) {
		t.Fatalf("input: active %v, field %q", m.jiraFieldActive, m.jiraFieldName)
	}
	if _, cmd := m.applyJiraField(); cmd != nil {
		t.Error("unchanged value should not write")
	}
	m.jiraFieldInput.SetValue(" T-9 ")
	_, cmd := m.applyJiraField()
	if cmd == nil {
		t.Fatal("expected a write")
	}
	cmd()
	if len(bodies) != 1 || !strings.HasSuffix(bodies[0], `{"fields":{"customfield_5":"T-9"}}`) {
		t.Errorf("requests = %q", bodies)
	}
}

// TestPanelExtraRequired: a field the field configuration requires is
// marked *; emptying it, typed or picked, is refused on the status line
// with nothing sent, the input still open with what was typed.
func TestPanelExtraRequired(t *testing.T) {
	var bodies []string
	m := withExtra(t, &bodies)
	m.panelExtra[1].required, m.panelExtra[1].val = true, jira.Value{Text: "T-1"}
	m.panelExtra = append(m.panelExtra, jiraFormField{FieldMeta: jira.FieldMeta{ID: "components", Name: "Components", Kind: jira.KindOptions,
		Options: []jira.Option{{ID: "1", Name: "Web"}}}, required: true, val: jira.Value{Options: []jira.Option{{ID: "1", Name: "Web"}}}})
	m.renderRef()
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Ticket ref *:") || !strings.Contains(view, "Components *:") || strings.Contains(view, "Team *") {
		t.Fatalf("marks:\n%s", view)
	}
	m.fieldCursor, m.fieldCursorKey = rowNamed(m, "Ticket ref"), "ABC-1"
	out, _ := m.handleRefKey(keyMsg(t, "enter"))
	m = out.(Model)
	m.jiraFieldInput.SetValue("  ")
	out, cmd := m.applyJiraField()
	m = out.(Model)
	if cmd != nil || !m.jiraFieldActive || m.jiraFieldInput.Value() != "  " || m.status != "Ticket ref is required" {
		t.Fatalf("emptied text: cmd %v, active %v, status %q", cmd != nil, m.jiraFieldActive, m.status)
	}
	m.closeJiraField()
	m.fieldCursor = rowNamed(m, "Components")
	out, _ = m.handleRefKey(keyMsg(t, "enter"))
	m = out.(Model)
	m.jiraPicker.idx = 0 // unticks the only one
	out, cmd = m.applyJiraPick()
	if m = out.(Model); cmd != nil || m.status != "Components is required" {
		t.Fatalf("emptied options: cmd %v, status %q", cmd != nil, m.status)
	}
	m.panelExtra = append(m.panelExtra, jiraFormField{FieldMeta: jira.FieldMeta{ID: "customfield_9", Name: "Test notes", Kind: jira.KindDoc}, required: true})
	m.openDescEdit(&descEdit{key: "ABC-1", field: "customfield_9", before: "old"}, "\n")
	out, cmd = m.handleDescEditKey(keyMsg(t, "ctrl+s"))
	if m = out.(Model); cmd != nil || m.descEdit == nil || m.status != "Test notes is required" {
		t.Fatalf("emptied doc: cmd %v, open %v, status %q", cmd != nil, m.descEdit != nil, m.status)
	}
	if len(bodies) != 0 {
		t.Errorf("requests = %q", bodies)
	}
}

// TestPanelExtraDate: a date field takes a typed day; a bad one keeps the
// input with the reason.
func TestPanelExtraDate(t *testing.T) {
	var bodies []string
	m := withExtra(t, &bodies)
	m.panelExtra = append(m.panelExtra, jiraFormField{FieldMeta: jira.FieldMeta{ID: "duedate", Name: "Due date", Kind: jira.KindDate}})
	m.fieldCursor, m.fieldCursorKey = rowNamed(m, "Due date"), "ABC-1"
	out, _ := m.handleRefKey(keyMsg(t, "enter"))
	m = out.(Model)
	m.jiraFieldInput.SetValue("soon")
	out, cmd := m.applyJiraField()
	if cmd != nil || !out.(Model).jiraFieldActive || !strings.Contains(out.(Model).status, "not a date") {
		t.Fatalf("bad date: cmd %v, status %q", cmd != nil, out.(Model).status)
	}
	m.jiraFieldInput.SetValue("2026-10-01")
	_, cmd = m.applyJiraField()
	if cmd == nil {
		t.Fatal("expected a write")
	}
	cmd()
	if len(bodies) != 1 || !strings.HasSuffix(bodies[0], `{"fields":{"duedate":"2026-10-01"}}`) {
		t.Errorf("requests = %q", bodies)
	}
}

// TestPanelExtraSprint: a sprint field picks from the board's sprints and
// writes the id.
func TestPanelExtraSprint(t *testing.T) {
	var bodies []string
	m := withExtra(t, &bodies)
	m.jiraTab.views = []jiraView{{kind: jiraViewSprint, name: "Sprint 9", sprint: 9}, {kind: jiraViewBacklog, name: "Backlog"}}
	m.panelExtra = append(m.panelExtra, jiraFormField{FieldMeta: jira.FieldMeta{ID: "customfield_20", Name: "Sprint", Kind: jira.KindSprint}})
	m.fieldCursor, m.fieldCursorKey = rowNamed(m, "Sprint"), "ABC-1"
	out, _ := m.handleRefKey(keyMsg(t, "enter"))
	m = out.(Model)
	if !m.jiraPicker.active || len(m.jiraPicker.items) != 2 || m.jiraPicker.items[1].label != "Sprint 9" {
		t.Fatalf("picker = %+v", m.jiraPicker.items)
	}
	m.jiraPicker.idx = 1
	_, cmd := m.applyJiraPick()
	cmd()
	if len(bodies) != 1 || !strings.HasSuffix(bodies[0], `{"fields":{"customfield_20":9}}`) {
		t.Errorf("requests = %q", bodies)
	}
}

// TestRichFieldSection: a filled rich-text field reads as its own markdown
// section; its row points there.
func TestRichFieldSection(t *testing.T) {
	m := loadedJiraModel(t)
	out, _ := m.handlePanelExtra(panelExtraMsg{key: "ABC-1", fields: []jiraFormField{
		{FieldMeta: jira.FieldMeta{ID: "customfield_7", Name: "Test information", Kind: jira.KindDoc}, val: jira.Value{Text: "Steps:\n\n- open cart\n- pay"}},
		{FieldMeta: jira.FieldMeta{ID: "customfield_8", Name: "Notes", Kind: jira.KindDoc}},
	}})
	m = out.(Model)
	view := ansi.Strip(m.renderJiraIssue(m.jiraIssue, 60))
	if !regexp.MustCompile(`Test information:\s+↓ below`).MatchString(view) {
		t.Errorf("row lacks the pointer:\n%s", view)
	}
	i := strings.LastIndex(view, "Test information")
	if i < 0 || !strings.Contains(view[i:], "open cart\n") || !strings.Contains(view[i:], "pay") {
		t.Errorf("no section with the list:\n%s", view)
	}
	if strings.Count(view, "Notes") > 1 { // folded under More: no row, no section
		t.Error("an empty rich field got a section")
	}
}
