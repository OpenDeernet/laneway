package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cornedor/laneway/internal/jira"
)

// TestCommentDraft: a comment typed is kept a moment later; a new session
// on the same state brings it back, and posting it drops it.
func TestCommentDraft(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraIssue = &jira.Issue{Key: "ABC-1"}
	m.openJiraCommentInput()
	for _, k := range []string{"h", "i"} {
		out, cmd := m.handleJiraCommentKey(keyStr(k))
		m = out.(Model)
		if k == "h" && cmd == nil {
			t.Fatal("typing should schedule a draft save")
		}
	}
	out, _ := m.Update(draftSaveMsg{})
	m = out.(Model)
	if d, ok := m.draft(commentDraft("ABC-1")); !ok || d.Text != "hi" {
		t.Fatalf("draft %q %v", d.Text, ok)
	}

	next := jiraTabModel(t)
	next.store = m.store // the app started again
	next.jiraIssue = &jira.Issue{Key: "ABC-1"}
	next.openJiraCommentInput()
	if next.jiraCommentInput.Value() != "hi" || !strings.Contains(next.status, "your draft from") {
		t.Fatalf("reopened with %q, status %q", next.jiraCommentInput.Value(), next.status)
	}
	out, _ = next.applyJiraComment()
	next = out.(Model)
	out, _ = next.handleJiraMutated(jiraMutatedMsg{key: "ABC-1", field: "comment", text: "hi"})
	next = out.(Model)
	if _, ok := next.draft(commentDraft("ABC-1")); ok {
		t.Error("a posted comment should drop its draft")
	}
}

// TestDescDraft: the description editor keeps its text on quit and offers
// it over Jira's on reopen; esc twice drops it.
func TestDescDraft(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraIssue = &jira.Issue{Key: "ABC-1"}
	m.refOpen = true
	m.refs = []reference{{kind: refJira, jiraKey: "ABC-1"}}
	out, _ := m.handleDescLoaded(descLoadedMsg{key: "ABC-1", md: "old"})
	m = out.(Model)
	m.descEdit.input.SetValue("new text")
	out, _ = m.quit()
	m = out.(Model) // asks first; the draft is kept either way
	m.quitAsked = true
	m.quit()
	if d, ok := m.draft("desc:ABC-1"); !ok || d.Text != "new text" {
		t.Fatalf("draft %q %v", d.Text, ok)
	}
	m.descEdit = nil
	out, _ = m.handleDescLoaded(descLoadedMsg{key: "ABC-1", md: "old"})
	m = out.(Model)
	if m.descEdit.input.Value() != "new text" || m.descEdit.before != "old" {
		t.Fatalf("reopened with %q over %q", m.descEdit.input.Value(), m.descEdit.before)
	}
	for range 2 {
		out, _ = m.handleDescEditKey(keyMsg(t, "esc"))
		m = out.(Model)
	}
	if _, ok := m.draft("desc:ABC-1"); ok || m.descEdit != nil {
		t.Error("esc twice should drop the draft")
	}
}

// TestDescConflict: a save finding the description changed in Jira writes
// nothing and opens the editor again on your text; ctrl+r swaps in Jira's
// and back, and ctrl+s then saves yours over it.
func TestDescConflict(t *testing.T) {
	theirs := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"theirs"}]}]}`
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			puts = append(puts, string(b))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		io.WriteString(w, `{"fields":{"description":`+theirs+`}}`)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.jiraIssue = &jira.Issue{Key: "ABC-1"}
	m.refOpen = true
	m.refs = []reference{{kind: refJira, jiraKey: "ABC-1"}}
	out, _ := m.handleDescLoaded(descLoadedMsg{key: "ABC-1", md: "old", base: jira.DocBase(nil)})
	m = out.(Model)
	m.descEdit.input.SetValue("mine")
	out, cmd := m.handleDescEditKey(keyMsg(t, "ctrl+s"))
	m = out.(Model)
	out, _ = m.Update(cmd())
	m = out.(Model)
	if len(puts) != 0 || m.descEdit == nil || m.descEdit.input.Value() != "mine" || !strings.Contains(m.status, "changed in Jira") {
		t.Fatalf("after a refused save: %d puts, status %q", len(puts), m.status)
	}
	if d, ok := m.draft("desc:ABC-1"); !ok || d.Text != "mine" {
		t.Errorf("the refused text should be kept as a draft: %+v", d)
	}
	for _, want := range []string{"theirs", "mine"} {
		out, _ = m.handleDescEditKey(keyMsg(t, "ctrl+r"))
		m = out.(Model)
		if m.descEdit.input.Value() != want {
			t.Fatalf("ctrl+r shows %q, want %q", m.descEdit.input.Value(), want)
		}
	}
	_, cmd = m.handleDescEditKey(keyMsg(t, "ctrl+s"))
	if msg, ok := cmd().(jiraMutatedMsg); !ok || msg.err != nil || len(puts) != 1 || !strings.Contains(puts[0], `"text":"mine"`) {
		t.Fatalf("save over theirs: %+v, puts %q", msg, puts)
	}
}

// TestDescDraftBase: a draft checks its save against the document it was
// written on; one kept before drafts had a base, against Jira's at open.
func TestDescDraftBase(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraIssue = &jira.Issue{Key: "ABC-1"}
	m.refOpen = true
	m.refs = []reference{{kind: refJira, jiraKey: "ABC-1"}}
	_ = m.store.SetMeta(draftPrefix+"desc:ABC-1", "1700000000\nold draft")
	out, _ := m.handleDescLoaded(descLoadedMsg{key: "ABC-1", md: "now", base: "b2"})
	m = out.(Model)
	if m.descEdit.input.Value() != "old draft" || m.descEdit.base != "b2" {
		t.Fatalf("old draft: %q on base %q", m.descEdit.input.Value(), m.descEdit.base)
	}
	m.descEdit = nil
	m.saveDraft("desc:ABC-1", "draft", "b1")
	out, _ = m.handleDescLoaded(descLoadedMsg{key: "ABC-1", md: "now", base: "b2"})
	m = out.(Model)
	if m.descEdit.base != "b1" || !strings.Contains(m.status, "changed since") {
		t.Fatalf("draft on b1: base %q, status %q", m.descEdit.base, m.status)
	}
}

func TestDecodeDraft(t *testing.T) {
	for v, want := range map[string]Draft{
		"1700000000\nhi\nthere":                        {Text: "hi\nthere", At: time.Unix(1700000000, 0)},
		"1700000000 ab12\nhi":                          {Text: "hi", Base: "ab12", At: time.Unix(1700000000, 0)},
		EncodeDraft(Draft{"x", "c3", time.Unix(5, 0)}): {Text: "x", Base: "c3", At: time.Unix(5, 0)},
	} {
		if got, ok := DecodeDraft(v); !ok || got != want {
			t.Errorf("%q = %+v %v", v, got, ok)
		}
	}
	if _, ok := DecodeDraft("nope\nx"); ok {
		t.Error("a bad head should be no draft")
	}
}

// TestCommentVisibilityCycle: ctrl+o asks once, then steps who the comment
// is for, shown in the composer's title.
func TestCommentVisibilityCycle(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraIssue = &jira.Issue{Key: "ABC-1"}
	m.openJiraCommentInput()
	out, cmd := m.handleJiraCommentKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	m = out.(Model)
	if cmd == nil {
		t.Fatal("the first ctrl+o should ask Jira")
	}
	out, _ = m.Update(commentVisMsg{project: "ABC", vis: []jira.Visibility{{Internal: true}, {Role: "Developers"}}})
	m = out.(Model)
	if !m.jiraCommentVis.Internal || !strings.Contains(m.renderJiraCommentInput(), "internal note") {
		t.Fatalf("vis %+v", m.jiraCommentVis)
	}
	for _, want := range []string{"Developers", ""} {
		out, _ = m.handleJiraCommentKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
		if m = out.(Model); m.jiraCommentVis.Role != want {
			t.Errorf("role %q, want %q", m.jiraCommentVis.Role, want)
		}
	}
}

// TestCommentCtrlEnterPosts: ctrl+enter posts like ctrl+s.
func TestCommentCtrlEnterPosts(t *testing.T) {
	m := jiraTabModel(t)
	m.jiraIssue = &jira.Issue{Key: "ABC-1"}
	m.openJiraCommentInput()
	m.jiraCommentInput.SetValue("hi")
	out, cmd := m.handleJiraCommentKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if out.(Model).jiraCommentActive || cmd == nil {
		t.Fatal("ctrl+enter should close the composer and post")
	}
}
