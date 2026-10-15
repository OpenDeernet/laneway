package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
)

// Editing any field of an issue: editmeta lists the fields the issue's edit
// screen offers, with the same shapes a transition screen uses.

// panelFieldIDs are the fields the panel edits with their own editors.
var panelFieldIDs = []string{
	"summary", "status", "priority", "assignee", "labels", "description",
	"comment", "issuetype", "project", "reporter", "attachment", "issuelinks",
}

// EditMeta lists the issue's editable fields beyond the panel's own (summary,
// priority, points, …), sorted by name, with their current values and
// whether the field configuration requires them. Fields of a shape the form
// can't write are left out, as are those editmeta lacks (hidden ones); on
// laneway-server a custom field editmeta lacks but the issue has a value
// for is one a field rule lets the person only read, and comes back
// read-only (ByRule). Where Jira offers no edit screen (a closed issue),
// the one last seen for its project and type comes back read-only
// (layout.go).
func (c *Client) EditMeta(ctx context.Context, key string) ([]FieldMeta, map[string]json.RawMessage, error) {
	fields, raw, _, err := c.EditMetaOwn(ctx, key)
	return fields, raw, err
}

// EditMetaOwn is EditMeta, and also the panel's own fields the edit
// screen requires (assignee, labels, a story points field, …), which
// EditMeta leaves out.
func (c *Client) EditMetaOwn(ctx context.Context, key string) ([]FieldMeta, map[string]json.RawMessage, []string, error) {
	if !c.Enabled() {
		return nil, nil, nil, errNotConfigured
	}
	var resp struct {
		Fields   map[string]json.RawMessage `json:"fields"`
		EditMeta struct {
			Fields map[string]rawFieldMeta `json:"fields"`
		} `json:"editmeta"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "?fields=*all,-comment,-description&expand=editmeta"
	if err := c.do(ctx, http.MethodGet, path, key, nil, &resp); err != nil {
		return nil, nil, nil, err
	}
	skip := append(slices.Clone(panelFieldIDs), c.resolveStoryPointFields(ctx)...)
	var out []FieldMeta
	var own []string
	for id, f := range resp.EditMeta.Fields {
		if slices.Contains(skip, id) {
			if f.Required {
				own = append(own, id)
			}
			continue
		}
		if fm := f.meta(id); fm.Kind != KindOther && fm.Kind != KindComment {
			fm.Required = f.Required
			out = append(out, fm)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	var project struct{ Key string }
	var typ struct{ ID string }
	_ = json.Unmarshal(resp.Fields["project"], &project)
	_ = json.Unmarshal(resp.Fields["issuetype"], &typ)
	if len(resp.EditMeta.Fields) == 0 {
		return c.layout(project.Key, typ.ID), resp.Fields, nil, nil
	}
	c.keepLayout(project.Key, typ.ID, out)
	if is, _ := c.Laneway(ctx); is {
		out = append(out, c.readByRule(ctx, resp.EditMeta.Fields, resp.Fields, skip)...)
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	}
	sort.Strings(own)
	return out, resp.Fields, own, nil
}

// readByRule are the custom fields with a value that editmeta left out:
// laneway-server drops those a field rule lets the person only read, and
// leaves hidden ones out of the issue too.
func (c *Client) readByRule(ctx context.Context, edit map[string]rawFieldMeta, vals map[string]json.RawMessage, skip []string) []FieldMeta {
	fields, err := c.fieldMeta(ctx)
	if err != nil {
		return nil
	}
	var out []FieldMeta
	for _, f := range fields {
		raw := vals[f.ID]
		if _, ok := edit[f.ID]; ok || !strings.HasPrefix(f.ID, "customfield_") || slices.Contains(skip, f.ID) || emptyValue(raw) {
			continue
		}
		rf := rawFieldMeta{Name: f.Name}
		rf.Schema.Type, rf.Schema.Items, rf.Schema.Custom, rf.Schema.System = f.Schema.Type, f.Schema.Items, f.Schema.Custom, f.Schema.System
		fm := rf.meta(f.ID)
		// Without allowed values meta can't tell an option field.
		switch {
		case fm.Kind == KindOther && f.Schema.Type == "option":
			fm.Kind = KindOption
		case fm.Kind == KindOther && f.Schema.Type == "array" && f.Schema.Items == "option":
			fm.Kind = KindOptions
		}
		if fm.Kind == KindOther || fm.Kind == KindComment {
			continue
		}
		fm.ReadOnly, fm.ByRule = true, true
		out = append(out, fm)
	}
	return out
}

// emptyValue is a field value that says nothing: none, null, "", [] or {}.
func emptyValue(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", `""`, "[]", "{}":
		return true
	}
	return false
}

// SetField writes one field, v as EncodeValue returns it (nil clears).
func (c *Client) SetField(ctx context.Context, key, id string, v any) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"fields": map[string]any{id: v}}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}
