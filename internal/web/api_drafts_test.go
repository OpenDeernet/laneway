package web

import (
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/demo"
)

func TestDrafts(t *testing.T) {
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ts := issueServer(t, base)
	u := ts.URL + "/api/drafts/"
	var d struct{ Text string }
	if issueCall(t, "GET", u+"comment:DEMO-1", nil, &d) != 200 || d.Text != "" {
		t.Fatalf("no draft = %+v", d)
	}
	for _, id := range []string{"desc:DEMO-1", "desc:DEMO-1:comment:10001", "comment:DEMO-2", "create", "wiki:10002", "wiki:10002:comment:new", "wiki:10002:comment:10005"} {
		if code := issueCall(t, "PUT", u+id, map[string]string{"Text": "half a\nthought"}, nil); code != 200 {
			t.Fatalf("put %s: %d", id, code)
		}
		if issueCall(t, "GET", u+id, nil, &d); d.Text != "half a\nthought" {
			t.Errorf("%s = %q", id, d.Text)
		}
	}
	for _, id := range []string{"wiki:x", "wiki:1:comment:", "wiki:1:field:a"} {
		if code := issueCall(t, "PUT", u+id, map[string]string{"Text": "x"}, nil); code != 400 {
			t.Errorf("bad wiki draft id %s: %d", id, code)
		}
	}
	var db struct{ Text, Base string }
	issueCall(t, "PUT", u+"desc:DEMO-1", map[string]string{"Text": "x", "Base": "0a1b"}, nil)
	if issueCall(t, "GET", u+"desc:DEMO-1", nil, &db); db.Text != "x" || db.Base != "0a1b" {
		t.Errorf("draft with a base = %+v", db)
	}
	if code := issueCall(t, "PUT", u+"desc:DEMO-1", map[string]string{"Text": "x", "Base": "a b\nc"}, nil); code != 400 {
		t.Errorf("bad base: %d", code)
	}
	issueCall(t, "DELETE", u+"desc:DEMO-1", nil, nil)
	if issueCall(t, "GET", u+"desc:DEMO-1", nil, &d); d.Text != "" {
		t.Errorf("deleted draft = %q", d.Text)
	}
	issueCall(t, "PUT", u+"comment:DEMO-2", map[string]string{"Text": "  "}, nil)
	if issueCall(t, "GET", u+"comment:DEMO-2", nil, &d); d.Text != "" {
		t.Errorf("blank draft kept: %q", d.Text)
	}
	for _, bad := range []string{"comment:nope", "other:DEMO-1", "comment:DEMO-1:comment:1", "desc:DEMO-1:comment:x"} {
		if code := issueCall(t, "PUT", u+bad, map[string]string{"Text": "x"}, nil); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
}

func TestStarredJQL(t *testing.T) {
	ts := issueServer(t, "http://127.0.0.1:1")
	var on struct{ On bool }
	var list []string
	if issueCall(t, "POST", ts.URL+"/api/jql/starred", map[string]string{"JQL": " assignee = currentUser() "}, &on); !on.On {
		t.Fatal("not starred")
	}
	if issueCall(t, "GET", ts.URL+"/api/jql/starred", nil, &list); len(list) != 1 || list[0] != "assignee = currentUser()" {
		t.Fatalf("starred = %q", list)
	}
	if issueCall(t, "POST", ts.URL+"/api/jql/starred", map[string]string{"JQL": "assignee = currentUser()"}, &on); on.On {
		t.Fatal("not unstarred")
	}
	if issueCall(t, "GET", ts.URL+"/api/jql/starred", nil, &list); list == nil || len(list) != 0 {
		t.Errorf("after unstar = %q", list)
	}
	if issueCall(t, "POST", ts.URL+"/api/jql/starred", map[string]string{"JQL": " "}, nil) != 400 {
		t.Error("blank query starred")
	}
}
