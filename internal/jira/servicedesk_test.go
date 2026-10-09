package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A site without Service Management answers the desk list 404: no desks,
// asked once, and a comment goes the usual way.
func TestServiceDesksNone(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path)
		if strings.HasPrefix(r.URL.Path, "/rest/servicedeskapi/") {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errorMessages":["dead link"]}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	ctx := context.Background()
	c := New(Config{BaseURL: srv.URL, Email: "a@b.c", APIToken: "t"})
	for range 2 {
		if ds, err := c.ServiceDesks(ctx); err != nil || len(ds) != 0 {
			t.Fatalf("desks %+v %v", ds, err)
		}
	}
	if qs, err := c.DeskQueues(ctx); err != nil || len(qs) != 0 {
		t.Fatalf("queues %+v %v", qs, err)
	}
	if err := c.PostComment(ctx, "ABC-1", CommentADF("hi", nil, nil), Visibility{}, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /rest/servicedeskapi/servicedesk", "POST /rest/api/3/issue/ABC-1/comment"}
	if strings.Join(asked, "\n") != strings.Join(want, "\n") {
		t.Errorf("asked %q, want %q", asked, want)
	}
}

func TestServiceDeskRequest(t *testing.T) {
	var posted []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/servicedeskapi/servicedesk":
			if r.URL.Query().Get("start") == "0" {
				fmt.Fprint(w, `{"isLastPage":false,"values":[{"id":"1","projectKey":"HELP","projectName":"Help desk"}]}`)
				return
			}
			fmt.Fprint(w, `{"isLastPage":true,"values":[{"id":"2","projectKey":"OPS","projectName":"Ops"}]}`)
		case r.URL.Path == "/rest/servicedeskapi/request/HELP-1":
			if r.URL.Query().Get("expand") != "requestType,sla" {
				t.Errorf("expand %q", r.URL.Query().Get("expand"))
			}
			fmt.Fprint(w, `{"issueKey":"HELP-1","requestType":{"id":"10","name":"Get IT help"},
				"reporter":{"displayName":"Ada","emailAddress":"ada@x.test"},
				"sla":{"isLastPage":true,"values":[
					{"name":"Time to first response","ongoingCycle":{"breached":false,"paused":true,"remainingTime":{"friendly":"2h 30m","millis":9000000},"goalDuration":{"friendly":"4h"}},"completedCycles":[]},
					{"name":"Time to resolution","ongoingCycle":{"breached":true,"paused":false,"remainingTime":{"friendly":"-1h 5m"}}},
					{"name":"Time to close","completedCycles":[{"breached":false,"remainingTime":{"friendly":"3h"}}]},
					{"name":"Not started","completedCycles":[]}]}}`)
		case r.URL.Path == "/rest/servicedeskapi/request/HELP-2":
			// laneway-server leaves the expands out: their own endpoints answer.
			fmt.Fprint(w, `{"issueKey":"HELP-2","serviceDeskId":"1","requestTypeId":"11","reporter":{"displayName":"Bo"}}`)
		case r.URL.Path == "/rest/servicedeskapi/servicedesk/1/requesttype/11":
			fmt.Fprint(w, `{"id":"11","name":"Report a bug"}`)
		case r.URL.Path == "/rest/servicedeskapi/request/HELP-2/sla":
			fmt.Fprint(w, `{"isLastPage":true,"values":[{"name":"Time to resolution","ongoingCycle":{"breached":false,"remainingTime":{"friendly":"5h"}}}]}`)
		case r.URL.Path == "/rest/servicedeskapi/servicedesk/1/queue":
			if r.URL.Query().Get("includeCount") != "true" {
				t.Errorf("queues without includeCount: %s", r.URL)
			}
			fmt.Fprint(w, `{"isLastPage":true,"values":[{"id":"5","name":"All open","jql":"project = HELP AND resolution = Unresolved ORDER BY created DESC","issueCount":7},
				{"id":"6","name":"Mine","jql":"assignee = currentUser()","issueCount":2}]}`)
		case r.URL.Path == "/rest/servicedeskapi/servicedesk/2/queue":
			w.WriteHeader(http.StatusForbidden) // not an agent there
		case r.Method == http.MethodPost:
			var b map[string]any
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &b)
			b["path"] = r.URL.Path
			posted = append(posted, b)
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c := New(Config{BaseURL: srv.URL, Email: "a@b.c", APIToken: "t"})
	ds, err := c.ServiceDesks(ctx)
	if err != nil || len(ds) != 2 || ds[0].ProjectKey != "HELP" || ds[1].ID != "2" {
		t.Fatalf("desks %+v %v", ds, err)
	}
	if is, _ := c.IsServiceDesk(ctx, "HELP"); !is {
		t.Error("HELP is a desk")
	}
	if is, _ := c.IsServiceDesk(ctx, "ABC"); is {
		t.Error("ABC is no desk")
	}

	r, err := c.Request(ctx, "HELP-1")
	if err != nil || r.Type != "Get IT help" || r.Customer != "Ada" || r.CustomerEmail != "ada@x.test" || len(r.SLAs) != 3 {
		t.Fatalf("request %+v %v", r, err)
	}
	if s := r.SLAs[0]; s.Name != "Time to first response" || s.Remaining != "2h 30m" || s.Goal != "4h" || !s.Paused || s.Breached || s.Done {
		t.Errorf("first response %+v", s)
	}
	if s := r.SLAs[1]; s.Remaining != "-1h 5m" || !s.Breached || s.Done {
		t.Errorf("resolution %+v", s)
	}
	if s := r.SLAs[2]; !s.Done || s.Breached || s.Remaining != "3h" {
		t.Errorf("close %+v", s)
	}
	r, err = c.Request(ctx, "HELP-2")
	if err != nil || r.Type != "Report a bug" || len(r.SLAs) != 1 || r.SLAs[0].Remaining != "5h" {
		t.Fatalf("request without expands %+v %v", r, err)
	}

	// A comment on a request is an internal note unless it is for the
	// customer; a reply is a new one; a role's goes the usual way.
	doc := CommentADF("on it", nil, nil)
	for _, v := range []Visibility{{}, {Internal: true}, {Customer: true}} {
		if err := c.PostComment(ctx, "HELP-1", doc, v, "10001"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.PostComment(ctx, "HELP-1", doc, Visibility{Role: "Agents"}, ""); err != nil {
		t.Fatal(err)
	}
	if len(posted) != 4 {
		t.Fatalf("posted %v", posted)
	}
	for i, public := range []bool{false, false, true} {
		p := posted[i]
		if p["path"] != "/rest/servicedeskapi/request/HELP-1/comment" || p["body"] != "on it" || p["public"] != public {
			t.Errorf("comment %d: %v, want public %v", i, p, public)
		}
	}
	if p := posted[3]; p["path"] != "/rest/api/3/issue/HELP-1/comment" || p["visibility"] == nil {
		t.Errorf("role comment %v", p)
	}

	qs, err := c.DeskQueues(ctx)
	if err != nil || len(qs) != 2 {
		t.Fatalf("queues %+v %v", qs, err)
	}
	if q := qs[0]; q.Name != "All open" || q.Count != 7 || q.Project != "HELP" || q.Label() != "HELP · All open" ||
		q.JQL != `project = HELP AND resolution = Unresolved ORDER BY created DESC` { // Jira's names its project: as is
		t.Errorf("queue %+v", q)
	}
	if q := qs[1]; q.JQL != `project = "HELP" AND (assignee = currentUser())` || q.Count != 2 {
		t.Errorf("queue %+v", q)
	}
}

func TestQueueJQL(t *testing.T) {
	for in, want := range map[string]string{
		`resolution = Unresolved ORDER BY created DESC`:               `project = "SD" AND (resolution = Unresolved) ORDER BY created DESC`,
		`project = SD AND (resolution = Unresolved) ORDER BY created`: `project = SD AND (resolution = Unresolved) ORDER BY created`,
		`project = "SD" AND resolution = Unresolved`:                  `project = "SD" AND resolution = Unresolved`,
		`project = SDX AND a = 1`:                                     `project = "SD" AND (project = SDX AND a = 1)`,
		``:                                                            `project = "SD"`,
	} {
		if got := queueJQL("SD", in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
