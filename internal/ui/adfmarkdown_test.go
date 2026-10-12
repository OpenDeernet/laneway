package ui

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderJiraBlocks: a panel draws behind a bar with its icon, an
// expand under its title, tasks as boxes and a decision with its mark;
// the markers themselves don't show.
func TestRenderJiraBlocks(t *testing.T) {
	applyTheme(defaultTheme())
	md := "<!-- panel:success -->\n\n**Done** when\n\n- [ ] one\n  - [x] two\n\n<!-- /panel -->\n\n<!-- expand: More -->\n\nhidden\n\n<!-- /expand -->\n\n<> agreed"
	got := ansi.Strip(renderMarkdown(md, nil, nil, ""))
	want := "  ┃ ✔ Done when\n  ┃   \n  ┃   ☐ one\n  ┃     ☑ two\n  \n  ▾ More\n  │ hidden\n  \n  ◆ agreed"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestMdTags: underline and colour style their text and end with it, a
// reset inside opens the colour again, sub- and superscript digits turn
// small, and a tag left open or unknown stays text.
func TestMdTags(t *testing.T) {
	applyTheme(defaultTheme())
	got := mdTags(`<u>u</u> <span style="color:#ff5630">a` + "\x1b[1mb\x1b[m" + `c</span> H<sub>2</sub>O x<sup>n</sup> </u>`)
	if ansi.Strip(got) != "u abc H₂O xn </u>" {
		t.Errorf("text %q", ansi.Strip(got))
	}
	for _, s := range []string{"\x1b[4mu\x1b[24m", "\x1b[m\x1b[91m", "c\x1b[39m"} {
		if !strings.Contains(got, s) {
			t.Errorf("lacks %q: %q", s, got)
		}
	}
	if colorFamily(`#97a0af"`) != curTheme["dim"] || colorFamily(`#0747a6"`) != "12" || colorFamily(`#ff991f"`) != "208" {
		t.Error("colour families")
	}
}

// TestNumberDesc: tasks and expands get numbered for clicks, outside
// code; an expand draws folded to its title until opened; each task's
// state is kept for the toggle.
func TestNumberDesc(t *testing.T) {
	applyTheme(defaultTheme())
	m := &Model{}
	md := "- [ ] one\n- [x] two\n\n```\n- [ ] code\n```\n\n<!-- expand: More -->\n\nhidden\n\n<!-- /expand -->"
	out := renderMarkdown(m.numberDesc("K-1", md), nil, nil, "")
	if got := ansi.Strip(out); strings.Contains(got, "hidden") || !strings.Contains(got, "▸ More") {
		t.Errorf("folded:\n%s", got)
	}
	var marks []string
	for _, cm := range clickMarkRe.FindAllStringSubmatch(out, -1) {
		marks = append(marks, cm[1]+cm[2])
	}
	if strings.Join(marks, " ") != "task1 task2 expand1" || len(m.descTasks) != 2 || m.descTasks[0] || !m.descTasks[1] {
		t.Errorf("marks %v, tasks %v", marks, m.descTasks)
	}
	m.descOpen = map[string]bool{"K-1#1": true}
	if got := ansi.Strip(renderMarkdown(m.numberDesc("K-1", md), nil, nil, "")); !strings.Contains(got, "▾ More\n  │ hidden") {
		t.Errorf("opened:\n%s", got)
	}
}

// TestLozenges: a status and a numbered Atlassian square draw on a tint
// of their colour once the terminal's background is known.
func TestLozenges(t *testing.T) {
	applyTheme(defaultTheme())
	defer func(bg color.Color) { termBG = bg }(termBG)
	termBG = color.RGBA{0x1a, 0x1b, 0x26, 0xff}
	got := renderInline(`<status color="green">DONE</status> :1_one_square_blue:`, nil, nil, "")
	if ansi.Strip(got) != " DONE   1 " || strings.Count(got, "48;2;") != 2 {
		t.Errorf("%q", got)
	}
}

// TestRenderTableCellLines: a cell's <br> starts a line in its cell; a
// pane too narrow for a box draws them as text.
func TestRenderTableCellLines(t *testing.T) {
	applyTheme(defaultTheme())
	md := "| h | i |\n| --- | --- |\n| one<br>longer two | 1 |"
	got := ansi.Strip(expandTables(renderMarkdown(md, nil, nil, ""), 60))
	for _, want := range []string{"│ one        │ 1 │", "│ longer two │   │"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if got := ansi.Strip(expandTables(renderMarkdown(md, nil, nil, ""), 8)); !strings.Contains(got, "two") || strings.Contains(got, "<br>") || strings.Contains(got, "\x00") {
		t.Errorf("narrow:\n%s", got)
	}
}

// TestRenderJiraInline: a date draws as a lozenge, a smart link without
// its brackets, a card as its link, a header cell off the header row
// without its marker.
func TestRenderJiraInline(t *testing.T) {
	applyTheme(defaultTheme())
	md := "due <date>2026-10-01</date>, see <https://x.test/a>\n\n<!-- card: https://x.test/b -->\n\n| h | i |\n| --- | --- |\n| <!-- th --> <!-- bg:#e3fcef --> side | 1 |"
	got := ansi.Strip(expandTables(renderMarkdown(md, nil, nil, ""), 60))
	for _, want := range []string{"due  2026-10-01 , see https://x.test/a", "  https://x.test/b", "│ side │ 1 │"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<!--") || strings.Contains(got, "<date>") {
		t.Errorf("markers show:\n%s", got)
	}
}

func TestRenderPageTree(t *testing.T) {
	got := ansi.Strip(renderMarkdown("Pages:\n\n<!-- tree: open=1, search -->\n\n- [Deploys](https://x.test/1)\n  - [Rollback](https://x.test/2)\n\n<!-- /tree -->\n\nAfter", nil, nil, ""))
	if strings.Contains(got, "tree") || !strings.Contains(got, "Deploys") || !strings.Contains(got, "Rollback") || !strings.Contains(got, "After") {
		t.Errorf("got\n%s", got)
	}
}
