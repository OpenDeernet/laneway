package jira

import (
	"encoding/json"
	"slices"
	"strings"
)

// Which fields an issue type's edit screen has follows its project and type
// (the screen scheme), not the issue: Jira serves it per issue (editmeta),
// and serves none at all where the workflow locks the issue (a closed one)
// or the user may not edit it. The last screen seen per project and type is
// kept, so those issues still show their fields, read-only.

// MetaStore keeps small values by key: the state file (internal/store).
type MetaStore interface {
	GetMeta(key string) (string, bool, error)
	SetMeta(key, value string) error
}

// SetLayouts gives the client somewhere to keep the edit screens it saw.
func (c *Client) SetLayouts(s MetaStore) { c.layouts = s }

func layoutKey(project, typeID string) string { return "layout:" + project + ":" + typeID }

// projectFieldsKey holds every field id seen on project's screens, which
// an issue fetch asks for: its values come with the issue (Issue.Screen).
func projectFieldsKey(project string) string { return "layout:" + project }

// keepLayout remembers fields as project's typeID's edit screen; their
// options are left out, as a read-only field needs none.
func (c *Client) keepLayout(project, typeID string, fields []FieldMeta) {
	if c.layouts == nil || project == "" || typeID == "" || len(fields) == 0 {
		return
	}
	slim := make([]FieldMeta, len(fields))
	for i, f := range fields {
		slim[i] = FieldMeta{ID: f.ID, Name: f.Name, Kind: f.Kind, Clause: f.Clause, Required: f.Required}
	}
	b, err := json.Marshal(slim)
	if err != nil {
		return
	}
	if old, ok, _ := c.layouts.GetMeta(layoutKey(project, typeID)); !ok || old != string(b) {
		_ = c.layouts.SetMeta(layoutKey(project, typeID), string(b)) // a convenience: failing it only forgets
	}
	ids := c.projectFields(project)
	n := len(ids)
	for _, f := range fields {
		if !slices.Contains(ids, f.ID) {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) > n {
		b, _ := json.Marshal(ids)
		_ = c.layouts.SetMeta(projectFieldsKey(project), string(b))
	}
}

// projectFields are the field ids seen on project's screens.
func (c *Client) projectFields(project string) []string {
	if c.layouts == nil {
		return nil
	}
	v, ok, err := c.layouts.GetMeta(projectFieldsKey(project))
	var ids []string
	if err != nil || !ok || json.Unmarshal([]byte(v), &ids) != nil {
		return nil
	}
	return slices.DeleteFunc(ids, func(id string) bool { return !fieldIDOK(id) })
}

// fieldIDOK is an id safe in a ?fields= list.
func fieldIDOK(id string) bool {
	return id != "" && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	}) < 0
}

// screen is the issue's edit screen as last seen for its project and type,
// with its values from the issue's own fields (raw): the panel draws them
// at once, before editmeta says what may be edited.
func (c *Client) screen(project, typeID string, raw map[string]json.RawMessage) ([]FieldMeta, map[string]Value) {
	fields := c.layout(project, typeID)
	if len(fields) == 0 {
		return nil, nil
	}
	vals := map[string]Value{}
	for i, f := range fields {
		fields[i].ReadOnly = false // unknown till editmeta
		vals[f.ID] = DecodeValue(f.Kind, raw[f.ID])
	}
	return fields, vals
}

// layout is the edit screen last seen for project's typeID, read-only.
func (c *Client) layout(project, typeID string) []FieldMeta {
	if c.layouts == nil {
		return nil
	}
	v, ok, err := c.layouts.GetMeta(layoutKey(project, typeID))
	if err != nil || !ok {
		return nil
	}
	var fields []FieldMeta
	if json.Unmarshal([]byte(v), &fields) != nil {
		return nil
	}
	for i := range fields {
		fields[i].ReadOnly = true
	}
	return fields
}
