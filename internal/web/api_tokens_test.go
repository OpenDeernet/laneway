package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/demo"
	"github.com/cornedor/laneway/internal/jira"
)

// tokensServer is laneway web on base.
func tokensServer(t *testing.T, base string) *httptest.Server {
	cl := jira.New(jira.Config{BaseURL: base, Email: "d@example.com", APIToken: "x"})
	ts := httptest.NewServer(New(context.Background(), Options{Client: cl, Site: "t"}))
	t.Cleanup(ts.Close)
	return ts
}

type tokensInfo struct {
	Laneway, MakesTokens bool
	TokenURL, Page       string
	Tokens               []jira.APIToken
	Apps                 []jira.AppGrant
}

// On laneway-server: the tokens and apps listed, one made, revoked one by
// one and all at once, an app signed out.
func TestSiteTokens(t *testing.T) {
	d := demo.New(time.Now())
	d.Laneway()
	base, stop, err := d.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	ts := tokensServer(t, base)
	var info tokensInfo
	if code := issueCall(t, "GET", ts.URL+"/api/site/tokens", nil, &info); code != 200 || !info.Laneway || len(info.Tokens) != 2 || len(info.Apps) != 3 ||
		!info.Apps[0].Current || info.Apps[1].Current || info.Page != base+"/admin#passkeys" || info.TokenURL != "" || !info.MakesTokens {
		t.Fatalf("%d %+v", code, info)
	}
	var made struct {
		Token  jira.APIToken
		Secret string
	}
	if code := issueCall(t, "POST", ts.URL+"/api/site/tokens", map[string]any{"Label": "script", "Days": 30}, &made); code != 200 || made.Secret == "" || made.Token.Label != "script" {
		t.Fatalf("make: %d %+v", code, made)
	}
	if code := issueCall(t, "POST", ts.URL+"/api/site/tokens", map[string]any{"Label": " "}, nil); code != 400 {
		t.Errorf("no label: %d", code)
	}
	if code := issueCall(t, "DELETE", ts.URL+"/api/site/tokens/"+made.Token.ID, nil, nil); code != 200 {
		t.Errorf("revoke: %d", code)
	}
	if code := issueCall(t, "DELETE", ts.URL+"/api/site/tokens/"+made.Token.ID, nil, nil); code != 404 {
		t.Errorf("revoked twice: %d", code)
	}
	if code := issueCall(t, "DELETE", ts.URL+"/api/site/tokens", nil, nil); code != 200 {
		t.Errorf("revoke all: %d", code)
	}
	if code := issueCall(t, "DELETE", ts.URL+"/api/site/apps/"+info.Apps[1].ID, nil, nil); code != 200 {
		t.Errorf("sign out: %d", code)
	}
	if issueCall(t, "GET", ts.URL+"/api/site/tokens", nil, &info); len(info.Tokens) != 0 || len(info.Apps) != 2 {
		t.Errorf("after: %+v", info)
	}
	if len(d.Unhandled) > 0 {
		t.Errorf("unhandled: %v", d.Unhandled)
	}
}

// Signed in by an app, laneway-server makes no token: the page that can.
func TestSiteTokenPage(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/serverInfo":
			fmt.Fprint(w, `{"lanewayServerVersion":"1"}`)
		case "/rest/laneway/1/myself/tokens":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"errorMessages":["scope does not match"]}`)
		default:
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(fake.Close)
	ts := tokensServer(t, fake.URL)
	var out struct{ Page, Secret string }
	if code := issueCall(t, "POST", ts.URL+"/api/site/tokens", map[string]any{"Label": "x"}, &out); code != 200 || out.Page != fake.URL+"/admin#passkeys" || out.Secret != "" {
		t.Errorf("%d %+v", code, out)
	}
}

// On Jira: Atlassian's page, and nothing asked but serverInfo.
func TestSiteTokensOnJira(t *testing.T) {
	d := demo.New(time.Now())
	base, stop, err := d.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	var info tokensInfo
	if code := issueCall(t, "GET", tokensServer(t, base).URL+"/api/site/tokens", nil, &info); code != 200 || info.Laneway || info.TokenURL != config.TokenURL || info.Tokens != nil {
		t.Errorf("%d %+v", code, info)
	}
	if len(d.Unhandled) > 0 {
		t.Errorf("unhandled: %v", d.Unhandled)
	}
}
