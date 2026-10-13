package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreviousWorkday(t *testing.T) {
	for now, want := range map[string]string{
		"2026-09-28": "2026-09-25", // Monday → Friday
		"2026-09-25": "2026-09-24", // Friday → Thursday
		"2026-09-27": "2026-09-25", // Sunday → Friday
	} {
		n, _ := time.Parse(time.DateOnly, now)
		if got := PreviousWorkday(n.Add(15*time.Hour), nil).Format(time.DateOnly); got != want {
			t.Errorf("%s: %s, want %s", now, got, want)
		}
	}
	// A Sunday-to-Thursday week: Sunday looks back to Thursday.
	sunThu := []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday}
	n, _ := time.Parse(time.DateOnly, "2026-09-27")
	if got := PreviousWorkday(n, sunThu).Format(time.DateOnly); got != "2026-09-24" {
		t.Errorf("sun-thu: %s", got)
	}
}

// TestStandup: your own changes and comments, and your worklogs, oldest
// first; others' left out.
func TestStandup(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(jiraTime) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/myself":
			io.WriteString(w, `{"accountId":"me"}`)
		case r.URL.Path == "/rest/api/3/search/jql":
			var body struct {
				JQL string `json:"jql"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.HasPrefix(body.JQL, `issue in updatedBy("me", "-`) {
				io.WriteString(w, `{"issues":[{"key":"A-1","fields":{"summary":"One"}}]}`)
			} else {
				io.WriteString(w, `{"issues":[]}`) // worklogDate searches
			}
		case r.URL.Path == "/rest/api/3/issue/A-1/changelog":
			io.WriteString(w, `{"total":3,"values":[
			  {"author":{"accountId":"me"},"created":"`+at(2*time.Hour)+`","items":[{"field":"status","fromString":"To Do","toString":"Done"},{"field":"timespent","fromString":"3600","toString":"7200"}]},
			  {"author":{"accountId":"me"},"created":"`+at(90*time.Minute)+`","items":[{"field":"timespent","toString":"3600"},{"field":"WorklogId","toString":"10042"}]},
			  {"author":{"accountId":"bob"},"created":"`+at(time.Hour)+`","items":[{"field":"labels","toString":"x"}]}]}`)
		case r.URL.Path == "/rest/api/3/issue/A-1/comment":
			io.WriteString(w, `{"comments":[{"author":{"accountId":"me"},"created":"`+at(3*time.Hour)+`",
			  "body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"on it"}]}]}}]}`)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	got, err := c.Standup(context.Background(), now.Add(-5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].What != "commented: on it" || got[1].What != "status: To Do → Done" {
		t.Errorf("entries = %+v", got)
	}
}

// TestTeamStandup: the people's changes and worklogs from one search, with
// who did them; others' left out.
func TestTeamStandup(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(jiraTime) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/search/jql":
			var body struct {
				JQL string `json:"jql"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch {
			case strings.HasPrefix(body.JQL, `issue in updatedBy("ann", "-`) && strings.Contains(body.JQL, `OR issue in updatedBy("bob", "-`):
				io.WriteString(w, `{"issues":[{"key":"A-1","fields":{"summary":"One"}}]}`)
			case strings.HasPrefix(body.JQL, `worklogAuthor in ("ann", "bob")`):
				io.WriteString(w, `{"issues":[{"key":"A-1","fields":{"summary":"One"}}]}`)
			default:
				t.Errorf("jql %s", body.JQL)
				io.WriteString(w, `{"issues":[]}`)
			}
		case r.URL.Path == "/rest/api/3/issue/A-1/changelog":
			io.WriteString(w, `{"total":2,"values":[
			  {"author":{"accountId":"ann","displayName":"Ann"},"created":"`+at(2*time.Hour)+`","items":[{"field":"status","fromString":"To Do","toString":"Done"}]},
			  {"author":{"accountId":"carol","displayName":"Carol"},"created":"`+at(time.Hour)+`","items":[{"field":"labels","toString":"x"}]}]}`)
		case r.URL.Path == "/rest/api/3/issue/A-1/comment":
			io.WriteString(w, `{"comments":[]}`)
		case r.URL.Path == "/rest/api/3/issue/A-1/worklog":
			io.WriteString(w, `{"worklogs":[{"author":{"accountId":"bob","displayName":"Bob"},"started":"`+at(3*time.Hour)+`","timeSpentSeconds":3600},
			  {"author":{"accountId":"carol","displayName":"Carol"},"started":"`+at(3*time.Hour)+`","timeSpentSeconds":60}]}`)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	got, err := c.TeamStandup(context.Background(), now.Add(-5*time.Hour), []string{"ann", "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Who != "Bob" || got[0].Logged != 3600 || got[1].Who != "Ann" || got[1].What != "status: To Do → Done" {
		t.Errorf("entries = %+v", got)
	}
}

// TestStandupCachesIssues: stepping the standup's start back reads only the
// issues it has not read, and still filters by the new start.
func TestStandupCachesIssues(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(jiraTime) }
	var reads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/myself":
			io.WriteString(w, `{"accountId":"me"}`)
		case r.URL.Path == "/rest/api/3/search/jql":
			var body struct {
				JQL string `json:"jql"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.HasPrefix(body.JQL, `issue in updatedBy("me", "-`) {
				io.WriteString(w, `{"issues":[{"key":"A-1","fields":{"summary":"One","updated":"`+at(time.Hour)+`"}}]}`)
			} else {
				io.WriteString(w, `{"issues":[]}`)
			}
		case r.URL.Path == "/rest/api/3/issue/A-1/changelog":
			reads++
			io.WriteString(w, `{"total":0,"values":[]}`)
		case r.URL.Path == "/rest/api/3/issue/A-1/comment":
			io.WriteString(w, `{"comments":[{"author":{"accountId":"me"},"created":"`+at(3*time.Hour)+`",
			  "body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"on it"}]}]}}]}`)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	for since, want := range map[time.Duration]int{5 * time.Hour: 1, 2 * time.Hour: 0} {
		got, err := c.Standup(context.Background(), now.Add(-since))
		if err != nil || len(got) != want {
			t.Errorf("since %s: %d entries, %v, want %d", since, len(got), err, want)
		}
	}
	if reads != 1 {
		t.Errorf("changelog read %d times, want 1", reads)
	}
}
