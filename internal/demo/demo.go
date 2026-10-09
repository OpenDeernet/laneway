// Package demo is a Jira served in-process for `laneway -demo`: a
// generated project to try the app on without a site or a token. Writes
// change it in memory and are gone when the app ends.
package demo

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	mu       sync.Mutex
	now      time.Time
	issues   map[string]*issue
	order    []string // board issues in rank order
	hidden   []string // closed sprints' issues
	sprints  []sprint
	versions []version
	seq      int
	links    []link
	linkSeq  int
	// base is where Start serves it, for links back to itself.
	base string
	git  gitlabState   // the GitLab's writes (gitlab.go)
	lw   *lanewayState // the laneway flavour's (laneway.go); nil: Jira's
	// Unhandled are the requests no route answered, for tests;
	// OnUnhandled, when set, hears each as it comes.
	Unhandled   []string
	OnUnhandled func(req string)
}

func (s *Server) unhandled(req string) {
	s.Unhandled = append(s.Unhandled, req)
	if s.OnUnhandled != nil {
		s.OnUnhandled(req)
	}
}

// New generates the project, dated around now.
func New(now time.Time) *Server { return generate(now) }

// Start serves s on a loopback port; stop ends it.
func (s *Server) Start() (baseURL string, stop func(), err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	s.mu.Lock()
	s.base = "http://" + ln.Addr().String()
	s.mu.Unlock()
	return s.base, func() { srv.Close() }, nil
}

var (
	issueRe      = regexp.MustCompile(`^/rest/api/3/issue/([A-Z]+-\d+)(/[a-z]+)?(/\d+|/move)?$`)
	sprintIssues = regexp.MustCompile(`^/rest/agile/1\.0/(?:board/\d+/)?sprint/(\d+)/issue$`)
	sprintRe     = regexp.MustCompile(`^/rest/agile/1\.0/sprint/(\d+)$`)
	versionRe    = regexp.MustCompile(`^/rest/api/3/version/(\d+)$`)
	attachRe     = regexp.MustCompile(`^/rest/api/3/issue/([A-Z]+-\d+)/attachments$`)
	attachmentRe = regexp.MustCompile(`^/rest/api/3/attachment/content/(\d+)$`)
	mediaFileRe  = regexp.MustCompile(`^/file/00000000-0000-4000-8000-(\d{12})/binary$`)
)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body map[string]any
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	p, q := r.URL.Path, r.URL.Query()
	send := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	if s.serveGitLab(w, r, body, send) || s.serveLaneway(w, r, body, send) {
		return
	}
	if r.Method == http.MethodPost && p == "/rest/api/3/search/jql" {
		jql, _ := body["jql"].(string)
		expand, _ := body["expand"].(string)
		var want []string
		if fs, ok := body["fields"].([]any); ok {
			for _, f := range fs {
				if f, ok := f.(string); ok {
					want = append(want, f)
				}
			}
		}
		// A page of maxResults (Jira's default 50), on from nextPageToken: here an offset.
		found, n := s.search(jql), 50
		if m, ok := body["maxResults"].(float64); ok && m > 0 {
			n = int(m)
		}
		from, _ := strconv.Atoi(fmt.Sprint(body["nextPageToken"]))
		from = min(from, len(found))
		to := min(from+n, len(found))
		resp := map[string]any{"issues": s.list(found[from:to], strings.Contains(expand, "changelog"), want), "isLast": to == len(found)}
		if to < len(found) {
			resp["nextPageToken"] = strconv.Itoa(to)
		}
		send(resp)
		return
	}
	switch {
	case r.Method == http.MethodGet && p == "/wiki/api/v2/pages/"+pageID+"/attachments":
		send(map[string]any{"results": []any{map[string]any{"id": "att" + pageImageID, "title": "checkout-flow.png", "fileId": "f-flow", "mediaType": "image/png"}}})
		return
	case r.Method == http.MethodGet && p == "/wiki/api/v2/attachments/att"+pageImageID:
		send(map[string]any{"id": "att" + pageImageID, "downloadLink": "/download/attachments/" + pageID + "/checkout-flow.png?api=v2"})
		return
	case r.Method == http.MethodGet && p == "/wiki/download/attachments/"+pageID+"/checkout-flow.png":
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(flowPNG())
		return
	}
	if r.Method == http.MethodGet && p == "/wiki/api/v2/pages/"+pageID {
		doc, _ := json.Marshal(pageDoc())
		send(map[string]any{"id": pageID, "title": pageTitle, "body": map[string]any{"atlas_doc_format": map[string]any{"value": string(doc)}},
			"_links": map[string]any{"webui": "/spaces/SHOP/pages/" + pageID, "base": s.base + "/wiki"}})
		return
	}
	if r.Method == http.MethodPost && p == "/rest/api/3/search/approximate-count" {
		jql, _ := body["jql"].(string)
		send(map[string]int{"count": len(s.search(jql))})
		return
	}
	if m := attachRe.FindStringSubmatch(p); m != nil && r.Method == http.MethodPost && s.issues[m[1]] != nil {
		send(s.attach(s.issues[m[1]], r.Header.Get("Content-Type"), raw))
		return
	}
	// As Jira's, an attachment's content redirects to its media file, which names its id.
	if m := attachmentRe.FindStringSubmatch(p); m != nil && r.Method == http.MethodGet {
		if a := s.attachment(m[1]); a != nil {
			http.Redirect(w, r, "/file/"+mediaFileID(a.id)+"/binary", http.StatusFound)
			return
		}
	}
	if m := mediaFileRe.FindStringSubmatch(p); m != nil && r.Method == http.MethodGet {
		if a := s.attachment(strings.TrimLeft(m[1], "0")); a != nil {
			w.Header().Set("Content-Type", a.mime)
			_, _ = w.Write(a.data)
			return
		}
	}
	if m := issueRe.FindStringSubmatch(p); m != nil {
		if iss := s.issues[m[1]]; iss != nil {
			if v, ok := s.issueRoute(r.Method, iss, m[2], strings.TrimPrefix(m[3], "/"), body, q); ok {
				if no, refused := v.(refusal); refused {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					send(map[string]any{"errorMessages": []string{string(no)}})
					return
				}
				if v == nil {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				send(v)
				return
			}
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			send(map[string]any{"errorMessages": []string{"Issue does not exist or you do not have permission to see it."}})
			return
		}
	}
	if m := sprintIssues.FindStringSubmatch(p); m != nil {
		id, _ := strconv.Atoi(m[1])
		if r.Method == http.MethodPost {
			s.moveTo(id, body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		send(s.page(s.filter(s.board(func(i *issue) bool { return i.sprint == id }), q.Get("jql")), q.Get("fields")))
		return
	}
	switch {
	case r.Method == http.MethodPost && p == "/rest/api/3/issue":
		send(s.create(body))
		return
	case r.Method == http.MethodPost && p == "/rest/agile/1.0/backlog/issue":
		s.moveTo(0, body)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPost && p == "/rest/agile/1.0/sprint":
		send(s.newSprint(body))
		return
	case r.Method == http.MethodPost && sprintRe.MatchString(p):
		id, _ := strconv.Atoi(sprintRe.FindStringSubmatch(p)[1])
		if sp := s.editSprint(id, body); sp != nil {
			send(sp)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		send(map[string]any{"errorMessages": []string{"Sprint does not exist"}})
		return
	case r.Method == http.MethodPut && versionRe.MatchString(p):
		s.release(versionRe.FindStringSubmatch(p)[1], body)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPut && p == "/rest/agile/1.0/issue/rank":
		s.rank(body)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPost && p == "/rest/api/3/issueLink":
		if !s.addLink(body) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			send(map[string]any{"errorMessages": []string{"unknown link type or issue"}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		return
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/rest/api/3/issueLink/"):
		id := strings.TrimPrefix(p, "/rest/api/3/issueLink/")
		s.links = slices.DeleteFunc(s.links, func(l link) bool { return l.id == id })
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method != http.MethodGet:
		s.unhandled(r.Method + " " + p)
		w.WriteHeader(http.StatusNoContent) // any other write succeeds and changes nothing
		return
	}
	if v, ok := s.read(p, q); ok {
		send(v)
		return
	}
	s.unhandled(r.Method + " " + p)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	send(map[string]any{"errorMessages": []string{"the demo has no " + p}})
}

// read answers the GETs that are not about one issue.
func (s *Server) read(p string, q map[string][]string) (any, bool) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	switch p {
	case "/rest/api/3/myself":
		return map[string]any{"accountId": me.id, "displayName": me.name, "emailAddress": "demo@example.com", "timeZone": "UTC"}, true
	case "/rest/api/3/dashboard/search":
		return s.dashboards(), true
	case "/rest/api/3/dashboard/" + demoDashboard + "/gadget":
		return s.gadgets(), true
	case "/rest/api/3/dashboard/" + demoDashboard + "/items/10002/properties/config":
		return map[string]any{"key": "config", "value": map[string]any{"jql": "project = DEMO ORDER BY updated DESC"}}, true
	case "/rest/api/3/serverInfo":
		if s.lw != nil {
			return map[string]any{"deploymentType": "Cloud", "lanewayServerVersion": "demo"}, true
		}
		return map[string]any{"deploymentType": "Cloud"}, true
	case "/rest/api/3/project/search":
		return map[string]any{"isLast": true, "total": 1, "values": []any{projectJSON()}}, true
	case "/rest/api/3/project/" + project:
		return projectJSON(), true
	case "/rest/api/3/field":
		return fieldsJSON(), true
	case "/rest/api/3/status":
		out := []any{}
		for _, st := range statuses {
			out = append(out, statusJSON(st))
		}
		return out, true
	case "/rest/api/3/priority":
		out := []any{}
		for i, n := range priorities {
			out = append(out, map[string]any{"id": strconv.Itoa(i + 1), "name": n})
		}
		return out, true
	case "/rest/api/3/filter/favourite":
		return []any{}, true
	case "/rest/api/3/issueLinkType":
		out := []any{}
		for i, t := range linkTypes {
			out = append(out, map[string]any{"id": strconv.Itoa(10000 + i), "name": t[0], "inward": t[1], "outward": t[2]})
		}
		return map[string]any{"issueLinkTypes": out}, true
	case "/rest/api/3/user/assignable/search", "/rest/api/3/user/search", "/rest/api/3/user/viewissue/search":
		out := []any{}
		for _, u := range users {
			if strings.Contains(strings.ToLower(u.name), strings.ToLower(get("query"))) {
				out = append(out, userJSON(&u))
			}
		}
		return out, true
	case "/rest/api/3/project/" + project + "/roledetails":
		return []any{map[string]any{"id": 10001, "name": "Developers"}, map[string]any{"id": 10002, "name": "Administrators"}}, true
	case "/rest/api/3/user/groups":
		out := []any{}
		for _, id := range slices.Sorted(maps.Keys(demoGroups)) {
			out = append(out, map[string]any{"name": demoGroups[id], "groupId": id})
		}
		return out, true
	case "/rest/api/3/project/" + project + "/version":
		return map[string]any{"values": s.versionsJSON(), "isLast": true}, true
	case "/rest/api/3/project/" + project + "/statuses":
		var sts []any
		for _, st := range statuses {
			sts = append(sts, statusJSON(st))
		}
		var out []any
		for name, id := range types {
			out = append(out, map[string]any{"id": id, "name": name, "subtask": name == "Sub-task", "statuses": sts})
		}
		return out, true
	case "/rest/api/3/issue/createmeta/" + project + "/issuetypes":
		var out []any
		for _, name := range []string{"Story", "Task", "Bug", "Epic", "Sub-task"} {
			out = append(out, map[string]any{"id": types[name], "name": name, "subtask": name == "Sub-task"})
		}
		return map[string]any{"issueTypes": out, "maxResults": len(out), "startAt": 0, "total": len(out)}, true
	case "/rest/api/3/jql/autocompletedata":
		return jqlWordsJSON(), true
	case "/rest/api/3/jql/autocompletedata/suggestions":
		return map[string]any{"results": s.jqlValues(get("fieldName"), get("fieldValue"))}, true
	case "/rest/dev-status/latest/issue/summary", "/rest/dev-status/latest/issue/detail":
		return s.devStatus(p, get), true
	case "/rest/api/3/workflowscheme/project":
		return map[string]any{"values": []any{map[string]any{"projectIds": []string{"10000"}, "workflowScheme": map[string]any{
			"id": 10000, "name": "DEMO workflow scheme", "description": "", "defaultWorkflow": workflowName, "issueTypeMappings": map[string]any{}}}}}, true
	case "/rest/api/3/workflow/search":
		return map[string]any{"isLast": true, "maxResults": 50, "startAt": 0, "total": 1, "values": []any{s.workflowJSON()}}, true
	case "/rest/greenhopper/1.0/rapidviewconfig/editmodel.json":
		return map[string]any{"id": boardID, "name": "DEMO board", "cardColorConfig": map[string]any{"rapidViewId": boardID, "canEdit": true, "cardColorStrategy": "none"}}, true
	case "/rest/agile/1.0/board":
		return map[string]any{"isLast": true, "total": 1, "values": []any{map[string]any{"id": boardID, "name": "DEMO board", "type": "scrum"}}}, true
	}
	if strings.HasPrefix(p, "/rest/api/3/issue/createmeta/"+project+"/issuetypes/") {
		return map[string]any{"fields": []any{
			map[string]any{"fieldId": "summary", "name": "Summary", "required": true, "schema": map[string]any{"type": "string"}},
			map[string]any{"fieldId": "description", "name": "Description", "required": false, "schema": map[string]any{"type": "string"}},
		}, "maxResults": 2, "startAt": 0, "total": 2}, true
	}
	b := "/rest/agile/1.0/board/" + strconv.Itoa(boardID)
	switch p {
	case b + "/configuration":
		var cols []any
		for i, st := range statuses {
			if st == qa { // shares the In Review column
				continue
			}
			sts := []any{map[string]any{"id": st.id}}
			if st == review {
				sts = append(sts, map[string]any{"id": qa.id})
			}
			col := map[string]any{"name": st.name, "statuses": sts}
			if i == 1 {
				col["max"] = 4
			}
			cols = append(cols, col)
		}
		return map[string]any{"id": boardID, "name": "DEMO board", "columnConfig": map[string]any{"columns": cols},
			"estimation": map[string]any{"type": "field", "field": map[string]any{"fieldId": pointsField, "displayName": "Story Points"}}}, true
	case b + "/quickfilter":
		qf := []any{
			map[string]any{"id": 1, "name": "Only my issues", "jql": "assignee = currentUser()"},
			map[string]any{"id": 2, "name": "Bugs", "jql": "type = Bug"},
			map[string]any{"id": 3, "name": "Flagged", "jql": "flagged is not EMPTY"},
		}
		return map[string]any{"values": qf, "total": len(qf), "isLast": true}, true
	case b + "/sprint":
		var out []any
		for _, sp := range s.sprints {
			if strings.Contains(get("state"), sp.state) || get("state") == "" {
				out = append(out, sprintJSON(sp))
			}
		}
		return map[string]any{"values": out, "isLast": true}, true
	case b + "/backlog":
		return s.page(s.filter(s.board(func(i *issue) bool { return i.sprint == 0 && i.typ != "Sub-task" }), get("jql")), get("fields")), true
	case b + "/issue":
		return s.page(s.filter(s.board(func(*issue) bool { return true }), get("jql")), get("fields")), true
	case b + "/features":
		return map[string]any{"features": []any{}}, true
	case b + "/epic":
		return map[string]any{"values": []any{}, "isLast": true}, true
	}
	if m := sprintRe.FindStringSubmatch(p); m != nil {
		id, _ := strconv.Atoi(m[1])
		for _, sp := range s.sprints {
			if sp.id == id {
				return sprintJSON(sp), true
			}
		}
	}
	return nil, false
}

// issueRoute answers /issue/KEY[/sub[/id]]; nil with ok is a 204.
// refusal is an issue route's 400, with Jira's message.
type refusal string

func (s *Server) issueRoute(method string, iss *issue, sub, id string, body map[string]any, q map[string][]string) (any, bool) {
	switch method + " " + sub {
	case "GET ":
		out := s.issueJSON(iss, true)
		if strings.Contains(firstOf(q["expand"]), "editmeta") {
			out["editmeta"] = editMetaJSON()
		}
		return out, true
	case "PUT ":
		s.edit(iss, body)
		return nil, true
	case "PUT /assignee":
		s.edit(iss, map[string]any{"fields": map[string]any{"assignee": body}})
		return nil, true
	case "GET /transitions":
		var out []any
		for _, st := range statuses {
			out = append(out, map[string]any{"id": "1" + st.id, "name": st.name, "to": statusJSON(st), "fields": map[string]any{}})
		}
		return map[string]any{"transitions": out}, true
	case "POST /transitions":
		if tr, _ := body["transition"].(map[string]any); tr != nil {
			tid, _ := tr["id"].(string)
			for _, st := range statuses {
				if "1"+st.id == tid && st != iss.status {
					iss.changes = append(iss.changes, change{author: me, at: time.Now(), field: "status", from: iss.status.name, to: st.name, fromID: iss.status.id, toID: st.id})
					iss.status, iss.updated = st, time.Now()
					if st == done {
						iss.resolved = time.Now()
					} else {
						iss.resolved = time.Time{}
					}
				}
			}
		}
		return nil, true
	case "GET /comment":
		out := []any{}
		for _, c := range iss.comments {
			out = append(out, commentJSON(c))
		}
		if strings.HasPrefix(firstOf(q["orderBy"]), "-") {
			slices.Reverse(out)
		}
		return map[string]any{"comments": out, "total": len(out), "maxResults": len(out), "startAt": 0}, true
	case "POST /comment":
		s.seq++
		c := comment{id: strconv.Itoa(s.seq), author: me, body: adfText(body["body"]), doc: written(body["body"]), created: time.Now()}
		c.parent, _ = body["parentId"].(string)
		if v, ok := body["visibility"].(map[string]any); ok {
			// As Jira: a group posted by its ID comes back with its name too.
			if id, _ := v["identifier"].(string); v["type"] == "group" && v["value"] == nil {
				v["value"] = demoGroups[id]
			}
			c.vis = v
		}
		if c.parent != "" {
			// As Jira: threads are one level deep, a reply's parent is a top-level comment.
			i := slices.IndexFunc(iss.comments, func(o comment) bool { return o.id == c.parent })
			if i < 0 || iss.comments[i].parent != "" {
				return refusal("comment: Parent comment not found and no child comments exist. Cannot create new child comments."), true
			}
		}
		iss.comments = append(iss.comments, c)
		iss.updated = time.Now()
		return commentJSON(c), true
	case "PUT /comment":
		for i, c := range iss.comments {
			if c.id == id {
				iss.comments[i].body, iss.comments[i].doc = adfText(body["body"]), written(body["body"])
				return commentJSON(iss.comments[i]), true
			}
		}
		return nil, true
	case "DELETE /comment":
		iss.comments = slices.DeleteFunc(iss.comments, func(c comment) bool { return c.id == id })
		return nil, true
	case "GET /worklog":
		out := []any{}
		for _, wl := range iss.worklogs {
			out = append(out, worklogJSON(wl))
		}
		return map[string]any{"worklogs": out, "total": len(out), "maxResults": len(out), "startAt": 0}, true
	case "POST /worklog":
		if id == "move" {
			return s.moveWorklogs(iss, body), true
		}
		secs, _ := body["timeSpentSeconds"].(float64)
		started := time.Now()
		if v, _ := body["started"].(string); v != "" {
			if t, err := time.Parse(jiraTime, v); err == nil {
				started = t
			}
		}
		s.seq++
		wl := worklog{id: strconv.Itoa(s.seq), author: me, started: started, seconds: int(secs), comment: adfText(body["comment"])}
		iss.worklogs = append(iss.worklogs, wl)
		return worklogJSON(wl), true
	case "DELETE /worklog":
		if id == "" { // the bulk delete, all or nothing
			ids, ok := issueWorklogs(iss, body)
			if !ok {
				return refusal("One or many worklogs specified do not belong to issue."), true
			}
			iss.worklogs = slices.DeleteFunc(iss.worklogs, func(w worklog) bool { return slices.Contains(ids, w.id) })
			return nil, true
		}
		iss.worklogs = slices.DeleteFunc(iss.worklogs, func(w worklog) bool { return w.id == id })
		return nil, true
	case "GET /changelog":
		out := []any{}
		for _, c := range iss.changes {
			out = append(out, changeJSON(c))
		}
		return map[string]any{"values": out, "total": len(out), "startAt": 0, "isLast": true}, true
	case "GET /editmeta":
		return map[string]any{"fields": map[string]any{}}, true
	case "GET /watchers":
		return map[string]any{"isWatching": false, "watchCount": 0, "watchers": []any{}}, true
	case "GET /votes":
		return map[string]any{"votes": 0, "hasVoted": false}, true
	case "GET /remotelink":
		if iss.key != pageIssue {
			return []any{}, true
		}
		return []any{map[string]any{"application": map[string]any{"name": "Confluence"},
			"object": map[string]any{"url": s.base + "/wiki/spaces/SHOP/pages/" + pageID + "/Guest+checkout", "title": pageTitle}}}, true
	}
	return nil, false
}

// issueWorklogs reads a bulk request's ids; ok is false unless each is one
// of iss's worklogs.
func issueWorklogs(iss *issue, body map[string]any) (ids []string, ok bool) {
	list, _ := body["ids"].([]any)
	for _, v := range list {
		n, _ := v.(float64)
		id := strconv.Itoa(int(n))
		if !slices.ContainsFunc(iss.worklogs, func(w worklog) bool { return w.id == id }) {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, len(ids) > 0
}

// moveWorklogs moves the body's worklogs from iss to its issueIdOrKey, all
// or nothing, as Jira's bulk move.
func (s *Server) moveWorklogs(iss *issue, body map[string]any) any {
	key, _ := body["issueIdOrKey"].(string)
	to := s.issues[key]
	if to == nil {
		return refusal("Issue does not exist or you do not have permission to see it.")
	}
	ids, ok := issueWorklogs(iss, body)
	if !ok {
		return refusal("One or many worklogs specified do not belong to issue.")
	}
	if to == iss {
		return nil
	}
	for _, w := range iss.worklogs {
		if slices.Contains(ids, w.id) {
			to.worklogs = append(to.worklogs, w)
		}
	}
	iss.worklogs = slices.DeleteFunc(iss.worklogs, func(w worklog) bool { return slices.Contains(ids, w.id) })
	iss.updated, to.updated = time.Now(), time.Now()
	return nil
}

func firstOf(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// edit applies a PUT's fields: summary, description, assignee, priority,
// points, labels, due date, flag.
func (s *Server) edit(iss *issue, body map[string]any) {
	before := tracked(iss)
	defer func() { // the changelog has each field that changed, as Jira's does
		after := tracked(iss)
		for _, name := range slices.Sorted(maps.Keys(after)) {
			if after[name] != before[name] {
				iss.changes = append(iss.changes, change{author: me, at: time.Now(), field: name, from: before[name], to: after[name]})
			}
		}
	}()
	f, _ := body["fields"].(map[string]any)
	for k, v := range f {
		switch k {
		case "summary":
			iss.summary, _ = v.(string)
		case "description":
			iss.description, iss.descDoc = adfText(v), written(v)
		case "assignee":
			iss.assignee = nil
			if m, _ := v.(map[string]any); m != nil {
				for i := range users {
					if users[i].id == m["accountId"] {
						iss.assignee = &users[i]
					}
				}
			}
		case "priority":
			if m, _ := v.(map[string]any); m != nil {
				if n, ok := m["name"].(string); ok {
					iss.priority = n
				} else if id, ok := m["id"].(string); ok {
					if i, _ := strconv.Atoi(id); i >= 1 && i <= len(priorities) {
						iss.priority = priorities[i-1]
					}
				}
			}
		case pointsField:
			iss.points, _ = v.(float64)
		case "labels":
			iss.labels = nil
			for _, l := range asSlice(v) {
				if s, ok := l.(string); ok {
					iss.labels = append(iss.labels, s)
				}
			}
		case "duedate":
			iss.due, _ = v.(string)
		case startField:
			iss.start, _ = v.(string)
		case "components":
			iss.components = nil
			for _, c := range asSlice(v) {
				m, _ := c.(map[string]any)
				if i := slices.IndexFunc(components, func(n string) bool { return optionID(components, n) == m["id"] }); i >= 0 {
					iss.components = append(iss.components, components[i])
				}
			}
		case testField:
			iss.testNotes, iss.testDoc = adfText(v), written(v)
		case legacyField:
			iss.legacy, _ = v.(string)
		case teamField:
			iss.team = ""
			if m, _ := v.(map[string]any); m != nil {
				if i := slices.IndexFunc(teams, func(n string) bool { return optionID(teams, n) == m["id"] }); i >= 0 {
					iss.team = teams[i]
				}
			}
		case "parent":
			iss.parent = ""
			if m, _ := v.(map[string]any); m != nil {
				iss.parent, _ = m["key"].(string)
			}
		case flagField:
			iss.flagged = len(asSlice(v)) > 0
		}
	}
	// Jira's other shape: update: {labels: [{add: x}], …}.
	u, _ := body["update"].(map[string]any)
	for _, op := range asSlice(u["labels"]) {
		m, _ := op.(map[string]any)
		if a, ok := m["add"].(string); ok && !slices.Contains(iss.labels, a) {
			iss.labels = append(iss.labels, a)
		}
		if r, ok := m["remove"].(string); ok {
			iss.labels = slices.DeleteFunc(iss.labels, func(l string) bool { return l == r })
		}
	}
	iss.updated = time.Now()
}

// tracked is the fields an edit's changelog entry names, by the
// changelog's field name, as text.
func tracked(iss *issue) map[string]string {
	who, pts := "", ""
	if iss.assignee != nil {
		who = iss.assignee.name
	}
	if iss.points > 0 {
		pts = strconv.FormatFloat(iss.points, 'f', -1, 64)
	}
	return map[string]string{"summary": iss.summary, "assignee": who, "priority": iss.priority, "Story Points": pts,
		"labels": strings.Join(iss.labels, " "), "duedate": iss.due, "Legacy ref": iss.legacy, "Team": iss.team}
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// create adds an issue from a POST: its summary, type, parent and
// description; a new one goes to the backlog.
func (s *Server) create(body map[string]any) any {
	f, _ := body["fields"].(map[string]any)
	key := fmt.Sprintf("%s-%d", project, len(s.order)+1)
	for s.issues[key] != nil {
		key += "0"
	}
	iss := &issue{key: key, typ: "Task", status: todo, priority: "Medium", reporter: me, created: time.Now(), updated: time.Now()}
	iss.summary, _ = f["summary"].(string)
	iss.description, iss.descDoc = adfText(f["description"]), written(f["description"])
	if t, _ := f["issuetype"].(map[string]any); t != nil {
		for name, id := range types {
			if t["id"] == id || t["name"] == name {
				iss.typ = name
			}
		}
	}
	if p, _ := f["parent"].(map[string]any); p != nil {
		iss.parent, _ = p["key"].(string)
	}
	s.issues[key] = iss
	s.order = append(s.order, key)
	return map[string]any{"id": numID(key), "key": key}
}

// moveTo puts the body's issues in sprint id, 0 the backlog.
func (s *Server) moveTo(id int, body map[string]any) {
	for _, k := range asSlice(body["issues"]) {
		if iss := s.issues[fmt.Sprint(k)]; iss != nil {
			iss.sprint = id
		}
	}
}

// newSprint adds a future sprint named as the body says.
func (s *Server) newSprint(body map[string]any) map[string]any {
	id := 0
	for _, sp := range s.sprints {
		id = max(id, sp.id)
	}
	name, _ := body["name"].(string)
	sp := sprint{id: id + 1, name: name, state: "future"}
	s.sprints = append(s.sprints, sp)
	return sprintJSON(sp)
}

// editSprint applies a sprint update: name, goal, dates, and state. A
// closed sprint's open issues go to the backlog and its done ones off the
// board, as Jira does. nil for no such sprint.
func (s *Server) editSprint(id int, body map[string]any) map[string]any {
	i := slices.IndexFunc(s.sprints, func(sp sprint) bool { return sp.id == id })
	if i < 0 {
		return nil
	}
	sp := &s.sprints[i]
	if v, ok := body["name"].(string); ok {
		sp.name = v
	}
	if v, ok := body["goal"].(string); ok {
		sp.goal = v
	}
	for k, t := range map[string]*time.Time{"startDate": &sp.start, "endDate": &sp.end} {
		if v, ok := body[k].(string); ok {
			if at, err := time.Parse(time.RFC3339, v); err == nil {
				*t = at
			}
		}
	}
	if v, ok := body["state"].(string); ok {
		sp.state = v
	}
	if sp.state == "closed" {
		for _, k := range slices.Clone(s.order) {
			if iss := s.issues[k]; iss.sprint == id {
				if iss.status == done {
					s.order = slices.DeleteFunc(s.order, func(o string) bool { return o == k })
					s.hidden = append(s.hidden, k)
				} else {
					iss.sprint = 0
				}
			}
		}
	}
	return sprintJSON(*sp)
}

// rank moves the body's issues before rankBeforeIssue, or after
// rankAfterIssue.
func (s *Server) rank(body map[string]any) {
	var keys []string
	for _, k := range asSlice(body["issues"]) {
		keys = append(keys, fmt.Sprint(k))
	}
	rest := slices.DeleteFunc(slices.Clone(s.order), func(k string) bool { return slices.Contains(keys, k) })
	at := len(rest)
	if b, ok := body["rankBeforeIssue"].(string); ok {
		if i := slices.Index(rest, b); i >= 0 {
			at = i
		}
	} else if a, ok := body["rankAfterIssue"].(string); ok {
		if i := slices.Index(rest, a); i >= 0 {
			at = i + 1
		}
	}
	s.order = slices.Insert(rest, at, keys...)
}

// board is the ranked board issues keep says yes to; epics are not on it.
func (s *Server) board(keep func(*issue) bool) []*issue {
	var out []*issue
	for _, k := range s.order {
		if iss := s.issues[k]; iss.typ != "Epic" && keep(iss) {
			out = append(out, iss)
		}
	}
	return out
}

// page is an Agile API page of issues with the fields asked for, comma
// separated.
func (s *Server) page(issues []*issue, fields string) map[string]any {
	var want []string
	if fields != "" {
		want = strings.Split(fields, ",")
	}
	return map[string]any{"startAt": 0, "maxResults": len(issues), "total": len(issues), "issues": s.list(issues, false, want)}
}

// list is issues with the fields in want, all of them for none.
func (s *Server) list(issues []*issue, changelog bool, want []string) []any {
	out := []any{}
	for _, iss := range issues {
		j := s.issueJSON(iss, false)
		onlyFields(j, want)
		if changelog {
			var hs []any
			for _, c := range iss.changes {
				hs = append(hs, changeJSON(c))
			}
			j["changelog"] = map[string]any{"histories": hs}
		}
		out = append(out, j)
	}
	return out
}

// linkTypes are name, inward, outward.
var linkTypes = [][3]string{
	{"Blocks", "is blocked by", "blocks"},
	{"Relates", "relates to", "relates to"},
	{"Duplicate", "is duplicated by", "duplicates"},
}

// link is one issue link: from is the inward end ("from blocks to").
type link struct{ id, typ, from, to string }

func (s *Server) addLink(body map[string]any) bool {
	name := func(k, f string) string {
		m, _ := body[k].(map[string]any)
		v, _ := m[f].(string)
		return v
	}
	typ, from, to := name("type", "name"), name("inwardIssue", "key"), name("outwardIssue", "key")
	if s.issues[from] == nil || s.issues[to] == nil || from == to || !slices.ContainsFunc(linkTypes, func(t [3]string) bool { return t[0] == typ }) {
		return false
	}
	s.linkSeq++
	s.links = append(s.links, link{strconv.Itoa(20000 + s.linkSeq), typ, from, to})
	return true
}

// linksJSON is the issuelinks field of key: each link names the other end.
func (s *Server) linksJSON(key string) []any {
	out := []any{}
	for _, l := range s.links {
		if l.from != key && l.to != key {
			continue
		}
		var t [3]string
		for _, lt := range linkTypes {
			if lt[0] == l.typ {
				t = lt
			}
		}
		e := map[string]any{"id": l.id, "type": map[string]any{"name": t[0], "inward": t[1], "outward": t[2]}}
		other, side := l.to, "outwardIssue"
		if l.to == key {
			other, side = l.from, "inwardIssue"
		}
		if o := s.issues[other]; o != nil {
			e[side] = map[string]any{"key": o.key, "fields": map[string]any{"summary": o.summary, "status": statusJSON(o.status)}}
			out = append(out, e)
		}
	}
	return out
}

// The Confluence page DEMO-4 links: the guest checkout's design.
const (
	pageIssue = "DEMO-4"
	pageID    = "40961"
	pageTitle = "Guest checkout: design"
	// pageImageID is its one image's attachment.
	pageImageID = "40962"
)

// flowPNG is the page's image: three steps, left to right.
func flowPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 240, 80))
	steps := []color.RGBA{{91, 141, 239, 255}, {59, 130, 196, 255}, {40, 128, 79, 255}}
	for y := range 80 {
		for x := range 240 {
			c := color.RGBA{250, 250, 250, 255}
			if i := x / 80; x%80 >= 10 && x%80 < 70 && y >= 20 && y < 60 {
				c = steps[i]
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func pageDoc() any {
	text := func(t string) map[string]any { return map[string]any{"type": "text", "text": t} }
	para := func(t string) map[string]any { return map[string]any{"type": "paragraph", "content": []any{text(t)}} }
	item := func(t string) map[string]any { return map[string]any{"type": "listItem", "content": []any{para(t)}} }
	return map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "extension", "attrs": map[string]any{"extensionKey": "toc"}},
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}, "content": []any{text("Goal")}},
		para("Let a shopper pay without making an account; offer one after the order, filled in from it."),
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}, "content": []any{text("Flow")}},
		map[string]any{"type": "orderedList", "content": []any{item("Cart → Checkout as guest"), item("Address and e-mail, validated as typed"), item("Pay; the order page offers an account")}},
		map[string]any{"type": "panel", "attrs": map[string]any{"panelType": "note"}, "content": []any{para("Behind the guest_checkout flag until the order page loads under 400 ms.")}},
		map[string]any{"type": "mediaSingle", "content": []any{map[string]any{"type": "media", "attrs": map[string]any{"id": "f-flow", "alt": "checkout-flow.png"}}}},
	}}
}

// onlyFields keeps the fields of issue j that want asks for, as Jira does;
// no want, *all or *navigable keeps them all.
func onlyFields(j map[string]any, want []string) {
	if len(want) == 0 || slices.Contains(want, "*all") || slices.Contains(want, "*navigable") {
		return
	}
	f, _ := j["fields"].(map[string]any)
	for k := range f {
		if !slices.Contains(want, k) {
			delete(f, k)
		}
	}
}

// attach keeps the files of a multipart upload on iss, as Jira's
// /issue/KEY/attachments does, and answers with them.
func (s *Server) attach(iss *issue, contentType string, raw []byte) []any {
	out := []any{}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return out
	}
	mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		if part.FileName() == "" {
			continue
		}
		data, _ := io.ReadAll(part)
		s.seq++
		a := attachment{id: fmt.Sprint(20000 + s.seq), name: part.FileName(), data: data, created: time.Now(),
			mime: cmp.Or(mime.TypeByExtension(filepath.Ext(part.FileName())), "application/octet-stream")}
		iss.attachments = append(iss.attachments, a)
		out = append(out, s.attachmentJSON(a))
	}
	iss.updated = time.Now()
	return out
}

// mediaFileID is attachment id's media file, as Jira's Media Services names it.
func mediaFileID(id string) string { return fmt.Sprintf("00000000-0000-4000-8000-%012s", id) }

// attachment is the upload with id, nil for none.
func (s *Server) attachment(id string) *attachment {
	for _, iss := range s.issues {
		for i := range iss.attachments {
			if iss.attachments[i].id == id {
				return &iss.attachments[i]
			}
		}
	}
	return nil
}
