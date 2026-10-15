package jira

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOfflineQueue: a write to an issue that can't reach Jira is queued,
// a read or a create isn't; Replay sends it once Jira is back.
func TestOfflineQueue(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close() // nothing listens: dials fail
	var queued []PendingWrite
	c := New(Config{BaseURL: "http://" + addr, Email: "me@x.test", APIToken: "tok"})
	c.SetQueue(func(w PendingWrite) { queued = append(queued, w) })
	err := c.SetSummary(context.Background(), "ABC-1", "New")
	if !errors.Is(err, ErrQueued) || len(queued) != 1 || queued[0].Key() != "ABC-1" || queued[0].Method != http.MethodPut {
		t.Fatalf("err %v, queued %+v", err, queued)
	}
	if _, err := c.Get(context.Background(), "ABC-1"); errors.Is(err, ErrQueued) {
		t.Error("a read should fail, not queue")
	}
	if _, err := c.CreateIssue(context.Background(), NewIssue{Project: "ABC", Type: "Task", Summary: "x"}); errors.Is(err, ErrQueued) {
		t.Error("a create should fail, not queue")
	}
	if _, err := c.CreateIssue(context.Background(), NewIssue{Project: "ABC", Type: "Task", Summary: "x"}); err == nil || !strings.Contains(err.Error(), "can't be reached") {
		t.Errorf("an unqueued write says the site can't be reached: %v", err)
	}
	if len(queued) != 1 {
		t.Errorf("queued %d", len(queued))
	}
	// A service desk request's comment queues as an issue's does.
	if err := c.AddRequestComment(context.Background(), "HELP-1", "later", true); !errors.Is(err, ErrQueued) || len(queued) != 2 || queued[1].Key() != "HELP-1" {
		t.Fatalf("request comment: %v, queued %+v", err, queued)
	}
	queued = queued[:1]
	if err := c.Replay(context.Background(), queued[0]); !errors.Is(err, ErrQueued) || len(queued) != 1 {
		t.Errorf("still offline: %v, queued %d", err, len(queued))
	}
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, `{"fields":{"updated":"2026-09-27T10:00:00.000+0000"}}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got = r.Method + " " + r.URL.Path + " " + string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	back := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if err := back.Replay(context.Background(), queued[0]); err != nil || got != `PUT /rest/api/3/issue/ABC-1 {"fields":{"summary":"New"}}` {
		t.Errorf("replay %v: %q", err, got)
	}
	changed, err := back.ChangedSince(context.Background(), "ABC-1", time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC))
	if err != nil || !changed {
		t.Errorf("changed %v %v", changed, err)
	}
}
