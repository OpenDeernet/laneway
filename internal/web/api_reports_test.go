package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/demo"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/store"
)

func reportsPost(t *testing.T, url string, body any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestReportsAndPlanning(t *testing.T) {
	ts := searchDemo(t)
	var boards []struct{ ID int }
	if searchGet(t, ts.URL+"/api/projects/DEMO/boards", &boards) != 200 || len(boards) == 0 {
		t.Fatal("no boards")
	}
	b := boards[0].ID
	at := func(p string) string { return ts.URL + "/api" + p }

	var plan struct {
		Sprints []struct {
			ID    int
			Cards []struct{ Key string }
		}
		Backlog struct{ Cards []struct{ Key string } }
	}
	if c := searchGet(t, at("/plan/"+itoa(b)), &plan); c != 200 || len(plan.Sprints) == 0 || len(plan.Backlog.Cards) == 0 {
		t.Fatalf("plan: %d %+v", c, plan)
	}
	// ?jql= narrows every section (the quick filters on); QuickFilters lists them.
	var narrowed struct {
		Sprints      []struct{ Cards []struct{ Key string } }
		Backlog      struct{ Cards []struct{ Key string } }
		QuickFilters []struct{ ID int }
	}
	only := plan.Backlog.Cards[0].Key
	if c := searchGet(t, at("/plan/"+itoa(b)+"?jql="+url.QueryEscape("key = "+only)), &narrowed); c != 200 || narrowed.QuickFilters == nil {
		t.Fatalf("plan ?jql: %d %+v", c, narrowed)
	}
	n := len(narrowed.Backlog.Cards)
	for _, s := range narrowed.Sprints {
		n += len(s.Cards)
	}
	if n != 1 || len(narrowed.Backlog.Cards) != 1 || narrowed.Backlog.Cards[0].Key != only {
		t.Errorf("plan ?jql=key = %s: %+v", only, narrowed)
	}

	var sp struct {
		Sprint  struct{ ID int }
		Issues  []struct{ Key string }
		Columns []struct{ Name string }
	}
	if c := searchGet(t, at("/reports/sprint/"+itoa(b)), &sp); c != 200 || len(sp.Issues) == 0 || len(sp.Columns) == 0 {
		t.Fatalf("sprint report: %d %+v", c, sp)
	}
	var vel []struct{ Name string }
	if c := searchGet(t, at("/reports/velocity/"+itoa(b)+"?n=3"), &vel); c != 200 || len(vel) == 0 {
		t.Fatalf("velocity: %d %v", c, vel)
	}
	var retro struct {
		Sprints []struct{ Name string }
		Cards   map[string]struct{ Summary string }
	}
	if c := searchGet(t, at("/reports/retro/"+itoa(b)), &retro); c != 200 || len(retro.Sprints) == 0 {
		t.Fatalf("retro: %d %+v", c, retro)
	}
	var cycle []struct{ Key string }
	if c := searchGet(t, at("/reports/cycle/DEMO?weeks=26"), &cycle); c != 200 || len(cycle) == 0 {
		t.Fatalf("cycle: %d %v", c, cycle)
	}
	var vers []struct{ Name string }
	if c := searchGet(t, at("/reports/versions/DEMO"), &vers); c != 200 || len(vers) == 0 {
		t.Fatalf("versions: %d %v", c, vers)
	}
	var rm struct{ Epics []struct{ Key string } }
	if c := searchGet(t, at("/roadmap/DEMO"), &rm); c != 200 || len(rm.Epics) == 0 {
		t.Fatalf("roadmap: %d %+v", c, rm)
	}

	key := plan.Backlog.Cards[0].Key
	if c := reportsPost(t, at("/plan/move"), map[string]any{"Keys": []string{key}, "Sprint": plan.Sprints[0].ID}); c != 200 {
		t.Fatalf("move: %d", c)
	}
	if c := reportsPost(t, at("/plan/move"), map[string]any{"Keys": []string{}}); c != 400 {
		t.Fatalf("empty move: %d", c)
	}
	if len(plan.Backlog.Cards) > 2 {
		ks := []string{plan.Backlog.Cards[1].Key, plan.Backlog.Cards[2].Key}
		if c := reportsPost(t, at("/plan/rank"), map[string]any{"Keys": ks, "Other": key, "After": true}); c != 200 {
			t.Fatalf("rank: %d", c)
		}
	}
	if c := reportsPost(t, at("/plan/rank"), map[string]any{"Keys": []string{key}, "Other": "nope"}); c != 400 {
		t.Fatalf("bad rank: %d", c)
	}
	if c := reportsPost(t, at("/plan/sprints"), map[string]any{"Board": b, "Name": "DEMO Sprint X"}); c != 200 {
		t.Fatalf("create: %d", c)
	}
	if c := reportsPost(t, at("/plan/sprints"), map[string]any{"Board": b}); c != 400 {
		t.Fatalf("nameless create: %d", c)
	}
	sid := itoa(plan.Sprints[len(plan.Sprints)-1].ID)
	if c := reportsPost(t, at("/plan/sprints/"+sid+"/start"), map[string]any{"End": "2001-01-01"}); c != 400 {
		t.Fatalf("past end: %d", c)
	}
	if c := reportsPost(t, at("/plan/sprints/"+sid+"/update"), map[string]any{"Name": "Renamed", "Goal": "ship"}); c != 200 {
		t.Fatalf("update: %d", c)
	}
	if c := reportsPost(t, at("/plan/sprints/"+sid+"/close"), map[string]any{"Board": b}); c != 200 {
		t.Fatalf("close: %d", c)
	}
	if c := reportsPost(t, at("/roadmap/"+rm.Epics[0].Key+"/dates"), map[string]any{"Start": "2030-01-01", "End": "2030-02-01"}); c != 200 {
		t.Fatalf("dates: %d", c)
	}
	var after struct {
		Epics []struct{ Key, Start, End string }
	}
	if c := searchGet(t, at("/roadmap/DEMO?fresh=1"), &after); c != 200 || !strings.HasPrefix(after.Epics[0].Start, "2030-01-01") || !strings.HasPrefix(after.Epics[0].End, "2030-02-01") {
		t.Errorf("dates after: %d %+v", c, after.Epics[0])
	}
	if c := reportsPost(t, at("/roadmap/"+rm.Epics[0].Key+"/dates"), map[string]any{"End": "soon"}); c != 400 {
		t.Fatalf("bad date: %d", c)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TestPlanMeetings: with ui.calendar and ui.capacity, each dated sprint
// says your capacity less your meetings.
func TestPlanMeetings(t *testing.T) {
	base, stop, err := demo.New(time.Now()).Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	dir := t.TempDir()
	ics := filepath.Join(dir, "cal.ics")
	start := time.Now().AddDate(0, 0, -30).UTC().Format("20060102") + "T080000Z"
	end := time.Now().AddDate(0, 0, -30).UTC().Format("20060102") + "T120000Z"
	if err := os.WriteFile(ics, []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:a\r\nDTSTAMP:"+start+"\r\nDTSTART:"+start+
		"\r\nDTEND:"+end+"\r\nRRULE:FREQ=DAILY\r\nSUMMARY:Meetings\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cl := jira.New(jira.Config{BaseURL: base, Email: "d@example.com", APIToken: "x", Projects: []string{"DEMO"}})
	ts := httptest.NewServer(New(context.Background(), Options{Client: cl, Store: st, Site: "demo", Demo: true,
		UI: config.UIConfig{Calendar: ics, Capacity: map[string]float64{"default": 10}}}))
	t.Cleanup(ts.Close)
	var boards []struct{ ID int }
	if searchGet(t, ts.URL+"/api/projects/DEMO/boards", &boards) != 200 || len(boards) == 0 {
		t.Fatal("no boards")
	}
	var plan struct {
		Sprints []struct {
			State string
			Mine  *planMine
		}
		Calendar string
	}
	if c := searchGet(t, ts.URL+"/api/plan/"+itoa(boards[0].ID), &plan); c != 200 || plan.Calendar != "" {
		t.Fatalf("plan: %d %q", c, plan.Calendar)
	}
	for _, sp := range plan.Sprints {
		if sp.State != "active" {
			continue
		}
		if m := sp.Mine; m == nil || m.Name == "" || m.Hours <= 0 || m.Capacity <= 0 || m.Capacity >= 10 {
			t.Errorf("active sprint: %+v", sp.Mine)
		}
		return
	}
	t.Error("no active sprint")
}
