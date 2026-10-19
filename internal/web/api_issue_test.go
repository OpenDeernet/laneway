package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/demo"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/store"
)

func issueServer(t *testing.T, base string) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cl := jira.New(jira.Config{BaseURL: base, Email: "d@example.com", APIToken: "x", Projects: []string{"DEMO"}})
	ts := httptest.NewServer(New(context.Background(), Options{Client: cl, Jira: config.JiraConfig{}, Store: st, Site: "demo", Demo: true}))
	t.Cleanup(ts.Close)
	return ts
}

func issueCall(t *testing.T, method, url string, body, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestIssuePanelRoutes(t *testing.T) {
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ts := issueServer(t, base)
	var cards struct{ Cards []jira.Card }
	if issueCall(t, "GET", ts.URL+"/api/search?jql=project%20%3D%20DEMO", nil, &cards) != 200 || len(cards.Cards) == 0 {
		t.Fatal("no demo cards")
	}
	key := cards.Cards[0].Key
	iu := ts.URL + "/api/issues/" + key

	var card jira.Card
	if code := issueCall(t, "GET", iu+"/card", nil, &card); code != 200 || card.Key != key {
		t.Fatalf("card: %d %q", code, card.Key)
	}
	var ed editable
	if code := issueCall(t, "GET", iu+"/description", nil, &ed); code != 200 || !ed.Editable {
		t.Fatalf("description: %d %+v", code, ed)
	}
	if code := issueCall(t, "PUT", iu+"/description", mdBody{Markdown: "hello **world**", Kept: ed.Kept, Base: ed.Base}, nil); code != 200 {
		t.Fatalf("set description: %d", code)
	}
	// A save on what the editor opened before that one is refused.
	if code := issueCall(t, "PUT", iu+"/description", mdBody{Markdown: "stale", Base: ed.Base}, nil); code != http.StatusConflict {
		t.Fatalf("stale description save: %d, want 409", code)
	}
	if issueCall(t, "GET", iu+"/description", nil, &ed); ed.Markdown != "hello **world**" {
		t.Fatalf("description after a refused save = %q", ed.Markdown)
	}

	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "  "}, nil); code != 400 {
		t.Errorf("empty comment: %d, want 400", code)
	}
	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "hi @Mira Jansen", "Mentions": []jira.Mention{{AccountID: "demo-0002", DisplayName: "Mira Jansen"}}}, nil); code != 200 {
		t.Fatalf("add comment: %d", code)
	}
	var is jira.Issue
	issueCall(t, "GET", iu+"?fresh=1", nil, &is)
	if len(is.Comments) == 0 {
		t.Fatal("comment not listed")
	}
	id := is.Comments[len(is.Comments)-1].ID
	if code := issueCall(t, "GET", iu+"/comments/"+id+"/edit", nil, &ed); code != 200 || !ed.Editable {
		t.Fatalf("comment edit: %d %+v", code, ed)
	}
	if code := issueCall(t, "PUT", iu+"/comments/"+id, mdBody{Markdown: "edited", Kept: ed.Kept}, nil); code != 200 {
		t.Fatalf("edit comment: %d", code)
	}
	var del struct{ Raw json.RawMessage }
	if code := issueCall(t, "DELETE", iu+"/comments/"+id, nil, &del); code != 200 || len(del.Raw) == 0 {
		t.Fatalf("delete comment: %d %s", code, del.Raw)
	}
	n := len(is.Comments)
	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Raw": del.Raw}, nil); code != 200 {
		t.Fatalf("undo delete: %d", code)
	}
	issueCall(t, "GET", iu+"?fresh=1", nil, &is)
	if len(is.Comments) != n || is.Comments[n-1].Body != "edited" {
		t.Errorf("comment not back: %+v", is.Comments)
	}
	// A reply comes back under its comment: read from the comment endpoint,
	// as the issue's own list has no parentId (the demo mirrors Jira).
	parent := is.Comments[n-1].ID
	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "agreed", "Parent": parent}, nil); code != 200 {
		t.Fatalf("reply: %d", code)
	}
	issueCall(t, "GET", iu+"?fresh=1", nil, &is)
	if last := is.Comments[len(is.Comments)-1]; last.Body != "agreed" || last.ParentID != parent {
		t.Errorf("reply = %+v, want parent %s", last, parent)
	}
	// Under a reply Jira (and the demo) refuses: the client names the top comment.
	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "nested", "Parent": is.Comments[len(is.Comments)-1].ID}, nil); code == 200 {
		t.Errorf("reply under a reply: %d, want a refusal", code)
	}
	var vis []jira.Visibility
	if code := issueCall(t, "GET", ts.URL+"/api/projects/DEMO/commentvis", nil, &vis); code != 200 || len(vis) != 4 || vis[0].Role != "Administrators" || vis[2].Group != "developers" {
		t.Errorf("commentvis: %d %+v", code, vis)
	}
	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "devs only", "Visibility": jira.Visibility{Role: "Developers"}}, nil); code != 200 {
		t.Errorf("role comment: %d", code)
	}
	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "group only", "Visibility": vis[2]}, nil); code != 200 {
		t.Errorf("group comment: %d", code)
	}
	var back jira.Issue
	if code := issueCall(t, "GET", iu+"?fresh=1", nil, &back); code != 200 || back.Comments[len(back.Comments)-1].Visibility != vis[2] ||
		back.Comments[len(back.Comments)-2].Visibility != (jira.Visibility{Role: "Developers"}) {
		t.Errorf("visibility read back: %d %+v", code, back.Comments)
	}
	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Raw": json.RawMessage(`{"type":"paragraph"}`)}, nil); code != 400 {
		t.Errorf("not a doc: %d", code)
	}
	// After a timeout the same text again checks first: not there, it posts;
	// there, it isn't posted twice.
	var found struct{ Found bool }
	for i, want := range []bool{false, true} {
		if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "may have landed", "Check": true}, &found); code != 200 || found.Found != want {
			t.Errorf("check %d: %d %v, want %v", i, code, found.Found, want)
		}
	}
	issueCall(t, "GET", iu+"?fresh=1", nil, &is)
	if n := len(is.Comments); n != len(back.Comments)+1 || is.Comments[n-1].Body != "may have landed" {
		t.Errorf("after the checks: %+v", is.Comments)
	}
	for _, p := range []string{"/history", "/children", "/weblinks", "/timeinstatus", "/dev"} {
		if code := issueCall(t, "GET", iu+p, nil, nil); code != 200 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if code := issueCall(t, "GET", ts.URL+"/api/issues/nope/history", nil, nil); code != 400 {
		t.Errorf("bad key: %d, want 400", code)
	}
}

func TestAttachmentProxy(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	big := make([]byte, 17<<20)
	files := map[string][]byte{"1": png, "2": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), "3": big}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/rest/api/3/attachment/content/"):]
		if b, ok := files[id]; ok && r.Header.Get("Authorization") != "" {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	defer fake.Close()
	ts := issueServer(t, fake.URL)

	res, err := http.Get(ts.URL + "/api/attachments/1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || res.Header.Get("Content-Disposition") != "" {
		t.Errorf("png: %d %q %q", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"))
	}
	res, _ = http.Get(ts.URL + "/api/attachments/2?name=a/b.svg")
	res.Body.Close()
	if res.Header.Get("Content-Type") != "application/octet-stream" || res.Header.Get("Content-Disposition") == "" {
		t.Errorf("svg must download: %q %q", res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"))
	}
	res, _ = http.Get(ts.URL + "/api/attachments/3?download=1&name=a.mov")
	n, _ := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || n != int64(len(big)) {
		t.Errorf("big file: %d, %d bytes", res.StatusCode, n)
	}
	if code := issueCall(t, "GET", ts.URL+"/api/attachments/9", nil, nil); code == 200 {
		t.Error("missing attachment answered 200")
	}
	if code := issueCall(t, "GET", ts.URL+"/api/attachments/a.b", nil, nil); code != 400 {
		t.Errorf("bad id: %d, want 400", code)
	}
}

// TestFlatReplies: with ui.threaded_replies off a reply is a new comment.
func TestFlatReplies(t *testing.T) {
	ts, _ := toolsServer(t, func(o *Options) { o.UI.ThreadedReplies = "off" })
	iu := ts.URL + "/api/issues/DEMO-4"
	var is jira.Issue
	issueCall(t, "GET", iu, nil, &is)
	if len(is.Comments) == 0 {
		t.Fatal("no demo comments")
	}
	if code := issueCall(t, "POST", iu+"/comments", map[string]any{"Markdown": "agreed", "Parent": is.Comments[0].ID}, nil); code != 200 {
		t.Fatalf("reply: %d", code)
	}
	issueCall(t, "GET", iu+"?fresh=1", nil, &is)
	if last := is.Comments[len(is.Comments)-1]; last.Body != "agreed" || last.ParentID != "" {
		t.Errorf("flat reply = %+v", last)
	}
}

// TestPinsAndDocFields: a pin, on or off, is kept in the state file and
// comes with editmeta; a rich-text field reads and writes as markdown.
func TestPinsAndDocFields(t *testing.T) {
	ts, _ := toolsServer(t, nil)
	var pins map[string]bool
	if code := issueCall(t, "PUT", ts.URL+"/api/fields/pinned/customfield_10030", map[string]any{"On": true}, &pins); code != 200 || len(pins) != 1 {
		t.Fatalf("pin: %d %v", code, pins)
	}
	if code := issueCall(t, "PUT", ts.URL+"/api/fields/pinned/priority", map[string]any{"On": false}, &pins); code != 200 || len(pins) != 2 {
		t.Fatalf("unpin: %d %v", code, pins)
	}
	if code := issueCall(t, "PUT", ts.URL+"/api/fields/pinned/bad%20id", map[string]any{"On": true}, nil); code != 400 {
		t.Errorf("bad id: %d", code)
	}
	var meta struct{ Pins map[string]bool }
	issueCall(t, "GET", ts.URL+"/api/issues/DEMO-4/editmeta", nil, &meta)
	if on, ok := meta.Pins["priority"]; len(meta.Pins) != 2 || !meta.Pins["customfield_10030"] || on || !ok {
		t.Errorf("editmeta pins = %v", meta.Pins)
	}
	iu := ts.URL + "/api/issues/DEMO-4/doc/customfield_10040"
	var ed editable
	if code := issueCall(t, "GET", iu, nil, &ed); code != 200 || !ed.Editable || !strings.Contains(ed.Markdown, "Check:") {
		t.Fatalf("doc: %d %+v", code, ed)
	}
	if code := issueCall(t, "PUT", iu, mdBody{Markdown: "new notes"}, nil); code != 200 {
		t.Fatalf("set doc: %d", code)
	}
	if issueCall(t, "GET", iu, nil, &ed); ed.Markdown != "new notes" {
		t.Errorf("after = %+v", ed)
	}
	if code := issueCall(t, "GET", ts.URL+"/api/issues/DEMO-4/doc/summary", nil, nil); code != 400 {
		t.Errorf("summary as a doc: %d", code)
	}
}
