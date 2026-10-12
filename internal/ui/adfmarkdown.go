package ui

import (
	"fmt"
	"image/color"
	"math"
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cornedor/laneway/internal/i18n"
)

// Jira's blocks and marks markdown lacks, as jira.adfToMarkdown writes
// them: panels and expands between marker lines, "- [ ]" tasks, "<>"
// decisions, and <u>, <sub>, <sup> and <span style="color:…"> text.

var (
	mdPanelOpenRe  = regexp.MustCompile(`^<!-- panel:([a-z]+) -->$`)
	mdExpandOpenRe = regexp.MustCompile(`^<!-- expand(?:\{(\d+)(-?)\})?(?:: (.*))? -->$`)
	mdFlatMarkRe   = regexp.MustCompile(`^<!-- /?(?:tree|layout|column)(?:: [^>]*)? -->$`)
	mdTaskRe       = regexp.MustCompile(`^( *)[-*] \[([ xX])\](?:\{(\d+)\})?(?: |$)`)
	mdDecisionRe   = regexp.MustCompile(`^( *)<>(?: |$)`)
)

// panelIcons mark a panel's first line, as Jira's do.
var panelIcons = map[string]string{"info": "ℹ", "note": "▪", "success": "✔", "warning": "⚠", "error": "✖"}

// mdPanelStyles colour a panel's bar and icon by its type, set by
// applyTheme; a type missing draws dim.
var mdPanelStyles map[string]lipgloss.Style

// mdDecisionStyle colours a decision's mark.
var mdDecisionStyle lipgloss.Style

// containerClose is the index of the line closing the panel or expand
// opened at lines[i], nested ones skipped; len(lines) when none does.
func containerClose(lines []string, i int) int {
	depth := 0
	for j := i; j < len(lines); j++ {
		switch ln := strings.TrimSpace(lines[j]); {
		case mdPanelOpenRe.MatchString(ln) || mdExpandOpenRe.MatchString(ln):
			depth++
		case ln == "<!-- /panel -->" || ln == "<!-- /expand -->":
			if depth--; depth == 0 {
				return j
			}
		}
	}
	return len(lines)
}

// renderContainer draws the panel or expand opened at lines[i] and
// returns its rows and the line after it; ok is false when lines[i] opens
// neither.
func renderContainer(lines []string, i int, ei *emojiImages, mr changeInlineFn, self string) (rows []string, next int, ok bool) {
	head := strings.TrimSpace(lines[i])
	panel := mdPanelOpenRe.FindStringSubmatch(head)
	expand := mdExpandOpenRe.FindStringSubmatch(head)
	if panel == nil && expand == nil {
		return nil, i, false
	}
	end := containerClose(lines, i)
	body := strings.Split(renderMarkdown(strings.Trim(strings.Join(lines[i+1:min(end, len(lines))], "\n"), "\n"), ei, mr, self), "\n")
	if expand != nil {
		title := expand[3]
		if title == "" {
			title = i18n.T("Details")
		}
		mark, fold := "", expand[2] == "-"
		if expand[1] != "" {
			mark = clickMark("expand", expand[1])
		}
		if fold {
			return []string{"  " + mark + mdBoldStyle.Render("▸ "+renderInline(title, ei, mr, self))}, end + 1, true
		}
		rows = append(rows, "  "+mark+mdBoldStyle.Render("▾ "+renderInline(title, ei, mr, self)))
		rows = append(rows, barred(body, mdQuoteBarStyle.Render("│"), "")...)
		return rows, end + 1, true
	}
	style, ok := mdPanelStyles[panel[1]]
	if !ok {
		style = mdFenceStyle
	}
	icon := panelIcons[panel[1]]
	if icon == "" {
		icon = "●"
	}
	return barred(body, style.Render("┃"), style.Render(icon)+" "), end + 1, true
}

// barred puts bar before each of rows' gutters, and first on the first
// row with text, the rest hanging under it. A table's encoded row, with
// no gutter, is left as is.
func barred(rows []string, bar, first string) []string {
	hang := strings.Repeat(" ", lipgloss.Width(first))
	out := make([]string, 0, len(rows))
	for _, ln := range rows {
		rest, ok := strings.CutPrefix(ln, "  ")
		if !ok {
			out = append(out, ln)
			continue
		}
		lead := hang
		if first != "" && strings.TrimSpace(rest) != "" {
			lead, first = first, ""
		}
		out = append(out, "  "+bar+" "+lead+rest)
	}
	return out
}

// taskLine draws a "- [ ]" task as a box and a "<>" decision as its mark;
// ok is false for any other line.
func taskLine(raw string) (string, bool) {
	if m := mdTaskRe.FindStringSubmatch(raw); m != nil {
		box := "☐"
		if m[2] != " " {
			box = "☑"
		}
		if m[3] != "" {
			box = clickMark("task", m[3]) + box
		}
		return m[1] + box + " " + raw[len(m[0]):], true
	}
	if m := mdDecisionRe.FindStringSubmatch(raw); m != nil {
		return m[1] + mdDecisionStyle.Render("◆") + " " + raw[len(m[0]):], true
	}
	return raw, false
}

// clickMark is an invisible mark for the panel to find what a click on
// its row does: a private OSC, like softMark, naming what and which.
func clickMark(what, n string) string { return "\x1b]5379;" + what + ";" + n + "\x1b\\" }

// clickMarkRe finds a clickMark.
var clickMarkRe = regexp.MustCompile(`\x1b\]5379;(task|expand);(\d+)\x1b\\`)

// numberDesc is issue key's description with its action items and
// expands numbered for clickMark, each expand folded unless opened.
func (m *Model) numberDesc(key, desc string) string {
	lines := strings.Split(desc, "\n")
	m.descTasks = nil
	expands := 0
	fence := false
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "```"):
			fence = !fence
		case fence:
		case mdTaskRe.MatchString(ln):
			m.descTasks = append(m.descTasks, mdTaskRe.FindStringSubmatch(ln)[2] != " ")
			at := strings.Index(ln, "]") + 1
			lines[i] = ln[:at] + "{" + strconv.Itoa(len(m.descTasks)) + "}" + ln[at:]
		case mdExpandOpenRe.MatchString(strings.TrimSpace(ln)):
			expands++
			fold := "-"
			if m.descOpen[key+"#"+strconv.Itoa(expands)] {
				fold = ""
			}
			lines[i] = strings.Replace(strings.TrimSpace(ln), "<!-- expand", "<!-- expand{"+strconv.Itoa(expands)+fold+"}", 1)
		}
	}
	return strings.Join(lines, "\n")
}

// mdTagRe finds the tags mdTags styles.
var mdTagRe = regexp.MustCompile(`</?(?:u|sub|sup)>|<span style="(?:color|background-color):\s*#[0-9a-fA-F]{3,6};?">|</span>`)

// mdTags styles s's <u>, <span style="color:…"> and
// <span style="background-color:…"> text, and writes <sub> and <sup> text
// in Unicode's small digits where it can. A reset inside (bold text ending)
// opens the tags' styles again.
func mdTags(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	type open struct{ name, seq, off string }
	var stack []open
	var b strings.Builder
	reopen := func() string {
		var r string
		for _, o := range stack {
			r += o.seq
		}
		return r
	}
	write := func(text string) {
		if len(stack) == 0 {
			b.WriteString(text)
			return
		}
		for _, o := range stack {
			if o.name == "sub" || o.name == "sup" {
				text = scriptText(text, o.name)
			}
		}
		for _, reset := range []string{"\x1b[m", "\x1b[0m"} {
			text = strings.ReplaceAll(text, reset, reset+reopen())
		}
		b.WriteString(text)
	}
	from := 0
	for _, loc := range mdTagRe.FindAllStringIndex(s, -1) {
		write(s[from:loc[0]])
		from = loc[1]
		tag := s[loc[0]:loc[1]]
		if strings.HasPrefix(tag, "</") {
			name := strings.Trim(tag, "</>")
			if len(stack) == 0 || stack[len(stack)-1].name != name {
				b.WriteString(tag) // unmatched: text
				continue
			}
			b.WriteString(stack[len(stack)-1].off)
			stack = stack[:len(stack)-1]
			b.WriteString(reopen())
			continue
		}
		o := open{name: strings.Trim(tag, "<>")}
		switch {
		case o.name == "u":
			o.seq, o.off = "\x1b[4m", "\x1b[24m"
		case strings.Contains(tag, "background-color"):
			o.name = "span"
			if fam := colorFamily(tag); fam != "" && !monoTheme {
				o.seq = ansiOpenSeq(lipgloss.NewStyle().Background(lipgloss.Color(fam)).Foreground(lipgloss.Color("0")))
				o.off = "\x1b[39;49m"
			}
		case strings.HasPrefix(tag, "<span"):
			o.name = "span"
			if fam := colorFamily(tag); fam != "" && !monoTheme {
				o.seq, o.off = ansiOpenSeq(lipgloss.NewStyle().Foreground(lipgloss.Color(fam))), "\x1b[39m"
			}
		}
		stack = append(stack, o)
		b.WriteString(o.seq)
	}
	write(s[from:])
	return b.String()
}

// colorFamily is the terminal colour nearest in hue to the #hex in tag:
// Jira's palette is picked for a light page, so its own shades would be
// unreadable on a dark terminal. Grey reads as the dim colour.
func colorFamily(tag string) string {
	hex := strings.TrimRight(tag[strings.Index(tag, "#"):], `;">`)
	h, ok := hexHue(hex)
	if !ok || len(strings.TrimPrefix(hex, "#"))%3 != 0 {
		return curTheme["dim"]
	}
	return hueFamily(h)
}

// scriptText is s in Unicode sub- or superscript when every character
// has one, else s.
func scriptText(s, name string) string {
	from, to := "0123456789+-=()", "⁰¹²³⁴⁵⁶⁷⁸⁹⁺⁻⁼⁽⁾"
	if name == "sub" {
		to = "₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎"
	}
	dst := []rune(to)
	var b strings.Builder
	for _, r := range s {
		i := strings.IndexRune(from, r) // from is ASCII: a byte index is a rune index
		if i < 0 {
			return s
		}
		b.WriteRune(dst[i])
	}
	return b.String()
}

// termBG is the terminal's background, once it has told; tints blend
// into it.
var termBG color.Color

// statusHues are the hues of Jira's status lozenge colours; neutral has none.
var statusHues = map[string]float64{"purple": 265, "blue": 215, "red": 0, "yellow": 45, "green": 145}

// mdStatusRe is a status lozenge, as jira.adfToMarkdown writes it.
var mdStatusRe = regexp.MustCompile(`<status color="([a-z]+)">([^<\n]+)</status>`)

// mdStatus draws s's status lozenges, bold text on a tint of their
// colour, and dates, on grey.
func mdStatus(s string) string {
	s = mdStatusRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdStatusRe.FindStringSubmatch(m)
		hue, ok := statusHues[sub[1]]
		return lozenge(sub[2], hue, ok)
	})
	s = mdDateRe.ReplaceAllStringFunc(s, func(m string) string {
		return lozenge(mdDateRe.FindStringSubmatch(m)[1], 0, false)
	})
	return s
}

var (
	mdDateRe     = regexp.MustCompile(`<date>(\d{4}-\d{2}-\d{2})</date>`)
	mdAutolinkRe = regexp.MustCompile(`<(https?://[^\s<>]+)>`)
	mdCardRe     = regexp.MustCompile(`^<!-- card: (\S+) -->$`)
)

// atlassianSquare is one of Atlassian's numbered square emoji,
// :1_one_square_blue:.
var atlassianSquare = regexp.MustCompile(`^(\d{1,2})_[a-z]+_square_([a-z]+)$`)

// squareHues are the colours those squares come in.
var squareHues = map[string]float64{"blue": 215, "green": 145, "orange": 30, "purple": 265, "red": 0, "yellow": 50, "teal": 180}

// atlassianEmoji draws an Atlassian emoji Unicode has no glyph for, when
// it can: a numbered square as its number on its colour. "" otherwise.
func atlassianEmoji(name string) string {
	m := atlassianSquare.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	hue, ok := squareHues[m[2]]
	return lozenge(m[1], hue, ok)
}

// lozenge is text bold on a tint of hue (grey when hued is false).
func lozenge(text string, hue float64, hued bool) string {
	style := lipgloss.NewStyle().Bold(true)
	switch {
	case monoTheme:
		style = style.Reverse(true)
	case termBG != nil:
		style = style.Background(tint(hue, hued, 0.28))
	case hued: // the background unknown: its colour on the text instead
		style = style.Foreground(lipgloss.Color(hueFamily(hue)))
	default:
		style = style.Reverse(true)
	}
	return style.Render(" " + text + " ")
}

// tint is a colour f of the way from the terminal's background toward a
// full colour of hue, or toward grey when hued is false.
func tint(hue float64, hued bool, f float64) color.Color {
	r, g, b := 0.5, 0.5, 0.5
	if hued {
		r, g, b = hsl(hue, 0.75, 0.5)
	}
	br, bg, bb, _ := termBG.RGBA()
	mix := func(c float64, base uint32) int {
		v := float64(base>>8)/255*(1-f) + c*f
		return int(math.Round(v * 255))
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(r, br), mix(g, bg), mix(b, bb)))
}

// hsl is a hue, saturation and lightness as red, green and blue in 0–1.
func hsl(h, s, l float64) (float64, float64, float64) {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return r + m, g + m, b + m
}

// hexHue is the hue of #rrggbb or #rgb, and whether it has one (grey
// hasn't).
func hexHue(hex string) (float64, bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil || len(hex) != 6 {
		return 0, false
	}
	r, g, b := float64(v>>16&0xff)/255, float64(v>>8&0xff)/255, float64(v&0xff)/255
	hi, lo := max(r, g, b), min(r, g, b)
	// Saturation, not chroma: Jira's pale cell colours are hued too.
	if hi == lo || (hi-lo)/(1-math.Abs(hi+lo-1)) < 0.25 {
		return 0, false
	}
	var h float64
	switch hi {
	case r:
		h = 60 * (g - b) / (hi - lo)
	case g:
		h = 60 * (2 + (b-r)/(hi-lo))
	default:
		h = 60 * (4 + (r-g)/(hi-lo))
	}
	if h < 0 {
		h += 360
	}
	return h, true
}

// hueFamily is the terminal colour nearest hue: a bright one, or on a
// light background, where those wash out, a normal one.
func hueFamily(h float64) string {
	light := termBG != nil && !isDark(termBG)
	for _, f := range []struct {
		upTo          float64
		bright, color string
	}{{15, "9", "1"}, {45, "208", "166"}, {70, "11", "3"}, {165, "10", "2"}, {200, "14", "6"}, {250, "12", "4"}, {330, "13", "5"}, {360, "9", "1"}} {
		if h < f.upTo {
			if light {
				return f.color
			}
			return f.bright
		}
	}
	return "9"
}

// cellBGRe is a table cell's background, leading its text.
var cellBGRe = regexp.MustCompile(`^<!-- bg:(#[0-9a-fA-F]{3,6}) -->\s*`)

// cellBackground is a cell's text without its markers, its background as
// a style opening sequence ("" for none, or when the terminal's background
// is unknown), and whether it is a header cell.
func cellBackground(cell string) (text, bg string, header bool) {
	for {
		if m := cellBGRe.FindStringSubmatch(cell); m != nil {
			cell = cell[len(m[0]):]
			if hue, ok := hexHue(m[1]); termBG != nil && !monoTheme {
				bg = ansiOpenSeq(lipgloss.NewStyle().Background(tint(hue, ok, 0.14)))
			}
		} else if rest, ok := strings.CutPrefix(cell, "<!-- th -->"); ok {
			cell, header = strings.TrimSpace(rest), true
		} else {
			return cell, bg, header
		}
	}
}

// onBackground paints s on the background bg opens, again after each reset
// inside it.
func onBackground(s, bg string) string {
	if bg == "" {
		return s
	}
	for _, reset := range []string{"\x1b[m", "\x1b[0m"} {
		s = strings.ReplaceAll(s, reset, reset+bg)
	}
	return bg + s + "\x1b[m"
}
