package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Writes made offline: a change to an issue that never reached Jira — the
// connection couldn't be made — goes to the queue the client is given
// instead of failing, to be sent again later. Anything that might have
// reached Jira (a timeout, an error answer) fails as before, so nothing is
// sent twice.

// PendingWrite is a queued request.
type PendingWrite struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	What   string          `json:"what"`
	Body   json.RawMessage `json:"body,omitempty"`
	At     time.Time       `json:"at"`
}

// Key is the issue the write is to, from its path.
func (w PendingWrite) Key() string { return issuePathKey(w.Path) }

// ErrQueued is a write kept for later because Jira couldn't be reached.
var ErrQueued = errors.New("offline: queued, sent when Jira is back")

// SetQueue gives the client somewhere to keep offline writes.
func (c *Client) SetQueue(q func(PendingWrite)) { c.queue = q }

// Indexer keeps the issues the client reads (internal/index).
type Indexer interface {
	PutCards([]Card)
	PutIssue(*Issue)
}

// SetIndex gives the client somewhere to mirror what it reads, and to find
// people in when it keeps them (People).
func (c *Client) SetIndex(ix Indexer) {
	c.index = ix
	c.people, _ = ix.(People)
}

// issuePathKey is the issue key in /rest/api/3/issue/KEY[/…], "" for
// another path (a create, the agile API).
func issuePathKey(path string) string {
	rest, ok := strings.CutPrefix(path, "/rest/api/3/issue/")
	if !ok {
		// A service desk's request (its comments, its moves) is an issue too.
		if rest, ok = strings.CutPrefix(path, "/rest/servicedeskapi/request/"); !ok {
			return ""
		}
	}
	key, _, _ := strings.Cut(rest, "/")
	key, _, _ = strings.Cut(key, "?")
	if k, err := url.PathUnescape(key); err == nil {
		key = k
	}
	if !strings.Contains(key, "-") {
		return ""
	}
	return key
}

// unreached reports whether err means the request never left: no
// connection could be made, or the host didn't resolve.
func unreached(err error) bool {
	var dns *net.DNSError
	var op *net.OpError
	return errors.As(err, &dns) || errors.As(err, &op) && op.Op == "dial"
}

// Offline reports whether err means Jira couldn't be reached: no
// connection, no such host, or no answer in time.
func Offline(err error) bool { return unreached(err) || isTimeout(err) }

// queueWrite keeps a write that never reached Jira, when it can be sent
// again safely; false when it can't be queued.
func (c *Client) queueWrite(method, path, what string, body any, err error) bool {
	if c.queue == nil || method == http.MethodGet || issuePathKey(path) == "" || !unreached(err) {
		return false
	}
	w := PendingWrite{Method: method, Path: path, What: what, At: time.Now()}
	if body != nil {
		b, jerr := json.Marshal(body)
		if jerr != nil {
			return false
		}
		w.Body = b
	}
	c.queue(w)
	return true
}

// Replay sends a queued write; one that still can't reach Jira fails with
// ErrQueued without being queued again.
func (c *Client) Replay(ctx context.Context, w PendingWrite) error {
	var body any
	if len(w.Body) > 0 {
		body = w.Body
	}
	_, err := c.send(ctx, w.Method, w.Path, w.What, body, false)
	if err != nil && unreached(err) {
		return ErrQueued
	}
	if err == nil {
		c.Invalidate(w.Key())
	}
	return err
}

// ChangedSince reports whether key was updated after at.
func (c *Client) ChangedSince(ctx context.Context, key string, at time.Time) (bool, error) {
	var resp struct {
		Fields struct {
			Updated string `json:"updated"`
		} `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?fields=updated", key, nil, &resp); err != nil {
		return false, err
	}
	u, err := time.Parse(jiraTime, resp.Fields.Updated)
	if err != nil {
		return false, fmt.Errorf("%s: updated %q", key, resp.Fields.Updated)
	}
	return u.After(at), nil
}
