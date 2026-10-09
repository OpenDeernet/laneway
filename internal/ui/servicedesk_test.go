package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/jira"
)

// TestDeskRequest: an issue of a service desk shows its Request block, and
// a comment on it is an internal note until ctrl+o makes it a reply to the
// customer, which goes through the Service Desk API as public.
func TestDeskRequest(t *testing.T) {
	var posted map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/servicedeskapi/servicedesk" {
			fmt.Fprint(w, `{"isLastPage":true,"values":[{"id":"1","projectKey":"ABC"}]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &posted)
		path = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	m := loadedJiraModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	out, _ := m.handlePanelExtra(panelExtraMsg{key: "ABC-1", desk: true, request: &jira.Request{Key: "ABC-1", Type: "Get IT help", Customer: "Ann", CustomerEmail: "ann@x.test",
		SLAs: []jira.SLA{{Name: "Time to first response", Remaining: "2h 30m"}, {Name: "Time to resolution", Remaining: "-1h 5m", Breached: true}}}})
	m = out.(Model)
	c := ansi.Strip(m.refView.GetContent())
	for _, want := range []string{"Request", "Get IT help", "Ann <ann@x.test>", "2h 30m left", "-1h 5m · breached"} {
		if !strings.Contains(c, want) {
			t.Errorf("panel lacks %q:\n%s", want, c)
		}
	}
	if s := slaText(jira.SLA{Remaining: "-1h", Breached: true}); s == ansi.Strip(s) && !monoTheme {
		t.Error("a breached SLA isn't coloured")
	}

	m.openJiraCommentInput()
	if !m.jiraCommentVis.Internal || !strings.Contains(ansi.Strip(m.renderJiraCommentInput()), "internal note") {
		t.Fatalf("default %+v:\n%s", m.jiraCommentVis, ansi.Strip(m.renderJiraCommentInput()))
	}
	m.commentVis = map[string][]jira.Visibility{"ABC": {{Internal: true}, {Role: "Agents"}}}
	for _, want := range []jira.Visibility{{Customer: true}, {Role: "Agents"}, {Internal: true}, {Customer: true}} {
		o, _ := m.handleJiraCommentKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
		if m = o.(Model); m.jiraCommentVis != want {
			t.Fatalf("ctrl+o: %+v, want %+v", m.jiraCommentVis, want)
		}
	}
	if !strings.Contains(ansi.Strip(m.renderJiraCommentInput()), "reply to the customer") {
		t.Errorf("composer doesn't say the customer reads it:\n%s", ansi.Strip(m.renderJiraCommentInput()))
	}
	m.jiraCommentInput.SetValue("we're on it")
	_, cmd := m.applyJiraComment()
	cmd()
	if path != "/rest/servicedeskapi/request/ABC-1/comment" || posted["public"] != true || posted["body"] != "we're on it" {
		t.Errorf("posted %s %v", path, posted)
	}

	// A reply there is a new comment, an internal note again.
	m.openJiraReply(jira.Comment{ID: "7", Author: "Ann", AuthorID: "a1", Body: "help"})
	if m.replyParent() != "" || !m.jiraCommentVis.Internal {
		t.Errorf("reply: parent %q, vis %+v", m.replyParent(), m.jiraCommentVis)
	}
	m.jiraCommentInput.SetValue(m.jiraCommentInput.Value() + "noted")
	_, cmd = m.applyJiraComment()
	cmd()
	if path != "/rest/servicedeskapi/request/ABC-1/comment" || posted["public"] != false {
		t.Errorf("reply posted %s %v", path, posted)
	}
}
