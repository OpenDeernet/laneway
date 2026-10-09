package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// lanewayFake is a laneway-server answering the token and grant routes;
// got are the requests it had.
func lanewayFake(t *testing.T, laneway bool, create int) (*Client, *[]string, *map[string]any) {
	var got []string
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /rest/api/3/serverInfo":
			if laneway {
				fmt.Fprint(w, `{"deploymentType":"Cloud","lanewayServerVersion":"0.9"}`)
			} else {
				fmt.Fprint(w, `{"deploymentType":"Cloud"}`)
			}
		case "GET /rest/laneway/1/myself/tokens":
			fmt.Fprint(w, `[{"id":"t1","label":"ci","scopes":[],"created":"2026-09-01T10:00:00.000+0200","lastUsed":null,"expires":"2026-12-01T10:00:00.000+0200"},
				{"id":"t2","label":"laptop","scopes":["read:jira-work"],"created":"2026-09-02T10:00:00.000+0000","lastUsed":"2026-10-08T09:30:00.000+0000","expires":"2026-11-01T10:00:00.000+0000"}]`)
		case "POST /rest/laneway/1/myself/tokens":
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(create)
			if create == http.StatusUnauthorized {
				fmt.Fprint(w, `{"errorMessages":["scope does not match"]}`)
				return
			}
			fmt.Fprint(w, `{"id":"t3","label":"new","scopes":[],"created":"2026-10-09T10:00:00.000+0000","expires":"2027-01-07T10:00:00.000+0000","token":"lws_secret"}`)
		case "GET /rest/laneway/1/myself/oauth/grants":
			fmt.Fprint(w, `[{"id":7,"app":{"id":"laneway","name":"laneway"},"scopes":["read:jira-work","offline_access"],"created":"2026-10-01T08:00:00.000+0000","lastUsed":"2026-10-09T08:00:00.000+0000","method":"passkey","expires":"2026-11-08T08:00:00.000+0000","current":true},{"id":8,"app":{"id":"laneway","name":"laneway"},"scopes":["read:jira-work"],"method":"device"}]`)
		case "DELETE /rest/laneway/1/myself/tokens/t1", "DELETE /rest/laneway/1/myself/tokens", "DELETE /rest/laneway/1/myself/oauth/grants/7":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"}), &got, &body
}

func TestAPITokens(t *testing.T) {
	c, got, body := lanewayFake(t, true, http.StatusCreated)
	ctx := context.Background()
	toks, err := c.APITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 2 || toks[0].ID != "t1" || toks[0].Label != "ci" || !toks[0].LastUsed.IsZero() ||
		!toks[0].Expires.Equal(time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)) || toks[1].Scopes[0] != "read:jira-work" || toks[1].LastUsed.IsZero() {
		t.Errorf("tokens = %+v", toks)
	}
	tok, secret, err := c.CreateAPIToken(ctx, "new", 90, nil)
	if err != nil || secret != "lws_secret" || tok.ID != "t3" {
		t.Fatalf("create = %+v %q %v", tok, secret, err)
	}
	if (*body)["label"] != "new" || (*body)["days"] != 90.0 || (*body)["scopes"] != nil {
		t.Errorf("create body = %v", *body)
	}
	if err := c.RevokeAPIToken(ctx, "t1"); err != nil {
		t.Error(err)
	}
	if err := c.RevokeAPITokens(ctx); err != nil {
		t.Error(err)
	}
	if n := strings.Count(strings.Join(*got, "\n"), "serverInfo"); n != 1 {
		t.Errorf("serverInfo asked %d times", n)
	}
}

func TestAppGrants(t *testing.T) {
	c, _, _ := lanewayFake(t, true, http.StatusCreated)
	gs, err := c.AppGrants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 2 || gs[0].ID != "7" || gs[0].AppID != "laneway" || !gs[0].Current || gs[1].Current || gs[0].Method != "passkey" || len(gs[0].Scopes) != 2 || gs[0].Expires.IsZero() {
		t.Errorf("grants = %+v", gs)
	}
	if err := c.RevokeAppGrant(context.Background(), "7"); err != nil {
		t.Error(err)
	}
}

// An app's sign-in can't make a token: the error names the server's page.
func TestCreateAPITokenSignedInByApp(t *testing.T) {
	c, _, _ := lanewayFake(t, true, http.StatusUnauthorized)
	_, _, err := c.CreateAPIToken(context.Background(), "new", 0, nil)
	if !errors.Is(err, ErrTokenPage) || !strings.Contains(err.Error(), c.TokenPage()) || !strings.HasSuffix(c.TokenPage(), "/admin#passkeys") {
		t.Errorf("err = %v", err)
	}
}

// Against Jira nothing but serverInfo is asked.
func TestTokensOnJira(t *testing.T) {
	c, got, _ := lanewayFake(t, false, http.StatusCreated)
	ctx := context.Background()
	if _, err := c.APITokens(ctx); !errors.Is(err, ErrNotLaneway) {
		t.Errorf("tokens: %v", err)
	}
	if _, _, err := c.CreateAPIToken(ctx, "x", 0, nil); !errors.Is(err, ErrNotLaneway) {
		t.Errorf("create: %v", err)
	}
	if _, err := c.AppGrants(ctx); !errors.Is(err, ErrNotLaneway) {
		t.Errorf("grants: %v", err)
	}
	if err := c.RevokeAPITokens(ctx); !errors.Is(err, ErrNotLaneway) {
		t.Errorf("revoke all: %v", err)
	}
	if fmt.Sprint(*got) != "[GET /rest/api/3/serverInfo]" {
		t.Errorf("asked %v", *got)
	}
}
