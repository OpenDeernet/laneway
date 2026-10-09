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
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]struct {
		secs int
		rest string
	}{
		"1h 30m fixed the tests": {5400, "fixed the tests"},
		"1h30m":                  {5400, ""},
		"1.5h review":            {5400, "review"},
		"45m":                    {2700, ""},
		"2d":                     {57600, ""},
		"10M":                    {600, ""},
	} {
		secs, rest, err := ParseDuration(in)
		if err != nil || secs != want.secs || rest != want.rest {
			t.Errorf("%q = %d %q %v", in, secs, rest, err)
		}
	}
	for _, in := range []string{"", "fixed it", "h", "1x"} {
		if _, _, err := ParseDuration(in); err == nil {
			t.Errorf("%q should fail", in)
		}
	}
	for secs, want := range map[int]string{60: "1m", 3600: "1h", 5430: "1h 31m", 7170: "2h"} {
		if got := FormatDuration(secs); got != want {
			t.Errorf("FormatDuration(%d) = %q, want %q", secs, got, want)
		}
	}
}

func TestAddWorklog(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/ABC-1/worklog" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	started := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	if err := c.AddWorklog(context.Background(), "ABC-1", 5400, started, "tests", ""); err != nil {
		t.Fatal(err)
	}
	if body["timeSpentSeconds"] != 5400.0 || body["started"] != "2026-09-25T09:00:00.000+0000" || body["comment"] == nil {
		t.Errorf("body = %v", body)
	}
}

// TestAddWorklogLeft: the remaining estimate is left alone or set.
func TestAddWorklogLeft(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	now := time.Now()
	_ = c.AddWorklog(context.Background(), "ABC-1", 3600, now, "", LeftKeep)
	_ = c.AddWorklog(context.Background(), "ABC-1", 3600, now, "", "2h")
	if len(got) != 2 || got[0] != "/rest/api/3/issue/ABC-1/worklog?adjustEstimate=leave" ||
		got[1] != "/rest/api/3/issue/ABC-1/worklog?adjustEstimate=new&newEstimate=2h" {
		t.Errorf("requests = %q", got)
	}
}

// TestMyWorklogs: your own entries of the day, across issues, by start.
func TestMyWorklogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/myself":
			io.WriteString(w, `{"accountId":"me"}`)
		case r.URL.Path == "/rest/api/3/search/jql":
			var body struct{ JQL string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.JQL != `worklogAuthor in ("me") AND worklogDate >= "2026-09-25" AND worklogDate < "2026-09-26"` {
				t.Errorf("jql %s", body.JQL)
			}
			io.WriteString(w, `{"issues":[{"key":"A-1","fields":{"summary":"One"}},{"key":"A-2","fields":{"summary":"Two"}}]}`)
		case r.URL.Path == "/rest/api/3/issue/A-1/worklog":
			io.WriteString(w, `{"worklogs":[
			  {"author":{"accountId":"me"},"started":"2026-09-25T14:00:00.000+0200","timeSpentSeconds":3600},
			  {"author":{"accountId":"other"},"started":"2026-09-25T10:00:00.000+0200","timeSpentSeconds":600}]}`)
		case r.URL.Path == "/rest/api/3/issue/A-2/worklog":
			io.WriteString(w, `{"worklogs":[{"author":{"accountId":"me"},"started":"2026-09-25T09:00:00.000+0200","timeSpentSeconds":1800}]}`)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	loc := time.FixedZone("CEST", 2*3600)
	logs, err := c.MyWorklogs(context.Background(), time.Date(2026, 9, 25, 12, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0].Key != "A-2" || logs[1].Key != "A-1" || logs[1].Seconds != 3600 || logs[1].Summary != "One" {
		t.Errorf("logs = %+v", logs)
	}
}

func TestIssueWorklogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/A-1/worklog" {
			t.Errorf("path %s", r.URL.Path)
		}
		io.WriteString(w, `{"worklogs":[
			{"id":"2","author":{"displayName":"Bob"},"started":"2026-09-25T13:00:00.000+0000","timeSpentSeconds":1800},
			{"id":"1","author":{"displayName":"Ann"},"started":"2026-09-25T09:00:00.000+0000","timeSpentSeconds":3600,
			 "comment":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"review"}]}]}}]}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	got, err := c.IssueWorklogs(context.Background(), "A-1")
	if err != nil || len(got) != 2 || got[0].Author != "Ann" || got[0].Comment != "review" || got[1].Seconds != 1800 {
		t.Errorf("%+v, %v", got, err)
	}
}

// TestDeleteWorklogs: one request for the lot, twice; a 404 (no Delete
// all worklogs) falls back to one delete per id each time, a 405
// (Server/DC) once and then deletes one at a time from the start.
func TestDeleteWorklogs(t *testing.T) {
	const bulk, one, two = "DELETE /rest/api/3/issue/ABC-1/worklog [1 2]", "DELETE /rest/api/3/issue/ABC-1/worklog/1 []", "DELETE /rest/api/3/issue/ABC-1/worklog/2 []"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   []string
	}{
		{"bulk", http.StatusNoContent, "", []string{bulk, bulk}},
		{"no permission", http.StatusNotFound, `{"errorMessages":["you do not have permission to delete the specified worklog."]}`, []string{bulk, one, two, bulk, one, two}},
		{"server", http.StatusMethodNotAllowed, "", []string{bulk, one, two, one, two}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var b struct{ IDs []int }
				_ = json.NewDecoder(r.Body).Decode(&b)
				got = append(got, fmt.Sprintf("%s %s %v", r.Method, r.URL.Path, b.IDs))
				if r.URL.Path == "/rest/api/3/issue/ABC-1/worklog" {
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
			for range 2 {
				if err := c.DeleteWorklogs(context.Background(), "ABC-1", []string{"1", "2"}); err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("requests =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestMoveWorklogs: the ids and target in one request; a site without the
// move says so, and isn't asked again.
func TestMoveWorklogs(t *testing.T) {
	var got []string
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	ctx := context.Background()
	if err := c.MoveWorklogs(ctx, "ABC-1", []string{"10", "11"}, "ABC-2"); err != nil {
		t.Fatal(err)
	}
	if want := `POST /rest/api/3/issue/ABC-1/worklog/move {"ids":[10,11],"issueIdOrKey":"ABC-2"}`; len(got) != 1 || got[0] != want {
		t.Errorf("requests = %q, want %q", got, want)
	}
	status = http.StatusNotFound // no Jira error body: the route is missing
	if err := c.MoveWorklogs(ctx, "ABC-1", []string{"10"}, "ABC-2"); !errors.Is(err, ErrNoWorklogMove) {
		t.Errorf("err = %v, want ErrNoWorklogMove", err)
	}
	if c.MovesWorklogs() {
		t.Error("MovesWorklogs after the move was turned away")
	}
	if err := c.MoveWorklogs(ctx, "ABC-1", []string{"10"}, "ABC-2"); !errors.Is(err, ErrNoWorklogMove) || len(got) != 2 {
		t.Errorf("err = %v after %d requests, want ErrNoWorklogMove without asking", err, len(got))
	}
}

// TestMoveWorklogsRefused: Jira's own 404 (a worklog not on the issue) is
// an error, not a site without the move.
func TestMoveWorklogsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"errorMessages":["One or many worklogs specified do not belong to issue."]}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	err := c.MoveWorklogs(context.Background(), "ABC-1", []string{"10"}, "ABC-2")
	if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrNoWorklogMove) || !c.MovesWorklogs() {
		t.Errorf("err = %v, moves %v", err, c.MovesWorklogs())
	}
}
