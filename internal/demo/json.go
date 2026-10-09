package demo

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// Jira's JSON for the generated project.

const jiraTime = "2006-01-02T15:04:05.000-0700"

func stamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Format(jiraTime)
}

func numID(key string) string {
	_, n, _ := strings.Cut(key, "-")
	return n
}

func statusJSON(st status) map[string]any {
	names := map[string]string{"new": "To Do", "indeterminate": "In Progress", "done": "Done"}
	return map[string]any{"id": st.id, "name": st.name, "statusCategory": map[string]any{"key": st.cat, "name": names[st.cat]}}
}

func userJSON(u *user) any {
	if u == nil {
		return nil
	}
	return map[string]any{"accountId": u.id, "displayName": u.name, "active": true}
}

func typeJSON(name string) map[string]any {
	return map[string]any{"id": types[name], "name": name, "subtask": name == "Sub-task"}
}

func projectJSON() map[string]any {
	return map[string]any{"id": "10000", "key": project, "name": "Demo Shop", "projectTypeKey": "software", "style": "classic"}
}

// sprintFieldJSON is sp as an issue's Sprint field has it, which names
// the board boardId where the Agile API says originBoardId.
// issueProjectJSON is the project as an issue's fields name it: Jira
// leaves out its style there.
func issueProjectJSON() map[string]any {
	j := projectJSON()
	delete(j, "style")
	return j
}

func sprintFieldJSON(sp sprint) map[string]any {
	j := sprintJSON(sp)
	j["boardId"] = j["originBoardId"]
	delete(j, "originBoardId")
	return j
}

func sprintJSON(sp sprint) map[string]any {
	j := map[string]any{"id": sp.id, "name": sp.name, "state": sp.state, "goal": sp.goal, "originBoardId": boardID}
	if !sp.start.IsZero() {
		j["startDate"], j["endDate"] = sp.start.Format(time.RFC3339), sp.end.Format(time.RFC3339)
	}
	if sp.state == "closed" {
		j["completeDate"] = sp.end.Format(time.RFC3339)
	}
	return j
}

func fieldsJSON() []any {
	f := func(id, name, typ, custom string) map[string]any {
		schema := map[string]any{"type": typ}
		if custom != "" {
			schema["custom"] = custom
		}
		return map[string]any{"id": id, "name": name, "custom": custom != "", "schema": schema}
	}
	return []any{
		f("summary", "Summary", "string", ""),
		f("status", "Status", "status", ""),
		f("duedate", "Due date", "date", ""),
		f(pointsField, "Story Points", "number", "com.atlassian.jira.plugin.system.customfieldtypes:float"),
		f(sprintField, "Sprint", "array", "com.pyxis.greenhopper.jira:gh-sprint"),
		f(flagField, "Flagged", "array", "com.atlassian.jira.plugin.system.customfieldtypes:multicheckboxes"),
		f(startField, "Start date", "date", "com.atlassian.jira.plugin.system.customfieldtypes:datepicker"),
		f(teamField, "Team", "option", "com.atlassian.jira.plugin.system.customfieldtypes:select"),
		f(devField, "Development", "any", "com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf"),
		f(testField, "Test notes", "string", "com.atlassian.jira.plugin.system.customfieldtypes:textarea"),
		f(legacyField, "Legacy ref", "string", "com.atlassian.jira.plugin.system.customfieldtypes:textfield"),
	}
}

// adf is text as an Atlassian document: a paragraph per blank-line block.
func adf(text string) any {
	if text == "" {
		return nil
	}
	var content []any
	for _, p := range strings.Split(text, "\n\n") {
		content = append(content, map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": p}}})
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// written is a written rich-text value kept as Jira keeps it: an
// Atlassian document; nil for anything else.
func written(v any) any {
	if m, ok := v.(map[string]any); ok && m["type"] == "doc" {
		return m
	}
	return nil
}

// commentDoc is c's body as written, else its text as paragraphs, its @names mentions.
func commentDoc(c comment) any {
	if c.doc != nil {
		return c.doc
	}
	return mentions(adf(c.body))
}

// docOr is doc, the value as written, or else text as paragraphs.
func docOr(doc any, text string) any {
	if doc != nil {
		return doc
	}
	return adf(text)
}

// adfText is the text of a document the app sent, its paragraphs apart by
// a blank line; a plain string stays as it is.
func adfText(v any) string {
	switch d := v.(type) {
	case string:
		return d
	case map[string]any:
		var blocks []string
		for _, b := range asSlice(d["content"]) {
			var sb strings.Builder
			collectText(b, &sb)
			blocks = append(blocks, sb.String())
		}
		return strings.Join(blocks, "\n\n")
	}
	return ""
}

func collectText(n any, sb *strings.Builder) {
	m, _ := n.(map[string]any)
	switch m["type"] {
	case "text":
		t, _ := m["text"].(string)
		sb.WriteString(t)
	case "hardBreak":
		sb.WriteString("\n")
	case "mention":
		if a, _ := m["attrs"].(map[string]any); a != nil {
			t, _ := a["text"].(string)
			sb.WriteString(t)
		}
	}
	for _, c := range asSlice(m["content"]) {
		collectText(c, sb)
	}
}

// commentJSON is a comment as the comment endpoint has it; parentId only
// there, as on Jira.
func commentJSON(c comment) map[string]any {
	m := map[string]any{"id": c.id, "author": userJSON(&c.author), "body": commentDoc(c), "created": stamp(c.created), "updated": stamp(c.created)}
	if c.parent != "" {
		n, _ := strconv.Atoi(c.parent) // Jira sends it as a number
		m["parentId"] = n
	}
	if c.vis != nil {
		m["visibility"] = c.vis
	}
	if c.public != nil {
		m["jsdPublic"] = *c.public
	}
	return m
}

func worklogJSON(w worklog) map[string]any {
	return map[string]any{"id": w.id, "author": userJSON(&w.author), "started": stamp(w.started), "timeSpentSeconds": w.seconds, "comment": adf(w.comment)}
}

func changeJSON(c change) map[string]any {
	return map[string]any{"author": userJSON(&c.author), "created": stamp(c.at),
		"items": []any{map[string]any{"field": c.field, "fieldId": c.field, "from": c.fromID, "to": c.toID, "fromString": c.from, "toString": c.to}}}
}

// brief is an issue as a parent, subtask or sprint reference shows it.
func (s *Server) brief(key string) any {
	iss := s.issues[key]
	if iss == nil {
		return nil
	}
	return map[string]any{"id": numID(key), "key": key, "fields": map[string]any{
		"summary": iss.summary, "status": statusJSON(iss.status), "issuetype": typeJSON(iss.typ), "priority": map[string]any{"name": iss.priority}}}
}

// issueJSON is iss with every field; full adds its comments.
func (s *Server) issueJSON(iss *issue, full bool) map[string]any {
	var subtasks []any
	for _, k := range s.order {
		if s.issues[k].parent == iss.key && s.issues[k].typ == "Sub-task" {
			subtasks = append(subtasks, s.brief(k))
		}
	}
	var sp any
	for _, x := range s.sprints {
		if x.id == iss.sprint {
			sp = []any{sprintFieldJSON(x)}
		}
	}
	var flag any
	if iss.flagged {
		flag = []any{map[string]any{"value": "Impediment"}}
	}
	var points any
	if iss.points > 0 {
		points = iss.points
	}
	var due, start any
	if iss.due != "" {
		due = iss.due
	}
	if iss.start != "" {
		start = iss.start
	}
	var parent any
	if iss.parent != "" {
		parent = s.brief(iss.parent)
	}
	var changed time.Time
	for _, c := range iss.changes {
		if c.field == "status" {
			changed = c.at
		}
	}
	if changed.IsZero() {
		changed = iss.created
	}
	fixVersions := []any{}
	for _, v := range s.versions {
		if v.id == iss.fixVersion {
			fixVersions = append(fixVersions, map[string]any{"id": v.id, "name": v.name, "released": v.released})
		}
	}
	labels := iss.labels
	if labels == nil {
		labels = []string{}
	}
	fields := map[string]any{
		"summary": iss.summary, "status": statusJSON(iss.status), "issuetype": typeJSON(iss.typ),
		"priority": map[string]any{"id": strconv.Itoa(slices.Index(priorities, iss.priority) + 1), "name": iss.priority}, "assignee": userJSON(iss.assignee), "reporter": userJSON(&iss.reporter),
		"labels": labels, "created": stamp(iss.created), "updated": stamp(iss.updated), "resolutiondate": stamp(iss.resolved),
		"statuscategorychangedate": stamp(changed), "duedate": due, "description": docOr(iss.descDoc, iss.description), "parent": parent,
		"subtasks": subtasks, "issuelinks": s.linksJSON(iss.key), "attachment": s.attachmentsJSON(iss), "fixVersions": fixVersions,
		"project":   issueProjectJSON(),
		pointsField: points, sprintField: sp, flagField: flag, startField: start, devField: s.devSummary(iss.key),
	}
	if full {
		var cs []any
		for _, c := range iss.comments {
			cj := commentJSON(c)
			delete(cj, "parentId") // Jira's issue never has it: the client must ask the endpoint
			cs = append(cs, cj)
		}
		fields["comment"] = map[string]any{"comments": cs, "total": len(cs), "maxResults": len(cs), "startAt": 0}
	}
	comps := []any{}
	for _, c := range iss.components {
		comps = append(comps, map[string]any{"id": optionID(components, c), "name": c})
	}
	fields["components"] = comps
	fields[testField], fields[legacyField] = docOr(iss.testDoc, iss.testNotes), nil
	if iss.legacy != "" {
		fields[legacyField] = iss.legacy
	}
	fields[teamField] = nil
	if iss.team != "" {
		fields[teamField] = map[string]any{"id": optionID(teams, iss.team), "value": iss.team}
	}
	return map[string]any{"id": numID(iss.key), "key": iss.key, "fields": fields}
}

// optionID is name's id among an edit screen field's options.
func optionID(names []string, name string) string {
	return strconv.Itoa(20000 + slices.Index(names, name))
}

// editMetaJSON is the edit screen's fields besides the panel's own: the
// components and the team, which the field configuration requires.
func editMetaJSON() map[string]any {
	opts := func(names []string, key string) []any {
		out := []any{}
		for _, n := range names {
			out = append(out, map[string]any{"id": optionID(names, n), key: n})
		}
		return out
	}
	return map[string]any{"fields": map[string]any{
		"components": map[string]any{"name": "Components", "required": false, "schema": map[string]any{"type": "array", "items": "component", "system": "components"}, "allowedValues": opts(components, "name")},
		testField:    map[string]any{"name": "Test notes", "required": false, "schema": map[string]any{"type": "string", "custom": "com.atlassian.jira.plugin.system.customfieldtypes:textarea"}},
		legacyField:  map[string]any{"name": "Legacy ref", "required": false, "schema": map[string]any{"type": "string", "custom": "com.atlassian.jira.plugin.system.customfieldtypes:textfield"}},
		teamField:    map[string]any{"name": "Team", "required": true, "schema": map[string]any{"type": "option", "custom": "com.atlassian.jira.plugin.system.customfieldtypes:select"}, "allowedValues": opts(teams, "value")},
	}}
}

// versionsJSON is the project's versions, newest first, with their issues'
// progress.
func (s *Server) versionsJSON() []any {
	out := []any{}
	for _, v := range slices.Backward(s.versions) {
		counts := map[string]int{"new": 0, "indeterminate": 0, "done": 0}
		for _, k := range append(slices.Clone(s.order), s.hidden...) {
			if iss := s.issues[k]; iss.fixVersion == v.id {
				counts[iss.status.cat]++
			}
		}
		out = append(out, map[string]any{"id": v.id, "name": v.name, "released": v.released, "archived": false, "releaseDate": v.date,
			"issuesStatusForFixVersion": map[string]int{"unmapped": 0, "toDo": counts["new"], "inProgress": counts["indeterminate"], "done": counts["done"]}})
	}
	return out
}

// release marks a version released, as the body says.
func (s *Server) release(id string, body map[string]any) {
	for i, v := range s.versions {
		if v.id != id {
			continue
		}
		if r, ok := body["released"].(bool); ok {
			s.versions[i].released = r
		}
		if d, ok := body["releaseDate"].(string); ok {
			s.versions[i].date = d
		}
	}
}

// mentions turns "@Name" of a demo user in doc's text into a mention, as
// Jira stores one.
func mentions(doc any) any {
	d, _ := doc.(map[string]any)
	if d == nil {
		return doc
	}
	for _, p := range asSlice(d["content"]) {
		para, _ := p.(map[string]any)
		var out []any
		for _, n := range asSlice(para["content"]) {
			t, _ := n.(map[string]any)
			text, _ := t["text"].(string)
			for _, u := range users {
				if before, after, ok := strings.Cut(text, "@"+u.name); ok && t["type"] == "text" {
					if before != "" {
						out = append(out, map[string]any{"type": "text", "text": before})
					}
					out = append(out, map[string]any{"type": "mention", "attrs": map[string]any{"id": u.id, "text": "@" + u.name}})
					text = after
				}
			}
			if text != "" || t["type"] != "text" {
				if t["type"] == "text" {
					n = map[string]any{"type": "text", "text": text}
				}
				out = append(out, n)
			}
		}
		para["content"] = out
	}
	return doc
}

// workflowName is the one workflow every demo issue type follows.
const workflowName = "DEMO workflow"

// workflowJSON is that workflow as /workflow/search expands it: a global
// transition into each status, none with a validator.
func (s *Server) workflowJSON() map[string]any {
	var trs []any
	for _, st := range statuses {
		trs = append(trs, map[string]any{"id": "1" + st.id, "name": st.name, "description": "", "from": []string{}, "to": st.id, "type": "global",
			"rules": map[string]any{"validators": []any{}, "postFunctions": []any{}}})
	}
	return map[string]any{"id": map[string]any{"name": workflowName, "entityId": "demo-workflow"}, "description": "", "transitions": trs}
}

func (s *Server) attachmentsJSON(iss *issue) []any {
	out := []any{}
	for _, a := range iss.attachments {
		out = append(out, s.attachmentJSON(a))
	}
	return out
}

func (s *Server) attachmentJSON(a attachment) map[string]any {
	return map[string]any{"self": s.base + "/rest/api/3/attachments/" + a.id, "id": a.id, "filename": a.name, "author": userJSON(&me),
		"created": stamp(a.created), "size": len(a.data), "mimeType": a.mime, "content": s.base + "/rest/api/3/attachment/content/" + a.id}
}
