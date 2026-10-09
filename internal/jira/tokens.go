package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// The signed-in account's own API tokens and the apps it let in (OAuth),
// on laneway-server. Jira has neither endpoint: every call asks Laneway
// first and returns ErrNotLaneway against Jira, without a request.

// ErrNotLaneway means the site is Jira, which keeps its tokens on
// Atlassian's own page.
var ErrNotLaneway = errors.New("jira: the site is Jira, not laneway-server")

// ErrTokenPage means this sign-in can't make an API token (an app's, not
// an API token's): TokenPage can.
var ErrTokenPage = errors.New("jira: this sign-in can't make API tokens")

// APIToken is one of the account's API tokens.
type APIToken struct {
	ID, Label string
	Scopes    []string // none: whatever the account may
	Created   time.Time
	LastUsed  time.Time // zero: never used
	Expires   time.Time
}

// AppGrant is an app the account signed in to the site with, until revoked.
type AppGrant struct {
	ID      string
	AppID   string
	App     string // its name
	Current bool   // the grant this client signs in with
	Scopes  []string
	Method  string // how the sign-in went
	Created time.Time
	// LastUsed is when the app last asked; Expires when the grant ends.
	LastUsed, Expires time.Time
}

// TokenPage is the server's own page for tokens and passkeys.
func (c *Client) TokenPage() string { return c.baseURL + "/admin#passkeys" }

// lanewayOnly is nil on laneway-server, else the error to return.
func (c *Client) lanewayOnly(ctx context.Context) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	is, err := c.Laneway(ctx)
	if err != nil {
		return err
	}
	if !is {
		return ErrNotLaneway
	}
	return nil
}

type apiToken struct {
	ID       json.RawMessage `json:"id"`
	Label    string          `json:"label"`
	Scopes   []string        `json:"scopes"`
	Created  string          `json:"created"`
	LastUsed string          `json:"lastUsed"`
	Expires  string          `json:"expires"`
	Token    string          `json:"token"`
}

func (t apiToken) token() APIToken {
	return APIToken{ID: looseID(t.ID), Label: t.Label, Scopes: t.Scopes, Created: parseStamp(t.Created), LastUsed: parseStamp(t.LastUsed), Expires: parseStamp(t.Expires)}
}

// parseStamp reads a Jira timestamp; zero when empty or not one.
func parseStamp(s string) time.Time {
	t, err := time.Parse(jiraTime, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

// APITokens lists the account's API tokens.
func (c *Client) APITokens(ctx context.Context) ([]APIToken, error) {
	if err := c.lanewayOnly(ctx); err != nil {
		return nil, err
	}
	var resp []apiToken
	if err := c.do(ctx, http.MethodGet, "/rest/laneway/1/myself/tokens", "API tokens", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]APIToken, len(resp))
	for i, t := range resp {
		out[i] = t.token()
	}
	return out, nil
}

// CreateAPIToken makes a token labelled label that lives days (0: the
// site's default), limited to scopes (none: whatever the account may). The
// secret is in the answer only. An app's sign-in can't: ErrTokenPage.
func (c *Client) CreateAPIToken(ctx context.Context, label string, days int, scopes []string) (APIToken, string, error) {
	if err := c.lanewayOnly(ctx); err != nil {
		return APIToken{}, "", err
	}
	body := map[string]any{"label": label}
	if days > 0 {
		body["days"] = days
	}
	if len(scopes) > 0 {
		body["scopes"] = scopes
	}
	var resp apiToken
	err := c.do(ctx, http.MethodPost, "/rest/laneway/1/myself/tokens", "API token", body, &resp)
	if errors.Is(err, ErrUnauthorized) {
		return APIToken{}, "", fmt.Errorf("%w: make one on %s", ErrTokenPage, c.TokenPage())
	}
	if err != nil {
		return APIToken{}, "", err
	}
	return resp.token(), resp.Token, nil
}

// RevokeAPIToken revokes the token with id.
func (c *Client) RevokeAPIToken(ctx context.Context, id string) error {
	if err := c.lanewayOnly(ctx); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/rest/laneway/1/myself/tokens/"+url.PathEscape(id), "API token "+id, nil, nil)
}

// RevokeAPITokens revokes every token of the account, the one this client
// signs in with too.
func (c *Client) RevokeAPITokens(ctx context.Context) error {
	if err := c.lanewayOnly(ctx); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/rest/laneway/1/myself/tokens", "API tokens", nil, nil)
}

// AppGrants lists the apps the account signed in with.
func (c *Client) AppGrants(ctx context.Context) ([]AppGrant, error) {
	if err := c.lanewayOnly(ctx); err != nil {
		return nil, err
	}
	var resp []struct {
		ID  json.RawMessage `json:"id"`
		App struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"app"`
		Scopes   []string `json:"scopes"`
		Method   string   `json:"method"`
		Created  string   `json:"created"`
		LastUsed string   `json:"lastUsed"`
		Expires  string   `json:"expires"`
		Current  bool     `json:"current"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/laneway/1/myself/oauth/grants", "signed-in apps", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]AppGrant, len(resp))
	for i, g := range resp {
		out[i] = AppGrant{ID: looseID(g.ID), AppID: g.App.ID, App: g.App.Name, Scopes: g.Scopes, Method: g.Method, Current: g.Current,
			Created: parseStamp(g.Created), LastUsed: parseStamp(g.LastUsed), Expires: parseStamp(g.Expires)}
	}
	return out, nil
}

// RevokeAppGrant signs the app of grant id out; it has to be let in again.
func (c *Client) RevokeAppGrant(ctx context.Context, id string) error {
	if err := c.lanewayOnly(ctx); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/rest/laneway/1/myself/oauth/grants/"+url.PathEscape(id), "signed-in app "+id, nil, nil)
}
