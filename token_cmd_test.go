package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/demo"
)

// demoConfig is a config file on a fresh demo, laneway-server's when laneway.
func demoConfig(t *testing.T, laneway bool) (string, *demo.Server) {
	s := demo.New(time.Now())
	if laneway {
		s.Laneway()
	}
	base, stop, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	p := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(p, []byte("jira: {base_url: \""+base+"\", email: demo@example.com, api_token: demo}\n"), 0o600)
	return p, s
}

// TestTokenCmd: tokens listed, made, revoked one and all; apps listed and
// revoked.
func TestTokenCmd(t *testing.T) {
	p, s := demoConfig(t, true)
	run := func(args ...string) (string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := subcommand(args, p, "", &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d, %s", args, code, errOut.String())
		}
		return out.String(), errOut.String()
	}
	if got, _ := run("token", "list"); !strings.Contains(got, "1  CI pipeline  read:jira-work") || !strings.Contains(got, "old laptop   everything") || !strings.Contains(got, "never used") {
		t.Errorf("list: %q", got)
	}
	got, note := run("token", "create", "script", "-days", "7")
	if !strings.HasPrefix(got, "lws_") || strings.Count(got, "\n") != 1 || !strings.Contains(note, "shows only once") ||
		!strings.Contains(note, time.Now().AddDate(0, 0, 7).Format(time.DateOnly)) {
		t.Errorf("create: %q %q", got, note)
	}
	if got, _ := run("token", "revoke", "1"); got != "revoked 1\n" {
		t.Errorf("revoke: %q", got)
	}
	if got, _ := run("token", "list"); strings.Contains(got, "CI pipeline") || !strings.Contains(got, "script") {
		t.Errorf("after revoke: %q", got)
	}
	run("token", "revoke-all")
	if got, _ := run("token", "list"); got != "" {
		t.Errorf("after revoke-all: %q", got)
	}
	if got, _ := run("apps", "list"); strings.Count(got, "laneway (this app)") != 1 || strings.Count(got, "laneway") != 2 || !strings.Contains(got, "Calendar sync") {
		t.Errorf("apps: %q", got)
	}
	run("apps", "revoke", "2")
	if got, _ := run("apps", "list"); strings.Contains(got, "Calendar sync") {
		t.Errorf("apps after revoke: %q", got)
	}
	var out, errOut bytes.Buffer
	if code := subcommand([]string{"token", "revoke", "99"}, p, "", &out, &errOut); code != 1 {
		t.Errorf("an unknown token: exit %d, %q", code, errOut.String())
	}
	if code := subcommand([]string{"token", "create"}, p, "", &out, &errOut); code != 2 {
		t.Errorf("no label: exit %d", code)
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unhandled: %v", s.Unhandled)
	}
}

// On Jira it says where Jira's tokens are, asking nothing of them.
func TestTokenCmdOnJira(t *testing.T) {
	p, s := demoConfig(t, false)
	for _, args := range [][]string{{"token", "list"}, {"apps", "list"}, {"token", "create", "x"}} {
		var out, errOut bytes.Buffer
		if code := subcommand(args, p, "", &out, &errOut); code != 1 || !strings.Contains(errOut.String(), config.TokenURL) {
			t.Errorf("%v: exit %d, %q", args, code, errOut.String())
		}
	}
	if len(s.Unhandled) > 0 {
		t.Errorf("unhandled: %v", s.Unhandled)
	}
}

// Signed in by an app: the server's page makes the token.
func TestTokenCmdPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/serverInfo" {
			w.Write([]byte(`{"lanewayServerVersion":"1"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(p, []byte("jira: {base_url: \""+srv.URL+"\", email: me@x.test, api_token: tok}\n"), 0o600)
	var out, errOut bytes.Buffer
	if code := subcommand([]string{"token", "create", "x"}, p, "", &out, &errOut); code != 1 || !strings.Contains(errOut.String(), srv.URL+"/admin#passkeys") {
		t.Errorf("exit %d, %q", code, errOut.String())
	}
}
