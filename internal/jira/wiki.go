package jira

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/safeterm"
)

// The wiki: Confluence's spaces, their page trees, a page to read, write
// and comment on, through its v2 API (on Confluence Cloud and on
// laneway-server alike).

// Space is a wiki space.
type Space struct {
	ID, Key, Name string
	HomepageID    string
	URL           string
}

// PageNode is a page in a space's tree: ParentID is "" at the top.
type PageNode struct {
	ID, Title, ParentID string
	Position            int
}

// WikiDoc is a page to edit: its title, its document (ADF) and the
// version an update builds on.
type WikiDoc struct {
	ID, Title, SpaceID, ParentID string
	Doc                          json.RawMessage
	Version                      int
}

// WikiComment is a footer comment of a page, with its replies.
type WikiComment struct {
	ID, AuthorID, Author, Created string
	Markdown                      string
	Replies                       []WikiComment
}

// PageVersion is a saved version of a page: who saved it, when and why,
// and the page as it read then.
type PageVersion struct {
	Number                                        int
	AuthorID, Author, AvatarURL, Created, Message string
	Minor                                         bool
	Title, Markdown                               string
}

// WikiHit is a page a wiki search found.
type WikiHit struct {
	ID, Title, SpaceKey, Excerpt string
}

// ErrPageChanged is an update refused because someone saved the page
// meanwhile (Confluence's version check).
var ErrPageChanged = errors.New(i18n.T("the page changed meanwhile: reload it"))

// wikiPages follows a v2 list's cursor (_links.next) up to max results.
func wikiPages[T any](ctx context.Context, c *Client, path, what string, max int) ([]T, error) {
	var out []T
	for path != "" && len(out) < max {
		var resp struct {
			Results []T `json:"results"`
			Links   struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		if err := c.do(ctx, http.MethodGet, path, what, nil, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Results...)
		path = resp.Links.Next
		if path != "" && !strings.HasPrefix(path, "/wiki/") {
			path = "/wiki" + path
		}
	}
	return out, nil
}

// Spaces are the spaces the person may read, by name.
func (c *Client) Spaces(ctx context.Context) ([]Space, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	type space struct {
		ID         json.Number `json:"id"`
		Key        string      `json:"key"`
		Name       string      `json:"name"`
		HomepageID json.Number `json:"homepageId"`
		Status     string      `json:"status"`
		Links      struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	}
	rs, err := wikiPages[space](ctx, c, "/wiki/api/v2/spaces?limit=250&status=current", "spaces", 2000)
	if err != nil {
		return nil, err
	}
	out := make([]Space, 0, len(rs))
	for _, s := range rs {
		u := ""
		if s.Links.WebUI != "" {
			u = c.baseURL + "/wiki" + s.Links.WebUI
		}
		out = append(out, Space{ID: s.ID.String(), Key: s.Key, Name: safeterm.Line(s.Name), HomepageID: s.HomepageID.String(), URL: u})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// SpacePages are a space's current pages, for its tree: each with its
// parent ("" for one under the space) and place among its siblings.
func (c *Client) SpacePages(ctx context.Context, spaceID string) ([]PageNode, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	type page struct {
		ID         json.Number `json:"id"`
		Title      string      `json:"title"`
		ParentID   json.Number `json:"parentId"`
		ParentType string      `json:"parentType"`
		Position   json.Number `json:"position"`
	}
	rs, err := wikiPages[page](ctx, c, "/wiki/api/v2/spaces/"+url.PathEscape(spaceID)+"/pages?limit=250&status=current", "space "+spaceID+" pages", 5000)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, p := range rs {
		ids[p.ID.String()] = true
	}
	out := make([]PageNode, 0, len(rs))
	for _, p := range rs {
		parent := p.ParentID.String()
		// Under a folder or a page this list leaves out: shown at the top.
		if !ids[parent] {
			parent = ""
		}
		pos, _ := strconv.Atoi(p.Position.String())
		out = append(out, PageNode{ID: p.ID.String(), Title: safeterm.Line(p.Title), ParentID: parent, Position: pos})
	}
	return out, nil
}

// pageBody is the body Confluence takes: the document as a JSON string.
func pageBody(doc json.RawMessage) (map[string]any, error) {
	v, err := CheckDoc(doc)
	if err != nil {
		return nil, err
	}
	if v == nil {
		v, _ = json.Marshal(MarkdownToADF(""))
	}
	return map[string]any{"representation": "atlas_doc_format", "value": string(v)}, nil
}

// WikiPageDoc reads page id to edit: its title, document and version.
func (c *Client) WikiPageDoc(ctx context.Context, id string) (WikiDoc, error) {
	if !c.Enabled() {
		return WikiDoc{}, errNotConfigured
	}
	var resp struct {
		ID       json.Number `json:"id"`
		Title    string      `json:"title"`
		SpaceID  json.Number `json:"spaceId"`
		ParentID json.Number `json:"parentId"`
		Version  struct {
			Number int `json:"number"`
		} `json:"version"`
		Body struct {
			ADF struct {
				Value string `json:"value"`
			} `json:"atlas_doc_format"`
		} `json:"body"`
	}
	if err := c.do(ctx, http.MethodGet, "/wiki/api/v2/pages/"+url.PathEscape(id)+"?body-format=atlas_doc_format", "page "+id, nil, &resp); err != nil {
		return WikiDoc{}, err
	}
	doc := json.RawMessage(resp.Body.ADF.Value)
	if len(doc) == 0 {
		doc = nil
	}
	return WikiDoc{ID: resp.ID.String(), Title: resp.Title, SpaceID: resp.SpaceID.String(), ParentID: resp.ParentID.String(), Doc: doc, Version: resp.Version.Number}, nil
}

// CreatePage makes a page in space spaceID, under parentID ("" for the
// space's top), and answers its id.
func (c *Client) CreatePage(ctx context.Context, spaceID, parentID, title string, doc json.RawMessage) (string, error) {
	if !c.Enabled() {
		return "", errNotConfigured
	}
	body, err := pageBody(doc)
	if err != nil {
		return "", err
	}
	req := map[string]any{"spaceId": spaceID, "status": "current", "title": strings.TrimSpace(title), "body": body}
	if parentID != "" {
		req["parentId"] = parentID
	}
	var resp struct {
		ID json.Number `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/wiki/api/v2/pages", "new page", req, &resp); err != nil {
		return "", err
	}
	return resp.ID.String(), nil
}

// UpdatePage writes page id's title and document as the version after
// version; ErrPageChanged when that is no longer the latest.
func (c *Client) UpdatePage(ctx context.Context, id, title string, doc json.RawMessage, version int) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body, err := pageBody(doc)
	if err != nil {
		return err
	}
	req := map[string]any{"id": id, "status": "current", "title": strings.TrimSpace(title), "body": body,
		"version": map[string]any{"number": version + 1}}
	err = c.do(ctx, http.MethodPut, "/wiki/api/v2/pages/"+url.PathEscape(id), "page "+id, req, nil)
	if re, ok := errors.AsType[*RequestError](err); ok && re.code == http.StatusConflict {
		return ErrPageChanged
	}
	return err
}

// DeletePage moves page id to the space's trash.
func (c *Client) DeletePage(ctx context.Context, id string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	return c.do(ctx, http.MethodDelete, "/wiki/api/v2/pages/"+url.PathEscape(id), "page "+id, nil, nil)
}

type wikiCommentJSON struct {
	ID      json.Number `json:"id"`
	Version struct {
		AuthorID  string `json:"authorId"`
		CreatedAt string `json:"createdAt"`
	} `json:"version"`
	Body struct {
		ADF struct {
			Value string `json:"value"`
		} `json:"atlas_doc_format"`
	} `json:"body"`
}

func (w wikiCommentJSON) comment() WikiComment {
	return WikiComment{ID: w.ID.String(), AuthorID: w.Version.AuthorID, Created: w.Version.CreatedAt,
		Markdown: safeterm.Text(adfToMarkdown(json.RawMessage(w.Body.ADF.Value)))}
}

// PageComments are page id's footer comments, oldest first, each with its
// replies.
func (c *Client) PageComments(ctx context.Context, id string) ([]WikiComment, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	top, err := wikiPages[wikiCommentJSON](ctx, c, "/wiki/api/v2/pages/"+url.PathEscape(id)+"/footer-comments?body-format=atlas_doc_format&limit=100", "page "+id+" comments", 500)
	if err != nil {
		return nil, err
	}
	out := make([]WikiComment, 0, len(top))
	for _, t := range top {
		cm := t.comment()
		kids, err := wikiPages[wikiCommentJSON](ctx, c, "/wiki/api/v2/footer-comments/"+url.PathEscape(cm.ID)+"/children?body-format=atlas_doc_format&limit=100", "comment "+cm.ID+" replies", 500)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			cm.Replies = append(cm.Replies, k.comment())
		}
		out = append(out, cm)
	}
	// Who wrote them, by name: one lookup per person.
	names := map[string]string{}
	var name func(cs []WikiComment)
	name = func(cs []WikiComment) {
		for i := range cs {
			id := cs[i].AuthorID
			if _, ok := names[id]; !ok && id != "" {
				names[id] = c.wikiUserName(ctx, id)
			}
			cs[i].Author = names[id]
			name(cs[i].Replies)
		}
	}
	name(out)
	return out, nil
}

// wikiUserName is a person's display name on the site; their id when the
// site won't say.
func (c *Client) wikiUserName(ctx context.Context, id string) string {
	name, _ := c.wikiUser(ctx, id)
	return name
}

// wikiUser is a person's display name (their id when the site won't say)
// and avatar: from Jira, else from Confluence (someone without Jira).
func (c *Client) wikiUser(ctx context.Context, id string) (name, avatar string) {
	var u struct {
		DisplayName string            `json:"displayName"`
		AvatarURLs  map[string]string `json:"avatarUrls"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/user?accountId="+url.QueryEscape(id), "user "+id, nil, &u); err == nil && u.DisplayName != "" {
		return safeterm.Line(u.DisplayName), u.AvatarURLs["48x48"]
	}
	var cu struct {
		DisplayName    string `json:"displayName"`
		PublicName     string `json:"publicName"`
		ProfilePicture struct {
			Path string `json:"path"`
		} `json:"profilePicture"`
	}
	if err := c.do(ctx, http.MethodGet, "/wiki/rest/api/user?accountId="+url.QueryEscape(id), "user "+id, nil, &cu); err != nil {
		return id, ""
	}
	name = firstNonEmptyStr(cu.DisplayName, cu.PublicName)
	if name == "" {
		return id, ""
	}
	return safeterm.Line(name), c.wikiPicture(cu.ProfilePicture.Path)
}

// wikiPicture is a Confluence profile picture's URL.
func (c *Client) wikiPicture(p string) string {
	switch {
	case strings.HasPrefix(p, "https://"):
		return p
	case strings.HasPrefix(p, "/wiki/"):
		return c.baseURL + p
	case strings.HasPrefix(p, "/"):
		return c.baseURL + "/wiki" + p
	}
	return ""
}

// versionAuthors are the names and pictures of who saved page id's latest
// n versions, as its v1 version list carries them: a site that won't show
// someone's profile still names them there. Empty when the site lacks it.
func (c *Client) versionAuthors(ctx context.Context, id string, n int) map[string][2]string {
	var resp struct {
		Results []struct {
			By struct {
				AccountID      string `json:"accountId"`
				DisplayName    string `json:"displayName"`
				PublicName     string `json:"publicName"`
				ProfilePicture struct {
					Path string `json:"path"`
				} `json:"profilePicture"`
			} `json:"by"`
		} `json:"results"`
	}
	out := map[string][2]string{}
	if c.do(ctx, http.MethodGet, "/wiki/rest/api/content/"+url.PathEscape(id)+"/version?limit="+strconv.Itoa(n), "page "+id+" version authors", nil, &resp) != nil {
		return out
	}
	for _, r := range resp.Results {
		if name := firstNonEmptyStr(r.By.DisplayName, r.By.PublicName); r.By.AccountID != "" && name != "" {
			out[r.By.AccountID] = [2]string{safeterm.Line(name), c.wikiPicture(r.By.ProfilePicture.Path)}
		}
	}
	return out
}

// maxPageVersions is how many of a page's latest versions its history reads.
const maxPageVersions = 250

// PageVersions are page id's versions, oldest first (its latest
// maxPageVersions), each with the page as it read then.
func (c *Client) PageVersions(ctx context.Context, id string) ([]PageVersion, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	type version struct {
		Number    int    `json:"number"`
		Message   string `json:"message"`
		MinorEdit bool   `json:"minorEdit"`
		AuthorID  string `json:"authorId"`
		CreatedAt string `json:"createdAt"`
		Page      struct {
			Title string `json:"title"`
			Body  struct {
				ADF struct {
					Value string `json:"value"`
				} `json:"atlas_doc_format"`
			} `json:"body"`
		} `json:"page"`
	}
	vs, err := wikiPages[version](ctx, c, "/wiki/api/v2/pages/"+url.PathEscape(id)+"/versions?body-format=atlas_doc_format&sort=-modified-date&limit=50", "page "+id+" versions", maxPageVersions)
	if err != nil {
		return nil, err
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Number < vs[j].Number })
	var atts []pageAttachment
	attsRead := false
	people := c.versionAuthors(ctx, id, 200)
	out := make([]PageVersion, 0, len(vs))
	for _, v := range vs {
		raw, title := json.RawMessage(v.Page.Body.ADF.Value), v.Page.Title
		// A site that leaves a version's body out of the list: the page at it.
		if len(raw) == 0 {
			var pg struct {
				Title string `json:"title"`
				Body  struct {
					ADF struct {
						Value string `json:"value"`
					} `json:"atlas_doc_format"`
				} `json:"body"`
			}
			n := strconv.Itoa(v.Number)
			if err := c.do(ctx, http.MethodGet, "/wiki/api/v2/pages/"+url.PathEscape(id)+"?body-format=atlas_doc_format&version="+n, "page "+id+" version "+n, nil, &pg); err != nil {
				return nil, err
			}
			raw, title = json.RawMessage(pg.Body.ADF.Value), pg.Title
		}
		if !attsRead && len(pageMedia(raw)) > 0 {
			atts, _ = c.pageAttachments(ctx, id) // without them, the images stand as their names
			attsRead = true
		}
		who, ok := people[v.AuthorID]
		if !ok && v.AuthorID != "" {
			who[0], who[1] = c.wikiUser(ctx, v.AuthorID)
			people[v.AuthorID] = who
		}
		out = append(out, PageVersion{Number: v.Number, AuthorID: v.AuthorID, Author: who[0], AvatarURL: who[1], Created: v.CreatedAt,
			Message: safeterm.Line(v.Message), Minor: v.MinorEdit, Title: safeterm.Line(title), Markdown: safeterm.Text(pageMarkdown(raw, atts))})
	}
	return out, nil
}

// RestorePage saves version n of page id as its latest, title included.
func (c *Client) RestorePage(ctx context.Context, id string, n int, message string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	req := map[string]any{"operationKey": "restore", "params": map[string]any{"versionNumber": n, "message": message, "restoreTitle": true}}
	return c.do(ctx, http.MethodPost, "/wiki/rest/api/content/"+url.PathEscape(id)+"/version", "page "+id+" restore", req, nil)
}

// AddPageComment writes a footer comment on page id, or a reply to
// comment parent when that is set.
func (c *Client) AddPageComment(ctx context.Context, id, parent string, doc json.RawMessage) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	body, err := pageBody(doc)
	if err != nil {
		return err
	}
	req := map[string]any{"body": body}
	if parent != "" {
		req["parentCommentId"] = parent
	} else {
		req["pageId"] = id
	}
	return c.do(ctx, http.MethodPost, "/wiki/api/v2/footer-comments", "page "+id+" comment", req, nil)
}

// SearchWiki finds pages whose title or text has text (Confluence's CQL),
// in space spaceKey when that is set.
func (c *Client) SearchWiki(ctx context.Context, text, spaceKey string) ([]WikiHit, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(strings.TrimSpace(text))
	cql := `type = page AND text ~ "` + q + `"`
	if spaceKey != "" {
		cql += ` AND space = "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(spaceKey) + `"`
	}
	var resp struct {
		Results []struct {
			Content struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"content"`
			Title                 string `json:"title"`
			Excerpt               string `json:"excerpt"`
			ResultGlobalContainer struct {
				Title string `json:"title"`
			} `json:"resultGlobalContainer"`
		} `json:"results"`
	}
	if err := c.do(ctx, http.MethodGet, "/wiki/rest/api/search?limit=25&cql="+url.QueryEscape(cql), "wiki search", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]WikiHit, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Content.ID == "" {
			continue
		}
		out = append(out, WikiHit{ID: r.Content.ID, Title: safeterm.Line(firstNonEmptyStr(r.Content.Title, r.Title)),
			SpaceKey: safeterm.Line(r.ResultGlobalContainer.Title), Excerpt: safeterm.Line(stripHighlight(r.Excerpt))})
	}
	return out, nil
}

// stripHighlight drops Confluence's @@@hl@@@ markers from an excerpt.
func stripHighlight(s string) string {
	return strings.NewReplacer("@@@hl@@@", "", "@@@endhl@@@", "").Replace(s)
}

func firstNonEmptyStr(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
