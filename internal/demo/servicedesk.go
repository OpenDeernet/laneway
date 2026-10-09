package demo

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Service desk requests (/rest/servicedeskapi), in the laneway flavour
// only: the demo project is a desk there, as laneway-server's can be. The
// plain demo is Jira without Service Management, which has no such API.

const demoDesk = "1"

// deskQueues are the desk's queues, their JQL as laneway-server keeps it
// (the desk's project left to the desk).
var deskQueues = []struct{ id, name, jql string }{
	{"1", "Open requests", "statusCategory != Done ORDER BY created DESC"},
	{"2", "Unassigned", "assignee is EMPTY ORDER BY created DESC"},
	{"3", "Waiting for review", `status = "In Review" ORDER BY created DESC`},
}

// The desk's two SLA goals.
const (
	firstResponseGoal = 8 * time.Hour
	resolutionGoal    = 10 * 24 * time.Hour
)

// serveDesk answers the Service Desk API: 404 on the plain demo, as Jira
// without it; the desk, its requests and queues in the laneway flavour.
func (s *Server) serveDesk(w http.ResponseWriter, r *http.Request, body map[string]any, send func(any)) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, "/rest/servicedeskapi/") {
		return false
	}
	gone := func(msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		send(map[string]any{"errorMessages": []string{msg}})
	}
	if s.lw == nil {
		gone("No service desk here.")
		return true
	}
	rest, isReq := strings.CutPrefix(p, "/rest/servicedeskapi/request/")
	key, sub, _ := strings.Cut(rest, "/")
	switch {
	case r.Method == http.MethodGet && p == "/rest/servicedeskapi/servicedesk":
		send(deskPage([]any{map[string]any{"id": demoDesk, "projectId": "10000", "projectKey": project, "projectName": "Demo Shop"}}))
	case r.Method == http.MethodGet && p == "/rest/servicedeskapi/servicedesk/"+demoDesk+"/queue":
		count := r.URL.Query().Get("includeCount") == "true"
		var out []any
		for _, q := range deskQueues {
			o := map[string]any{"id": q.id, "name": q.name, "jql": q.jql, "fields": []string{"issuetype", "issuekey", "summary", "created", "reporter", "status"}}
			if count {
				o["issueCount"] = len(s.search(q.jql))
			}
			out = append(out, o)
		}
		send(deskPage(out))
	case isReq && (sub == "" || sub == "comment"):
		iss := s.issues[key]
		if iss == nil {
			gone("Request " + key + " does not exist or you do not have permission to see it.")
			return true
		}
		if sub == "" && r.Method == http.MethodGet {
			send(s.requestJSON(iss, r.URL.Query().Get("expand")))
			return true
		}
		if r.Method != http.MethodPost {
			return false
		}
		text, _ := body["body"].(string)
		public, _ := body["public"].(bool)
		s.seq++
		c := comment{id: strconv.Itoa(s.seq), author: me, body: text, created: time.Now(), public: &public}
		iss.comments = append(iss.comments, c)
		iss.updated = time.Now()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		send(map[string]any{"id": c.id, "body": text, "public": public, "author": userJSON(&me), "created": deskDate(c.created)})
	default:
		return false
	}
	return true
}

// requestJSON is iss as a customer request, with the request type and
// SLAs when expand asks for them.
func (s *Server) requestJSON(iss *issue, expand string) map[string]any {
	rtID, rtName := "11", "Get help"
	if iss.typ == "Bug" {
		rtID, rtName = "12", "Report a problem"
	}
	o := map[string]any{"issueId": numID(iss.key), "issueKey": iss.key, "requestTypeId": rtID, "serviceDeskId": demoDesk,
		"createdDate": deskDate(iss.created), "reporter": customerJSON(iss.reporter),
		"requestFieldValues": []any{map[string]any{"fieldId": "summary", "label": "Summary", "value": iss.summary}},
		"currentStatus":      map[string]any{"status": iss.status.name, "statusCategory": strings.ToUpper(iss.status.cat)},
		"_links":             map[string]any{"web": s.base + "/servicedesk/customer/portal/1/" + iss.key}}
	ex := strings.Split(expand, ",")
	if slices.Contains(ex, "requestType") {
		o["requestType"] = map[string]any{"id": rtID, "name": rtName, "serviceDeskId": demoDesk, "issueTypeId": types[iss.typ]}
	}
	if slices.Contains(ex, "sla") {
		o["sla"] = deskPage(s.slas(iss))
	}
	return o
}

// slas are iss's two clocks against s.now: first response (stopped by
// someone else's comment) and resolution (stopped by Done, paused while
// in review).
func (s *Server) slas(iss *issue) []any {
	var answered time.Time
	for _, c := range iss.comments {
		if c.author != iss.reporter {
			answered = c.created
			break
		}
	}
	end := s.now
	if !iss.resolved.IsZero() {
		end = iss.resolved
	}
	return []any{
		slaJSON("1", "Time to first response", firstResponseGoal, iss.created, answered, s.now, false),
		slaJSON("2", "Time to resolution", resolutionGoal, iss.created, iss.resolved, end, iss.status == review),
	}
}

// slaJSON is one SLA as Jira Service Management has it: an ongoing cycle
// until stop, then a completed one.
func slaJSON(id, name string, goal time.Duration, start, stop, now time.Time, paused bool) map[string]any {
	end := now
	if !stop.IsZero() {
		end = stop
	}
	elapsed := end.Sub(start)
	cycle := map[string]any{"startTime": deskDate(start), "breached": elapsed > goal, "goalDuration": deskDuration(goal),
		"elapsedTime": deskDuration(elapsed), "remainingTime": deskDuration(goal - elapsed), "breachTime": deskDate(start.Add(goal))}
	o := map[string]any{"id": id, "name": name, "completedCycles": []any{}}
	if stop.IsZero() {
		cycle["paused"], cycle["withinCalendarHours"] = paused, false
		o["ongoingCycle"] = cycle
	} else {
		cycle["stopTime"] = deskDate(stop)
		o["completedCycles"] = []any{cycle}
	}
	return o
}

func customerJSON(u user) map[string]any {
	first, _, _ := strings.Cut(u.name, " ")
	return map[string]any{"accountId": u.id, "displayName": u.name, "emailAddress": strings.ToLower(first) + "@example.com", "active": true, "timeZone": "UTC"}
}

// deskPage is the Service Desk API's one page of values.
func deskPage(values []any) map[string]any {
	if values == nil {
		values = []any{}
	}
	return map[string]any{"size": len(values), "start": 0, "limit": 50, "isLastPage": true, "values": values}
}

func deskDate(t time.Time) map[string]any {
	return map[string]any{"iso8601": t.Format("2006-01-02T15:04:05-0700"), "jira": t.Format(jiraTime), "friendly": t.Format("02/Jan/06 3:04 PM"), "epochMillis": t.UnixMilli()}
}

// deskDuration is a duration as Jira words it: "4h", "2h 30m", "-1d".
func deskDuration(d time.Duration) map[string]any {
	ms, sign := d.Milliseconds(), ""
	if d < 0 {
		sign, d = "-", -d
	}
	m := int(d.Round(time.Minute).Minutes())
	var f string
	switch {
	case m >= 24*60 && m%(24*60) == 0:
		f = fmt.Sprintf("%dd", m/(24*60))
	case m >= 24*60:
		f = fmt.Sprintf("%dd %dh", m/(24*60), m%(24*60)/60)
	case m >= 60 && m%60 == 0:
		f = fmt.Sprintf("%dh", m/60)
	case m >= 60:
		f = fmt.Sprintf("%dh %dm", m/60, m%60)
	default:
		f = fmt.Sprintf("%dm", m)
	}
	return map[string]any{"millis": ms, "friendly": sign + f}
}
