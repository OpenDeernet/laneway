package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/safeterm"
)

// Jira-specific half of the reference panel: fetching an issue and rendering it.
// The shared panel machinery (open/close/cycle/keys/layout) lives in ref.go;
// the inline field editors live in jira_edit.go.

// jiraLoadedMsg carries a finished background fetch. gen guards a stale result
// the user already cycled or closed past (see Model.refGen); key records which
// issue it was for.
type jiraLoadedMsg struct {
	gen   int
	key   string
	issue *jira.Issue
	err   error
}

// fetchJira fetches (and caches) an issue in the background, returning a
// jiraLoadedMsg tagged with gen.
func (m Model) fetchJira(gen int, key string) tea.Cmd {
	client := m.jiraClient
	ctx := m.ctx
	return func() tea.Msg {
		issue, err := client.Get(ctx, key)
		return jiraLoadedMsg{gen: gen, key: key, issue: issue, err: err}
	}
}

// handleJiraLoaded installs a finished fetch, unless the user has since cycled
// or closed the panel (stale gen) or moved to a non-Jira reference.
func (m Model) handleJiraLoaded(msg jiraLoadedMsg) (tea.Model, tea.Cmd) {
	r := m.currentRef()
	if !m.refOpen || msg.gen != m.refGen || r == nil {
		return m, nil
	}
	m.refLoading = false
	if iss, ok := m.indexedIssue(r.jiraKey, msg.err); ok {
		m.refErr, m.jiraIssue = nil, iss
	} else if msg.err != nil {
		m.refErr = msg.err
		m.jiraIssue = nil
	} else {
		m.refErr = nil
		m.jiraIssue = msg.issue
		m.rememberRecent(msg.issue.Key, msg.issue.Summary)
	}
	m.activity = activityState{} // a refetch reloads the history too
	activity := m.loadActivity()
	m.earlyExtra()
	m.renderRef()
	return m, tea.Batch(m.swapIssueImages(m.jiraIssue), m.fetchIssueImages(m.jiraIssue), m.fetchPanelExtra(), activity)
}

// renderJiraIssue formats one issue for the viewport: a key + type header, the
// summary, an aligned meta block, then the description rendered through the
// shared markdown renderer (so links are clickable and inline styling matches
// the message pane).
func (m *Model) renderJiraIssue(iss *jira.Issue, width int) string {
	var b strings.Builder

	header := refKeyStyle.Render(iss.Key)
	if iss.Type != "" {
		header += "  " + refDimStyle.Render(iss.Type)
	}
	if tm := m.timerMark(iss.Key); tm != "" {
		header += "  " + tm
	}
	b.WriteString(header + "\n")
	m.panelFieldLine = m.panelFieldLine[:0]
	m.pickerLine, m.commentIndent = -1, 0
	// The rows past the pinned own ones sit apart, their labels as wide as
	// the widest name.
	rows := m.panelRows()
	split := slices.IndexFunc(rows, func(r panelRow) bool { return r.own == nil })
	lw, w := panelLabelW(), panelLabelW()
	if split >= 0 {
		for _, r := range rows[split:] {
			n := len([]rune(i18n.T(r.name()))) + 2
			if r.ff != nil {
				n = len([]rune(extraLabel(*r.ff))) + 2
			}
			w = max(w, n)
		}
	}
	for i, r := range rows {
		if i == split {
			b.WriteString("\n")
		}
		m.panelFieldLine = append(m.panelFieldLine, strings.Count(b.String(), "\n"))
		sel, rw := m.panelFieldIdx() == i, lw
		if split >= 0 && i >= split {
			rw = w
		}
		switch {
		case i == 0:
			m.writeSummaryRow(&b, iss, sel, width)
		case r.more:
			_, rest := m.splitRows()
			refField(&b, i18n.T(moreFieldsName), m.moreLabel(len(rest)), rw, sel)
		case r.own != nil:
			m.writeOwnRow(&b, iss, *r.own, sel, rw, width)
		default:
			m.writeExtraRow(&b, *r.ff, sel, rw, width)
		}
	}
	if n := m.hiddenFields(); n > 0 {
		b.WriteString("\n" + refDimStyle.Render(i18n.Tf(emptyFieldsRow, n)) + "\n")
	}

	// Edit affordances: the field cursor (panel_fields.go), comments
	// (jira_comment.go).
	b.WriteString("\n" + refDimStyle.Render(m.panelHintLine()) + "\n")

	if m.descEditOn("") {
		b.WriteString(sectionHead(i18n.T("Description"), "  "+i18n.T(descEditHint), max(width, 1)))
		b.WriteString(descEditMark + "\n")
	} else {
		desc := strings.TrimSpace(iss.Description)
		b.WriteString(sectionHead(i18n.T(descHead), "  "+m.descHint(desc != ""), max(width, 1)))
		if desc != "" {
			b.WriteString(renderMarkdown(m.numberDesc(iss.Key, desc), m.emojiImg, nil, ""))
		}
	}
	// Rich-text fields read like the description, under their own heads.
	for _, ff := range m.extraFields() {
		if m.descEditOn(ff.ID) {
			b.WriteString(sectionHead(ff.Name, "  "+i18n.T(descEditHint), max(width, 1)))
			b.WriteString(descEditMark + "\n")
		} else if richField(ff) {
			b.WriteString(sectionHead(ff.Name, "", max(width, 1)))
			b.WriteString(renderMarkdown(ff.val.Text, m.emojiImg, nil, ""))
		}
	}

	m.renderNotes(&b, iss.Key, width)
	m.renderAgents(&b, iss.Key, width)
	m.renderJiraLinks(&b, iss, width)
	m.renderChildren(&b, width)
	m.renderWebLinks(&b, iss, width)
	m.renderJiraAttachments(&b, iss, width)
	m.renderJiraActivity(&b, iss, width)
	return b.String()
}

var descHead = i18n.N("Description")

// panelLabelW is the width of the panel's label column: the translated
// labels fit.
func panelLabelW() int {
	w := 10
	for _, l := range []string{i18n.T("Status"), i18n.T("Priority"), i18n.T("Points"), i18n.T("Assignee"), i18n.T("Reporter"), i18n.T("Labels"),
		i18n.T("Updated"), i18n.T("Deployed"), i18n.T("Created"), i18n.T("Resolved"), i18n.T("Watchers"), i18n.T("Votes"), i18n.T("Time")} {
		w = max(w, len([]rune(l))+2)
	}
	return w
}

// descHint is the Description heading's key: edit it, or add one.
func (m *Model) descHint(has bool) string {
	if has {
		return i18n.Tf("%s edit", helpKey(m.keys.JiraDescription))
	}
	return i18n.Tf("%s add one", helpKey(m.keys.JiraDescription))
}

// refFieldEdit writes the panel's own field row: the inline input while it
// is edited, else refField.
func (m *Model) refFieldEdit(b *strings.Builder, name, label, value string, labelW, width int, sel bool) {
	if m.fieldInlineOn(name) {
		b.WriteString(refLabelStyle.Render(refMetaLabel(label, labelW)) + m.fieldInlineView(labelW, width) + "\n")
		return
	}
	refField(b, label, value, labelW, sel)
}

// inlinePickerUnder drops the inline picker under row name when it is open
// there.
func (m *Model) inlinePickerUnder(b *strings.Builder, name string, indent, width int) {
	if m.pickerInlineOn(name) {
		m.renderInlinePicker(b, indent, width)
	}
}

// fieldInlineView is the field input fitted to the row after a label
// labelW wide.
func (m *Model) fieldInlineView(labelW, width int) string {
	m.jiraFieldInput.SetWidth(max(width-labelW-lipgloss.Width(m.jiraFieldInput.Prompt)-1, 4))
	return m.jiraFieldInput.View()
}

// richField is a filled rich-text (ADF) field, drawn as its own section
// rather than squeezed onto its row.
func richField(ff jiraFormField) bool {
	return ff.Kind == jira.KindDoc && strings.TrimSpace(ff.val.Text) != ""
}

// statusLozenge colours a status by its category, as Jira's lozenges do:
// grey to do, blue in progress, green done. Set by applyTheme.
var statusLozenge map[string]lipgloss.Style

// sectionHead opens a panel section: a shaded bar with its label and hint,
// or without shading a rule above the label.
func sectionHead(label, hint string, width int) string {
	head := refLabelStyle.Render(label) + refDimStyle.Render(hint)
	if shadeOn {
		return "\n" + bar(" "+head, max(width, 1)) + "\n"
	}
	return "\n" + refDimStyle.Render(strings.Repeat("─", max(width, 1))) + "\n" + head + "\n"
}

// renderJiraLinks lists the parent, linked issues and subtasks; L picks one
// to open.
func (m *Model) renderJiraLinks(b *strings.Builder, iss *jira.Issue, width int) {
	if len(iss.Links) == 0 {
		return
	}
	b.WriteString(sectionHead(i18n.Tf("Links (%d)", len(iss.Links)), "  L "+i18n.T("open"), width))
	for _, l := range iss.Links {
		line := refDimStyle.Render(l.Rel+" ") + jiraKeyStyle.Render(l.Key) + " " + l.Summary
		if l.Status != "" {
			line += refDimStyle.Render(" · " + l.Status)
		}
		b.WriteString(ansi.Truncate(line, max(width, 1), "…") + "\n")
	}
}

// isEpic is whether iss is an epic, whose children are not subtasks.
func (m *Model) isEpic(iss *jira.Issue) bool {
	return strings.EqualFold(iss.Type, "epic") || strings.EqualFold(iss.Type, m.opts.epicType)
}

// renderChildren lists an epic's child issues, open ones first, under a
// progress count; L picks one to open.
func (m *Model) renderChildren(b *strings.Builder, width int) {
	kids := m.shownChildren()
	if len(kids) == 0 {
		return
	}
	b.WriteString(sectionHead(childrenHead(kids), "  L "+i18n.T("open"), width))
	for _, ch := range kids {
		line := jiraKeyStyle.Render(ch.Key) + " " + ch.Summary
		meta := ch.Status
		if ch.Assignee != "" {
			meta += " · " + ch.Assignee
		}
		line += refDimStyle.Render(" · " + meta)
		if ch.Done {
			line = refDimStyle.Render(ansi.Strip(line))
		}
		b.WriteString(ansi.Truncate(line, max(width, 1), "…") + "\n")
	}
}

// childrenHead is the Children section's label: "Children (3/8 done)".
func childrenHead(kids []jira.Child) string {
	done := 0
	for _, ch := range kids {
		if ch.Done {
			done++
		}
	}
	return i18n.Tf("Children (%d/%d done)", done, len(kids))
}

// shownChildren are the panel epic's child issues, open ones first, once
// loaded.
func (m *Model) shownChildren() []jira.Child {
	if m.jiraIssue == nil || m.webLinksKey != m.jiraIssue.Key {
		return nil
	}
	kids := slices.Clone(m.children)
	slices.SortStableFunc(kids, func(a, b jira.Child) int {
		switch {
		case a.Done == b.Done:
			return 0
		case a.Done:
			return 1
		}
		return -1
	})
	return kids
}

// renderWebLinks lists the issue's remote links, each opening its page.
func (m *Model) renderWebLinks(b *strings.Builder, iss *jira.Issue, width int) {
	links := m.shownWebLinks()
	if len(links) == 0 {
		return
	}
	b.WriteString(sectionHead(i18n.Tf("Web links (%d)", len(links)), "  L "+i18n.T("open"), width))
	for _, l := range links {
		line := osc8Link(l.URL, mdLinkStyle.Render(safeterm.Line(l.Title)))
		if l.App != "" {
			line += refDimStyle.Render(" · " + safeterm.Line(l.App))
		}
		b.WriteString(ansi.Truncate(line, max(width, 1), "…") + "\n")
	}
}

// shownWebLinks are the panel issue's remote links, once loaded.
func (m *Model) shownWebLinks() []jira.WebLink {
	if m.jiraIssue == nil || m.webLinksKey != m.jiraIssue.Key {
		return nil
	}
	return m.webLinks
}

// openJiraLinkPicker lists the shown issue's links to jump to, and its web
// links to open.
func (m *Model) openJiraLinkPicker() {
	web, kids := m.shownWebLinks(), m.shownChildren()
	if m.jiraIssue == nil || len(m.jiraIssue.Links)+len(kids)+len(web) == 0 {
		m.status = i18n.T("no links")
		return
	}
	m.startJiraPicker(jiraPickLink, i18n.T("Go to a link"), true)
	var items []jiraPickerItem
	for _, l := range m.jiraIssue.Links {
		items = append(items, jiraPickerItem{id: l.Key, label: l.Rel + " " + l.Key + " " + l.Summary})
	}
	for _, ch := range kids {
		items = append(items, jiraPickerItem{id: ch.Key, label: i18n.Tf("child %s %s", ch.Key, ch.Summary)})
	}
	for _, l := range web {
		label := i18n.Tf("web %s", safeterm.Line(l.Title)) // opens in the browser; a page of this site's Confluence reads in the panel
		if m.jiraClient.PageID(l.URL) != "" {
			label = i18n.Tf("page %s", safeterm.Line(l.Title))
		}
		items = append(items, jiraPickerItem{id: l.URL, label: label, value: "web"})
	}
	m.setJiraPickerItems(items)
}

// renderJiraAttachments lists the files the description and comments don't
// already show, each a link that downloads it.
func (m *Model) renderJiraAttachments(b *strings.Builder, iss *jira.Issue, width int) {
	var rest []jira.Attachment
	for _, a := range iss.Attachments {
		if !issueShowsAttachment(iss, a.ID) {
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return
	}
	b.WriteString(sectionHead(i18n.Tf("Attachments (%d)", len(rest)), "", width))
	for _, a := range rest {
		name := attachmentStyle.Render("📎 " + a.Filename)
		if u := m.jiraClient.AttachmentURL(a.ID); u != "" {
			name = osc8Link(u, name)
		}
		b.WriteString(name + refDimStyle.Render("  "+byteSize(a.Size)) + "\n")
	}
}

// threadedComment is a comment's place in the thread: its index into the
// issue's comments and how deep a reply it is.
type threadedComment struct{ i, depth int }

// replyDepthMax caps the indent; deeper replies line up with it.
const replyDepthMax = 3

// commentThread orders comments as a thread: each top-level comment by
// date, its replies (parentId) under it, theirs under them. parentId is
// undocumented, so anything odd falls back to the flat list: a parent not
// among the loaded comments (older than the page, deleted) makes a comment
// top-level, and a loop is broken where it's found.
func commentThread(cs []jira.Comment) []threadedComment {
	at := map[string]int{}
	for i, c := range cs {
		if c.ID != "" {
			at[c.ID] = i
		}
	}
	kids := map[int][]int{}
	var roots []int
	for i, c := range cs {
		if p, ok := at[c.ParentID]; ok && c.ParentID != "" && p != i {
			kids[p] = append(kids[p], i)
		} else {
			roots = append(roots, i)
		}
	}
	out := make([]threadedComment, 0, len(cs))
	seen := make([]bool, len(cs))
	var walk func(i, depth int)
	walk = func(i, depth int) {
		if seen[i] {
			return
		}
		seen[i] = true
		out = append(out, threadedComment{i, depth})
		for _, k := range kids[i] {
			walk(k, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	for i := range cs { // in a loop, unreachable from any root: flat, in order
		walk(i, 0)
	}
	return out
}

// commentOrder is the Comments tab's order: the thread (or, with
// ui.comment_layout flat, every comment by date, none indented), and with
// ui.comment_order newest the latest thread first, its replies still under
// it oldest first.
func (m *Model) commentOrder(cs []jira.Comment) []threadedComment {
	var out []threadedComment
	if m.opts.flatComments {
		for i := range cs {
			out = append(out, threadedComment{i: i})
		}
	} else {
		out = commentThread(cs)
	}
	if !m.opts.newestFirst {
		return out
	}
	rev := make([]threadedComment, 0, len(out))
	for end := len(out); end > 0; {
		start := end - 1
		for start > 0 && out[start].depth > 0 {
			start--
		}
		rev = append(rev, out[start:end]...)
		end = start
	}
	return rev
}

// parentRef is the line above a flat reply quoting its parent: its author
// and the start of its body, cut to width; "" when the parent isn't loaded.
func parentRef(cs []jira.Comment, c jira.Comment, width int) string {
	if c.ParentID == "" {
		return ""
	}
	i := slices.IndexFunc(cs, func(p jira.Comment) bool { return p.ID == c.ParentID })
	if i < 0 {
		return ""
	}
	p := cs[i]
	who := p.Author
	if who == "" {
		who = i18n.T("Unknown")
	}
	return truncate("↪ "+who+": "+strings.Join(strings.Fields(p.Body), " "), width)
}

// indentReply puts a reply's lines behind a bar per level.
func indentReply(s string, depth int) string {
	if depth == 0 {
		return s
	}
	bar := refDimStyle.Render(strings.Repeat("│ ", min(depth, replyDepthMax)))
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = bar + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// byteSize is n bytes for people: 512 B, 3.4 KB, 12 MB.
func byteSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 10<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d MB", n>>20)
}

// renderJiraComments writes the Comments tab in commentOrder, each a dim
// author·timestamp line and its markdown body. When the issue has more
// comments than the inline field returned, a trailing note points at the
// browser.
func (m *Model) renderJiraComments(b *strings.Builder, iss *jira.Issue, width int) {
	if len(iss.Comments) == 0 && iss.CommentTotal == 0 {
		b.WriteString(refDimStyle.Render(i18n.T("no comments yet")) + "\n")
		return
	}
	thread := m.commentOrder(iss.Comments)
	for n, tc := range thread {
		if m.opts.flatComments {
			if ref := parentRef(iss.Comments, iss.Comments[tc.i], width); ref != "" {
				b.WriteString(refDimStyle.Render(ref) + "\n")
			}
		}
		var cb strings.Builder
		if c := iss.Comments[tc.i]; m.commentEditOn(c.ID) {
			// Your comment's edit replaces its body, under its byline.
			b.WriteString(indentReply(refDimStyle.Render(m.commentByline(c))+"\n", tc.depth))
			b.WriteString(m.commentMarkLine(descEditMark, min(tc.depth, replyDepthMax)))
		} else {
			m.renderComment(&cb, c)
		}
		bars := strings.Repeat("│ ", min(tc.depth, replyDepthMax))
		head := commentHead{i: tc.i, text: bars + m.commentByline(iss.Comments[tc.i])}
		if !m.commentEditOn(iss.Comments[tc.i].ID) {
			head.acts = m.commentActionLine(iss.Comments[tc.i])
			if bars != "" { // the gutter's spaces sit after the bars, not trimmed
				head.acts, head.bar = bars+"  "+head.acts, ansi.StringWidth(bars)+2
			}
		}
		m.commentHeads = append(m.commentHeads, head)
		b.WriteString(indentReply(cb.String(), tc.depth))
		if c := iss.Comments[tc.i]; m.commentInline() && c.ID != "" && c.ID == m.jiraCommentReplyID {
			if !strings.HasSuffix(b.String(), "\n") {
				b.WriteString("\n")
			}
			b.WriteString(m.commentMarkLine(commentMark, min(tc.depth+1, replyDepthMax)))
		}
		if n < len(thread)-1 {
			b.WriteString("\n")
		}
	}

	if extra := iss.CommentTotal - len(iss.Comments); extra > 0 {
		b.WriteString("\n" + refDimStyle.Render(i18n.Tf(moreLinksRow, extra)) + "\n")
	}
}

// commentByline is a comment's first line: its author, time and, when
// not everyone may read it, a lock with who may.
func (m *Model) commentByline(c jira.Comment) string {
	line := c.Author
	if line == "" {
		line = i18n.T("Unknown")
	}
	if !c.Created.IsZero() {
		line += " · " + m.when(c.Created)
	}
	if c.Visibility != (jira.Visibility{}) {
		line += " · 🔒 " + c.Visibility.Label()
	}
	return line
}

// renderComment writes one comment: author and time, its body, and the
// row of its actions.
func (m *Model) renderComment(b *strings.Builder, c jira.Comment) {
	by := refDimStyle
	if i, ok := m.selectedComment(); ok && m.jiraIssue.Comments[i].ID == c.ID {
		by = selectedRow
	}
	b.WriteString(by.Render(m.commentByline(c)) + "\n")
	if body := strings.TrimSpace(c.Body); body != "" {
		b.WriteString(strings.TrimSuffix(renderMarkdown(body, m.emojiImg, nil, ""), "\n") + "\n")
	}
	b.WriteString("  " + refDimStyle.Render(m.commentActionLine(c)) + "\n") // in the body's gutter
}

// commentActions are what a click on comment c's action row can do: reply,
// and on your own edit and delete. Each is its label and action.
func (m *Model) commentActions(c jira.Comment) [][2]string {
	acts := [][2]string{{i18n.T("↩ reply"), "reply"}}
	if me := m.jiraClient.KnownMyself(); me != "" && c.AuthorID == me && c.ID != "" {
		del := i18n.T("✕ delete")
		if m.commentDelete == c.ID {
			del = i18n.T("✕ delete? again")
		}
		acts = append(acts, [2]string{i18n.T("✎ edit"), "edit"}, [2]string{del, "delete"})
	}
	return acts
}

// commentActionLine is comment c's action row.
func (m *Model) commentActionLine(c jira.Comment) string {
	var labels []string
	for _, a := range m.commentActions(c) {
		labels = append(labels, a[0])
	}
	return strings.Join(labels, " · ")
}

// commentActionAt is the action at column col of comment c's action row,
// "" between them.
func (m *Model) commentActionAt(c jira.Comment, col int) string {
	acts := m.commentActions(c)
	if i, _, _ := labelAt(firsts(acts), col); i >= 0 {
		return acts[i][1]
	}
	return ""
}

// writeSummaryRow writes the summary: the panel's title, an input while
// it is edited.
func (m *Model) writeSummaryRow(b *strings.Builder, iss *jira.Issue, sel bool, width int) {
	switch {
	case m.fieldInlineOn("Summary"):
		b.WriteString(m.fieldInlineView(0, width) + "\n")
	case sel:
		b.WriteString(selectedRow.Render(orDash(iss.Summary)) + "\n")
	case iss.Summary != "":
		b.WriteString(titleStyle.Render(iss.Summary) + "\n")
	}
	b.WriteString("\n")
}

// writeOwnRow writes one of the panel's own field rows, with its inline
// editor when open.
func (m *Model) writeOwnRow(b *strings.Builder, iss *jira.Issue, f panelField, sel bool, lw, width int) {
	label := i18n.T(f.name)
	switch f.id {
	case "status":
		if st, ok := statusLozenge[iss.StatusCategory]; ok && iss.Status != "" && !sel {
			b.WriteString(refLabelStyle.Render(refMetaLabel(label, lw)) + st.Render(" "+strings.ToUpper(iss.Status)+" ") + "\n")
		} else {
			refField(b, label, iss.Status, lw, sel)
		}
	case "points", "labels":
		m.refFieldEdit(b, f.name, label, m.ownValue(f.id), lw, width, sel)
		if f.id == "labels" && m.jiraFieldActive && m.jiraFieldName == "labels" && m.fieldInline() {
			for _, l := range m.labelLines(10) {
				b.WriteString(l + "\n")
			}
		}
	default:
		refField(b, label, m.ownValue(f.id), lw, sel)
	}
	m.inlinePickerUnder(b, f.name, lw, width)
}

// writeExtraRow writes an editmeta field's row, with its inline editor when
// open.
func (m *Model) writeExtraRow(b *strings.Builder, ff jiraFormField, sel bool, w, width int) {
	val := jiraValueText(ff.val)
	if richField(ff) {
		val = i18n.T("↓ below")
	}
	if m.fieldInlineOn(ff.ID) {
		b.WriteString(refLabelStyle.Render(refMetaLabel(extraLabel(ff), w)) + m.fieldInlineView(w, width) + "\n")
		if ff.Clause != "" {
			for _, l := range m.labelLines(w) {
				b.WriteString(l + "\n")
			}
		}
		return
	}
	refField(b, extraLabel(ff), val, w, sel)
	m.inlinePickerUnder(b, ff.ID, w, width)
}

// extraLabel is an editmeta field's row label: its name, marked * where the
// field configuration requires it, as the create form marks one.
func extraLabel(ff jiraFormField) string {
	if ff.required && !ff.ReadOnly {
		return ff.Name + " *"
	}
	return ff.Name
}

// ownValue is the shown issue's value of the panel's own field id, "" when
// it has none. The read-only details past Updated come with editmeta; the
// deployment rides on the board's card (its Development field).
func (m *Model) ownValue(id string) string {
	iss := m.jiraIssue
	if iss == nil {
		return ""
	}
	f := m.panelFacts
	if m.panelExtraKey != iss.Key {
		f = jira.Facts{}
	}
	count := func(n int, you bool) string {
		switch {
		case n == 0:
			return ""
		case you:
			return i18n.Tf("%d (you)", n)
		}
		return strconv.Itoa(n)
	}
	switch id {
	case "status":
		return iss.Status
	case "priority":
		return iss.Priority
	case "points":
		return iss.StoryPoints
	case "assignee":
		return iss.Assignee
	case "reporter":
		return iss.Reporter
	case "labels":
		return strings.Join(iss.Labels, ", ")
	case "updated":
		if !iss.Updated.IsZero() {
			return m.when(iss.Updated)
		}
	case "created":
		if !f.Created.IsZero() {
			return m.when(f.Created)
		}
	case "resolution":
		res := f.Resolution
		if res != "" && !f.Resolved.IsZero() {
			res += " · " + m.when(f.Resolved)
		}
		return res
	case "watches":
		return count(f.Watchers, f.Watching)
	case "votes":
		return count(f.Votes, f.Voted)
	case "timetracking":
		if f.Spent <= 0 && f.Estimate <= 0 && f.Left <= 0 {
			return ""
		}
		t := i18n.T("nothing logged")
		if f.Spent > 0 {
			t = i18n.Tf("%s logged", jira.FormatDuration(f.Spent))
		}
		if f.Left > 0 || f.Estimate > 0 {
			t += i18n.Tf(" · %s left", jira.FormatDuration(f.Left))
		}
		if f.Estimate > 0 {
			t += i18n.Tf(" of %s", jira.FormatDuration(f.Estimate))
		}
		return t
	case "deployed":
		if i := slices.IndexFunc(m.jiraTab.cards, func(c jira.Card) bool { return c.Key == iss.Key }); i >= 0 {
			return m.jiraTab.cards[i].Deploy
		}
	}
	return ""
}

// moreLabel is the More row's value: how many it folds, and how to open it.
func (m *Model) moreLabel(n int) string {
	if m.moreFields {
		return i18n.Tf("▾ %d · %s to pin/unpin", n, helpKey(m.keys.Pin))
	}
	return i18n.Tf("▸ %d · ↵ shows them", n)
}

// emptyFieldsRow stands for the empty fields ui.empty_fields: hide folds;
// a click (or the palette) shows them.
var emptyFieldsRow = i18n.N("%d empty fields · a click shows them")

// moreLinksRow ends a list cut short.
var moreLinksRow = i18n.N("…and %d more — o opens in browser")
