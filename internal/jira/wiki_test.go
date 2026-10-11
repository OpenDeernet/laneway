package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The wiki through Confluence's v2 API: spaces across the cursor's pages,
// a space's tree with pages under one it doesn't list at the top, a page
// read to edit, created and updated (a stale version refused), comments
// with replies and their authors by name, and a search.
func TestWiki(t *testing.T) {
	doc := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Keep logs"}]}]}`
	var created, updated map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		switch r.Method + " " + r.URL.Path {
		case "GET /wiki/api/v2/spaces":
			if r.URL.Query().Get("cursor") == "" {
				io.WriteString(w, `{"results":[{"id":2,"key":"OPS","name":"ops","homepageId":20}],"_links":{"next":"/api/v2/spaces?cursor=b"}}`)
			} else {
				io.WriteString(w, `{"results":[{"id":1,"key":"ENG","name":"Engineering","homepageId":10,"_links":{"webui":"/spaces/ENG"}}]}`)
			}
		case "GET /wiki/api/v2/spaces/1/pages":
			io.WriteString(w, `{"results":[{"id":10,"title":"Home","position":1},{"id":11,"title":"Policy","parentId":10,"position":2},{"id":12,"title":"In a folder","parentId":99,"position":3}]}`)
		case "GET /wiki/api/v2/pages/11":
			json.NewEncoder(w).Encode(map[string]any{"id": "11", "title": "Policy", "spaceId": "1", "parentId": "10", "version": map[string]any{"number": 3},
				"body": map[string]any{"atlas_doc_format": map[string]any{"value": doc}}})
		case "POST /wiki/api/v2/pages":
			json.Unmarshal(body, &created)
			io.WriteString(w, `{"id":"12"}`)
		case "PUT /wiki/api/v2/pages/11":
			json.Unmarshal(body, &updated)
			if v := updated["version"].(map[string]any)["number"].(float64); v != 4 {
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, `{"errors":[{"status":409,"title":"Version must be incremented"}]}`)
			}
		case "GET /wiki/api/v2/pages/11/footer-comments":
			io.WriteString(w, `{"results":[{"id":30,"version":{"authorId":"u1","createdAt":"2026-10-01T10:00:00Z"},"body":{"atlas_doc_format":{"value":"{\"type\":\"doc\",\"version\":1,\"content\":[{\"type\":\"paragraph\",\"content\":[{\"type\":\"text\",\"text\":\"Which logs?\"}]}]}"}}}]}`)
		case "GET /wiki/api/v2/footer-comments/30/children":
			io.WriteString(w, `{"results":[{"id":31,"version":{"authorId":"u2"},"body":{"atlas_doc_format":{"value":""}}}]}`)
		case "GET /rest/api/3/user":
			io.WriteString(w, `{"displayName":"Ann `+r.URL.Query().Get("accountId")+`"}`)
		case "GET /wiki/rest/api/search":
			if q := r.URL.Query().Get("cql"); q != `type = page AND text ~ "log \"x\"" AND space = "ENG"` {
				t.Errorf("cql %s", q)
			}
			io.WriteString(w, `{"results":[{"content":{"id":"11","title":"Policy"},"excerpt":"Keep @@@hl@@@logs@@@endhl@@@","resultGlobalContainer":{"title":"Engineering"}}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	ctx := context.Background()

	spaces, err := c.Spaces(ctx)
	if err != nil || len(spaces) != 2 || spaces[0].Key != "ENG" || spaces[0].HomepageID != "10" || spaces[0].URL != srv.URL+"/wiki/spaces/ENG" {
		t.Fatalf("spaces %+v %v", spaces, err)
	}
	pages, err := c.SpacePages(ctx, "1")
	if err != nil || len(pages) != 3 || pages[1].ParentID != "10" || pages[2].ParentID != "" {
		t.Fatalf("pages %+v %v", pages, err)
	}
	d, err := c.WikiPageDoc(ctx, "11")
	if err != nil || d.Version != 3 || d.Title != "Policy" || !strings.Contains(string(d.Doc), "Keep logs") {
		t.Fatalf("doc %+v %v", d, err)
	}
	if id, err := c.CreatePage(ctx, "1", "10", " Retention ", json.RawMessage(doc)); err != nil || id != "12" ||
		created["title"] != "Retention" || created["parentId"] != "10" || created["body"].(map[string]any)["representation"] != "atlas_doc_format" {
		t.Errorf("create %v %v %v", id, created, err)
	}
	if err := c.UpdatePage(ctx, "11", "Policy", json.RawMessage(doc), 3); err != nil {
		t.Errorf("update: %v", err)
	}
	if err := c.UpdatePage(ctx, "11", "Policy", json.RawMessage(doc), 1); !errors.Is(err, ErrPageChanged) {
		t.Errorf("stale update: %v", err)
	}
	cs, err := c.PageComments(ctx, "11")
	if err != nil || len(cs) != 1 || cs[0].Author != "Ann u1" || !strings.Contains(cs[0].Markdown, "Which logs?") || len(cs[0].Replies) != 1 || cs[0].Replies[0].Author != "Ann u2" {
		t.Fatalf("comments %+v %v", cs, err)
	}
	hits, err := c.SearchWiki(ctx, `log "x"`, "ENG")
	if err != nil || len(hits) != 1 || hits[0].Excerpt != "Keep logs" || hits[0].SpaceKey != "Engineering" {
		t.Fatalf("search %+v %v", hits, err)
	}
}

// A page's history: versions oldest first whatever order the site sends,
// one the list leaves the body out of read on its own, each author looked
// up once (in Confluence when Jira doesn't know them); and a restore as
// Confluence's v1 operation.
func TestPageVersions(t *testing.T) {
	para := func(s string) string {
		b, _ := json.Marshal(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"` + s + `"}]}]}`)
		return string(b)
	}
	users := 0
	var restore map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		switch r.Method + " " + r.URL.Path {
		case "GET /wiki/api/v2/pages/11/versions":
			if r.URL.Query().Get("body-format") != "atlas_doc_format" {
				t.Errorf("versions %s", r.URL)
			}
			io.WriteString(w, `{"results":[`+
				`{"number":3,"message":"fix","authorId":"u1","createdAt":"2026-10-03T10:00:00Z","page":{"title":"Policy","body":{"atlas_doc_format":{"value":`+para("Keep logs a year")+`}}}},`+
				`{"number":2,"minorEdit":true,"authorId":"u2","createdAt":"2026-10-02T10:00:00Z","page":{"title":"Policy"}},`+
				`{"number":1,"authorId":"u1","createdAt":"2026-10-01T10:00:00Z","page":{"title":"Draft","body":{"atlas_doc_format":{"value":`+para("Keep logs")+`}}}}]}`)
		case "GET /wiki/api/v2/pages/11":
			if r.URL.Query().Get("version") != "2" {
				t.Errorf("page %s", r.URL)
			}
			io.WriteString(w, `{"title":"Policy","body":{"atlas_doc_format":{"value":`+para("Keep logs a month")+`}}}`)
		case "GET /rest/api/3/user":
			users++
			// u2 has no Jira: Confluence knows them.
			if r.URL.Query().Get("accountId") == "u2" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			io.WriteString(w, `{"displayName":"Ann `+r.URL.Query().Get("accountId")+`","avatarUrls":{"48x48":"https://a.test/`+r.URL.Query().Get("accountId")+`"}}`)
		case "GET /wiki/rest/api/user":
			io.WriteString(w, `{"displayName":"Bea","profilePicture":{"path":"/wiki/aa-avatar/u2"}}`)
		case "POST /wiki/rest/api/content/11/version":
			json.Unmarshal(body, &restore)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	ctx := context.Background()

	vs, err := c.PageVersions(ctx, "11")
	if err != nil || len(vs) != 3 {
		t.Fatalf("versions %+v %v", vs, err)
	}
	if vs[0].Number != 1 || vs[0].Title != "Draft" || !strings.Contains(vs[0].Markdown, "Keep logs") || vs[0].Author != "Ann u1" || vs[0].AvatarURL != "https://a.test/u1" {
		t.Errorf("v1 %+v", vs[0])
	}
	if !vs[1].Minor || !strings.Contains(vs[1].Markdown, "a month") || vs[1].Author != "Bea" || vs[1].AvatarURL != srv.URL+"/wiki/aa-avatar/u2" {
		t.Errorf("v2 %+v", vs[1])
	}
	if vs[2].Message != "fix" || !strings.Contains(vs[2].Markdown, "a year") {
		t.Errorf("v3 %+v", vs[2])
	}
	if users != 2 {
		t.Errorf("%d user lookups, want 2", users)
	}
	if err := c.RestorePage(ctx, "11", 1, "Restored version 1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := restore["params"].(map[string]any); restore["operationKey"] != "restore" || p["versionNumber"] != float64(1) || p["restoreTitle"] != true {
		t.Errorf("restore %v", restore)
	}
}
