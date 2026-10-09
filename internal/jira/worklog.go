package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Time tracking: log work on an issue, and read back a day of your own.

// LeftKeep leaves the remaining estimate as it is when logging work.
const LeftKeep = "keep"

// SetEstimate sets key's original estimate ("2d 4h").
func (c *Client) SetEstimate(ctx context.Context, key, estimate string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"fields": map[string]any{"timetracking": map[string]string{"originalEstimate": estimate}}}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// jiraTime is the timestamp shape worklogs take and return.
const jiraTime = "2006-01-02T15:04:05.000-0700"

// AddWorklog logs seconds of work on key, started at started, with an
// optional comment. left is what the remaining estimate becomes: "" takes
// the time off it (Jira's auto), LeftKeep leaves it, else a duration ("2h").
func (c *Client) AddWorklog(ctx context.Context, key string, seconds int, started time.Time, comment, left string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	if seconds < 60 {
		return fmt.Errorf("jira: log at least a minute")
	}
	body := map[string]any{"timeSpentSeconds": seconds, "started": started.Format(jiraTime)}
	if strings.TrimSpace(comment) != "" {
		body["comment"] = textToADF(comment, nil)
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/worklog"
	switch left {
	case "":
	case LeftKeep:
		path += "?adjustEstimate=leave"
	default:
		path += "?adjustEstimate=new&newEstimate=" + url.QueryEscape(left)
	}
	if err := c.do(ctx, http.MethodPost, path, key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// UpdateWorklog sets worklog id's time, its comment (markdown) unless
// comment is nil, and its start when started isn't zero (moved to another
// day).
func (c *Client) UpdateWorklog(ctx context.Context, key, id string, seconds int, started time.Time, comment *string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	if seconds < 60 {
		return fmt.Errorf("jira: log at least a minute")
	}
	body := map[string]any{"timeSpentSeconds": seconds}
	if comment != nil { // an empty one clears it
		body["comment"] = MarkdownToADF(*comment)
	}
	if !started.IsZero() {
		body["started"] = started.Format(jiraTime)
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/worklog/" + url.PathEscape(id)
	if err := c.do(ctx, http.MethodPut, path, key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// DeleteWorklog removes worklog id from key.
func (c *Client) DeleteWorklog(ctx context.Context, key, id string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/worklog/" + url.PathEscape(id)
	if err := c.do(ctx, http.MethodDelete, path, key, nil, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// ErrNoWorklogMove is a site without Jira Cloud's worklog move (Server,
// Data Center), for errors.Is.
var ErrNoWorklogMove = errors.New("jira: this site can't move worklogs")

// missingRoute is whether err says the site lacks the endpoint: a 405, or
// a 404 without Jira's error body.
func missingRoute(err error) bool {
	var nf notFound
	var re *RequestError
	return errors.As(err, &nf) && !nf.said || errors.As(err, &re) && re.code == http.StatusMethodNotAllowed
}

// worklogIDs is ids as the bulk endpoints take them, numbers.
func worklogIDs(ids []string) ([]int, error) {
	out := make([]int, len(ids))
	for i, id := range ids {
		n, err := strconv.Atoi(id)
		if err != nil {
			return nil, fmt.Errorf("jira: bad worklog id %q", id)
		}
		out[i] = n
	}
	return out, nil
}

// DeleteWorklogs removes worklogs ids from key in one request. A site
// without the bulk delete, or one that turns it away with a 404 (it takes
// Delete all worklogs), gets one delete per id.
func (c *Client) DeleteWorklogs(ctx context.Context, key string, ids []string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	nums, err := worklogIDs(ids)
	if err != nil || len(ids) == 0 {
		return err
	}
	c.mu.Lock()
	bulk := !c.noBulkDelete && len(ids) > 1
	c.mu.Unlock()
	if bulk {
		path := "/rest/api/3/issue/" + url.PathEscape(key) + "/worklog"
		err := c.do(ctx, http.MethodDelete, path, key, map[string]any{"ids": nums}, nil)
		if err == nil || !errors.Is(err, ErrNotFound) && !missingRoute(err) {
			c.Invalidate(key)
			return err
		}
		if missingRoute(err) {
			c.mu.Lock()
			c.noBulkDelete = true
			c.mu.Unlock()
		}
	}
	for _, id := range ids {
		if err := c.DeleteWorklog(ctx, key, id); err != nil {
			return err
		}
	}
	return nil
}

// MovesWorklogs is false for Jira Server or Data Center (serverInfo,
// once read), and once the site turned a worklog move away as unknown.
func (c *Client) MovesWorklogs() bool {
	if !c.Enabled() {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.noWorklogMove && (c.site == nil || !c.site.SelfHosted)
}

// MoveWorklogs moves worklogs ids from key to the issue to, keeping their
// author and start. ErrNoWorklogMove is a site without the move.
func (c *Client) MoveWorklogs(ctx context.Context, key string, ids []string, to string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	if !c.MovesWorklogs() {
		return ErrNoWorklogMove
	}
	nums, err := worklogIDs(ids)
	if err != nil || len(ids) == 0 {
		return err
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/worklog/move"
	err = c.do(ctx, http.MethodPost, path, key, map[string]any{"ids": nums, "issueIdOrKey": to}, nil)
	if missingRoute(err) {
		c.mu.Lock()
		c.noWorklogMove = true
		c.mu.Unlock()
		return ErrNoWorklogMove
	}
	c.Invalidate(key)
	c.Invalidate(to)
	return err
}

// Worklog is one entry: your own (MyWorklogs) or anyone's on an issue
// (IssueWorklogs, with Author).
type Worklog struct {
	ID           string
	Key, Summary string
	Author       string
	AuthorID     string
	Seconds      int
	Started      time.Time
	Comment      string
}

// MyWorklogs lists what you logged on day, in the order you started it.
func (c *Client) MyWorklogs(ctx context.Context, day time.Time) ([]Worklog, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return c.MyWorklogsBetween(ctx, from, from.AddDate(0, 0, 1))
}

// MyWorklogsBetween lists what you logged from from until to (both
// midnights), in the order you started it.
func (c *Client) MyWorklogsBetween(ctx context.Context, from, to time.Time) ([]Worklog, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	me, err := c.Myself(ctx)
	if err != nil {
		return nil, err
	}
	return c.worklogsBetween(ctx, from, to, []string{me.AccountID})
}

// worklogsBetween is what the accounts logged from from until to, with
// each entry's Author.
func (c *Client) worklogsBetween(ctx context.Context, from, to time.Time, accountIDs []string) ([]Worklog, error) {
	quoted := make([]string, len(accountIDs))
	for i, id := range accountIDs {
		quoted[i] = strconv.Quote(id)
	}
	jql := fmt.Sprintf(`worklogAuthor in (%s) AND worklogDate >= "%s" AND worklogDate < "%s"`,
		strings.Join(quoted, ", "), from.Format(time.DateOnly), to.Format(time.DateOnly))
	issues, err := c.search(ctx, jql, []string{"summary"})
	if err != nil {
		return nil, err
	}
	var (
		out  []Worklog
		mu   sync.Mutex
		wg   sync.WaitGroup
		errs = make([]error, len(issues))
	)
	for i, is := range issues {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var summary string
			_ = json.Unmarshal(is.Fields["summary"], &summary)
			var resp struct {
				Worklogs []struct {
					ID               string          `json:"id"`
					Author           user            `json:"author"`
					Started          string          `json:"started"`
					TimeSpentSeconds int             `json:"timeSpentSeconds"`
					Comment          json.RawMessage `json:"comment"`
				} `json:"worklogs"`
			}
			path := "/rest/api/3/issue/" + url.PathEscape(is.Key) + "/worklog?startedAfter=" +
				strconv.FormatInt(from.UnixMilli(), 10) + "&startedBefore=" + strconv.FormatInt(to.UnixMilli(), 10)
			if errs[i] = c.do(ctx, http.MethodGet, path, is.Key, nil, &resp); errs[i] != nil {
				return
			}
			for _, w := range resp.Worklogs {
				started, _ := time.Parse(jiraTime, w.Started)
				if !slices.Contains(accountIDs, w.Author.AccountID) || started.Before(from) || !started.Before(to) {
					continue
				}
				wl := Worklog{ID: w.ID, Key: is.Key, Summary: summary, Author: w.Author.DisplayName, AuthorID: w.Author.AccountID, Seconds: w.TimeSpentSeconds, Started: started}
				if len(w.Comment) > 0 && string(w.Comment) != "null" {
					wl.Comment = strings.TrimSpace(adfToMarkdown(w.Comment))
				}
				mu.Lock()
				out = append(out, wl)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(out, func(a, b Worklog) int { return a.Started.Compare(b.Started) })
	return out, nil
}

// IssueWorklogs is everyone's work logged on key, oldest first.
func (c *Client) IssueWorklogs(ctx context.Context, key string) ([]Worklog, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var resp struct {
		Worklogs []struct {
			ID               string          `json:"id"`
			Author           user            `json:"author"`
			Started          string          `json:"started"`
			TimeSpentSeconds int             `json:"timeSpentSeconds"`
			Comment          json.RawMessage `json:"comment"`
		} `json:"worklogs"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/worklog", key, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Worklog, 0, len(resp.Worklogs))
	for _, w := range resp.Worklogs {
		started, _ := time.Parse(jiraTime, w.Started)
		wl := Worklog{ID: w.ID, Key: key, Author: w.Author.DisplayName, Seconds: w.TimeSpentSeconds, Started: started}
		if len(w.Comment) > 0 && string(w.Comment) != "null" {
			wl.Comment = strings.TrimSpace(adfToMarkdown(w.Comment))
		}
		out = append(out, wl)
	}
	slices.SortStableFunc(out, func(a, b Worklog) int { return a.Started.Compare(b.Started) })
	return out, nil
}

// ParseDuration reads leading work time off s: "1h 30m", "1h30m", "1.5h",
// "45m", "2d" (a day is 8h), and returns the seconds and what follows.
func ParseDuration(s string) (int, string, error) {
	fields := strings.Fields(s)
	secs, used := 0.0, 0
	for _, f := range fields {
		n, ok := durationToken(f)
		if !ok {
			break
		}
		secs += n
		used++
	}
	if used == 0 {
		return 0, s, fmt.Errorf("start with a time: 1h 30m, 1.5h, 45m")
	}
	return int(secs), strings.Join(fields[used:], " "), nil
}

// durationToken is one token's seconds: "1h30m" or "2.5h".
func durationToken(t string) (float64, bool) {
	unit := map[byte]float64{'d': 8 * 3600, 'h': 3600, 'm': 60}
	total, num := 0.0, ""
	for i := 0; i < len(t); i++ {
		ch := t[i]
		if u, ok := unit[ch|0x20]; ok && num != "" {
			v, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, false
			}
			total += v * u
			num = ""
			continue
		}
		if (ch >= '0' && ch <= '9') || ch == '.' {
			num += string(ch)
			continue
		}
		return 0, false
	}
	return total, num == "" && total > 0
}

// FormatDuration is seconds as "2h 5m".
func FormatDuration(secs int) string {
	h, m := secs/3600, (secs%3600+30)/60
	if m == 60 {
		h, m = h+1, 0
	}
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
