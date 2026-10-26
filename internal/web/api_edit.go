package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// Editing: one field at a time (PUT /issues/{key}/field/{field}), many issues
// at once (POST /bulk), moving with the fields a workflow asks for, creating.
// Every write answers with the edit that takes it back, built from the values
// before it, so the frontend's undo needs no state of its own.

func init() {
	get("/priorities", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().Priorities(ctx)
	})
	get("/users", users)
	get("/projects/creatable", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().Projects(ctx)
	})
	get("/projects/{project}/issuetypes", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		c, p := s.Client(), r.PathValue("project")
		types, err := c.IssueTypes(ctx, p)
		if err != nil {
			return nil, err
		}
		subs, _ := c.SubtaskTypes(ctx, p)
		return map[string]any{"Types": uniqueByID(types), "Subtasks": uniqueByID(subs)}, nil
	})
	get("/projects/{project}/createfields", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		typ := Q(r, "type")
		if typ == "" {
			return nil, badRequest(i18n.T("type is required"))
		}
		return s.Client().CreateFields(ctx, r.PathValue("project"), typ)
	})
	get("/projects/{project}/labels", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		ls, err := s.Client().RecentLabels(ctx, r.PathValue("project"))
		if ls == nil {
			ls = []string{}
		}
		return ls, err
	})
	// Jira's labels starting with ?q=, as you type (TUI label_suggest.go);
	// ?field= is a custom labels field's clause, cf[10050].
	get("/labels", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		field := cmpOr(Q(r, "field"), "labels")
		if field != "labels" && !labelClause.MatchString(field) {
			return nil, badRequest(i18n.T("bad labels field"))
		}
		ls, err := s.Client().JQLValues(ctx, field, strings.TrimSpace(Q(r, "q")))
		return nonNil(ls), err
	})
	get("/projects/{project}/statuses", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().ProjectStatuses(ctx, r.PathValue("project"))
	})
	get("/projects/{project}/sprints", projectSprints)
	get("/issues/{key}/editmeta", editMeta)
	put("/fields/pinned/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ On bool }](r)
		if err != nil {
			return nil, err
		}
		if !fieldIDRe.MatchString(r.PathValue("id")) {
			return nil, badRequest(i18n.T("bad field id"))
		}
		if s.opt.Store == nil {
			return nil, httpError{http.StatusNotImplemented, i18n.T("no state file to keep pins in")}
		}
		return jira.SetFieldPin(s.opt.Store, r.PathValue("id"), b.On)
	})
	get("/issues/{key}/transitionmeta", transitionMeta)
	post("/issues/{key}/transitionwith", transitionWith)
	put("/issues/{key}/field/{field}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		e, err := Body[edit](r)
		if err != nil {
			return nil, err
		}
		e.Field = r.PathValue("field")
		undo, err := applyEdit(ctx, s.Client(), r.PathValue("key"), e)
		if err != nil {
			return nil, err
		}
		return map[string]any{"Undo": undo}, nil
	})
	del("/issues/{key}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key := r.PathValue("key")
		if !jira.ValidKey(key) {
			return nil, badRequest(i18n.T("bad issue key"))
		}
		return nil, s.Client().DeleteIssue(ctx, key, Q(r, "subtasks") != "")
	})
	post("/issues", createIssue)
	post("/form", formRules)
	get("/issues/{key}/clonedraft", cloneDraft)
	post("/bulk", bulk)
}

// edit is one change to one field. Field is the path's: status, priority,
// assignee, reporter, points, summary, labels, duedate, issuetype, flag,
// sprint, parent (Text is its key, "" clears), estimate (the original, "2d 4h"), or a field id written from Kind and Value (EncodeValue's shapes).
type edit struct {
	Field  string
	ID     string // priority, assignee, reporter, issuetype, status transition; "" unassigns
	Text   string // points, summary, duedate; "" clears
	To     string // status: the name to move to
	Labels []string
	Add    []string
	Remove []string
	On     bool
	Sprint *int // 0 is the backlog
	Kind   string
	Value  jira.Value
}

func applyEdit(ctx context.Context, c *jira.Client, key string, e edit) (*edit, error) {
	if !jira.ValidKey(key) {
		return nil, badRequest(i18n.T("bad issue key"))
	}
	switch e.Field {
	case "status":
		return editStatus(ctx, c, key, e)
	case "priority", "assignee", "reporter", "points", "summary", "labels":
		return editBasic(ctx, c, key, e)
	}
	return editOther(ctx, c, key, e)
}

func editStatus(ctx context.Context, c *jira.Client, key string, e edit) (*edit, error) {
	iss, err := c.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	id := e.ID // a picked move: Jira checks it, no need to list the moves first
	if id == "" {
		trs, err := c.Transitions(ctx, key)
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(trs, func(t jira.Option) bool { return strings.EqualFold(t.Name, e.To) })
		if i < 0 {
			return nil, badRequest(i18n.Tf("no transition to %s from here", e.To))
		}
		id = trs[i].ID
	}
	if err := c.DoTransition(ctx, key, id); err != nil {
		return nil, err
	}
	return &edit{Field: "status", To: iss.Status}, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func editBasic(ctx context.Context, c *jira.Client, key string, e edit) (*edit, error) {
	iss, err := c.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	switch e.Field {
	case "priority":
		if e.ID == "" {
			return nil, badRequest(i18n.T("priority needs an ID"))
		}
		if err := c.SetPriority(ctx, key, e.ID); err != nil {
			return nil, err
		}
		return undoIf(iss.PriorityID != "", edit{Field: "priority", ID: iss.PriorityID}), nil
	case "assignee":
		if err := c.SetAssignee(ctx, key, e.ID); err != nil {
			return nil, err
		}
		return &edit{Field: "assignee", ID: iss.AssigneeAccountID}, nil
	case "reporter":
		if e.ID == "" {
			return nil, badRequest(i18n.T("reporter needs an ID"))
		}
		if err := c.SetReporter(ctx, key, e.ID); err != nil {
			return nil, err
		}
		return undoIf(iss.ReporterAccountID != "", edit{Field: "reporter", ID: iss.ReporterAccountID}), nil
	case "points":
		if err := c.SetStoryPoints(ctx, key, e.Text); err != nil {
			return nil, err
		}
		return &edit{Field: "points", Text: iss.StoryPoints}, nil
	case "summary":
		if strings.TrimSpace(e.Text) == "" {
			return nil, badRequest(i18n.T("summary can't be empty"))
		}
		if err := c.SetSummary(ctx, key, strings.TrimSpace(e.Text)); err != nil {
			return nil, err
		}
		return &edit{Field: "summary", Text: iss.Summary}, nil
	}
	// labels: replace, or add and remove leaving the others be
	if len(e.Add) > 0 || len(e.Remove) > 0 {
		if err := c.EditLabels(ctx, key, e.Add, e.Remove); err != nil {
			return nil, err
		}
		back := edit{Field: "labels"}
		for _, l := range e.Add {
			if !slices.Contains(iss.Labels, l) {
				back.Remove = append(back.Remove, l)
			}
		}
		for _, l := range e.Remove {
			if slices.Contains(iss.Labels, l) {
				back.Add = append(back.Add, l)
			}
		}
		return &back, nil
	}
	if err := c.SetLabels(ctx, key, e.Labels); err != nil {
		return nil, err
	}
	return &edit{Field: "labels", Labels: append([]string{}, iss.Labels...)}, nil
}

func undoIf(ok bool, e edit) *edit {
	if !ok {
		return nil
	}
	return &e
}

// editOther are the fields whose old value needs the issue's raw fields.
func editOther(ctx context.Context, c *jira.Client, key string, e edit) (*edit, error) {
	if e.Field == "flag" {
		was, err := c.Flagged(ctx, key)
		if err != nil {
			return nil, err
		}
		if err := c.SetFlagged(ctx, key, e.On); err != nil {
			return nil, err
		}
		return &edit{Field: "flag", On: was}, nil
	}
	ic, err := c.IssueContext(ctx, key)
	if err != nil {
		return nil, err
	}
	switch e.Field {
	case "duedate":
		var prev string
		_ = json.Unmarshal(ic.Values["duedate"], &prev)
		if err := required(ctx, c, key, "duedate", jira.Value{Text: e.Text}); err != nil {
			return nil, err
		}
		var v any
		if strings.TrimSpace(e.Text) != "" {
			d, err := jira.ParseDate(e.Text, time.Now())
			if err != nil {
				return nil, badRequest(err.Error())
			}
			v = d.Format(time.DateOnly)
		}
		if err := c.SetField(ctx, key, "duedate", v); err != nil {
			return nil, err
		}
		return &edit{Field: "duedate", Text: prev}, nil
	case "estimate":
		if secs, extra, err := jira.ParseDuration(e.Text); err != nil || secs == 0 || extra != "" {
			return nil, badRequest(i18n.T("an estimate is a time: 2d 4h, 1.5h, 45m"))
		}
		var prev struct {
			Original string `json:"originalEstimate"`
		}
		_ = json.Unmarshal(ic.Values["timetracking"], &prev)
		if err := c.SetEstimate(ctx, key, strings.TrimSpace(e.Text)); err != nil {
			return nil, err
		}
		return undoIf(prev.Original != "", edit{Field: "estimate", Text: prev.Original}), nil
	case "parent":
		var prev struct{ Key string }
		_ = json.Unmarshal(ic.Values["parent"], &prev)
		text := e.Text
		if text == "" && e.Kind != "" { // the panel's generic field editor sends Value
			text = e.Value.Text
		}
		if err := required(ctx, c, key, "parent", jira.Value{Text: text}); err != nil {
			return nil, err
		}
		var v any // no parent: cleared
		if k := strings.TrimSpace(text); k != "" {
			if !jira.ValidKey(k) {
				return nil, badRequest(i18n.T("bad parent key"))
			}
			v = map[string]string{"key": k}
		}
		if err := c.SetField(ctx, key, "parent", v); err != nil {
			return nil, err
		}
		return &edit{Field: "parent", Text: prev.Key}, nil
	case "issuetype":
		if e.ID == "" {
			return nil, badRequest(i18n.T("issuetype needs an ID"))
		}
		if err := c.SetIssueType(ctx, key, e.ID); err != nil {
			return nil, err
		}
		return undoIf(ic.TypeID != "", edit{Field: "issuetype", ID: ic.TypeID}), nil
	case "sprint":
		if e.Sprint == nil {
			return nil, badRequest(i18n.T("sprint needs a Sprint id (0 for the backlog)"))
		}
		prev, _ := currentSprint(ic.Values)
		var err error
		if *e.Sprint == 0 {
			err = c.MoveToBacklog(ctx, key)
		} else {
			err = c.MoveToSprint(ctx, *e.Sprint, key)
		}
		if err != nil {
			return nil, err
		}
		c.Invalidate(key)
		return &edit{Field: "sprint", Sprint: &prev.ID}, nil
	}
	if !strings.HasPrefix(e.Field, "customfield_") && !slices.Contains(screenFields, e.Field) {
		return nil, badRequest(i18n.Tf("unknown field %s", e.Field))
	}
	v, ok, err := jira.EncodeValue(e.Kind, e.Value)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if !ok {
		return nil, badRequest(i18n.Tf("can't write a %s field", e.Kind))
	}
	if err := required(ctx, c, key, e.Field, e.Value); err != nil {
		return nil, err
	}
	if err := c.SetField(ctx, key, e.Field, v); err != nil {
		return nil, err
	}
	return &edit{Field: e.Field, Kind: e.Kind, Value: jira.DecodeValue(e.Kind, ic.Values[e.Field])}, nil
}

// required refuses v where it would empty a field key's edit screen
// requires (editmeta), as the page does: a stale page can't skip it. When
// editmeta fails, Jira's own check stands.
func required(ctx context.Context, c *jira.Client, key, id string, v jira.Value) error {
	if !v.Clears() {
		return nil
	}
	fields, _, err := c.EditMeta(ctx, key)
	if err != nil {
		return nil
	}
	if i := slices.IndexFunc(fields, func(f jira.FieldMeta) bool { return f.ID == id }); i >= 0 && fields[i].Required && !fields[i].ReadOnly {
		return badRequest(i18n.Tf("%s is required", fields[i].Name))
	}
	return nil
}

// labelClause is a custom labels field's JQL name.
var labelClause = regexp.MustCompile(`^cf\[[0-9]+\]$`)

// screenFields are the system fields an edit screen offers that are written
// from their kind, as custom fields are.
var screenFields = []string{"duedate", "components", "fixVersions", "versions", "environment"}

// currentSprint finds the open or future sprint among an issue's raw
// fields: an array of objects holding a boardId and a state.
func currentSprint(values map[string]json.RawMessage) (jira.Sprint, bool) {
	for _, raw := range values {
		var sp []struct {
			ID      int    `json:"id"`
			Name    string `json:"name"`
			State   string `json:"state"`
			BoardID *int   `json:"boardId"`
		}
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &sp) != nil {
			continue
		}
		for _, s := range sp {
			if s.BoardID != nil && s.State != "closed" {
				return jira.Sprint{ID: s.ID, Name: s.Name, State: s.State}, true
			}
		}
	}
	return jira.Sprint{}, false
}

// users: ?issue=KEY or ?project=P narrows to who can be assigned; q filters.
func users(ctx context.Context, s *Server, r *http.Request) (any, error) {
	where := Q(r, "issue")
	if where == "" {
		where = Q(r, "project")
	}
	if where == "" {
		return nil, badRequest(i18n.T("issue or project is required"))
	}
	us, err := s.Client().AssignableUsers(ctx, where, Q(r, "q"))
	if us == nil {
		us = []jira.User{}
	}
	return us, err
}

// projectSprints are the open and future sprints of the project's first
// board that has any, for moving an issue into one.
func projectSprints(ctx context.Context, s *Server, r *http.Request) (any, error) {
	c := s.Client()
	boards, err := c.Boards(ctx, r.PathValue("project"))
	if err != nil {
		return nil, err
	}
	for _, b := range boards {
		if sp, err := c.Sprints(ctx, b.ID); err == nil && len(sp) > 0 {
			return map[string]any{"Board": b.ID, "Sprints": sp}, nil
		}
	}
	return map[string]any{"Board": 0, "Sprints": []jira.Sprint{}}, nil
}

var fieldIDRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// fieldPins are the field pins (shared with the terminal), never null.
func fieldPins(s *Server) map[string]bool {
	if s.opt.Store == nil {
		return map[string]bool{}
	}
	return jira.FieldPins(s.opt.Store)
}

// editMeta is what editing an issue needs beyond the issue itself: the
// fields its edit screen offers with their values, and the ones the panel
// has no own editor for (due date, type, sprint, flag).
func editMeta(ctx context.Context, s *Server, r *http.Request) (any, error) {
	c, key := s.Client(), r.PathValue("key")
	if !jira.ValidKey(key) {
		return nil, badRequest(i18n.T("bad issue key"))
	}
	fields, raw, own, err := c.EditMetaOwn(ctx, key)
	if err != nil {
		return nil, err
	}
	values := map[string]jira.Value{}
	for _, f := range fields {
		values[f.ID] = jira.DecodeValue(f.Kind, raw[f.ID])
	}
	var due string
	_ = json.Unmarshal(raw["duedate"], &due)
	var typ struct{ ID, Name string }
	_ = json.Unmarshal(raw["issuetype"], &typ)
	sp, _ := currentSprint(raw)
	out := map[string]any{"Fields": fields, "Values": values, "Due": due, "TypeID": typ.ID, "Type": typ.Name, "Sprint": sp, "Pins": fieldPins(s),
		"PointsField": c.StoryPointsField(ctx), "OwnRequired": own} // the panel's own rows the edit screen requires (LW-357)
	if fl, err := c.Flagged(ctx, key); err == nil {
		out["Flagged"] = fl
	}
	return out, nil
}

// moveField is a field of a move's form: a transition screen field, or one
// the workflow requires that the screen lacks.
type moveField struct {
	jira.FieldMeta
	Required bool
	Value    jira.Value
}

type moveOption struct {
	jira.TransitionMeta
	Fields     []moveField
	Message    string
	NeedsInput bool // a required field is empty, or the rules are unknown and the screen has fields
}

// transitionMeta lists the moves offered on an issue, each with the form it
// needs, the way the TUI decides whether to show one.
func transitionMeta(ctx context.Context, s *Server, r *http.Request) (any, error) {
	c, key := s.Client(), r.PathValue("key")
	if !jira.ValidKey(key) {
		return nil, badRequest(i18n.T("bad issue key"))
	}
	var (
		metas      []jira.TransitionMeta
		ic         jira.IssueContext
		mErr, iErr error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); metas, mErr = c.TransitionsMeta(ctx, key) }()
	go func() { defer wg.Done(); ic, iErr = c.IssueContext(ctx, key) }()
	wg.Wait()
	if mErr != nil {
		return nil, mErr
	}
	var rules map[string]jira.TransitionRule
	var rErr error
	if iErr == nil {
		rules, rErr = c.TransitionRules(ctx, ic.Project, ic.TypeID)
	}
	out := make([]moveOption, 0, len(metas))
	for _, t := range metas {
		rule := rules[t.ID]
		mo := moveOption{TransitionMeta: t, Message: rule.Message}
		mo.TransitionMeta.Fields = nil
		for _, fm := range t.Fields {
			mo.Fields = append(mo.Fields, moveField{FieldMeta: fm, Required: slices.Contains(rule.Required, fm.ID), Value: jira.DecodeValue(fm.Kind, ic.Values[fm.ID])})
		}
		for _, id := range rule.Required {
			if slices.ContainsFunc(mo.Fields, func(f moveField) bool { return f.ID == id }) {
				continue
			}
			fm := jira.FieldMeta{ID: id, Name: id, Kind: jira.KindOther}
			if id == jira.CommentField {
				fm.Name, fm.Kind = "Comment", jira.KindComment
			}
			mo.Fields = append(mo.Fields, moveField{FieldMeta: fm, Required: true, Value: jira.DecodeValue(fm.Kind, ic.Values[id])})
		}
		// Jira's own dialog offers a comment on every screen: a validator may want one the rules can't tell.
		if t.HasScreen && !slices.ContainsFunc(mo.Fields, func(f moveField) bool { return f.ID == jira.CommentField }) {
			mo.Fields = append(mo.Fields, moveField{FieldMeta: jira.FieldMeta{ID: jira.CommentField, Name: "Comment", Kind: jira.KindComment}})
		}
		sort.SliceStable(mo.Fields, func(i, j int) bool { return mo.Fields[i].Required && !mo.Fields[j].Required })
		for _, f := range mo.Fields {
			mo.NeedsInput = mo.NeedsInput || (f.Required && f.Value.Empty())
		}
		if (rErr != nil || iErr != nil) && t.HasScreen && len(mo.Fields) > 0 {
			mo.NeedsInput = true
		}
		out = append(out, mo)
	}
	return map[string]any{"Transitions": out}, nil
}

// fieldVal is a field to write: Kind says how Value is encoded.
type fieldVal struct {
	ID    string
	Kind  string
	Value jira.Value
}

// encodeFields is the fields to write, and the comment (Text, the people
// it mentions as Users) apart.
func encodeFields(fs []fieldVal) (map[string]any, jira.Value, error) {
	out := map[string]any{}
	var comment jira.Value
	for _, f := range fs {
		if f.Kind == jira.KindComment {
			comment = f.Value
			continue
		}
		v, ok, err := jira.EncodeValue(f.Kind, f.Value)
		if err != nil {
			return nil, jira.Value{}, badRequest(fmt.Sprintf("%s: %v", f.ID, err))
		}
		if ok {
			out[f.ID] = v
		}
	}
	return out, comment, nil
}

// commentDoc is markdown as a comment's document, each "@Name" of people a
// mention.
func commentDoc(ctx context.Context, s *Server, md string, people []jira.User) map[string]any {
	return jira.MarkdownToADFKept(s.Client().EmbedImages(ctx, md), jira.MentionNodes(people))
}

// transitionWith moves the issue writing the form's fields and comment in
// the same request, so the validators see the new values.
func transitionWith(ctx context.Context, s *Server, r *http.Request) (any, error) {
	b, err := Body[struct {
		ID     string
		Fields []fieldVal
	}](r)
	if err != nil {
		return nil, err
	}
	key := r.PathValue("key")
	if !jira.ValidKey(key) || b.ID == "" {
		return nil, badRequest(i18n.T("bad issue key or transition"))
	}
	fields, comment, err := encodeFields(b.Fields)
	if err != nil {
		return nil, err
	}
	iss, _ := s.Client().Get(ctx, key)
	var doc any
	if given(comment.Doc) {
		if d, err := jira.CheckDoc(comment.Doc); err != nil {
			return nil, badRequest(err.Error())
		} else if d != nil {
			doc = d
		}
	} else if strings.TrimSpace(comment.Text) != "" {
		doc = jira.MarkdownToADFKept(s.Client().EmbedImages(ctx, comment.Text), append(comment.Kept, jira.MentionNodes(comment.Users)...))
	}
	if err := s.Client().TransitionWithDoc(ctx, key, b.ID, fields, doc); err != nil {
		return nil, err
	}
	var undo *edit
	if iss != nil {
		undo = &edit{Field: "status", To: iss.Status}
	}
	return map[string]any{"Undo": undo}, nil
}

// cloneDraft is the create form's start for a clone of key (TUI
// openJiraClone): its project, type, "CLONE - " summary, parent and the
// description as markdown, and as it is (DescriptionDoc, for the visual
// editor); Note says when markdown can't carry it and it is copied as it is.
func cloneDraft(ctx context.Context, s *Server, r *http.Request) (any, error) {
	key, err := issueKey(r)
	if err != nil {
		return nil, err
	}
	d, err := s.Client().CloneDraft(ctx, key)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"Project": d.Project, "Type": d.Type, "Summary": d.Summary, "Parent": d.Parent, "DescriptionDoc": d.DescriptionADF}
	if ed, err := jira.EditableDescription(d.DescriptionADF); err != nil {
		out["Note"] = i18n.Tf("The description is copied from %s as it is.", key)
	} else {
		out["Description"] = ed.Markdown
	}
	return out, nil
}

// createIssue makes one issue; the frontend calls it once per summary of a
// batch. Fields are the create screen's own, written through EncodeValue.
// CloneOf starts from that issue's copy (labels, priority, components, fix
// versions, its description unless edited) and links the new one to it.
func createIssue(ctx context.Context, s *Server, r *http.Request) (any, error) {
	b, err := Body[struct {
		Project, Type, Summary, Description, Parent string
		CloneOf                                     string
		Sprint                                      int
		Fields                                      []fieldVal
		// DescriptionDoc is the description as ADF (the visual editor's), in place of Description;
		// DescriptionKept the nodes Description's placeholder lines name.
		DescriptionDoc  json.RawMessage
		DescriptionKept []json.RawMessage
	}](r)
	if err != nil {
		return nil, err
	}
	fields, _, err := encodeFields(b.Fields)
	if err != nil {
		return nil, err
	}
	c := s.Client()
	in := jira.NewIssue{Description: b.Description}
	if b.CloneOf != "" {
		if !jira.ValidKey(b.CloneOf) {
			return nil, badRequest(i18n.T("bad CloneOf key"))
		}
		if in, err = cloneInput(ctx, c, b.CloneOf, b.Description); err != nil {
			return nil, err
		}
	}
	if given(b.DescriptionDoc) {
		doc, err := jira.CheckDoc(b.DescriptionDoc)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		in.Description, in.DescriptionADF = "", doc
	} else if len(b.DescriptionKept) > 0 && strings.TrimSpace(b.Description) != "" {
		doc, err := json.Marshal(jira.MarkdownToADFKept(b.Description, b.DescriptionKept))
		if err != nil {
			return nil, err
		}
		in.Description, in.DescriptionADF = "", doc
	}
	in.Project, in.Type, in.Summary, in.Parent = b.Project, b.Type, b.Summary, b.Parent
	if in.Fields == nil {
		in.Fields = map[string]any{}
	}
	for id, v := range fields {
		in.Fields[id] = v
	}
	key, err := c.CreateIssue(ctx, in)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"Key": key}
	if b.CloneOf != "" {
		if err := c.LinkClone(ctx, key, b.CloneOf); err != nil {
			out["Warning"] = err.Error()
		}
	}
	if b.Sprint > 0 {
		if err := c.MoveToSprint(ctx, b.Sprint, key); err != nil {
			out["Warning"] = i18n.Tf("created, but not moved to the sprint: %s", err.Error())
		}
	}
	return out, nil
}

// cloneInput is key's copy with desc as its description: the original
// document while desc is what cloneDraft offered (or that was unreadable and
// desc is blank), else desc with the blocks markdown can't hold kept.
func cloneInput(ctx context.Context, c *jira.Client, key, desc string) (jira.NewIssue, error) {
	in, err := c.CloneDraft(ctx, key)
	if err != nil {
		return in, err
	}
	ed, err := jira.EditableDescription(in.DescriptionADF)
	switch {
	case err != nil && strings.TrimSpace(desc) == "":
	case err == nil && desc == ed.Markdown:
	case strings.TrimSpace(desc) == "":
		in.DescriptionADF = nil
	default:
		raw, err := json.Marshal(jira.MarkdownToADFKept(desc, ed.Kept))
		if err != nil {
			return in, err
		}
		in.DescriptionADF = raw
	}
	return in, nil
}

// bulk applies the same edit to Keys, or Each's own edit per key (an undo),
// a few at a time. The answer lists what changed, the edits that take it
// back, and why the others did not.
func bulk(ctx context.Context, s *Server, r *http.Request) (any, error) {
	b, err := Body[struct {
		Keys []string
		Edit edit
		Each map[string]edit
	}](r)
	if err != nil {
		return nil, err
	}
	keys := b.Keys
	if len(b.Each) > 0 {
		keys = keys[:0]
		for k := range b.Each {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	if len(keys) == 0 {
		return nil, badRequest(i18n.T("no issues"))
	}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		done   = []string{}
		undo   = map[string]*edit{}
		failed = map[string]string{}
		sem    = make(chan struct{}, 4)
	)
	for _, k := range keys {
		e := b.Edit
		if len(b.Each) > 0 {
			e = b.Each[k]
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			u, err := applyEdit(ctx, s.Client(), k, e)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed[k] = err.Error()
				return
			}
			done = append(done, k)
			if u != nil {
				undo[k] = u
			}
		}()
	}
	wg.Wait()
	sort.Strings(done)
	return map[string]any{"Done": done, "Undo": undo, "Failed": failed}, nil
}

// uniqueByID drops repeats: the old and new createmeta shapes can both answer.
func uniqueByID(os []jira.Option) []jira.Option {
	out := []jira.Option{}
	for _, o := range os {
		if !slices.ContainsFunc(out, func(x jira.Option) bool { return x.ID == o.ID }) {
			out = append(out, o)
		}
	}
	return out
}

// formRules is what laneway-server's form rules do to a form as it stands
// (LW-423): which fields hide, become required, narrow their options or
// take a default. Jira has none: an empty answer.
func formRules(ctx context.Context, s *Server, r *http.Request) (any, error) {
	b, err := Body[struct {
		Project, Type, Issue, Form string
		Fields                     []fieldVal
	}](r)
	if err != nil {
		return nil, err
	}
	fields, _, err := encodeFields(b.Fields)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	out, err := s.Client().FormRules(ctx, jira.FormInput{Project: b.Project, IssueType: b.Type, Issue: b.Issue, Form: b.Form, Fields: fields})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]jira.FormEffect{}
	}
	return map[string]any{"Fields": out}, nil
}
