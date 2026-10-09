package demo

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/jira"
)

// The laneway flavour serves the account's tokens and apps; the plain demo
// is Jira, and the client asks it nothing of them.
func TestDemoLaneway(t *testing.T) {
	c, s := demoClient(t)
	ctx := context.Background()
	if _, err := c.APITokens(ctx); !errors.Is(err, jira.ErrNotLaneway) {
		t.Errorf("Jira's tokens: %v", err)
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unhandled: %v", s.Unhandled)
	}

	s = New(time.Now())
	s.Laneway()
	url, stop, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	c = jira.New(jira.Config{BaseURL: url, Email: "demo@example.com", APIToken: "demo"})
	toks, err := c.APITokens(ctx)
	if err != nil || len(toks) != 2 || toks[0].Label != "CI pipeline" || toks[0].Expires.IsZero() || !toks[1].LastUsed.IsZero() {
		t.Fatalf("tokens = %+v, %v", toks, err)
	}
	tok, secret, err := c.CreateAPIToken(ctx, "script", 7, nil)
	if err != nil || secret == "" || tok.Label != "script" || tok.Expires.Sub(tok.Created) != 7*24*time.Hour {
		t.Fatalf("create = %+v %q %v", tok, secret, err)
	}
	if err := c.RevokeAPIToken(ctx, toks[0].ID); err != nil {
		t.Error(err)
	}
	if err := c.RevokeAPIToken(ctx, toks[0].ID); !errors.Is(err, jira.ErrNotFound) {
		t.Errorf("revoked twice: %v", err)
	}
	if err := c.RevokeAPITokens(ctx); err != nil {
		t.Error(err)
	}
	if toks, _ := c.APITokens(ctx); len(toks) != 0 {
		t.Errorf("after revoke all: %+v", toks)
	}
	gs, err := c.AppGrants(ctx)
	if err != nil || len(gs) != 2 || gs[0].AppID != jira.SelfApp {
		t.Fatalf("grants = %+v, %v", gs, err)
	}
	if err := c.RevokeAppGrant(ctx, gs[1].ID); err != nil {
		t.Error(err)
	}
	if gs, _ := c.AppGrants(ctx); len(gs) != 1 {
		t.Errorf("after revoke: %+v", gs)
	}
	// No change feed in the demo: the app polls, and that is no miss.
	resp, err := http.Get(url + "/rest/laneway/1/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("events: %d", resp.StatusCode)
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unhandled: %v", s.Unhandled)
	}
}

// The laneway flavour's project is a service desk: requests with their
// SLAs, comments for the customer or not, queues. The plain demo has no
// desk, as Jira without Service Management.
func TestDemoServiceDesk(t *testing.T) {
	c, s := demoClient(t)
	ctx := context.Background()
	if ds, err := c.ServiceDesks(ctx); err != nil || len(ds) != 0 {
		t.Errorf("plain demo desks %+v %v", ds, err)
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unhandled: %v", s.Unhandled)
	}

	s = New(time.Now())
	s.Laneway()
	url, stop, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	c = jira.New(jira.Config{BaseURL: url, Email: "demo@example.com", APIToken: "demo"})
	if desk, err := c.IsServiceDesk(ctx, project); err != nil || !desk {
		t.Fatalf("desk %v %v", desk, err)
	}
	key := s.order[0]
	r, err := c.Request(ctx, key)
	if err != nil || r.Type == "" || r.Customer == "" || !strings.HasSuffix(r.CustomerEmail, "@example.com") || len(r.SLAs) != 2 || r.SLAs[1].Remaining == "" {
		t.Fatalf("request %+v %v", r, err)
	}
	if err := c.PostComment(ctx, key, jira.CommentADF("looking into it", nil, nil), jira.Visibility{}, ""); err != nil {
		t.Fatal(err)
	}
	iss, err := c.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if last := iss.Comments[len(iss.Comments)-1]; last.Body != "looking into it" || !last.Visibility.Internal {
		t.Errorf("comment %+v, want an internal note", last)
	}
	qs, err := c.DeskQueues(ctx)
	if err != nil || len(qs) != len(deskQueues) || qs[0].Count == 0 || qs[0].Count != len(s.search(qs[0].JQL)) {
		t.Fatalf("queues %+v %v", qs, err)
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unhandled: %v", s.Unhandled)
	}
}
