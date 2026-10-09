package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/index"
	"github.com/cornedor/laneway/internal/jira"
)

func TestMentionAt(t *testing.T) {
	for _, c := range []struct {
		text  string
		q     string
		start int
		ok    bool
	}{
		{"hi @ad", "ad", 3, true},
		{"@Ann", "Ann", 0, true},
		{"mail me@x", "", 0, false},
		{"hi @", "", 0, false},
		{"hi @ada lov", "", 0, false},
	} {
		q, start, ok := mentionAt(c.text, len([]rune(c.text)))
		if q != c.q || start != c.start || ok != c.ok {
			t.Errorf("%q: %q %d %v", c.text, q, start, ok)
		}
	}
}

// TestMentionFlow: "@ad" finds Ada, tab writes her name, posting sends a
// real mention.
func TestMentionFlow(t *testing.T) {
	var posted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/rest/servicedeskapi/") {
			http.NotFound(w, r) // Jira without Service Management
			return
		}
		if r.Method == http.MethodGet {
			io.WriteString(w, `[{"accountId":"a1","displayName":"Ada Lovelace"}]`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		posted = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	m := loadedJiraModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.openJiraCommentInput()
	m.jiraCommentInput.SetValue("thanks @ad")
	m.jiraCommentInput.CursorEnd()
	cmd := m.scheduleMention()
	out, cmd := m.handleMentionSearch(cmd().(mentionSearchMsg))
	m = out.(Model)
	out, _ = m.Update(cmd().(mentionFoundMsg))
	m = out.(Model)
	if !strings.Contains(m.View().Content, "@Ada Lovelace") {
		t.Fatal("suggestion not shown")
	}
	out, _ = m.handleJiraCommentKey(keyMsg(t, "tab"))
	m = out.(Model)
	if got := m.jiraCommentInput.Value(); got != "thanks @Ada Lovelace " {
		t.Fatalf("value = %q", got)
	}
	_, cmd = m.applyJiraComment()
	cmd()
	if !strings.Contains(posted, `"type":"mention"`) || !strings.Contains(posted, `"id":"a1"`) {
		t.Errorf("posted = %s", posted)
	}
}

// TestEmojiFlow: ":rocke" offers the rocket, tab writes it, posting sends
// Jira's emoji; a comment's emoji shows as its glyph.
func TestEmojiFlow(t *testing.T) {
	var posted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		posted = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	m := loadedJiraModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.openJiraCommentInput()
	m.jiraCommentInput.SetValue("ship it :rocke")
	m.jiraCommentInput.CursorEnd()
	if cmd := m.scheduleMention(); cmd != nil || len(m.jiraMention.emoji) == 0 || m.jiraMention.emoji[0] != "rocket" {
		t.Fatalf("offered %v", m.jiraMention.emoji)
	}
	m.renderRef() // as Update does after a key
	if !strings.Contains(ansi.Strip(m.View().Content), "🚀 :rocket:") {
		t.Fatal("emoji not shown")
	}
	out, _ := m.handleJiraCommentKey(keyMsg(t, "tab"))
	m = out.(Model)
	if got := m.jiraCommentInput.Value(); got != "ship it :rocket: " {
		t.Fatalf("value = %q", got)
	}
	_, cmd := m.applyJiraComment()
	cmd()
	if !strings.Contains(posted, `"shortName":":rocket:"`) || !strings.Contains(posted, `"type":"emoji"`) {
		t.Errorf("posted = %s", posted)
	}
	for _, text := range []string{"at 10:30", "mail me:ab", "ok :)", "hm :-)", ":a", "done :tada: ok"} {
		if _, _, ok := emojiQueryAt(text, len(text)); ok {
			t.Errorf("%q offered emoji", text)
		}
	}

	m = loadedJiraModel(t)
	m.jiraIssue.Comments = []jira.Comment{{ID: "1", Author: "Ann", Body: "nice 👍🏽:tada:"}} // as jira reads the emoji nodes (TestEmojiShows)
	m.renderRef()
	if got := ansi.Strip(m.refView.GetContent()); !strings.Contains(got, "nice 👍🏽🎉") {
		t.Errorf("comment shows:\n%s", got)
	}
}

// TestEmojiMatches: exact, then prefix, then inside, then the letters in
// order; one taken before rises within its band.
func TestEmojiMatches(t *testing.T) {
	m := loadedJiraModel(t)
	if got := m.emojiMatches("smile"); got[0] != "smile" {
		t.Errorf("exact first: %v", got)
	}
	if got := m.emojiMatches("smle"); !slices.Contains(got, "smile") {
		t.Errorf("fuzzy: %v", got)
	}
	if got := m.emojiMatches("tad"); !slices.Contains(got, "tada") || slices.ContainsFunc(got, func(n string) bool { return strings.Contains(n, "standing") }) {
		t.Errorf("tad: %v; want no letters-in-order matches beside nearer ones", got)
	}
	if got := m.emojiMatches("+1"); slices.ContainsFunc(got, func(n string) bool { return strings.HasSuffix(n, "skin_tone") }) {
		t.Errorf("+1: %v; want skin tones only when asked", got)
	}
	if got := m.emojiMatches("+1_skin"); len(got) == 0 {
		t.Error("+1_skin: want the tones")
	}
	got := m.emojiMatches("rock")
	if got[0] != "rock" || got[1] != "rocket" {
		t.Fatalf("rock: %v", got)
	}
	m.openJiraCommentInput()
	m.jiraCommentInput.SetValue(":rocke")
	m.jiraCommentInput.CursorEnd()
	m.scheduleMention()
	m.acceptEmoji("rocket")
	if got := m.emojiMatches("roc"); got[0] != "rocket" {
		t.Errorf("taken before should lead its band: %v", got)
	}
}

// TestEmojiSkinTone: ui.skin_tone offers the toned name where one exists,
// the plain one elsewhere.
func TestEmojiSkinTone(t *testing.T) {
	m := loadedJiraModel(t)
	m.opts.skinTone = "_medium_skin_tone"
	if got := m.emojiMatches("+1"); got[0] != "+1_medium_skin_tone" {
		t.Errorf("+1: %v", got)
	}
	if got := m.emojiMatches("rocket"); got[0] != "rocket" {
		t.Errorf("rocket: %v", got)
	}
}

// TestEmojiListKeys: while the list shows, ↓ chooses, enter takes (not a
// newline) and esc closes it, the comment kept; in the description editor
// too.
func TestEmojiListKeys(t *testing.T) {
	m := loadedJiraModel(t)
	m.openJiraCommentInput()
	type_ := func(s string) {
		t.Helper()
		for _, r := range s {
			out, _ := m.handleJiraCommentKey(keyMsg(t, string(r)))
			m = out.(Model)
		}
	}
	type_("hi :tad")
	if len(m.jiraMention.emoji) == 0 || m.jiraMention.emoji[0] != "tada" {
		t.Fatalf("offered %v", m.jiraMention.emoji)
	}
	out, _ := m.handleJiraCommentKey(keyMsg(t, "enter"))
	if m = out.(Model); m.jiraCommentInput.Value() != "hi :tada: " {
		t.Fatalf("enter: %q", m.jiraCommentInput.Value())
	}
	type_(":smi")
	out, _ = m.handleJiraCommentKey(keyMsg(t, "esc"))
	if m = out.(Model); !m.jiraCommentActive || len(m.jiraMention.emoji) != 0 || m.jiraCommentDiscard {
		t.Fatalf("esc should close the list only: active %v list %v", m.jiraCommentActive, m.jiraMention.emoji)
	}

	m = loadedJiraModel(t)
	out, _ = m.handleDescLoaded(descLoadedMsg{key: "ABC-1", md: "see :rocke"})
	m = out.(Model)
	m.descEdit.input.CursorEnd()
	out, _ = m.handleDescEditKey(keyMsg(t, "t"))
	m = out.(Model)
	if len(m.jiraMention.emoji) == 0 || !strings.Contains(ansi.Strip(m.View().Content), ":rocket:") {
		t.Fatalf("description editor: %v", m.jiraMention.emoji)
	}
	out, _ = m.handleDescEditKey(keyMsg(t, "tab"))
	if m = out.(Model); m.descEdit.input.Value() != "see :rocket: " {
		t.Errorf("tab: %q", m.descEdit.input.Value())
	}
}

// TestMentionListInView: under a long thread the composer sits at the
// panel's foot; the list floats above it, nothing moving under it.
func TestMentionListInView(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"accountId":"a1","displayName":"Claude"}]`)
	}))
	defer srv.Close()
	m := loadedJiraModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	for i := range 30 {
		m.jiraIssue.Comments = append(m.jiraIssue.Comments, jira.Comment{ID: string(rune('a' + i)), Author: "Ann", Body: "a comment"})
	}
	m.renderRef()
	out, _ := m.handleRefKey(keyStr("c"))
	m = out.(Model)
	for _, k := range []string{"@", "c", "l"} {
		out, _ = m.Update(keyMsg(t, k))
		m = out.(Model)
	}
	row := func(s string) int {
		return slices.IndexFunc(strings.Split(ansi.Strip(m.View().Content), "\n"), func(l string) bool { return strings.Contains(l, s) })
	}
	before := row("┃ @cl")
	out, cmd := m.Update(mentionSearchMsg{m.jiraMention.seq, "cl"})
	m = out.(Model)
	out, _ = m.Update(cmd())
	m = out.(Model)
	if !m.commentInline() {
		t.Fatal("composer should sit in the panel")
	}
	if at := row("@Claude"); at < 0 || at >= before {
		t.Fatalf("suggestion at row %d, composer at %d; want it above:\n%s", at, before, ansi.Strip(m.View().Content))
	}
	if after := row("┃ @cl"); after != before {
		t.Errorf("composer moved from row %d to %d", before, after)
	}
}

// TestMentionFromKept: with the project's people kept, "@cl" lists them on
// the keystroke, no search; the one chosen stays chosen as you type on.
func TestMentionFromKept(t *testing.T) {
	ix, err := index.Open(filepath.Join(t.TempDir(), "ix.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	ix.SyncAssignable("ABC", []jira.User{{AccountID: "c1", DisplayName: "Claude"}, {AccountID: "c2", DisplayName: "Clara Oswald"}, {AccountID: "a1", DisplayName: "Ada"}})
	m := loadedJiraModel(t).WithIndex(ix)
	m.openJiraCommentInput()
	type_ := func(s string) {
		t.Helper()
		for _, r := range s {
			out, _ := m.handleJiraCommentKey(keyMsg(t, string(r)))
			m = out.(Model)
		}
	}
	type_("hi @cl")
	if ms := m.jiraMention; len(ms.sugg) != 2 || ms.searching {
		t.Fatalf("sugg %v searching %v; want both at once", ms.sugg, ms.searching)
	}
	out, _ := m.handleJiraCommentKey(keyMsg(t, "down"))
	m = out.(Model)
	chosen := m.jiraMention.sugg[1]
	type_("a")
	if ms := m.jiraMention; ms.sugg[ms.idx] != chosen {
		t.Errorf("chose %v; want %v kept", ms.sugg[ms.idx], chosen)
	}
}

// TestMentionListModal: in the composer drawn over the board, the list
// floats under the cursor and the composer keeps its place.
func TestMentionListModal(t *testing.T) {
	m := loadedJiraModel(t)
	m.openJiraCommentInput()
	m.refOpen = false
	for _, k := range []string{"@", "a", "d"} {
		out, _ := m.handleJiraCommentKey(keyMsg(t, k))
		m = out.(Model)
	}
	lines := func() []string { return strings.Split(ansi.Strip(m.View().Content), "\n") }
	row := func(s string) int {
		return slices.IndexFunc(lines(), func(l string) bool { return strings.Contains(l, s) })
	}
	before := row("┃ @ad")
	out, _ := m.Update(mentionFoundMsg{seq: m.jiraMention.seq, users: []jira.User{{AccountID: "a1", DisplayName: "Ada Lovelace"}}})
	m = out.(Model)
	if m.commentInline() {
		t.Fatal("want the composer over the board")
	}
	if at := row("@Ada Lovelace"); at != before+2 { // under the cursor's line and the box's top
		t.Fatalf("suggestion at row %d, composer at %d:\n%s", at, before, strings.Join(lines(), "\n"))
	}
	if after := row("┃ @ad"); after != before {
		t.Errorf("composer moved from row %d to %d", before, after)
	}
}

// TestReplyPostsParent: a reply goes under its comment in Jira's thread
// (parentId), and who sees it is the comment's, not ctrl+o's.
func TestReplyPostsParent(t *testing.T) {
	var posted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		posted = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	m := loadedJiraModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	m.jiraIssue.Comments = []jira.Comment{{ID: "10363", Author: "Ann", AuthorID: "a1", Body: "ship it?"}}
	m.openJiraReply(m.jiraIssue.Comments[0])
	if m.cycleCommentVis(); m.jiraCommentVis != (jira.Visibility{}) || !strings.Contains(m.status, "its comment") {
		t.Errorf("ctrl+o on a reply: %+v %q", m.jiraCommentVis, m.status)
	}
	m.jiraCommentInput.SetValue(m.jiraCommentInput.Value() + "yes")
	_, cmd := m.applyJiraComment()
	cmd()
	if !strings.Contains(posted, `"parentId":"10363"`) || strings.Contains(posted, "visibility") {
		t.Errorf("posted = %s", posted)
	}
	// A reply to a reply goes under the thread's top: Jira's are one level deep.
	m.jiraIssue.Comments = append(m.jiraIssue.Comments, jira.Comment{ID: "10364", ParentID: "10363", Author: "Bo", Body: "yes"})
	m.openJiraReply(m.jiraIssue.Comments[1])
	m.jiraCommentInput.SetValue(m.jiraCommentInput.Value() + "same")
	_, cmd = m.applyJiraComment()
	cmd()
	if !strings.Contains(posted, `"parentId":"10363"`) {
		t.Errorf("reply to a reply posted = %s", posted)
	}

	// ui.threaded_replies: off: a new comment (quoting it), ctrl+o free.
	m.opts.threaded = false
	m.openJiraReply(m.jiraIssue.Comments[0])
	if m.cycleCommentVis(); strings.Contains(m.status, "its comment") {
		t.Errorf("ctrl+o refused with threads off: %q", m.status)
	}
	m.jiraCommentVis = jira.Visibility{}
	m.jiraCommentInput.SetValue(m.jiraCommentInput.Value() + "yes")
	_, cmd = m.applyJiraComment()
	cmd()
	if strings.Contains(posted, "parentId") {
		t.Errorf("flat reply posted = %s", posted)
	}
}
