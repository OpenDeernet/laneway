package jira

import (
	"context"
	"errors"
	"net/http"
)

// FormEffect is what laneway-server's form rules do to one field of a
// form, as the values hold now.
type FormEffect struct {
	Hidden   bool     `json:"hidden,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"` // nil: any
	Default  string   `json:"default,omitempty"`
	Message  string   `json:"message,omitempty"`
}

// FormInput is a form as it stands: a new issue's project and type, or
// the issue being edited or moved (form: create, edit or transition).
type FormInput struct {
	Project   string         `json:"project,omitempty"`
	IssueType string         `json:"issueType,omitempty"`
	Issue     string         `json:"issue,omitempty"`
	Form      string         `json:"form,omitempty"`
	Fields    map[string]any `json:"fields"`
}

// FormRules asks laneway-server what its form rules do to a form; nil,
// nil on Jira, which has none.
func (c *Client) FormRules(ctx context.Context, in FormInput) (map[string]FormEffect, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	if is, _ := c.Laneway(ctx); !is {
		return nil, nil
	}
	var out struct {
		Fields map[string]FormEffect `json:"fields"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/laneway/1/form", "form rules", in, &out); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return out.Fields, nil
}
