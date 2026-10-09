package web

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/ui"
)

// The settings API serves the ui: options of the config file, the same
// table the terminal's settings screen uses, and writes them back through
// config.SetUI (comments stay), so both front ends share one file.

// UIConfig is the current ui: section (settings edits apply at once, for
// every site).
func init() {
	// As the TUI's ↑ v1.2: the newer release, once a day, ui.update_check off stops it.
	get("/update", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		if strings.EqualFold(strings.TrimSpace(s.UIConfig().UpdateCheck), "off") {
			return map[string]string{}, nil
		}
		tag := ui.NewerRelease(ctx, s.opt.Store, s.opt.Version)
		if tag == "" {
			return map[string]string{}, nil
		}
		return map[string]string{"Tag": tag, "Command": s.opt.UpgradeCmd, "Page": ui.ReleasePage}, nil
	})
}

func (s *Server) UIConfig() config.UIConfig {
	if s.sites == nil {
		return s.opt.UI
	}
	s.sites.uiMu.RLock()
	defer s.sites.uiMu.RUnlock()
	return s.sites.ui
}

// Setting is one ui: option for the settings screen. Type is bool, enum,
// duration, number, list, text or yaml (too big for a line).
type Setting struct {
	Name     string
	Group    string
	Type     string
	Default  string
	Doc      string
	Choices  []string `json:",omitempty"`
	Restart  bool
	Advanced bool   // not in config.SettingsBasic: behind the page's Advanced
	Terminal bool   // config.SettingsTerminal: the browser ignores it
	Set      bool   // the file gives it a value
	Value    any    // string, number or []string; nil when unset
	YAML     string `json:",omitempty"` // the value as YAML, for type yaml
}

var durations = map[string]bool{"auto_refresh": true, "stale_after": true, "full_refresh": true, "inbox_every": true, "inbox_lookback": true, "double_click": true, "timer_round": true, "standup_length": true, "standup_timebox": true}

func init() {
	get("/settings", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		c := s.UIConfig()
		acts := ui.KeyActions()
		return map[string]any{
			"path":       s.opt.ConfigPath,
			"editable":   s.opt.ConfigPath != "",
			"groups":     groupTitles(),
			"settings":   settings(c),
			"warnings":   ui.ValidateUI(c),
			"keyActions": acts,
		}, nil
	})
	put("/settings/{name}", putSetting)
}

func groupTitles() []string {
	out := make([]string, 0, len(config.SettingGroups)+1)
	for _, g := range config.SettingGroups {
		out = append(out, g.Title)
	}
	return append(out, "Other")
}

func fieldByName(v reflect.Value, name string) reflect.Value {
	t := v.Type()
	for i := range t.NumField() {
		if n, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); n == name {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}

func settings(c config.UIConfig) []Setting {
	v := reflect.ValueOf(c)
	group := map[string]string{}
	for _, g := range config.SettingGroups {
		for _, n := range g.Names {
			group[n] = g.Title
		}
	}
	var out []Setting
	add := func(name string) {
		f := fieldByName(v, name)
		if f.IsValid() {
			out = append(out, describe(name, group[name], f))
		}
	}
	done := map[string]bool{}
	for _, g := range config.SettingGroups {
		for _, n := range g.Names {
			add(n)
			done[n] = true
		}
	}
	t := v.Type()
	for i := range t.NumField() {
		if n, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); n != "" && !done[n] {
			add(n)
		}
	}
	for i := range out {
		if out[i].Group == "" {
			out[i].Group = "Other"
		}
	}
	return out
}

func describe(name, group string, f reflect.Value) Setting {
	st := Setting{Name: name, Group: group, Default: config.SettingDefaults[name], Doc: config.SettingDocs[name], Restart: config.SettingsRestart[name], Advanced: !config.SettingsBasic[name], Terminal: config.SettingsTerminal[name], Set: !f.IsZero()}
	st.Choices = ui.SettingChoices(name)
	switch f.Kind() {
	case reflect.String:
		st.Type, st.Value = "text", nil
		if st.Set {
			st.Value = f.String()
		}
		switch {
		case durations[name]:
			st.Type = "duration"
		case len(st.Choices) == 2 && slices.Contains(st.Choices, "on") && slices.Contains(st.Choices, "off"):
			st.Type = "bool"
		case st.Choices != nil:
			st.Type = "enum"
		}
	case reflect.Int:
		st.Type = "number"
		if st.Set {
			st.Value = f.Int()
		}
	case reflect.Slice:
		if f.Type().Elem().Kind() == reflect.String {
			st.Type = "list"
			if st.Set {
				st.Value = f.Interface()
			}
			break
		}
		st.Type = "yaml"
	case reflect.Map:
		st.Type = "yaml"
		if t, ok := f.Interface().(config.Theme); ok && (len(t) == 0 || len(t) == 1 && t["preset"] != "") {
			st.Type, st.Value = "enum", t["preset"]
			if len(t) == 0 {
				st.Value = nil
			}
		}
	default:
		st.Type = "yaml"
	}
	if st.Type == "yaml" && st.Set {
		if b, err := yaml.Marshal(f.Interface()); err == nil {
			st.YAML = strings.TrimRight(string(b), "\n")
		}
	}
	if st.Type != "enum" && st.Type != "bool" {
		st.Choices = nil
	}
	return st
}

// editUI is c with option name set from raw (JSON or YAML decoded); the
// value to write is nil when the result is the default.
func editUI(c config.UIConfig, name string, raw any) (config.UIConfig, any, error) {
	next := c
	dst := fieldByName(reflect.ValueOf(&next).Elem(), name)
	if !dst.IsValid() {
		return c, nil, badRequest(i18n.Tf("unknown option %s", name))
	}
	if s, ok := raw.(string); ok {
		raw = strings.TrimSpace(s)
		if raw == "" {
			raw = nil
		}
	}
	if raw == nil {
		dst.Set(reflect.Zero(dst.Type()))
		return next, nil, nil
	}
	switch dst.Kind() {
	case reflect.Int:
		switch n := raw.(type) {
		case string:
			i, err := strconv.Atoi(n)
			if err != nil {
				return c, nil, badRequest(i18n.Tf("%s: %q is not a number", name, n))
			}
			raw = i
		case float64:
			if n != float64(int(n)) {
				return c, nil, badRequest(i18n.Tf("%s: %v is not a whole number", name, n))
			}
			raw = int(n)
		}
	case reflect.Slice:
		if s, ok := raw.(string); ok {
			raw = strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
		}
	case reflect.String:
		if _, ok := raw.(string); !ok {
			raw = fmt.Sprint(raw)
		}
	}
	doc, err := yaml.Marshal(map[string]any{name: raw})
	if err != nil {
		return c, nil, badRequest(err.Error())
	}
	var tmp config.UIConfig
	if err := yaml.Unmarshal(doc, &tmp); err != nil {
		return c, nil, badRequest(fmt.Sprintf("%s: %v", name, err))
	}
	src := fieldByName(reflect.ValueOf(&tmp).Elem(), name)
	dst.Set(src)
	if dst.IsZero() {
		return next, nil, nil
	}
	return next, src.Interface(), nil
}

func putSetting(ctx context.Context, s *Server, r *http.Request) (any, error) {
	name := r.PathValue("name")
	b, err := Body[struct {
		Value any
		YAML  *string
	}](r)
	if err != nil {
		return nil, err
	}
	raw := b.Value
	if b.YAML != nil {
		raw = nil
		if strings.TrimSpace(*b.YAML) != "" {
			if err := yaml.Unmarshal([]byte(*b.YAML), &raw); err != nil {
				return nil, badRequest(name + ": " + err.Error())
			}
		}
	}
	if s.opt.ConfigPath == "" {
		return nil, httpError{http.StatusConflict, i18n.T("no config file to write to")}
	}
	// With -remote the token holder could otherwise make laneway run any command.
	if s.opt.Token != "" && config.SettingsCommand[name] {
		return nil, httpError{http.StatusForbidden, i18n.Tf("ui.%s runs commands: edit it in the config file, not over -remote", name)}
	}
	if s.opt.Hosted && !hostedSetting(name) {
		return nil, httpError{http.StatusForbidden, i18n.Tf("ui.%s acts on the machine laneway runs on: not on a laneway web shared by many people", name)}
	}
	s.sites.uiMu.Lock()
	defer s.sites.uiMu.Unlock()
	next, value, err := editUI(s.sites.ui, name, raw)
	if err != nil {
		return nil, err
	}
	for _, w := range ui.ValidateUI(next) {
		if strings.HasPrefix(w, "ui."+name) {
			return nil, badRequest(w)
		}
	}
	if err := config.SetUI(s.opt.ConfigPath, name, value); err != nil {
		return nil, err
	}
	s.sites.ui = next
	for _, st := range settings(next) {
		if st.Name == name {
			return st, nil
		}
	}
	return nil, nil
}

// projectKey is a Jira project key: a capital, then capitals, digits or _.
var projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

func init() {
	// The projects the project picker lists first, the first one opened at
	// start: the site's projects: in the config, in the order given.
	put("/site/projects", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ Projects []string }](r)
		if err != nil {
			return nil, err
		}
		var keys []string
		for _, k := range b.Projects {
			k = strings.ToUpper(strings.TrimSpace(k))
			if !projectKey.MatchString(k) {
				return nil, badRequest(i18n.Tf("%q is not a project key", k))
			}
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
		if s.opt.ConfigPath == "" {
			return nil, httpError{http.StatusConflict, i18n.T("no config file to write to")}
		}
		if err := config.SetProjects(s.opt.ConfigPath, s.opt.Site, keys); err != nil {
			return nil, err
		}
		s.sites.mu.Lock()
		s.sites.projects[s.opt.Site] = keys
		s.sites.mu.Unlock()
		return map[string]any{"projects": keys}, nil
	})
	// Restart serves the app again with the config read anew, on the same
	// address, site and -remote token.
	post("/restart", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		if s.sites.base.Restart == nil {
			return nil, httpError{http.StatusNotImplemented, i18n.T("this server can't restart itself")}
		}
		s.sites.base.Restart()
		return map[string]any{}, nil
	})
}

// projects are the site's projects: the config's, or the last saved.
func (s *Server) projects() []string {
	s.sites.mu.Lock()
	defer s.sites.mu.Unlock()
	if p, ok := s.sites.projects[s.opt.Site]; ok {
		return p
	}
	return s.opt.Jira.Projects
}
