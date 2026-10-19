package ui

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cornedor/laneway/internal/editor"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// The Jira comment composer: a modal multi-line input for adding a comment to
// the open issue (c) or replying to one (R, via the reply-target picker in
// jira_edit.go). ctrl+s posts, enter inserts a newline, esc cancels — and
// it is fully modal: it owns
// every keystroke while open (dispatched in update.go before the focus-based
// routing) and overlays the screen (view.go), like the field pickers. A
// confirmed post goes through internal/jira, reusing the jiraMutated path so the
// panel refetches and the new comment shows. A reply prefills an editable quote
// of the original and pings its author with a real ADF mention (a plain "@name"
// would not notify).

// commentQuoteMaxLines caps how much of the original comment a reply quotes, so
// a long comment doesn't bury the composer.
const commentQuoteMaxLines = 8

// newCommentTextarea is the shared modal composer, seeded with Jira's
// placeholder. See modalcomposer.go for the box it is drawn in.
func newCommentTextarea() editor.Model {
	return newModalComposer(i18n.T("comment…"))
}

// openJiraCommentInput opens an empty composer for a new top-level comment on
// the shown issue.
func (m *Model) openJiraCommentInput() { m.openJiraCommentInputFor(m.jiraIssue.Key) }

// openJiraCommentInputFor opens the composer on key, shown or not (the
// inbox's): not shown, it opens over the screen.
func (m *Model) openJiraCommentInputFor(key string) {
	m.jiraCommentActive = true
	m.jiraCommentKey = key
	m.jiraCommentMention = nil
	m.jiraCommentReplyTo, m.jiraCommentReplyID = "", ""
	m.jiraCommentInput = newCommentTextarea()
	m.jiraCommentBefore, m.jiraCommentDiscard = "", false
	m.jiraCommentVis = m.commentVisDefault(key)
	if m.unsent.key == m.jiraCommentKey && m.unsent.text != "" {
		m.jiraCommentInput.SetValue(m.unsent.text)
		m.jiraCommentInput.CursorEnd()
		if m.unsent.maybe != nil {
			m.jiraCommentMaybe = m.unsent
		}
		m.unsent = unsentComment{}
		m.status = i18n.T("your unsent comment is back")
	} else if d, ok := m.draft(commentDraft(m.jiraCommentKey)); ok {
		m.jiraCommentInput.SetValue(d.Text)
		m.jiraCommentInput.CursorEnd()
		m.status = i18n.Tf("your draft from %s is back · esc twice drops it", draftWhen(d.At, time.Now()))
	}
}

// commentInline is whether the composer sits in the panel: under the comment
// it replies to, else after the activity.
func (m *Model) commentInline() bool {
	return m.jiraCommentActive && m.refOpen && m.jiraIssue != nil && m.jiraIssue.Key == m.jiraCommentKey && m.refErr == nil && !m.refLoading
}

// panelComposing is whether an editor is open in the panel's body.
func (m *Model) panelComposing() bool { return m.descEditInline() || m.commentInline() }

// commentMarkLine is an editor's stand-in in the thread (the composer's or
// a comment edit's mark), indented by depth reply bars.
func (m *Model) commentMarkLine(mark string, depth int) string {
	m.commentIndent = depth
	return mark + "\n"
}

// openJiraReply opens the composer prefilled with an editable quote of c and
// arranged to @mention its author, so the post reads as (and notifies like) a
// reply. The user is free to trim the quote or change the text before posting.
func (m *Model) openJiraReply(c jira.Comment) { m.openJiraReplyFor(m.jiraIssue.Key, c) }

// openJiraReplyFor is openJiraReply on key's comment c, shown or not.
func (m *Model) openJiraReplyFor(key string, c jira.Comment) {
	m.jiraCommentActive = true
	m.jiraCommentKey = key
	m.jiraCommentReplyTo, m.jiraCommentReplyID = c.Author, c.ID
	if c.AuthorID != "" {
		m.jiraCommentMention = &jira.Mention{AccountID: c.AuthorID, DisplayName: c.Author}
	} else {
		m.jiraCommentMention = nil
	}
	ta := newCommentTextarea()
	ta.SetValue(replyQuote(c))
	ta.CursorEnd()
	m.jiraCommentInput = ta
	m.jiraCommentBefore, m.jiraCommentDiscard = ta.Value(), false
	m.jiraCommentVis = m.commentVisDefault(key)
}

// commentVisDefault is who a new comment on key is for: an internal note
// on a service desk's request, so nothing reaches a customer by
// accident; everyone elsewhere.
func (m *Model) commentVisDefault(key string) jira.Visibility {
	if m.deskComment(key) {
		return jira.Visibility{Internal: true}
	}
	return jira.Visibility{}
}

// replyQuote builds the editable reply seed: a markdown blockquote of c's
// author + body (capped), then a blank line the cursor lands on.
func replyQuote(c jira.Comment) string {
	author := c.Author
	if author == "" {
		author = "comment"
	}
	var b strings.Builder
	b.WriteString("> " + author + " wrote:\n")
	lines := strings.Split(strings.TrimSpace(c.Body), "\n")
	for i, ln := range lines {
		if i >= commentQuoteMaxLines {
			b.WriteString("> …\n")
			break
		}
		b.WriteString("> " + ln + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// closeJiraComment tears the composer down.
func (m *Model) closeJiraComment() {
	m.jiraCommentActive = false
	m.jiraCommentVis = jira.Visibility{}
	m.jiraCommentKey = ""
	m.jiraCommentMention = nil
	m.jiraCommentReplyTo, m.jiraCommentReplyID = "", ""
	m.jiraCommentInput = editor.Model{}
	m.jiraCommentBefore, m.jiraCommentDiscard = "", false
	m.jiraCommentMaybe = unsentComment{}
	m.jiraCommentMentions = nil
	m.jiraMention = mentionState{seq: m.jiraMention.seq + 1}
}

// handleJiraCommentKey owns every keystroke while the composer is open: esc
// cancels (asking once when you wrote something), ctrl+s or ctrl+enter
// posts, enter inserts a newline (bound on the textarea), everything else edits the text.
func (m Model) handleJiraCommentKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.mentionKey(msg.String()) { // an open list takes its keys first
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		if strings.TrimSpace(m.jiraCommentInput.Value()) != strings.TrimSpace(m.jiraCommentBefore) && !m.jiraCommentDiscard {
			m.jiraCommentDiscard = true
			m.status = i18n.T("esc again discards your comment · ctrl+s posts it")
			return m, nil
		}
		if m.jiraCommentReplyTo == "" {
			m.dropDraft(commentDraft(m.jiraCommentKey))
		}
		m.closeJiraComment()
		m.status = ""
		return m, nil
	case "ctrl+s", "ctrl+enter":
		return m.applyJiraComment()
	case "ctrl+o":
		return m, m.cycleCommentVis()
	}
	m.jiraCommentDiscard = false
	var cmd tea.Cmd
	m.jiraCommentInput, cmd = m.jiraCommentInput.Update(msg)
	return m, tea.Batch(cmd, m.scheduleMention(), m.scheduleDraftSave())
}

// applyJiraComment closes the composer and posts the comment (or reply). An
// empty body with no mention is treated as a cancel. A comment brought
// back after a post that may have landed is looked for first, so it is
// not posted twice.
func (m Model) applyJiraComment() (tea.Model, tea.Cmd) {
	key := m.jiraCommentKey
	text := strings.TrimSpace(m.jiraCommentInput.Value())
	mention, inline, vis, parent := m.jiraCommentMention, m.jiraCommentMentions, m.jiraCommentVis, m.replyParent()
	var maybe json.RawMessage
	if m.jiraCommentMaybe.text == text {
		maybe = m.jiraCommentMaybe.maybe
	}
	m.saveOpenDrafts() // until Jira has it
	m.closeJiraComment()
	if text == "" && mention == nil {
		return m, nil
	}
	client, ctx := m.jiraClient, m.ctx
	m.status = i18n.Tf("posting comment on %s…", key)
	if mention != nil {
		m.status = i18n.Tf("posting reply to %s…", key)
	}
	return m, func() tea.Msg {
		msg := jiraMutatedMsg{key: key, field: "comment", text: text}
		if maybe != nil {
			found, err := client.HasComment(ctx, key, maybe)
			if err != nil || found {
				msg.err, msg.found, msg.maybe = err, found, maybe
				return msg
			}
		}
		doc := jira.CommentADF(text, mention, inline)
		msg.err = client.PostComment(ctx, key, doc, vis, parent)
		if jira.TimedOut(msg.err) {
			msg.maybe = doc
		}
		return msg
	}
}

// replyParent is the comment a reply goes under in Jira's thread (a reply's
// own parent: threads are one level deep); "" for a comment, with
// ui.threaded_replies off or on a service desk's request (a new comment,
// quoting it).
func (m *Model) replyParent() string {
	if !m.opts.threaded || m.jiraCommentReplyID == "" || m.deskComment(m.jiraCommentKey) {
		return ""
	}
	if m.jiraIssue != nil && m.jiraIssue.Key == m.jiraCommentKey {
		return jira.ThreadRoot(m.jiraIssue.Comments, m.jiraCommentReplyID)
	}
	return m.jiraCommentReplyID
}

// renderJiraCommentInput draws the modal composer, with a "replying to" line in
// reply mode. The box itself is the shared one (modalcomposer.go) — the diff
// view's inline note uses the same frame.
func (m *Model) renderJiraCommentInput() string {
	if !m.jiraCommentActive {
		return ""
	}
	titleTxt := i18n.Tf("Comment — %s", m.jiraCommentKey)
	var above []string
	if m.jiraCommentReplyTo != "" {
		titleTxt = i18n.Tf("Reply — %s", m.jiraCommentKey)
	}
	if v := m.jiraCommentVis; v != (jira.Visibility{}) {
		titleTxt += " · " + v.Label()
	}
	if m.jiraCommentReplyTo != "" {
		above = append(above, lipgloss.NewStyle().Foreground(dimColor).Italic(true).
			Render(i18n.Tf("↩ replying to %s", m.jiraCommentReplyTo)))
	}
	if l := m.deskCommentLine(); l != "" {
		above = append(above, l)
	}
	return m.renderModalComposer(titleTxt, above, i18n.T("ctrl+s post · @ mention · : emoji · ctrl+o who sees it · esc cancel"), &m.jiraCommentInput)
}

// commentVisMsg is who a comment in project can be limited to.
type commentVisMsg struct {
	project string
	vis     []jira.Visibility
	err     error
}

// cycleCommentVis steps who the comment is for: everyone, an internal
// note (Service Desk), each role and group the user is in; on a service
// desk's request an internal note, a reply to the customer, then the
// roles and groups. The first press asks Jira.
func (m *Model) cycleCommentVis() tea.Cmd {
	if m.replyParent() != "" {
		m.status = i18n.T("a reply is for whoever its comment is for")
		return nil
	}
	project := issueProject(m.jiraCommentKey)
	opts, ok := m.commentVis[project]
	if !ok {
		m.status = i18n.T("asking who a comment can be for…")
		c, ctx := m.jiraClient, m.ctx
		return func() tea.Msg {
			vis, err := c.CommentVisibilities(ctx, project)
			return commentVisMsg{project, vis, err}
		}
	}
	all := append([]jira.Visibility{{}}, opts...)
	if m.deskComment(m.jiraCommentKey) {
		all = deskVisibilities(opts)
	}
	i := slices.Index(all, m.jiraCommentVis)
	m.jiraCommentVis = all[(i+1)%len(all)]
	m.status = i18n.Tf("the comment is for %s", m.jiraCommentVis.Label())
	return nil
}

func (m Model) handleCommentVis(msg commentVisMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.fail(i18n.Tf("who can see comments: %s", msg.err.Error()))
		return m, nil
	}
	if m.commentVis == nil {
		m.commentVis = map[string][]jira.Visibility{}
	}
	m.commentVis[msg.project] = msg.vis
	if len(msg.vis) == 0 && !m.desks[msg.project] {
		m.status = i18n.Tf("comments in %s are for everyone: you're in no role or group to limit them to", msg.project)
		return m, nil
	}
	if m.jiraCommentActive && issueProject(m.jiraCommentKey) == msg.project {
		return m, m.cycleCommentVis()
	}
	return m, nil
}

// deleteComment deletes comment i of the panel issue; u posts it again.
func (m *Model) deleteComment(i int) tea.Cmd {
	key, id, c, ctx := m.jiraIssue.Key, m.jiraIssue.Comments[i].ID, m.jiraClient, m.ctx
	m.undoDeleteComment(key, m.jiraIssue.Comments[i])
	m.status = i18n.T("deleting the comment…")
	return jiraMutateCmd(key, "comment deleted", func() error { return c.DeleteComment(ctx, key, id) })
}

// clickCommentAction does the action at column col of comment i's action
// row.
func (m Model) clickCommentAction(i, col int) (tea.Model, tea.Cmd) {
	if m.jiraIssue == nil || i < 0 || i >= len(m.jiraIssue.Comments) {
		return m, nil
	}
	return m.commentAction(i, m.commentActionAt(m.jiraIssue.Comments[i], col))
}

// commentAction does act on comment i: reply, edit, or delete, which the
// same click or key again confirms; enter edits your own, replies to
// others'.
func (m Model) commentAction(i int, act string) (tea.Model, tea.Cmd) {
	c := m.jiraIssue.Comments[i]
	own := len(m.commentActions(c)) > 1
	if act == "enter" {
		act = "reply"
		if own {
			act = "edit"
		}
	}
	confirm := m.commentDelete == c.ID
	m.commentDelete = ""
	switch {
	case act == "reply":
		m.openJiraReply(c)
	case act == "edit" && own:
		return m, m.editComment(i)
	case act == "delete" && !own:
		m.status = i18n.T("only your own comments can be deleted")
	case act == "delete" && confirm:
		return m, m.deleteComment(i)
	case act == "delete":
		m.commentDelete = c.ID
		m.status = i18n.T("delete again to delete the comment")
	}
	m.renderRef()
	return m, nil
}

// selectedComment is the comment } and { selected, ok false when none on
// the panel's issue.
func (m *Model) selectedComment() (int, bool) {
	iss := m.jiraIssue
	if iss == nil || m.commentCursorKey != iss.Key || m.commentCursor < 0 || m.commentCursor >= len(iss.Comments) {
		return 0, false
	}
	return m.commentCursor, true
}

// moveCommentCursor selects the next (d 1) or previous (-1) comment in the
// thread's order, the Comments tab showing, and scrolls to it.
func (m *Model) moveCommentCursor(d int) tea.Cmd {
	iss := m.jiraIssue
	thread := m.commentOrder(iss.Comments)
	if len(thread) == 0 {
		m.status = i18n.T("no comments")
		return nil
	}
	at := -1
	if i, ok := m.selectedComment(); ok {
		at = slices.IndexFunc(thread, func(tc threadedComment) bool { return tc.i == i })
	}
	switch {
	case at < 0 && d < 0:
		at = len(thread) - 1
	case at < 0:
		at = 0
	default:
		at = min(max(at+d, 0), len(thread)-1)
	}
	m.clearPanelField()
	m.commentCursor, m.commentCursorKey = thread[at].i, iss.Key
	var cmd tea.Cmd
	if m.activityTab != activityComments {
		cmd = m.switchActivity(activityComments)
	}
	m.renderRef()
	for line, h := range m.panelHits {
		if h.reply && h.field == m.commentCursor {
			row := visualRowsBefore(strings.Split(m.refView.GetContent(), "\n"), line, m.refView.Width())
			if top := m.refView.YOffset(); row < top || row+3 >= top+m.refView.Height() {
				m.refView.SetYOffset(max(row-2, 0))
			}
		}
	}
	return cmd
}
