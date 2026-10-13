package jira

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Against Confluence the client expands, when it reads a page, the macros
// laneway-server expands for it (internal/macro there): toc, children and
// pagetree, as the same plain ADF. The pages come from the space's page
// list, which holds what the reader may read. Any other macro stays its
// placeholder.

// wikiPageRef is the page being read.
type wikiPageRef struct{ ID, SpaceID, ParentID string }

// treePage is one page of a space's list, for children and pagetree.
type treePage struct {
	ID       json.Number `json:"id"`
	Title    string      `json:"title"`
	ParentID json.Number `json:"parentId"`
	Position json.Number `json:"position"`
	Created  string      `json:"createdAt"`
	Version  struct {
		Created string `json:"createdAt"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

// wikiMacros expands one page's macros; the space lists are read once.
type wikiMacros struct {
	c      *Client
	ctx    context.Context
	page   wikiPageRef
	doc    []adfNode
	spaces map[string][]treePage // by space id
	keys   map[string]string     // space key → id
	infos  map[string][2]string  // space id → key, home page
}

// expandPageMacros answers raw with toc, children and pagetree expanded;
// raw as is when it has none.
func (c *Client) expandPageMacros(ctx context.Context, raw json.RawMessage, page wikiPageRef) json.RawMessage {
	var doc adfNode
	if !strings.Contains(string(raw), `"extensionKey"`) || json.Unmarshal(raw, &doc) != nil || doc.Type != "doc" {
		return raw
	}
	w := &wikiMacros{c: c, ctx: ctx, page: page, doc: doc.Content, spaces: map[string][]treePage{}, keys: map[string]string{}, infos: map[string][2]string{}}
	doc.Content = w.expandAll(doc.Content)
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

func (w *wikiMacros) expandAll(ns []adfNode) []adfNode {
	var out []adfNode
	for _, n := range ns {
		switch n.Type {
		case "extension", "inlineExtension":
			key, _ := n.Attrs["extensionKey"].(string)
			if got, ok := w.expand(key, macroParams(n.Attrs)); ok {
				out = append(out, got...)
				continue
			}
			out = append(out, n)
		case "paragraph", "heading":
			n.Content = w.expandAll(n.Content)
			out = append(out, splitLine(n)...)
		default:
			n.Content = w.expandAll(n.Content)
			out = append(out, n)
		}
	}
	return out
}

// expand answers a macro's nodes; ok is false for a macro left as it is.
func (w *wikiMacros) expand(key string, p map[string]string) (out []adfNode, ok bool) {
	switch key {
	case "toc":
		out, ok = tocMacro(w.doc, p)
	case "children":
		out, ok = w.children(p)
	case "pagetree":
		out, ok = w.pagetree(p)
	case "recently-updated":
		out, ok = w.recentlyUpdated(p)
	}
	return out, ok
}

// macroParams reads attrs.parameters.macroParams: {name: {value: …}}.
func macroParams(attrs map[string]any) map[string]string {
	out := map[string]string{}
	ps, _ := attrs["parameters"].(map[string]any)
	mp, _ := ps["macroParams"].(map[string]any)
	for k, v := range mp {
		if m, ok := v.(map[string]any); ok {
			out[k], _ = m["value"].(string)
		}
	}
	return out
}

// blockTypes are the nodes that can't stand in a line.
var blockTypes = map[string]bool{"paragraph": true, "heading": true, "bulletList": true, "orderedList": true, "taskList": true,
	"decisionList": true, "table": true, "panel": true, "expand": true, "nestedExpand": true, "codeBlock": true, "blockquote": true,
	"rule": true, "mediaSingle": true, "mediaGroup": true, "layoutSection": true}

// splitLine splits a paragraph or heading around the blocks an inline
// macro answered (Confluence keeps a page tree in a line).
func splitLine(n adfNode) []adfNode {
	if !slices.ContainsFunc(n.Content, func(k adfNode) bool { return blockTypes[k.Type] }) {
		return []adfNode{n}
	}
	var out []adfNode
	line := n
	line.Content = nil
	flush := func() {
		if slices.ContainsFunc(line.Content, func(k adfNode) bool { return k.Type != "text" || strings.TrimSpace(k.Text) != "" }) {
			out = append(out, line)
		}
		line.Content = nil
	}
	for _, k := range n.Content {
		if blockTypes[k.Type] {
			flush()
			out = append(out, k)
			continue
		}
		line.Content = append(line.Content, k)
	}
	flush()
	return out
}

func para(text string, marks ...adfMark) adfNode {
	return adfNode{Type: "paragraph", Content: []adfNode{{Type: "text", Text: text, Marks: marks}}}
}

func noPage(ref string) []adfNode {
	return []adfNode{para("No page "+ref+".", adfMark{Type: "em"})}
}

func atoiOr(s string, or int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return or
}

// ---- toc: the page's headings, as a nested list or one line

type tocItem struct {
	level int
	text  string
	kids  []*tocItem
}

func tocMacro(doc []adfNode, p map[string]string) ([]adfNode, bool) {
	minLevel, maxLevel := atoiOr(p["minLevel"], 1), atoiOr(p["maxLevel"], 7)
	include, err1 := wholeMatch(p["include"])
	exclude, err2 := wholeMatch(p["exclude"])
	if err1 != nil || err2 != nil {
		return nil, false
	}
	root := &tocItem{}
	stack := []*tocItem{root}
	for _, h := range tocHeadings(doc, nil) {
		if h.level < minLevel || h.level > maxLevel || include != nil && !include.MatchString(h.text) || exclude != nil && exclude.MatchString(h.text) {
			continue
		}
		for len(stack) > 1 && stack[len(stack)-1].level >= h.level {
			stack = stack[:len(stack)-1]
		}
		stack[len(stack)-1].kids = append(stack[len(stack)-1].kids, h)
		stack = append(stack, h)
	}
	if len(root.kids) == 0 {
		return nil, true
	}
	outline := p["outline"] == "true"
	if p["type"] == "flat" {
		var texts []string
		var flatten func(items []*tocItem, prefix string)
		flatten = func(items []*tocItem, prefix string) {
			for i, it := range items {
				num := prefix + strconv.Itoa(i+1)
				texts = append(texts, numbered(outline, num, it.text))
				flatten(it.kids, num+".")
			}
		}
		flatten(root.kids, "")
		return []adfNode{para(flatLine(texts, cmp.Or(p["separator"], "brackets")))}, true
	}
	return []adfNode{tocList(root.kids, "", outline)}, true
}

func tocList(items []*tocItem, prefix string, outline bool) adfNode {
	list := adfNode{Type: "bulletList"}
	for i, it := range items {
		num := prefix + strconv.Itoa(i+1)
		li := adfNode{Type: "listItem", Content: []adfNode{para(numbered(outline, num, it.text))}}
		if len(it.kids) > 0 {
			li.Content = append(li.Content, tocList(it.kids, num+".", outline))
		}
		list.Content = append(list.Content, li)
	}
	return list
}

func numbered(outline bool, num, text string) string {
	if outline {
		return num + " " + text
	}
	return text
}

func flatLine(texts []string, separator string) string {
	switch separator {
	case "brackets":
		return "[ " + strings.Join(texts, " ] [ ") + " ]"
	case "braces":
		return "{ " + strings.Join(texts, " } { ") + " }"
	case "parens":
		return "( " + strings.Join(texts, " ) ( ") + " )"
	case "pipe":
		return strings.Join(texts, " | ")
	}
	return strings.Join(texts, separator)
}

func tocHeadings(ns []adfNode, out []*tocItem) []*tocItem {
	for _, n := range ns {
		if n.Type == "heading" {
			level, _ := n.Attrs["level"].(float64)
			if t := strings.TrimSpace(nodeText(n)); t != "" {
				out = append(out, &tocItem{level: int(level), text: t})
			}
			continue
		}
		out = tocHeadings(n.Content, out)
	}
	return out
}

func nodeText(n adfNode) string {
	s := n.Text
	for _, k := range n.Content {
		s += nodeText(k)
	}
	return s
}

func wholeMatch(p string) (*regexp.Regexp, error) {
	if p == "" {
		return nil, nil
	}
	return regexp.Compile("^(?:" + p + ")$")
}

// ---- children and pagetree: the space's pages

// treeMaxDepth bounds an "all" tree, in case it loops.
const treeMaxDepth = 50

// pages is a space's list, read once.
func (w *wikiMacros) pages(spaceID string) []treePage {
	if ps, ok := w.spaces[spaceID]; ok {
		return ps
	}
	ps, _ := wikiPages[treePage](w.ctx, w.c, "/wiki/api/v2/spaces/"+url.PathEscape(spaceID)+"/pages?limit=250&status=current", "space "+spaceID+" pages", 5000)
	w.spaces[spaceID] = ps
	return ps
}

// space is a space key's id, "" when there is none.
func (w *wikiMacros) space(key string) string {
	if id, ok := w.keys[key]; ok {
		return id
	}
	var resp struct {
		Results []struct {
			ID json.Number `json:"id"`
		} `json:"results"`
	}
	id := ""
	if err := w.c.do(w.ctx, http.MethodGet, "/wiki/api/v2/spaces?keys="+url.QueryEscape(key), "space "+key, nil, &resp); err == nil && len(resp.Results) > 0 {
		id = resp.Results[0].ID.String()
	}
	w.keys[key] = id
	return id
}

// kids are the pages under parent ("" for the space's top), sorted by p.
func (w *wikiMacros) kids(spaceID, parent string, p map[string]string) []treePage {
	ps := w.pages(spaceID)
	ids := map[string]bool{}
	for _, pg := range ps {
		ids[pg.ID.String()] = true
	}
	var out []treePage
	for _, pg := range ps {
		under := pg.ParentID.String()
		if !ids[under] {
			under = "" // under the space, or a page or folder the list leaves out
		}
		if under == parent {
			out = append(out, pg)
		}
	}
	// The sidebar's order: position, then title.
	slices.SortStableFunc(out, func(a, b treePage) int {
		return cmp.Or(cmp.Compare(atoiOr(a.Position.String(), 0), atoiOr(b.Position.String(), 0)),
			strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)))
	})
	var key func(a, b treePage) int
	switch p["sort"] {
	case "creation":
		key = func(a, b treePage) int { return strings.Compare(a.Created, b.Created) }
	case "modified":
		key = func(a, b treePage) int { return strings.Compare(a.Version.Created, b.Version.Created) }
	case "title", "natural":
		key = func(a, b treePage) int { return naturalCompare(strings.ToLower(a.Title), strings.ToLower(b.Title)) }
	case "bitwise":
		key = func(a, b treePage) int { return strings.Compare(a.Title, b.Title) }
	}
	if key != nil {
		slices.SortStableFunc(out, key)
	}
	if p["reverse"] == "true" {
		slices.Reverse(out)
	}
	return out
}

// titled is the page of spaceID titled title, nil for none.
func (w *wikiMacros) titled(spaceID, title string) *treePage {
	for _, pg := range w.pages(spaceID) {
		if strings.EqualFold(pg.Title, title) {
			return &pg
		}
	}
	return nil
}

func (w *wikiMacros) list(spaceID string, ps []treePage, depth int, p map[string]string) adfNode {
	ul := adfNode{Type: "bulletList"}
	for _, pg := range ps {
		li := adfNode{Type: "listItem", Content: []adfNode{w.pageLine(pg)}}
		if depth > 1 {
			if ks := w.kids(spaceID, pg.ID.String(), p); len(ks) > 0 {
				li.Content = append(li.Content, w.list(spaceID, ks, depth-1, p))
			}
		}
		ul.Content = append(ul.Content, li)
	}
	return ul
}

func (w *wikiMacros) pageLine(pg treePage) adfNode {
	href := w.c.wikiURL + "/wiki" + pg.Links.WebUI
	if pg.Links.WebUI == "" {
		href = w.c.wikiURL + "/wiki/pages/viewpage.action?pageId=" + pg.ID.String()
	}
	return para(cmp.Or(pg.Title, "Page "+pg.ID.String()), adfMark{Type: "link", Attrs: map[string]any{"href": href}})
}

// target reads a page reference: "KEY:Title", "KEY:" (the space's top) or
// a title in this space.
func (w *wikiMacros) target(ref string) (spaceID, parent string, ok bool) {
	spaceID = w.page.SpaceID
	if key, title, found := strings.Cut(ref, ":"); found {
		if spaceID = w.space(key); spaceID == "" {
			return "", "", false
		}
		if ref = title; ref == "" {
			return spaceID, "", true
		}
	}
	if pg := w.titled(spaceID, ref); pg != nil {
		return spaceID, pg.ID.String(), true
	}
	return "", "", false
}

func (w *wikiMacros) children(p map[string]string) ([]adfNode, bool) {
	spaceID, parent, ok := w.page.SpaceID, w.page.ID, true
	switch ref := p["page"]; ref {
	case "", "current":
	case "/":
		parent = ""
	default:
		if spaceID, parent, ok = w.target(ref); !ok {
			return noPage(ref), true
		}
	}
	depth := atoiOr(cmp.Or(p["depth"], "1"), 1)
	if p["all"] == "true" || depth < 1 {
		depth = treeMaxDepth
	}
	top := w.kids(spaceID, parent, p)
	if n := atoiOr(p["first"], 0); n > 0 && n < len(top) {
		top = top[:n]
	}
	if len(top) == 0 {
		return nil, true
	}
	if s := p["style"]; len(s) == 2 && s[0] == 'h' && s[1] >= '1' && s[1] <= '6' {
		var out []adfNode
		for _, pg := range top {
			out = append(out, adfNode{Type: "heading", Attrs: map[string]any{"level": float64(s[1] - '0')}, Content: w.pageLine(pg).Content})
			if ks := w.kids(spaceID, pg.ID.String(), p); depth > 1 && len(ks) > 0 {
				out = append(out, w.list(spaceID, ks, depth-1, p))
			}
		}
		return out, true
	}
	return []adfNode{w.list(spaceID, top, depth, p)}, true
}

func (w *wikiMacros) pagetree(p map[string]string) ([]adfNode, bool) {
	spaceID, parent := w.page.SpaceID, ""
	switch root := cmp.Or(p["root"], "@home"); root {
	case "@self":
		parent = w.page.ID
	case "@parent":
		parent = w.page.ParentID
	case "@none":
	case "@home":
		parent = w.home(spaceID)
	default:
		var ok bool
		if spaceID, parent, ok = w.target(root); !ok {
			return noPage(root), true
		}
	}
	top := w.kids(spaceID, parent, p)
	if len(top) == 0 {
		return nil, true
	}
	list := w.list(spaceID, top, treeMaxDepth, p)
	list.Attrs = map[string]any{"lanewayTree": map[string]any{
		"open":      float64(max(atoiOr(p["startDepth"], 1), 1)),
		"toggleAll": p["expandCollapseAll"] == "true",
		"search":    p["searchBox"] == "true",
	}}
	return []adfNode{list}, true
}

// home is a space's home page, "" (its top) when it can't be read.
func (w *wikiMacros) home(spaceID string) string { return w.spaceInfo(spaceID)[1] }

// spaceInfo is a space's key and home page, read once.
func (w *wikiMacros) spaceInfo(spaceID string) [2]string {
	if v, ok := w.infos[spaceID]; ok {
		return v
	}
	var resp struct {
		Key        string      `json:"key"`
		HomepageID json.Number `json:"homepageId"`
	}
	var v [2]string
	if w.c.do(w.ctx, http.MethodGet, "/wiki/api/v2/spaces/"+url.PathEscape(spaceID), "space "+spaceID, nil, &resp) == nil {
		v = [2]string{resp.Key, resp.HomepageID.String()}
	}
	w.infos[spaceID] = v
	return v
}

// naturalCompare orders "Step 2" before "Step 10".
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := leadingDigits(a), leadingDigits(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if r := cmp.Or(cmp.Compare(len(na), len(nb)), strings.Compare(na, nb)); r != 0 {
				return r
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func leadingDigits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// ---- recently-updated: a CQL search, newest first

// recentlyUpdated lists the newest changes Confluence's search finds for
// the macro's spaces, labels, types and authors; a search Confluence
// refuses leaves the macro as it is.
func (w *wikiMacros) recentlyUpdated(p map[string]string) ([]adfNode, bool) {
	cql, ok := w.recentCQL(p)
	if !ok {
		return nil, false
	}
	var resp struct {
		Results []struct {
			Title        string `json:"title"`
			URL          string `json:"url"`
			LastModified string `json:"lastModified"`
			Content      struct {
				Type    string `json:"type"`
				Version struct {
					By struct {
						DisplayName string `json:"displayName"`
					} `json:"by"`
				} `json:"version"`
				Container struct {
					Title string `json:"title"`
				} `json:"container"`
			} `json:"content"`
		} `json:"results"`
	}
	n := min(max(atoiOr(cmp.Or(p["max"], "15"), 15), 1), 200)
	path := "/wiki/rest/api/search?limit=" + strconv.Itoa(n) + "&expand=content.version,content.container&cql=" + url.QueryEscape(cql)
	if w.c.do(w.ctx, http.MethodGet, path, "recently updated", nil, &resp) != nil {
		return nil, false
	}
	var out []adfNode
	if p["hideHeading"] != "true" {
		out = append(out, adfNode{Type: "heading", Attrs: map[string]any{"level": float64(3)}, Content: []adfNode{{Type: "text", Text: "Recently Updated"}}})
	}
	if len(resp.Results) == 0 {
		return append(out, para("Nothing updated yet.", adfMark{Type: "em"})), true
	}
	var items []recentChange
	for _, r := range resp.Results {
		text := r.Title
		switch r.Content.Type {
		case "comment":
			text = "Comment on " + cmp.Or(r.Content.Container.Title, strings.TrimPrefix(r.Title, "Re: "))
		case "attachment":
			if r.Content.Container.Title != "" {
				text = r.Title + " on " + r.Content.Container.Title
			}
		}
		it := recentChange{text: text, href: w.c.wikiURL + "/wiki" + r.URL, who: r.Content.Version.By.DisplayName}
		if t, err := time.Parse(time.RFC3339, r.LastModified); err == nil {
			it.when = t
		}
		items = append(items, it)
	}
	return append(out, recentList(items, p["theme"])), true
}

// recentChange is one line of a recently updated list.
type recentChange struct {
	text, href, who string
	when            time.Time
}

// recentList lists changes newest first. concise: each its link, who and
// when; sidebar: no who; social: a person's changes in a row under their
// name and the newest one's date.
func recentList(items []recentChange, theme string) adfNode {
	date := func(t time.Time) []adfNode {
		if t.IsZero() {
			return nil
		}
		return []adfNode{{Type: "text", Text: " · "}, {Type: "date", Attrs: map[string]any{"timestamp": strconv.FormatInt(t.UnixMilli(), 10)}}}
	}
	line := func(it recentChange, who bool) adfNode {
		l := []adfNode{{Type: "text", Text: it.text, Marks: []adfMark{{Type: "link", Attrs: map[string]any{"href": it.href}}}}}
		if who && it.who != "" {
			l = append(l, adfNode{Type: "text", Text: " · " + it.who})
		}
		return adfNode{Type: "listItem", Content: []adfNode{{Type: "paragraph", Content: append(l, date(it.when)...)}}}
	}
	list := adfNode{Type: "bulletList"}
	for i := 0; i < len(items); i++ {
		if theme != "social" {
			list.Content = append(list.Content, line(items[i], theme != "sidebar"))
			continue
		}
		group := adfNode{Type: "bulletList"}
		j := i
		for ; j < len(items) && items[j].who == items[i].who; j++ {
			group.Content = append(group.Content, line(items[j], false))
		}
		head := []adfNode{{Type: "text", Text: cmp.Or(items[i].who, "Someone"), Marks: []adfMark{{Type: "strong"}}}}
		list.Content = append(list.Content, adfNode{Type: "listItem", Content: []adfNode{{Type: "paragraph", Content: append(head, date(items[i].when)...)}, group}})
		i = j - 1
	}
	return list
}

// recentCQL is the macro's search: spaces (@self by default), labels and
// types (a - before one leaves it out, a + one is required) and authors.
func (w *wikiMacros) recentCQL(p map[string]string) (string, bool) {
	quoted := func(ws []string) string {
		q := make([]string, len(ws))
		for i, v := range ws {
			q[i] = strconv.Quote(v)
		}
		return strings.Join(q, ",")
	}
	var clauses []string
	// split sorts a list into its plain, + and - words.
	split := func(s string) (plain, plus, minus []string) {
		for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
			switch {
			case strings.HasPrefix(v, "+"):
				plus = append(plus, v[1:])
			case strings.HasPrefix(v, "-"):
				minus = append(minus, v[1:])
			default:
				plain = append(plain, v)
			}
		}
		return plain, plus, minus
	}
	// Types: those named (all four when none is), less those after a -.
	news := func(t string) string { return strings.Replace(t, "news", "blogpost", 1) }
	plain, plus, minus := split(strings.ToLower(p["types"]))
	types := slices.Concat(plain, plus)
	if len(types) == 0 {
		types = []string{"page", "blogpost", "comment", "attachment"}
	}
	types = slices.DeleteFunc(slices.Clone(types), func(t string) bool {
		return slices.ContainsFunc(minus, func(m string) bool { return news(m) == news(t) })
	})
	for i, t := range types {
		types[i] = news(t)
	}
	if len(types) == 0 {
		return "", false
	}
	clauses = append(clauses, "type in ("+strings.Join(types, ",")+")")
	plain, plus, minus = split(cmp.Or(p["spaces"], "@self"))
	var keys, spaceOr []string
	for _, k := range append(plain, plus...) {
		switch strings.ToLower(k) {
		case "@all", "*":
			spaceOr = append(spaceOr, "") // no space clause
		case "@self":
			key := w.spaceInfo(w.page.SpaceID)[0]
			if key == "" {
				return "", false
			}
			keys = append(keys, key)
		case "@global":
			spaceOr = append(spaceOr, "space.type = global")
		case "@personal":
			spaceOr = append(spaceOr, "space.type = personal")
		case "@favorite", "@favourite":
			spaceOr = append(spaceOr, "space in favouriteSpaces()")
		default:
			keys = append(keys, k)
		}
	}
	if len(keys) > 0 {
		spaceOr = append(spaceOr, "space in ("+quoted(keys)+")")
	}
	if !slices.Contains(spaceOr, "") && len(spaceOr) > 0 {
		clauses = append(clauses, "("+strings.Join(spaceOr, " OR ")+")")
	}
	if len(minus) > 0 {
		clauses = append(clauses, "space not in ("+quoted(minus)+")")
	}
	plain, plus, minus = split(strings.ToLower(p["labels"]))
	if len(plain) > 0 {
		clauses = append(clauses, "label in ("+quoted(plain)+")")
	}
	for _, l := range plus {
		clauses = append(clauses, "label = "+strconv.Quote(l))
	}
	if len(minus) > 0 {
		clauses = append(clauses, "label not in ("+quoted(minus)+")")
	}
	if authors := strings.FieldsFunc(p["author"], func(r rune) bool { return r == ',' || r == ' ' }); len(authors) > 0 {
		clauses = append(clauses, "contributor in ("+quoted(authors)+")")
	}
	return strings.Join(clauses, " AND ") + " order by lastmodified desc", true
}
