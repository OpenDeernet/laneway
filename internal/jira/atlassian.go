package jira

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sign-in to Jira Cloud through Atlassian's OAuth 2.0 (3LO): laneway web
// for many people, registered as an app in Atlassian's developer console.
// The access token is a plain bearer token, so it goes only to the site's
// API roots on api.atlassian.com (atlassianSession.allowed), and the
// rotating refresh token is kept by the server alone, sealed at rest.

// Atlassian's endpoints; tests point them at a fake.
var (
	AtlassianAuth = "https://auth.atlassian.com"
	AtlassianAPI  = "https://api.atlassian.com"
)

// AtlassianScopes are what laneway web asks for: classic scopes for Jira
// and Service Management, granular ones where the API takes no others
// (Jira Software, Confluence v2). Confluence is read only. The app in the
// developer console must have each of them.
const AtlassianScopes = "read:jira-work write:jira-work read:jira-user manage:jira-project " +
	"read:board-scope:jira-software write:board-scope:jira-software read:board-scope.admin:jira-software " +
	"read:sprint:jira-software write:sprint:jira-software read:issue:jira-software write:issue:jira-software " +
	"read:servicedesk-request write:servicedesk-request " +
	"search:confluence read:confluence-user readonly:content.attachment:confluence read:page:confluence read:space:confluence read:attachment:confluence " +
	"read:me report:personal-data offline_access"

// AtlassianCredentials are a person's Atlassian sign-in: the refresh token
// (rotates at each use), the access token last issued, and the site.
type AtlassianCredentials struct {
	Refresh string    `json:"refresh"`
	Access  string    `json:"access,omitempty"`
	Expires time.Time `json:"expires,omitzero"`
	Account string    `json:"account"`
	CloudID string    `json:"cloudId"`
	// WikiCloudID is the Confluence site's, when it is another site; ""
	// while the grant leaves it out.
	WikiCloudID string `json:"wikiCloudId,omitempty"`
}

// AtlassianLogin is a sign-in under way: send the person to AuthURL, then
// Finish with what came back to Redirect.
type AtlassianLogin struct {
	AuthURL, State string
	client         OAuthClient
	redirect       string
	site, wiki     string
}

// BeginAtlassianLogin starts a sign-in to site (https://x.atlassian.net),
// and wiki when Confluence is on another site (else ""), as client,
// coming back to redirect (as registered in the console).
func BeginAtlassianLogin(client OAuthClient, site, wiki, redirect, scopes string) *AtlassianLogin {
	b := make([]byte, 32)
	rand.Read(b)
	state := hex.EncodeToString(b)
	q := url.Values{
		"audience":      {"api.atlassian.com"},
		"client_id":     {client.ID},
		"scope":         {scopes},
		"redirect_uri":  {redirect},
		"state":         {state},
		"response_type": {"code"},
		"prompt":        {"consent"},
	}
	return &AtlassianLogin{AuthURL: AtlassianAuth + "/authorize?" + q.Encode(), State: state, client: client, redirect: redirect, site: SiteRoot(site), wiki: SiteRoot(wiki)}
}

// ErrNoSiteAccess is a sign-in whose grant doesn't take in the site.
var ErrNoSiteAccess = errors.New("jira: the sign-in doesn't grant access to this site; pick it when Atlassian asks which site")

// Finish swaps the code that came back for a sign-in on the login's
// site, with who signed in.
func (l *AtlassianLogin) Finish(ctx context.Context, back url.Values) (AtlassianCredentials, error) {
	if e := back.Get("error"); e != "" {
		return AtlassianCredentials{}, fmt.Errorf("atlassian: sign-in refused: %s %s", e, back.Get("error_description"))
	}
	if back.Get("state") != l.State || back.Get("code") == "" {
		return AtlassianCredentials{}, errors.New("atlassian: sign-in answer doesn't match")
	}
	hc := &http.Client{Timeout: DefaultTimeout}
	a, err := atlassianToken(ctx, hc, l.client, map[string]string{"grant_type": "authorization_code", "code": back.Get("code"), "redirect_uri": l.redirect})
	if err != nil {
		return AtlassianCredentials{}, err
	}
	if a.Refresh == "" {
		return AtlassianCredentials{}, errors.New("atlassian: no refresh token; the app needs the offline_access scope")
	}
	creds := AtlassianCredentials{Refresh: a.Refresh, Access: a.Access, Expires: time.Now().Add(time.Duration(a.ExpiresIn) * time.Second)}
	var sites []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := atlassianGet(ctx, hc, AtlassianAPI+"/oauth/token/accessible-resources", a.Access, &sites); err != nil {
		return creds, err
	}
	for _, s := range sites {
		if s.ID == "" || strings.Trim(strings.ToLower(s.ID), "0123456789abcdef-") != "" {
			continue
		}
		switch SiteRoot(s.URL) {
		case l.site:
			creds.CloudID = s.ID
		case l.wiki:
			creds.WikiCloudID = s.ID
		}
	}
	if creds.CloudID == "" {
		return creds, ErrNoSiteAccess
	}
	var me struct {
		AccountID string `json:"account_id"`
	}
	if err := atlassianGet(ctx, hc, AtlassianAPI+"/me", a.Access, &me); err != nil {
		return creds, err
	}
	if me.AccountID == "" {
		return creds, errors.New("atlassian: didn't say who signed in")
	}
	creds.Account = me.AccountID
	return creds, nil
}

// SiteRoot is site's scheme and host, lower case: how two site URLs
// compare.
func SiteRoot(site string) string {
	u, err := url.Parse(strings.TrimSpace(site))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// atlassianToken posts body, with the client's credentials, to the token
// endpoint.
func atlassianToken(ctx context.Context, hc *http.Client, client OAuthClient, body map[string]string) (tokenAnswer, error) {
	var a tokenAnswer
	body["client_id"], body["client_secret"] = client.ID, client.Secret
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, AtlassianAuth+"/oauth/token", bytes.NewReader(raw))
	if err != nil {
		return a, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return a, fmt.Errorf("call atlassian: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(out, &a) != nil && resp.StatusCode == http.StatusOK {
		return a, errors.New("atlassian: unreadable token answer")
	}
	if resp.StatusCode != http.StatusOK && a.Error == "" {
		a.Error = http.StatusText(resp.StatusCode)
	}
	if a.Error != "" {
		return a, fmt.Errorf("atlassian: %s %s", a.Error, a.Desc)
	}
	if a.Access == "" {
		return a, errors.New("atlassian: no access token in the answer")
	}
	return a, nil
}

func atlassianGet(ctx context.Context, hc *http.Client, target, access string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("call atlassian: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("atlassian: %s answered %s", target, resp.Status)
	}
	return json.Unmarshal(raw, out)
}

// atlassianSession holds a person's Atlassian sign-in while the client
// runs.
type atlassianSession struct {
	client OAuthClient
	store  CredentialStore
	http   *http.Client
	// jira and wiki are the site's API roots, the only places the token
	// goes.
	jira, wiki string

	mu         sync.Mutex
	creds      AtlassianCredentials
	turnedAway string
}

func newAtlassianSession(client OAuthClient, store CredentialStore, cloudID, wikiCloudID string, timeout time.Duration) *atlassianSession {
	return &atlassianSession{client: client, store: store, http: &http.Client{Timeout: timeout},
		jira: AtlassianAPI + "/ex/jira/" + url.PathEscape(cloudID), wiki: AtlassianAPI + "/ex/confluence/" + url.PathEscape(wikiCloudID)}
}

// apiURL is where path (as on the site: /rest/…, /wiki/…) is asked.
func (o *atlassianSession) apiURL(path string) string {
	if strings.HasPrefix(path, "/wiki/") {
		return o.wiki + path
	}
	return o.jira + path
}

// allowed reports whether the token may go to target: the site's API
// roots on api.atlassian.com, nowhere else.
func (o *atlassianSession) allowed(target *url.URL) bool {
	if target.User != nil || target.Fragment != "" {
		return false
	}
	t := target.Scheme + "://" + target.Host + target.EscapedPath()
	for _, root := range []string{o.jira, o.wiki} {
		if strings.HasPrefix(t, root+"/") && !strings.Contains(target.EscapedPath(), "/../") && !strings.Contains(strings.ToLower(target.EscapedPath()), "%2e%2e") {
			return true
		}
	}
	return false
}

// token is a valid access token, refreshed when it ends within a minute.
func (o *atlassianSession) token(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.creds.Access != "" && o.creds.Access != o.turnedAway && time.Until(o.creds.Expires) > time.Minute {
		return o.creds.Access, nil
	}
	if l, ok := o.store.(Locker); ok {
		unlock, err := l.Lock()
		if err != nil {
			return "", err
		}
		defer unlock()
	}
	// Another process may have refreshed meanwhile: its refresh token is
	// the one to use.
	raw, err := o.store.Load()
	if err != nil || len(raw) == 0 {
		return "", ErrSignedOut
	}
	var c AtlassianCredentials
	if json.Unmarshal(raw, &c) != nil || c.Refresh == "" {
		return "", ErrSignedOut
	}
	o.creds = c
	if c.Access != "" && c.Access != o.turnedAway && time.Until(c.Expires) > time.Minute {
		return c.Access, nil
	}
	a, err := atlassianToken(ctx, o.http, o.client, map[string]string{"grant_type": "refresh_token", "refresh_token": c.Refresh})
	if err != nil {
		if a.Error == "invalid_grant" || a.Error == "unauthorized_client" {
			o.creds = AtlassianCredentials{}
			o.store.Save(nil)
			return "", ErrSignedOut
		}
		return "", err
	}
	c.Access, c.Expires = a.Access, time.Now().Add(time.Duration(a.ExpiresIn)*time.Second)
	if a.Refresh != "" {
		c.Refresh = a.Refresh
	}
	// The new refresh token first: the old one stops working.
	raw, _ = json.Marshal(c)
	if err := o.store.Save(raw); err != nil {
		return "", fmt.Errorf("atlassian: keeping the sign-in: %w", err)
	}
	o.creds = c
	return c.Access, nil
}

// expire drops the access token the API turned away.
func (o *atlassianSession) expire(access string) {
	o.mu.Lock()
	if o.creds.Access == access {
		o.turnedAway = access
	}
	o.mu.Unlock()
}

// authorize sets req's bearer token, if req goes to the site's API.
func (o *atlassianSession) authorize(req *http.Request) (string, error) {
	if !o.allowed(req.URL) {
		return "", fmt.Errorf("atlassian: refused to send the sign-in to %s", req.URL.Host)
	}
	tok, err := o.token(req.Context())
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return tok, nil
}

// AccountReport is one account an app keeps personal data of, and when it
// last fetched it, for Atlassian's personal data reporting.
type AccountReport struct {
	AccountID string    `json:"accountId"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ReportAnswer is what Atlassian said of reported accounts: closed ones
// to erase, updated ones to fetch again, and how long until the next
// report is due (0: the default week).
type ReportAnswer struct {
	Closed, Updated []string
	Cycle           time.Duration
}

// ErrReportLater is Atlassian asking to report again later (429, 503).
var ErrReportLater = errors.New("atlassian: personal data report refused for now; try later")

// maxReport is how many accounts one report takes.
const maxReport = 90

// ReportAccounts reports accounts (Atlassian's personal data reporting
// API) with the client's sign-in, in batches of maxReport.
func (c *Client) ReportAccounts(ctx context.Context, accounts []AccountReport) (ReportAnswer, error) {
	var out ReportAnswer
	if c.atl == nil {
		return out, errNotConfigured
	}
	tok, err := c.atl.token(ctx)
	if err != nil {
		return out, err
	}
	for len(accounts) > 0 {
		batch := accounts[:min(maxReport, len(accounts))]
		accounts = accounts[len(batch):]
		raw, _ := json.Marshal(map[string]any{"accounts": batch})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, AtlassianAPI+"/app/report-accounts/", bytes.NewReader(raw))
		if err != nil {
			return out, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := c.atl.http.Do(req)
		if err != nil {
			return out, fmt.Errorf("call atlassian: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Cycle-Period"))); err == nil && s > 0 {
			out.Cycle = time.Duration(s) * time.Second
		}
		switch resp.StatusCode {
		case http.StatusNoContent:
		case http.StatusOK:
			var a struct {
				Accounts []struct {
					AccountID string `json:"accountId"`
					Status    string `json:"status"`
				} `json:"accounts"`
			}
			if err := json.Unmarshal(body, &a); err != nil {
				return out, fmt.Errorf("atlassian: unreadable personal data report answer: %w", err)
			}
			for _, x := range a.Accounts {
				switch x.Status {
				case "closed":
					out.Closed = append(out.Closed, x.AccountID)
				case "updated":
					out.Updated = append(out.Updated, x.AccountID)
				}
			}
		case http.StatusTooManyRequests, http.StatusServiceUnavailable:
			return out, ErrReportLater
		default:
			return out, fmt.Errorf("atlassian: personal data report answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
	}
	return out, nil
}
