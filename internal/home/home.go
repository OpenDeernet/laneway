// Package home is the start screen both front ends draw (ui.home): which
// widgets in which order, the active sprint's health, and a count per saved
// search.
package home

import (
	"context"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cornedor/laneway/internal/jira"
)

// Widgets are the names ui.home picks from, in the order an empty pick
// would show them.
var Widgets = []string{"work", "inbox", "sprint", "timer", "filters"}

// Retired are widgets laneway dropped; old configs naming them stay quiet.
var Retired = []string{"reviews"}

// Pick is names' known widgets in their order, each once: the home screen,
// none when it names none.
func Pick(names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if slices.Contains(Widgets, n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// Sprint is how the active sprint stands.
type Sprint struct {
	ID         int
	Name, Goal string
	// DaysLeft are the days to its end: 0 on its last day, below 0 past it.
	DaysLeft     int
	Issues, Done int
	Points       float64
	DonePoints   float64
	// Elapsed is the share of its time gone, 0 to 1.
	Elapsed float64
}

// Health is sp's standing from its issues (SprintBurn's): done is resolved.
func Health(sp jira.Sprint, issues []jira.BurnIssue, now time.Time) Sprint {
	out := Sprint{ID: sp.ID, Name: sp.Name, Goal: sp.Goal, Issues: len(issues)}
	for _, is := range issues {
		out.Points += is.Points
		if !is.Resolved.IsZero() {
			out.Done++
			out.DonePoints += is.Points
		}
	}
	if !sp.End.IsZero() {
		day := func(t time.Time) time.Time {
			y, m, d := t.Local().Date()
			return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
		}
		out.DaysLeft = int(math.Round(day(sp.End).Sub(day(now)).Hours() / 24))
		if !sp.Start.IsZero() && sp.End.After(sp.Start) {
			out.Elapsed = min(max(float64(now.Sub(sp.Start))/float64(sp.End.Sub(sp.Start)), 0), 1)
		}
	}
	return out
}

// Progress is the share done: by points when the sprint has them, else by
// issues.
func (s Sprint) Progress() float64 {
	if s.Points > 0 {
		return s.DonePoints / s.Points
	}
	if s.Issues > 0 {
		return float64(s.Done) / float64(s.Issues)
	}
	return 0
}

// Behind: the work done trails the time gone by more than a tenth.
func (s Sprint) Behind() bool { return s.Issues > 0 && s.Progress() < s.Elapsed-0.1 }

// Filter is a saved search and how many issues it finds now; Err when
// counting failed.
type Filter struct {
	Name, JQL string
	Count     int
	Err       string `json:",omitempty"`
}

// Counter is the Jira the filters widget reads.
type Counter interface {
	FavouriteFilters(ctx context.Context) ([]jira.QuickFilter, error)
	DeskQueues(ctx context.Context) ([]jira.Queue, error)
	Count(ctx context.Context, jql string) (int, error)
}

// Filters counts your starred Jira filters (when favourites) and the starred
// searches, at once; the starred searches are named by their JQL. With the
// favourites come the service desks' queues, counted by the desk.
func Filters(ctx context.Context, c Counter, favourites bool, starred []string) ([]Filter, error) {
	var out []Filter
	if favourites {
		fs, err := c.FavouriteFilters(ctx)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			out = append(out, Filter{Name: f.Name, JQL: f.JQL})
		}
	}
	for _, q := range starred {
		if !slices.ContainsFunc(out, func(f Filter) bool { return f.JQL == q }) {
			out = append(out, Filter{Name: q, JQL: q})
		}
	}
	var queues []jira.Queue
	var wg sync.WaitGroup
	if favourites {
		wg.Go(func() { queues, _ = c.DeskQueues(ctx) }) // a failure only leaves them out
	}
	for i := range out {
		wg.Go(func() {
			n, err := c.Count(ctx, out[i].JQL)
			out[i].Count = n
			if err != nil {
				out[i].Err = err.Error()
			}
		})
	}
	wg.Wait()
	for _, q := range queues {
		out = append(out, Filter{Name: q.Label(), JQL: q.JQL, Count: q.Count})
	}
	return out, nil
}
