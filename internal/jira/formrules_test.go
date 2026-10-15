package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// On laneway-server the form's rules come back; Jira has none (LW-423).
func TestFormRules(t *testing.T) {
	for _, lw := range []bool{true, false} {
		var asked map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rest/api/3/serverInfo":
				if lw {
					io.WriteString(w, `{"deploymentType":"Server","lanewayServerVersion":"1"}`)
				} else {
					io.WriteString(w, `{"deploymentType":"Cloud"}`)
				}
			case "/rest/laneway/1/form":
				json.NewDecoder(r.Body).Decode(&asked)
				io.WriteString(w, `{"fields":{"duedate":{"required":true,"message":"Highest needs a due date"}}}`)
			default:
				http.NotFound(w, r)
			}
		}))
		c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
		got, err := c.FormRules(context.Background(), FormInput{Project: "ABC", IssueType: "Task", Form: "create", Fields: map[string]any{"priority": map[string]string{"id": "1"}}})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if lw && (!got["duedate"].Required || asked["project"] != "ABC" || asked["form"] != "create") {
			t.Errorf("laneway: %+v, asked %v", got, asked)
		}
		if !lw && (got != nil || asked != nil) {
			t.Errorf("Jira: %+v, asked %v", got, asked)
		}
	}
}
