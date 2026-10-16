package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cornedor/laneway/internal/jira"
)

// The site's change stream (laneway-server; Jira has none): a change in the
// board's project refreshes the board at once instead of at the next idle
// tick. Polling stays on as the fallback.

type jiraSiteChangeMsg struct {
	src chan tea.Msg
	ch  jira.SiteChange
}

type jiraSiteEventsEndMsg struct{}

// jiraEventRefreshMsg is the debounced refresh a burst of changes asks for.
type jiraEventRefreshMsg struct{}

// jiraEventInboxMsg is the debounced inbox sync changes to issues ask for.
type jiraEventInboxMsg struct{}

func waitSiteEvent(src chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-src }
}

// followSiteEvents starts following the change stream, if the site has one.
func (m *Model) followSiteEvents() tea.Cmd {
	if m.jiraClient == nil || !m.jiraClient.Enabled() {
		return nil
	}
	src := make(chan tea.Msg, 64)
	client := m.jiraClient
	go func() {
		client.Events(context.Background(), func(c jira.SiteChange) {
			select {
			case src <- jiraSiteChangeMsg{src: src, ch: c}:
			default: // a refresh is coming anyway
			}
		})
		src <- jiraSiteEventsEndMsg{}
	}()
	return waitSiteEvent(src)
}

// handleSiteChange arms one refresh for a burst of changes to the board's
// project and keeps listening.
func (m Model) handleSiteChange(msg jiraSiteChangeMsg) (tea.Model, tea.Cmd) {
	next := waitSiteEvent(msg.src)
	// Any issue or comment change may be news for the inbox: one sync for
	// a burst, a little later so a run of edits lands together.
	if (msg.ch.Kind == "issue" || msg.ch.Kind == "comment") && !m.eventInboxPending && m.opts.inboxEvery > 0 {
		m.eventInboxPending = true
		next = tea.Batch(next, tea.Tick(15*time.Second, func(time.Time) tea.Msg { return jiraEventInboxMsg{} }))
	}
	t := m.jiraTab
	relevant := t.project != "" && (strings.HasPrefix(msg.ch.Key, t.project+"-") || msg.ch.Kind == "sprint" || msg.ch.Kind == "board")
	if !relevant || m.eventRefreshPending {
		return m, next
	}
	m.eventRefreshPending = true
	return m, tea.Batch(next, tea.Tick(time.Second, func(time.Time) tea.Msg { return jiraEventRefreshMsg{} }))
}

func (m Model) handleEventRefresh() (tea.Model, tea.Cmd) {
	m.eventRefreshPending = false
	t := m.jiraTab
	if m.modalOpen() || t.loading || t.searching || m.jiraDragging() || t.cfg == nil {
		return m, nil // the idle tick picks it up
	}
	return m, m.refreshJiraView()
}

func (m Model) handleEventInbox() (tea.Model, tea.Cmd) {
	m.eventInboxPending = false
	return m, m.syncInbox()
}
