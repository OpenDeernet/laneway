package demo

import (
	"context"
	"errors"
	"net/http"
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
