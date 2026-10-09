package demo

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The laneway flavour: the demo as laneway-server rather than Jira Cloud.
// serverInfo names a version, the account's API tokens and signed-in apps
// are served, and the project is a service desk (servicedesk.go). Off (the
// default) none of it is, and the demo stays as the contract recording has
// Jira.

type lanewayState struct {
	tokens []demoToken
	grants []demoGrant
	seq    int
	// The account's own settings (settings_account.js): time zone, mail
	// language and how it is mailed, its sessions, an uploaded avatar.
	timeZone, locale, mailMode string
	mailOff                    bool
	sessions                   []demoSession
	avatar                     []byte
	avatarType                 string
	avatarSeq                  int
}

type demoSession struct {
	id, ip, method   string
	created, lastSee time.Time
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
	current                  bool // the one the app signs in with
}

// Laneway makes s laneway-server.
func (s *Server) Laneway() {
	s.mu.Lock()
	defer s.mu.Unlock()
	day := func(d int) time.Time { return s.now.AddDate(0, 0, d).Truncate(time.Hour) }
	s.lw = &lanewayState{seq: 2, timeZone: "UTC", locale: "en_US", mailMode: "instant",
		sessions: []demoSession{{id: "a1b2c3d4e5f6", ip: "192.0.2.10", method: "passkey", created: day(-3), lastSee: day(0)}, {id: "f6e5d4c3b2a1", ip: "198.51.100.7", method: "password", created: day(-12), lastSee: day(-2)}},
		tokens: []demoToken{
			{id: "1", label: "CI pipeline", scopes: []string{"read:jira-work"}, created: day(-60), lastUsed: day(0), until: day(30)},
			{id: "2", label: "old laptop", created: day(-80), until: day(10)},
		},
		grants: []demoGrant{
			{id: "1", app: "laneway", name: "laneway", scopes: []string{"read:jira-work", "write:jira-work", "offline_access"}, method: "passkey", created: day(-2), lastUsed: day(0), until: day(28), current: true},
			{id: "3", app: "laneway", name: "laneway", scopes: []string{"read:jira-work", "write:jira-work", "offline_access"}, method: "device", created: day(-9), lastUsed: day(-3), until: day(21)},
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
				"created": stamp(g.created), "lastUsed": stamp(g.lastUsed), "expires": stamp(g.until), "current": g.current})
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

// serveAccount answers the account's own settings in the laneway flavour:
// mypreferences (time zone, as text), its locale, how it is mailed, its
// sessions and an uploaded avatar.
func (s *Server) serveAccount(w http.ResponseWriter, r *http.Request, raw []byte, body map[string]any, send func(any)) bool {
	if s.lw == nil {
		return false
	}
	lw, p, get := s.lw, r.URL.Path, r.Method == http.MethodGet
	owner := "/rest/api/3/universal_avatar/type/user/owner/" + me.id
	none := func() { w.WriteHeader(http.StatusNoContent) }
	switch {
	case p == "/rest/api/3/mypreferences" && r.URL.Query().Get("key") == "jira.user.timezone":
		if get {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(lw.timeZone))
			return true
		}
		lw.timeZone = strings.TrimSpace(string(raw))
		none()
	case p == "/rest/api/3/mypreferences/locale":
		if get {
			send(map[string]any{"locale": lw.locale})
			return true
		}
		lw.locale, _ = body["locale"].(string)
		none()
	case p == "/rest/laneway/1/myself/notifications":
		if get {
			send(map[string]any{"email": !lw.mailOff, "mode": lw.mailMode})
			return true
		}
		if v, ok := body["email"].(bool); ok {
			lw.mailOff = !v
		}
		if v, ok := body["mode"].(string); ok {
			lw.mailMode = v
		}
		none()
	case p == "/rest/laneway/1/myself/sessions":
		if get {
			out := []any{}
			for _, ss := range lw.sessions {
				out = append(out, map[string]any{"id": ss.id, "ip": ss.ip, "method": ss.method, "created": stamp(ss.created), "lastSeen": stamp(ss.lastSee), "expires": stamp(ss.created.AddDate(0, 0, 7))})
			}
			send(out)
			return true
		}
		lw.sessions = nil
		none()
	case p == owner && get:
		custom := []any{}
		if lw.avatar != nil {
			custom = append(custom, map[string]any{"id": strconv.Itoa(lw.avatarSeq), "isSelected": true, "isDeletable": true})
		}
		send(map[string]any{"system": []any{map[string]any{"id": "1", "isSystemAvatar": true, "isSelected": lw.avatar == nil}}, "custom": custom})
	case p == owner && r.Method == http.MethodPost:
		lw.avatar, lw.avatarType = raw, r.Header.Get("Content-Type")
		lw.avatarSeq++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		send(map[string]any{"id": strconv.Itoa(lw.avatarSeq), "isSelected": true, "isDeletable": true})
	case p == owner+"/avatar/"+strconv.Itoa(lw.avatarSeq) && r.Method == http.MethodDelete && lw.avatar != nil:
		lw.avatar = nil
		none()
	case p == "/avatar/user/"+me.id && get && lw.avatar != nil:
		w.Header().Set("Content-Type", lw.avatarType)
		_, _ = w.Write(lw.avatar)
	default:
		return false
	}
	return true
}
