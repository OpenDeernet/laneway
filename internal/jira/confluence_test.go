package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPageID(t *testing.T) {
	c := New(Config{BaseURL: "https://acme.atlassian.net", Email: "me@x.test", APIToken: "tok"})
	for u, want := range map[string]string{
		"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456/Release+plan":     "123456",
		"https://acme.atlassian.net/wiki/pages/viewpage.action?pageId=987":         "987",
		"https://other.atlassian.net/wiki/spaces/ENG/pages/1/X":                    "",
		"https://acme.atlassian.net/browse/ABC-1":                                  "",
		"https://acme.atlassian.net/wiki/pages/viewpage.action?pageId=1%3B%20drop": "",
	} {
		if got := c.PageID(u); got != want {
			t.Errorf("PageID(%s) = %q, want %q", u, got, want)
		}
	}
}

// TestConfluencePage: the page's ADF reads as markdown, a macro as a line
// naming it, an image of its own attachments by id, another by its name;
// who saved it last named from the version list; PageImage downloads one.
func TestConfluencePage(t *testing.T) {
	media := func(id, alt string) any {
		return map[string]any{"type": "mediaSingle", "content": []any{map[string]any{"type": "media", "attrs": map[string]any{"id": id, "alt": alt}}}}
	}
	doc, _ := json.Marshal(map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 1}, "content": []any{map[string]any{"type": "text", "text": "Plan"}}},
		map[string]any{"type": "extension", "attrs": map[string]any{"extensionKey": "roadmap"}},
		media("f1", "diagram.png"), media("f9", "elsewhere.png"),
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/pages/42":
			if r.URL.Query().Get("body-format") != "atlas_doc_format" || r.URL.Query().Has("laneway-macros") {
				t.Errorf("%s", r.URL)
			}
			b, _ := json.Marshal(string(doc))
			fmt.Fprintf(w, `{"id":"42","title":"Release plan","version":{"number":4,"authorId":"u3","createdAt":"2026-09-22T08:45:11Z"},"body":{"atlas_doc_format":{"value":%s}},"_links":{"webui":"/spaces/ENG/pages/42"}}`, b)
		case "/rest/api/3/serverInfo":
			fmt.Fprint(w, `{"deploymentType":"Cloud"}`)
		case "/wiki/rest/api/content/42/version":
			fmt.Fprint(w, `{"results":[{"number":4,"by":{"accountId":"u3","displayName":"Cas","profilePicture":{"path":"/wiki/aa-avatar/u3"}}}]}`)
		case "/wiki/api/v2/pages/42/attachments":
			fmt.Fprint(w, `{"results":[{"id":"att77","title":"diagram.png","fileId":"f1","mediaType":"image/png"}]}`)
		case "/wiki/api/v2/attachments/att77":
			fmt.Fprint(w, `{"id":"att77","downloadLink":"/download/attachments/42/diagram.png?api=v2"}`)
		case "/wiki/download/attachments/42/diagram.png":
			fmt.Fprint(w, "PNG")
		default:
			t.Errorf("%s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	p, err := c.ConfluencePage(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Release plan" || p.URL != srv.URL+"/wiki/spaces/ENG/pages/42" || p.Version != 4 || p.Author != "Cas" ||
		p.AuthorAvatar != srv.URL+"/wiki/aa-avatar/u3" || p.Edited != "2026-09-22T08:45:11Z" {
		t.Errorf("page %+v", p)
	}
	for _, want := range []string{"# Plan", "_[roadmap macro]_", "![diagram.png](confluence:77)", "_[image: elsewhere.png]_"} {
		if !strings.Contains(p.Markdown, want) {
			t.Errorf("no %q in:\n%s", want, p.Markdown)
		}
	}
	if b, err := c.PageImage(context.Background(), "77"); string(b) != "PNG" || err != nil {
		t.Errorf("PageImage = %q, %v", b, err)
	}
}

// TestConfluencePageLanewayMacros: laneway-server is asked to expand the
// page's macros.
func TestConfluencePageLanewayMacros(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/serverInfo":
			fmt.Fprint(w, `{"lanewayServerVersion":"0.1.0"}`)
		case "/wiki/api/v2/pages/42":
			if r.URL.Query().Get("laneway-macros") != "expand" {
				t.Errorf("%s", r.URL)
			}
			fmt.Fprint(w, `{"id":"42","title":"T","body":{"atlas_doc_format":{"value":"{\"type\":\"doc\",\"version\":1,\"content\":[]}"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if _, err := c.ConfluencePage(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
}

// TestConfluencePageTableImage: an image the markdown leaves out (in a
// table cell) does not shift the next one onto its file.
func TestConfluencePageTableImage(t *testing.T) {
	media := func(id, alt string) map[string]any {
		return map[string]any{"type": "media", "attrs": map[string]any{"id": id, "alt": alt}}
	}
	cell := map[string]any{"type": "tableCell", "content": []any{map[string]any{"type": "mediaSingle", "content": []any{media("f1", "table.png")}}}}
	doc, _ := json.Marshal(map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "table", "content": []any{map[string]any{"type": "tableRow", "content": []any{cell}}}},
		map[string]any{"type": "mediaSingle", "content": []any{media("f2", "after.png")}},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wiki/api/v2/pages/42/attachments" {
			fmt.Fprint(w, `{"results":[{"id":"att1","title":"table.png","fileId":"f1","mediaType":"image/png"},{"id":"att2","title":"after.png","fileId":"f2","mediaType":"image/png"}]}`)
			return
		}
		b, _ := json.Marshal(string(doc))
		fmt.Fprintf(w, `{"id":"42","title":"T","body":{"atlas_doc_format":{"value":%s}}}`, b)
	}))
	defer srv.Close()
	p, err := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"}).ConfluencePage(context.Background(), "42")
	if err != nil || !strings.Contains(p.Markdown, "![after.png](confluence:2)") {
		t.Errorf("markdown %q, %v", p.Markdown, err)
	}
}

// TestPageTreeMarkdown: laneway-server's page tree reads as its list
// between tree markers.
func TestPageTreeMarkdown(t *testing.T) {
	item := func(text string, kids ...any) any {
		c := []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text,
			"marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": "https://x.test/wiki/" + text}}}}}}}
		if len(kids) > 0 {
			c = append(c, map[string]any{"type": "bulletList", "content": kids})
		}
		return map[string]any{"type": "listItem", "content": c}
	}
	doc, _ := json.Marshal(map[string]any{"type": "doc", "version": 1, "content": []any{map[string]any{"type": "bulletList",
		"attrs":   map[string]any{"lanewayTree": map[string]any{"open": 2, "toggleAll": true, "search": false}},
		"content": []any{item("Deploys", item("Rollback")), item("Oncall")}}}})
	want := "<!-- tree: open=2, toggle -->\n\n- [Deploys](https://x.test/wiki/Deploys)\n  - [Rollback](https://x.test/wiki/Rollback)\n- [Oncall](https://x.test/wiki/Oncall)\n\n<!-- /tree -->"
	if got := strings.TrimSpace(DocMarkdown(doc)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestConfluencePageMacros: on Confluence the client expands toc, children
// and pagetree itself, from the space's page list; an inline page tree
// splits its line.
func TestConfluencePageMacros(t *testing.T) {
	ext := func(typ, key string, params map[string]string) map[string]any {
		mp := map[string]any{}
		for k, v := range params {
			mp[k] = map[string]any{"value": v}
		}
		return map[string]any{"type": typ, "attrs": map[string]any{"extensionKey": key, "parameters": map[string]any{"macroParams": mp}}}
	}
	text := func(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
	doc, _ := json.Marshal(map[string]any{"type": "doc", "version": 1, "content": []any{
		ext("extension", "toc", nil),
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 1}, "content": []any{text("Steps")}},
		ext("extension", "children", map[string]string{"sort": "natural"}),
		map[string]any{"type": "paragraph", "content": []any{text("All: "), ext("inlineExtension", "pagetree", map[string]string{"sort": "natural"})}},
		ext("extension", "children", map[string]string{"page": "Nope"}),
	}})
	pages := `{"results":[` +
		`{"id":"1","title":"Home","position":0,"_links":{"webui":"/spaces/ISO/overview"}},` +
		`{"id":"42","title":"Guide","parentId":"1","position":1,"_links":{"webui":"/spaces/ISO/pages/42/Guide"}},` +
		`{"id":"43","title":"Step 10","parentId":"42","position":1,"_links":{"webui":"/spaces/ISO/pages/43"}},` +
		`{"id":"44","title":"Step 2","parentId":"42","position":2,"_links":{"webui":"/spaces/ISO/pages/44"}},` +
		`{"id":"45","title":"Detail","parentId":"44","position":1,"_links":{"webui":"/spaces/ISO/pages/45"}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/serverInfo":
			fmt.Fprint(w, `{"deploymentType":"Cloud"}`)
		case "/wiki/api/v2/pages/42":
			b, _ := json.Marshal(string(doc))
			fmt.Fprintf(w, `{"id":"42","title":"Guide","spaceId":"7","parentId":"1","body":{"atlas_doc_format":{"value":%s}}}`, b)
		case "/wiki/api/v2/spaces/7/pages":
			fmt.Fprint(w, pages)
		case "/wiki/api/v2/spaces/7":
			fmt.Fprint(w, `{"id":"7","key":"ISO","homepageId":"1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	p, err := c.ConfluencePage(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	u := srv.URL + "/wiki/spaces/ISO/pages/"
	want := "- Steps\n\n# Steps\n\n" +
		"- [Step 2](" + u + "44)\n- [Step 10](" + u + "43)\n\n" +
		"All: \n\n<!-- tree: open=1 -->\n\n- [Guide](" + u + "42/Guide)\n  - [Step 2](" + u + "44)\n    - [Detail](" + u + "45)\n  - [Step 10](" + u + "43)\n\n<!-- /tree -->\n\n" +
		"*No page Nope.*"
	if got := strings.TrimSpace(p.Markdown); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestRecentlyUpdatedMacro: on Confluence the macro is a CQL search,
// newest first, each change as its link, who and when.
func TestRecentlyUpdatedMacro(t *testing.T) {
	var cqls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/serverInfo":
			fmt.Fprint(w, `{"deploymentType":"Cloud"}`)
		case "/wiki/api/v2/pages/42":
			doc := `{"type":"doc","version":1,"content":[` +
				`{"type":"extension","attrs":{"extensionKey":"recently-updated","parameters":{"macroParams":{"types":{"value":"page"},"max":{"value":"5"},"hideHeading":{"value":"true"}}}}},` +
				`{"type":"extension","attrs":{"extensionKey":"recently-updated","parameters":{"macroParams":{"spaces":{"value":"@self, OPS -ARCH"},"labels":{"value":"iso +audit -draft"},"types":{"value":"-attachment"},"author":{"value":"557058:a"},"theme":{"value":"sidebar"}}}}}]}`
			b, _ := json.Marshal(doc)
			fmt.Fprintf(w, `{"id":"42","title":"Guide","spaceId":"7","body":{"atlas_doc_format":{"value":%s}}}`, b)
		case "/wiki/api/v2/spaces/7":
			fmt.Fprint(w, `{"id":"7","key":"ISO","homepageId":"1"}`)
		case "/wiki/rest/api/search":
			cqls = append(cqls, r.URL.Query().Get("limit")+" "+r.URL.Query().Get("cql"))
			fmt.Fprint(w, `{"results":[`+
				`{"title":"Policy","url":"/spaces/ISO/pages/9/Policy","lastModified":"2026-10-10T12:22:46.000Z","content":{"type":"page","version":{"by":{"displayName":"Cas"}}}},`+
				`{"title":"Re: Policy","url":"/spaces/ISO/pages/9/Policy?focusedCommentId=3","lastModified":"2026-10-09T08:00:00.000Z","content":{"type":"comment","version":{"by":{"displayName":"Ada"}},"container":{"title":"Policy"}}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	p, err := c.ConfluencePage(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`5 type in (page) AND (space in ("ISO")) order by lastmodified desc`,
		`15 type in (page,blogpost,comment) AND (space in ("ISO","OPS")) AND space not in ("ARCH") AND label in ("iso") AND label = "audit" AND label not in ("draft") AND contributor in ("557058:a") order by lastmodified desc`,
	}
	if strings.Join(cqls, "\n") != strings.Join(want, "\n") {
		t.Errorf("cql\n%s\nwant\n%s", strings.Join(cqls, "\n"), strings.Join(want, "\n"))
	}
	u := srv.URL + "/wiki/spaces/ISO/pages/9/Policy"
	for _, w := range []string{
		"- [Policy](" + u + ") · Cas · <date>2026-10-10</date>\n- [Comment on Policy](" + u + "?focusedCommentId=3) · Ada · <date>2026-10-09</date>",
		"### Recently Updated\n\n- [Policy](" + u + ") · <date>2026-10-10</date>",
	} {
		if !strings.Contains(p.Markdown, w) {
			t.Errorf("no\n%s\nin\n%s", w, p.Markdown)
		}
	}
}

// TestRecentListSocial: the social theme groups a person's changes in a
// row under their name.
func TestRecentListSocial(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 9, 0, 0, 0, time.UTC) }
	list := recentList([]recentChange{
		{text: "Policy", href: "https://x.test/1", who: "Cas", when: day(10)},
		{text: "Notes", href: "https://x.test/2", who: "Cas", when: day(9)},
		{text: "Comment on Policy", href: "https://x.test/1", who: "Ada", when: day(8)},
	}, "social")
	doc, _ := json.Marshal(adfNode{Type: "doc", Content: []adfNode{list}})
	want := "- **Cas** · <date>2026-10-10</date>\n  - [Policy](https://x.test/1) · <date>2026-10-10</date>\n  - [Notes](https://x.test/2) · <date>2026-10-09</date>\n" +
		"- **Ada** · <date>2026-10-08</date>\n  - [Comment on Policy](https://x.test/1) · <date>2026-10-08</date>"
	if got := DocMarkdown(doc); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
