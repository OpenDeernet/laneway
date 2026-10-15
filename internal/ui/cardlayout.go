package ui

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// ui.card_layout places a lane card's fields, ui.card_styles restyles the
// cards a board query matches. The web's twin is
// internal/web/static/js/lib/cardstyle.js.

// cardLayoutFields are the fields a layout places, besides custom fields.
var cardLayoutFields = []string{"type", "key", "flagged", "priority", "status", "points", "parent", "subtasks", "due", "pr", "deploy", "labels", "age", "avatar", "assignee"}

// cardLayout is ui.card_layout resolved: each side's fields, a custom
// field as "x:" and its lower-cased name.
type cardLayout struct {
	top, topRight, bottom, bottomRight []string
}

// cardStyle is one ui.card_styles entry, its query parsed. Edge and tint
// are colour names (accent, ok, warn, err, info) or hex, "" for none.
type cardStyle struct {
	when       []jiraTerm
	edge, tint string
	fade, bold bool
	hide, show []string
}

// cardLook is what the styles make of one card.
type cardLook struct {
	edge, tint string
	fade, bold bool
	hidden     []string // sorted
}

// cardStyleColours are the colour names a style takes, as theme colours.
var cardStyleColours = map[string]string{"accent": "accent", "ok": "roadmap_done", "warn": "highlight", "err": "error", "info": "roadmap_todo"}

// cardFieldID is a layout or style field's id, false for an unknown one.
func cardFieldID(name string, custom []string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if slices.Contains(cardLayoutFields, n) {
		return n, true
	}
	for _, cf := range custom {
		if strings.EqualFold(strings.TrimSpace(cf), n) {
			return "x:" + n, true
		}
	}
	return "", false
}

// cardLayoutFrom resolves ui.card_layout, nil when it is unset. A field
// placed twice keeps its first place; the key always shows.
func cardLayoutFrom(c config.CardLayout, custom []string) (*cardLayout, []string) {
	if len(c.Top)+len(c.TopRight)+len(c.Bottom)+len(c.BottomRight) == 0 {
		return nil, nil
	}
	var warn []string
	seen := map[string]bool{}
	side := func(names []string) []string {
		var out []string
		for _, n := range names {
			id, ok := cardFieldID(n, custom)
			switch {
			case !ok:
				warn = append(warn, i18n.Tf("ui.card_layout: unknown field %q", n))
			case !seen[id]:
				seen[id] = true
				out = append(out, id)
			}
		}
		return out
	}
	l := &cardLayout{top: side(c.Top), topRight: side(c.TopRight), bottom: side(c.Bottom), bottomRight: side(c.BottomRight)}
	if !seen["key"] {
		l.top = append([]string{"key"}, l.top...)
	}
	return l, warn
}

// defaultCardLayout is the card ui.card_fields makes without a
// ui.card_layout, one per f; "x:*" stands for every custom field.
func defaultCardLayout(f cardFields) *cardLayout {
	if l, ok := defaultLayouts[f]; ok {
		return l
	}
	pick := func(on []bool, ids ...string) []string {
		var out []string
		for i, id := range ids {
			if on[i] {
				out = append(out, id)
			}
		}
		return out
	}
	l := &cardLayout{
		top: pick([]bool{f.flagged, true, f.typ, f.priority, f.pr, f.deploy, f.subtasks, f.due, f.age, f.points},
			"flagged", "key", "type", "priority", "pr", "deploy", "subtasks", "due", "age", "points"),
		bottom: pick([]bool{f.avatar, f.assignee, f.parent, true}, "avatar", "assignee", "parent", "x:*"),
	}
	defaultLayouts[f] = l
	return l
}

// defaultLayouts caches defaultCardLayout; renders run on one goroutine.
var defaultLayouts = map[cardFields]*cardLayout{}

// cardStylesFrom resolves ui.card_styles, dropping what it can't use.
func cardStylesFrom(list []config.CardStyle, custom []string) ([]cardStyle, []string) {
	var out []cardStyle
	var warn []string
	for i, s := range list {
		at := fmt.Sprintf("ui.card_styles[%d]", i)
		if strings.TrimSpace(s.When) == "" {
			warn = append(warn, i18n.Tf("%s: when is empty", at))
			continue
		}
		cs := cardStyle{when: jiraParseQuery(strings.ToLower(strings.TrimSpace(s.When))), fade: s.Fade, bold: s.Bold}
		colour := func(name, v string) string {
			v = strings.TrimSpace(v)
			if v == "" || hexColor.MatchString(v) {
				return v
			}
			if _, ok := cardStyleColours[strings.ToLower(v)]; ok {
				return strings.ToLower(v)
			}
			warn = append(warn, i18n.Tf("%s.%s: %q is not accent, ok, warn, err, info or #rrggbb", at, name, v))
			return ""
		}
		cs.edge, cs.tint = colour("edge", s.Edge), colour("tint", s.Tint)
		fields := func(name string, names []string) []string {
			var ids []string
			for _, n := range names {
				if id, ok := cardFieldID(n, custom); ok {
					ids = append(ids, id)
				} else {
					warn = append(warn, i18n.Tf("%s.%s: unknown field %q", at, name, n))
				}
			}
			return ids
		}
		cs.hide, cs.show = fields("hide", s.Hide), fields("show", s.Show)
		out = append(out, cs)
	}
	return out, warn
}

// cardLookOf is what styles make of c: a later match's colours win; a
// field in some style's show is hidden unless one that shows it matches,
// and a match's hide hides it whatever shows it.
func cardLookOf(c jira.Card, styles []cardStyle, env jiraQueryEnv) cardLook {
	var lk cardLook
	if len(styles) == 0 {
		return lk
	}
	hidden := map[string]bool{}
	for _, s := range styles {
		for _, f := range s.show {
			hidden[f] = true
		}
	}
	var hits []cardStyle
	for _, s := range styles {
		if !jiraCardMatches(c, s.when, env) {
			continue
		}
		hits = append(hits, s)
		if s.edge != "" {
			lk.edge = s.edge
		}
		if s.tint != "" {
			lk.tint = s.tint
		}
		lk.fade = lk.fade || s.fade
		lk.bold = lk.bold || s.bold
		for _, f := range s.show {
			delete(hidden, f)
		}
	}
	for _, s := range hits {
		for _, f := range s.hide {
			hidden[f] = true
		}
	}
	for f := range hidden {
		lk.hidden = append(lk.hidden, f)
	}
	slices.Sort(lk.hidden)
	return lk
}

// cardStyleColour is a style's colour name as a colour, nil for none.
func cardStyleColour(v string) color.Color {
	if v == "" || monoTheme {
		return nil
	}
	if name, ok := cardStyleColours[v]; ok {
		v = curTheme[name]
	}
	return lipgloss.Color(v)
}

// cardTintStyle is the background a tint gives a card: its colour a fifth
// of the way from the terminal's background; none until the terminal says
// what its background is.
func cardTintStyle(v string) (lipgloss.Style, bool) {
	col := cardStyleColour(v)
	if col == nil || termBG == nil {
		return lipgloss.Style{}, false
	}
	const f = 0.2
	r, g, b, _ := col.RGBA()
	br, bg, bb, _ := termBG.RGBA()
	mix := func(c, base uint32) int { return int(math.Round(float64(base>>8)*(1-f) + float64(c>>8)*f)) }
	return lipgloss.NewStyle().Background(lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(r, br), mix(g, bg), mix(b, bb)))), true
}

// cardSideSep splits a styled layout line into its left and right sides,
// which jiraLaneCard pushes apart.
const cardSideSep = "\x1e"

// cardTextFields read as text: two side by side get a dot between them.
var cardTextFields = map[string]bool{"status": true, "parent": true, "labels": true, "assignee": true, "deploy": true}

// layoutCardLines are a card's three lines as l places them: top,
// summary, bottom; styled lines hold cardSideSep between their sides.
func layoutCardLines(c jira.Card, l *cardLayout, styled bool, stale int, lk cardLook) []string {
	now := time.Now()
	extra := jiraExtra(c)
	dimmed := func(s string) string { return dimIf(styled, s) }
	plain := func(styledS, plainS string) string {
		if styled {
			return styledS
		}
		return plainS
	}
	piece := func(f string) string {
		switch f {
		case "type":
			return plain(jiraTypeIcon(c.TypeKind, c.Type), "")
		case "key":
			return plain(jiraKeyStyle.Render(c.Key), c.Key)
		case "flagged":
			if c.Flagged {
				return plain(jiraOverStyle.Render("⚑"), "⚑")
			}
		case "priority":
			return plain(jiraPriorityMark(c.Priority), "")
		case "status":
			return dimmed(c.Status)
		case "points":
			if c.Points != "" {
				return dimmed(c.Points + "p")
			}
		case "parent":
			if c.ParentSummary != "" {
				return dimmed("⌃ " + c.ParentSummary)
			}
		case "subtasks":
			return plain(jiraSubtaskMark(c), "")
		case "due":
			return plain(jiraDueMark(c, now), "")
		case "pr":
			return plain(jiraPRMark(c.PR), "")
		case "deploy":
			return plain(jiraDeployMark(c.Deploy), "")
		case "labels":
			if c.Labels != "" {
				return dimmed("#" + strings.Join(strings.Fields(c.Labels), " #"))
			}
		case "age":
			return plain(jiraAgeMark(c, now, stale), "")
		case "avatar":
			if c.Assignee != "" {
				return plain(jiraAvatar(c.Assignee), jiraInitials(c.Assignee))
			}
		case "assignee":
			if c.Assignee == "" {
				return dimmed(i18n.T("unassigned"))
			}
			return dimmed(c.Assignee)
		case "x:*":
			if vs := jiraExtraValues(c); len(vs) > 0 {
				return dimmed(strings.Join(vs, " · "))
			}
		default:
			if v := extra[strings.TrimPrefix(f, "x:")]; v != "" {
				return dimmed(v)
			}
		}
		return ""
	}
	side := func(fields []string) string {
		var b strings.Builder
		prev := ""
		for _, f := range fields {
			if slices.Contains(lk.hidden, f) {
				continue
			}
			p := piece(f)
			if p == "" {
				continue
			}
			text := cardTextFields[f] || strings.HasPrefix(f, "x:")
			if b.Len() > 0 {
				if text && (cardTextFields[prev] || strings.HasPrefix(prev, "x:")) {
					b.WriteString(dimmed(" · "))
				} else {
					b.WriteString(" ")
				}
			}
			b.WriteString(p)
			prev = f
		}
		return b.String()
	}
	line := func(left, right []string) string {
		l, r := side(left), side(right)
		switch {
		case r == "":
			return l
		case styled:
			return l + cardSideSep + r
		case l == "":
			return r
		}
		return l + " " + r
	}
	sum := c.Summary
	if styled && lk.bold {
		sum = lipgloss.NewStyle().Bold(true).Render(sum)
	}
	return []string{line(l.top, l.topRight), sum, line(l.bottom, l.bottomRight)}
}

// dimIf is s in the dim style when styled.
func dimIf(styled bool, s string) string {
	if styled {
		return jiraDimStyle.Render(s)
	}
	return s
}

// cardSides lays a styled card line's sides out across width: the left
// truncated before the right gives way.
func cardSides(line string, width int) string {
	left, right, ok := strings.Cut(line, cardSideSep)
	if !ok {
		return line
	}
	rw := visualWidth(right)
	if rw >= width {
		return ansi.Truncate(right, width, "…")
	}
	left = ansi.Truncate(left, width-rw-1, "…")
	return left + strings.Repeat(" ", max(width-visualWidth(left)-rw, 1)) + right
}

// joinSides is a card line with its sides one space apart, for where
// there is no width to push them apart in.
func joinSides(line string) string { return strings.ReplaceAll(line, cardSideSep, " ") }
