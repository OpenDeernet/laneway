package ui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// jiraBacklogID is the sprint picker's backlog row.
const jiraBacklogID = "backlog"

// openJiraSprintPicker offers the board's open sprints and its backlog as
// where the selected card goes. A kanban board has neither.
func (m *Model) openJiraSprintPicker() {
	t := m.jiraTab
	c, ok := m.selectedJiraCard()
	if !ok {
		return
	}
	var items []jiraPickerItem
	for i, v := range t.views {
		cur := i == t.viewIdx
		switch v.kind {
		case jiraViewSprint:
			items = append(items, jiraPickerItem{id: strconv.Itoa(v.sprint), label: v.name, current: cur})
		case jiraViewBacklog:
			items = append(items, jiraPickerItem{id: jiraBacklogID, label: i18n.T("Backlog"), current: cur})
		}
	}
	if len(items) == 0 {
		m.status = i18n.T("this board has no sprints")
		return
	}
	m.startJiraPicker(jiraPickSprint, i18n.Tf("Move %s to", c.Key), true)
	m.jiraPicker.issueKey = c.Key
	m.setJiraPickerItems(items)
}

// moveJiraToSprint puts keys in the picked sprint or the backlog.
func (m *Model) moveJiraToSprint(keys []string, it jiraPickerItem) tea.Cmd {
	if it.current {
		return nil
	}
	client, ctx := m.jiraClient, m.ctx
	run := func() error { return client.MoveToBacklog(ctx, keys...) }
	if it.id != jiraBacklogID {
		sprint, _ := strconv.Atoi(it.id)
		run = func() error { return client.MoveToSprint(ctx, sprint, keys...) }
	}
	m.undoSprintMove(keys)
	key := strings.Join(keys, ", ")
	m.status = i18n.Tf("moving %s to %s…", key, it.label)
	return jiraMutateCmd(key, "sprint", run)
}

// sprintViews are a scrum board's open sprints as views, the active ones in
// lanes.
func sprintViews(sprints []jira.Sprint) []jiraView {
	views := make([]jiraView, 0, len(sprints))
	for _, s := range sprints {
		views = append(views, jiraView{kind: jiraViewSprint, name: s.Name, sprint: s.ID, lanes: s.State == "active",
			start: s.Start, end: s.End, goal: s.Goal})
	}
	return views
}

// sameSprintView reports whether two sprint views show the same.
func sameSprintView(a, b jiraView) bool {
	return a.sprint == b.sprint && a.name == b.name && a.lanes == b.lanes && a.start.Equal(b.start) && a.end.Equal(b.end) && a.goal == b.goal
}

// jiraSprintsMsg is a scrum board's open sprints, read again by a refresh.
type jiraSprintsMsg struct {
	board   int
	sprints []jira.Sprint
	err     error
}

// refreshJiraView refetches the view's changed cards; on a scrum board it
// reads the sprints first, so one closed or started elsewhere shows.
func (m *Model) refreshJiraView() tea.Cmd {
	t := m.jiraTab
	if t.board >= len(t.boards) || t.boards[t.board].Type != "scrum" {
		return m.loadJiraDelta()
	}
	c, ctx, board := m.jiraClient, m.ctx, m.jiraBoardID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, c.Scaled(60*time.Second))
		defer cancel()
		sprints, err := c.Sprints(ctx, board)
		return jiraSprintsMsg{board: board, sprints: sprints, err: err}
	}
}

// handleJiraSprints puts the board's sprints in place of the ones it shows;
// the sprint shown, when it has closed, gives way to the first view.
func (m Model) handleJiraSprints(msg jiraSprintsMsg) (tea.Model, tea.Cmd) {
	t := m.jiraTab
	if msg.board != m.jiraBoardID() || t.loading || t.cfg == nil {
		return m, nil // a board switch or a load since: that one is fresh
	}
	if msg.err != nil {
		return m, m.loadJiraDelta()
	}
	n := 0
	for n < len(t.views) && t.views[n].kind == jiraViewSprint && t.views[n].closed.IsZero() {
		n++
	}
	fresh := sprintViews(msg.sprints)
	if slices.EqualFunc(t.views[:n], fresh, sameSprintView) {
		return m, m.loadJiraDelta()
	}
	// Where view i went: a sprint by its ID, -1 when it closed.
	moved := func(i int) int {
		if i >= n {
			return i - n + len(fresh)
		}
		return slices.IndexFunc(fresh, func(v jiraView) bool { return v.sprint == t.views[i].sprint })
	}
	cur, _ := m.jiraCurrentView()
	wasFull := t.fullKey == m.jiraFetchKey(t.viewIdx)
	idx, from := moved(t.viewIdx), moved(t.closedFrom)
	t.views = append(fresh, t.views[n:]...)
	t.closedFrom = max(from, 0)
	if idx < 0 {
		t.viewIdx, t.fullKey = 0, ""
		m.status = i18n.Tf("%s has closed · showing %s", cur.name, t.views[0].name)
		return m, m.loadJiraCards(0, false)
	}
	t.viewIdx = idx
	if wasFull {
		t.fullKey = m.jiraFetchKey(idx)
	}
	m.buildJiraLanes()
	m.renderJira()
	return m, m.loadJiraDelta()
}
