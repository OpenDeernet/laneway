package jira

import (
	"bytes"
	"cmp"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cornedor/laneway/internal/emoji"
	"github.com/cornedor/laneway/internal/safeterm"
)

// adfNode is one node in an Atlassian Document Format tree. Block and inline
// nodes share the shape; Text is set on leaf "text" nodes, Content holds
// children, Marks decorate text, Attrs carries node-specific fields (heading
// level, link href, code language, …).
type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Content []adfNode      `json:"content"`
	Marks   []adfMark      `json:"marks"`
	Attrs   map[string]any `json:"attrs"`
}

type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// adfToMarkdown flattens an ADF document into markdown the TUI's renderer
// understands (see internal/ui/markdown.go). It handles the common node types;
// anything unrecognised degrades to its text content rather than vanishing, so
// an exotic issue still reads sensibly. A nil/empty/garbage document yields "".
func adfToMarkdown(raw json.RawMessage) string { return adfWithFiles(raw, nil) }

// DocMarkdown is raw as markdown to read: what markdown can't carry
// degrades to its text.
func DocMarkdown(raw json.RawMessage) string { return adfToMarkdown(raw) }

// adfWithFiles is adfToMarkdown with each inline file as files links it, by
// media id; the rest stay _[file]_.
func adfWithFiles(raw json.RawMessage, files map[string]string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var doc adfNode
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	writeBlocks(&b, doc.Content, "")
	md := strings.TrimSpace(b.String())
	if !strings.Contains(md, "]("+fileRef) {
		return md
	}
	return fileRe.ReplaceAllStringFunc(md, func(s string) string {
		return cmp.Or(files[fileRe.FindStringSubmatch(s)[1]], "_[file]_")
	})
}

// mediaIDRe is a media file id as a markdown image target may carry it.
var mediaIDRe = regexp.MustCompile(`^[0-9a-fA-F-]{1,64}$`)

var mediaLayout = regexp.MustCompile(`^[a-z-]{1,20}$`)

// mediaTitle is a mediaSingle's width and layout as a markdown image title
// ( "320px wrap-left", "50%"), "" when it is the default: as wide as it
// is, centred.
func mediaTitle(attrs map[string]any) string {
	var parts []string
	if w, ok := attrs["width"].(float64); ok && w > 0 {
		if t, _ := attrs["widthType"].(string); t == "pixel" {
			parts = append(parts, strconv.Itoa(int(w))+"px")
		} else {
			parts = append(parts, strconv.FormatFloat(min(w, 100), 'f', -1, 64)+"%")
		}
	}
	if l, _ := attrs["layout"].(string); l != "" && l != "center" && mediaLayout.MatchString(l) {
		parts = append(parts, l)
	}
	if len(parts) == 0 {
		return ""
	}
	return ` "` + strings.Join(parts, " ") + `"`
}

// fileRef is the placeholder target inline writes for an inline file, its
// media id after; adfWithFiles swaps it for the file's link.
const fileRef = "inline-file:"

var fileRe = regexp.MustCompile(`\[file\]\(` + fileRef + `([^)\s]+)\)`)

// writeBlocks renders a slice of block-level nodes, each separated by a blank
// line. indent is the running left margin for nested list items.
func writeBlocks(b *strings.Builder, nodes []adfNode, indent string) {
	for _, n := range nodes {
		writeBlock(b, n, indent)
	}
}

func writeBlock(b *strings.Builder, n adfNode, indent string) {
	switch n.Type {
	case "paragraph":
		if line := strings.Trim(inline(n.Content), "\n"); strings.TrimSpace(line) != "" {
			b.WriteString(indent + line + "\n\n")
		}
	case "heading":
		level := 2
		if v, ok := n.Attrs["level"].(float64); ok && v >= 1 && v <= 6 {
			level = int(v)
		}
		b.WriteString(strings.Repeat("#", level) + " " + inline(n.Content) + "\n\n")
	case "bulletList":
		// laneway-server's page tree (attrs.lanewayTree): the list between
		// tree markers, for the web to draw collapsible.
		tree, isTree := n.Attrs["lanewayTree"].(map[string]any)
		if isTree {
			b.WriteString(indent + treeOpen(tree) + "\n\n")
		}
		writeList(b, n.Content, indent, "- ")
		if isTree {
			b.WriteString(indent + treeClose + "\n\n")
		}
	case "orderedList":
		writeOrderedList(b, n.Content, indent, listOrder(n))
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		b.WriteString("```" + lang + "\n" + codeText(n.Content) + "\n```\n\n")
	case "blockquote":
		var inner strings.Builder
		writeBlocks(&inner, n.Content, "")
		for _, line := range strings.Split(strings.TrimRight(inner.String(), "\n"), "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\n")
	case "rule":
		b.WriteString("---\n\n")
	case "table":
		writeTable(b, n)
	case "taskList":
		writeTaskList(b, n.Content, indent)
		if indent == "" {
			b.WriteString("\n")
		}
	case "decisionList":
		for _, item := range n.Content {
			b.WriteString(indent + "<> " + inline(item.Content) + "\n")
		}
		if indent == "" {
			b.WriteString("\n")
		}
	case "panel":
		typ, _ := n.Attrs["panelType"].(string)
		if !panelOpen.MatchString("<!-- panel:" + typ + " -->") {
			writeBlocks(b, n.Content, indent)
			break
		}
		b.WriteString("<!-- panel:" + typ + " -->\n\n")
		writeBlocks(b, n.Content, "")
		b.WriteString(panelClose + "\n\n")
	case "expand", "nestedExpand":
		open := "<!-- expand -->"
		if title, _ := n.Attrs["title"].(string); title != "" {
			open = "<!-- expand: " + title + " -->"
		}
		b.WriteString(open + "\n\n")
		writeBlocks(b, n.Content, "")
		b.WriteString(expandClose + "\n\n")
	case "blockCard", "embedCard":
		if href, ok := n.Attrs["url"].(string); ok {
			b.WriteString(indent + "<!-- card: " + mdHref(href) + " -->\n\n")
		}
	case "extension", "bodiedExtension":
		// A macro (Confluence's, mostly): named where it stood, its body after.
		name, _ := n.Attrs["extensionKey"].(string)
		b.WriteString(indent + "_[" + cmp.Or(name, "macro") + " macro]_\n\n")
		writeBlocks(b, n.Content, indent)
	case "mediaGroup", "mediaSingle":
		// Each media names its file in alt and its media file in id;
		// toIssue resolves them to an attachment (see resolveMedia). A
		// picture's size and place ride in the title: "320px wrap-left".
		title := ""
		if n.Type == "mediaSingle" {
			title = mediaTitle(n.Attrs)
		}
		wrote := false
		for _, c := range n.Content {
			if c.Type != "media" {
				continue
			}
			alt, _ := c.Attrs["alt"].(string)
			ref := mediaRef
			if id, _ := c.Attrs["id"].(string); mediaIDRe.MatchString(id) {
				ref += "#" + id
			} else if alt == "" {
				continue
			}
			b.WriteString(indent + "![" + escapeMediaAlt(alt) + "](" + ref + title + ")\n\n")
			wrote = true
		}
		if !wrote {
			b.WriteString(indent + "_[attachment]_\n\n")
		}
		for _, c := range n.Content {
			if cap := strings.TrimSpace(inline(c.Content)); c.Type == "caption" && cap != "" {
				b.WriteString(indent + "_" + cap + "_\n\n") // the image's caption, under it
			}
		}
	case "layoutSection":
		// Confluence's columns: each between column markers, its width (a
		// percentage) in its own. The web lays them out side by side; the
		// TUI reads them one after the other.
		b.WriteString(indent + "<!-- layout -->\n\n")
		writeBlocks(b, n.Content, indent)
		b.WriteString(indent + layoutClose + "\n\n")
	case "layoutColumn":
		open := "<!-- column -->"
		if w, ok := n.Attrs["width"].(float64); ok && w > 0 && w <= 100 {
			open = "<!-- column: " + strconv.FormatFloat(w, 'f', -1, 64) + " -->"
		}
		b.WriteString(indent + open + "\n\n")
		writeBlocks(b, n.Content, indent)
		b.WriteString(indent + columnClose + "\n\n")
	default:
		// Unknown block: recurse so nested text isn't lost.
		if len(n.Content) > 0 {
			writeBlocks(b, n.Content, indent)
		}
	}
}

// writeList renders bullet list items, recursing into nested lists with a
// deeper indent.
func writeList(b *strings.Builder, items []adfNode, indent, marker string) {
	for _, item := range items {
		writeListItem(b, item, indent, marker)
	}
	// A blank line after the list only when at the top level, so nested lists
	// stay attached to their parent item.
	if indent == "" {
		b.WriteString("\n")
	}
}

func writeOrderedList(b *strings.Builder, items []adfNode, indent string, order int) {
	for i, item := range items {
		writeListItem(b, item, indent, itoa(order+i)+". ")
	}
	if indent == "" {
		b.WriteString("\n")
	}
}

// listOrder is the number an ordered list starts at.
func listOrder(n adfNode) int {
	if o, ok := n.Attrs["order"].(float64); ok && o >= 0 {
		return int(o)
	}
	return 1
}

// writeTaskList renders task items as "- [ ] " / "- [x] " lines, a nested
// task list one step deeper.
func writeTaskList(b *strings.Builder, items []adfNode, indent string) {
	for _, it := range items {
		switch it.Type {
		case "taskItem":
			box := "[ ]"
			if it.Attrs["state"] == "DONE" {
				box = "[x]"
			}
			b.WriteString(indent + "- " + box + " " + inline(it.Content) + "\n")
		case "taskList":
			writeTaskList(b, it.Content, indent+"  ")
		}
	}
}

// writeTable renders a table as a GFM pipe table. A table without a header
// row gets an empty one, which GFM requires. What a pipe table can't hold
// (a list in a cell, a merged cell) reads flattened.
func writeTable(b *strings.Builder, n adfNode) {
	cols := 0
	for _, row := range n.Content {
		cols = max(cols, len(row.Content))
	}
	if cols == 0 {
		return
	}
	rows := n.Content
	line := func(cells []string) {
		for len(cells) < cols {
			cells = append(cells, "")
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	if headerRow(n) {
		line(cellTexts(rows[0], true))
		rows = rows[1:]
	} else {
		line(nil)
	}
	line(slices.Repeat([]string{"---"}, cols))
	for _, row := range rows {
		line(cellTexts(row, false))
	}
	b.WriteString("\n")
}

// headerRow reports whether a table's first row is all header cells.
func headerRow(table adfNode) bool {
	return len(table.Content) > 0 && len(table.Content[0].Content) > 0 &&
		!slices.ContainsFunc(table.Content[0].Content, func(c adfNode) bool { return c.Type != "tableHeader" })
}

// cellTexts is a row's cells as one line each, pipes escaped. A cell's
// background leads it as <!-- bg:#rrggbb -->, a header cell off the
// header row as <!-- th -->.
func cellTexts(row adfNode, header bool) []string {
	var out []string
	for _, cell := range row.Content {
		text := strings.ReplaceAll(cellText(cell.Content), "|", `\|`)
		if cell.Type == "tableHeader" && !header {
			text = "<!-- th --> " + text
		}
		if bg, _ := cell.Attrs["background"].(string); cssColor.MatchString(bg) {
			text = "<!-- bg:" + bg + " --> " + text
		}
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

// statusColor is a status lozenge's colour name.
var statusColor = regexp.MustCompile(`^(neutral|purple|blue|red|yellow|green)$`)

// cellText writes a cell's blocks on one line: a line break as <br>, the
// next paragraph after <br><br>; what else a cell holds flattened.
func cellText(nodes []adfNode) string {
	var parts []string
	for _, n := range nodes {
		s := flatText([]adfNode{n})
		switch n.Type {
		case "paragraph":
			s = strings.ReplaceAll(strings.Trim(inline(n.Content), " \n"), "\n", "<br>")
		case "codeBlock": // its lines kept, each as code
			s = codeCell(n)
		}
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "<br><br>")
}

// codeCell is a code block in a table cell: each line inline code, joined
// by <br>.
func codeCell(n adfNode) string {
	var text strings.Builder
	for _, c := range n.Content {
		text.WriteString(c.Text)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(text.String(), "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			lines = append(lines, "")
			continue
		}
		fence := "`"
		for strings.Contains(l, fence) {
			fence += "`"
		}
		pad := ""
		if strings.HasPrefix(l, "`") || strings.HasSuffix(l, "`") {
			pad = " "
		}
		lines = append(lines, fence+pad+l+pad+fence)
	}
	return strings.Join(lines, "<br>")
}

// flatText flattens blocks onto one line.
func flatText(nodes []adfNode) string {
	var parts []string
	for _, n := range nodes {
		var s string
		switch n.Type {
		case "paragraph", "heading", "taskItem", "decisionItem":
			s = inline(n.Content)
		case "text":
			s = n.Text
		case "codeBlock":
			s = "`" + codeText(n.Content) + "`"
		default:
			s = flatText(n.Content)
		}
		if s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " ")); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// CellLines is a table cell's text split on its unescaped <br>s; an empty
// line between two starts a paragraph.
func CellLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\':
			i++
		case strings.HasPrefix(s[i:], "<br>"):
			out = append(out, s[start:i])
			i += len("<br>") - 1
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// writeListItem renders one listItem: its first paragraph on the marker line,
// then any nested lists indented beneath it.
func writeListItem(b *strings.Builder, item adfNode, indent, marker string) {
	for i, child := range item.Content {
		switch {
		case child.Type == "paragraph" && i == 0:
			b.WriteString(indent + marker + inline(child.Content) + "\n")
		case child.Type == "bulletList":
			writeList(b, child.Content, indent+"  ", "- ")
		case child.Type == "orderedList":
			writeOrderedList(b, child.Content, indent+"  ", listOrder(child))
		case child.Type == "paragraph":
			b.WriteString(indent + strings.Repeat(" ", len(marker)) + inline(child.Content) + "\n")
		default:
			writeBlock(b, child, indent+"  ")
		}
	}
}

// codeText concatenates the raw text of a code block's children, ignoring marks
// (code is already verbatim).
func codeText(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range nodes {
		if n.Type == "hardBreak" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(n.Text)
	}
	return b.String()
}

// inline renders a run of inline nodes (the children of a paragraph/heading)
// into a single markdown line.
func inline(nodes []adfNode) string {
	var b strings.Builder
	for _, n := range mergeTexts(nodes) {
		switch n.Type {
		case "text":
			b.WriteString(applyMarks(n.Text, n.Marks))
		case "hardBreak":
			b.WriteString("\n")
		case "mention":
			if name, ok := n.Attrs["text"].(string); ok {
				b.WriteString("@" + strings.TrimPrefix(name, "@"))
			}
		case "emoji":
			b.WriteString(emojiText(n))
		case "mediaInline": // a file in the line: its media id, which adfWithFiles names
			if id, _ := n.Attrs["id"].(string); id != "" {
				b.WriteString("[file](" + fileRef + id + ")")
			} else {
				b.WriteString("_[file]_")
			}
		case "inlineExtension": // a macro in the line
			name, _ := n.Attrs["extensionKey"].(string)
			b.WriteString("_[" + cmp.Or(name, "macro") + " macro]_")
		case "inlineCard": // <https://…>, a smart link
			if href, ok := n.Attrs["url"].(string); ok {
				b.WriteString("<" + mdHref(href) + ">")
			}
		case "date": // attrs.timestamp: ms since the epoch, a UTC midnight
			if ms, ok := adfMillis(n.Attrs["timestamp"]); ok {
				b.WriteString("<date>" + time.UnixMilli(ms).UTC().Format("2006-01-02") + "</date>")
			}
		case "status": // <status color="green">DONE</status>, as the panel draws it
			if txt, ok := n.Attrs["text"].(string); ok && txt != "" {
				if n.Attrs["style"] != "mixedCase" {
					txt = strings.ToUpper(txt)
				}
				color, _ := n.Attrs["color"].(string)
				if !statusColor.MatchString(color) {
					color = "neutral"
				}
				b.WriteString(`<status color="` + color + `">` + txt + "</status>")
			}
		default:
			if len(n.Content) > 0 {
				b.WriteString(inline(n.Content))
			}
		}
	}
	return b.String()
}

// mergeTexts joins adjacent text nodes carrying the same marks, so
// "**a b**" writes as one span rather than "**a** **b**".
func mergeTexts(nodes []adfNode) []adfNode {
	var out []adfNode
	for _, n := range nodes {
		if l := len(out) - 1; l >= 0 && n.Type == "text" && out[l].Type == "text" && sameMarks(out[l].Marks, n.Marks) {
			out[l].Text += n.Text
			continue
		}
		out = append(out, n)
	}
	return out
}

func sameMarks(a, b []adfMark) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// adfMillis reads a date node's timestamp: a string by the spec, a number
// from some clients.
func adfMillis(v any) (int64, bool) {
	switch t := v.(type) {
	case string:
		ms, err := strconv.ParseInt(t, 10, 64)
		return ms, err == nil
	case float64:
		return int64(t), true
	}
	return 0, false
}

// applyMarks wraps text in the markdown for each of its marks. A link mark wraps
// last so the visible text keeps its other styling inside the [..](..).
func applyMarks(text string, marks []adfMark) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	var href, title string
	marks = slices.Clone(marks) // in one order, whatever order Jira keeps them in
	slices.SortStableFunc(marks, func(a, b adfMark) int { return markRank(a.Type) - markRank(b.Type) })
	for _, mk := range marks {
		switch mk.Type {
		case "strong":
			text = "**" + text + "**"
		case "em":
			text = "*" + text + "*"
		case "code":
			text = "`" + text + "`"
		case "strike":
			text = "~~" + text + "~~"
		case "underline":
			text = "<u>" + text + "</u>"
		case "subsup":
			if t, _ := mk.Attrs["type"].(string); t == "sub" || t == "sup" {
				text = "<" + t + ">" + text + "</" + t + ">"
			}
		case "textColor", "backgroundColor":
			if c, _ := mk.Attrs["color"].(string); cssColor.MatchString(c) {
				prop := "color"
				if mk.Type == "backgroundColor" {
					prop = "background-color"
				}
				text = `<span style="` + prop + ":" + c + `">` + text + "</span>"
			}
		case "link":
			if h, ok := mk.Attrs["href"].(string); ok {
				href = h
			}
			title, _ = mk.Attrs["title"].(string)
		}
	}
	if href != "" {
		if title != "" && !strings.ContainsAny(title, "\")\n") {
			href = mdHref(href) + ` "` + title + `"` // [text](url "title"); a title it can't hold stays kept
		} else {
			href = mdHref(href)
		}
		text = "[" + text + "](" + href + ")"
	}
	return text
}

// markRank orders marks innermost first as applyMarks writes them.
func markRank(typ string) int {
	return slices.Index([]string{"code", "em", "strong", "strike", "underline", "subsup", "textColor", "backgroundColor", "link"}, typ)
}

// mdHref percent-encodes the characters that end a markdown link's URL early.
var mdHref = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29").Replace

// textToADF builds a minimal ADF document from plain text for posting a
// comment (the inverse of adfToMarkdown, much narrower). Blank lines separate
// paragraphs; single newlines within a paragraph become hardBreaks; a run of
// lines beginning with ">" becomes a blockquote (so a quoted reply renders as
// one). When mention is non-nil a real mention node — the only form Jira
// notifies on — plus a space is prepended to the first body paragraph, or
// added as its own trailing paragraph when the body is empty or only a quote.
func textToADF(text string, mention *Mention) map[string]any {
	blocks := parseADFBlocks(text)
	if mention != nil {
		nodes := []any{
			map[string]any{
				"type":  "mention",
				"attrs": map[string]any{"id": mention.AccountID, "text": "@" + mention.DisplayName},
			},
			map[string]any{"type": "text", "text": " "},
		}
		blocks = prependMention(blocks, nodes)
	}
	if len(blocks) == 0 {
		// Jira rejects an empty doc; a single space keeps it valid.
		blocks = []any{paragraphNode([]string{" "})}
	}
	doc := map[string]any{"type": "doc", "version": 1, "content": blocks}
	splitTexts(doc, splitEmoji)
	return doc
}

// splitEmoji cuts text into text and emoji nodes, one per :shortcode: the
// emoji table knows.
func splitEmoji(text string) []any {
	var out []any
	from := 0
	for i := 0; i < len(text); i++ {
		short, n, ok := emojiAt(text, i)
		if !ok {
			continue
		}
		if i > from {
			out = append(out, map[string]any{"type": "text", "text": text[from:i]})
		}
		out = append(out, map[string]any{"type": "emoji", "attrs": map[string]any{"shortName": short, "text": emoji.Glyph(short[1 : len(short)-1])}})
		i += n - 1
		from = i + 1
	}
	if from < len(text) {
		out = append(out, map[string]any{"type": "text", "text": text[from:]})
	}
	return out
}

// parseADFBlocks splits plain text into ADF block nodes (paragraphs and
// blockquotes), grouping consecutive ">" lines into a single blockquote.
func parseADFBlocks(text string) []any {
	var blocks []any
	var para, quote []string

	flushPara := func() {
		if len(para) > 0 {
			blocks = append(blocks, paragraphNode(para))
			para = nil
		}
	}
	flushQuote := func() {
		if len(quote) > 0 {
			blocks = append(blocks, map[string]any{
				"type":    "blockquote",
				"content": []any{paragraphNode(quote)},
			})
			quote = nil
		}
	}

	for _, ln := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(ln, ">"):
			flushPara()
			quote = append(quote, strings.TrimPrefix(strings.TrimPrefix(ln, ">"), " "))
		case strings.TrimSpace(ln) == "":
			flushPara()
			flushQuote()
		default:
			flushQuote()
			para = append(para, ln)
		}
	}
	flushPara()
	flushQuote()
	return blocks
}

// paragraphNode builds a paragraph node whose lines are joined by hardBreaks.
func paragraphNode(lines []string) map[string]any {
	var content []any
	for i, ln := range lines {
		if i > 0 {
			content = append(content, map[string]any{"type": "hardBreak"})
		}
		if ln != "" {
			content = append(content, map[string]any{"type": "text", "text": ln})
		}
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": " "})
	}
	return map[string]any{"type": "paragraph", "content": content}
}

// prependMention inserts nodes at the start of the first paragraph block, so a
// reply's @mention sits with the author's own text. When there's no paragraph
// (e.g. the body is only a quote) it appends a paragraph carrying just nodes.
func prependMention(blocks []any, nodes []any) []any {
	for i, blk := range blocks {
		m, ok := blk.(map[string]any)
		if !ok || m["type"] != "paragraph" {
			continue
		}
		content, _ := m["content"].([]any)
		m["content"] = append(append([]any{}, nodes...), content...)
		blocks[i] = m
		return blocks
	}
	return append(blocks, map[string]any{"type": "paragraph", "content": nodes})
}

// itoa is a tiny strconv.Itoa to avoid the import for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// inlineMentions turns each "@Name" in the document's text into a mention of
// that person, longest names first so "@Ada Lovelace" wins over "@Ada".
func inlineMentions(node map[string]any, ms []Mention) {
	if len(ms) == 0 {
		return
	}
	ms = slices.Clone(ms)
	slices.SortFunc(ms, func(a, b Mention) int { return len(b.DisplayName) - len(a.DisplayName) })
	splitTexts(node, func(text string) []any { return splitMentions(text, ms) })
}

// mentioned is each person a's description and comments mention, once per
// name a mention reads.
func mentioned(a apiIssue) []User {
	raws := []json.RawMessage{a.Fields.Description}
	if a.Fields.Comment != nil {
		for _, ac := range a.Fields.Comment.Comments {
			raws = append(raws, ac.Body)
		}
	}
	var out []User
	var walk func(n adfNode)
	walk = func(n adfNode) {
		if n.Type == "mention" {
			id, _ := n.Attrs["id"].(string)
			text, _ := n.Attrs["text"].(string)
			name := safeterm.Line(strings.TrimPrefix(text, "@"))
			if name != "" && !slices.ContainsFunc(out, func(u User) bool { return u.DisplayName == name }) {
				out = append(out, User{AccountID: id, DisplayName: name})
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	for _, raw := range raws {
		var doc adfNode
		if bytes.Contains(raw, []byte(`"mention"`)) && json.Unmarshal(raw, &doc) == nil {
			walk(doc)
		}
	}
	return out
}

// splitTexts replaces each unmarked text node under node, outside code
// blocks, by split's nodes.
func splitTexts(node map[string]any, split func(string) []any) {
	content, ok := node["content"].([]any)
	if !ok || node["type"] == "codeBlock" {
		return
	}
	var out []any
	for _, c := range content {
		cm, ok := c.(map[string]any)
		if !ok {
			out = append(out, c)
			continue
		}
		if cm["type"] == "text" && cm["marks"] == nil {
			out = append(out, split(cm["text"].(string))...)
			continue
		}
		splitTexts(cm, split)
		out = append(out, cm)
	}
	node["content"] = out
}

// splitMentions cuts text into text and mention nodes.
func splitMentions(text string, ms []Mention) []any {
	for _, m := range ms {
		at := "@" + m.DisplayName
		if i := strings.Index(text, at); i >= 0 {
			var out []any
			if i > 0 {
				out = append(out, splitMentions(text[:i], ms)...)
			}
			out = append(out, map[string]any{"type": "mention", "attrs": map[string]any{"id": m.AccountID, "text": at}})
			if rest := text[i+len(at):]; rest != "" {
				out = append(out, splitMentions(rest, ms)...)
			}
			return out
		}
	}
	return []any{map[string]any{"type": "text", "text": text}}
}

// emojiText is how an emoji node reads: its :shortcode: when the emoji
// table knows it (the panel draws it, an edit keeps it), else its text,
// the glyph Jira gives a skin tone or a custom emoji.
func emojiText(n adfNode) string {
	short, _ := n.Attrs["shortName"].(string)
	text, _ := n.Attrs["text"].(string)
	if name := strings.Trim(short, ":"); mdEmoji.MatchString(short) && len(name)+2 == len(short) && emoji.Glyph(name) != "" || text == "" {
		return short
	}
	return text
}

const (
	treeClose   = "<!-- /tree -->"
	layoutClose = "<!-- /layout -->"
	columnClose = "<!-- /column -->"
)

// treeOpen is a tree's marker: <!-- tree: open=2, toggle, search -->, the
// levels shown at first, then expand/collapse all and a filter box when on.
func treeOpen(t map[string]any) string {
	open, _ := t["open"].(float64)
	s := "<!-- tree: open=" + strconv.Itoa(max(int(open), 1))
	if on, _ := t["toggleAll"].(bool); on {
		s += ", toggle"
	}
	if on, _ := t["search"].(bool); on {
		s += ", search"
	}
	return s + " -->"
}
