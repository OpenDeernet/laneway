package home

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/jira"
)

func TestPick(t *testing.T) {
	if got := Pick([]string{"Sprint", "nope", "work", "sprint"}); !slices.Equal(got, []string{"sprint", "work"}) {
		t.Errorf("Pick = %q", got)
	}
	if got := Pick(nil); got != nil {
		t.Errorf("Pick(nil) = %q", got)
	}
}

func TestHealth(t *testing.T) {
	start := time.Date(2026, 9, 21, 9, 0, 0, 0, time.Local)
	sp := jira.Sprint{ID: 8, Name: "S8", Start: start, End: start.AddDate(0, 0, 10)}
	done := start.AddDate(0, 0, 1)
	issues := []jira.BurnIssue{{Points: 3, Resolved: done}, {Points: 5}, {Points: 2}}
	h := Health(sp, issues, start.AddDate(0, 0, 5))
	if h.Issues != 3 || h.Done != 1 || h.Points != 10 || h.DonePoints != 3 || h.DaysLeft != 5 || h.Elapsed != 0.5 {
		t.Errorf("Health = %+v", h)
	}
	if !h.Behind() || h.Progress() != 0.3 {
		t.Errorf("progress %v, behind %v", h.Progress(), h.Behind())
	}
	if h = Health(sp, issues, start.AddDate(0, 0, 12)); h.DaysLeft != -2 || h.Elapsed != 1 {
		t.Errorf("past the end: %+v", h)
	}
	// Unestimated: progress by issues.
	h = Health(sp, []jira.BurnIssue{{Resolved: done}, {}}, start.AddDate(0, 0, 5))
	if h.Progress() != 0.5 || h.Behind() {
		t.Errorf("unestimated: %v, behind %v", h.Progress(), h.Behind())
	}
	if (Sprint{}).Behind() {
		t.Error("an empty sprint is behind")
	}
}

type fakeCounter struct{ favs []jira.QuickFilter }

func (f fakeCounter) FavouriteFilters(context.Context) ([]jira.QuickFilter, error) {
	return f.favs, nil
}
func (f fakeCounter) DeskQueues(context.Context) ([]jira.Queue, error) {
	return []jira.Queue{{Project: "HELP", Name: "Open", JQL: `project = "HELP"`, Count: 4}}, nil
}
func (f fakeCounter) Count(_ context.Context, jql string) (int, error) {
	if jql == "bad" {
		return 0, errors.New("no")
	}
	return len(jql), nil
}

func TestFilters(t *testing.T) {
	c := fakeCounter{favs: []jira.QuickFilter{{Name: "Mine", JQL: "assignee = me"}}}
	got, err := Filters(context.Background(), c, true, []string{"assignee = me", "bad", "x = 1"})
	if err != nil || len(got) != 4 {
		t.Fatalf("Filters = %+v, %v", got, err)
	}
	if got[0] != (Filter{Name: "Mine", JQL: "assignee = me", Count: 13}) || got[1].Err == "" || got[2] != (Filter{Name: "x = 1", JQL: "x = 1", Count: 5}) {
		t.Errorf("Filters = %+v", got)
	}
	// A desk's queue, counted by the desk.
	if got[3] != (Filter{Name: "HELP · Open", JQL: `project = "HELP"`, Count: 4}) {
		t.Errorf("queue = %+v", got[3])
	}
	if got, _ = Filters(context.Background(), c, false, nil); len(got) != 0 {
		t.Errorf("no favourites, nothing starred: %+v", got)
	}
}
