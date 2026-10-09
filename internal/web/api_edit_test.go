package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/demo"
	"github.com/cornedor/laneway/internal/jira"
)

func TestEditRoutes(t *testing.T) {
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ts := issueServer(t, base)
	iu := ts.URL + "/api/issues/DEMO-5"

	var pr []jira.Option
	if issueCall(t, "GET", ts.URL+"/api/priorities", nil, &pr) != 200 || len(pr) < 2 {
		t.Fatalf("priorities: %v", pr)
	}
	var us []jira.User
	if issueCall(t, "GET", ts.URL+"/api/users?issue=DEMO-5&q=", nil, &us) != 200 || len(us) == 0 {
		t.Fatalf("users: %v", us)
	}
	if issueCall(t, "GET", ts.URL+"/api/users", nil, nil) != 400 {
		t.Error("users without issue or project should be 400")
	}

	var cur jira.Issue
	issueCall(t, "GET", iu+"?fresh=1", nil, &cur)
	type reply struct{ Undo *edit }
	put := func(field string, e edit) (int, reply) {
		var r reply
		code := issueCall(t, "PUT", iu+"/field/"+field, e, &r)
		return code, r
	}

	orig := cur.PriorityID
	code, r := put("priority", edit{ID: pr[len(pr)-1].ID})
	if code != 200 || r.Undo == nil || r.Undo.ID != cur.PriorityID {
		t.Fatalf("priority: %d %+v (was %q)", code, r.Undo, cur.PriorityID)
	}
	if code, _ = put("priority", *r.Undo); code != 200 {
		t.Fatalf("priority undo: %d", code)
	}
	issueCall(t, "GET", iu+"?fresh=1", nil, &cur)
	if cur.PriorityID != orig {
		t.Errorf("priority not restored: %q, was %q", cur.PriorityID, orig)
	}

	code, r = put("summary", edit{Text: "  New summary "})
	if code != 200 || r.Undo == nil || r.Undo.Text == "" {
		t.Fatalf("summary: %d %+v", code, r.Undo)
	}
	issueCall(t, "GET", iu+"?fresh=1", nil, &cur)
	if cur.Summary != "New summary" {
		t.Errorf("summary = %q", cur.Summary)
	}
	if code, _ := put("summary", edit{Text: " "}); code != 400 {
		t.Errorf("blank summary: %d", code)
	}

	code, r = put("assignee", edit{ID: us[0].AccountID})
	if code != 200 || r.Undo == nil {
		t.Fatalf("assignee: %d", code)
	}
	issueCall(t, "GET", iu+"?fresh=1", nil, &cur)
	if cur.AssigneeAccountID != us[0].AccountID {
		t.Errorf("assignee = %q, want %q", cur.AssigneeAccountID, us[0].AccountID)
	}
	code, r = put("labels", edit{Add: []string{"web-test"}})
	if code != 200 || r.Undo == nil || len(r.Undo.Remove) != 1 {
		t.Fatalf("labels: %d %+v", code, r.Undo)
	}
	if code, _ := put("points", edit{Text: "x"}); code == 200 {
		t.Error("non-numeric points accepted")
	}
	if code, _ := put("duedate", edit{Text: "nonsense"}); code != 400 {
		t.Errorf("bad due date: %d", code)
	}
	code, r = put("parent", edit{Text: "DEMO-1"})
	if code != 200 || r.Undo == nil || r.Undo.Field != "parent" {
		t.Fatalf("parent: %d %+v", code, r.Undo)
	}
	// the undo's own undo is the parent just set
	if code, r = put("parent", *r.Undo); code != 200 || r.Undo == nil || r.Undo.Text != "DEMO-1" {
		t.Fatalf("parent undo: %d %+v", code, r.Undo)
	}
	if code, _ := put("parent", edit{Text: "not a key"}); code != 400 {
		t.Errorf("bad parent: %d", code)
	}
	if code, _ := put("estimate", edit{Text: "2d 4h"}); code != 200 {
		t.Errorf("estimate: %d", code)
	}
	for _, bad := range []string{"", "soon", "2h later"} {
		if code, _ := put("estimate", edit{Text: bad}); code != 400 {
			t.Errorf("estimate %q: %d", bad, code)
		}
	}
	var em struct {
		Fields []jira.FieldMeta
		Values map[string]jira.Value
	}
	issueCall(t, "GET", iu+"/editmeta", nil, &em)
	comps := slices.IndexFunc(em.Fields, func(f jira.FieldMeta) bool { return f.ID == "components" })
	if comps < 0 || len(em.Fields[comps].Options) == 0 {
		t.Fatalf("editmeta fields = %+v", em.Fields)
	}
	pick := em.Fields[comps].Options[:1]
	if code, r := put("components", edit{Kind: em.Fields[comps].Kind, Value: jira.Value{Options: pick}}); code != 200 || r.Undo == nil {
		t.Fatalf("components: %d %+v", code, r.Undo)
	}
	issueCall(t, "GET", iu+"/editmeta", nil, &em)
	if v := em.Values["components"]; len(v.Options) != 1 || v.Options[0].ID != pick[0].ID {
		t.Errorf("components after = %+v", v)
	}
	var ls []string
	if code := issueCall(t, "GET", ts.URL+"/api/labels?q=front", nil, &ls); code != 200 || !slices.Contains(ls, "frontend") {
		t.Errorf("labels: %d %v", code, ls)
	}
	if code := issueCall(t, "GET", ts.URL+"/api/labels?q=x&field=summary", nil, nil); code != 400 {
		t.Errorf("labels of another field: %d", code)
	}
	if code, _ := put("nonsense", edit{}); code != 400 {
		t.Errorf("unknown field: %d", code)
	}

	code, r = put("status", edit{To: "done"})
	if code != 200 || r.Undo == nil || r.Undo.To == "" {
		t.Fatalf("status: %d %+v", code, r.Undo)
	}
	if code, _ = put("status", edit{To: "no such status"}); code != 400 {
		t.Errorf("unknown status: %d", code)
	}
}

// TestEditRequired: editmeta's required reaches the page, and the field
// route refuses emptying a required field itself, a stale page or not;
// another field still clears.
func TestEditRequired(t *testing.T) {
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ts := issueServer(t, base)
	iu := ts.URL + "/api/issues/DEMO-5"
	var em struct{ Fields []jira.FieldMeta }
	issueCall(t, "GET", iu+"/editmeta", nil, &em)
	i := slices.IndexFunc(em.Fields, func(f jira.FieldMeta) bool { return f.Name == "Team" })
	if i < 0 || !em.Fields[i].Required || slices.ContainsFunc(em.Fields, func(f jira.FieldMeta) bool { return f.Required && f.Name != "Team" }) {
		t.Fatalf("editmeta: %+v", em.Fields)
	}
	team := em.Fields[i]
	put := func(id, kind string, v jira.Value) (int, string) {
		var out struct{ Error string }
		code := issueCall(t, "PUT", iu+"/field/"+id, edit{Kind: kind, Value: v}, &out)
		return code, out.Error
	}
	if code, msg := put(team.ID, team.Kind, jira.Value{Options: team.Options[:1]}); code != 200 {
		t.Fatalf("set team: %d %s", code, msg)
	}
	if code, msg := put(team.ID, team.Kind, jira.Value{}); code != 400 || msg != "Team is required" {
		t.Errorf("empty team: %d %q", code, msg)
	}
	var after struct{ Values map[string]jira.Value }
	if issueCall(t, "GET", iu+"/editmeta", nil, &after); len(after.Values[team.ID].Options) != 1 {
		t.Errorf("team after a refused clear: %+v", after.Values[team.ID])
	}
	legacy := slices.IndexFunc(em.Fields, func(f jira.FieldMeta) bool { return f.Name == "Legacy ref" })
	if code, msg := put(em.Fields[legacy].ID, em.Fields[legacy].Kind, jira.Value{}); code != 200 {
		t.Errorf("empty legacy ref: %d %s", code, msg)
	}
}

func TestEditMetaAndCreate(t *testing.T) {
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ts := issueServer(t, base)
	iu := ts.URL + "/api/issues/DEMO-5"

	var tm struct{ Transitions []moveOption }
	if issueCall(t, "GET", iu+"/transitionmeta", nil, &tm) != 200 || len(tm.Transitions) == 0 {
		t.Fatalf("transitionmeta: %+v", tm)
	}
	var types struct{ Types []jira.Option }
	if issueCall(t, "GET", ts.URL+"/api/projects/DEMO/issuetypes", nil, &types) != 200 || len(types.Types) == 0 {
		t.Fatalf("issuetypes: %+v", types)
	}
	var cf []jira.CreateField
	if issueCall(t, "GET", ts.URL+"/api/projects/DEMO/createfields?type="+types.Types[0].Name, nil, &cf) != 200 || len(cf) == 0 {
		t.Fatalf("createfields: %+v", cf)
	}
	var em map[string]any
	if code := issueCall(t, "GET", iu+"/editmeta", nil, &em); code != 200 {
		t.Fatalf("editmeta: %d", code)
	}

	var made struct{ Key string }
	body := map[string]any{"Project": "DEMO", "Type": types.Types[0].Name, "Summary": "From the web", "Description": "hello"}
	if issueCall(t, "POST", ts.URL+"/api/issues", body, &made) != 200 || made.Key == "" {
		t.Fatalf("create: %+v", made)
	}
	if issueCall(t, "POST", ts.URL+"/api/issues", map[string]any{"Project": "DEMO"}, nil) == 200 {
		t.Error("create without summary should fail")
	}

	var draft struct{ Project, Type, Summary, Description, Parent, Note string }
	if issueCall(t, "GET", iu+"/clonedraft", nil, &draft) != 200 || !strings.HasPrefix(draft.Summary, "CLONE - ") || draft.Description == "" {
		t.Fatalf("clonedraft: %+v", draft)
	}
	var orig, clone jira.Issue
	issueCall(t, "GET", iu+"?fresh=1", nil, &orig)
	clonePost := func(desc string) jira.Issue {
		t.Helper()
		var made struct{ Key string }
		b := map[string]any{"Project": draft.Project, "Type": draft.Type, "Summary": draft.Summary, "Description": desc, "Parent": draft.Parent, "CloneOf": "DEMO-5"}
		if code := issueCall(t, "POST", ts.URL+"/api/issues", b, &made); code != 200 || made.Key == "" {
			t.Fatalf("clone: %d %+v", code, made)
		}
		var iss jira.Issue
		issueCall(t, "GET", ts.URL+"/api/issues/"+made.Key+"?fresh=1", nil, &iss)
		return iss
	}
	if clone = clonePost(draft.Description); clone.Description != orig.Description || clone.Summary != draft.Summary {
		t.Errorf("untouched clone: %q / %q, want the original's %q", clone.Summary, clone.Description, orig.Description)
	}
	if clone = clonePost("Edited"); !strings.Contains(clone.Description, "Edited") {
		t.Errorf("edited clone description = %q", clone.Description)
	}
	if issueCall(t, "POST", ts.URL+"/api/issues", map[string]any{"Project": "DEMO", "Type": "Task", "Summary": "x", "CloneOf": "bad"}, nil) != 400 {
		t.Error("bad CloneOf should be 400")
	}

	// DEMO-6 blocks DEMO-5
	if code := issueCall(t, "POST", iu+"/links", map[string]any{"Type": "Blocks", "Other": "DEMO-6", "Outward": false}, nil); code != 200 {
		t.Fatalf("link: %d", code)
	}
	var deps struct {
		Root              jira.DepNode
		BlockedBy, Blocks []jira.DepNode
	}
	if code := issueCall(t, "GET", iu+"/deps", nil, &deps); code != 200 || deps.Root.Key != "DEMO-5" || deps.Blocks == nil {
		t.Fatalf("deps: %d %+v", code, deps)
	}
	if len(deps.BlockedBy) != 1 || deps.BlockedBy[0].Key != "DEMO-6" {
		t.Errorf("blocked by = %+v, want DEMO-6", deps.BlockedBy)
	}

	var res struct {
		Done   []string
		Failed map[string]string
		Undo   map[string]edit
	}
	code := issueCall(t, "POST", ts.URL+"/api/bulk", map[string]any{"Keys": []string{"DEMO-5", "DEMO-6", "NOPE"}, "Edit": edit{Field: "labels", Add: []string{"bulk"}}}, &res)
	if code != 200 || len(res.Done) != 2 || len(res.Failed) != 1 || len(res.Undo) != 2 {
		t.Fatalf("bulk: %d %+v", code, res)
	}
	code = issueCall(t, "POST", ts.URL+"/api/bulk", map[string]any{"Each": res.Undo}, &res)
	if code != 200 || len(res.Done) != 2 {
		t.Fatalf("bulk undo: %d %+v", code, res)
	}
	if issueCall(t, "POST", ts.URL+"/api/bulk", map[string]any{}, nil) != 400 {
		t.Error("empty bulk should be 400")
	}
	if issueCall(t, "DELETE", ts.URL+"/api/issues/DEMO-7", nil, nil) != 200 {
		t.Error("delete failed")
	}
}

// TestCreateFieldErrors: Jira's reasons for refusing a create come back by
// field, for the form to show under each.
func TestCreateFieldErrors(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"errorMessages":[],"errors":{"summary":"Summary is too long","customfield_10030":"Team is required"}}`)
	}))
	t.Cleanup(fake.Close)
	ts := issueServer(t, fake.URL)
	var out struct {
		Error  string
		Fields map[string]string
	}
	code := issueCall(t, "POST", ts.URL+"/api/issues", map[string]any{"Project": "DEMO", "Type": "Task", "Summary": "x"}, &out)
	if code == 200 || out.Fields["summary"] != "Summary is too long" || out.Fields["customfield_10030"] != "Team is required" {
		t.Fatalf("%d %+v", code, out)
	}
}

// TestTransitionWithMentions: the move's comment is markdown whose people
// (Users) are written as mentions, in the transition request itself.
func TestTransitionWithMentions(t *testing.T) {
	var sent map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transitions") {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(fake.Close)
	ts := issueServer(t, fake.URL)
	body := map[string]any{"ID": "13", "Fields": []map[string]any{{"ID": "comment", "Kind": "comment",
		"Value": map[string]any{"Text": "**Waiting** on @Gaia Giovanelli", "Users": []map[string]string{{"AccountID": "g1", "DisplayName": "Gaia Giovanelli"}}}}}}
	if code := issueCall(t, "POST", ts.URL+"/api/issues/DEMO-1/transitionwith", body, nil); code != 200 {
		t.Fatalf("transitionwith: %d", code)
	}
	b, _ := json.Marshal(sent["update"])
	if !strings.Contains(string(b), `"type":"mention"`) || !strings.Contains(string(b), `"id":"g1"`) || !strings.Contains(string(b), `"strong"`) {
		t.Errorf("update = %s", b)
	}
}
