package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sort"
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
// can't write are left out, as are those editmeta lacks (hidden ones). Where Jira offers no edit
// screen (a closed issue), the one last seen for its project and type comes
// back read-only (layout.go).
func (c *Client) EditMeta(ctx context.Context, key string) ([]FieldMeta, map[string]json.RawMessage, error) {
	if !c.Enabled() {
		return nil, nil, errNotConfigured
	}
	var resp struct {
		Fields   map[string]json.RawMessage `json:"fields"`
		EditMeta struct {
			Fields map[string]rawFieldMeta `json:"fields"`
		} `json:"editmeta"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key) + "?fields=*all,-comment,-description&expand=editmeta"
	if err := c.do(ctx, http.MethodGet, path, key, nil, &resp); err != nil {
		return nil, nil, err
	}
	skip := append(slices.Clone(panelFieldIDs), c.resolveStoryPointFields(ctx)...)
	var out []FieldMeta
	for id, f := range resp.EditMeta.Fields {
		if slices.Contains(skip, id) {
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
		return c.layout(project.Key, typ.ID), resp.Fields, nil
	}
	c.keepLayout(project.Key, typ.ID, out)
	return out, resp.Fields, nil
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
