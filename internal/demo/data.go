package demo

import (
	"fmt"
	"time"
)

// The generated project: DEMO on one scrum board, an active sprint with
// work in every lane, the next sprint, a backlog, three epics, and four
// closed sprints behind the velocity chart. Dates are relative to now.

type user struct{ id, name string }

var (
	me    = user{"demo-0001", "Jamie Rivers"}
	mira  = user{"demo-0002", "Mira Jansen"}
	tomas = user{"demo-0003", "Tomás Ruiz"}
	priya = user{"demo-0004", "Priya Nair"}
	sam   = user{"demo-0005", "Sam Okafor"}
	users = []user{me, mira, tomas, priya, sam}
)

type status struct{ id, name, cat string }

var (
	todo     = status{"1", "To Do", "new"}
	doing    = status{"2", "In Progress", "indeterminate"}
	review   = status{"3", "In Review", "indeterminate"}
	qa       = status{"5", "Testing", "indeterminate"}
	done     = status{"4", "Done", "done"}
	statuses = []status{todo, doing, review, qa, done}
)

var types = map[string]string{"Epic": "10000", "Story": "10001", "Task": "10002", "Sub-task": "10003", "Bug": "10004"}

var priorities = []string{"Highest", "High", "Medium", "Low", "Lowest"}

const (
	pointsField = "customfield_10016"
	sprintField = "customfield_10020"
	flagField   = "customfield_10021"
	startField  = "customfield_10015"
	teamField   = "customfield_10030"
	testField   = "customfield_10040" // a rich-text field (textarea), as test notes often are
	legacyField = "customfield_10041" // a one-line field no one fills any more
)

// components and teams are what the edit screen (editmeta) offers for the
// fields of those names.
var (
	components = []string{"Checkout", "Payments", "Search"}
	teams      = []string{"Web", "Platform"}
)

const (
	project  = "DEMO"
	boardID  = 1
	activeID = 12
	nextID   = 13
)

type sprint struct {
	id          int
	name, state string
	start, end  time.Time
	goal        string
}

// version is one of the project's releases.
type version struct {
	id, name string
	released bool
	date     string // its release date, "" for none
}

// demoGroups are the demo user's groups, by ID.
var demoGroups = map[string]string{"g1": "developers", "g2": "jira-software-users"}

type comment struct {
	id      string
	parent  string // the comment it replies to
	author  user
	body    string
	doc     any // the body as last written, nil: body's paragraphs
	created time.Time
	vis     map[string]any // the visibility it was posted with, nil for everyone
	public  *bool          // posted on the request (servicedesk.go): for the customer, else an internal note
}

type worklog struct {
	id      string
	author  user
	started time.Time
	seconds int
	comment string
}

type change struct {
	author   user
	at       time.Time
	field    string
	from, to string
	fromID   string
	toID     string
}

type issue struct {
	key, typ, summary string
	status            status
	priority          string
	assignee          *user
	reporter          user
	points            float64
	parent            string
	sprint            int
	due               string
	flagged           bool
	labels            []string
	description       string
	descDoc           any // the description as last written, nil: description's paragraphs
	created, updated  time.Time
	resolved          time.Time
	start             string // an epic's start date
	fixVersion        string // a version's id, "" for none
	components        []string
	team              string
	testNotes         string // testField, markdown-ish text
	testDoc           any    // testNotes as last written, as descDoc
	legacy            string // legacyField
	comments          []comment
	worklogs          []worklog
	attachments       []attachment
	changes           []change
}

// attachment is a file uploaded to an issue.
type attachment struct {
	id, name, mime string
	data           []byte
	created        time.Time
}

// seed is one issue of the generated project.
type seed struct {
	key, typ, summary string
	st                status
	prio              int
	who               *user
	pts               float64
	parent            string
	sprint            int
	due               int // days from now; 0 for none
	flagged           bool
	labels            []string
}

func generate(now time.Time) *Server {
	day := func(d int) time.Time { return now.AddDate(0, 0, d) }
	date := func(d int) string { return day(d).Format(time.DateOnly) }
	s := &Server{now: now, issues: map[string]*issue{}, sprints: []sprint{
		{id: activeID, name: "Sprint 12 - Checkout", state: "active", start: day(-5), end: day(9),
			goal: "Guest checkout behind a flag, and the order page under 400ms."},
		{id: nextID, name: "Sprint 13", state: "future"},
	}}
	for i, n := range []struct{ committed, done int }{{31, 26}, {38, 38}, {35, 29}, {40, 34}} {
		end := day(-6 - 14*(3-i))
		s.sprints = append(s.sprints, sprint{id: 8 + i, name: fmt.Sprintf("Sprint %d", 8+i), state: "closed", start: end.AddDate(0, 0, -13), end: end})
		s.closedWork(8+i, n.committed, n.done, end)
	}
	seeds := []seed{
		{"DEMO-1", "Epic", "Guest checkout", doing, 2, &mira, 0, "", 0, 20, false, nil},
		{"DEMO-2", "Epic", "Order page performance", doing, 3, &priya, 0, "", 0, 12, false, nil},
		{"DEMO-3", "Epic", "Invoices", todo, 3, nil, 0, "", 0, 45, false, nil},
		{"DEMO-4", "Story", "Checkout without an account", doing, 2, &me, 5, "DEMO-1", activeID, 4, false, []string{"frontend"}},
		{"DEMO-5", "Story", "Remember the guest's address for the next order", todo, 3, &tomas, 3, "DEMO-1", activeID, 8, false, []string{"frontend"}},
		{"DEMO-6", "Bug", "Order total rounds a cent off on split payments", doing, 1, &sam, 3, "DEMO-1", activeID, 1, true, []string{"backend"}},
		{"DEMO-7", "Task", "Cache the shipping-rate lookup per region", review, 3, &priya, 2, "DEMO-2", activeID, 3, false, []string{"backend"}},
		{"DEMO-8", "Story", "Lazy-load product images on the order page", review, 3, &mira, 3, "DEMO-2", activeID, 0, false, []string{"frontend"}},
		{"DEMO-9", "Bug", "Double click on Pay places two orders", todo, 1, &me, 2, "DEMO-1", activeID, 0, true, []string{"backend"}},
		{"DEMO-10", "Task", "p95 latency panel for the order page", todo, 4, &priya, 1, "DEMO-2", activeID, 12, false, nil},
		{"DEMO-11", "Story", "Order confirmation e-mail for guests", done, 3, &tomas, 3, "DEMO-1", activeID, 0, false, nil},
		{"DEMO-12", "Task", "Drop the legacy /v1/cart endpoint", done, 4, &sam, 2, "", activeID, 0, false, []string{"backend"}},
		{"DEMO-13", "Bug", "Address form loses the house number", review, 2, &me, 1, "DEMO-1", activeID, 0, false, []string{"frontend"}},
		{"DEMO-14", "Story", "Invoice PDF per order", todo, 3, nil, 5, "DEMO-3", nextID, 0, false, nil},
		{"DEMO-15", "Task", "VAT line on invoices for the EU", todo, 3, nil, 3, "DEMO-3", nextID, 0, false, nil},
		{"DEMO-16", "Story", "Resend an invoice from the order page", todo, 4, &tomas, 2, "DEMO-3", nextID, 0, false, nil},
		{"DEMO-17", "Bug", "Search results jump when images load", todo, 3, nil, 0, "", 0, 0, false, []string{"frontend"}},
		{"DEMO-18", "Task", "Upgrade the payment SDK", todo, 4, nil, 2, "", 0, 0, false, nil},
		{"DEMO-19", "Story", "Saved carts across devices", todo, 5, nil, 8, "", 0, 0, false, nil},
		{"DEMO-20", "Task", "Retry failed webhooks with a backoff", todo, 3, nil, 3, "DEMO-2", 0, 0, false, []string{"backend"}},
		{"DEMO-21", "Sub-task", "Guest session token", done, 3, &me, 0, "DEMO-4", 0, 0, false, nil},
		{"DEMO-22", "Sub-task", "Skip the account step in the flow", doing, 3, &me, 0, "DEMO-4", 0, 0, false, nil},
		{"DEMO-23", "Sub-task", "Guest checkout behind the feature flag", todo, 3, nil, 0, "DEMO-4", 0, 0, false, nil},
	}
	for i, sd := range seeds {
		iss := &issue{key: sd.key, typ: sd.typ, summary: sd.summary, status: sd.st, priority: priorities[sd.prio-1],
			assignee: sd.who, reporter: mira, points: sd.pts, parent: sd.parent, sprint: sd.sprint,
			flagged: sd.flagged, labels: sd.labels, created: day(-20 + i/2), updated: day(-1).Add(time.Duration(i) * time.Hour),
			description: "What: " + sd.summary + ".\n\nWhy: part of " + orNone(sd.parent) + "."}
		if sd.due != 0 {
			iss.due = date(sd.due)
		}
		if sd.st == done {
			iss.resolved = day(-4 + i%3)
		}
		if sd.typ == "Epic" {
			iss.start = date(-20 + 12*i)
		}
		if sd.st != todo && sd.typ == "Story" {
			iss.testNotes = "Check: " + sd.summary + ".\n\nOn staging, as a guest and as a customer."
		}
		// A status history that walks the workflow up to where it is.
		at := day(-6)
		for j := 1; j < len(statuses) && statuses[j-1] != sd.st; j++ {
			from, to := statuses[j-1], statuses[j]
			who := me
			if sd.who != nil {
				who = *sd.who
			}
			iss.changes = append(iss.changes, change{author: who, at: at, field: "status", from: from.name, to: to.name, fromID: from.id, toID: to.id})
			at = at.AddDate(0, 0, 2)
		}
		s.issues[sd.key] = iss
		s.order = append(s.order, sd.key)
	}
	// A release behind, the one this sprint ships and the next.
	s.versions = []version{{"10100", "2.3", true, date(-6)}, {"10101", "2.4", false, date(9)}, {"10102", "2.5", false, ""}}
	for v, keys := range map[string][]string{
		"10100": {"DEMO-11", "DEMO-12"},
		"10101": {"DEMO-4", "DEMO-5", "DEMO-6", "DEMO-9", "DEMO-13"},
		"10102": {"DEMO-14", "DEMO-15", "DEMO-16"},
	} {
		for _, k := range keys {
			s.issues[k].fixVersion = v
		}
	}
	s.comment("DEMO-4", mira, "Can we keep the account upsell on the confirmation page?", day(-2))
	s.comment("DEMO-4", me, "Yes, after the order: it won't block paying.", day(-1))
	s.comment("DEMO-6", sam, "Reproduced with a 3-way split of 10.00: one part gets 3.34.", day(-1))
	s.comment("DEMO-7", priya, "Hit rate is 92% on staging.", now.Add(-3*time.Hour))
	// Others on your issues this week, for the inbox, the standup and the
	// history: a raised priority, a review note, a mention, a reply.
	s.change("DEMO-9", mira, now.Add(-5*time.Hour), "priority", "High", "Highest")
	s.comment("DEMO-9", sam, "@Jamie Rivers this hit two customers this morning; the logs are on the incident.", now.Add(-50*time.Minute))
	s.comment("DEMO-13", priya, "Reviewed: one nit on the postcode pattern, otherwise good to go.", now.Add(-2*time.Hour))
	s.change("DEMO-13", priya, now.Add(-2*time.Hour), "labels", "frontend", "frontend reviewed")
	s.comment("DEMO-22", tomas, "Copy for the button: \"Continue as guest\".", day(-1).Add(15*time.Hour))
	s.comment("DEMO-4", mira, "Ship it behind the flag; I'll tell support.", now.Add(-time.Hour))
	// Your own yesterday, for the standup.
	s.comment("DEMO-13", me, "Fixed: the postcode lookup no longer overwrites the house number.", day(-1).Add(16*time.Hour))
	s.worklog("DEMO-13", me, day(-1).Truncate(24*time.Hour).Add(14*time.Hour), 5400, "house number fix")
	s.worklog("DEMO-22", me, day(-1).Truncate(24*time.Hour).Add(10*time.Hour), 2*3600, "skip step")
	for _, w := range []struct {
		key  string
		who  user
		secs int
		text string
	}{{"DEMO-6", sam, 2 * 3600, "rounding per part"}, {"DEMO-7", priya, 5400, "cache keys per region"}, {"DEMO-8", mira, 3 * 3600, "lazy images"}, {"DEMO-4", tomas, 3600, "copy review"}} {
		s.worklog(w.key, w.who, day(-1).Truncate(24*time.Hour).Add(13*time.Hour), w.secs, w.text)
	}
	for d, w := range []struct {
		key  string
		secs int
		text string
	}{{"DEMO-4", 3 * 3600, "guest session"}, {"DEMO-13", 5400, "reproduce"}, {"DEMO-4", 4 * 3600, "skip the account step"}, {"DEMO-9", 3600, "look into it"}} {
		s.worklog(w.key, me, day(-d-1).Truncate(24*time.Hour).Add(9*time.Hour), w.secs, w.text)
	}
	return s
}

func orNone(parent string) string {
	if parent == "" {
		return "the backlog"
	}
	return parent
}

// closedWork adds a closed sprint's issues: only what the velocity chart
// reads, off the board.
func (s *Server) closedWork(sp, committed, doneP int, end time.Time) {
	sizes := []float64{3, 5, 2, 8, 3, 5, 1, 2, 5, 3}
	for i := 0; committed > 0; i++ {
		pts := min(float64(committed), sizes[i%len(sizes)])
		committed -= int(pts)
		iss := &issue{key: fmt.Sprintf("DEMO-%d", 100+len(s.hidden)), typ: "Story", summary: "Earlier work", status: todo,
			priority: "Medium", reporter: mira, points: pts, sprint: sp, created: end.AddDate(0, 0, -20), updated: end}
		if doneP >= int(pts) {
			doneP -= int(pts)
			iss.status, iss.resolved = done, end.AddDate(0, 0, -1-i%5)
		}
		s.issues[iss.key] = iss
		s.hidden = append(s.hidden, iss.key)
	}
}

func (s *Server) comment(key string, who user, body string, at time.Time) {
	iss := s.issues[key]
	s.seq++
	iss.comments = append(iss.comments, comment{id: fmt.Sprint(s.seq), author: who, body: body, created: at})
	iss.updated = maxTime(iss.updated, at)
}

// change is who changing an issue's field at at, as its changelog has it.
func (s *Server) change(key string, who user, at time.Time, field, from, to string) {
	iss := s.issues[key]
	iss.changes = append(iss.changes, change{author: who, at: at, field: field, from: from, to: to})
	iss.updated = maxTime(iss.updated, at)
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (s *Server) worklog(key string, who user, at time.Time, secs int, text string) {
	iss := s.issues[key]
	s.seq++
	iss.worklogs = append(iss.worklogs, worklog{id: fmt.Sprint(s.seq), author: who, started: at, seconds: secs, comment: text})
}

// Bulk adds n issues more, DEMO-1000 on, for timing the UI on a big board:
// two of three in the active sprint, across its lanes, the rest in the
// backlog.
func (s *Server) Bulk(n int) {
	kinds := []string{"Story", "Task", "Bug"}
	labels := [][]string{nil, {"frontend"}, {"backend"}, {"frontend", "backend"}}
	for i := range n {
		iss := &issue{key: fmt.Sprintf("DEMO-%d", 1000+i), typ: kinds[i%len(kinds)], status: statuses[i%len(statuses)],
			summary: fmt.Sprintf("Filler work item %d for a big board", i), priority: priorities[i%len(priorities)],
			assignee: &users[i%len(users)], reporter: mira, points: float64(i%8 + 1), labels: labels[i%len(labels)],
			created: s.now.AddDate(0, 0, -30+i%30), updated: s.now.Add(-time.Duration(i) * time.Minute)}
		iss.description = "What: " + iss.summary + ".\n\nWhy: the board has to stay quick with this many."
		if i%3 != 2 {
			iss.sprint = activeID
		} else {
			iss.status = todo
		}
		if iss.status == done {
			iss.resolved = s.now.AddDate(0, 0, -1)
		}
		s.issues[iss.key] = iss
		s.order = append(s.order, iss.key)
	}
}
