package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// Personal views: my work, time tracking and the inbox.

const defaultMyWorkJQL = "assignee = currentUser() AND (statusCategory != Done OR resolved >= -7d) ORDER BY updated DESC"

// The TUI keeps the same marks; sharing the keys keeps read state in step.
const (
	inboxMarksMeta = "jira_tab:inbox_marks"
	inboxFloorMeta = "jira_tab:inbox_seen"
	// inboxUnreadMeta is the unread count laneway prompt shows.
	inboxUnreadMeta = "jira_tab:inbox_unread"
)

func init() {
	get("/work", myWork)
	get("/worklogs", myWorklogs)
	post("/worklog/{key}", addWorklog)
	put("/worklog/{key}/{id}", updateWorklog)
	del("/worklog/{key}/{id}", deleteWorklog)
	del("/worklog/{key}", deleteWorklogs)
	post("/worklog/{key}/move", moveWorklogs)
	get("/inbox", inbox)
	put("/inbox/state/{key}", putInboxState)
}

func validKey(r *http.Request) (string, error) {
	key := r.PathValue("key")
	if !jira.ValidKey(key) {
		return "", badRequest(i18n.T("bad issue key"))
	}
	return key, nil
}

func myWork(ctx context.Context, s *Server, r *http.Request) (any, error) {
	jql := Q(r, "jql")
	if jql == "" {
		jql = strings.TrimSpace(s.UIConfig().MyWorkJQL)
	}
	if jql == "" {
		jql = defaultMyWorkJQL
	}
	cards, err := s.Client().SearchCards(ctx, jql)
	return map[string]any{"cards": cards}, err
}

// day reads a YYYY-MM-DD query parameter as a local midnight.
func day(r *http.Request, name string) (time.Time, error) {
	t, err := time.ParseInLocation(time.DateOnly, Q(r, name), time.Local)
	if err != nil {
		return t, badRequest(i18n.Tf("bad %s: want YYYY-MM-DD", name))
	}
	return t, nil
}

// myWorklogs: ?from=DAY&to=DAY, to exclusive.
func myWorklogs(ctx context.Context, s *Server, r *http.Request) (any, error) {
	from, err := day(r, "from")
	if err != nil {
		return nil, err
	}
	to, err := day(r, "to")
	if err != nil {
		return nil, err
	}
	if !to.After(from) || to.Sub(from) > 62*24*time.Hour {
		return nil, badRequest(i18n.T("to must follow from, within two months"))
	}
	logs, err := s.Client().MyWorklogsBetween(ctx, from, to)
	if logs == nil {
		logs = []jira.Worklog{}
	}
	// canMove is false on Server and DC (serverInfo, read here first) and
	// once the site turned a move away.
	s.Client().Laneway(ctx)
	return map[string]any{"worklogs": logs, "canMove": s.Client().MovesWorklogs()}, err
}

type worklogBody struct {
	Seconds int
	Started string // RFC 3339; "" on an update keeps it
	Comment *string
	Left    string // new worklogs: "" takes it off the estimate, "keep", or "2h"
}

func (b worklogBody) start() (time.Time, error) {
	if b.Started == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, b.Started)
	if err != nil {
		return t, badRequest(i18n.T("bad Started: want RFC 3339"))
	}
	return t, nil
}

func addWorklog(ctx context.Context, s *Server, r *http.Request) (any, error) {
	key, err := validKey(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[worklogBody](r)
	if err != nil {
		return nil, err
	}
	started, err := b.start()
	if err != nil {
		return nil, err
	}
	if started.IsZero() {
		started = time.Now().Add(-time.Duration(b.Seconds) * time.Second)
	}
	comment := ""
	if b.Comment != nil {
		comment = *b.Comment
	}
	return nil, s.Client().AddWorklog(ctx, key, b.Seconds, started, comment, b.Left)
}

func updateWorklog(ctx context.Context, s *Server, r *http.Request) (any, error) {
	key, err := validKey(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[worklogBody](r)
	if err != nil {
		return nil, err
	}
	started, err := b.start()
	if err != nil {
		return nil, err
	}
	return nil, s.Client().UpdateWorklog(ctx, key, r.PathValue("id"), b.Seconds, started, b.Comment)
}

func deleteWorklog(ctx context.Context, s *Server, r *http.Request) (any, error) {
	key, err := validKey(r)
	if err != nil {
		return nil, err
	}
	return nil, s.Client().DeleteWorklog(ctx, key, r.PathValue("id"))
}

// worklogsBody is a bulk delete's or move's worklogs, all on the path's
// issue; To is the move's issue.
type worklogsBody struct {
	IDs []string
	To  string
}

func deleteWorklogs(ctx context.Context, s *Server, r *http.Request) (any, error) {
	key, err := validKey(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[worklogsBody](r)
	if err != nil {
		return nil, err
	}
	if len(b.IDs) == 0 {
		return nil, badRequest(i18n.T("no worklogs"))
	}
	return nil, s.Client().DeleteWorklogs(ctx, key, b.IDs)
}

func moveWorklogs(ctx context.Context, s *Server, r *http.Request) (any, error) {
	key, err := validKey(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[worklogsBody](r)
	if err != nil {
		return nil, err
	}
	switch {
	case len(b.IDs) == 0:
		return nil, badRequest(i18n.T("no worklogs"))
	case !jira.ValidKey(b.To):
		return nil, badRequest(i18n.T("bad issue key"))
	}
	return nil, s.Client().MoveWorklogs(ctx, key, b.IDs, b.To)
}

// ---- inbox

// InboxThread is an issue and what others did on it. The inbox reads every
// configured site: ID is site/key, URL opens another site's in Jira.
type InboxThread struct {
	jira.InboxIssue
	ID, Site, URL string
	Entries       []jira.InboxEntry
}

func (t InboxThread) latest() time.Time { return t.Entries[len(t.Entries)-1].When }

// inboxMark mirrors the TUI's stored marks, Unix milliseconds.
type inboxMark struct {
	R int64 `json:"r,omitempty"`
	D int64 `json:"d,omitempty"`
	S int64 `json:"s,omitempty"`
}

// InboxMark is a thread's marks as the API speaks them.
type InboxMark struct{ Read, Done, Snooze int64 }

var (
	inboxMu    sync.Mutex
	inboxCache = map[string]InboxThread{}   // by site/key, reused while the issue is not updated
	inboxLast  = map[string][]InboxThread{} // the last threads by shown site, to count the unread again
)

// inboxLookback is ui.inbox_lookback, the TUI's week when unset.
func inboxLookback(s *Server) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(s.UIConfig().InboxLookback)); err == nil && d > 0 {
		return d
	}
	return 7 * 24 * time.Hour
}

// inbox: every site's threads with news, newest first; ?days=N overrides
// ui.inbox_lookback. Another site failing only leaves it out.
func inbox(ctx context.Context, s *Server, r *http.Request) (any, error) {
	look := inboxLookback(s)
	if days, _ := strconv.Atoi(Q(r, "days")); days >= 1 && days <= 90 {
		look = time.Duration(days) * 24 * time.Hour
	}
	since := time.Now().Add(-look)
	var (
		out     []InboxThread
		mu      sync.Mutex
		wg      sync.WaitGroup
		shownEr error
	)
	for _, sc := range s.siteClients() {
		site := sc.Site
		wg.Go(func() {
			ts, err := siteInbox(ctx, sc.Client, site, since)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && site == s.opt.Site {
				shownEr = err
			}
			out = append(out, ts...)
		})
	}
	wg.Wait()
	if shownEr != nil {
		return nil, shownEr
	}
	slices.SortStableFunc(out, func(a, b InboxThread) int { return b.latest().Compare(a.latest()) })
	marks := map[string]InboxMark{}
	for id, m := range readMarks(s) {
		marks[id] = InboxMark{m.R, m.D, m.S}
	}
	floor := inboxFloor(s)
	inboxMu.Lock()
	inboxLast[s.opt.Site] = out
	inboxMu.Unlock()
	setInboxUnread(s, out, floor)
	if out == nil {
		out = []InboxThread{} // nothing new: a list, not null (the page filters it)
	}
	return map[string]any{"threads": out, "marks": marks, "floor": floor, "lookback": int((look + 24*time.Hour - 1) / (24 * time.Hour)), "site": s.opt.Site}, nil
}

// siteInbox is one site's threads with news, those of issues not updated
// since the last read reused. An issue that fails to read keeps its last
// thread, or is left out, and is read again next time; only when every read
// fails does the site fail.
func siteInbox(ctx context.Context, c *jira.Client, site string, since time.Time) ([]InboxThread, error) {
	issues, err := c.InboxIssues(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]InboxThread, len(issues))
	errs := make([]error, len(issues))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	read := 0
	for i, is := range issues {
		out[i] = InboxThread{InboxIssue: is, ID: site + "/" + is.Key, Site: site, URL: c.BrowseURL(is.Key)}
		inboxMu.Lock()
		k, ok := inboxCache[out[i].ID]
		inboxMu.Unlock()
		if ok && k.Updated.Equal(is.Updated) {
			out[i].Entries = slices.DeleteFunc(slices.Clone(k.Entries), func(e jira.InboxEntry) bool { return !e.When.After(since) })
			continue
		}
		read++
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i].Entries, errs[i] = c.IssueInbox(ctx, is.Key, is.Summary, since)
		})
	}
	wg.Wait()
	failed := 0
	inboxMu.Lock()
	for i, t := range out {
		if errs[i] == nil {
			inboxCache[t.ID] = t
			continue
		}
		failed++
		out[i] = inboxCache[t.ID] // its old Updated: read again next time
	}
	inboxMu.Unlock()
	if failed > 0 && failed == read {
		return nil, errors.Join(errs...)
	}
	return slices.DeleteFunc(out, func(t InboxThread) bool { return len(t.Entries) == 0 }), nil
}

// setInboxUnread keeps the unread count, neither done nor snoozed, for
// laneway prompt as the TUI's header does.
func setInboxUnread(s *Server, threads []InboxThread, floor int64) {
	marks, now, n := readMarks(s), time.Now().UnixMilli(), 0
	for _, t := range threads {
		m, at := marks[t.ID], t.latest().UnixMilli()
		if at > cmp.Or(m.R, floor) && !(m.D != 0 && at <= m.D) && m.S <= now {
			n++
		}
	}
	_ = s.opt.Store.SetMeta(inboxUnreadMeta, strconv.Itoa(n))
}

func readMarks(s *Server) map[string]inboxMark {
	marks := map[string]inboxMark{}
	if raw, ok, _ := s.opt.Store.GetMeta(inboxMarksMeta); ok {
		_ = json.Unmarshal([]byte(raw), &marks)
	}
	return marks
}

// inboxFloor is when news starts counting as unread, in Unix milliseconds:
// the first time the inbox was read, a day before.
func inboxFloor(s *Server) int64 {
	if v, ok, _ := s.opt.Store.GetMeta(inboxFloorMeta); ok {
		if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
			return sec * 1000
		}
	}
	floor := time.Now().Add(-24 * time.Hour)
	_ = s.opt.Store.SetMeta(inboxFloorMeta, strconv.FormatInt(floor.Unix(), 10))
	return floor.UnixMilli()
}

// putInboxState: the body's Site is the thread's ("" the jira: block's),
// the shown one when left out.
func putInboxState(ctx context.Context, s *Server, r *http.Request) (any, error) {
	key, err := validKey(r)
	if err != nil {
		return nil, err
	}
	b, err := Body[struct {
		InboxMark
		Site *string
	}](r)
	if err != nil {
		return nil, err
	}
	site := s.opt.Site
	if b.Site != nil && *b.Site != site {
		if !slices.Contains(s.opt.Sites, *b.Site) {
			return nil, httpError{http.StatusBadRequest, i18n.T("unknown site")}
		}
		site = *b.Site
	}
	inboxMu.Lock()
	marks := readMarks(s)
	marks[site+"/"+key] = inboxMark{b.Read, b.Done, b.Snooze}
	old := time.Now().Add(-inboxLookback(s)).UnixMilli()
	maps.DeleteFunc(marks, func(_ string, m inboxMark) bool { return max(m.R, m.D, m.S) < old })
	raw, _ := json.Marshal(marks)
	err = s.opt.Store.SetMeta(inboxMarksMeta, string(raw))
	last := inboxLast[s.opt.Site]
	inboxMu.Unlock()
	if err == nil {
		setInboxUnread(s, last, inboxFloor(s))
	}
	return nil, err
}

// workdays are ui.workdays as weekdays, nil for the default.
func workdays(s *Server) []time.Weekday {
	var out []time.Weekday
	for _, d := range s.UIConfig().Workdays {
		for wd := time.Sunday; wd <= time.Saturday; wd++ {
			if len(d) >= 3 && strings.HasPrefix(strings.ToLower(wd.String()), strings.ToLower(d[:3])) {
				out = append(out, wd)
			}
		}
	}
	return out
}
