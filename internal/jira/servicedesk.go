package jira

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cornedor/laneway/internal/safeterm"
)

// Service desk requests, through Jira Service Management's API
// (/rest/servicedeskapi), which laneway-server answers too: the site's
// desks, a request's type, customer and SLAs, a comment as a reply to the
// customer or an internal note, and the desks' queues. A site without a
// service desk answers the desk list 404 (or 403, or none): it is asked
// once and nothing more.

// ServiceDesk is one desk: its id and its project.
type ServiceDesk struct {
	ID          string
	ProjectKey  string
	ProjectName string
}

// Request is an issue as its service desk sees it.
type Request struct {
	Key           string
	Type          string // its request type's name
	Customer      string // who raised it
	CustomerEmail string
	SLAs          []SLA
}

// SLA is one of a request's service level goals: its running clock, or
// once stopped its last one.
type SLA struct {
	Name      string
	Remaining string // time left as Jira words it ("2h 30m", "-1h 5m" past the goal)
	Goal      string
	Breached  bool
	Paused    bool
	Done      bool // stopped: met, or Breached
}

// Queue is one of a desk's queues: its issues' JQL and how many it holds.
type Queue struct {
	ID      string
	Desk    string // the desk's id
	Project string // the desk's project key
	Name    string
	JQL     string
	Count   int
}

// sdPage is the Service Desk API's page: values, then isLastPage.
type sdPage[T any] struct {
	Values     []T  `json:"values"`
	IsLastPage bool `json:"isLastPage"`
}

// noDesk is an answer that means the site has no service desk to show:
// Jira without Service Management (404), or no agent access (403).
func noDesk(err error) bool { return errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) }

// ServiceDesks are the desks the person may see, read once per client;
// none on a site without Service Management. An error (offline) is
// returned and not kept.
func (c *Client) ServiceDesks(ctx context.Context) ([]ServiceDesk, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	bm := &c.boardMeta
	v, err, _ := c.shared.Do("servicedesks", func() (any, error) {
		return cached(&bm.mu, &bm.desks, "", func() ([]ServiceDesk, error) {
			out := []ServiceDesk{}
			for start := 0; start < 1000; {
				var page sdPage[struct {
					ID          string `json:"id"`
					ProjectKey  string `json:"projectKey"`
					ProjectName string `json:"projectName"`
				}]
				err := c.do(ctx, http.MethodGet, "/rest/servicedeskapi/servicedesk?limit=100&start="+strconv.Itoa(start), "service desks", nil, &page)
				if noDesk(err) {
					return out, nil
				}
				if err != nil {
					return nil, err
				}
				for _, d := range page.Values {
					out = append(out, ServiceDesk{ID: d.ID, ProjectKey: d.ProjectKey, ProjectName: safeterm.Line(d.ProjectName)})
				}
				if page.IsLastPage || len(page.Values) == 0 {
					break
				}
				start += len(page.Values)
			}
			return out, nil
		})
	})
	if err != nil {
		return nil, err
	}
	return v.([]ServiceDesk), nil
}

// IsServiceDesk reports whether project is one of the site's desks.
func (c *Client) IsServiceDesk(ctx context.Context, project string) (bool, error) {
	ds, err := c.ServiceDesks(ctx)
	for _, d := range ds {
		if d.ProjectKey == project {
			return true, nil
		}
	}
	return false, err
}

type sdDuration struct {
	Friendly string `json:"friendly"`
}

type sdCycle struct {
	Breached      bool       `json:"breached"`
	Paused        bool       `json:"paused"`
	RemainingTime sdDuration `json:"remainingTime"`
	GoalDuration  sdDuration `json:"goalDuration"`
}

type sdSLA struct {
	Name            string    `json:"name"`
	OngoingCycle    *sdCycle  `json:"ongoingCycle"`
	CompletedCycles []sdCycle `json:"completedCycles"`
}

// Request is key as a service desk request: its type, its customer and
// its SLAs (expand=requestType,sla). A site that leaves an expand out
// (laneway-server) has it read from its own endpoint; SLAs an account may
// not see (a customer's) are left out.
func (c *Client) Request(ctx context.Context, key string) (*Request, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var resp struct {
		IssueKey      string `json:"issueKey"`
		ServiceDeskID string `json:"serviceDeskId"`
		RequestTypeID string `json:"requestTypeId"`
		RequestType   *struct {
			Name string `json:"name"`
		} `json:"requestType"`
		Reporter *struct {
			DisplayName  string `json:"displayName"`
			EmailAddress string `json:"emailAddress"`
		} `json:"reporter"`
		SLA *sdPage[sdSLA] `json:"sla"`
	}
	base := "/rest/servicedeskapi/request/" + url.PathEscape(key)
	if err := c.do(ctx, http.MethodGet, base+"?expand=requestType,sla", key+" request", nil, &resp); err != nil {
		return nil, err
	}
	out := &Request{Key: cmp.Or(resp.IssueKey, key)}
	if resp.Reporter != nil {
		out.Customer, out.CustomerEmail = safeterm.Line(resp.Reporter.DisplayName), safeterm.Line(resp.Reporter.EmailAddress)
	}
	if resp.RequestType == nil && resp.ServiceDeskID != "" && resp.RequestTypeID != "" {
		path := "/rest/servicedeskapi/servicedesk/" + url.PathEscape(resp.ServiceDeskID) + "/requesttype/" + url.PathEscape(resp.RequestTypeID)
		_ = c.do(ctx, http.MethodGet, path, key+" request type", nil, &resp.RequestType) // a failure only leaves it out
	}
	if resp.RequestType != nil {
		out.Type = safeterm.Line(resp.RequestType.Name)
	}
	if resp.SLA == nil {
		var page sdPage[sdSLA]
		if c.do(ctx, http.MethodGet, base+"/sla", key+" SLAs", nil, &page) == nil {
			resp.SLA = &page
		}
	}
	if resp.SLA != nil {
		for _, s := range resp.SLA.Values {
			sla := SLA{Name: safeterm.Line(s.Name)}
			cy := s.OngoingCycle
			if cy == nil && len(s.CompletedCycles) > 0 {
				cy, sla.Done = &s.CompletedCycles[len(s.CompletedCycles)-1], true
			}
			if cy == nil {
				continue // no clock started
			}
			sla.Remaining, sla.Goal = safeterm.Line(cy.RemainingTime.Friendly), safeterm.Line(cy.GoalDuration.Friendly)
			sla.Breached, sla.Paused = cy.Breached, cy.Paused && !sla.Done
			out.SLAs = append(out.SLAs, sla)
		}
	}
	return out, nil
}

// AddRequestComment posts text (plain, as the Service Desk API takes it)
// on the request key: a reply the customer reads when public, else an
// internal note only agents see.
func (c *Client) AddRequestComment(ctx context.Context, key, text string, public bool) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("jira: empty comment")
	}
	body := map[string]any{"body": text, "public": public}
	if err := c.do(ctx, http.MethodPost, "/rest/servicedeskapi/request/"+url.PathEscape(key)+"/comment", key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// PostComment posts doc on key as the composers do. On a service desk's
// request a comment for everyone or an internal note goes through the
// Service Desk API as its text: an internal note unless vis.Customer, so
// nothing reaches a customer by accident. A reply there is a new comment
// (Jira's threads are no part of a request). Elsewhere, and for a role or
// group, it is AddCommentADFFor.
func (c *Client) PostComment(ctx context.Context, key string, doc json.RawMessage, vis Visibility, parentID string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	if vis.Role == "" && vis.Group == "" {
		project, _, _ := strings.Cut(key, "-")
		desk, err := c.IsServiceDesk(ctx, project)
		if err != nil {
			return err
		}
		if desk {
			return c.AddRequestComment(ctx, key, DocMarkdown(doc), vis.Customer)
		}
	}
	return c.AddCommentADFFor(ctx, key, doc, vis, parentID)
}

// Queues are a desk's queues with their counts, their JQL as the desk has
// it.
func (c *Client) Queues(ctx context.Context, deskID string) ([]Queue, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var out []Queue
	for start := 0; start < 1000; {
		var page sdPage[struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			JQL        string `json:"jql"`
			IssueCount int    `json:"issueCount"`
		}]
		path := "/rest/servicedeskapi/servicedesk/" + url.PathEscape(deskID) + "/queue?includeCount=true&limit=100&start=" + strconv.Itoa(start)
		if err := c.do(ctx, http.MethodGet, path, "queues", nil, &page); err != nil {
			return nil, err
		}
		for _, q := range page.Values {
			out = append(out, Queue{ID: q.ID, Desk: deskID, Name: safeterm.Line(q.Name), JQL: q.JQL, Count: q.IssueCount})
		}
		if page.IsLastPage || len(page.Values) == 0 {
			break
		}
		start += len(page.Values)
	}
	return out, nil
}

// DeskQueues are the queues of every desk the person works on, each's
// JQL kept to its desk's project; none (and no request past the desk
// list) on a site without one. A desk that refuses its queues (a
// customer's) is left out.
func (c *Client) DeskQueues(ctx context.Context) ([]Queue, error) {
	ds, err := c.ServiceDesks(ctx)
	if err != nil {
		return nil, err
	}
	var out []Queue
	for _, d := range ds {
		qs, err := c.Queues(ctx, d.ID)
		if noDesk(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, q := range qs {
			q.Project, q.JQL = d.ProjectKey, queueJQL(d.ProjectKey, q.JQL)
			out = append(out, q)
		}
	}
	return out, nil
}

// Label is how a saved list names q: its desk's project, then its name.
func (q Queue) Label() string { return q.Project + " · " + q.Name }

// queueJQL is a queue's JQL kept to its desk's project, ORDER BY last.
// Jira's (and laneway-server's) start with that project already: as is.
func queueJQL(project, jql string) string {
	if namesProject(project, jql) {
		return strings.TrimSpace(jql)
	}
	where, order := strings.TrimSpace(jql), ""
	if i := strings.LastIndex(strings.ToUpper(where), "ORDER BY"); i >= 0 {
		where, order = strings.TrimSpace(where[:i]), " "+strings.TrimSpace(where[i:])
	}
	q := "project = " + strconv.Quote(project)
	if where != "" {
		q += " AND (" + where + ")"
	}
	return q + order
}

// namesProject reports whether jql starts with project = project.
func namesProject(project, jql string) bool {
	rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(jql)), "project")
	if !ok {
		return false
	}
	rest, ok = strings.CutPrefix(strings.TrimSpace(rest), "=")
	if !ok {
		return false
	}
	rest = strings.TrimPrefix(strings.TrimSpace(rest), `"`)
	p := strings.ToLower(project)
	if !strings.HasPrefix(rest, p) {
		return false
	}
	next := strings.TrimPrefix(rest, p)
	return next == "" || next[0] == '"' || next[0] == ' '
}
