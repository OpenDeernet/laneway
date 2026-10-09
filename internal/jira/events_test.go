package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/serverInfo" {
			fmt.Fprint(w, `{"deploymentType":"Cloud","lanewayServerVersion":"0.1.0"}`)
			return
		}
		if r.URL.Path != "/rest/laneway/1/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": connected\n\n")
		fmt.Fprint(w, "id: 7\nevent: issue\ndata: {\"seq\":7,\"kind\":\"issue\",\"action\":\"put\",\"id\":\"10001\",\"key\":\"ABC-1\",\"projectId\":10000}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "a@b.c", APIToken: "t"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := make(chan SiteChange, 1)
	go c.Events(ctx, func(ch SiteChange) { got <- ch; cancel() })
	select {
	case ch := <-got:
		if ch.Key != "ABC-1" || ch.Seq != 7 || ch.Kind != "issue" {
			t.Errorf("change %+v", ch)
		}
	case <-ctx.Done():
		t.Fatal("no change read")
	}

	// Jira: serverInfo without laneway's version, and the feed isn't asked for.
	var mu sync.Mutex
	var asked []string
	jira := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/rest/api/3/serverInfo" {
			fmt.Fprint(w, `{"deploymentType":"Cloud","version":"1001.0.0-SNAPSHOT"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer jira.Close()
	c = New(Config{BaseURL: jira.URL, Email: "a@b.c", APIToken: "t"})
	if err := c.Events(context.Background(), func(SiteChange) {}); err != ErrNoEvents {
		t.Errorf("against Jira: %v, want ErrNoEvents", err)
	}
	if err := c.Events(context.Background(), func(SiteChange) {}); err != ErrNoEvents {
		t.Errorf("against Jira again: %v, want ErrNoEvents", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(asked) != "[/rest/api/3/serverInfo]" {
		t.Errorf("asked Jira %v, want serverInfo once", asked)
	}
}

func TestLanewayUnknownWhileOffline(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"lanewayServerVersion":"0.1.0"}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "a@b.c", APIToken: "t"})
	if _, err := c.Laneway(context.Background()); err == nil {
		t.Fatal("no error while the site is down")
	}
	down.Store(false)
	if is, err := c.Laneway(context.Background()); err != nil || !is {
		t.Errorf("after the site is back: %v %v", is, err)
	}
}

// The answer is kept per site: a start while it can't be reached knows
// the flavour.
func TestLanewayKeptForOffline(t *testing.T) {
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"lanewayServerVersion":"0.1.0"}`)
	}))
	defer srv.Close()
	st := memMeta{}
	c := New(Config{BaseURL: srv.URL, Email: "a@b.c", APIToken: "t"})
	c.SetLayouts(st)
	if is, err := c.Laneway(context.Background()); err != nil || !is {
		t.Fatalf("online: %v %v", is, err)
	}
	down.Store(true)
	c = New(Config{BaseURL: srv.URL, Email: "a@b.c", APIToken: "t"})
	c.SetLayouts(st)
	if is, err := c.Laneway(context.Background()); err != nil || !is {
		t.Errorf("a start offline: %v %v", is, err)
	}
}
