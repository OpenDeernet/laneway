package ui

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cornedor/laneway/internal/editor"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// E in the panel edits the description as markdown in the in-app editor
// (ctrl+s saves, ctrl+e hands it to $VISUAL / $EDITOR). Only a description
// markdown can carry both ways is offered (see jira.EditableDescription);
// saving it unchanged writes nothing. Rich-text fields and your comments
// edit the same way.
//
// A description or field is saved only when Jira still has the document
// the editor opened on (jira.SetDoc). When someone changed it meanwhile,
// nothing is written and the editor opens again on your text: ctrl+r
// swaps it with Jira's, ctrl+s saves what shows over it.

// descEdit is the in-app editor open on a description, field or comment.
type descEdit struct {
	key, comment, field, before string
	// base is the jira.DocBase the save expects Jira to have, "" for none.
	base    string
	kept    []json.RawMessage
	input   editor.Model
	discard bool // a first esc on changed text asked to confirm
	// other is, after a refused save, the text ctrl+r swaps in: Jira's
	// while yours shows, else yours; theirs is whether Jira's shows.
	other  *docText
	theirs bool
}

// docText is a document's markdown and the blocks its placeholders keep.
type docText struct {
	md   string
	kept []json.RawMessage
}

// descLoadedMsg is the description fetched for editing.
type descLoadedMsg struct {
	key     string
	comment string // the comment being edited, "" for the description
	field   string // or the multi-line field being edited
	md      string
	// kept are the blocks the markdown holds as placeholder lines.
	kept []json.RawMessage
	base string // jira.DocBase of the document, "" for a comment
	err  error
}

// descEditedMsg is the editor closed on path.
type descEditedMsg struct {
	key, comment, field, path, before, base string
	kept                                    []json.RawMessage
	err                                     error
}

// descConflictMsg is a save refused because the document changed in Jira
// (jira.DocChangedError): mine is the text not saved, raw Jira's document.
type descConflictMsg struct {
	key, field, mine, base, path string
	kept                         []json.RawMessage
	raw                          json.RawMessage
}

// editDescription fetches the panel issue's description for the editor.
func (m *Model) editDescription() tea.Cmd {
	if m.jiraIssue == nil {
		return nil
	}
	key, c, ctx := m.jiraIssue.Key, m.jiraClient, m.ctx
	m.status = i18n.Tf("loading %s description…", key)
	return func() tea.Msg {
		raw, err := c.Description(ctx, key)
		if err != nil {
			return descLoadedMsg{key: key, err: err}
		}
		ed, err := jira.EditableDescription(raw)
		return descLoadedMsg{key: key, md: ed.Markdown, kept: ed.Kept, base: jira.DocBase(raw), err: err}
	}
}

// descEditFits is whether an editor on key can open: the panel shows it
// and nothing else is open.
func (m *Model) descEditFits(key string) bool {
	r := m.currentRef()
	return r != nil && r.jiraKey == key && m.jiraIssue != nil && m.jiraIssue.Key == key && !m.modalOpen()
}

// handleDescLoaded opens the in-app editor on the markdown, unless the
// panel moved on from the issue or something else opened meanwhile.
func (m Model) handleDescLoaded(msg descLoadedMsg) (tea.Model, tea.Cmd) {
	if !m.descEditFits(msg.key) {
		if m.status == i18n.Tf("loading %s description…", msg.key) {
			m.status = ""
		}
		return m, nil
	}
	if msg.err != nil {
		m.fail(i18n.Tf("%s: %s — edit it in Jira (o)", msg.key, msg.err.Error()))
		return m, nil
	}
	d := &descEdit{key: msg.key, comment: msg.comment, field: msg.field, before: msg.md, base: msg.base, kept: msg.kept}
	text := msg.md
	m.status = ""
	if dr, ok := m.draft(d.draftID()); ok && dr.Text != msg.md {
		text = dr.Text
		m.status = i18n.Tf("your draft from %s is back · esc twice keeps Jira's", draftWhen(dr.At, time.Now()))
		if dr.Base != "" && d.base != "" && dr.Base != d.base {
			d.base = dr.Base // saving it asks first
			m.status = i18n.Tf("your draft from %s is back, but Jira's changed since · esc twice keeps Jira's", draftWhen(dr.At, time.Now()))
		}
	}
	m.openDescEdit(d, text)
	return m, nil
}

// openDescEdit opens the editor d on text.
func (m *Model) openDescEdit(d *descEdit, text string) {
	d.input = newModalComposer("")
	d.input.MaxHeight = max(m.bodyH()-12, 6)
	d.input.SetValue(text)
	m.descEdit = d
	if m.descEditInline() {
		d.input.MaxHeight = max(m.refView.Height()-4, 6)
		m.renderRef()
		m.showInlineEditor()
	}
}

// handleDescConflict opens the editor again on the text a save didn't
// write, Jira's a ctrl+r away. When it can't open, the text waits as a
// draft whose save asks again.
func (m Model) handleDescConflict(msg descConflictMsg) (tea.Model, tea.Cmd) {
	what := cmp.Or(msg.field, "description")
	d := &descEdit{key: msg.key, field: msg.field, kept: msg.kept, base: jira.DocBase(msg.raw)}
	if msg.path != "" {
		defer os.Remove(msg.path) // the editor or the draft holds it
	}
	if !m.descEditFits(msg.key) {
		m.saveDraft(d.draftID(), msg.mine, msg.base)
		m.fail(i18n.Tf("%s %s changed in Jira since you opened it; nothing saved · your text is kept as a draft", msg.key, what))
		return m, nil
	}
	if ed, err := jira.EditableDescription(msg.raw); err == nil {
		d.before, d.other = ed.Markdown, &docText{md: ed.Markdown, kept: ed.Kept}
		m.fail(i18n.Tf("%s %s changed in Jira since you opened it; nothing saved · ctrl+r shows Jira's · ctrl+s saves yours over it", msg.key, what))
	} else {
		m.fail(i18n.Tf("%s %s changed in Jira since you opened it; nothing saved · ctrl+s saves yours over it", msg.key, what))
	}
	m.openDescEdit(d, msg.mine)
	m.saveDraft(d.draftID(), msg.mine, d.base)
	return m, nil
}

// descEditMark stands in the panel's content for the inline editor, which
// renderRef puts in after the tables and images are laid out.
const descEditMark = "\x00descedit\x00"

// descEditHint is the editor's keys.
var descEditHint = i18n.N("ctrl+s save · ctrl+e $EDITOR · esc cancel")

// descEditInline is whether the editor sits in the panel's body, in place of
// the description, field or comment it edits.
func (m *Model) descEditInline() bool {
	d := m.descEdit
	return d != nil && m.refOpen && m.jiraIssue != nil && m.jiraIssue.Key == d.key && m.refErr == nil && !m.refLoading
}

// descEditOn is whether the inline editor is on field ("" the description).
func (m *Model) descEditOn(field string) bool {
	return m.descEditInline() && m.descEdit.comment == "" && m.descEdit.field == field
}

// commentEditOn is whether the inline editor is on the comment id.
func (m *Model) commentEditOn(id string) bool {
	return m.descEditInline() && id != "" && m.descEdit.comment == id
}

// commentMark stands in the same way for the comment composer; its line
// may carry the reply's indent after it.
const commentMark = "\x00comment\x00"

// inlineEditor is the editor drawn in the panel's body, nil for none, and
// how many reply bars indent it.
func (m *Model) inlineEditor() (ed *editor.Model, indent int) {
	switch {
	case m.descEditInline():
		return &m.descEdit.input, m.commentIndent
	case m.commentInline():
		return &m.jiraCommentInput, m.commentIndent
	}
	return nil, 0
}

// placeInlineEditor swaps the mark for the inline editor, recording its
// line; a comment's composer brings its mention list and keys.
func (m *Model) placeInlineEditor(content string, width int) string {
	m.inlineLine = -1
	ed, indent := m.inlineEditor()
	if ed == nil {
		return content
	}
	mark := descEditMark
	if ed == &m.jiraCommentInput {
		mark = commentMark
	}
	lines := strings.Split(content, "\n")
	i := slices.Index(lines, mark)
	if i < 0 {
		return content
	}
	m.inlineLine = i
	ed.SetWidth(max(width-2*indent, 8))
	view := strings.Split(ed.View(), "\n")
	if mark == commentMark {
		hint := i18n.T("ctrl+s post · @ mention · : emoji · ctrl+o who sees it · esc cancel")
		if v := m.jiraCommentVis; v != (jira.Visibility{}) {
			hint = i18n.Tf("for %s", v.Label()) + " · " + hint
		}
		if l := m.deskCommentLine(); l != "" {
			view = append(view, l)
		}
		view = append(view, refDimStyle.Render(hint))
	} else if m.descEdit.comment != "" {
		view = append(view, refDimStyle.Render(i18n.T(descEditHint)))
	}
	bars := refDimStyle.Render(strings.Repeat("│ ", indent))
	for j := range view {
		view[j] = bars + view[j]
	}
	return strings.Join(slices.Concat(lines[:i], view, lines[i+1:]), "\n")
}

// inlineEditorCursor is the inline editor's cursor on screen, when it is in
// view.
func (m *Model) inlineEditorCursor() (x, y int, ok bool) {
	ed, indent := m.inlineEditor()
	if ed == nil || m.inlineLine < 0 {
		return 0, 0, false
	}
	cx, cy, ok := ed.CursorViewPos()
	if !ok {
		return 0, 0, false
	}
	row := m.inlineEditorRow() + cy - m.refView.YOffset()
	if row < 0 || row >= m.refView.Height() {
		return 0, 0, false
	}
	listW, _ := m.jiraListWidth(m.width)
	return listW + 1 + 2*indent + cx, 1 + m.crumbRows() + row, true
}

// inlineEditorRow is the editor's first row in the panel's wrapped content.
func (m *Model) inlineEditorRow() int {
	return visualRowsBefore(strings.Split(m.refView.GetContent(), "\n"), m.inlineLine, m.refView.Width())
}

// showInlineEditor scrolls the panel to keep the inline editor's cursor in
// view, a line or two above it too when the cursor allows.
func (m *Model) showInlineEditor() {
	ed, _ := m.inlineEditor()
	if ed == nil || m.inlineLine < 0 {
		return
	}
	_, cy, _ := ed.CursorViewPos()
	row, top, h := m.inlineEditorRow()+cy, m.refView.YOffset(), m.refView.Height()
	switch {
	case row < top:
		m.refView.SetYOffset(max(row-2, 0))
	case row >= top+h:
		m.refView.SetYOffset(row - h + 3) // the keys line below it too
	}
}

// descEditTitle names what the editor is on.
func (d *descEdit) title() string {
	switch {
	case d.comment != "":
		return "Comment — " + d.key
	case d.field != "":
		return "Field — " + d.key
	}
	return "Description — " + d.key
}

func (m Model) handleDescEditKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	d := m.descEdit
	if m.mentionKey(msg.String()) { // the emoji list takes its keys first
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		if (d.input.Value() != d.before || d.other != nil) && !d.discard {
			d.discard = true
			m.status = i18n.T("esc again discards your changes · ctrl+s saves")
			return m, nil
		}
		m.dropDraft(d.draftID())
		m.descEdit, m.status = nil, ""
		return m, nil
	case "ctrl+s", "ctrl+enter":
		if ff, ok := m.extraField(d.key, d.field); ok && d.comment == "" {
			ff.val = jira.Value{Text: d.input.Value()}
			if m.refuseEmpty(ff) {
				return m, nil
			}
		}
		m.dropDraft(d.draftID()) // a failed save keeps the text in a file
		m.descEdit = nil
		return m.saveDesc(descEditedMsg{key: d.key, comment: d.comment, field: d.field, before: d.before, base: d.base, kept: d.kept}, d.input.Value())
	case "ctrl+e":
		m.dropDraft(d.draftID()) // the file holds it now
		m.descEdit = nil
		return m.openExternalEditor(descLoadedMsg{key: d.key, comment: d.comment, field: d.field, md: d.input.Value(), kept: d.kept, base: d.base}, d.before)
	case "ctrl+r":
		if o := d.other; o != nil {
			md, kept := d.input.Value(), d.kept
			d.input.SetValue(o.md)
			d.kept, o.md, o.kept, d.theirs = o.kept, md, kept, !d.theirs
			m.status = i18n.T("yours · ctrl+r shows Jira's · ctrl+s saves yours over it")
			if d.theirs {
				m.status = i18n.T("Jira's · ctrl+r shows yours · ctrl+s saves this")
			}
			if m.descEditInline() {
				m.renderRef()
			}
			return m, nil
		}
	}
	d.discard = false
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return m, tea.Batch(cmd, m.scheduleMention(), m.scheduleDraftSave())
}

func (m *Model) renderDescEdit() string {
	d := m.descEdit
	return m.renderModalComposer(d.title(), nil, i18n.T("ctrl+s save · ctrl+e $EDITOR · : emoji · esc cancel"), &d.input)
}

// openExternalEditor writes md to a file and opens $VISUAL / $EDITOR on it;
// before is the text as Jira has it, to tell a change.
func (m Model) openExternalEditor(msg descLoadedMsg, before string) (tea.Model, tea.Cmd) {
	f, err := os.CreateTemp("", "laneway-"+msg.key+"-*.md")
	if err == nil {
		_, err = f.WriteString(msg.md + "\n")
		err = firstErr(err, f.Close())
	}
	if err != nil {
		m.fail(i18n.Tf("description: %s", err.Error()))
		return m, nil
	}
	m.status = i18n.Tf("editing %s description…", msg.key)
	key, comment, field, path, kept, base := msg.key, msg.comment, msg.field, f.Name(), msg.kept, msg.base
	return m, tea.ExecProcess(editorCommand(path), func(err error) tea.Msg {
		return descEditedMsg{key: key, comment: comment, field: field, path: path, before: before, base: base, kept: kept, err: err}
	})
}

// editorCommand opens path in $VISUAL, else $EDITOR, else vi. The variable
// may carry arguments ("code -w").
func editorCommand(path string) *exec.Cmd {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	args := strings.Fields(ed)
	if len(args) == 0 {
		args = []string{"vi"}
	}
	return exec.Command(args[0], append(args[1:], path)...)
}

// handleDescEdited saves the file back when it changed.
func (m Model) handleDescEdited(msg descEditedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		os.Remove(msg.path)
		m.fail(i18n.Tf("editor: %s", msg.err.Error()))
		return m, nil
	}
	b, err := os.ReadFile(msg.path)
	if err != nil {
		m.fail(i18n.Tf("description: %s", err.Error()))
		return m, nil
	}
	return m.saveDesc(msg, string(b))
}

// saveDesc writes text back when it changed. With msg.path the text came
// from that file, which goes only once Jira has it; from the in-app editor
// a failed save puts the text in a file to keep it.
func (m Model) saveDesc(msg descEditedMsg, text string) (tea.Model, tea.Cmd) {
	after := strings.TrimSpace(text)
	if after == strings.TrimSpace(msg.before) {
		if msg.path != "" {
			os.Remove(msg.path)
		}
		m.status = i18n.Tf("%s unchanged", msg.key)
		return m, nil
	}
	c, ctx, key, kept, comment, path := m.jiraClient, m.ctx, msg.key, msg.kept, msg.comment, msg.path
	keep := func(err error) error {
		switch {
		case err != nil && path == "":
			f, ferr := os.CreateTemp("", "laneway-"+key+"-*.md")
			if ferr != nil {
				return err
			}
			_, _ = f.WriteString(after + "\n")
			f.Close()
			return fmt.Errorf(i18n.T("%w — your text is kept in %s"), err, f.Name())
		case err != nil:
			return fmt.Errorf(i18n.T("%w — your text is kept in %s"), err, path)
		case path != "":
			os.Remove(path)
		}
		return nil
	}
	if comment != "" {
		m.status = i18n.Tf("saving the comment on %s…", key)
		return m, jiraMutateCmd(key, "comment", func() error { return keep(c.SetComment(ctx, key, comment, after, kept)) })
	}
	field, base, what := msg.field, msg.base, cmp.Or(msg.field, "description")
	if field != "" {
		m.status = i18n.Tf("saving %s %s…", key, field)
	} else {
		m.status = i18n.Tf("saving %s description…", key)
	}
	return m, func() tea.Msg {
		err := c.SetDoc(ctx, key, what, after, kept, base)
		if dc := (*jira.DocChangedError)(nil); errors.As(err, &dc) {
			return descConflictMsg{key: key, field: field, mine: after, base: base, path: path, kept: kept, raw: dc.Raw}
		}
		return jiraMutatedMsg{key: key, field: what, err: keep(err)}
	}
}

// openCommentPicker lists your own comments on the panel issue to edit or
// (kind jiraPickDeleteComment) delete.
func (m *Model) openCommentPicker(kind jiraPickerKind) tea.Cmd {
	if m.jiraIssue == nil {
		return nil
	}
	iss := m.jiraIssue
	title := i18n.Tf("Edit a comment on %s", iss.Key)
	if kind == jiraPickDeleteComment {
		title = i18n.Tf("Delete a comment on %s", iss.Key)
	}
	gen := m.startJiraPicker(kind, title, false)
	seq, c, ctx := m.jiraPicker.fetchSeq, m.jiraClient, m.ctx
	comments := iss.Comments
	return func() tea.Msg {
		me, err := c.Myself(ctx)
		var items []jiraPickerItem
		for i := len(comments) - 1; i >= 0; i-- { // newest first
			cm := comments[i]
			if err == nil && cm.AuthorID == me.AccountID && cm.ID != "" {
				items = append(items, jiraPickerItem{id: strconv.Itoa(i), label: commentPickerLabel(cm)})
			}
		}
		if err == nil && len(items) == 0 {
			items = []jiraPickerItem{{id: "", label: i18n.T("no comments of yours here")}}
		}
		return jiraPickerLoadedMsg{gen: gen, seq: seq, kind: kind, items: items, err: err}
	}
}

// editComment opens comment i of the panel issue in the editor.
func (m *Model) editComment(i int) tea.Cmd {
	if m.jiraIssue == nil || i < 0 || i >= len(m.jiraIssue.Comments) {
		return nil
	}
	key, cm := m.jiraIssue.Key, m.jiraIssue.Comments[i]
	return func() tea.Msg {
		ed, err := jira.EditableDescription(cm.Raw)
		return descLoadedMsg{key: key, comment: cm.ID, md: ed.Markdown, kept: ed.Kept, err: err}
	}
}

// toggleTask checks the description's nth action item, or unchecks it.
func (m *Model) toggleTask(n int) tea.Cmd {
	if m.jiraIssue == nil || n < 1 || n > len(m.descTasks) {
		return nil
	}
	key, done, total := m.jiraIssue.Key, !m.descTasks[n-1], len(m.descTasks)
	c, ctx := m.jiraClient, m.ctx
	if done {
		m.status = i18n.T("checking the action item…")
	} else {
		m.status = i18n.T("unchecking the action item…")
	}
	return jiraMutateCmd(key, "action item", func() error { return c.ToggleTask(ctx, key, n, total, done) })
}
