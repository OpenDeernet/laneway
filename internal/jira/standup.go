package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

// Standup is your own activity since since, oldest first: status and field
// changes and comments on the issues you updated, and the work you logged.
func (c *Client) Standup(ctx context.Context, since time.Time) ([]InboxEntry, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	me, err := c.Myself(ctx)
	if err != nil {
		return nil, err
	}
	return c.activity(ctx, since, []string{me.AccountID})
}

// TeamStandup is what the accounts did since since, oldest first, each
// entry's Who its person: the standup of each of them, for whoever runs it.
func (c *Client) TeamStandup(ctx context.Context, since time.Time, accountIDs []string) ([]InboxEntry, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	if len(accountIDs) == 0 {
		return nil, nil
	}
	return c.activity(ctx, since, accountIDs)
}

// activity is the accounts' changes, comments and logged work since since,
// oldest first: one search for the issues any of them updated, each read
// once.
func (c *Client) activity(ctx context.Context, since time.Time, accountIDs []string) ([]InboxEntry, error) {
	mins := int(math.Ceil(time.Since(since).Minutes())) + 1
	// updatedBy takes a user, not currentUser(): JQL won't nest functions.
	var by []string
	for _, id := range accountIDs {
		by = append(by, fmt.Sprintf(`issue in updatedBy("%s", "-%dm")`, id, mins))
	}
	issues, err := c.search(ctx, strings.Join(by, " OR ")+" ORDER BY updated DESC", []string{"summary", "updated"})
	if err != nil {
		return nil, err
	}
	issues = issues[:min(len(issues), c.inboxCap*len(accountIDs))]
	var (
		out  []InboxEntry
		mu   sync.Mutex
		wg   sync.WaitGroup
		errs = make([]error, len(issues)+1)
	)
	theirs := func(who string) bool { return slices.Contains(accountIDs, who) }
	for i, is := range issues {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var summary, updated string
			_ = json.Unmarshal(is.Fields["summary"], &summary)
			_ = json.Unmarshal(is.Fields["updated"], &updated)
			all, err := c.issueEntries(ctx, is.Key, summary, updated)
			var entries []InboxEntry
			for _, e := range all {
				if e.When.After(since) && theirs(e.WhoID) {
					entries = append(entries, e)
				}
			}
			entries = dropWorklogChanges(entries)
			mu.Lock()
			out = append(out, entries...)
			mu.Unlock()
			errs[i] = err
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		from := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
		logs, err := c.worklogsBetween(ctx, from, time.Now().AddDate(0, 0, 1), accountIDs)
		if err != nil {
			errs[len(issues)] = err
			return
		}
		for _, w := range logs {
			if w.Started.Before(since) {
				continue
			}
			what := "logged " + FormatDuration(w.Seconds)
			if w.Comment != "" {
				what += ": " + w.Comment
			}
			mu.Lock()
			out = append(out, InboxEntry{Key: w.Key, Summary: w.Summary, When: w.Started, Who: w.Author, WhoID: w.AuthorID, What: what, Logged: w.Seconds})
			mu.Unlock()
		}
	}()
	wg.Wait()
	if err := firstError(errs); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b InboxEntry) int { return a.When.Compare(b.When) })
	return out, nil
}

// issueEntries is every change and comment on key, whoever made it. They
// are kept while the issue's updated stamp stays, so a standup stepped a day
// back reads only the issues it has not seen.
func (c *Client) issueEntries(ctx context.Context, key, summary, updated string) ([]InboxEntry, error) {
	id := key + " " + updated
	if v, ok := c.actCache.Load(id); ok && updated != "" {
		return v.([]InboxEntry), nil
	}
	entries, err := c.issueActivity(ctx, key, summary, time.Time{}, func(string) bool { return true }, "")
	if err == nil && updated != "" {
		c.actCache.Store(id, entries)
	}
	return entries, err
}

// worklogFields are the changelog's side of logging work, which the
// standup lists from the worklogs themselves.
var worklogFields = []string{"timespent", "timeestimate", "WorklogId", "WorklogTimeSpent", LoggedField}

// dropWorklogChanges leaves worklogFields out of entries' changes, and an
// entry out when nothing else changed.
func dropWorklogChanges(entries []InboxEntry) []InboxEntry {
	var out []InboxEntry
	for _, e := range entries {
		if e.Changes == nil {
			out = append(out, e)
			continue
		}
		var changes []Change
		for _, ch := range e.Changes {
			if !slices.Contains(worklogFields, ch.Field) {
				changes = append(changes, ch)
			}
		}
		if len(changes) > 0 {
			e.What, e.Changes = changesText(changes), changes
			out = append(out, e)
		}
	}
	return out
}

// PreviousWorkday is the start of the last workday before now's day, the
// workdays Monday to Friday when none are given: a Monday looks back to
// Friday.
func PreviousWorkday(now time.Time, workdays []time.Weekday) time.Time {
	if len(workdays) == 0 {
		workdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	}
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	for !slices.Contains(workdays, d.Weekday()) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}
