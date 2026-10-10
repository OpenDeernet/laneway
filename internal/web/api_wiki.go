package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// The wiki: the site's Confluence spaces, a space's page tree, a page to
// read (as markdown), edit (as the description is) and comment on.

// wikiID checks a page, space or comment id: Confluence's are numbers.
func wikiID(r *http.Request, name string) (string, error) {
	id := r.PathValue(name)
	if id == "" || len(id) > 20 || strings.Trim(id, "0123456789") != "" {
		return "", badRequest(i18n.T("bad wiki id"))
	}
	return id, nil
}

// wikiPage is a page to edit: the document as the description editor
// takes it, with its title and the version a save builds on.
type wikiPage struct {
	editable
	ID, Title, SpaceID, ParentID string
	Version                      int
}

// wikiBody is a page or comment as the editor sends it: the document, or
// markdown with the nodes it keeps.
type wikiBody struct {
	Title, SpaceID, ParentID, Parent string
	Version                          int
	Markdown                         string
	Kept                             []json.RawMessage
	Doc                              json.RawMessage
}

// doc is the body's document: the editor's, or its markdown made one.
func (b wikiBody) doc() (json.RawMessage, error) {
	if given(b.Doc) {
		return b.Doc, nil
	}
	return json.Marshal(jira.MarkdownToADFKept(b.Markdown, b.Kept))
}

func init() {
	get("/wiki/spaces", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().Spaces(ctx)
	})
	get("/wiki/spaces/{id}/pages", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id, err := wikiID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.Client().SpacePages(ctx, id)
	})
	get("/wiki/search", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			return []jira.WikiHit{}, nil
		}
		return s.Client().SearchWiki(ctx, q, r.URL.Query().Get("space"))
	})
	// A page to read: /confluence/pages/{id} (markdown, its images by id).
	get("/wiki/pages/{id}/edit", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id, err := wikiID(r, "id")
		if err != nil {
			return nil, err
		}
		d, err := s.Client().WikiPageDoc(ctx, id)
		if err != nil {
			return nil, err
		}
		return wikiPage{editable: editableOf(d.Doc), ID: d.ID, Title: d.Title, SpaceID: d.SpaceID, ParentID: d.ParentID, Version: d.Version}, nil
	})
	put("/wiki/pages/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id, err := wikiID(r, "id")
		if err != nil {
			return nil, err
		}
		b, err := Body[wikiBody](r)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(b.Title) == "" {
			return nil, badRequest(i18n.T("a page needs a title"))
		}
		doc, err := b.doc()
		if err != nil {
			return nil, err
		}
		err = s.Client().UpdatePage(ctx, id, b.Title, doc, b.Version)
		if errors.Is(err, jira.ErrPageChanged) {
			return nil, httpError{http.StatusConflict, err.Error()}
		}
		return nil, err
	})
	post("/wiki/pages", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[wikiBody](r)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(b.Title) == "" {
			return nil, badRequest(i18n.T("a page needs a title"))
		}
		if b.SpaceID == "" || strings.Trim(b.SpaceID+b.ParentID, "0123456789") != "" {
			return nil, badRequest(i18n.T("bad wiki id"))
		}
		doc, err := b.doc()
		if err != nil {
			return nil, err
		}
		id, err := s.Client().CreatePage(ctx, b.SpaceID, b.ParentID, b.Title, doc)
		if err != nil {
			return nil, err
		}
		return map[string]string{"ID": id}, nil
	})
	del("/wiki/pages/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id, err := wikiID(r, "id")
		if err != nil {
			return nil, err
		}
		return nil, s.Client().DeletePage(ctx, id)
	})
	get("/wiki/pages/{id}/comments", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id, err := wikiID(r, "id")
		if err != nil {
			return nil, err
		}
		return s.Client().PageComments(ctx, id)
	})
	post("/wiki/pages/{id}/comments", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id, err := wikiID(r, "id")
		if err != nil {
			return nil, err
		}
		b, err := Body[wikiBody](r)
		if err != nil {
			return nil, err
		}
		if b.Parent != "" && strings.Trim(b.Parent, "0123456789") != "" {
			return nil, badRequest(i18n.T("bad wiki id"))
		}
		doc, err := b.doc()
		if err != nil {
			return nil, err
		}
		return nil, s.Client().AddPageComment(ctx, id, b.Parent, doc)
	})
}
