// Package jira fetches issue detail from a Jira Cloud instance so the TUI
// can show it inline in a side panel (press the open-reference key on a
// message that names an issue — see internal/ui). It deliberately has no
// dependency on the UI or store packages so it can be unit-tested against an
// httptest server with no real instance.
//
// Cloud only: authentication is HTTP Basic with an account email and an API
// token (id.atlassian.com → API tokens), and descriptions come back as ADF
// (Atlassian Document Format) JSON, which adfToMarkdown flattens to markdown.
package jira

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/cornedor/laneway/internal/safeterm"
)

// errNotConfigured is returned by every call when the client lacks a base URL
// or credentials (Enabled would report false).
var errNotConfigured = fmt.Errorf("jira: not configured (need base_url, email, api_token)")

// DefaultTimeout bounds a single request unless Config.Timeout says
// otherwise. Generous for a slow instance; a
// stalled server fails the call rather than hanging the UI's fetch goroutine.
const DefaultTimeout = 20 * time.Second

// issueFields is the field list requested from the API. Keeping it explicit
// (rather than the default "*all") keeps the response small and the JSON we
// have to decode predictable.
const issueFields = "summary,status,assignee,reporter,issuetype,priority,labels,updated,description,comment,attachment,parent,issuelinks,subtasks"

// Config is the subset of the user config this package needs. BaseURL is the
// instance root (https://your-instance.atlassian.net); Email + APIToken are the
// Cloud Basic-auth pair; Projects is the allowlist that gates bare-ID detection
// (see detect.go). StoryPointsField optionally pins the custom-field id for
// story points (e.g. "customfield_10016"); empty auto-detects it.
type Config struct {
	BaseURL          string
	Email            string
	APIToken         string
	Projects         []string
	StoryPointsField string
	CardLimit        int           // 0: DefaultCardLimit
	FlagValue        string        // the Flagged option flagging sets; "": Impediment
	InboxIssues      int           // recently updated issues the inbox and standup read; 0: 30
	Timeout          time.Duration // one request's limit; 0: DefaultTimeout
	CustomFields     []string      // fields by name cards carry in Card.Extra
	FlatReplies      bool          // read comments without who they reply to (ui.threaded_replies: off)
	// SignIn keeps a laneway-server sign-in (oauth.go): set, it is used
	// instead of Email and APIToken.
	SignIn CredentialStore
	// OAuthClient is who SignIn's sign-in renews as; zero: the laneway app.
	OAuthClient OAuthClient
	// Atlassian keeps an Atlassian OAuth sign-in (atlassian.go) for the
	// site CloudID: set, requests go to api.atlassian.com as OAuthClient,
	// and BaseURL is the site for links.
	Atlassian CredentialStore
	CloudID   string
	// WikiURL is the site Confluence is on when it isn't BaseURL;
	// WikiCloudID its cloud id for an Atlassian sign-in.
	WikiURL, WikiCloudID string
}

// Client fetches and caches issues for one instance. The zero value is not
// usable; use New. Safe for concurrent use.
type Client struct {
	baseURL    string            // trimmed of any trailing slash
	wikiURL    string            // Confluence's site: baseURL unless Config.WikiURL
	auth       string            // pre-encoded "Basic …" header value, empty when unconfigured
	oauth      *oauthSession     // a laneway-server sign-in instead (oauth.go)
	atl        *atlassianSession // an Atlassian OAuth sign-in instead (atlassian.go)
	spOverride string            // configured story-points custom-field id, "" to auto-detect
	cardLimit  int               // most cards one board fetch returns
	flagValue  string            // the Flagged option SetFlagged sets
	inboxCap   int               // issues the inbox and standup read
	actCache   sync.Map          // issue key + its updated stamp → every change and comment on it ([]InboxEntry), for activity
	timeout    time.Duration     // one request's limit
	custom     []string          // Config.CustomFields
	flat       bool              // Config.FlatReplies
	http       *http.Client
	// transfer is http without the whole-request limit, for attachment
	// bodies that may take longer than timeout; stallGuard bounds them.
	transfer *http.Client
	// queue keeps writes that never reached Jira (queue.go); nil fails them.
	queue func(PendingWrite)
	// index mirrors the cards and issues read (queue.go); nil keeps none.
	index Indexer
	// layouts keeps the edit screens seen (layout.go); nil keeps none.
	layouts MetaStore
	// people answers person searches (users.go); nil asks Jira each time.
	// peopleTried are the projects synced this session, behind mu.
	people      People
	peopleTried map[string]bool
	writing     atomic.Int32  // writes (not GETs) on their way
	slots       chan struct{} // requests on their way, at most inFlight
	// shared lets concurrent asks for the same session-wide answer (who
	// you are, the field metadata) send one request.
	shared singleflight.Group

	mu    sync.Mutex
	cache map[string]cachedIssue
	// gens counts each key's Invalidates, so a fetch that raced one isn't
	// cached.
	gens map[string]int
	// spFields are the resolved story-points custom-field ids (a configured
	// override, or every field named "story point…" from the field metadata).
	// spResolved guards the one-time resolution; both are behind mu.
	spFields   []string
	spResolved bool
	// priorities is the instance-wide priority list, fetched once and cached
	// (it's global, not per-issue). myself is the authenticated account, also
	// cached (used for "Assign to me"). Both are behind mu and nil until first
	// resolved. See Priorities / Myself.
	priorities []Option
	myself     *Me
	// site is what serverInfo said of the site (Laneway), behind mu;
	// nil until read.
	site *siteInfo
	// noBulkDelete and noWorklogMove: the site lacks Jira Cloud's bulk
	// worklog endpoints (worklog.go), behind mu.
	noBulkDelete, noWorklogMove bool

	// rules caches the workflow walk (transition.go); boardMeta the Agile
	// API's slow-changing answers (agile.go). Both have their own locks.
	rules     rulesCache
	boardMeta boardMetaCache
	// roadmapFields are the date and sprint field ids (roadmap.go), behind mu.
	roadmapFields *roadmapFieldIDs
	// fields is the field metadata (fieldMeta), behind mu; nil until read.
	fields []apiField
	// mediaIDs are attachments' Media Services file ids (attachment.go),
	// behind mu.
	mediaIDs map[string]string
	// mediaURLs are where attachments' content was last redirected to
	// (attachment.go), behind mu.
	mediaURLs map[string]mediaURL
}

// New builds a Client from cfg. The returned client is always non-nil; call
// Enabled to see whether it has enough configuration to actually fetch.
func New(cfg Config) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		wikiURL:    strings.TrimRight(strings.TrimSpace(cmp.Or(cfg.WikiURL, cfg.BaseURL)), "/"),
		spOverride: strings.TrimSpace(cfg.StoryPointsField),
		cardLimit:  cfg.CardLimit,
		flagValue:  cmp.Or(strings.TrimSpace(cfg.FlagValue), "Impediment"),
		inboxCap:   cmp.Or(max(cfg.InboxIssues, 0), inboxIssues),
		timeout:    cmp.Or(max(cfg.Timeout, 0), DefaultTimeout),
		custom:     cfg.CustomFields,
		flat:       cfg.FlatReplies,
		cache:      map[string]cachedIssue{},
		gens:       map[string]int{},
		slots:      make(chan struct{}, inFlight),
	}
	c.http = &http.Client{Timeout: c.timeout}
	c.transfer = &http.Client{}
	if c.cardLimit <= 0 {
		c.cardLimit = DefaultCardLimit
	}
	switch {
	case cfg.Atlassian != nil && cfg.CloudID != "":
		c.atl = newAtlassianSession(cfg.OAuthClient, cfg.Atlassian, cfg.CloudID, cmp.Or(cfg.WikiCloudID, cfg.CloudID), c.timeout)
	case cfg.SignIn != nil:
		c.oauth = &oauthSession{base: c.baseURL, client: cfg.OAuthClient, store: cfg.SignIn, http: &http.Client{Timeout: c.timeout}}
	case cfg.Email != "" && cfg.APIToken != "":
		raw := cfg.Email + ":" + cfg.APIToken
		c.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
	}
	return c
}

// Enabled reports whether the client has a base URL and credentials — i.e.
// whether a fetch can succeed. The UI uses it to decide whether to offer the
// panel at all.
func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != "" && (c.auth != "" || c.oauth != nil || c.atl != nil)
}

// BaseURL returns the configured instance root (no trailing slash). Used by
// Refs to recognise /browse/KEY links pointing at this instance.
func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.baseURL
}

// BrowseURL returns the human browse URL for an issue key (what `o` opens in a
// browser), e.g. https://your-instance.atlassian.net/browse/ABC-123.
func (c *Client) BrowseURL(key string) string {
	if c == nil || c.baseURL == "" {
		return ""
	}
	return c.baseURL + "/browse/" + key
}

// Issue is the flattened, render-ready form of a Jira issue. Description is
// markdown (converted from ADF); the rest are plain strings ready to label.
type Issue struct {
	Key     string
	Summary string
	Type    string
	// TypeKind is the stock type its icon shows (see TypeKind), TypeAvatar
	// that icon's URL.
	TypeKind, TypeAvatar string
	Status               string
	// StatusCategory is Jira's for the status: "new", "indeterminate" or
	// "done".
	StatusCategory string
	Priority       string
	Assignee       string
	Reporter       string
	Labels         []string
	Updated        time.Time
	URL            string
	Description    string
	StoryPoints    string // formatted estimate (e.g. "5", "2.5"), "" when unset

	// Attachments are the issue's files. Media in Description and comment
	// bodies shows as ![name](attachment:<id>) when its name matches one.
	Attachments []Attachment
	// Links are the parent, linked issues and subtasks.
	Links []Link

	// Comments is the issue's comment thread (oldest first, the order the API
	// returns), flattened for display. CommentTotal is the server's total — it
	// can exceed len(Comments) when the inline field paged, so the panel can
	// say "…and N more".
	Comments     []Comment
	CommentTotal int
	// Mentioned are the people its description and comments mention, by
	// the name each mention reads, so "@Ada Lovelace" styles whole.
	Mentioned []User `json:",omitempty"`

	// Screen is the rest of its edit screen as last seen for its project and
	// type (layout.go), with ScreenValues: drawn before EditMeta answers
	// (which then has the say); none until a screen was seen.
	Screen       []FieldMeta      `json:",omitempty"`
	ScreenValues map[string]Value `json:",omitempty"`

	// IDs of the current selection, so the field pickers can mark the active
	// row. Status needs none: its changes go through Transitions, not by id.
	PriorityID        string
	AssigneeAccountID string
	ReporterAccountID string
}

// Comment is one issue comment, flattened for display. Body is markdown
// (converted from ADF); AuthorID (accountId) is what a reply's @mention writes.
type Comment struct {
	ID string
	// ParentID is the comment this one replies to, "" for a top-level one.
	// Jira sends it undocumented (a string id), so it may also go missing.
	ParentID string
	Raw      json.RawMessage // the body as Jira stores it, for an edit
	Author   string
	AuthorID string
	Body     string
	Created  time.Time
	// Visibility is who may read it; zero for everyone.
	Visibility Visibility
}

// Mention names a user to ping in a comment. AddComment turns it into a real
// ADF mention node — the only form Jira notifies on; plain "@name" text does
// not.
type Mention struct {
	AccountID   string
	DisplayName string
}

// Option is a pickable choice with a stable id and a human label — a workflow
// transition (id = transition id, Name = the resulting status) or a priority.
// StatusID is a transition's resulting status id.
type Option struct {
	ID       string
	Name     string
	StatusID string
	TypeKind string // an issue type's, see TypeKind
}

// User is an assignable account: AccountID is what SetAssignee writes,
// DisplayName is shown in the picker.
type User struct {
	AccountID   string
	DisplayName string
}

// Me is the signed-in account as /myself says it, with what the web app's
// account menu shows besides.
type Me struct {
	User
	Email     string
	AvatarURL string
}

// apiIssue mirrors the slice of the REST response we read. Optional objects are
// pointers so an absent assignee/priority decodes to nil rather than an error.
type apiIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Labels      []string        `json:"labels"`
		Updated     string          `json:"updated"`
		Status      *apiStatus      `json:"status"`
		Priority    *named          `json:"priority"`
		IssueType   *named          `json:"issuetype"`
		Assignee    *user           `json:"assignee"`
		Reporter    *user           `json:"reporter"`
		Attachment  []apiAttachment `json:"attachment"`
		Parent      *apiLinked      `json:"parent"`
		IssueLinks  []apiIssueLink  `json:"issuelinks"`
		Subtasks    []apiLinked     `json:"subtasks"`
		Comment     *struct {
			Comments []apiComment `json:"comments"`
			Total    int          `json:"total"`
		} `json:"comment"`
	} `json:"fields"`
}

type apiStatus struct {
	Name     string `json:"name"`
	Category struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

type named struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IconURL string `json:"iconUrl"`
}

type user struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

// apiComment mirrors one entry of the issue's inline comment field. Body is the
// ADF document, flattened to markdown via adfToMarkdown.
type apiComment struct {
	ID       string          `json:"id"`
	ParentID json.RawMessage `json:"parentId"` // undocumented: string or number, often absent
	Author   *user           `json:"author"`
	Body     json.RawMessage `json:"body"`
	Created  string          `json:"created"`
	// Visibility limits it to a role or group; JSDPublic false (Service
	// Desk only) is an internal note.
	Visibility *struct {
		Type       string `json:"type"`
		Value      string `json:"value"`
		Identifier string `json:"identifier"`
	} `json:"visibility"`
	JSDPublic *bool `json:"jsdPublic"`
}

// visibility is who may read ac.
func (ac apiComment) visibility() Visibility {
	switch {
	case ac.JSDPublic != nil && !*ac.JSDPublic:
		return Visibility{Internal: true}
	case ac.Visibility == nil:
		return Visibility{}
	case ac.Visibility.Type == "role":
		return Visibility{Role: safeterm.Line(ac.Visibility.Value)}
	case ac.Visibility.Type == "group":
		return Visibility{Group: safeterm.Line(ac.Visibility.Value), GroupID: ac.Visibility.Identifier}
	}
	return Visibility{}
}

// looseID reads an id sent as a string or a number; "" for anything else.
func looseID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// issueTTL is how long a fetched issue is served from the cache: the
// board's own refresh pace, so a prefetched issue is about as fresh as its
// card.
const issueTTL = 2 * time.Minute

// cachedIssue is an issue and when it was fetched.
type cachedIssue struct {
	iss *Issue
	at  time.Time
}

// cachedFresh is key's cached issue while it is younger than issueTTL.
func (c *Client) cachedFresh(key string) (*Issue, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hit, ok := c.cache[key]
	if !ok || time.Since(hit.at) >= issueTTL {
		return nil, false
	}
	return hit.iss, true
}

// Get returns the issue for key, serving a cached copy younger than
// issueTTL. Use Invalidate (then Get) to force a refetch.
func (c *Client) Get(ctx context.Context, key string) (*Issue, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	if iss, ok := c.cachedFresh(key); ok {
		return iss, nil
	}
	c.mu.Lock()
	gen := c.gens[key]
	c.mu.Unlock()
	issue, err := c.fetch(ctx, key)
	if err != nil {
		return nil, err
	}
	if c.index != nil {
		c.index.PutIssue(issue)
	}
	c.mu.Lock()
	if c.gens[key] == gen { // else a write invalidated it meanwhile
		c.cache[key] = cachedIssue{issue, time.Now()}
	}
	c.mu.Unlock()
	return issue, nil
}

// prefetchWorkers is how many issues Prefetch loads at once.
const prefetchWorkers = 3

// Prefetch loads the keys not freshly cached into the cache, a few at a
// time; failures are left for Get to report.
func (c *Client) Prefetch(ctx context.Context, keys []string) {
	if !c.Enabled() {
		return
	}
	sem := make(chan struct{}, prefetchWorkers)
	var wg sync.WaitGroup
	for _, k := range keys {
		if _, ok := c.cachedFresh(k); ok {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			_, _ = c.Get(ctx, k)
		}()
	}
	wg.Wait()
}

// Invalidate drops any cached copy of key so the next Get refetches.
func (c *Client) Invalidate(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.cache, key)
	c.gens[key]++
	c.mu.Unlock()
}

func (c *Client) fetch(ctx context.Context, key string) (*Issue, error) {
	// Resolve the story-points custom field(s) first so we can request them
	// alongside the standard fields. A failure here is non-fatal — the issue
	// still loads, just without story points.
	spFields := c.resolveStoryPointFields(ctx)
	fields := issueFields
	for _, f := range spFields {
		fields += "," + f
	}
	project, _, _ := strings.Cut(key, "-")
	for _, f := range c.projectFields(project) { // the screens seen: their values come along
		fields += "," + f
	}

	path := "/rest/api/3/issue/" + url.PathEscape(key) + "?fields=" + url.QueryEscape(fields)
	body, err := c.doRaw(ctx, http.MethodGet, path, key, nil)
	if err != nil {
		return nil, err
	}

	var decoded apiIssue
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode issue: %w", err)
	}
	if !ValidKey(decoded.Key) {
		return nil, fmt.Errorf("jira: %s came back with a bad key %q", key, decoded.Key)
	}
	// Only the comment endpoint says which comment a reply answers
	// (parentId); the issue's own list never does, and stops at 50.
	if cm := decoded.Fields.Comment; cm != nil && c.flat && cm.Total > len(cm.Comments) {
		cm.Comments = append(cm.Comments, c.moreComments(ctx, key, len(cm.Comments), cm.Total)...)
		for i := range cm.Comments {
			cm.Comments[i].ParentID = nil // flat throughout, the paged-in ones too
		}
	} else if cm != nil && !c.flat && cm.Total > 0 {
		if all := c.moreComments(ctx, key, 0, cm.Total); len(all) >= len(cm.Comments) {
			cm.Comments = all
		}
	}
	files, media := c.issueMedia(ctx, decoded)
	iss := c.toIssue(decoded, files, media)
	iss.StoryPoints = extractStoryPoints(body, spFields)
	if t := decoded.Fields.IssueType; t != nil && c.layouts != nil {
		var raw struct {
			Fields map[string]json.RawMessage `json:"fields"`
		}
		if json.Unmarshal(body, &raw) == nil {
			iss.Screen, iss.ScreenValues = c.screen(project, t.ID, raw.Fields)
		}
	}
	return iss, nil
}

// moreComments pages in key's comments from start up to total (at most
// 1000), oldest first. A failure keeps what came: the panel then says how
// many are missing.
func (c *Client) moreComments(ctx context.Context, key string, start, total int) []apiComment {
	var out []apiComment
	for start < min(total, 1000) {
		var resp struct {
			Comments []apiComment `json:"comments"`
		}
		path := "/rest/api/3/issue/" + url.PathEscape(key) + "/comment?orderBy=created&maxResults=100&startAt=" + strconv.Itoa(start)
		if c.do(ctx, http.MethodGet, path, key, nil, &resp) != nil || len(resp.Comments) == 0 {
			break
		}
		out = append(out, resp.Comments...)
		start += len(resp.Comments)
	}
	return out
}

// doRaw performs an authenticated request to path (relative to baseURL, which
// must begin with "/" and may carry a query string), sending body as JSON when
// non-nil, and returns the raw response body. A non-2xx status becomes a
// statusError; what labels the request in that error (an issue key, or e.g.
// "priorities"). Each call carries its own timeout.
func (c *Client) doRaw(ctx context.Context, method, path, what string, body any) ([]byte, error) {
	return c.send(ctx, method, path, what, body, true)
}

// inFlight caps the requests one client has on their way: a cold start's
// fan-out (inbox, home, prefetch) otherwise sends dozens at once, which is
// what draws Jira's 429s.
const inFlight = 8

// A request Jira turned away for load (a 429, or a 503 on a read) is sent
// again up to retries times: after its Retry-After, or else after
// retryWait doubled per try, with jitter. A Retry-After past maxRetryWait
// fails at once.
const retries = 2

var (
	retryWait    = time.Second
	maxRetryWait = 10 * time.Second
)

// send is doRaw; queue lets a write that never reached Jira go to the
// offline queue (queue.go).
func (c *Client) send(ctx context.Context, method, path, what string, body any, queue bool) ([]byte, error) {
	var buf []byte
	if body != nil {
		var err error
		if buf, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
	}
	if method != http.MethodGet {
		c.writing.Add(1)
		defer c.writing.Add(-1)
	}
	for try := 0; ; try++ {
		out, wait, err := c.attempt(ctx, method, path, what, body, buf, queue, try)
		if wait < 0 || try == retries {
			return out, err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, err
		case <-t.C:
		}
	}
}

// attempt sends the request once, holding one of the client's in-flight
// slots. wait is how long to hold off before sending it again, or -1 when
// the answer stands.
func (c *Client) attempt(ctx context.Context, method, path, what string, body any, buf []byte, queue bool, try int) (_ []byte, wait time.Duration, _ error) {
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return nil, -1, fmt.Errorf("call jira: %w", ctx.Err())
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var rdr io.Reader
	if buf != nil {
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, c.apiURL(path), rdr)
	if err != nil {
		return nil, -1, fmt.Errorf("build request: %w", err)
	}
	access, err := c.authorize(req)
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Accept", "application/json")
	if buf != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if queue && c.queueWrite(method, path, what, body, err) {
			return nil, -1, fmt.Errorf("%s: %w", what, ErrQueued)
		}
		if ctx.Err() == nil && isTimeout(err) {
			return nil, -1, fmt.Errorf("jira: %s timed out after %s · raise jira.timeout for a slow instance: %w", what, c.timeout, err)
		}
		if unreached(err) {
			return nil, -1, fmt.Errorf("%s: the site can't be reached; try again when it's back: %w", what, err)
		}
		return nil, -1, fmt.Errorf("call jira: %w", err)
	}
	defer resp.Body.Close()
	if c.oauth != nil {
		learnClock(resp) // proofs carry the laneway-server's time
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, -1, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized && access != "" && try == 0 {
		// The access token was turned away (revoked, or the server
		// restarted its clock): refresh and send it once more.
		c.expire(access)
		return nil, 0, statusError(resp.StatusCode, what, respBody, "")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryAfter := resp.Header.Get("Retry-After")
		err := statusError(resp.StatusCode, what, respBody, retryAfter)
		if u, ok := err.(unauthorized); ok && (c.atl != nil || c.oauth != nil) {
			u.oauth = true
			err = u
		}
		return nil, retryIn(method, resp.StatusCode, retryAfter, try), err
	}
	// Jira's text reaches the terminal by many paths; no string in it may
	// carry an escape.
	return safeterm.JSON(respBody), -1, nil
}

// retryIn is how long to wait before sending a request again that came
// back with code, or -1 not to. A 429 was refused before Jira acted on it,
// so any method goes again; a 503 only when it reads.
func retryIn(method string, code int, retryAfter string, try int) time.Duration {
	if code != http.StatusTooManyRequests && (code != http.StatusServiceUnavailable || method != http.MethodGet) {
		return -1
	}
	if s, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && s >= 0 {
		d := time.Duration(s) * time.Second
		if d > maxRetryWait {
			return -1
		}
		// Sent early, it fails again with as long a wait: jitter only adds.
		return d + rand.N(retryWait/2+1)
	}
	d := retryWait << try
	return d/2 + rand.N(d/2+1)
}

// do is doRaw plus JSON decode into out (skip with out=nil, e.g. a 204 mutation
// response).
func (c *Client) do(ctx context.Context, method, path, what string, body, out any) error {
	respBody, err := c.doRaw(ctx, method, path, what, body)
	if err != nil {
		return err
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// ErrNotFound is a 404, for errors.Is.
var ErrNotFound = errors.New("jira: not found")

type notFound struct {
	what string
	said bool // with Jira's message: the route exists, the thing doesn't
}

func (e notFound) Error() string   { return fmt.Sprintf("jira: %s not found (or no access)", e.what) }
func (e notFound) Is(t error) bool { return t == ErrNotFound }

// ErrUnauthorized is a 401 (a wrong email or token), for errors.Is.
var ErrUnauthorized = errors.New("jira: not authorized")

// oauth: the client signs in through OAuth (Atlassian or laneway-server),
// whose 401 for a scope the sign-in lacks has nothing to do with a token
// in the config.
type unauthorized struct {
	what  string
	oauth bool
}

func (e unauthorized) Error() string {
	if e.oauth {
		return fmt.Sprintf("jira: not authorized for %s · the sign-in lacks a scope for it; sign out and in again once the app has it", e.what)
	}
	return fmt.Sprintf("jira: not authorized for %s · check email and api_token in the config", e.what)
}
func (e unauthorized) Is(t error) bool { return t == ErrUnauthorized }

// ErrForbidden is a 403 (no permission), for errors.Is.
var ErrForbidden = errors.New("jira: no permission")

type forbidden struct{ msg string }

func (e forbidden) Error() string   { return e.msg }
func (e forbidden) Is(t error) bool { return t == ErrForbidden }

// statusError turns a non-2xx into a message the panel can show, saying
// what to do where a user can: sign in, ask for permission, wait, raise a
// limit. what labels the request (an issue key, or e.g. "priorities");
// retryAfter is a 429's Retry-After.
func statusError(code int, what string, body []byte, retryAfter string) error {
	// The body is Jira's, and may be a proxy's HTML; it ends up on the
	// status line.
	msg := safeterm.Line(jiraMessages(body))
	switch code {
	case http.StatusUnauthorized:
		return unauthorized{what: what}
	case http.StatusForbidden:
		if msg != "" {
			return forbidden{fmt.Sprintf("jira: no permission for %s: %s", what, msg)}
		}
		return forbidden{fmt.Sprintf("jira: no permission for %s · ask a Jira admin, or check the api_token", what)}
	case http.StatusNotFound:
		return notFound{what, msg != ""}
	case http.StatusTooManyRequests:
		if retryAfter != "" {
			return fmt.Errorf("jira: rate-limited on %s · retry in %ss", what, retryAfter)
		}
		return fmt.Errorf("jira: rate-limited on %s · retry in a minute", what)
	}
	if msg == "" {
		msg = strings.TrimSpace(safeterm.Line(string(body)))
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
	}
	if msg == "" {
		msg = http.StatusText(code)
	}
	re := &RequestError{msg: fmt.Sprintf("jira server %d: %s", code, msg), code: code}
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(body, &e) == nil {
		for _, m := range e.ErrorMessages {
			re.Messages = append(re.Messages, safeterm.Line(m))
		}
		if e.Errors != nil {
			re.Fields = make(map[string]string, len(e.Errors))
			for k, v := range e.Errors {
				re.Fields[k] = safeterm.Line(v)
			}
		}
	}
	return re
}

// RequestError is a request Jira refused, with its reasons: Messages about
// the whole, Fields by field id (a create's "components": "… is required").
type RequestError struct {
	msg      string
	code     int
	Messages []string
	Fields   map[string]string
}

func (e *RequestError) Error() string { return e.msg }

// jiraMessages reads Jira's error body, {"errorMessages": [...], "errors":
// {field: message}}, as "message; field: message", "" for another body.
func jiraMessages(body []byte) string {
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	parts := slices.Clone(e.ErrorMessages)
	for _, f := range slices.Sorted(maps.Keys(e.Errors)) {
		parts = append(parts, f+": "+e.Errors[f])
	}
	return strings.Join(parts, "; ")
}

// Writing is how many writes are on their way to Jira, for a quit to wait
// on.
func (c *Client) Writing() int {
	if c == nil {
		return 0
	}
	return int(c.writing.Load())
}

// TimedOut reports whether err is a request that ran out of time: unlike
// one that never left, Jira may have acted on it.
func TimedOut(err error) bool { return isTimeout(err) }

// isTimeout is whether err is a request running out of time.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

// toIssue makes a's Issue, its inline files linked as files has them and
// its pictures pointed at their attachments by media (see issueMedia).
func (c *Client) toIssue(a apiIssue, files map[string]string, media map[string]Attachment) *Issue {
	iss := &Issue{
		Key:         a.Key,
		Summary:     safeterm.Line(a.Fields.Summary),
		URL:         c.BrowseURL(a.Key),
		Description: safeterm.Text(adfWithFiles(a.Fields.Description, files)),
		Assignee:    "Unassigned",
	}
	for _, l := range a.Fields.Labels {
		iss.Labels = append(iss.Labels, safeterm.Line(l))
	}
	for _, at := range a.Fields.Attachment {
		iss.Attachments = append(iss.Attachments, Attachment{ID: at.ID, Filename: safeterm.Line(at.Filename), MimeType: at.MimeType, Size: at.Size})
	}
	iss.Description = resolveMedia(iss.Description, iss.Attachments, media)
	iss.Links = issueLinks(a.Fields.Parent, a.Fields.IssueLinks, a.Fields.Subtasks)
	if a.Fields.Status != nil {
		iss.Status, iss.StatusCategory = safeterm.Line(a.Fields.Status.Name), a.Fields.Status.Category.Key
	}
	if a.Fields.Priority != nil {
		iss.Priority = safeterm.Line(a.Fields.Priority.Name)
		iss.PriorityID = a.Fields.Priority.ID
	}
	if a.Fields.IssueType != nil {
		iss.Type = safeterm.Line(a.Fields.IssueType.Name)
		iss.TypeAvatar, iss.TypeKind = a.Fields.IssueType.IconURL, TypeKind(a.Fields.IssueType.IconURL)
	}
	if a.Fields.Assignee != nil && a.Fields.Assignee.DisplayName != "" {
		iss.Assignee = safeterm.Line(a.Fields.Assignee.DisplayName)
		iss.AssigneeAccountID = a.Fields.Assignee.AccountID
	}
	if a.Fields.Reporter != nil {
		iss.Reporter = safeterm.Line(a.Fields.Reporter.DisplayName)
		iss.ReporterAccountID = a.Fields.Reporter.AccountID
	}
	// Jira stamps updated as e.g. 2026-06-15T09:41:00.000+0200.
	if t, err := time.Parse("2006-01-02T15:04:05.999-0700", a.Fields.Updated); err == nil {
		iss.Updated = t
	}
	if a.Fields.Comment != nil {
		iss.CommentTotal = a.Fields.Comment.Total
		for _, ac := range a.Fields.Comment.Comments {
			cm := Comment{ID: ac.ID, ParentID: looseID(ac.ParentID), Raw: ac.Body, Body: safeterm.Text(resolveMedia(adfWithFiles(ac.Body, files), iss.Attachments, media)), Visibility: ac.visibility()}
			if ac.Author != nil {
				cm.Author = safeterm.Line(ac.Author.DisplayName)
				cm.AuthorID = ac.Author.AccountID
			}
			if t, err := time.Parse("2006-01-02T15:04:05.999-0700", ac.Created); err == nil {
				cm.Created = t
			}
			iss.Comments = append(iss.Comments, cm)
		}
	}
	iss.Mentioned = mentioned(a)
	return iss
}

// resolveStoryPointFields returns the custom-field id(s) that hold story
// points: the configured override if set, otherwise every field whose name
// contains "story point" (case-insensitive) from the instance's field
// metadata. The result is resolved once and cached. A failed lookup is cached
// as "none" only when an override is set; for auto-detect it stays unresolved
// so a later fetch retries (the metadata endpoint may have been transiently
// down). Returns nil when there's nothing to request.
func (c *Client) resolveStoryPointFields(ctx context.Context) []string {
	c.mu.Lock()
	if c.spResolved {
		f := c.spFields
		c.mu.Unlock()
		return f
	}
	c.mu.Unlock()

	if c.spOverride != "" {
		c.mu.Lock()
		c.spFields = []string{c.spOverride}
		c.spResolved = true
		f := c.spFields
		c.mu.Unlock()
		return f
	}

	ids, err := c.fetchStoryPointFieldIDs(ctx)
	if err != nil {
		return nil // leave unresolved so the next fetch retries
	}
	c.mu.Lock()
	c.spFields = ids
	c.spResolved = true
	c.mu.Unlock()
	return ids
}

// StoryPointsField is the story-points field id: the configured one or the
// first detected, "" when there is none.
func (c *Client) StoryPointsField(ctx context.Context) string {
	if f := c.resolveStoryPointFields(ctx); len(f) > 0 {
		return f[0]
	}
	return ""
}

// fetchStoryPointFieldIDs reads the instance field metadata and returns the ids
// of every field named like story points. Order follows the API response, so
// extractStoryPoints prefers whichever candidate the issue actually populates.
func (c *Client) fetchStoryPointFieldIDs(ctx context.Context) ([]string, error) {
	fields, err := c.fieldMeta(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f.Name), "story point") || strings.Contains(strings.ToLower(f.UntranslatedName), "story point") {
			ids = append(ids, f.ID)
		}
	}
	return ids, nil
}

// extractStoryPoints pulls the first non-null numeric value among the candidate
// field ids out of the raw issue JSON, formatted without trailing zeros ("5",
// "2.5"). Returns "" when none is set.
func extractStoryPoints(body []byte, ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	var wrap struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return ""
	}
	for _, id := range ids {
		raw, ok := wrap.Fields[id]
		if !ok || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var v float64
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// Transitions returns the workflow transitions available from the issue's
// current status — the only status changes Jira will accept. Each Option's ID
// is the transition id (pass to DoTransition); Name is the resulting status.
func (c *Client) Transitions(ctx context.Context, key string) ([]Option, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var resp struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   named  `json:"to"`
		} `json:"transitions"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions"
	if err := c.do(ctx, http.MethodGet, path, key, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Option, 0, len(resp.Transitions))
	for _, t := range resp.Transitions {
		name := t.To.Name // the status the transition lands on
		if name == "" {
			name = t.Name
		}
		out = append(out, Option{ID: t.ID, Name: name, StatusID: t.To.ID})
	}
	return out, nil
}

// StartTransition is the first transition from key's status to one in
// progress (Jira's indeterminate category); false when there is none.
func (c *Client) StartTransition(ctx context.Context, key string) (Option, bool, error) {
	if !c.Enabled() {
		return Option{}, false, errNotConfigured
	}
	var resp struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Category struct {
					Key string `json:"key"`
				} `json:"statusCategory"`
			} `json:"to"`
		} `json:"transitions"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions"
	if err := c.do(ctx, http.MethodGet, path, key, nil, &resp); err != nil {
		return Option{}, false, err
	}
	for _, t := range resp.Transitions {
		if t.To.Category.Key == "indeterminate" {
			return Option{ID: t.ID, Name: t.To.Name, StatusID: t.To.ID}, true, nil
		}
	}
	return Option{}, false, nil
}

// DoTransition moves the issue along the given transition, then invalidates the
// cache so the next Get reflects the new status (and any cascading fields like
// resolution).
func (c *Client) DoTransition(ctx context.Context, key, transitionID string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"transition": map[string]string{"id": transitionID}}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions"
	if err := c.do(ctx, http.MethodPost, path, key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// Priorities returns the instance's global priority list (Highest…Lowest),
// cached after the first call.
func (c *Client) Priorities(ctx context.Context) ([]Option, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	c.mu.Lock()
	if c.priorities != nil {
		p := c.priorities
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()

	var resp []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/priority", "priorities", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Option, 0, len(resp))
	for _, p := range resp {
		out = append(out, Option{ID: p.ID, Name: p.Name})
	}
	c.mu.Lock()
	c.priorities = out
	c.mu.Unlock()
	return out, nil
}

// SetPriority sets the issue's priority and invalidates the cache.
func (c *Client) SetPriority(ctx context.Context, key, priorityID string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"fields": map[string]any{"priority": map[string]string{"id": priorityID}}}
	path := "/rest/api/3/issue/" + url.PathEscape(key)
	if err := c.do(ctx, http.MethodPut, path, key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// AssignableUsers returns users who can be assigned to the issue, optionally
// narrowed by query. With People it answers from the project's full list,
// read once a week (users.go); a query that matches no one there, or no
// cache, asks Jira, matching name and email. Jira caps the response (50 by
// default), so a large project must search rather than rely on the first
// page. A key without a dash is a project's: users assignable in it, for an
// issue yet to be made. Offline, the cache answers even when stale.
func (c *Client) AssignableUsers(ctx context.Context, key, query string) ([]User, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	people, project := c.usersFrom(ctx, key)
	if us, ok := c.KnownUsers(key, query); ok {
		return us, nil
	}
	var resp []struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	}
	path := "/rest/api/3/user/assignable/search?issueKey=" + url.QueryEscape(key)
	if !strings.Contains(key, "-") {
		path = "/rest/api/3/user/assignable/search?project=" + url.QueryEscape(key)
	}
	if q := strings.TrimSpace(query); q != "" {
		path += "&query=" + url.QueryEscape(q)
	}
	if err := c.do(ctx, http.MethodGet, path, key, nil, &resp); err != nil {
		if people != nil && Offline(err) {
			if us := people.Users(project, query, true); len(us) > 0 {
				return us[:min(len(us), peopleShown)], nil
			}
		}
		return nil, err
	}
	out := make([]User, 0, len(resp))
	for _, u := range resp {
		out = append(out, User{AccountID: u.AccountID, DisplayName: u.DisplayName})
	}
	if people != nil {
		people.PutUsers(project, out, true)
	}
	return out, nil
}

// Scaled stretches an action's limit d (several requests) by how much
// Config.Timeout stretches one request's.
func (c *Client) Scaled(d time.Duration) time.Duration {
	return time.Duration(float64(d) * float64(c.timeout) / float64(DefaultTimeout))
}

// KnownMyself is your accountId once Myself has fetched it, else "".
func (c *Client) KnownMyself() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.myself == nil {
		return ""
	}
	return c.myself.AccountID
}

// Myself returns the authenticated account (for "Assign to me"), cached.
func (c *Client) Myself(ctx context.Context) (User, error) {
	me, err := c.Me(ctx)
	return me.User, err
}

// Me is Myself with the account's email and avatar.
func (c *Client) Me(ctx context.Context) (Me, error) {
	if !c.Enabled() {
		return Me{}, errNotConfigured
	}
	c.mu.Lock()
	if c.myself != nil {
		u := *c.myself
		c.mu.Unlock()
		return u, nil
	}
	c.mu.Unlock()

	v, err, _ := c.shared.Do("myself", func() (any, error) {
		var resp struct {
			AccountID   string            `json:"accountId"`
			DisplayName string            `json:"displayName"`
			Email       string            `json:"emailAddress"`
			Avatars     map[string]string `json:"avatarUrls"`
		}
		if err := c.do(ctx, http.MethodGet, "/rest/api/3/myself", "current user", nil, &resp); err != nil {
			return Me{}, err
		}
		u := Me{User: User{AccountID: resp.AccountID, DisplayName: resp.DisplayName}, Email: resp.Email, AvatarURL: resp.Avatars["48x48"]}
		c.mu.Lock()
		c.myself = &u
		c.mu.Unlock()
		return u, nil
	})
	return v.(Me), err
}

// SetAssignee assigns the issue to accountID, or unassigns it when accountID is
// "" (sends accountId:null). Invalidates the cache.
func (c *Client) SetAssignee(ctx context.Context, key, accountID string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"accountId": nil}
	if accountID != "" {
		body["accountId"] = accountID
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/assignee"
	if err := c.do(ctx, http.MethodPut, path, key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// SetStoryPoints writes raw (a number, or "" to clear) to the issue's
// story-points custom field, then invalidates the cache. Errors clearly when no
// such field is detected or raw isn't numeric.
func (c *Client) SetStoryPoints(ctx context.Context, key, raw string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	fields := c.resolveStoryPointFields(ctx)
	if len(fields) == 0 {
		return fmt.Errorf("jira: no story-points field detected — set jira.story_points_field")
	}
	var value any // nil clears the field
	if raw = strings.TrimSpace(raw); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("jira: story points must be a number, got %q", raw)
		}
		value = v
	}
	body := map[string]any{"fields": map[string]any{fields[0]: value}}
	path := "/rest/api/3/issue/" + url.PathEscape(key)
	if err := c.do(ctx, http.MethodPut, path, key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// SetReporter makes accountID the issue's reporter.
func (c *Client) SetReporter(ctx context.Context, key, accountID string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"fields": map[string]any{"reporter": map[string]string{"accountId": accountID}}}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// SetSummary replaces the issue's summary.
func (c *Client) SetSummary(ctx context.Context, key, summary string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"fields": map[string]any{"summary": summary}}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// SetLabels replaces the issue's labels; none clears them.
func (c *Client) SetLabels(ctx context.Context, key string, labels []string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	if labels == nil {
		labels = []string{}
	}
	body := map[string]any{"fields": map[string]any{"labels": labels}}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// EditLabels adds and removes labels, leaving the issue's others as they
// are.
func (c *Client) EditLabels(ctx context.Context, key string, add, remove []string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	var ops []map[string]string
	for _, l := range add {
		ops = append(ops, map[string]string{"add": l})
	}
	for _, l := range remove {
		ops = append(ops, map[string]string{"remove": l})
	}
	body := map[string]any{"update": map[string]any{"labels": ops}}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// AddComment posts text as a new comment on the issue, then invalidates the
// cache so the next Get includes it. text is plain (blank lines separate
// paragraphs, "> " lines become a blockquote — see textToADF); when mention is
// non-nil the comment opens with a real @mention of that user, which is what a
// reply uses to actually notify them.
func (c *Client) AddComment(ctx context.Context, key, text string, mention *Mention) error {
	return c.AddCommentMentions(ctx, key, text, mention, nil, Visibility{}, "")
}

// AddCommentADF posts body, a comment as Jira stores it, on key.
func (c *Client) AddCommentADF(ctx context.Context, key string, body json.RawMessage) error {
	return c.AddCommentADFFor(ctx, key, body, Visibility{}, "")
}

// AddCommentADFFor is AddCommentADF for who vis lets read it, as a reply
// to the comment parentID ("" for none).
func (c *Client) AddCommentADFFor(ctx context.Context, key string, body json.RawMessage, vis Visibility, parentID string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	req := commentRequest(body, vis, parentID)
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/comment"
	if err := c.do(ctx, http.MethodPost, path, key, req, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// Visibility is who may read a comment: everyone (zero), a Service Desk
// internal note or reply to the customer (PostComment), the members of a
// project role or of a group.
type Visibility struct {
	Internal bool
	Customer bool
	Role     string
	Group    string
	GroupID  string
}

// commentRequest is a comment's POST body. A reply names its parent
// (parentId: undocumented, but Jira threads it) and takes the parent's
// readers: Jira refuses a visibility on it.
func commentRequest(doc any, vis Visibility, parentID string) map[string]any {
	req := map[string]any{"body": doc}
	if parentID != "" {
		req["parentId"] = parentID
	} else {
		vis.addTo(req)
	}
	return req
}

// addTo puts v in a comment's request body: Service Desk's internal
// property, or a role's or group's visibility.
func (v Visibility) addTo(body map[string]any) {
	switch {
	case v.Internal:
		body["properties"] = []any{map[string]any{"key": "sd.public.comment", "value": map[string]any{"internal": true}}}
	case v.Role != "":
		body["visibility"] = map[string]string{"type": "role", "value": v.Role}
	case v.Group != "":
		// Jira takes the group's ID or its name, never both.
		if v.GroupID != "" {
			body["visibility"] = map[string]string{"type": "group", "identifier": v.GroupID}
		} else {
			body["visibility"] = map[string]string{"type": "group", "value": v.Group}
		}
	}
}

// Label is how the composer names it.
func (v Visibility) Label() string {
	switch {
	case v.Internal:
		return "internal note"
	case v.Customer:
		return "reply to the customer"
	case v.Role != "":
		return "only " + v.Role
	case v.Group != "":
		return "only " + v.Group
	}
	return "everyone"
}

// CommentVisibilities are who a comment in project can be limited to, as
// Jira's own menu offers them: an internal note in a Service Desk project,
// then each project role and each group the user is in.
func (c *Client) CommentVisibilities(ctx context.Context, project string) ([]Visibility, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var p struct {
		Type string `json:"projectTypeKey"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(project), project, nil, &p); err != nil {
		return nil, err
	}
	var roles []struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(project)+"/roledetails?currentMember=true", project+" roles", nil, &roles); err != nil {
		return nil, err
	}
	me, err := c.Myself(ctx)
	if err != nil {
		return nil, err
	}
	var groups []struct {
		Name    string `json:"name"`
		GroupID string `json:"groupId"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/user/groups?accountId="+url.QueryEscape(me.AccountID), "your groups", nil, &groups); err != nil {
		return nil, err
	}
	var out []Visibility
	if p.Type == "service_desk" {
		out = append(out, Visibility{Internal: true})
	}
	var rs, gs []Visibility
	for _, r := range roles {
		rs = append(rs, Visibility{Role: r.Name})
	}
	for _, g := range groups {
		gs = append(gs, Visibility{Group: g.Name, GroupID: g.GroupID})
	}
	slices.SortFunc(rs, func(a, b Visibility) int { return strings.Compare(a.Role, b.Role) })
	slices.SortFunc(gs, func(a, b Visibility) int { return strings.Compare(a.Group, b.Group) })
	return append(append(out, rs...), gs...), nil
}

// AddCommentMentions is AddComment where each "@Name" of inline in text
// becomes a real mention of that person too, for vis, as a reply to the
// comment parentID ("" for none).
func (c *Client) AddCommentMentions(ctx context.Context, key, text string, mention *Mention, inline []Mention, vis Visibility, parentID string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	if strings.TrimSpace(text) == "" && mention == nil {
		return fmt.Errorf("jira: empty comment")
	}
	return c.AddCommentADFFor(ctx, key, CommentADF(text, mention, inline), vis, parentID)
}

// CommentADF is the document AddCommentMentions posts for text.
func CommentADF(text string, mention *Mention, inline []Mention) json.RawMessage {
	doc := textToADF(text, mention)
	inlineMentions(doc, inline)
	b, _ := json.Marshal(doc)
	return b
}

// HasComment reports whether one of key's newest comments is yours and
// reads as doc does: a post that timed out may have landed all the same.
func (c *Client) HasComment(ctx context.Context, key string, doc json.RawMessage) (bool, error) {
	if !c.Enabled() {
		return false, errNotConfigured
	}
	me, err := c.Myself(ctx)
	if err != nil {
		return false, err
	}
	var resp struct {
		Comments []apiComment `json:"comments"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/comment?orderBy=-created&maxResults=10"
	if err := c.do(ctx, http.MethodGet, path, key, nil, &resp); err != nil {
		return false, err
	}
	want := strings.Fields(adfToMarkdown(doc))
	for _, ac := range resp.Comments {
		if ac.Author != nil && ac.Author.AccountID == me.AccountID && slices.Equal(strings.Fields(adfToMarkdown(ac.Body)), want) {
			return true, nil
		}
	}
	return false, nil
}
