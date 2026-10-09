package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Moving an issue with the fields its workflow asks for. Jira's transition
// screen lists the fields a move offers, but not which of them it insists on:
// that lives in the workflow's FieldRequiredValidator entries (the "Vul de code
// reviewer in" refusal), which the per-issue API never marks. TransitionRules
// walks project → workflow scheme → workflow for them, the way autojira's
// `rules update` does, and caches the result for the session.

// CommentField is the pseudo field id a validator uses for "this move needs a
// comment". It is written through the comment update, not as a field.
const CommentField = "comment"

// Field kinds a transition form knows how to fill.
const (
	KindUser    = "user"    // one person: {"accountId": …}
	KindUsers   = "users"   // several people: [{"accountId": …}]
	KindNumber  = "number"  // a float
	KindText    = "text"    // a one-line string
	KindStrings = "strings" // several words, typed apart by spaces: ["a", "b"] (labels)
	KindDate    = "date"    // a day, "2006-01-02"
	KindTime    = "time"    // a moment: a day and a clock time
	KindIssue   = "issue"   // another issue by key: {"key": …} (parent)
	KindSprint  = "sprint"  // a sprint by id (the Agile sprint field)
	KindDoc     = "doc"     // prose, written as an ADF document
	KindOption  = "option"  // one of AllowedValues: {"id": …}
	KindOptions = "options" // several of AllowedValues: [{"id": …}]
	KindComment = "comment" // the comment pseudo field
	KindOther   = "other"   // a shape the form can't write; set it in Jira
)

// FieldMeta is one field on a transition screen.
type FieldMeta struct {
	ID      string
	Name    string
	Kind    string
	Options []Option // for KindOption / KindOptions
	// Clause is the JQL name of a labels field (labels, cf[10050]), whose
	// words complete from Jira's; "" for other fields.
	Clause string
	// ReadOnly is a field the issue's edit screen has, which Jira lets no
	// one edit on this issue now (layout.go).
	ReadOnly bool `json:",omitempty"`
	// Required is a field the edit screen's field configuration (editmeta)
	// won't let be emptied; create and transition screens say their own
	// (CreateField, TransitionRule).
	Required bool `json:",omitempty"`
	// Hint is the screen's help text for the field (its description);
	// Default the value it starts with when the issue has none
	// (defaultValue). Jira rarely sends either; laneway-server's
	// workflows do.
	Hint    string          `json:",omitempty"`
	Default json.RawMessage `json:",omitempty"`
}

// TransitionMeta is one move offered on an issue, with its screen's fields.
type TransitionMeta struct {
	ID        string
	Name      string
	ToID      string
	ToName    string
	HasScreen bool
	Fields    []FieldMeta
}

// TransitionRule is what a move's validators refuse it without: the field ids
// that must hold a value, and the validators' own explanation.
type TransitionRule struct {
	Required []string
	Message  string
}

// Value is a field's current value on an issue, decoded for a form.
type Value struct {
	Text    string   // text, doc (as markdown), number
	Users   []User   // user, users
	Options []Option // option, options
	// Doc is a doc's (or comment's) ADF, the web's visual editor's, in place of Text.
	Doc json.RawMessage `json:",omitempty"`
	// Kept are the nodes Text's placeholder lines name (EditableDescription's).
	Kept []json.RawMessage `json:",omitempty"`
}

// Empty reports whether the value holds nothing.
func (v Value) Empty() bool {
	return strings.TrimSpace(v.Text) == "" && len(v.Users) == 0 && len(v.Options) == 0 && !hasContent(v.Doc)
}

// Clears reports whether writing v leaves a field empty: nothing, or the
// none a sprint offers.
func (v Value) Clears() bool {
	return v.Empty() || len(v.Options) == 1 && v.Options[0].ID == ""
}

// hasContent reports whether doc is an ADF document with content.
func hasContent(doc json.RawMessage) bool { d, err := CheckDoc(doc); return err == nil && d != nil }

// TransitionsMeta lists the moves offered on an issue, with screen fields.
func (c *Client) TransitionsMeta(ctx context.Context, key string) ([]TransitionMeta, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var resp struct {
		Transitions []struct {
			ID        string                  `json:"id"`
			Name      string                  `json:"name"`
			HasScreen bool                    `json:"hasScreen"`
			To        named                   `json:"to"`
			Fields    map[string]rawFieldMeta `json:"fields"`
		} `json:"transitions"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions?expand=transitions.fields"
	if err := c.do(ctx, http.MethodGet, path, key, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]TransitionMeta, 0, len(resp.Transitions))
	for _, t := range resp.Transitions {
		tm := TransitionMeta{ID: t.ID, Name: t.Name, ToID: t.To.ID, ToName: t.To.Name, HasScreen: t.HasScreen}
		if tm.ToName == "" {
			tm.ToName = t.Name
		}
		for id, f := range t.Fields {
			tm.Fields = append(tm.Fields, f.meta(id))
		}
		// Map order is random; keep the form stable.
		sort.Slice(tm.Fields, func(i, j int) bool { return tm.Fields[i].Name < tm.Fields[j].Name })
		out = append(out, tm)
	}
	return out, nil
}

// rawFieldMeta is a field's description as a transition screen or editmeta
// sends it.
type rawFieldMeta struct {
	Name   string `json:"name"`
	Schema struct {
		Type   string `json:"type"`
		Items  string `json:"items"`
		Custom string `json:"custom"`
		System string `json:"system"`
	} `json:"schema"`
	AllowedValues []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"allowedValues"`
	Description     string          `json:"description"`
	Required        bool            `json:"required"`
	HasDefaultValue bool            `json:"hasDefaultValue"`
	DefaultValue    json.RawMessage `json:"defaultValue"`
}

// meta is the field as a form fills it: its kind and options.
func (f rawFieldMeta) meta(id string) FieldMeta {
	fm := FieldMeta{ID: id, Name: f.Name, Hint: strings.TrimSpace(f.Description)}
	if f.HasDefaultValue && len(f.DefaultValue) > 0 && string(f.DefaultValue) != "null" {
		fm.Default = f.DefaultValue
	}
	for _, av := range f.AllowedValues {
		label := av.Name
		if label == "" {
			label = av.Value
		}
		fm.Options = append(fm.Options, Option{ID: av.ID, Name: label})
	}
	s := f.Schema
	switch {
	case id == CommentField:
		fm.Kind = KindComment
	case s.Type == "user":
		fm.Kind = KindUser
	case s.Type == "array" && s.Items == "user":
		fm.Kind = KindUsers
	case len(fm.Options) > 0 && s.Type == "array":
		fm.Kind = KindOptions
	case len(fm.Options) > 0:
		fm.Kind = KindOption
	case s.Type == "number":
		fm.Kind = KindNumber
	case s.Type == "date":
		fm.Kind = KindDate
	case s.Type == "datetime":
		fm.Kind = KindTime
	case id == "parent" || s.System == "parent":
		fm.Kind = KindIssue
	case strings.HasSuffix(s.Custom, ":gh-sprint"):
		fm.Kind = KindSprint
	case s.Type == "string" && (strings.HasSuffix(s.Custom, ":textarea") || s.System == "description" || s.System == "environment"):
		fm.Kind = KindDoc
	case s.Type == "string":
		fm.Kind = KindText
	case s.Type == "array" && s.Items == "string":
		fm.Kind = KindStrings
		switch {
		case s.System == "labels":
			fm.Clause = "labels"
		case strings.HasSuffix(s.Custom, ":labels") && strings.HasPrefix(id, "customfield_"):
			fm.Clause = "cf[" + strings.TrimPrefix(id, "customfield_") + "]"
		}
	default:
		fm.Kind = KindOther
	}
	return fm
}

// IssueContext is what deciding a move needs from the issue itself: which
// workflow governs it, and every field's raw current value.
type IssueContext struct {
	Project string
	TypeID  string
	Values  map[string]json.RawMessage
}

// IssueContext fetches an issue's project, type and field values.
func (c *Client) IssueContext(ctx context.Context, key string) (IssueContext, error) {
	if !c.Enabled() {
		return IssueContext{}, errNotConfigured
	}
	var resp struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "?fields=*all,-comment,-description"
	if err := c.do(ctx, http.MethodGet, path, key, nil, &resp); err != nil {
		return IssueContext{}, err
	}
	ic := IssueContext{Values: resp.Fields}
	var p struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(resp.Fields["project"], &p)
	ic.Project = p.Key
	var t struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(resp.Fields["issuetype"], &t)
	ic.TypeID = t.ID
	return ic, nil
}

// DecodeValue reads a raw field value as a form shows it.
func DecodeValue(kind string, raw json.RawMessage) Value {
	var v Value
	if len(raw) == 0 || string(raw) == "null" {
		return v
	}
	switch kind {
	case KindUser:
		var u user
		if json.Unmarshal(raw, &u) == nil && u.AccountID != "" {
			v.Users = []User{{AccountID: u.AccountID, DisplayName: u.DisplayName}}
		}
	case KindUsers:
		var us []user
		_ = json.Unmarshal(raw, &us)
		for _, u := range us {
			v.Users = append(v.Users, User{AccountID: u.AccountID, DisplayName: u.DisplayName})
		}
	case KindNumber:
		var f float64
		if json.Unmarshal(raw, &f) == nil {
			v.Text = strconv.FormatFloat(f, 'f', -1, 64)
		}
	case KindText, KindDate:
		_ = json.Unmarshal(raw, &v.Text)
	case KindStrings:
		var ss []string
		_ = json.Unmarshal(raw, &ss)
		v.Text = strings.Join(ss, " ")
	case KindTime:
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if t, err := time.Parse(jiraTime, s); err == nil {
				v.Text = t.Local().Format("2006-01-02 15:04")
			}
		}
	case KindIssue:
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(raw, &p)
		v.Text = p.Key
	case KindSprint:
		var sprints []struct {
			ID    int    `json:"id"`
			Name  string `json:"name"`
			State string `json:"state"`
		}
		_ = json.Unmarshal(raw, &sprints)
		for _, s := range sprints {
			if s.State != "closed" { // the sprint it is in now
				v.Options = []Option{{ID: strconv.Itoa(s.ID), Name: s.Name}}
			}
		}
	case KindDoc:
		if json.Unmarshal(raw, &v.Text) != nil {
			v.Text = adfToMarkdown(raw) // v3 returns multi-line text as ADF
		}
	case KindOption, KindOptions:
		var one struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		var many []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if json.Unmarshal(raw, &many) != nil {
			if json.Unmarshal(raw, &one) == nil && one.ID != "" {
				many = append(many, one)
			}
		}
		for _, o := range many {
			label := o.Name
			if label == "" {
				label = o.Value
			}
			v.Options = append(v.Options, Option{ID: o.ID, Name: label})
		}
	default:
		// Other shapes: non-null counts as filled.
		v.Text = strings.TrimSpace(string(raw))
	}
	return v
}

// EncodeValue is the JSON a transition writes for a field of kind, or false
// when the kind can't be written.
func EncodeValue(kind string, v Value) (any, bool, error) {
	switch kind {
	case KindUser:
		if len(v.Users) == 0 {
			return nil, true, nil
		}
		return map[string]string{"accountId": v.Users[0].AccountID}, true, nil
	case KindUsers:
		out := make([]map[string]string, 0, len(v.Users))
		for _, u := range v.Users {
			out = append(out, map[string]string{"accountId": u.AccountID})
		}
		return out, true, nil
	case KindNumber:
		s := strings.TrimSpace(v.Text)
		if s == "" {
			return nil, true, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, true, fmt.Errorf("%q is not a number", s)
		}
		return f, true, nil
	case KindText:
		return v.Text, true, nil
	case KindStrings:
		return append([]string{}, strings.Fields(v.Text)...), true, nil
	case KindDate:
		if strings.TrimSpace(v.Text) == "" {
			return nil, true, nil
		}
		d, err := ParseDate(v.Text, time.Now())
		if err != nil {
			return nil, true, err
		}
		return d.Format(time.DateOnly), true, nil
	case KindTime:
		if strings.TrimSpace(v.Text) == "" {
			return nil, true, nil
		}
		t, err := ParseDateTime(v.Text, time.Now())
		if err != nil {
			return nil, true, err
		}
		return t.Format(jiraTime), true, nil
	case KindIssue:
		if strings.TrimSpace(v.Text) == "" {
			return map[string]any{"key": nil}, true, nil
		}
		return map[string]string{"key": strings.ToUpper(strings.TrimSpace(v.Text))}, true, nil
	case KindSprint:
		if len(v.Options) == 0 || v.Options[0].ID == "" {
			return nil, true, nil
		}
		id, err := strconv.Atoi(v.Options[0].ID)
		return id, true, err
	case KindDoc:
		if len(v.Doc) > 0 && string(v.Doc) != "null" {
			d, err := CheckDoc(v.Doc)
			if err != nil || d == nil {
				return nil, true, err
			}
			return d, true, nil
		}
		if strings.TrimSpace(v.Text) == "" {
			return nil, true, nil
		}
		return MarkdownToADFKept(v.Text, append(slices.Clone(v.Kept), MentionNodes(v.Users)...)), true, nil
	case KindOption:
		if len(v.Options) == 0 {
			return nil, true, nil
		}
		return map[string]string{"id": v.Options[0].ID}, true, nil
	case KindOptions:
		out := make([]map[string]string, 0, len(v.Options))
		for _, o := range v.Options {
			out = append(out, map[string]string{"id": o.ID})
		}
		return out, true, nil
	}
	return nil, false, nil
}

// MentionNodes are users as the kept mentions MarkdownToADFKept turns
// their "@Name" into.
func MentionNodes(users []User) []json.RawMessage {
	var out []json.RawMessage
	for _, u := range users {
		if u.AccountID != "" && u.DisplayName != "" {
			out = append(out, MentionNode("mention", map[string]any{"id": u.AccountID, "text": "@" + u.DisplayName}))
		}
	}
	return out
}

// TransitionWith moves the issue along transitionID, writing fields (id →
// EncodeValue output) and adding comment in the same request — the only way
// the validators judge the new values rather than the stored ones.
func (c *Client) TransitionWith(ctx context.Context, key, transitionID string, fields map[string]any, comment string) error {
	var doc any
	if strings.TrimSpace(comment) != "" {
		doc = textToADF(comment, nil)
	}
	return c.TransitionWithDoc(ctx, key, transitionID, fields, doc)
}

// TransitionWithDoc is TransitionWith with the comment as a document
// (nil for none).
func (c *Client) TransitionWithDoc(ctx context.Context, key, transitionID string, fields map[string]any, comment any) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"transition": map[string]string{"id": transitionID}}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	if comment != nil {
		body["update"] = map[string]any{
			"comment": []any{map[string]any{"add": map[string]any{"body": comment}}},
		}
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "/transitions"
	if err := c.do(ctx, http.MethodPost, path, key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// rulesCache holds the workflow walk: per project its scheme, per workflow
// its rules by transition id. Workflows are shared across projects, so each
// is fetched once.
type rulesCache struct {
	mu        sync.Mutex
	schemes   map[string]workflowScheme
	workflows map[string]map[string]TransitionRule
}

type workflowScheme struct {
	Default string
	ByType  map[string]string // issue type id → workflow name
}

// TransitionRules returns the validator rules, by transition id, of the
// workflow governing issue type typeID in project. Cached for the session.
func (c *Client) TransitionRules(ctx context.Context, project, typeID string) (map[string]TransitionRule, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	rc := &c.rules
	rc.mu.Lock()
	scheme, ok := rc.schemes[project]
	rc.mu.Unlock()
	if !ok {
		var err error
		if scheme, err = c.fetchScheme(ctx, project); err != nil {
			return nil, err
		}
		rc.mu.Lock()
		if rc.schemes == nil {
			rc.schemes = map[string]workflowScheme{}
		}
		rc.schemes[project] = scheme
		rc.mu.Unlock()
	}
	name := scheme.ByType[typeID]
	if name == "" {
		name = scheme.Default
	}
	rc.mu.Lock()
	rules, ok := rc.workflows[name]
	rc.mu.Unlock()
	if ok {
		return rules, nil
	}
	rules, err := c.fetchWorkflowRules(ctx, name)
	if err != nil {
		return nil, err
	}
	rc.mu.Lock()
	if rc.workflows == nil {
		rc.workflows = map[string]map[string]TransitionRule{}
	}
	rc.workflows[name] = rules
	rc.mu.Unlock()
	return rules, nil
}

func (c *Client) fetchScheme(ctx context.Context, project string) (workflowScheme, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(project), "project "+project, nil, &p); err != nil {
		return workflowScheme{}, err
	}
	var resp struct {
		Values []struct {
			WorkflowScheme struct {
				DefaultWorkflow   string            `json:"defaultWorkflow"`
				IssueTypeMappings map[string]string `json:"issueTypeMappings"`
			} `json:"workflowScheme"`
		} `json:"values"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/workflowscheme/project?projectId="+url.QueryEscape(p.ID), "workflow scheme", nil, &resp); err != nil {
		return workflowScheme{}, err
	}
	if len(resp.Values) == 0 {
		return workflowScheme{}, fmt.Errorf("jira: %s has no workflow scheme", project)
	}
	ws := resp.Values[0].WorkflowScheme
	return workflowScheme{Default: ws.DefaultWorkflow, ByType: ws.IssueTypeMappings}, nil
}

func (c *Client) fetchWorkflowRules(ctx context.Context, name string) (map[string]TransitionRule, error) {
	var resp struct {
		Values []struct {
			ID struct {
				Name string `json:"name"`
			} `json:"id"`
			Transitions []struct {
				ID    string `json:"id"`
				Rules *struct {
					Validators []struct {
						Type          string          `json:"type"`
						Configuration json.RawMessage `json:"configuration"`
					} `json:"validators"`
				} `json:"rules"`
			} `json:"transitions"`
		} `json:"values"`
	}
	q := url.Values{"workflowName": {name}, "expand": {"transitions,transitions.rules"}}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/workflow/search?"+q.Encode(), "workflow "+name, nil, &resp); err != nil {
		return nil, err
	}
	out := map[string]TransitionRule{}
	for _, v := range resp.Values {
		if v.ID.Name != name {
			continue
		}
		for _, t := range v.Transitions {
			if t.Rules == nil {
				continue
			}
			var r TransitionRule
			for _, val := range t.Rules.Validators {
				if val.Type != "FieldRequiredValidator" {
					continue
				}
				var cfg struct {
					Fields       []string `json:"fields"`
					ErrorMessage string   `json:"errorMessage"`
				}
				if json.Unmarshal(val.Configuration, &cfg) != nil {
					continue
				}
				r.Required = append(r.Required, cfg.Fields...)
				if msg := strings.TrimSpace(cfg.ErrorMessage); msg != "" {
					r.Message = strings.TrimSpace(r.Message + " " + msg)
				}
			}
			if len(r.Required) > 0 {
				out[t.ID] = r
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("jira: workflow %q not found", name)
}
