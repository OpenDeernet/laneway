package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/cornedor/laneway/internal/safeterm"
)

// Confluence pages linked from an issue, read as markdown with the site's
// own login: Confluence Cloud lives on the same host as Jira, under /wiki.

// Page is a Confluence page as the panel shows it, with who saved its
// latest version and when.
type Page struct {
	ID, Title, URL               string
	Markdown                     string
	Version                      int
	Author, AuthorAvatar, Edited string
}

var pagePathRe = regexp.MustCompile(`/wiki/spaces/[^/]+/pages/(\d+)`)

// PageID is the id of the Confluence page u links on this site
// (…/wiki/spaces/KEY/pages/123/Title, …/viewpage.action?pageId=123); ""
// for anything else, a page on another site too.
func (c *Client) PageID(u string) string {
	pu, err := url.Parse(u)
	base, err2 := url.Parse(c.wikiURL)
	if err != nil || err2 != nil || !strings.EqualFold(pu.Host, base.Host) || !strings.HasPrefix(pu.Path, "/wiki/") {
		return ""
	}
	if m := pagePathRe.FindStringSubmatch(pu.Path); m != nil {
		return m[1]
	}
	if id := pu.Query().Get("pageId"); id != "" && strings.Trim(id, "0123456789") == "" {
		return id
	}
	return ""
}

// PageImageScheme marks an image of a Confluence page in its markdown:
// ![name](confluence:123), the page attachment's id.
const PageImageScheme = "confluence:"

// ConfluencePage reads page id, its body as markdown; an image of its own
// attachments as ![name](confluence:ID), any other by its name.
func (c *Client) ConfluencePage(ctx context.Context, id string) (Page, error) {
	if !c.Enabled() {
		return Page{}, errNotConfigured
	}
	var resp struct {
		ID       string      `json:"id"`
		SpaceID  json.Number `json:"spaceId"`
		ParentID json.Number `json:"parentId"`
		Title    string      `json:"title"`
		Version  struct {
			Number    int    `json:"number"`
			AuthorID  string `json:"authorId"`
			CreatedAt string `json:"createdAt"`
		} `json:"version"`
		Body struct {
			ADF struct {
				Value string `json:"value"` // the document as a JSON string
			} `json:"atlas_doc_format"`
		} `json:"body"`
		Links struct {
			WebUI string `json:"webui"`
			Base  string `json:"base"`
		} `json:"_links"`
	}
	path := "/wiki/api/v2/pages/" + url.PathEscape(id) + "?body-format=atlas_doc_format"
	// laneway-server expands macros for the reader; on Confluence the
	// client expands those it knows (wikimacros.go).
	lw, _ := c.Laneway(ctx)
	if lw {
		path += "&laneway-macros=expand"
	}
	if err := c.do(ctx, http.MethodGet, path, "page "+id, nil, &resp); err != nil {
		return Page{}, err
	}
	raw := json.RawMessage(resp.Body.ADF.Value)
	if !lw {
		raw = c.expandPageMacros(ctx, raw, wikiPageRef{ID: resp.ID, SpaceID: resp.SpaceID.String(), ParentID: resp.ParentID.String()})
	}
	var atts []pageAttachment
	if len(pageMedia(raw)) > 0 {
		atts, _ = c.pageAttachments(ctx, id) // without them, the images stand as their names
	}
	md := pageMarkdown(raw, atts)
	base := strings.TrimSuffix(resp.Links.Base, "/")
	if base == "" {
		base = c.wikiURL + "/wiki"
	}
	pg := Page{ID: resp.ID, Title: safeterm.Line(resp.Title), URL: base + resp.Links.WebUI, Markdown: safeterm.Text(md),
		Version: resp.Version.Number, Edited: resp.Version.CreatedAt}
	if a := resp.Version.AuthorID; a != "" {
		who, ok := c.versionAuthors(ctx, id, 1)[a]
		if !ok {
			who[0], who[1] = c.wikiUser(ctx, a)
		}
		pg.Author, pg.AuthorAvatar = who[0], who[1]
	}
	return pg, nil
}

// pageMarkdown is a page body as markdown: an image of attachments atts
// as ![name](confluence:ID), any other by its name.
func pageMarkdown(raw json.RawMessage, atts []pageAttachment) string {
	// By the name the markdown carries: some images (in table cells) are
	// not written at all, so their order does not line up.
	byName := map[string]string{}
	for _, m := range pageMedia(raw) {
		for _, a := range atts {
			n, ok := strings.CutPrefix(a.ID, "att")
			if ok && (a.FileID == m.id && m.id != "" || a.Title == m.alt) && strings.Trim(n, "0123456789") == "" && strings.HasPrefix(a.MediaType, "image/") {
				if name := escapeMediaAlt(m.alt); byName[name] == "" {
					byName[name] = n
				}
				break
			}
		}
	}
	return pageImageRe.ReplaceAllStringFunc(adfToMarkdown(raw), func(s string) string {
		alt := pageImageRe.FindStringSubmatch(s)[1]
		if n := byName[alt]; n != "" {
			return "![" + alt + "](" + PageImageScheme + n + ")"
		}
		return "_[image: " + alt + "]_"
	})
}

// pageImageRe is an image of the page's: its name (its media id and size
// as adfToMarkdown writes them dropped).
var pageImageRe = regexp.MustCompile(`!\[([^\]\n]*)\]\(` + mediaRef + `(?:#[0-9a-fA-F-]{1,64})?(?: "[^"\n]*")?\)`)

// pageMedium is a media node of a page: its file and name.
type pageMedium struct{ id, alt string }

func pageMedia(raw json.RawMessage) []pageMedium {
	var doc adfNode
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var out []pageMedium
	var walk func(n adfNode)
	walk = func(n adfNode) {
		if n.Type == "media" {
			if alt, _ := n.Attrs["alt"].(string); alt != "" {
				id, _ := n.Attrs["id"].(string)
				out = append(out, pageMedium{id, alt})
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// pageAttachment is a file attached to a page.
type pageAttachment struct {
	ID, Title, FileID, MediaType string
	DownloadLink                 string
}

func (c *Client) pageAttachments(ctx context.Context, id string) ([]pageAttachment, error) {
	var resp struct {
		Results []pageAttachment `json:"results"`
	}
	err := c.do(ctx, http.MethodGet, "/wiki/api/v2/pages/"+url.PathEscape(id)+"/attachments?limit=250", "page "+id+" attachments", nil, &resp)
	return resp.Results, err
}

// PageImage downloads page attachment id (the number of its att… id).
func (c *Client) PageImage(ctx context.Context, id string) ([]byte, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var a pageAttachment
	if err := c.do(ctx, http.MethodGet, "/wiki/api/v2/attachments/att"+url.PathEscape(id), "page image "+id, nil, &a); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(a.DownloadLink, "/") {
		return nil, fmt.Errorf("page image %s: no download link", id)
	}
	return c.download(ctx, "/wiki"+a.DownloadLink, "page image "+id)
}
