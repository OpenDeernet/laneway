package jira

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEditMeta: the issue's editable fields beyond the panel's own, sorted,
// with story points, system fields and unwritable shapes left out, and
// whether the field configuration requires them.
func TestEditMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/serverInfo" {
			io.WriteString(w, `{"deploymentType":"Cloud"}`)
			return
		}
		if r.URL.Path != "/rest/api/3/issue/ABC-1" || r.URL.Query().Get("expand") != "editmeta" {
			t.Errorf("%s %s", r.Method, r.URL)
		}
		io.WriteString(w, `{"fields":{"customfield_2":{"accountId":"a1","displayName":"Ada"}},
		"editmeta":{"fields":{
		  "summary":{"name":"Summary","schema":{"type":"string","system":"summary"}},
		  "customfield_1":{"name":"Story Points","schema":{"type":"number"}},
		  "customfield_2":{"name":"Reviewer","schema":{"type":"user"}},
		  "customfield_3":{"name":"Team","required":true,"schema":{"type":"option"},"allowedValues":[{"id":"7","value":"Core"}]},
		  "customfield_4":{"name":"Due","schema":{"type":"date"}},
		  "customfield_6":{"name":"Seen","schema":{"type":"datetime"}},
		  "environment":{"name":"Environment","schema":{"type":"string","system":"environment"}}
		}}}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", StoryPointsField: "customfield_1"})
	fields, values, err := c.EditMeta(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fields {
		got = append(got, f.Name+":"+f.Kind)
	}
	want := []string{"Due:date", "Environment:doc", "Reviewer:user", "Seen:time", "Team:option"}
	if len(got) != len(want) {
		t.Fatalf("fields = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fields = %v", got)
		}
	}
	if v := DecodeValue(KindUser, values["customfield_2"]); len(v.Users) != 1 || v.Users[0].DisplayName != "Ada" {
		t.Errorf("reviewer value = %+v", v)
	}
	if fields[4].Options[0].Name != "Core" {
		t.Errorf("options = %+v", fields[4].Options)
	}
	for i, f := range fields {
		if f.Required != (i == 4) {
			t.Errorf("%s required = %v", f.Name, f.Required)
		}
	}
}

// TestEditMetaHidden: a field editmeta leaves out (hidden by the field
// configuration) is no editable field, though the issue's values or an
// earlier screen of its project and type hold it.
func TestEditMetaHidden(t *testing.T) {
	seen := `{"fields":{"project":{"key":"ABC"},"issuetype":{"id":"1"}},
		"editmeta":{"fields":{"customfield_1":{"name":"Shown","schema":{"type":"string"}},"customfield_2":{"name":"Hidden","schema":{"type":"string"}}}}}`
	now := `{"fields":{"project":{"key":"ABC"},"issuetype":{"id":"1"},"customfield_1":"x","customfield_2":"y"},
		"editmeta":{"fields":{"customfield_1":{"name":"Shown","schema":{"type":"string"}}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/issue/ABC-1" {
			io.WriteString(w, seen)
		} else {
			io.WriteString(w, now)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	c.SetLayouts(memMeta{})
	if f, _, err := c.EditMeta(context.Background(), "ABC-1"); err != nil || len(f) != 2 {
		t.Fatalf("before: %+v %v", f, err)
	}
	f, _, err := c.EditMeta(context.Background(), "ABC-2")
	if err != nil || len(f) != 1 || f[0].Name != "Shown" {
		t.Fatalf("hidden: %+v %v", f, err)
	}
}

// TestEditMetaReadByRule: on laneway-server a custom field the issue has a
// value for but editmeta lacks is one a field rule lets the person only
// read: it shows, read-only. Empty ones and system fields don't.
func TestEditMetaReadByRule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/serverInfo":
			io.WriteString(w, `{"lanewayServerVersion":"0.1.0"}`)
		case "/rest/api/3/field":
			io.WriteString(w, `[{"id":"customfield_1","name":"Shown","schema":{"type":"string"}},
			  {"id":"customfield_2","name":"Locked","schema":{"type":"string"}},
			  {"id":"customfield_3","name":"Empty","schema":{"type":"string"}},
			  {"id":"customfield_4","name":"Size","schema":{"type":"option"}},
			  {"id":"environment","name":"Environment","schema":{"type":"string","system":"environment"}}]`)
		default:
			io.WriteString(w, `{"fields":{"project":{"key":"ABC"},"issuetype":{"id":"1"},"customfield_1":"x","customfield_2":"y","customfield_3":null,
			  "customfield_4":{"id":"7","value":"Large"},"environment":"prod"},
			  "editmeta":{"fields":{"customfield_1":{"name":"Shown","schema":{"type":"string"}}}}}`)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	fields, _, err := c.EditMeta(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fields {
		got = append(got, fmt.Sprintf("%s:%s:%v", f.Name, f.Kind, f.ByRule && f.ReadOnly))
	}
	if strings.Join(got, " ") != "Locked:text:true Shown:text:false Size:option:true" {
		t.Errorf("fields %v", got)
	}
}

// TestSetField: one field in a PUT, nil clears.
func TestSetField(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if err := c.SetField(context.Background(), "ABC-1", "customfield_3", nil); err != nil {
		t.Fatal(err)
	}
	if body != `{"fields":{"customfield_3":null}}` {
		t.Errorf("body = %s", body)
	}
}

// TestEditLabels: add and remove through update verbs.
func TestEditLabels(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if err := c.EditLabels(context.Background(), "ABC-1", []string{"ui"}, []string{"old"}); err != nil {
		t.Fatal(err)
	}
	if body != `{"update":{"labels":[{"add":"ui"},{"remove":"old"}]}}` {
		t.Errorf("body = %s", body)
	}
}

// TestNewKinds: parent, sprint and date-time fields read and write.
func TestNewKinds(t *testing.T) {
	sprint := DecodeValue(KindSprint, []byte(`[{"id":40,"name":"S40","state":"closed"},{"id":41,"name":"S41","state":"active"}]`))
	if len(sprint.Options) != 1 || sprint.Options[0].Name != "S41" {
		t.Errorf("sprint = %+v", sprint)
	}
	if v, _, err := EncodeValue(KindSprint, sprint); err != nil || v != 41 {
		t.Errorf("sprint encodes %v, %v", v, err)
	}
	if v := DecodeValue(KindIssue, []byte(`{"key":"ABC-5","fields":{}}`)); v.Text != "ABC-5" {
		t.Errorf("parent = %+v", v)
	}
	if v, _, _ := EncodeValue(KindIssue, Value{Text: "abc-6"}); v.(map[string]string)["key"] != "ABC-6" {
		t.Errorf("parent encodes %v", v)
	}
	v, _, err := EncodeValue(KindTime, Value{Text: "2026-10-01 09:30"})
	if err != nil || !strings.HasPrefix(v.(string), "2026-10-01T09:30:00.000") {
		t.Errorf("time encodes %v, %v", v, err)
	}
}

// TestLabelsClause: labels fields name their JQL clause for completion.
func TestLabelsClause(t *testing.T) {
	var f rawFieldMeta
	f.Schema.Type, f.Schema.Items, f.Schema.Custom = "array", "string", "com.atlassian.jira.plugin.system.customfieldtypes:labels"
	if fm := f.meta("customfield_10050"); fm.Kind != KindStrings || fm.Clause != "cf[10050]" {
		t.Errorf("custom labels: %+v", fm)
	}
	f.Schema.Custom, f.Schema.System = "", "labels"
	if fm := f.meta("labels"); fm.Clause != "labels" {
		t.Errorf("labels: %+v", fm)
	}
	f.Schema.System = ""
	if fm := f.meta("customfield_1"); fm.Clause != "" {
		t.Errorf("a plain string list: %+v", fm)
	}
}

// The panel's own fields the screen requires come back apart (LW-357).
func TestEditMetaOwnRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/serverInfo" {
			io.WriteString(w, `{"deploymentType":"Cloud"}`)
			return
		}
		io.WriteString(w, `{"fields":{},"editmeta":{"fields":{
		  "summary":{"name":"Summary","required":true,"schema":{"type":"string","system":"summary"}},
		  "assignee":{"name":"Assignee","required":true,"schema":{"type":"user","system":"assignee"}},
		  "labels":{"name":"Labels","schema":{"type":"array","system":"labels"}},
		  "customfield_1":{"name":"Story Points","required":true,"schema":{"type":"number"}},
		  "customfield_3":{"name":"Team","required":true,"schema":{"type":"option"},"allowedValues":[{"id":"7","value":"Core"}]}
		}}}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", StoryPointsField: "customfield_1"})
	fields, _, own, err := c.EditMetaOwn(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(own) != "[assignee customfield_1 summary]" || len(fields) != 1 || !fields[0].Required {
		t.Errorf("own %v, fields %+v", own, fields)
	}
}
