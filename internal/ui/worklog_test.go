package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/store"
)

// worklogJira records worklog posts on the model's client.
func worklogJira(t *testing.T, m *Model, bodies *[]map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.Method == http.MethodGet {
			return // the reload after a delete
		}
		b["path"] = r.URL.Path
		*bodies = append(*bodies, b)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
}

// TestLogWork: w in the panel, "1h 30m tests" logs 90 minutes ending now.
func TestLogWork(t *testing.T) {
	var bodies []map[string]any
	m := loadedJiraModel(t)
	worklogJira(t, &m, &bodies)
	out, _ := m.handleRefKey(keyMsg(t, "w"))
	m = out.(Model)
	if !m.jiraFieldActive || m.jiraFieldName != "worklog" || !strings.Contains(m.View().Content, "Log work — ABC-1") {
		t.Fatal("w should open the worklog input")
	}
	m.jiraFieldInput.SetValue("tests")
	out, cmd := m.applyJiraField()
	if cmd != nil || !out.(Model).jiraFieldActive || !strings.Contains(out.(Model).status, "start with a time") {
		t.Fatal("no time should be refused")
	}
	m.jiraFieldInput.SetValue("1h 30m tests")
	_, cmd = m.applyJiraField()
	if msg := cmd().(worklogLoggedMsg); msg.err != nil || msg.fromTimer {
		t.Fatal(msg.err)
	}
	b := bodies[0]
	started, _ := time.Parse("2006-01-02T15:04:05.000-0700", b["started"].(string))
	if b["path"] != "/rest/api/3/issue/ABC-1/worklog" || b["timeSpentSeconds"] != 5400.0 || b["comment"] == nil ||
		time.Since(started) < 89*time.Minute || time.Since(started) > 91*time.Minute {
		t.Errorf("body = %v", b)
	}
}

// TestTimer: T starts a timer the header shows and the store keeps; T again
// stops it into the log input, started when the timer did.
func TestTimer(t *testing.T) {
	var bodies []map[string]any
	m := jiraTabModel(t)
	worklogJira(t, &m, &bodies)
	out, cmd := m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	if m.timer.key != "ABC-1" {
		t.Fatalf("timer = %+v", m.timer)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "⏱ ABC-1 0m") {
		t.Error("header lacks the timer")
	}
	// A restart picks it up.
	m.timer = workTimer{}
	m.loadTimer()
	if m.timer.key != "ABC-1" {
		t.Fatal("timer not restored from the store")
	}
	m.timer.start = time.Now().Add(-25 * time.Minute)
	start := m.timer.start
	out, _ = m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	if !m.jiraFieldActive || m.jiraFieldInput.Value() != "25m " || m.jiraFieldKey != "ABC-1" {
		t.Fatalf("stop: input %q", m.jiraFieldInput.Value())
	}
	// Until the log is in, the timer runs on: esc must not lose its time.
	if m.timer.key != "ABC-1" {
		t.Fatal("the timer should run until the log is written")
	}
	out, cmd = m.applyJiraField()
	m = out.(Model)
	msg := cmd().(worklogLoggedMsg)
	if got := bodies[0]["started"]; got != start.Format("2006-01-02T15:04:05.000-0700") {
		t.Errorf("started = %v, want the timer's start", got)
	}
	out, _ = m.handleWorklogLogged(msg)
	m = out.(Model)
	if v, _, _ := m.store.GetMeta(timerMeta); m.timer.key != "" || v != "" {
		t.Errorf("after the log: timer %+v, stored %q", m.timer, v)
	}
}

// TestTimerShared: a timer laneway web starts or stops in the state file
// shows here on the next tick.
func TestTimerShared(t *testing.T) {
	m := jiraTabModel(t)
	if m.timerStart() == nil {
		t.Fatal("no first tick with a store")
	}
	start := time.Now().Add(-10 * time.Minute)
	_ = m.store.SetMeta(timerMeta, "ABC-2 "+strconv.FormatInt(start.Unix(), 10))
	out, cmd := m.handleTimerTick()
	m = out.(Model)
	if m.timer.key != "ABC-2" || cmd == nil {
		t.Fatalf("after web started it: timer %+v, tick %v", m.timer, cmd != nil)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "⏱ ABC-2 10m") {
		t.Error("header lacks the web's timer")
	}
	_ = m.store.SetMeta(timerMeta, "")
	out, cmd = m.handleTimerTick()
	if m = out.(Model); m.timer.key != "" || cmd == nil {
		t.Fatalf("after web stopped it: timer %+v, tick %v", m.timer, cmd != nil)
	}
}

// TestTimerOnDay: stopping the timer into "yesterday 2h" logs it yesterday,
// from the workday start.
func TestTimerOnDay(t *testing.T) {
	var bodies []map[string]any
	m := jiraTabModel(t)
	worklogJira(t, &m, &bodies)
	out, _ := m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	out, _ = m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	m.jiraFieldInput.SetValue("yesterday 2h")
	_, cmd := m.applyJiraField()
	if msg := cmd().(worklogLoggedMsg); msg.err != nil || !msg.fromTimer {
		t.Fatalf("logged = %+v", msg)
	}
	now := time.Now()
	want := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.Local).Add(m.opts.workdayStart)
	if got := bodies[0]["started"]; got != want.Format("2006-01-02T15:04:05.000-0700") || bodies[0]["timeSpentSeconds"] != 7200.0 {
		t.Errorf("body = %v, want started %v", bodies[0], want)
	}
}

// TestTimerKeptOnFailure: a failed log leaves the timer running.
func TestTimerKeptOnFailure(t *testing.T) {
	m := jiraTabModel(t)
	m.timer = workTimer{key: "ABC-1", start: time.Now().Add(-time.Hour)}
	out, _ := m.handleWorklogLogged(worklogLoggedMsg{key: "ABC-1", fromTimer: true, err: errors.New("503")})
	if m = out.(Model); m.timer.key != "ABC-1" || !strings.Contains(m.status, "keeps running") {
		t.Errorf("timer %+v, status %q", m.timer, m.status)
	}
}

func TestTimesheet(t *testing.T) {
	m := jiraTabModel(t)
	out, cmd := m.handleJiraKey(keyMsg(t, "W"))
	m = out.(Model)
	if !m.jiraPicker.active || m.jiraPicker.kind != jiraPickTimesheet || cmd == nil {
		t.Fatal("W should open the timesheet")
	}
	out, _ = m.handleJiraPickerLoaded(jiraPickerLoadedMsg{gen: m.jiraPicker.gen, seq: m.jiraPicker.fetchSeq, kind: jiraPickTimesheet,
		title: "Today — 1h 30m", items: []jiraPickerItem{{id: "ABC-2", label: "09:00  1h 30m  ABC-2 Second"}}})
	m = out.(Model)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Today — 1h 30m") || !strings.Contains(view, "ABC-2 Second") {
		t.Error("timesheet not drawn")
	}
	out, _ = m.applyJiraPick()
	if m = out.(Model); !m.refOpen || m.refs[m.refIdx].jiraKey != "ABC-2" {
		t.Error("enter should open the issue")
	}
}

// TestTimesheetDays: [ ] step the day; d twice deletes the entry.
func TestTimesheetDays(t *testing.T) {
	var bodies []map[string]any
	m := jiraTabModel(t)
	worklogJira(t, &m, &bodies)
	out, _ := m.handleJiraKey(keyMsg(t, "W"))
	m = out.(Model)
	today := m.jiraPicker.day
	out, _ = m.handleJiraPickerKey(keyMsg(t, "]"))
	if m = out.(Model); m.status != "today is the last day to show" || !m.jiraPicker.day.Equal(today) {
		t.Fatalf("] on today: %q, day %v", m.status, m.jiraPicker.day)
	}
	out, _ = m.handleJiraPickerKey(keyMsg(t, "["))
	m = out.(Model)
	if !m.jiraPicker.active || m.jiraPicker.day.Format(time.DateOnly) != today.AddDate(0, 0, -1).Format(time.DateOnly) || m.jiraPicker.title != "Yesterday" {
		t.Fatalf("day %v, title %q", m.jiraPicker.day, m.jiraPicker.title)
	}
	m.setJiraPickerItems([]jiraPickerItem{{id: "ABC-2/10101", label: "09:00  1h  ABC-2"}})
	out, cmd := m.handleJiraPickerKey(keyMsg(t, "d"))
	m = out.(Model)
	if cmd != nil || !strings.Contains(m.status, "d again") {
		t.Fatalf("first d: status %q", m.status)
	}
	_, cmd = m.handleJiraPickerKey(keyMsg(t, "d"))
	if cmd == nil {
		t.Fatal("second d should delete")
	}
	cmd()
	if len(bodies) == 0 || bodies[0]["path"] != "/rest/api/3/issue/ABC-2/worklog/10101" {
		t.Errorf("requests = %v", bodies)
	}
}

// TestTimesheetMarks: x marks entries (a proposal isn't one), d twice
// deletes the marked ones, a request per issue.
func TestTimesheetMarks(t *testing.T) {
	var bodies []map[string]any
	m := jiraTabModel(t)
	worklogJira(t, &m, &bodies)
	out, _ := m.handleJiraKey(keyMsg(t, "W"))
	m = out.(Model)
	m.setJiraPickerItems([]jiraPickerItem{{id: "ABC-2/101", label: "09:00  1h  ABC-2"}, {id: "ABC-3/102", label: "10:00  1h  ABC-3"},
		{id: "ABC-2/103", label: "11:00  1h  ABC-2"}, {id: proposalID + "ABC-4/1700000000", label: "≈ 12:00  1h  ABC-4"}})
	for _, k := range []string{"x", "x", "x", "x"} {
		out, _ = m.handleJiraPickerKey(keyMsg(t, k))
		m = out.(Model)
	}
	if got := m.jiraPicker.timesheetTargets(); !slices.Equal(got, []string{"ABC-2/101", "ABC-3/102", "ABC-2/103"}) {
		t.Fatalf("marked %v, status %q", got, m.status)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "✓ 09:00") || strings.Contains(view, "✓ ≈") {
		t.Errorf("marks not drawn:\n%s", view)
	}
	out, cmd := m.handleJiraPickerKey(keyMsg(t, "d"))
	if m = out.(Model); cmd != nil || !strings.Contains(m.status, "deletes 3 worklogs") {
		t.Fatalf("first d: status %q", m.status)
	}
	_, cmd = m.handleJiraPickerKey(keyMsg(t, "d"))
	if cmd == nil {
		t.Fatal("second d should delete")
	}
	cmd()
	if len(bodies) < 2 || bodies[0]["path"] != "/rest/api/3/issue/ABC-2/worklog" || fmt.Sprint(bodies[0]["ids"]) != "[101 103]" ||
		bodies[1]["path"] != "/rest/api/3/issue/ABC-3/worklog/102" || len(bodies) > 2 && bodies[2]["path"] != "/rest/api/3/search/jql" {
		t.Errorf("requests = %v", bodies)
	}
}

// TestTimesheetMove: X marks every entry, m asks the issue and moves them,
// a request per issue they're on; one there already stays.
func TestTimesheetMove(t *testing.T) {
	var bodies []map[string]any
	m := jiraTabModel(t)
	worklogJira(t, &m, &bodies)
	out, _ := m.handleJiraKey(keyMsg(t, "W"))
	m = out.(Model)
	m.setJiraPickerItems([]jiraPickerItem{{id: "ABC-2/101", label: "09:00  1h  ABC-2"}, {id: "ABC-3/102", label: "10:00  1h  ABC-3"},
		{id: "ABC-9/103", label: "11:00  1h  ABC-9"}})
	out, _ = m.handleJiraPickerKey(keyMsg(t, "X"))
	out, _ = out.(Model).handleJiraPickerKey(keyMsg(t, "m"))
	if m = out.(Model); !m.jiraFieldActive || m.jiraFieldName != "worklog-move" || !strings.Contains(m.View().Content, "Move 3 worklogs to") {
		t.Fatalf("m: field %q, status %q", m.jiraFieldName, m.status)
	}
	m.jiraFieldInput.SetValue("9")
	out, cmd := m.applyJiraField()
	if m = out.(Model); cmd == nil || !strings.Contains(m.status, "moving 3 worklogs to ABC-9") {
		t.Fatalf("enter: status %q", m.status)
	}
	cmd()
	if len(bodies) < 2 || bodies[0]["path"] != "/rest/api/3/issue/ABC-2/worklog/move" || bodies[0]["issueIdOrKey"] != "ABC-9" ||
		fmt.Sprint(bodies[0]["ids"]) != "[101]" || bodies[1]["path"] != "/rest/api/3/issue/ABC-3/worklog/move" || len(bodies) > 2 && bodies[2]["path"] != "/rest/api/3/search/jql" {
		t.Errorf("requests = %v", bodies)
	}
}

// TestTimesheetMoveUnsupported: a site without the move says so, and m
// isn't offered again.
func TestTimesheetMoveUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	out, _ := m.handleJiraKey(keyMsg(t, "W"))
	m = out.(Model)
	m.setJiraPickerItems([]jiraPickerItem{{id: "ABC-2/101", label: "09:00  1h  ABC-2"}})
	out, _ = m.handleJiraPickerKey(keyMsg(t, "m"))
	m = out.(Model)
	m.jiraFieldInput.SetValue("ABC-9")
	out, cmd := m.applyJiraField()
	out, _ = out.(Model).Update(cmd())
	if m = out.(Model); !strings.Contains(m.status, "can't move worklogs") || !m.jiraPicker.active {
		t.Fatalf("status %q, timesheet back %v", m.status, m.jiraPicker.active)
	}
	m.setJiraPickerItems([]jiraPickerItem{{id: "ABC-2/101", label: "09:00  1h  ABC-2"}})
	out, _ = m.handleJiraPickerKey(keyMsg(t, "m"))
	if m = out.(Model); m.jiraFieldActive || m.status != "this Jira can't move worklogs" {
		t.Errorf("m again: field %v, status %q", m.jiraFieldActive, m.status)
	}
}

// TestTimerOnStart: with ui.timer_on_start, S's success starts the timer.
func TestTimerOnStart(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraWork(jiraWorkMsg{key: "ABC-1", path: "/w"})
	if m = out.(Model); m.timer.key != "" {
		t.Fatal("off by default")
	}
	m.opts.timerOnStart = true
	out, _ = m.handleJiraWork(jiraWorkMsg{key: "ABC-1", path: "/w"})
	if m = out.(Model); m.timer.key != "ABC-1" || !strings.Contains(m.status, "timer started") {
		t.Errorf("timer %+v, status %q", m.timer, m.status)
	}
}

// TestWorkActionsRow: the start work form ticks the configured actions
// only when one is set; unticked, S's success leaves the timer alone.
func TestWorkActionsRow(t *testing.T) {
	m := jiraTabModel(t)
	iss := &jira.Issue{Key: "ABC-1", Summary: "Fix"}
	m.openWorkForm(iss)
	for _, ff := range m.jiraForm.fields {
		if ff.ID == workActionsField {
			t.Fatal("actions row without actions")
		}
	}
	m.opts.timerOnStart, m.opts.startStatus = true, "In Progress"
	m.openWorkForm(iss)
	f := m.jiraForm
	ff := &f.fields[len(f.fields)-1]
	if ff.ID != workActionsField || ff.val.Text != "move to In Progress · start timer" {
		t.Fatalf("row %+v", ff)
	}
	if !strings.Contains(m.renderJiraForm(), "[x] move to In Progress") {
		t.Error("ticked row not drawn")
	}
	f.idx = len(f.fields) - 1
	m.editJiraFormField()
	if !ff.val.Empty() || !strings.Contains(m.renderJiraForm(), "[ ] ") {
		t.Fatal("enter should untick")
	}
	m.jiraStartStatus = map[string]string{"ABC": "Doing", "XYZ": ""}
	if got := m.startActions("ABC-1"); !slices.Equal(got, []string{"move to Doing", "start timer"}) {
		t.Errorf("project override: %v", got)
	}
	if got := m.startActions("XYZ-1"); !slices.Equal(got, []string{"start timer"}) {
		t.Errorf("project without a move: %v", got)
	}
	if got := m.startStatus("QQ-1"); got != "In Progress" {
		t.Errorf("fallback = %q", got)
	}
	out, cmd := m.handleJiraWork(jiraWorkMsg{key: "ABC-1", path: "/w", skipActions: true})
	if m = out.(Model); m.timer.key != "" || cmd != nil {
		t.Errorf("unticked still acted: timer %+v", m.timer)
	}
}

// TestTimesheetEdit: e opens the entry's time and comment; enter updates it.
func TestTimesheetEdit(t *testing.T) {
	var bodies []map[string]any
	m := jiraTabModel(t)
	worklogJira(t, &m, &bodies)
	out, _ := m.handleJiraKey(keyMsg(t, "W"))
	m = out.(Model)
	m.setJiraPickerItems([]jiraPickerItem{{id: "ABC-2/10101", label: "09:00  1h  ABC-2", value: "1h review"}})
	out, _ = m.handleJiraPickerKey(keyMsg(t, "e"))
	m = out.(Model)
	if !m.jiraFieldActive || m.jiraFieldInput.Value() != "1h review" || m.worklogEdit != "10101" {
		t.Fatalf("input %q edit %q", m.jiraFieldInput.Value(), m.worklogEdit)
	}
	m.jiraFieldInput.SetValue("1h 30m review")
	_, cmd := m.applyJiraField()
	cmd()
	if len(bodies) == 0 || bodies[0]["path"] != "/rest/api/3/issue/ABC-2/worklog/10101" || bodies[0]["timeSpentSeconds"] != 5400.0 {
		t.Errorf("requests = %v", bodies)
	}
	if _, ok := bodies[0]["comment"]; ok {
		t.Errorf("an untouched comment was sent: %v", bodies[0])
	}
	m.openWorklogInput("ABC-1", "", time.Time{})
	if m.worklogEdit != "" {
		t.Error("a new log must not update the edited entry")
	}
}

// TestTimesheetEditFails: a refused edit says why and brings back the
// input with what was typed, still editing the entry.
func TestTimesheetEditFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errorMessages":["no"]}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	out, _ := m.handleJiraKey(keyMsg(t, "W"))
	m = out.(Model)
	m.setJiraPickerItems([]jiraPickerItem{{id: "ABC-2/10101", label: "09:00  1h  ABC-2", value: "1h review"}})
	out, _ = m.handleJiraPickerKey(keyMsg(t, "e"))
	m = out.(Model)
	m.jiraFieldInput.SetValue("2h review, **done**")
	out, cmd := m.applyJiraField()
	m = out.(Model)
	msg, ok := cmd().(worklogFailedMsg)
	if !ok {
		t.Fatalf("msg %T", msg)
	}
	out, _ = m.Update(msg)
	m = out.(Model)
	if m.jiraPicker.active || !m.jiraFieldActive || m.jiraFieldInput.Value() != "2h review, **done**" || m.worklogEdit != "10101" || m.worklogComment != "review" {
		t.Errorf("picker %v, input %v %q, edit %q, comment %q", m.jiraPicker.active, m.jiraFieldActive, m.jiraFieldInput.Value(), m.worklogEdit, m.worklogComment)
	}
}

// TestTimesheetCopy: y copies the day's worklogs and total as a markdown
// table.
func TestTimesheetCopy(t *testing.T) {
	now := time.Now()
	started := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 1, 0, time.Local).Format("2006-01-02T15:04:05.000-0700")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/myself"):
			io.WriteString(w, `{"accountId":"me"}`)
		case strings.HasSuffix(r.URL.Path, "/search/jql"):
			io.WriteString(w, `{"issues":[{"key":"ABC-2","fields":{"summary":"Second | part"}}]}`)
		case strings.HasSuffix(r.URL.Path, "/worklog"):
			io.WriteString(w, `{"worklogs":[{"id":"1","author":{"accountId":"me"},"started":"`+started+`","timeSpentSeconds":5400}]}`)
		}
	}))
	defer srv.Close()
	m := jiraTabModel(t)
	m.jiraClient = jira.New(jira.Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	out, cmd := m.handleJiraKey(keyMsg(t, "W"))
	m = out.(Model)
	out, _ = m.handleJiraPickerLoaded(cmd().(jiraPickerLoadedMsg))
	m = out.(Model)
	_, cmd = m.handleJiraPickerKey(keyMsg(t, "y"))
	if cmd == nil {
		t.Fatal("y copied nothing")
	}
	got := fmt.Sprint(cmd())
	if !strings.Contains(got, "| 1h 30m | ABC-2 | Second \\| part |") || !strings.Contains(got, "| 1h 30m | total |") {
		t.Errorf("table = %q", got)
	}
}

func TestTimerRound(t *testing.T) {
	for _, tc := range []struct {
		elapsed, step time.Duration
		want          int
	}{
		{20 * time.Second, 0, 60},
		{7*time.Minute + 40*time.Second, 0, 8 * 60},
		{7 * time.Minute, 15 * time.Minute, 15 * 60},
		{15 * time.Minute, 15 * time.Minute, 15 * 60},
		{16 * time.Minute, 15 * time.Minute, 30 * 60},
		{0, 15 * time.Minute, 15 * 60},
	} {
		if got := timerSeconds(tc.elapsed, tc.step); got != tc.want {
			t.Errorf("%v by %v = %ds, want %ds", tc.elapsed, tc.step, got, tc.want)
		}
	}
	o, warn := optionsFrom(config.UIConfig{TimerRound: "15m"})
	if o.timerRound != 15*time.Minute || len(warn) != 0 {
		t.Errorf("timer_round = %v %v", o.timerRound, warn)
	}
	if _, warn = optionsFrom(config.UIConfig{TimerRound: "10s"}); len(warn) != 1 {
		t.Errorf("10s accepted: %v", warn)
	}
}

// TestTimerSaveFails: a timer the state file can't keep says a restart
// loses it.
func TestTimerSaveFails(t *testing.T) {
	m := jiraTabModel(t)
	dir := filepath.Join(t.TempDir(), "state")
	st, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o600); err != nil { // its directory is a file now
		t.Fatal(err)
	}
	m.store = st
	m.toggleTimer("ABC-1")
	if m.timer.key != "ABC-1" || !strings.Contains(m.statusErr, "a restart loses it") {
		t.Errorf("timer %+v, status %q", m.timer, m.statusErr)
	}
}

// TestWorklogLeft: a left: word anywhere sets what's left of the estimate.
func TestWorklogLeft(t *testing.T) {
	for in, want := range map[string][2]string{
		"1h fix":           {"", "1h fix"},
		"1h left:2h fix":   {"2h", "1h fix"},
		"1h fix left:KEEP": {"keep", "1h fix"},
	} {
		left, rest, err := worklogLeft(in)
		if err != nil || left != want[0] || rest != want[1] {
			t.Errorf("%q = %q %q %v", in, left, rest, err)
		}
	}
	if _, _, err := worklogLeft("1h left:soon"); err == nil {
		t.Error("left:soon should fail")
	}
}

// TestEstimate: A → set the original estimate writes timetracking.
func TestEstimate(t *testing.T) {
	m, writes := actionsModel(t, nil)
	m, _ = pickAction(t, m, "estimate")
	m.jiraFieldInput.SetValue("2d 4h")
	_, cmd := m.applyJiraField()
	cmd()
	if w := writes(); len(w) != 1 || w[0] != `PUT /rest/api/3/issue/ABC-1 {"fields":{"timetracking":{"originalEstimate":"2d 4h"}}}` {
		t.Errorf("writes = %q", w)
	}
}

// TestTimerMarksTheCard: the timed card says so on the board, in the list
// and in the panel.
func TestTimerMarksTheCard(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	if !strings.Contains(ansi.Strip(m.jiraTab.lanesOut), " ⏱ 0m") {
		t.Errorf("lanes lack the mark:\n%s", ansi.Strip(m.jiraTab.lanesOut))
	}
	if strings.Count(ansi.Strip(m.View().Content), "⏱ 0m") != 1 {
		t.Error("the mark is on more than the timed card")
	}
	out, _ = m.handleJiraKey(keyMsg(t, "t"))
	m = out.(Model)
	if row := ansi.Strip(m.jiraListRow(m.jiraTab.cards[0], false, listCols{width: 120, key: 6, status: 8})); !strings.Contains(strings.Join(strings.Fields(row), " "), "⏱ 0m First") {
		t.Errorf("list row = %q", row)
	}
	m.jiraIssue = &jira.Issue{Key: "ABC-1", Type: "Story", Summary: "First"}
	if head := ansi.Strip(m.renderJiraIssue(m.jiraIssue, 80)); !strings.Contains(strings.SplitN(head, "\n", 2)[0], "⏱ 0m") {
		t.Errorf("panel title = %q", strings.SplitN(head, "\n", 2)[0])
	}
}

// TestTimerDrop: ctrl+d in the stop prompt drops the timer unlogged, asking
// twice once it ran a while.
func TestTimerDrop(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	m.timer.start = time.Now().Add(-20 * time.Minute)
	out, _ = m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	out, _ = m.handleKey(keyMsg(t, "ctrl+d"))
	m = out.(Model)
	if m.timer.key != "ABC-1" || !m.jiraFieldActive || !strings.Contains(m.status, "again") {
		t.Fatalf("first ctrl+d: timer %q, prompt %v, status %q", m.timer.key, m.jiraFieldActive, m.status)
	}
	out, _ = m.handleKey(keyMsg(t, "ctrl+d"))
	m = out.(Model)
	if m.timer.key != "" || m.jiraFieldActive || !strings.Contains(m.status, "20m on ABC-1 not logged") {
		t.Errorf("second ctrl+d: timer %q, prompt %v, status %q", m.timer.key, m.jiraFieldActive, m.status)
	}
}

// TestTimerSwitch: T on another card logs the timed one, then times the
// new one; ctrl+t moves the timer there instead.
func TestTimerSwitch(t *testing.T) {
	m := jiraTabModel(t)
	out, _ := m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	m.selectJiraKey("ABC-3")
	out, _ = m.handleJiraKey(keyMsg(t, "T"))
	m = out.(Model)
	if m.jiraFieldKey != "ABC-1" || m.timerNext != "ABC-3" || !strings.Contains(m.View().Content, "then time ABC-3") {
		t.Fatalf("T on ABC-3: logging %q, next %q", m.jiraFieldKey, m.timerNext)
	}
	out, _ = m.Update(worklogLoggedMsg{key: "ABC-1", fromTimer: true, next: "ABC-3"})
	if m2 := out.(Model); m2.timer.key != "ABC-3" || !strings.Contains(m2.status, "timer started on ABC-3") {
		t.Errorf("logged: timer %q, status %q", m2.timer.key, m2.status)
	}
	start := m.timer.start
	out, _ = m.handleKey(keyMsg(t, "ctrl+t"))
	if m = out.(Model); m.timer.key != "ABC-3" || !m.timer.start.Equal(start) || m.jiraFieldActive {
		t.Errorf("ctrl+t: timer %+v, prompt %v", m.timer, m.jiraFieldActive)
	}
}
