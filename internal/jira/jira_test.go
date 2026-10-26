package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestADFToMarkdown(t *testing.T) {
	tests := []struct {
		name string
		adf  string
		want string
	}{
		{
			name: "nil description",
			adf:  `null`,
			want: "",
		},
		{
			name: "paragraph with marks",
			adf: `{"type":"doc","content":[
				{"type":"paragraph","content":[
					{"type":"text","text":"Hello "},
					{"type":"text","text":"bold","marks":[{"type":"strong"}]},
					{"type":"text","text":" and "},
					{"type":"text","text":"code","marks":[{"type":"code"}]}
				]}
			]}`,
			want: "Hello **bold** and `code`",
		},
		{
			name: "link mark",
			adf: `{"type":"doc","content":[
				{"type":"paragraph","content":[
					{"type":"text","text":"site","marks":[{"type":"link","attrs":{"href":"https://x.test"}}]}
				]}
			]}`,
			want: "[site](https://x.test)",
		},
		{
			name: "link href with parens and a space",
			adf: `{"type":"doc","content":[
				{"type":"paragraph","content":[
					{"type":"text","text":"Go","marks":[{"type":"link","attrs":{"href":"https://en.wikipedia.org/wiki/Go_(lang) x"}}]}
				]}
			]}`,
			want: "[Go](https://en.wikipedia.org/wiki/Go_%28lang%29%20x)",
		},
		{
			name: "heading and bullet list",
			adf: `{"type":"doc","content":[
				{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Title"}]},
				{"type":"bulletList","content":[
					{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]},
					{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}
				]}
			]}`,
			want: "## Title\n\n- one\n- two",
		},
		{
			name: "code block keeps verbatim",
			adf: `{"type":"doc","content":[
				{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"a := 1"}]}
			]}`,
			want: "```go\na := 1\n```",
		},
		{
			name: "inline date and status keep their attrs",
			adf: `{"type":"doc","content":[
				{"type":"paragraph","content":[{"type":"text","text":"Due "},
					{"type":"date","attrs":{"timestamp":"1790380800000"}},
					{"type":"text","text":" is "},
					{"type":"status","attrs":{"text":"Blocked","color":"red"}}]}
			]}`,
			want: `Due <date>2026-09-26</date> is <status color="red">BLOCKED</status>`,
		},
		{
			name: "date with a numeric timestamp",
			adf:  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"date","attrs":{"timestamp":1790380800000}}]}]}`,
			want: "<date>2026-09-26</date>",
		},
		{
			name: "unknown block degrades to text",
			adf: `{"type":"doc","content":[
				{"type":"panel","content":[{"type":"paragraph","content":[{"type":"text","text":"note"}]}]}
			]}`,
			want: "note",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adfToMarkdown(json.RawMessage(tt.adf))
			if got != tt.want {
				t.Errorf("adfToMarkdown()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestGetParsesIssue(t *testing.T) {
	const body = `{
		"key": "ABC-123",
		"fields": {
			"summary": "Fix the thing",
			"description": {"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"do it"}]}]},
			"labels": ["backend","urgent"],
			"updated": "2026-06-15T09:41:00.000+0200",
			"status": {"name": "In Progress", "statusCategory": {"key": "indeterminate"}},
			"priority": {"name": "High"},
			"issuetype": {"name": "Bug"},
			"assignee": {"displayName": "Ada Lovelace"},
			"reporter": {"displayName": "Alan Turing"}
		}
	}`

	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if !c.Enabled() {
		t.Fatal("client should be enabled")
	}
	iss, err := c.Get(context.Background(), "ABC-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("expected Basic auth, got %q", gotAuth)
	}
	if gotPath != "/rest/api/3/issue/ABC-123" {
		t.Errorf("path = %q", gotPath)
	}
	if iss.Summary != "Fix the thing" {
		t.Errorf("summary = %q", iss.Summary)
	}
	if iss.Status != "In Progress" || iss.StatusCategory != "indeterminate" || iss.Priority != "High" || iss.Type != "Bug" {
		t.Errorf("meta = %+v", iss)
	}
	if iss.Assignee != "Ada Lovelace" || iss.Reporter != "Alan Turing" {
		t.Errorf("people = %q / %q", iss.Assignee, iss.Reporter)
	}
	if iss.Description != "do it" {
		t.Errorf("description = %q", iss.Description)
	}
	if iss.Updated.IsZero() {
		t.Error("updated not parsed")
	}
	if iss.URL != srv.URL+"/browse/ABC-123" {
		t.Errorf("url = %q", iss.URL)
	}
}

func TestStoryPointsAutoDetected(t *testing.T) {
	const fieldMeta = `[
		{"id":"summary","name":"Summary"},
		{"id":"customfield_10016","name":"Story point estimate"},
		{"id":"customfield_99","name":"Sprint"}
	]`
	const issueBody = `{"key":"ABC-7","fields":{"summary":"s","assignee":{"displayName":"x"},"customfield_10016":5}}`

	var gotIssueQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/field":
			_, _ = w.Write([]byte(fieldMeta))
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/"):
			gotIssueQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(issueBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	iss, err := c.Get(context.Background(), "ABC-7")
	if err != nil {
		t.Fatal(err)
	}
	if iss.StoryPoints != "5" {
		t.Errorf("StoryPoints = %q, want 5", iss.StoryPoints)
	}
	if !strings.Contains(gotIssueQuery, "customfield_10016") {
		t.Errorf("issue request did not include the detected field: %q", gotIssueQuery)
	}
}

func TestStoryPointsOverrideSkipsMetadata(t *testing.T) {
	var metaCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/field":
			metaCalls++
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{"key":"ABC-8","fields":{"summary":"s","assignee":{"displayName":"x"},"customfield_42":2.5}}`))
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", StoryPointsField: "customfield_42"})
	iss, err := c.Get(context.Background(), "ABC-8")
	if err != nil {
		t.Fatal(err)
	}
	if iss.StoryPoints != "2.5" {
		t.Errorf("StoryPoints = %q, want 2.5", iss.StoryPoints)
	}
	if metaCalls != 0 {
		t.Errorf("expected no field-metadata call with an override, got %d", metaCalls)
	}
}

func TestStoryPointsAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/field" {
			_, _ = w.Write([]byte(`[{"id":"customfield_10016","name":"Story point estimate"}]`))
			return
		}
		// Issue has no story-points value set (null).
		_, _ = w.Write([]byte(`{"key":"ABC-9","fields":{"summary":"s","assignee":{"displayName":"x"},"customfield_10016":null}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	iss, err := c.Get(context.Background(), "ABC-9")
	if err != nil {
		t.Fatal(err)
	}
	if iss.StoryPoints != "" {
		t.Errorf("StoryPoints = %q, want empty", iss.StoryPoints)
	}
}

func TestStoryPointsMetadataFailureIsNonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/field" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"key":"ABC-1","fields":{"summary":"s","assignee":{"displayName":"x"}}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	iss, err := c.Get(context.Background(), "ABC-1")
	if err != nil {
		t.Fatalf("a down field-metadata endpoint must not fail the issue fetch: %v", err)
	}
	if iss.StoryPoints != "" || iss.Summary != "s" {
		t.Errorf("unexpected issue %+v", iss)
	}
}

func TestGetCachesAndInvalidates(t *testing.T) {
	var calls int // issue fetches only — the field-metadata probe is routed apart
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/field" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		calls++
		_, _ = w.Write([]byte(`{"key":"ABC-1","fields":{"summary":"s","assignee":{"displayName":"x"}}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), "ABC-1"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 HTTP call (cached), got %d", calls)
	}
	c.Invalidate("ABC-1")
	if _, err := c.Get(context.Background(), "ABC-1"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected refetch after Invalidate, got %d calls", calls)
	}
}

// TestGetRacingInvalidate: an Invalidate while a fetch is out keeps that
// fetch's (possibly stale) answer out of the cache.
func TestGetRacingInvalidate(t *testing.T) {
	var calls int
	var c *Client
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/field" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		calls++
		if calls == 1 {
			c.Invalidate("ABC-1") // a write lands mid-request
		}
		_, _ = w.Write([]byte(`{"key":"ABC-1","fields":{"summary":"s"}}`))
	}))
	defer srv.Close()

	c = New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	for i := 0; i < 2; i++ {
		if _, err := c.Get(context.Background(), "ABC-1"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Errorf("expected the raced fetch left uncached, got %d calls", calls)
	}
}

func TestGetUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	_, err := c.Get(context.Background(), "ABC-1")
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Errorf("expected auth error, got %v", err)
	}
}

func TestDisabledClient(t *testing.T) {
	c := New(Config{BaseURL: "https://example.atlassian.net"}) // no creds
	if c.Enabled() {
		t.Error("client without creds should be disabled")
	}
	if _, err := c.Get(context.Background(), "ABC-1"); err == nil {
		t.Error("expected error from disabled client")
	}
}

func TestGetParsesIDs(t *testing.T) {
	const body = `{"key":"ABC-1","fields":{"summary":"s",
		"priority":{"id":"2","name":"High"},
		"assignee":{"accountId":"acc-9","displayName":"Ada"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", StoryPointsField: "customfield_1"})
	iss, err := c.Get(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	if iss.PriorityID != "2" {
		t.Errorf("PriorityID = %q, want 2", iss.PriorityID)
	}
	if iss.AssigneeAccountID != "acc-9" {
		t.Errorf("AssigneeAccountID = %q, want acc-9", iss.AssigneeAccountID)
	}
}

func TestTransitions(t *testing.T) {
	const body = `{"transitions":[
		{"id":"11","name":"Start","to":{"id":"3","name":"In Progress"}},
		{"id":"21","name":"Done","to":{"id":"5","name":"Done"}}
	]}`
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	opts, err := c.Transitions(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/rest/api/3/issue/ABC-1/transitions" {
		t.Errorf("path = %q", gotPath)
	}
	if len(opts) != 2 || opts[0].ID != "11" || opts[0].Name != "In Progress" || opts[1].Name != "Done" {
		t.Errorf("transitions = %+v", opts)
	}
}

func TestDoTransitionPostsAndInvalidates(t *testing.T) {
	var gotMethod, gotBody string
	var issueCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/field":
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/transitions"):
			gotMethod = r.Method
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusNoContent)
		default: // GET issue
			issueCalls++
			_, _ = w.Write([]byte(`{"key":"ABC-1","fields":{"summary":"s"}}`))
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if _, err := c.Get(context.Background(), "ABC-1"); err != nil { // prime the cache
		t.Fatal(err)
	}
	if err := c.DoTransition(context.Background(), "ABC-1", "11"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.Contains(gotBody, `"transition"`) || !strings.Contains(gotBody, `"11"`) {
		t.Errorf("body = %q", gotBody)
	}
	// A transition must drop the cached copy so the next Get refetches.
	if _, err := c.Get(context.Background(), "ABC-1"); err != nil {
		t.Fatal(err)
	}
	if issueCalls != 2 {
		t.Errorf("expected refetch after transition, got %d issue calls", issueCalls)
	}
}

func TestPrioritiesCached(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`[{"id":"1","name":"Highest"},{"id":"2","name":"High"}]`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	for i := 0; i < 3; i++ {
		opts, err := c.Priorities(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(opts) != 2 || opts[0].Name != "Highest" {
			t.Errorf("priorities = %+v", opts)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 call (cached), got %d", calls)
	}
}

func TestSetPriority(t *testing.T) {
	var gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if err := c.SetPriority(context.Background(), "ABC-1", "3"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if !strings.Contains(gotBody, `"priority"`) || !strings.Contains(gotBody, `"3"`) {
		t.Errorf("body = %q", gotBody)
	}
}

func TestAssignableUsersAndMyself(t *testing.T) {
	var myselfCalls int
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/user/assignable/search":
			if r.URL.Query().Get("issueKey") != "ABC-1" {
				t.Errorf("issueKey = %q", r.URL.Query().Get("issueKey"))
			}
			gotQuery = r.URL.Query().Get("query")
			_, _ = w.Write([]byte(`[{"accountId":"a1","displayName":"Ada"},{"accountId":"a2","displayName":"Alan"}]`))
		case "/rest/api/3/myself":
			myselfCalls++
			_, _ = w.Write([]byte(`{"accountId":"me-1","displayName":"Me"}`))
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	users, err := c.AssignableUsers(context.Background(), "ABC-1", "ala")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "ala" {
		t.Errorf("forwarded query = %q, want ala", gotQuery)
	}
	if len(users) != 2 || users[0].AccountID != "a1" || users[1].DisplayName != "Alan" {
		t.Errorf("users = %+v", users)
	}
	for i := 0; i < 2; i++ {
		me, err := c.Myself(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if me.AccountID != "me-1" {
			t.Errorf("myself = %+v", me)
		}
	}
	if myselfCalls != 1 {
		t.Errorf("expected myself cached (1 call), got %d", myselfCalls)
	}
}

func TestSetAssignee(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		wantBody  string
	}{
		{"assign", "a1", `{"accountId":"a1"}`},
		{"unassign", "", `{"accountId":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody, gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				b, _ := io.ReadAll(r.Body)
				gotBody = strings.TrimSpace(string(b))
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
			if err := c.SetAssignee(context.Background(), "ABC-1", tt.accountID); err != nil {
				t.Fatal(err)
			}
			if gotMethod != http.MethodPut {
				t.Errorf("method = %q, want PUT", gotMethod)
			}
			if gotBody != tt.wantBody {
				t.Errorf("body = %q, want %q", gotBody, tt.wantBody)
			}
		})
	}
}

func TestSetStoryPoints(t *testing.T) {
	const fieldMeta = `[{"id":"customfield_10016","name":"Story point estimate"}]`
	var gotBody string
	newSrv := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/rest/api/3/field" {
				_, _ = w.Write([]byte(fieldMeta))
				return
			}
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusNoContent)
		}))
	}

	t.Run("set", func(t *testing.T) {
		srv := newSrv()
		defer srv.Close()
		c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
		if err := c.SetStoryPoints(context.Background(), "ABC-1", "5"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(gotBody, `"customfield_10016":5`) {
			t.Errorf("body = %q", gotBody)
		}
	})

	t.Run("clear", func(t *testing.T) {
		srv := newSrv()
		defer srv.Close()
		c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
		if err := c.SetStoryPoints(context.Background(), "ABC-1", ""); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(gotBody, `"customfield_10016":null`) {
			t.Errorf("body = %q", gotBody)
		}
	})

	t.Run("non-numeric", func(t *testing.T) {
		srv := newSrv()
		defer srv.Close()
		c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
		err := c.SetStoryPoints(context.Background(), "ABC-1", "lots")
		if err == nil || !strings.Contains(err.Error(), "must be a number") {
			t.Errorf("expected numeric error, got %v", err)
		}
	})

	t.Run("no field detected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[]`)) // field metadata returns nothing story-points-like
		}))
		defer srv.Close()
		c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
		err := c.SetStoryPoints(context.Background(), "ABC-1", "5")
		if err == nil || !strings.Contains(err.Error(), "no story-points field") {
			t.Errorf("expected no-field error, got %v", err)
		}
	})
}

func TestGetParsesComments(t *testing.T) {
	const body = `{
		"key": "ABC-1",
		"fields": {
			"summary": "s",
			"comment": {
				"total": 3,
				"comments": [
					{
						"author": {"accountId": "a1", "displayName": "Ada Lovelace"},
						"created": "2026-06-15T09:41:00.000+0200",
						"body": {"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"first note"}]}]}
					},
					{
						"author": {"accountId": "a2", "displayName": "Alan Turing"},
						"created": "2026-06-16T10:00:00.000+0200",
						"body": {"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"reply cc "},{"type":"mention","attrs":{"id":"a3","text":"@Grace Hopper"}},{"type":"mention","attrs":{"id":"a1","text":"@Ada Lovelace"}}]}]}
					}
				]
			}
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	iss, err := c.Get(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	if iss.CommentTotal != 3 {
		t.Errorf("CommentTotal = %d, want 3", iss.CommentTotal)
	}
	if len(iss.Comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(iss.Comments))
	}
	if iss.Comments[0].Author != "Ada Lovelace" || iss.Comments[0].AuthorID != "a1" || iss.Comments[0].Body != "first note" {
		t.Errorf("comment[0] = %+v", iss.Comments[0])
	}
	if iss.Comments[0].Created.IsZero() {
		t.Error("comment[0] created not parsed")
	}
	if iss.Comments[1].Author != "Alan Turing" || iss.Comments[1].Body != "reply cc @Grace Hopper@Ada Lovelace" {
		t.Errorf("comment[1] = %+v", iss.Comments[1])
	}
	if want := []User{{"a3", "Grace Hopper"}, {"a1", "Ada Lovelace"}}; !slices.Equal(iss.Mentioned, want) {
		t.Errorf("Mentioned = %v, want %v", iss.Mentioned, want)
	}
}

func TestAddCommentPostsAndInvalidates(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	var issueCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/field":
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/comment") && r.Method == http.MethodPost:
			gotMethod, gotPath = r.Method, r.URL.Path
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"10001"}`))
		default: // GET issue
			issueCalls++
			_, _ = w.Write([]byte(`{"key":"ABC-1","fields":{"summary":"s"}}`))
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if _, err := c.Get(context.Background(), "ABC-1"); err != nil { // prime the cache
		t.Fatal(err)
	}
	if err := c.AddComment(context.Background(), "ABC-1", "looks good", nil); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/rest/api/3/issue/ABC-1/comment" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"type":"doc"`) || !strings.Contains(gotBody, `"looks good"`) {
		t.Errorf("body = %q", gotBody)
	}
	// Posting a comment must drop the cached copy so the next Get refetches.
	if _, err := c.Get(context.Background(), "ABC-1"); err != nil {
		t.Fatal(err)
	}
	if issueCalls != 2 {
		t.Errorf("expected refetch after comment, got %d issue calls", issueCalls)
	}
}

func TestTextToADF(t *testing.T) {
	t.Run("paragraphs and hardBreaks", func(t *testing.T) {
		doc := textToADF("line one\nline two\n\nsecond para", nil)
		raw, _ := json.Marshal(doc)
		s := string(raw)
		if !strings.Contains(s, `"type":"doc"`) || !strings.Contains(s, `"version":1`) {
			t.Errorf("doc shape = %s", s)
		}
		if !strings.Contains(s, `"hardBreak"`) {
			t.Errorf("expected a hardBreak between lines: %s", s)
		}
		if !strings.Contains(s, `"line one"`) || !strings.Contains(s, `"second para"`) {
			t.Errorf("missing text: %s", s)
		}
	})

	t.Run("blockquote", func(t *testing.T) {
		doc := textToADF("> quoted line\n\nmy reply", nil)
		raw, _ := json.Marshal(doc)
		s := string(raw)
		if !strings.Contains(s, `"type":"blockquote"`) {
			t.Errorf("expected a blockquote: %s", s)
		}
		if !strings.Contains(s, `"quoted line"`) || !strings.Contains(s, `"my reply"`) {
			t.Errorf("missing text: %s", s)
		}
	})

	t.Run("mention prepended to first paragraph", func(t *testing.T) {
		doc := textToADF("> quoted\n\nthanks", &Mention{AccountID: "a1", DisplayName: "Ada"})
		raw, _ := json.Marshal(doc)
		s := string(raw)
		if !strings.Contains(s, `"type":"mention"`) || !strings.Contains(s, `"id":"a1"`) || !strings.Contains(s, `"@Ada"`) {
			t.Errorf("expected a mention node: %s", s)
		}
		// The mention must lead the reply paragraph, not sit inside the quote.
		if i, j := strings.Index(s, `"blockquote"`), strings.Index(s, `"mention"`); i < 0 || j < 0 || j < i {
			t.Errorf("mention should follow the blockquote: %s", s)
		}
	})
}

func TestFindIssues(t *testing.T) {
	var jql string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			JQL string `json:"jql"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		jql = body.JQL
		io.WriteString(w, `{"issues":[{"key":"X-9","fields":{"summary":"Login broken"}}]}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	got, err := c.FindIssues(context.Background(), `log "in`, 20)
	if err != nil || len(got) != 1 || got[0].Summary != "Login broken" {
		t.Fatalf("%+v %v", got, err)
	}
	if jql != `text ~ "log* in*" ORDER BY updated DESC` {
		t.Errorf("jql = %q", jql)
	}
}

// TestFindIssuesProjectFirst: with a project, its matches come before the
// others', open before done, each issue once.
func TestFindIssuesProjectFirst(t *testing.T) {
	var mu sync.Mutex
	var jqls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			JQL string `json:"jql"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		jqls = append(jqls, body.JQL)
		mu.Unlock()
		done := `{"status":{"name":"Done","statusCategory":{"key":"done"}}}`
		open := `{"status":{"name":"To Do","statusCategory":{"key":"new"}}}`
		if strings.HasPrefix(body.JQL, "(issuetype in (\"Epic\")) AND project = ") {
			fmt.Fprintf(w, `{"issues":[{"key":"EM-9","fields":%s},{"key":"EM-23","fields":%s}]}`, done, open)
			return
		}
		fmt.Fprintf(w, `{"issues":[{"key":"XX-1","fields":%s},{"key":"EM-9","fields":%s},{"key":"YY-2","fields":%s}]}`, done, done, open)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	got, err := c.FindIssuesIn(context.Background(), "checkout", `issuetype in ("Epic")`, "EM", 20)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, g := range got {
		keys = append(keys, g.Key)
	}
	if want := []string{"EM-23", "EM-9", "YY-2", "XX-1"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v (jql %q)", keys, want, jqls)
	}
}

func TestScaledTimeout(t *testing.T) {
	if d := New(Config{}).Scaled(30 * time.Second); d != 30*time.Second {
		t.Errorf("default = %v", d)
	}
	if d := New(Config{Timeout: 40 * time.Second}).Scaled(30 * time.Second); d != time.Minute {
		t.Errorf("doubled = %v", d)
	}
}

// TestStatusError: Jira's error body reads as its messages, and each
// status a user can act on says what to do.
func TestStatusError(t *testing.T) {
	for _, c := range []struct {
		code       int
		body, want string
		retry      string
	}{
		{400, `{"errorMessages":["Bad JQL"],"errors":{"summary":"required","priority":"invalid"}}`, "jira server 400: Bad JQL; priority: invalid; summary: required", ""},
		{400, `not json`, "jira server 400: not json", ""},
		{401, ``, "not authorized for ABC-1 · check email and api_token", ""},
		{403, `{"errorMessages":["You cannot move this issue"]}`, "no permission for ABC-1: You cannot move this issue", ""},
		{403, ``, "no permission for ABC-1 · ask a Jira admin", ""},
		{429, ``, "rate-limited on ABC-1 · retry in 30s", "30"},
		{502, ``, "jira server 502: Bad Gateway", ""},
	} {
		if got := statusError(c.code, "ABC-1", []byte(c.body), c.retry).Error(); !strings.Contains(got, c.want) {
			t.Errorf("%d %s: %q, want %q", c.code, c.body, got, c.want)
		}
	}
}

// TestTimeoutError: a request past jira.timeout says to raise it.
func TestTimeoutError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", Timeout: 20 * time.Millisecond})
	_, err := c.Get(context.Background(), "ABC-1")
	if err == nil || !strings.Contains(err.Error(), "raise jira.timeout") || !TimedOut(err) {
		t.Errorf("got %v", err)
	}
}

// TestWriting counts writes on their way, not reads.
func TestWriting(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	done := make(chan struct{})
	go func() {
		_ = c.do(context.Background(), http.MethodPut, "/rest/api/3/issue/ABC-1", "ABC-1", map[string]any{}, nil)
		close(done)
	}()
	for c.Writing() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	<-done
	if n := c.Writing(); n != 0 {
		t.Errorf("after the write: %d", n)
	}
}

// TestAssignableUsersInProject: a project key searches the project, for an
// issue not made yet.
func TestAssignableUsersInProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("project") != "ABC" || q.Has("issueKey") {
			t.Errorf("query = %v", q)
		}
		_, _ = w.Write([]byte(`[{"accountId":"a1","displayName":"Ada"}]`))
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if users, err := c.AssignableUsers(context.Background(), "ABC", ""); err != nil || len(users) != 1 {
		t.Fatalf("AssignableUsers = %v, %v", users, err)
	}
}

// TestGetPagesComments: a thread longer than the issue carries pages the
// rest in, oldest first.
func TestGetPagesComments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/issue/ABC-1/comment" {
			// Every page from the start: only these carry a reply's parentId.
			switch r.URL.Query().Get("startAt") {
			case "0":
				_, _ = w.Write([]byte(`{"comments":[{"id":"1","body":{"type":"doc","content":[]}}]}`))
			case "1":
				_, _ = w.Write([]byte(`{"comments":[{"id":"2","body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"newest"}]}]}}]}`))
			default:
				t.Errorf("startAt = %s", r.URL.Query().Get("startAt"))
			}
			return
		}
		_, _ = w.Write([]byte(`{"key":"ABC-1","fields":{"summary":"s","comment":{"total":2,"comments":[{"id":"1","body":{"type":"doc","content":[]}}]}}}`))
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	iss, err := c.Get(context.Background(), "ABC-1")
	if err != nil || len(iss.Comments) != 2 || iss.Comments[1].Body != "newest" {
		t.Fatalf("comments = %+v, %v", iss.Comments, err)
	}
}

func TestGetStripsTerminalEscapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/field" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"key":"ABC-1","fields":{
			"summary":"a\u001b]52;c;eA==\u0007b\nc",
			"description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x\u001b[2Jy"}]}]},
			"labels":["l\u009b31m"],
			"status":{"name":"S\u001b[31m"},
			"assignee":{"displayName":"Eve\u001b]0;title\u0007"}}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	iss, err := c.Get(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{"summary": iss.Summary, "description": iss.Description,
		"labels": strings.Join(iss.Labels, " "), "status": iss.Status, "assignee": iss.Assignee} {
		if strings.ContainsAny(got, "\x1b\x07\u009b") {
			t.Errorf("%s kept an escape: %q", name, got)
		}
	}
	if iss.Summary != "a]52;c;eA==bc" {
		t.Errorf("summary = %q, want one line", iss.Summary)
	}
}

func TestStatusErrorStripsTerminalEscapes(t *testing.T) {
	err := statusError(400, "ABC-1", []byte(`{"errorMessages":["bad\u001b]52;c;eA==\u0007"],"errors":{"summary":"x\u001b[2J"}}`), "")
	if strings.ContainsAny(err.Error(), "\x1b\x07") {
		t.Errorf("error kept an escape: %q", err.Error())
	}
	var re *RequestError
	if !errors.As(err, &re) || strings.ContainsAny(re.Messages[0]+re.Fields["summary"], "\x1b\x07") {
		t.Errorf("RequestError kept an escape: %+v", re)
	}
}
