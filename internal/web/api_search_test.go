package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/demo"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/store"
)

func searchDemo(t *testing.T) *httptest.Server {
	t.Helper()
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cl := jira.New(jira.Config{BaseURL: base, Email: "d@example.com", APIToken: "x", Projects: []string{"DEMO"}})
	s := New(context.Background(), Options{Client: cl, Jira: config.JiraConfig{}, Store: st, Site: "demo", Demo: true})
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

func searchGet(t *testing.T, url string, v any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if v != nil {
		_ = json.NewDecoder(res.Body).Decode(v)
	}
	return res.StatusCode
}

// TestSearchTotal: a search or filter view the card limit cuts off says how
// many issues Jira has.
func TestSearchTotal(t *testing.T) {
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cl := jira.New(jira.Config{BaseURL: base, Email: "d@example.com", APIToken: "x", CardLimit: 3})
	ts := httptest.NewServer(New(context.Background(), Options{Client: cl, Store: st, Site: "demo", Demo: true}))
	t.Cleanup(ts.Close)
	q := url.QueryEscape("project = DEMO")
	for _, path := range []string{"/api/search?jql=" + q, "/api/boards/1/viewcards?kind=filter&jql=" + q} {
		var out struct {
			Cards []jira.Card
			Total int
		}
		if code := searchGet(t, ts.URL+path, &out); code != 200 || len(out.Cards) != 3 || out.Total <= 3 {
			t.Errorf("%s: %d, %d cards of %d", path, code, len(out.Cards), out.Total)
		}
	}
}

func TestSearchRoutes(t *testing.T) {
	ts := searchDemo(t)
	var out struct{ Cards []jira.Card }
	if code := searchGet(t, ts.URL+"/api/search?jql=project%20%3D%20DEMO", &out); code != 200 || len(out.Cards) == 0 {
		t.Fatalf("search: %d, %d cards", code, len(out.Cards))
	}
	if code := searchGet(t, ts.URL+"/api/search", nil); code != 400 {
		t.Errorf("empty jql: %d, want 400", code)
	}
	word := strings.Fields(out.Cards[0].Summary)[0]
	if code := searchGet(t, ts.URL+"/api/find?q="+url.QueryEscape(word), nil); code != 200 {
		t.Errorf("find: %d", code)
	}
	find := func(query string) []jira.Card {
		t.Helper()
		var f struct{ Cards []jira.Card }
		if code := searchGet(t, ts.URL+"/api/find?"+query, &f); code != 200 {
			t.Fatalf("find %s: %d", query, code)
		}
		return f.Cards
	}
	key := out.Cards[0].Key
	if got := find("q=" + strings.ToLower(key)); len(got) != 1 || got[0].Key != key {
		t.Errorf("find by key %s: %+v", key, got)
	}
	if got := find("project=DEMO&q=" + key[strings.LastIndex(key, "-")+1:]); len(got) != 1 || got[0].Key != key {
		t.Errorf("find by number of %s: %+v", key, got)
	}
	typed := func(typ string) jira.Card {
		t.Helper()
		var o struct{ Cards []jira.Card }
		searchGet(t, ts.URL+"/api/search?jql="+url.QueryEscape("issuetype = "+typ), &o)
		if len(o.Cards) == 0 {
			t.Fatalf("no %s in the demo", typ)
		}
		return o.Cards[0]
	}
	story, sub, epic := typed("Story"), typed("Sub-task"), typed("Epic")
	// a story's parent is an epic; a typed key of another type finds nothing
	for _, c := range find("parentOf=" + story.Key + "&q=" + url.QueryEscape(strings.Fields(epic.Summary)[0])) {
		if c.Type != "Epic" {
			t.Errorf("story's parent candidate %s is a %s", c.Key, c.Type)
		}
	}
	if got := find("parentOf=" + story.Key + "&q=" + sub.Key); len(got) != 0 {
		t.Errorf("a subtask offered as a story's parent: %+v", got)
	}
	if got := find("project=DEMO&type=Story&q=" + epic.Key); len(got) != 1 {
		t.Errorf("new story's parent %s: %+v", epic.Key, got)
	}
	// a subtask's is a standard issue, not an epic nor itself
	if got := find("parentOf=" + sub.Key + "&q=" + epic.Key); len(got) != 0 {
		t.Errorf("an epic offered as a subtask's parent: %+v", got)
	}
	if got := find("parentOf=" + sub.Key + "&q=" + story.Key); len(got) != 1 {
		t.Errorf("subtask's parent %s: %+v", story.Key, got)
	}
	var words jira.JQLWords
	if code := searchGet(t, ts.URL+"/api/jql/words", &words); code != 200 || len(words.Fields) == 0 {
		t.Errorf("words: %d %v", code, words)
	}
	if code := searchGet(t, ts.URL+"/api/jql/values", nil); code != 400 {
		t.Errorf("values without field: %d, want 400", code)
	}
	var n struct{ Count int }
	if code := searchGet(t, ts.URL+"/api/jql/count?jql=project%20%3D%20DEMO", &n); code != 200 || n.Count != len(out.Cards) {
		t.Errorf("count: %d, %d, want %d", code, n.Count, len(out.Cards))
	}
	if code := searchGet(t, ts.URL+"/api/jql/count", nil); code != 400 {
		t.Errorf("count without jql: %d, want 400", code)
	}
	var vals []string
	if code := searchGet(t, ts.URL+"/api/jql/values?field=status&prefix=", &vals); code != 200 {
		t.Errorf("values: %d", code)
	}
	var fs []jira.QuickFilter
	if code := searchGet(t, ts.URL+"/api/filters", &fs); code != 200 || fs == nil {
		t.Errorf("filters: %d %v", code, fs)
	}
	res, err := http.Post(ts.URL+"/api/filters", "application/json", strings.NewReader(`{"Name":"","JQL":""}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("save empty filter: %d, want 400", res.StatusCode)
	}
}

// TestConfluenceRoutes: a demo page reads as markdown with its image, which
// the server fetches; ids must be numbers.
func TestConfluenceRoutes(t *testing.T) {
	ts := searchDemo(t)
	var links []struct{ URL, Page string }
	if c := searchGet(t, ts.URL+"/api/issues/DEMO-4/weblinks", &links); c != 200 || len(links) != 1 || links[0].Page == "" {
		t.Fatalf("weblinks %d %+v", c, links)
	}
	var p struct{ Title, Markdown string }
	if c := searchGet(t, ts.URL+"/api/confluence/pages/"+links[0].Page, &p); c != 200 || !strings.Contains(p.Markdown, "](confluence:") {
		t.Fatalf("page %d %+v", c, p)
	}
	id := p.Markdown[strings.Index(p.Markdown, "](confluence:")+len("](confluence:"):]
	id = id[:strings.IndexByte(id, ')')]
	res, err := http.Get(ts.URL + "/api/confluence/images/" + id)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Errorf("image: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, bad := range []string{"/api/confluence/images/x1", "/api/confluence/pages/1;2"} {
		if c := searchGet(t, ts.URL+bad, nil); c != 400 {
			t.Errorf("%s: %d", bad, c)
		}
	}
}
