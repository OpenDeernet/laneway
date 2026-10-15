package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostedRoutesClassified: every route is in hosted-routes.txt, on or
// off, and the file names no route that doesn't exist; a new route
// fails here until it is classified (unlisted ones are off).
func TestHostedRoutesClassified(t *testing.T) {
	listed := map[string]bool{}
	for _, l := range strings.Split(hostedRoutesTxt, "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		state, p, ok := strings.Cut(l, " ")
		if !ok || (state != "on" && state != "off") {
			t.Errorf("hosted-routes.txt: %q is not on/off and a route", l)
			continue
		}
		listed[strings.TrimSpace(p)] = true
	}
	seen := map[string]bool{}
	for _, r := range routes {
		seen[r.pattern] = true
		if !listed[r.pattern] {
			t.Errorf("route %s isn't in hosted-routes.txt: add it as on (serves the person through their Jira client) or off (acts as this machine)", r.pattern)
		}
	}
	for p := range listed {
		if !seen[p] {
			t.Errorf("hosted-routes.txt lists %s, which is no route", p)
		}
	}
	// What acts as this machine's user is off.
	for _, p := range []string{"POST /api/actions/{id}/run", "POST /api/issues/{key}/ask", "GET /api/agents/{pane}/terminal", "POST /api/issues/{key}/work",
		"POST /api/issues/{key}/pr", "GET /api/worklog/proposals", "GET /api/branch", "PUT /api/autostart", "POST /api/setup", "POST /api/sites", "GET /api/gitlab/mr"} {
		if hostedOn[p] {
			t.Errorf("%s is on for a hosted app", p)
		}
	}
}

func TestHostedGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("ui: {}\n"), 0o600)
	s := New(t.Context(), Options{Hosted: true, ConfigPath: path})
	ts := httptest.NewServer(s)
	defer ts.Close()
	call := func(method, p, body string) int {
		req, _ := http.NewRequest(method, ts.URL+p, bytes.NewBufferString(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for _, p := range [][2]string{{"POST", "/api/actions/a/run"}, {"GET", "/api/branch"}, {"POST", "/api/issues/ABC-1/ask"}, {"GET", "/api/agents"}, {"POST", "/api/setup"}} {
		if c := call(p[0], p[1], "{}"); c != http.StatusForbidden {
			t.Errorf("%s %s on a hosted app: %d", p[0], p[1], c)
		}
	}
	for _, name := range []string{"llm", "actions", "work_agent", "calendar", "download_dir"} {
		if c := call("PUT", "/api/settings/"+name, `{"Value":"x"}`); c != http.StatusForbidden {
			t.Errorf("setting %s on a hosted app: %d", name, c)
		}
	}
	if c := call("PUT", "/api/settings/stale_days", `{"Value":5}`); c != http.StatusOK && c != http.StatusNoContent {
		t.Errorf("an ordinary setting on a hosted app: %d", c)
	}
	// The settings screen lists neither those nor the terminal's own.
	res, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for _, name := range []string{"llm", "calendar", "agent_view", "theme"} {
		if bytes.Contains(body, []byte(`"Name":"`+name+`"`)) {
			t.Errorf("a hosted app lists %s", name)
		}
	}
	if !bytes.Contains(body, []byte(`"Name":"stale_days"`)) {
		t.Error("a hosted app lists no stale_days")
	}
	// Not hosted: the gate is open.
	plain := New(t.Context(), Options{})
	if hostedGate(plain, "POST /api/actions/{id}/run", httptest.NewRecorder()) {
		t.Error("the gate closed for a local app")
	}
}
