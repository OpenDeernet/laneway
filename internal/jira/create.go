package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// Creating issues, for the thread-to-ticket action (internal/ui/threadticket.go).
// Only what a create needs: the projects the user may create in, a project's
// issue types, the labels a project already uses (so a draft sticks to the
// team's vocabulary instead of inventing new ones), and the create itself.

// Project is a Jira project the user can create issues in.
type Project struct {
	Key  string
	Name string
}

// NewIssue is a draft to create. Type is the issue type's name ("Bug");
// Description is plain text (see textToADF). Labels may be empty.
type NewIssue struct {
	Project     string
	Type        string
	Summary     string
	Description string
	Labels      []string
	// Parent is the epic or, for a subtask, the issue it sits under.
	Parent string
	// DescriptionADF replaces Description with a ready document (a clone's).
	DescriptionADF json.RawMessage
	Priority       string // a priority id, "" for the default
	// Mentions are the people "@Name" in the description means, made
	// mentions that notify them.
	Mentions []Mention
	// Fields are more fields to set, id → EncodeValue output (a required
	// Component the create screen asks for).
	Fields map[string]any
}

// Projects lists the projects the user can create issues in, up to 100.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var resp struct {
		Values []struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"values"`
	}
	path := "/rest/api/3/project/search?action=create&maxResults=100"
	if err := c.do(ctx, http.MethodGet, path, "projects", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(resp.Values))
	for _, p := range resp.Values {
		out = append(out, Project{Key: p.Key, Name: p.Name})
	}
	return out, nil
}

// IssueTypes lists the non-subtask issue types creatable in project.
func (c *Client) IssueTypes(ctx context.Context, project string) ([]Option, error) {
	return c.issueTypes(ctx, project, false)
}

// SubtaskTypes lists the project's subtask issue types.
func (c *Client) SubtaskTypes(ctx context.Context, project string) ([]Option, error) {
	return c.issueTypes(ctx, project, true)
}

func (c *Client) issueTypes(ctx context.Context, project string, subtask bool) ([]Option, error) {
	types, err := c.allIssueTypes(ctx, project)
	var out []Option
	for _, t := range types {
		if t.Subtask == subtask {
			out = append(out, t.Option)
		}
	}
	return out, err
}

// TypesLike lists the project's issue types an issue of type current can
// change to: subtask types for a subtask, else the others, current left out.
func (c *Client) TypesLike(ctx context.Context, project, current string) ([]Option, error) {
	types, err := c.allIssueTypes(ctx, project)
	sub := slices.ContainsFunc(types, func(t issueType) bool { return t.Subtask && strings.EqualFold(t.Name, current) })
	var out []Option
	for _, t := range types {
		if t.Subtask == sub && !strings.EqualFold(t.Name, current) {
			out = append(out, t.Option)
		}
	}
	return out, err
}

// SetIssueType changes key's issue type to typeID.
func (c *Client) SetIssueType(ctx context.Context, key, typeID string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body := map[string]any{"fields": map[string]any{"issuetype": map[string]string{"id": typeID}}}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), key, body, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// issueType is an issue type creatable in a project.
type issueType struct {
	Option
	Subtask bool
}

// allIssueTypes lists the issue types creatable in project, subtasks too.
func (c *Client) allIssueTypes(ctx context.Context, project string) ([]issueType, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	type rawType struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Subtask bool   `json:"subtask"`
		IconURL string `json:"iconUrl"`
	}
	var resp struct {
		IssueTypes []rawType `json:"issueTypes"`
		Values     []rawType `json:"values"` // older createmeta shape
	}
	path := "/rest/api/3/issue/createmeta/" + url.PathEscape(project) + "/issuetypes"
	if err := c.do(ctx, http.MethodGet, path, project, nil, &resp); err != nil {
		return nil, err
	}
	var out []issueType
	for _, t := range append(resp.IssueTypes, resp.Values...) {
		out = append(out, issueType{Option: Option{ID: t.ID, Name: t.Name, TypeKind: TypeKind(t.IconURL)}, Subtask: t.Subtask})
	}
	return out, nil
}

// CreateField is a field on a create screen and whether Jira insists on it
// without a default of its own.
type CreateField struct {
	FieldMeta
	Required bool
}

// CreateFields lists the create screen's fields for the issue type named
// typ in project (createmeta).
func (c *Client) CreateFields(ctx context.Context, project, typ string) ([]CreateField, error) {
	types, err := c.allIssueTypes(ctx, project)
	if err != nil {
		return nil, err
	}
	id := ""
	for _, t := range types {
		if strings.EqualFold(t.Name, typ) {
			id = t.ID
		}
	}
	if id == "" {
		return nil, fmt.Errorf("jira: no issue type %q in %s", typ, project)
	}
	type field struct {
		rawFieldMeta
		FieldID         string `json:"fieldId"`
		HasDefaultValue bool   `json:"hasDefaultValue"`
	}
	var resp struct {
		Fields  []field `json:"fields"`
		Results []field `json:"results"`
		Values  []field `json:"values"`
	}
	path := "/rest/api/3/issue/createmeta/" + url.PathEscape(project) + "/issuetypes/" + url.PathEscape(id) + "?maxResults=200"
	if err := c.do(ctx, http.MethodGet, path, project, nil, &resp); err != nil {
		return nil, err
	}
	var out []CreateField
	for _, f := range append(append(resp.Fields, resp.Results...), resp.Values...) {
		out = append(out, CreateField{FieldMeta: f.meta(f.FieldID), Required: f.Required && !f.HasDefaultValue})
	}
	return out, nil
}

// TypeStatuses is an issue type's statuses in a project.
type TypeStatuses struct {
	Type     string
	Subtask  bool
	Statuses []ProjectStatus
}

// ProjectStatus is a status and its category key: new, indeterminate or
// done.
type ProjectStatus struct{ Name, Category string }

// ProjectStatuses lists project's issue types with the statuses each can
// have.
func (c *Client) ProjectStatuses(ctx context.Context, project string) ([]TypeStatuses, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var resp []struct {
		Name     string `json:"name"`
		Subtask  bool   `json:"subtask"`
		Statuses []struct {
			Name     string `json:"name"`
			Category struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"statuses"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(project)+"/statuses", project, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]TypeStatuses, len(resp))
	for i, t := range resp {
		out[i] = TypeStatuses{Type: t.Name, Subtask: t.Subtask}
		for _, s := range t.Statuses {
			out[i].Statuses = append(out[i].Statuses, ProjectStatus{Name: s.Name, Category: s.Category.Key})
		}
	}
	return out, nil
}

// RecentLabels returns the labels on the project's 50 most recent labelled
// issues, most used first.
func (c *Client) RecentLabels(ctx context.Context, project string) ([]string, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	body := map[string]any{
		"jql":        fmt.Sprintf("project = %q AND labels is not EMPTY ORDER BY created DESC", project),
		"fields":     []string{"labels"},
		"maxResults": 50,
	}
	var resp struct {
		Issues []struct {
			Fields struct {
				Labels []string `json:"labels"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", project+" labels", body, &resp); err != nil {
		return nil, err
	}
	count := map[string]int{}
	var order []string
	for _, is := range resp.Issues {
		for _, l := range is.Fields.Labels {
			if count[l] == 0 {
				order = append(order, l)
			}
			count[l]++
		}
	}
	// Stable insertion sort by count: the list is short.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && count[order[j]] > count[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	return order, nil
}

// CreateIssue creates the issue and returns its key.
func (c *Client) CreateIssue(ctx context.Context, in NewIssue) (string, error) {
	if !c.Enabled() {
		return "", errNotConfigured
	}
	in.Project = strings.TrimSpace(in.Project)
	in.Summary = strings.TrimSpace(in.Summary)
	if in.Project == "" || in.Type == "" || in.Summary == "" {
		return "", fmt.Errorf("jira: project, type and summary are required")
	}
	fields := map[string]any{
		"project":   map[string]string{"key": in.Project},
		"issuetype": map[string]string{"name": in.Type},
		"summary":   in.Summary,
	}
	if strings.TrimSpace(in.Description) != "" {
		doc := MarkdownToADF(in.Description)
		inlineMentions(doc, in.Mentions)
		fields["description"] = doc
	}
	if len(in.DescriptionADF) > 0 && string(in.DescriptionADF) != "null" {
		var doc map[string]any
		if len(in.Mentions) > 0 && json.Unmarshal(in.DescriptionADF, &doc) == nil {
			inlineMentions(doc, in.Mentions)
			fields["description"] = doc
		} else {
			fields["description"] = in.DescriptionADF
		}
	}
	if len(in.Labels) > 0 {
		fields["labels"] = in.Labels
	}
	if in.Parent != "" {
		fields["parent"] = map[string]string{"key": in.Parent}
	}
	if in.Priority != "" {
		fields["priority"] = map[string]string{"id": in.Priority}
	}
	for id, v := range in.Fields {
		fields[id] = v
	}
	var resp struct {
		Key string `json:"key"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/issue", in.Project, map[string]any{"fields": fields}, &resp); err != nil {
		return "", err
	}
	if resp.Key == "" {
		return "", fmt.Errorf("jira: create returned no key")
	}
	return resp.Key, nil
}
