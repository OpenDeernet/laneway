package demo

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The laneway flavour: the demo as laneway-server rather than Jira Cloud.
// serverInfo names a version and the account's API tokens and signed-in
// apps are served. Off (the default) none of it is, and the demo stays as
// the contract recording has Jira.

type lanewayState struct {
	tokens []demoToken
	grants []demoGrant
	seq    int
}

type demoToken struct {
	id, label                string
	scopes                   []string
	created, lastUsed, until time.Time
}

type demoGrant struct {
	id, app, name            string
	scopes                   []string
	method                   string
	created, lastUsed, until time.Time
}

// Laneway makes s laneway-server.
func (s *Server) Laneway() {
	s.mu.Lock()
	defer s.mu.Unlock()
	day := func(d int) time.Time { return s.now.AddDate(0, 0, d).Truncate(time.Hour) }
	s.lw = &lanewayState{seq: 2,
		tokens: []demoToken{
			{id: "1", label: "CI pipeline", scopes: []string{"read:jira-work"}, created: day(-60), lastUsed: day(0), until: day(30)},
			{id: "2", label: "old laptop", created: day(-80), until: day(10)},
		},
		grants: []demoGrant{
			{id: "1", app: "laneway", name: "laneway", scopes: []string{"read:jira-work", "write:jira-work", "offline_access"}, method: "passkey", created: day(-2), lastUsed: day(0), until: day(28)},
			{id: "2", app: "calendar", name: "Calendar sync", scopes: []string{"read:jira-work"}, method: "password", created: day(-20), lastUsed: day(-1), until: day(10)},
		},
	}
}

// serveLaneway answers laneway-server's own routes in the laneway flavour.
func (s *Server) serveLaneway(w http.ResponseWriter, r *http.Request, body map[string]any, send func(any)) bool {
	p := r.URL.Path
	if s.lw == nil || !strings.HasPrefix(p, "/rest/laneway/1/") {
		return false
	}
	lw := s.lw
	gone := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		send(map[string]any{"errorMessages": []string{"No such one."}})
	}
	id, _ := strings.CutPrefix(p, "/rest/laneway/1/myself/tokens/")
	gid, _ := strings.CutPrefix(p, "/rest/laneway/1/myself/oauth/grants/")
	switch {
	case p == "/rest/laneway/1/events": // the demo has no change feed: the app polls
		gone()
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/rest/laneway/1/dashboard/"+demoDashboard+"/gadget/") && strings.HasSuffix(p, "/data"):
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(p, "/rest/laneway/1/dashboard/"+demoDashboard+"/gadget/"), "/data"))
		if v, ok := s.gadgetData(n); ok {
			send(v)
		} else {
			gone()
		}
	case r.Method == http.MethodGet && p == "/rest/laneway/1/myself/tokens":
		out := []any{}
		for _, t := range lw.tokens {
			out = append(out, t.json())
		}
		send(out)
	case r.Method == http.MethodPost && p == "/rest/laneway/1/myself/tokens":
		label, _ := body["label"].(string)
		days, _ := body["days"].(float64)
		if days <= 0 {
			days = 90
		}
		lw.seq++
		t := demoToken{id: strconv.Itoa(lw.seq), label: label, scopes: anyStrings(body["scopes"]), created: s.now, until: s.now.AddDate(0, 0, int(days))}
		lw.tokens = append(lw.tokens, t)
		o := t.json()
		o["token"] = "lws_demo" + t.id + "_not-a-real-secret"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		send(o)
	case r.Method == http.MethodDelete && p == "/rest/laneway/1/myself/tokens":
		n := len(lw.tokens)
		lw.tokens = nil
		send(map[string]any{"revoked": n})
	case r.Method == http.MethodDelete && id != p:
		n := len(lw.tokens)
		lw.tokens = slices.DeleteFunc(lw.tokens, func(t demoToken) bool { return t.id == id })
		if n == len(lw.tokens) {
			gone()
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && p == "/rest/laneway/1/myself/oauth/grants":
		out := []any{}
		for _, g := range lw.grants {
			out = append(out, map[string]any{"id": atoi(g.id), "app": map[string]any{"id": g.app, "name": g.name}, "scopes": g.scopes, "method": g.method,
				"created": stamp(g.created), "lastUsed": stamp(g.lastUsed), "expires": stamp(g.until)})
		}
		send(out)
	case r.Method == http.MethodDelete && gid != p:
		n := len(lw.grants)
		lw.grants = slices.DeleteFunc(lw.grants, func(g demoGrant) bool { return g.id == gid })
		if n == len(lw.grants) {
			gone()
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

func (t demoToken) json() map[string]any {
	return map[string]any{"id": t.id, "label": t.label, "scopes": append([]string{}, t.scopes...), "created": stamp(t.created), "lastUsed": stamp(t.lastUsed), "expires": stamp(t.until)}
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func anyStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
