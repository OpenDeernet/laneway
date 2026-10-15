package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI: list as plain, CSV and JSON; view; create; move by status name.
func TestCLI(t *testing.T) {
	var moved string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/search/jql":
			io.WriteString(w, `{"issues":[{"key":"ABC-1","fields":{"summary":"Fix, login","status":{"name":"To Do"},"issuetype":{"name":"Bug"},"assignee":{"displayName":"Ada"}}}]}`)
		case r.URL.Path == "/rest/api/3/issue" && r.Method == http.MethodPost:
			io.WriteString(w, `{"key":"ABC-2"}`)
		case r.URL.Path == "/rest/api/3/issue/ABC-1/transitions" && r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			moved = string(b)
		case r.URL.Path == "/rest/api/3/issue/ABC-1/transitions":
			io.WriteString(w, `{"transitions":[{"id":"31","to":{"name":"Done"}}]}`)
		case r.URL.Path == "/rest/api/3/issue/ABC-1":
			io.WriteString(w, `{"key":"ABC-1","fields":{"summary":"Fix, login","status":{"name":"To Do"},"issuetype":{"name":"Bug"}}}`)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	defer srv.Close()
	p := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(p, []byte("jira: {base_url: \""+srv.URL+"\", email: me@x.test, api_token: tok}\n"), 0o600)
	run := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := subcommand(args, p, "", &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d, %s", args, code, errOut.String())
		}
		return out.String()
	}
	if got := run("list"); !strings.Contains(got, "ABC-1  To Do  Ada  Fix, login") {
		t.Errorf("plain: %q", got)
	}
	if got := run("list", "-format", "csv"); !strings.Contains(got, `ABC-1,"Fix, login",To Do,Bug,Ada`) {
		t.Errorf("csv: %q", got)
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(run("list", "-format", "json", "-jql", "project = ABC")), &rows); err != nil || rows[0]["key"] != "ABC-1" {
		t.Errorf("json: %v %v", rows, err)
	}
	if got := run("view", "ABC-1"); !strings.HasPrefix(got, "ABC-1  Fix, login") || !strings.Contains(got, "Status    To Do") {
		t.Errorf("view: %q", got)
	}
	if got := run("create", "-project", "ABC", "-summary", "New"); got != "ABC-2\n" {
		t.Errorf("create: %q", got)
	}
	if got := run("move", "ABC-1"); got != "Done\n" {
		t.Errorf("move without a status: %q", got)
	}
	if got := run("move", "ABC-1", "done"); got != "ABC-1 → Done\n" || !strings.Contains(moved, `"31"`) {
		t.Errorf("move: %q, %q", got, moved)
	}
	var out, errOut bytes.Buffer
	if code := subcommand([]string{"move", "ABC-1", "Nope"}, p, "", &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "it can to Done") {
		t.Errorf("a bad status: exit %d, %q", code, errOut.String())
	}
}
