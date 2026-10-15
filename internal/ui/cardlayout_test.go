package ui

import (
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/jira"
)

func TestCardLayoutFrom(t *testing.T) {
	if l, warn := cardLayoutFrom(config.CardLayout{}, nil); l != nil || warn != nil {
		t.Errorf("unset: %v %v", l, warn)
	}
	l, warn := cardLayoutFrom(config.CardLayout{Top: []string{"Type", "nope"}, TopRight: []string{"points", "type"}, Bottom: []string{"test type"}}, []string{"Test Type"})
	if len(warn) != 1 || !strings.Contains(warn[0], `"nope"`) {
		t.Errorf("warn = %q", warn)
	}
	if !slices.Equal(l.top, []string{"key", "type"}) || !slices.Equal(l.topRight, []string{"points"}) || !slices.Equal(l.bottom, []string{"x:test type"}) {
		t.Errorf("layout = %+v: the key always shows, a field once, custom fields by name", l)
	}
}

func TestLayoutCardLines(t *testing.T) {
	c := jira.Card{Key: "ABC-1", Summary: "s", Status: "In Review", Assignee: "Ada", Points: "3", Labels: "ui web", Extra: "Team=Core"}
	l := &cardLayout{top: []string{"key", "status"}, topRight: []string{"points"}, bottom: []string{"labels", "x:team"}, bottomRight: []string{"assignee"}}
	got := layoutCardLines(c, l, false, 5, cardLook{})
	if want := []string{"ABC-1 In Review 3p", "s", "#ui #web · Core Ada"}; !slices.Equal(got, want) {
		t.Errorf("plain = %q, want %q", got, want)
	}
	styled := layoutCardLines(c, l, true, 5, cardLook{hidden: []string{"status"}})
	if line := ansi.Strip(cardSides(styled[0], 20)); line != "ABC-1             3p" {
		t.Errorf("sides = %q: the right side at the right edge, status hidden", line)
	}
	if line := ansi.Strip(cardSides(styled[2], 12)); line != "#ui #we… Ada" {
		t.Errorf("narrow = %q: the left side gives way", line)
	}
	// No extra fields: no dot after the last piece.
	bare := layoutCardLines(jira.Card{Key: "ABC-2", Assignee: "Ada"}, &cardLayout{bottom: []string{"assignee", "x:*"}}, true, 5, cardLook{})
	if line := ansi.Strip(bare[2]); line != "Ada" {
		t.Errorf("no extras = %q", line)
	}
}

func TestCardLook(t *testing.T) {
	styles, warn := cardStylesFrom([]config.CardStyle{
		{When: "prio>=high", Edge: "err", Bold: true},
		{When: "label:ui", Edge: "#00ff00", Tint: "warn", Hide: []string{"labels"}},
		{When: "due<7d", Show: []string{"due"}},
		{When: " ", Edge: "err"},
		{When: "x", Tint: "pink", Hide: []string{"nope"}},
	}, nil)
	if len(warn) != 3 || len(styles) != 4 {
		t.Fatalf("warn = %q (empty when, bad colour, unknown field), %d styles", warn, len(styles))
	}
	env := jiraQueryEnv{now: time.Now()}
	lk := cardLookOf(jira.Card{Key: "A-1", Priority: "High", Labels: "ui"}, styles, env)
	if lk.edge != "#00ff00" || lk.tint != "warn" || !lk.bold || !slices.Equal(lk.hidden, []string{"due", "labels"}) {
		t.Errorf("look = %+v: the later edge wins; due waits for its show", lk)
	}
	lk = cardLookOf(jira.Card{Key: "A-2", Due: time.Now().Add(48 * time.Hour)}, styles, env)
	if lk.edge != "" || lk.bold || len(lk.hidden) != 0 {
		t.Errorf("due soon = %+v", lk)
	}
}

func TestLaneCardStyles(t *testing.T) {
	m := jiraTabModel(t)
	m.opts.cardStyles, _ = cardStylesFrom([]config.CardStyle{{When: "is:flagged", Edge: "err", Tint: "err", Fade: true}}, nil)
	termBG = color.Black
	defer func() { termBG = nil }()
	plainCard := m.jiraLaneCard(jira.Card{Key: "A-1", Summary: "s"}, false, 30)
	flagged := m.jiraLaneCard(jira.Card{Key: "A-2", Summary: "s", Flagged: true}, false, 30)
	if strings.Contains(plainCard[0], "▌") || !strings.HasPrefix(ansi.Strip(flagged[0]), "▌") {
		t.Errorf("edge: %q / %q", ansi.Strip(plainCard[0]), ansi.Strip(flagged[0]))
	}
	if !strings.Contains(flagged[1], "48;2;") {
		t.Errorf("tint: no background in %q", flagged[1])
	}
	for _, line := range flagged {
		if w := ansi.StringWidth(line); w != 30 {
			t.Errorf("width %d: %q", w, ansi.Strip(line))
		}
	}
}
