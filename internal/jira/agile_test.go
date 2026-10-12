package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCardsPagesAndDecodes: the board issue list is paged until the total, and
// each issue flattens into a card with the board's points field.
func TestCardsPagesAndDecodes(t *testing.T) {
	var jqls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/field" {
			io.WriteString(w, `[{"id":"customfield_99","name":"Development","schema":{"custom":"com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf"}}]`)
			return
		}
		if r.URL.Path != "/rest/agile/1.0/board/7/issue" {
			t.Errorf("path = %q", r.URL.Path)
		}
		jqls = append(jqls, r.URL.Query().Get("jql"))
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"total": 2, "issues": [{"key": "ABC-%d", "fields": {
			"summary": "Card %d", "status": {"id": "3", "name": "In progress"},
			"issuetype": {"name": "Bug"}, "assignee": {"accountId": "a1", "displayName": "Ada"},
			"customfield_1": 2.5,
			"customfield_99": "{pullrequest={dataType=pullrequest, state=OPEN, stateCount=1}, json={}}"}}]}`, start+1, start+1)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	cards, total, err := c.BoardIssues(context.Background(), 7, "status != Done", "customfield_1")
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(cards) != 2 || cards[1].Key != "ABC-2" {
		t.Fatalf("total=%d cards=%+v", total, cards)
	}
	want := Card{Key: "ABC-1", Summary: "Card 1", Type: "Bug", Status: "In progress", StatusID: "3", Assignee: "Ada", AssigneeID: "a1", Points: "2.5", PR: "OPEN"}
	if cards[0] != want {
		t.Errorf("card = %+v, want %+v", cards[0], want)
	}
	if jqls[0] != "status != Done" {
		t.Errorf("jql = %q", jqls[0])
	}
}

func TestBoardConfiguration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"columnConfig": {"columns": [
			{"name": "To do", "statuses": [{"id": "1"}, {"id": "2"}]},
			{"name": "Done", "statuses": [{"id": "5"}]}]},
			"estimation": {"type": "field", "field": {"fieldId": "customfield_9"}}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	cfg, err := c.BoardConfiguration(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PointsField != "customfield_9" || len(cfg.Columns) != 2 || len(cfg.Columns[0].StatusIDs) != 2 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestTransitionMetaAndRules(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/issue/ABC-1/transitions" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&sent)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/rest/api/3/issue/ABC-1/transitions":
			_, _ = w.Write([]byte(`{"transitions": [{"id": "15", "name": "Code review", "hasScreen": true,
				"to": {"id": "10004", "name": "Code review"},
				"fields": {"customfield_2": {"name": "Code Reviewer", "schema": {"type": "user"}},
					"customfield_3": {"name": "Tester", "schema": {"type": "array", "items": "user"}},
					"customfield_4": {"name": "Info", "schema": {"type": "string", "custom": "x:textarea"}}}}]}`))
		case r.URL.Path == "/rest/api/3/project/ABC":
			_, _ = w.Write([]byte(`{"id": "10"}`))
		case r.URL.Path == "/rest/api/3/workflowscheme/project":
			_, _ = w.Write([]byte(`{"values": [{"workflowScheme": {"defaultWorkflow": "W", "issueTypeMappings": {"5": "Epic W"}}}]}`))
		case r.URL.Path == "/rest/api/3/workflow/search":
			if r.URL.Query().Get("workflowName") != "W" {
				t.Errorf("workflow = %q, want W", r.URL.Query().Get("workflowName"))
			}
			_, _ = w.Write([]byte(`{"values": [{"id": {"name": "W"}, "transitions": [{"id": "15", "rules": {"validators": [
				{"type": "FieldRequiredValidator", "configuration": {"fields": ["customfield_2"], "errorMessage": "Vul de code reviewer in."}},
				{"type": "SomethingElse", "configuration": {}}]}}]}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	ctx := context.Background()

	metas, err := c.TransitionsMeta(ctx, "ABC-1")
	if err != nil || len(metas) != 1 {
		t.Fatalf("metas = %+v, err %v", metas, err)
	}
	kinds := map[string]string{}
	for _, f := range metas[0].Fields {
		kinds[f.Name] = f.Kind
	}
	if kinds["Code Reviewer"] != KindUser || kinds["Tester"] != KindUsers || kinds["Info"] != KindDoc {
		t.Errorf("kinds = %v", kinds)
	}

	rules, err := c.TransitionRules(ctx, "ABC", "1")
	if err != nil {
		t.Fatal(err)
	}
	if r := rules["15"]; len(r.Required) != 1 || r.Required[0] != "customfield_2" || r.Message != "Vul de code reviewer in." {
		t.Errorf("rule = %+v", r)
	}

	v, _, _ := EncodeValue(KindUser, Value{Users: []User{{AccountID: "a1"}}})
	if err := c.TransitionWith(ctx, "ABC-1", "15", map[string]any{"customfield_2": v}, "why"); err != nil {
		t.Fatal(err)
	}
	if sent["transition"].(map[string]any)["id"] != "15" || sent["fields"].(map[string]any)["customfield_2"].(map[string]any)["accountId"] != "a1" {
		t.Errorf("sent = %v", sent)
	}
	if _, ok := sent["update"].(map[string]any)["comment"]; !ok {
		t.Errorf("no comment in %v", sent)
	}
}

func TestDecodeValue(t *testing.T) {
	if v := DecodeValue(KindUsers, json.RawMessage(`[{"accountId": "a", "displayName": "A"}]`)); len(v.Users) != 1 || v.Empty() {
		t.Errorf("users = %+v", v)
	}
	doc := `{"type": "doc", "version": 1, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "steps"}]}]}`
	if v := DecodeValue(KindDoc, json.RawMessage(doc)); v.Text != "steps" {
		t.Errorf("doc = %q", v.Text)
	}
	// A doc round-trips its markdown, not flattened text.
	enc, _, _ := EncodeValue(KindDoc, DecodeValue(KindDoc, json.RawMessage(`{"type": "doc", "version": 1, "content": [
		{"type": "paragraph", "content": [{"type": "text", "text": "bold", "marks": [{"type": "strong"}]}]},
		{"type": "bulletList", "content": [{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "a"}]}]}]}]}`)))
	if b, _ := json.Marshal(enc); !strings.Contains(string(b), `"strong"`) || !strings.Contains(string(b), `"bulletList"`) || strings.Contains(string(b), "**") {
		t.Errorf("doc encoded = %s", b)
	}
	if !DecodeValue(KindUser, json.RawMessage(`null`)).Empty() {
		t.Error("null not empty")
	}
	// The people a doc mentions (Users) are written as mentions.
	enc, _, _ = EncodeValue(KindDoc, Value{Text: "Hi @Gaia Giovanelli, @Bo", Users: []User{{AccountID: "g1", DisplayName: "Gaia Giovanelli"}}})
	if b, _ := json.Marshal(enc); !strings.Contains(string(b), `"type":"mention"`) || !strings.Contains(string(b), `"id":"g1"`) || !strings.Contains(string(b), `"text":", @Bo"`) {
		t.Errorf("mention encoded = %s", b)
	}
}

// TestCardLimit: paging stops at the configured limit, the total stays the
// server's.
func TestCardLimit(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/field" {
			io.WriteString(w, `[]`)
			return
		}
		requests.Add(1)
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		var issues []string
		for i := start; i < start+10; i++ {
			issues = append(issues, fmt.Sprintf(`{"key": "ABC-%d", "fields": {}}`, i))
		}
		fmt.Fprintf(w, `{"total": 1000, "issues": [%s]}`, strings.Join(issues, ","))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", CardLimit: 25})
	cards, total, err := c.BoardIssues(context.Background(), 7, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 25 || total != 1000 || requests.Load() != 3 {
		t.Errorf("cards=%d total=%d requests=%d", len(cards), total, requests.Load())
	}
	if New(Config{}).cardLimit != DefaultCardLimit {
		t.Error("zero limit is not the default")
	}
}

// TestSprints: active sprints come first, with their dates and goal.
func TestSprints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values": [{"id": 2, "name": "S2", "state": "future"},
			{"id": 1, "name": "S1", "state": "active", "startDate": "2026-09-21T08:00:00.000Z",
			 "endDate": "2026-10-03T16:00:00.000Z", "goal": " Ship it "}]}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	ss, err := c.Sprints(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 || ss[0].ID != 1 || ss[0].Goal != "Ship it" || ss[0].End.Day() != 3 || !ss[1].Start.IsZero() {
		t.Errorf("sprints = %+v", ss)
	}
}

// TestClosedSprints: state=closed, the last closed first, with when.
func TestClosedSprints(t *testing.T) {
	var q string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query().Get("state")
		fmt.Fprint(w, `{"isLast": true, "values": [{"id": 1, "name": "S1", "state": "closed", "completeDate": "2026-09-14T15:00:00.000Z"},
			{"id": 2, "name": "S2", "state": "closed", "completeDate": "2026-09-28T15:00:00.000Z"}]}`)
	}))
	defer srv.Close()
	ss, err := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"}).ClosedSprints(context.Background(), 7)
	if err != nil || q != "closed" || len(ss) != 2 || ss[0].ID != 2 || ss[0].Complete.Day() != 28 {
		t.Errorf("sprints = %+v, %v (state %q)", ss, err, q)
	}
}

func TestRank(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = r.Method + " " + r.URL.Path + " " + string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if err := c.Rank(context.Background(), "ABC-2", "ABC-1", false); err != nil {
		t.Fatal(err)
	}
	if got != `PUT /rest/agile/1.0/issue/rank {"issues":["ABC-2"],"rankBeforeIssue":"ABC-1"}` {
		t.Errorf("request = %s", got)
	}
}

// TestRankKeys: past 50 keys a second request ranks the rest after the last of the first.
func TestRankKeys(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Issues []string
			After  string `json:"rankAfterIssue"`
			Before string `json:"rankBeforeIssue"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		got = append(got, fmt.Sprintf("%d %s..%s before=%s after=%s", len(b.Issues), b.Issues[0], b.Issues[len(b.Issues)-1], b.Before, b.After))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	keys := make([]string, 60)
	for i := range keys {
		keys[i] = fmt.Sprintf("ABC-%d", i+10)
	}
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if err := c.RankKeys(context.Background(), keys, "ABC-1", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"50 ABC-10..ABC-59 before=ABC-1 after=", "10 ABC-60..ABC-69 before= after=ABC-59"}
	if !slices.Equal(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// TestRankRefused: a 207 with a per-issue error is a failed rank.
func TestRankRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `{"entries":[{"issueId":10002,"issueKey":"ABC-2","status":400,"errors":["Rank not possible"]}]}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if err := c.Rank(context.Background(), "ABC-2", "ABC-1", false); err == nil || !strings.Contains(err.Error(), "Rank not possible") {
		t.Errorf("err = %v", err)
	}
}

func TestFavouriteFilters(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `[{"id":"10100","name":"My bugs","jql":"type = Bug ORDER BY created DESC"}]`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	for range 2 {
		got, err := c.FavouriteFilters(context.Background())
		if err != nil || len(got) != 1 || got[0].ID != 10100 || got[0].Name != "My bugs" {
			t.Fatalf("%+v, %v", got, err)
		}
	}
	if calls != 1 {
		t.Errorf("%d calls, want 1 (cached)", calls)
	}
}

func TestStartAndCloseSprint(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	start := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	if err := c.StartSprint(context.Background(), 12, start, start.AddDate(0, 0, 14)); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseSprint(context.Background(), 11); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != `POST /rest/agile/1.0/sprint/12 {"endDate":"2026-10-12T09:00:00Z","startDate":"2026-09-28T09:00:00Z","state":"active"}` ||
		got[1] != `POST /rest/agile/1.0/sprint/11 {"state":"closed"}` {
		t.Errorf("requests = %q", got)
	}
}

func TestPRState(t *testing.T) {
	for in, want := range map[string]string{
		`"{pullrequest={dataType=pullrequest, state=MERGED, stateCount=2}, build={}}"`: "MERGED",
		`"{branch={count=1}}"`: "",
		`null`:                 "",
	} {
		if got := prState(json.RawMessage(in)); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestDeployEnv(t *testing.T) {
	for in, want := range map[string]string{
		`"{pullrequest={state=OPEN}, json={\"cachedValue\":{\"summary\":{\"deployment-environment\":{\"overall\":{\"topEnvironments\":[{\"title\":\"production\"},{\"title\":\"staging\"}],\"count\":2}}}}}}"`: "production",
		`"{json={\"cachedValue\":{\"summary\":{\"deployment-environment\":{\"overall\":{\"topEnvironments\":[]}}}}}}"`:                                                                                         "",
		`"{pullrequest={state=OPEN}}"`: "",
		`null`:                         "",
	} {
		if got := deployEnv(json.RawMessage(in)); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestCardSubtasks(t *testing.T) {
	f := map[string]json.RawMessage{"subtasks": json.RawMessage(`[
	  {"key":"A-2","fields":{"status":{"statusCategory":{"key":"done"}}}},
	  {"key":"A-3","fields":{"status":{"statusCategory":{"key":"indeterminate"}}}}]`)}
	if c := toCard("A-1", f, ""); c.Subtasks != 2 || c.SubtasksDone != 1 {
		t.Errorf("card = %+v", c)
	}
}

func TestFlagSet(t *testing.T) {
	if !flagSet(json.RawMessage(`[{"value":"Impediment"}]`)) || flagSet(json.RawMessage(`null`)) || flagSet(nil) {
		t.Error("flagSet")
	}
}

// TestMoveChunks: more than 50 issues go in several requests.
func TestMoveChunks(t *testing.T) {
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Issues []string `json:"issues"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sizes = append(sizes, len(body.Issues))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	keys := make([]string, 120)
	for i := range keys {
		keys[i] = fmt.Sprintf("A-%d", i)
	}
	if err := c.MoveToSprint(context.Background(), 9, keys...); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 3 || sizes[0] != 50 || sizes[2] != 20 {
		t.Errorf("sizes = %v", sizes)
	}
}

func TestSetFlaggedValue(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/field" {
			_, _ = w.Write([]byte(`[{"id":"customfield_50","name":"Flagged"}]`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	for value, want := range map[string]string{"": "Impediment", "Blocked": "Blocked"} {
		c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", FlagValue: value})
		if err := c.SetFlagged(context.Background(), "ABC-1", true); err != nil {
			t.Fatal(err)
		}
		if body != `{"fields":{"customfield_50":[{"value":"`+want+`"}]}}` {
			t.Errorf("%q: body = %s", value, body)
		}
	}
}

func TestToCardUpdated(t *testing.T) {
	c := toCard("ABC-1", map[string]json.RawMessage{"updated": json.RawMessage(`"2026-09-25T10:00:00.000+0000"`)}, "")
	if c.Updated.UTC().Format(time.RFC3339) != "2026-09-25T10:00:00Z" {
		t.Errorf("updated = %v", c.Updated)
	}
}

// TestCardsMoreFields: cards carry their current sprint and the configured
// custom fields by name, as text.
func TestCardsMoreFields(t *testing.T) {
	var fields string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/field":
			w.Write([]byte(`[{"id":"customfield_1","name":"Sprint","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}},
				{"id":"customfield_2","name":"Test type"},{"id":"customfield_3","name":"Team"},{"id":"customfield_4","name":"Unused"}]`))
		default:
			fields = r.URL.Query().Get("fields")
			w.Write([]byte(`{"total":1,"issues":[{"key":"ABC-1","fields":{"summary":"x",
				"customfield_1":[{"name":"Sprint 3","state":"closed"},{"name":"Sprint 4","state":"active"}],
				"customfield_2":{"value":"e2e"},"customfield_3":[{"value":"Web"},{"value":"App"}]}}]}`))
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", CustomFields: []string{"Test type", "team", "Nope"}})
	cards, _, err := c.BoardIssues(context.Background(), 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fields, "customfield_1") || !strings.Contains(fields, "customfield_2") || strings.Contains(fields, "customfield_4") {
		t.Errorf("fields = %s", fields)
	}
	if c := cards[0]; c.Sprint != "Sprint 4" || c.Extra != "Test type=e2e"+ExtraSep+"team=Web, App" {
		t.Errorf("card = %q %q", c.Sprint, c.Extra)
	}
}

// TestBoardsAndSprintsPage: boards, sprints and quick filters past 50, the next pages are fetched too.
func TestSimpleBoardType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			fmt.Fprint(w, `{"values":[{"id":1,"type":"simple"},{"id":2,"type":"simple"},{"id":3,"type":"simple"},{"id":4,"type":"kanban"}],"isLast":true}`)
		case "/rest/agile/1.0/board/1/features":
			fmt.Fprint(w, `{"features":[{"feature":"jsw.agility.backlog","state":"ENABLED"},{"feature":"jsw.agility.sprints","state":"ENABLED"}]}`)
		case "/rest/agile/1.0/board/2/features":
			fmt.Fprint(w, `{"features":[{"feature":"jsw.agility.sprints","state":"DISABLED"}]}`)
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	bs, err := c.Boards(context.Background(), "ABC")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range bs {
		got = append(got, b.Type)
	}
	if want := "scrum kanban kanban kanban"; strings.Join(got, " ") != want {
		t.Errorf("types = %v, want %s", got, want)
	}
}

func TestBoardsAndSprintsPage(t *testing.T) {
	page := func(start, n int, isLast bool) string {
		var vals []string
		for i := start; i < start+n; i++ {
			vals = append(vals, fmt.Sprintf(`{"id":%d,"name":"N%d","state":"future"}`, i+1, i+1))
		}
		return fmt.Sprintf(`{"values":[%s],"isLast":%v}`, strings.Join(vals, ","), isLast)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("startAt") == "50" {
			fmt.Fprint(w, page(50, 3, true))
			return
		}
		fmt.Fprint(w, page(0, 50, false))
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if bs, err := c.Boards(context.Background(), "ABC"); err != nil || len(bs) != 53 || bs[52].ID != 53 {
		t.Errorf("boards = %d, %v", len(bs), err)
	}
	if ss, err := c.Sprints(context.Background(), 7); err != nil || len(ss) != 53 {
		t.Errorf("sprints = %d, %v", len(ss), err)
	}
	if qs, err := c.QuickFilters(context.Background(), 7); err != nil || len(qs) != 53 {
		t.Errorf("quick filters = %d, %v", len(qs), err)
	}
}
