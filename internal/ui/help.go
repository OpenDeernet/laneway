package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/i18n"
)

// The ? overlay: every key of the board and the panel, as bound (ui.keys
// rebinds them), paged to the screen's width. Any other key closes it.

type helpRow struct {
	keys string
	desc string
}

func row(b key.Binding, desc string) helpRow { return helpRow{keysLabel(b), desc} }

func (m *Model) helpSections() []struct {
	title string
	rows  []helpRow
} {
	k := m.keys
	join := func(a, b key.Binding) string { return helpKey(a) + " / " + helpKey(b) }
	sections := []struct {
		title string
		rows  []helpRow
	}{
		{i18n.T("Board"), []helpRow{
			{join(k.Up, k.Down) + "  " + join(k.Left, k.Right), i18n.T("move")},
			{join(k.Home, k.End) + "  " + join(k.PageUp, k.PageDown), i18n.T("top / bottom, a page")},
			row(k.OpenChannel, i18n.T("open issue")),
			row(k.OpenRef, i18n.T("show / hide the panel")),
			row(k.Goto, i18n.T("go to issue by key")),
			row(k.Create, i18n.T("new issue")),
			row(k.OpenAttach, i18n.T("open in browser")),
			{join(k.CopyKey, k.CopyURL), i18n.T("copy key / URL; marked: table")},
			row(k.CopyBranch, i18n.T("copy branch name")),
			{join(k.Project, k.Board), i18n.T("project / board")},
			{join(k.PrevView, k.NextView), i18n.T("previous / next view")},
			row(k.ToggleMode, i18n.T("lanes / list")),
			row(k.Compact, i18n.T("one-line cards / full")),
			row(k.EmptyLanes, i18n.T("hide / show empty lanes")),
			row(k.LaneLayout, i18n.T("next lane layout: the board's columns or a ui.lane_layouts entry")),
			row(k.ArrangeLanes, i18n.T("arrange the lanes: stack, split, move, rename, hide columns")),
			row(k.MergeRequests, i18n.T("merge requests waiting on you, every GitLab")),
			row(k.Palette, i18n.T("command palette")),
			row(k.JQL, i18n.T("JQL search with completion, as a view")),
			row(k.StartScreen, i18n.T("home: my work, inbox, sprint, timer, saved searches (ui.home starts on it)")),
			row(k.MyWork, i18n.T("my work: yours in every project, by status")),
			{join(k.Timer, k.Timesheet), i18n.T("timer / today's worklogs")},
			row(k.Inbox, i18n.T("inbox: others' news on your issues, a thread each")),
			row(k.Agents, i18n.T("agents: every herdr agent by state, its terminal beside the list")),
			row(k.Standup, i18n.T("standup: what you did since the last workday")),
			row(k.Charts, i18n.T("sprint charts (y copies the numbers)")),
			row(k.TimeMachine, i18n.T("time machine: ← → the board a day earlier / later, esc now")),
			row(k.ClosedSprint, i18n.T("closed sprints: one as it closed, and what carried over")),
			row(k.Releases, i18n.T("releases: versions, done / total; enter lists one, ↳ releases it")),
			row(k.Plan, i18n.T("sprint planning: backlog beside a sprint (K J rank)")),
			row(k.Roadmap, i18n.T("roadmap: epics on a timeline (space children, H L < > e dates, y copy)")),
			row(k.Sort, i18n.T("sort the list; lanes: swimlanes")),
			{join(k.Fold, k.UnfoldAll), i18n.T("fold the swimlane or stacked lane's section / unfold all")},
			{join(k.PrevBand, k.NextBand), i18n.T("previous / next swimlane, or the list's group")},
			{join(k.MoveCardLeft, k.MoveCardRight), i18n.T("move card a lane")},
			{join(k.RankUp, k.RankDown), i18n.T("rank up / down in its lane")},
			{join(k.RankTop, k.RankBottom), i18n.T("rank to the top / bottom of its lane")},
			row(k.Undo, i18n.T("undo the last move, rank, band drop or edit (fields, bulk, sprint, a deleted comment); again, the one before")),
			row(k.Refine, i18n.T("refine: the view's issues one at a time in the panel, unestimated first (J K, esc)")),
			row(k.Repeat, i18n.T("do the last move or quick / bulk edit again on the selected card")),
			row(k.MoveSprint, i18n.T("move to sprint / backlog")),
			row(k.QuickEdit, i18n.T("quick edit: status, priority, assignee, points…")),
			{join(k.Mark, k.Bulk), i18n.T("mark card / edit marked (esc clears)")},
			row(k.MarkAll, i18n.T("mark the lane / every row")),
			row(k.Pin, i18n.T("pin: ★, first in the palette")),
			row(k.Search, i18n.T("search (esc clears)")),
			row(k.FilterBuilder, i18n.T("filter builder: field, compare, value side by side → search")),
			{join(k.Assignee, k.Mine), i18n.T("assignee filter / mine")},
			{"1-9 / " + helpKey(k.ClearFilters), i18n.T("quick filter / clear")},
			row(k.Refresh, i18n.T("refresh")),
			row(k.Tab, i18n.T("to panel")),
			row(k.Site, i18n.T("switch or add a Jira site")),
			row(k.Settings, i18n.T("settings: every ui: option, editable")),
			{join(k.PanelWider, k.PanelNarrower), i18n.T("widen / narrow the panel")},
			row(k.Quit, i18n.T("quit")),
		}},
		{i18n.T("Panel"), []helpRow{
			row(k.JiraStatus, i18n.T("status")),
			row(k.JiraPriority, i18n.T("priority")),
			row(k.JiraPoints, i18n.T("story points")),
			row(k.JiraSummary, i18n.T("edit summary")),
			row(k.JiraLabels, i18n.T("edit labels")),
			row(k.JiraDescription, i18n.T("edit description (ctrl+e: $EDITOR)")),
			row(k.JiraAssignee, i18n.T("assignee")),
			{join(k.JiraComment, k.JiraReply), i18n.T("comment / reply")},
			{join(k.NextComment, k.PrevComment), i18n.Tf("select a comment: R reply, ↵ edit yours, %s twice delete", helpKey(k.DeleteComment))},
			{join(k.LogWork, k.Timer), i18n.T("log work / timer")},
			row(k.Undo, i18n.T("undo the last edit")),
			row(k.Timesheet, i18n.T("today's worklogs")),
			row(k.Inbox, i18n.T("inbox")),
			row(k.Agents, i18n.T("agents")),
			row(k.IssueActions, i18n.T("subtask / child, link, clone, change type, move, delete, watchers, agents")),
			row(k.Notes, i18n.T("private notes, on this machine ($EDITOR)")),
			row(k.Ask, i18n.T("ask ui.llm: summary, acceptance criteria, subtasks, points")),
			{join(k.PrevView, k.NextView), i18n.T("activity: comments, history, work log, all")},
			row(k.History, i18n.T("history: changes and comments")),
			row(k.DevInfo, i18n.T("pull requests, builds, deploys, branches, commits")),
			row(k.Pin, i18n.T("pin: first in the palette")),
			row(k.JiraStart, i18n.T("start work, or attach to its agent")),
			row(k.AgentBack, i18n.T("from the agent in the panel back to its issue")),
			row(k.OpenAttach, i18n.T("open in browser")),
			{join(k.CopyKey, k.CopyURL), i18n.T("copy key / URL")},
			row(k.CopyBranch, i18n.T("copy branch name")),
			row(k.JiraLinks, i18n.T("go to linked issue, child or web link")),
			row(k.Search, i18n.T("find in the issue (n / N next / previous)")),
			row(k.Image, i18n.T("view images full size")),
			row(k.Back, i18n.T("previous issue")),
			row(k.Refresh, i18n.T("refresh")),
			row(k.Palette, i18n.T("command palette")),
			{join(k.PanelWider, k.PanelNarrower), i18n.T("widen / narrow the panel")},
			{join(k.Tab, k.ShiftTab), i18n.T("walk fields, then to board")},
			{"enter", i18n.T("edit selected field")},
			{"esc", i18n.T("drop field, close")},
		}},
	}
	type section = struct {
		title string
		rows  []helpRow
	}
	sections = append(sections,
		section{i18n.T("Forms"), []helpRow{
			{"tab / ↓ / ctrl+n", i18n.T("next field, keeping what you typed")},
			{"shift+tab / ↑ / ctrl+p", i18n.T("previous field")},
			{"↑ ↓ in a description", i18n.T("move lines; leave at the top / bottom")},
			{"enter", i18n.T("edit the field; on the summary, create")},
			{"ctrl+s", i18n.T("save the form")},
			{"esc", i18n.T("undo the field, then close")},
		}},
		section{i18n.T("Planning"), []helpRow{
			{join(k.Left, k.Right), i18n.T("switch side")},
			{join(k.PrevView, k.NextView), i18n.T("the sprint on the right")},
			row(k.Mark, i18n.T("mark a card")),
			{helpKey(k.MoveSprint) + " / space", i18n.T("move the marked (or the card) across")},
			{join(k.RankUp, k.RankDown), i18n.T("rank up / down")},
			row(k.PlanStart, i18n.T("start the sprint / move its end")),
			row(k.PlanGoal, i18n.T("edit the sprint's goal")),
			row(k.PlanRename, i18n.T("rename the sprint")),
			row(k.PlanNew, i18n.T("new sprint")),
			row(k.PlanComplete, i18n.T("complete the active sprint (twice)")),
			row(k.Search, i18n.T("filter both sides (esc clears)")),
			row(k.FilterBuilder, i18n.T("filter builder: field, compare, value side by side → filter")),
			row(k.QuickEdit, i18n.T("quick edit: status, priority, assignee, points…")),
			row(k.Bulk, i18n.T("edit the marked cards")),
			row(k.Undo, i18n.T("undo the last move across")),
			row(k.CopyKey, i18n.T("copy the sprint as a table")),
			{"esc / " + helpKey(k.Quit), i18n.T("back to the board")},
		}},
		section{i18n.T("Roadmap"), []helpRow{
			{join(k.Left, k.Right), i18n.T("scroll the timeline")},
			{join(k.ZoomIn, k.ZoomOut), i18n.T("zoom in / out")},
			row(k.Today, i18n.T("back to today")),
			row(k.RoadmapFold, i18n.T("fold out the epic's issues")),
			{join(k.MoveCardLeft, k.MoveCardRight), i18n.T("move the bar a column")},
			{join(k.EndEarlier, k.EndLater), i18n.T("move its end")},
			row(k.RoadmapGrip, i18n.T("grip its start, then end, then let go")),
			row(k.RoadmapIssues, i18n.T("the epic's issues as a view")),
			row(k.Search, i18n.T("filter the epics and their issues (esc clears)")),
			row(k.RoadmapEdit, i18n.T("quick edit the row's issue")),
			row(k.Undo, i18n.T("put the last moved bar's dates back")),
			row(k.Create, i18n.T("new epic")),
			row(k.CopyKey, i18n.T("copy the epics as a table")),
			{"esc / " + helpKey(k.Quit), i18n.T("back to the board")},
		}},
		section{i18n.T("Charts"), []helpRow{
			{helpKey(k.Tab) + " / " + join(k.PrevView, k.NextView), i18n.T("next chart / previous / next")},
			row(k.ChartDone, i18n.T("the column done counts from: it and those right of it (ui.report_done); Jira's resolution by default")),
			row(k.ChartCompare, i18n.T("a second line beside done, till you leave; again removes it")),
			row(k.CopyKey, i18n.T("copy the numbers as a table")),
			row(k.Refresh, i18n.T("refresh")),
			{"esc / " + helpKey(k.Quit), i18n.T("back to the board")},
		}},
		section{i18n.T("Standup"), []helpRow{
			{join(k.Left, k.Right), i18n.T("previous / next person, Everyone first: the board walked right to left")},
			row(k.Assignee, i18n.T("who takes part, kept for the board: the timer splits the length over them")),
			row(k.StandupPause, i18n.T("start / pause the timer (a turn each, and the whole standup; ui.standup_timer)")),
			row(k.StandupShuffle, i18n.T("the people in a random order / the board's")),
			{join(k.PrevView, k.NextView), i18n.T("a workday further back / later")},
			row(k.OpenChannel, i18n.T("open the issue in the panel")),
			row(k.Fold, i18n.T("show Off the board")),
			row(k.StandupPark, i18n.T("park the card: Everyone's parking lot comes last, kept for the sprint")),
			row(k.CopyKey, i18n.T("copy the stop as text")),
			{"esc / " + helpKey(k.Standup), i18n.T("back to the board")},
		}},
		section{i18n.T("Inbox"), []helpRow{
			{helpKey(k.Tab), i18n.T("Inbox / Mentions / All")},
			row(k.OpenChannel, i18n.T("open the issue in the panel (another site's in the browser)")),
			{join(k.JiraComment, k.JiraReply), i18n.T("comment / reply to the newest comment")},
			row(k.InboxDone, i18n.T("done: off the list until something new happens")),
			row(k.InboxDoneAll, i18n.T("every read thread done")),
			row(k.InboxUnread, i18n.T("read / unread")),
			row(k.InboxSnooze, i18n.T("snooze till the next workday")),
			{join(k.PageUp, k.PageDown), i18n.T("scroll the thread")},
			row(k.OpenAttach, i18n.T("open in the browser")),
			row(k.Refresh, i18n.T("sync now")),
			{"esc / " + helpKey(k.Inbox), i18n.T("back to the board")},
		}},
		section{i18n.T("Agents"), []helpRow{
			row(k.Tab, i18n.T("the worktrees without an agent too / not")),
			row(k.OpenChannel, i18n.T("type into the agent's terminal beside the list (narrow: attach; a worktree: its issue)")),
			row(k.AgentBack, i18n.T("from the terminal back to the list")),
			row(k.OpenRef, i18n.T("open the issue in the panel (another site's in the browser)")),
			row(k.AgentPrompt, i18n.T("send the agent a prompt")),
			row(k.AgentStop, i18n.T("stop the agent, closing its tab (twice)")),
			row(k.OpenAttach, i18n.T("open in the browser")),
			row(k.CopyKey, i18n.T("copy the key")),
			row(k.Refresh, i18n.T("read the agents and issues again")),
			{"esc / " + helpKey(k.Agents), i18n.T("back to the board")},
		}},
		section{i18n.T("Merge requests"), []helpRow{
			row(k.OpenChannel, i18n.T("read it in the panel: pipeline, approvals, description")),
			{"d", i18n.T("review its diff")},
			row(k.OpenAttach, i18n.T("open in GitLab")),
			row(k.Refresh, i18n.T("read them again")),
			{"esc / " + helpKey(k.MergeRequests), i18n.T("back to the board")},
		}},
		section{i18n.T("Merge request"), []helpRow{
			{"d", i18n.T("review the diff: notes, replies, suggestions")},
			{"A", i18n.T("approve")},
			{"M", i18n.T("merge: squash, delete the branch, GitLab's defaults first")},
			{"e", i18n.T("edit: title, draft, reviewers, assignees, labels, target branch, description")},
			{"C", i18n.T("an agent reviews it; its findings land in your review")},
			{"i", i18n.T("the Jira issue it names")},
			{"p", i18n.T("its pipeline's jobs: ↵ reads one's log")},
			row(k.OpenAttach, i18n.T("open in GitLab")),
			row(k.Refresh, i18n.T("read it again")),
			{"esc / " + helpKey(k.Back), i18n.T("back to the issue")},
		}},
		section{i18n.T("Job log"), []helpRow{
			{"↑ ↓ / pgup pgdn / g", i18n.T("scroll")},
			{"G", i18n.T("the end; a running job's log follows it")},
			{"← →", i18n.T("pan a wide line")},
			{"o", i18n.T("the job in GitLab")},
			{"r", i18n.T("read it again")},
			{"esc / q", i18n.T("close")},
		}},
		section{i18n.T("Diff review"), []helpRow{
			{"↑ ↓ / pgup pgdn / g G", i18n.T("move")},
			{"] / [", i18n.T("next / previous file")},
			{"n / N", i18n.T("next / previous thread")},
			{"tab", i18n.T("the file tree / the diff")},
			{"← →", i18n.T("pan a wide line")},
			{"c", i18n.T("note the line, reply on a thread")},
			{"V", i18n.T("start a range: c notes it, s suggests")},
			{"s", i18n.T("suggest a change to the line or range")},
			{"E / x", i18n.T("edit / drop your pending note")},
			{"R", i18n.T("resolve / reopen the thread")},
			{"S", i18n.T("submit your review: comment, approve, request changes")},
			{"A", i18n.T("approve without a review")},
			{"M", i18n.T("merge")},
			{"C", i18n.T("an agent reviews it")},
			{"e", i18n.T("the whole file / the changes only")},
			{"z / Z", i18n.T("fold the file / every file")},
			{"v", i18n.T("an earlier version (push)")},
			{"o / r", i18n.T("open in GitLab / reload")},
			{"esc / q", i18n.T("close")},
		}},
		section{i18n.T("Week"), []helpRow{
			{join(k.PrevView, k.NextView), i18n.T("previous / next week")},
			row(k.OpenChannel, i18n.T("log work on the cell's issue and day")),
			row(k.Goto, i18n.T("add an issue's row, to log on it")),
			row(k.CopyKey, i18n.T("copy the week as a markdown table")),
			row(k.Refresh, i18n.T("refresh")),
			{"esc / " + helpKey(k.Quit) + " / " + helpKey(k.Timesheet), i18n.T("back to the board")},
		}},
		section{i18n.T("Timesheet"), []helpRow{
			{join(k.PrevView, k.NextView), i18n.T("previous / next day")},
			row(k.EditEntry, i18n.T("edit the entry")),
			row(k.DeleteEntry, i18n.T("delete it (twice)")),
			{join(k.Mark, k.MarkAll), i18n.T("mark the entry / all: delete and move take the marked")},
			row(k.MoveEntry, i18n.T("move it to another issue")),
			row(k.ProposeWork, i18n.T("propose what's missing: commits, ui.activity, meetings")),
			row(k.OpenChannel, i18n.T("open the issue; a proposal: log it")),
			row(k.CopyKey, i18n.T("copy the day as a table")),
			row(k.Timesheet, i18n.T("the week")),
		}},
	)
	if m.opts.mouse {
		sections = append(sections, struct {
			title string
			rows  []helpRow
		}{i18n.T("Mouse"), []helpRow{
			{i18n.T("click"), i18n.T("select; a field again: edit")},
			{i18n.T("double-click"), i18n.T("open the issue")},
			{i18n.T("right-click"), i18n.T("a card's quick actions: status, assignee, points, …")},
			{i18n.T("drag"), i18n.T("a card to a lane, band or sprint, or within its lane to rank; a roadmap bar")},
			{i18n.T("header"), i18n.T("views, filters, chips and key hints act")},
			{"▾ ▸", i18n.T("fold a swimlane, epic or parent")},
			{i18n.T("panel edges"), i18n.T("left: resize · right: scroll")},
			{i18n.T("drag panel text"), i18n.T("select it; letting go copies")},
			{"↩ ✎ ✕", i18n.T("under a comment: reply, edit, delete (twice)")},
			{i18n.T("an agent"), i18n.T("in the panel: attach to it")},
			{i18n.T("wheel"), i18n.T("scroll, move the cursor")},
			{"esc", i18n.T("cancel a drag")},
			{"ui.mouse: off", i18n.T("leave the mouse to the terminal")},
		}})
	}
	return sections
}

// helpTitle heads a help column: a shaded bar across it, or without
// shading the title over a rule, so the columns read as groups.
func helpTitle(title string, width int) string {
	if shadeOn {
		return bar(titleStyle.Render(" "+title), width) + "\n"
	}
	return titleStyle.Render(title) + "\n" + refDimStyle.Render(strings.Repeat("─", width))
}

// helpPages lays the sections out side by side, a section running on into
// another column when it is taller than height allows, and splits the
// columns into pages as wide as the screen allows.
func (m *Model) helpPages(height int) [][]string {
	keyStyle := lipgloss.NewStyle().Foreground(focusedColor).Bold(true)
	perCol := max(height-10, 6) // border, padding, title and hint
	var cols []string
	for _, s := range m.helpSections() {
		keyW := 0
		for _, r := range s.rows {
			keyW = max(keyW, lipgloss.Width(r.keys))
		}
		for start := 0; start < len(s.rows); start += perCol {
			title := s.title
			if start > 0 {
				title += i18n.T(" (more)")
			}
			rows := s.rows[start:min(start+perCol, len(s.rows))]
			descW := max(m.width-8-keyW-2, 10) // a description past the screen's edge is cut
			colW := lipgloss.Width(title) + 2
			for _, r := range rows {
				colW = max(colW, keyW+2+min(lipgloss.Width(r.desc), descW))
			}
			lines := []string{helpTitle(title, colW)}
			for _, r := range rows {
				pad := strings.Repeat(" ", keyW-lipgloss.Width(r.keys))
				lines = append(lines, keyStyle.Render(r.keys)+pad+"  "+truncate(r.desc, descW))
			}
			cols = append(cols, strings.Join(lines, "\n"))
		}
	}
	avail := m.width - 8 // border and padding
	var pages [][]string
	w := 0
	for _, c := range cols {
		cw := lipgloss.Width(c)
		if len(pages) == 0 || w+3+cw > avail {
			pages, w = append(pages, nil), -3
		}
		pages[len(pages)-1] = append(pages[len(pages)-1], c)
		w += 3 + cw
	}
	return pages
}

// renderHelp draws the help's current page.
func (m *Model) renderHelp(height int) string {
	pages := m.helpPages(height)
	page := pages[min(m.helpPage, len(pages)-1)]
	var cols []string
	for i, c := range page {
		if i > 0 {
			cols = append(cols, "   ")
		}
		cols = append(cols, c)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, cols...)
	hintText := i18n.T("any key closes · rebind in ui.keys")
	if len(pages) > 1 {
		hintText = i18n.Tf("page %d/%d · ← → more · any other key closes · rebind in ui.keys", min(m.helpPage, len(pages)-1)+1, len(pages))
	}
	hint := lipgloss.NewStyle().Foreground(dimColor).Italic(true).Render(hintText)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(focusedColor).
		Padding(1, 3).Render(lipgloss.JoinVertical(lipgloss.Left, body, "", hint))
}

// openHelp shows the help at the page with title's keys (the focused
// pane's).
func (m *Model) openHelp(title string) {
	m.helpOpen, m.helpPage = true, 0
	for i, p := range m.helpPages(m.bodyH()) {
		for _, c := range p {
			first, _, _ := strings.Cut(ansi.Strip(c), "\n")
			if strings.TrimSpace(first) == i18n.T(title) {
				m.helpPage = i
				return
			}
		}
	}
}

// handleHelpKey pages the help with ← →; any other key closes it.
func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := len(m.helpPages(m.bodyH()))
	switch {
	case msg.String() == "ctrl+c":
		return m.quit()
	case n > 1 && (key.Matches(msg, m.keys.Right) || msg.String() == "right"):
		m.helpPage = min(m.helpPage+1, n-1)
	case n > 1 && (key.Matches(msg, m.keys.Left) || msg.String() == "left"):
		m.helpPage = max(m.helpPage-1, 0)
	default:
		m.helpOpen, m.helpPage = false, 0
	}
	return m, nil
}
