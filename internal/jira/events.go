package jira

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SiteChange is one entry of laneway-server's change feed: something in a
// project changed; refetch what shows it. Jira has no such feed.
type SiteChange struct {
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`   // issue, comment, worklog, sprint, board, ...
	Action    string `json:"action"` // put or delete
	ID        string `json:"id"`
	Key       string `json:"key"`
	ProjectID int    `json:"projectId"`
}

// ErrNoEvents means the site has no change feed (it is Jira): keep polling.
var ErrNoEvents = errors.New("jira: the site has no change feed")

// Events follows laneway-server's change stream until ctx ends, calling fn
// for each change. It reconnects (resuming after the last change seen)
// when the stream drops, and returns ErrNoEvents at once against Jira.
func (c *Client) Events(ctx context.Context, fn func(SiteChange)) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	// Jira has no feed: don't ask it. Unknown yet (offline): try the
	// stream, which retries until the site answers.
	if is, err := c.Laneway(ctx); err == nil && !is {
		return ErrNoEvents
	}
	client := &http.Client{Transport: c.http.Transport} // no timeout: the stream stays open
	var last int64
	backoff := time.Second
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/rest/laneway/1/events", nil)
		if err != nil {
			return err
		}
		if _, err := c.authorize(req); err != nil {
			return err
		}
		req.Header.Set("Accept", "text/event-stream")
		if last > 0 {
			req.Header.Set("Last-Event-ID", strconv.FormatInt(last, 10))
		}
		resp, err := client.Do(req)
		if err == nil && (resp.StatusCode == http.StatusNotFound || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")) {
			resp.Body.Close()
			return ErrNoEvents
		}
		if err == nil && resp.StatusCode == http.StatusOK {
			backoff = time.Second
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			var data string
			for sc.Scan() {
				line := sc.Text()
				switch {
				case strings.HasPrefix(line, "data:"):
					if len(data) < 1<<20 { // past that it's no change of ours: let it fail to decode
						data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					}
				case line == "" && data != "":
					var ch SiteChange
					if json.Unmarshal([]byte(data), &ch) == nil {
						if ch.Seq > last {
							last = ch.Seq
						}
						fn(ch)
					}
					data = ""
				}
			}
			resp.Body.Close()
		} else if err == nil {
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
	return ctx.Err()
}
