package web

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/ui"
)

// Routes for the issue panel: description and comment editing, history,
// links, children, time in status, dev info and the attachment proxy.

func init() {
	get("/issues/{key}/card", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		cards, err := s.Client().SearchCards(ctx, "key = "+key)
		if err != nil {
			return nil, err
		}
		if len(cards) == 0 {
			return nil, httpError{http.StatusNotFound, i18n.Tf("%s not found", key)}
		}
		return cards[0], nil
	})
	get("/issues/{key}/description", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		raw, err := s.Client().Description(ctx, key)
		if err != nil {
			return nil, err
		}
		return editableOf(raw), nil
	})
	put("/issues/{key}/description", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		b, err := Body[mdBody](r)
		if err != nil {
			return nil, err
		}
		if given(b.Doc) {
			return nil, s.Client().SetDocADF(ctx, key, "description", b.Doc, b.Base)
		}
		return nil, s.Client().SetDescription(ctx, key, b.Markdown, b.Kept, b.Base)
	})
	// A rich-text field (a custom textarea) as markdown to edit like the description.
	get("/issues/{key}/doc/{field}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		if !docField.MatchString(r.PathValue("field")) {
			return nil, badRequest(i18n.T("not a rich-text field"))
		}
		raw, err := s.Client().RawField(ctx, key, r.PathValue("field"))
		if err != nil {
			return nil, err
		}
		return editableOf(raw), nil
	})
	put("/issues/{key}/doc/{field}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		if !docField.MatchString(r.PathValue("field")) {
			return nil, badRequest(i18n.T("not a rich-text field"))
		}
		b, err := Body[mdBody](r)
		if err != nil {
			return nil, err
		}
		if err := required(ctx, s.Client(), key, r.PathValue("field"), jira.Value{Text: b.Markdown, Doc: b.Doc}); err != nil {
			return nil, err
		}
		if given(b.Doc) {
			return nil, s.Client().SetDocADF(ctx, key, r.PathValue("field"), b.Doc, b.Base)
		}
		return nil, s.Client().SetDoc(ctx, key, r.PathValue("field"), b.Markdown, b.Kept, b.Base)
	})
	post("/issues/{key}/description/task", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		b, err := Body[struct {
			N, Total int
			Done     bool
		}](r)
		if err != nil {
			return nil, err
		}
		return nil, s.Client().ToggleTask(ctx, key, b.N, b.Total, b.Done)
	})

	post("/issues/{key}/comments", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		b, err := Body[struct {
			Markdown string
			Mentions []jira.User
			// Raw posts a document as it is: a deleted comment's, to undo the delete.
			Raw json.RawMessage
			// Doc is the comment as ADF, from the visual editor.
			Doc json.RawMessage
			// Visibility limits who reads it: an internal note, a role or a group.
			Visibility jira.Visibility
			// Parent is the comment it replies to (which sets who reads it).
			Parent string
			// Check looks for it among the issue's newest comments first: a
			// post that timed out may have landed. Found says it had.
			Check bool
		}](r)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(strings.TrimSpace(s.UIConfig().ThreadedReplies), "off") {
			b.Parent = "" // ui.threaded_replies: a new comment
		}
		if len(b.Raw) > 0 {
			var doc struct{ Type string }
			if json.Unmarshal(b.Raw, &doc) != nil || doc.Type != "doc" {
				return nil, badRequest(i18n.T("Raw is not a document"))
			}
			return nil, s.Client().AddCommentADFFor(ctx, key, b.Raw, jira.Visibility{}, b.Parent)
		}
		var doc json.RawMessage
		if given(b.Doc) {
			if doc, err = jira.CheckDoc(b.Doc); err != nil {
				return nil, badRequest(err.Error())
			}
		} else if strings.TrimSpace(b.Markdown) != "" {
			doc, _ = json.Marshal(commentDoc(ctx, s, b.Markdown, b.Mentions))
		}
		if doc == nil {
			return nil, badRequest(i18n.T("empty comment"))
		}
		if b.Check {
			if found, err := s.Client().HasComment(ctx, key, doc); err != nil || found {
				return map[string]bool{"Found": found}, err
			}
		}
		return nil, s.Client().AddCommentADFFor(ctx, key, doc, b.Visibility, b.Parent)
	})
	get("/issues/{key}/comments/{id}/edit", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		iss, err := s.Client().Get(ctx, key)
		if err != nil {
			return nil, err
		}
		for _, c := range iss.Comments {
			if c.ID == r.PathValue("id") {
				return editableOf(c.Raw), nil
			}
		}
		return nil, jira.ErrNotFound
	})
	put("/issues/{key}/comments/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		b, err := Body[mdBody](r)
		if err != nil {
			return nil, err
		}
		if given(b.Doc) {
			return nil, s.Client().SetCommentADF(ctx, key, r.PathValue("id"), b.Doc)
		}
		return nil, s.Client().SetComment(ctx, key, r.PathValue("id"), b.Markdown, b.Kept)
	})
	del("/issues/{key}/comments/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		// The answer holds the comment's document, which posted again undoes the delete.
		var raw json.RawMessage
		if iss, err := s.Client().Get(ctx, key); err == nil {
			for _, c := range iss.Comments {
				if c.ID == r.PathValue("id") {
					raw = c.Raw
				}
			}
		}
		if err := s.Client().DeleteComment(ctx, key, r.PathValue("id")); err != nil {
			return nil, err
		}
		return map[string]any{"Raw": raw}, nil
	})

	// Who a comment in the project can be limited to (TUI ctrl+o): an internal
	// note in a Service Desk project, then each role and group the user is in.
	get("/projects/{project}/commentvis", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		vis, err := s.Client().CommentVisibilities(ctx, r.PathValue("project"))
		return nonNil(vis), err
	})

	get("/issues/{key}/history", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		log, err := s.Client().Changelog(ctx, key)
		return nonNil(log), err
	})
	get("/issues/{key}/worklogs", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		logs, err := s.Client().IssueWorklogs(ctx, key)
		return nonNil(logs), err
	})
	get("/issues/{key}/timeinstatus", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		return s.Client().TimeInStatus(ctx, key, time.Now())
	})
	get("/issues/{key}/dev", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		items, err := s.Client().DevInfo(ctx, key)
		if err != nil {
			return nil, err
		}
		return ui.DevWithGitLab(ctx, items, s.opt.GitLab, key), nil
	})
	get("/issues/{key}/children", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		return s.Client().Children(ctx, key)
	})
	get("/issues/{key}/weblinks", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		ls, err := s.Client().WebLinks(ctx, key)
		out := make([]webLink, len(ls))
		for i, l := range ls {
			out[i] = webLink{WebLink: l, Page: s.Client().PageID(l.URL)}
		}
		return out, err
	})
	// GET /confluence/pages/{id}: a page of this site's Confluence as markdown.
	get("/confluence/pages/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		id := r.PathValue("id")
		if id == "" || strings.Trim(id, "0123456789") != "" {
			return nil, badRequest(i18n.T("bad page id"))
		}
		return s.Client().ConfluencePage(ctx, id)
	})
	get("/linktypes", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().LinkTypes(ctx)
	})
	post("/issues/{key}/links", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		// Outward: key does the type's outward verb to Other; else Other does it to key.
		b, err := Body[struct {
			Type, Other string
			Outward     bool
		}](r)
		if err != nil {
			return nil, err
		}
		if !jira.ValidKey(b.Other) || b.Type == "" {
			return nil, badRequest(i18n.T("need a type and an issue key"))
		}
		from, to := key, b.Other
		if !b.Outward {
			from, to = to, from
		}
		return nil, s.Client().LinkIssues(ctx, b.Type, from, to)
	})
	del("/issues/{key}/links/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		return nil, s.Client().DeleteLink(ctx, key, r.PathValue("id"))
	})
	post("/issues/{key}/weblinks", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		b, err := Body[struct{ URL, Title string }](r)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(b.URL, "https://") && !strings.HasPrefix(b.URL, "http://") {
			return nil, badRequest(i18n.T("a link needs an http(s) URL"))
		}
		return nil, s.Client().AddWebLink(ctx, key, b.URL, b.Title)
	})
	del("/issues/{key}/attachments/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		key, err := issueKey(r)
		if err != nil {
			return nil, err
		}
		return nil, s.Client().DeleteAttachment(ctx, key, r.PathValue("id"))
	})

	handle("GET /api/attachments/{id}", attachment)
	handle("GET /api/confluence/images/{id}", pageImage)
}

type mdBody struct {
	Markdown string
	Kept     []json.RawMessage
	// Doc is the document as ADF (the visual editor's), in place of Markdown.
	Doc json.RawMessage
	// Base is the document the editor opened on (editable's); a save
	// finding another one in Jira fails with 409, writing nothing.
	Base string
}

func issueKey(r *http.Request) (string, error) {
	key := r.PathValue("key")
	if !jira.ValidKey(key) {
		return "", badRequest(i18n.T("bad issue key"))
	}
	return key, nil
}

// editable is a document to edit: Doc as Jira has it (null when empty), for
// the visual editor, and as markdown. When markdown can't carry it,
// Editable is false, Reason says why and Markdown is empty. Base marks the
// document (jira.DocBase), for the save to check.
type editable struct {
	Doc      json.RawMessage
	Markdown string
	Kept     []json.RawMessage
	Editable bool
	Reason   string
	Base     string
}

func editableOf(raw json.RawMessage) editable {
	doc := raw
	if len(doc) == 0 {
		doc = json.RawMessage("null")
	}
	ed, err := jira.EditableDescription(raw)
	if err != nil {
		return editable{Doc: doc, Reason: err.Error(), Base: jira.DocBase(raw)}
	}
	if ed.Kept == nil {
		ed.Kept = []json.RawMessage{}
	}
	return editable{Doc: doc, Markdown: ed.Markdown, Kept: ed.Kept, Editable: true, Base: jira.DocBase(raw)}
}

// docField is a field /doc edits: a custom one, or environment.
var docField = regexp.MustCompile(`^(customfield_\d{1,10}|environment)$`)

var attachmentID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// inlineTypes are the content types sniffed from the bytes that may show in
// the browser; everything else downloads. SVG is never among them: it runs
// script.
var inlineTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true}

// attachment streams a Jira attachment with the client's credentials. The
// type comes from the bytes, not from Jira or the query.
func attachment(s *Server, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !attachmentID.MatchString(id) {
		writeErr(w, badRequest(i18n.T("bad attachment id")))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	body, err := s.Client().AttachmentContent(ctx, id)
	serveFile(w, r, body, err, "attachment-"+id)
}

// pageImage is an image of a Confluence page, through the server so the
// token stays here.
func pageImage(s *Server, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || len(id) > 20 || strings.Trim(id, "0123456789") != "" {
		writeErr(w, badRequest(i18n.T("bad image id")))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	body, err := s.Client().PageImage(ctx, id)
	serveFile(w, r, body, err, "image-"+id)
}

// serveFile answers with a downloaded file: shown when the browser can show
// it safely, else saved as ?name= (or fallback).
func serveFile(w http.ResponseWriter, r *http.Request, body []byte, err error, fallback string) {
	if err != nil {
		writeErr(w, err)
		return
	}
	typ := http.DetectContentType(body)
	if i := strings.IndexByte(typ, ';'); i >= 0 {
		typ = typ[:i]
	}
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=86400")
	if inlineTypes[typ] && Q(r, "download") == "" {
		h.Set("Content-Type", typ)
	} else {
		h.Set("Content-Type", "application/octet-stream")
		name := strings.NewReplacer("/", "_", "\\", "_", "\r", "", "\n", "").Replace(Q(r, "name"))
		if name == "" {
			name = fallback
		}
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	_, _ = w.Write(body)
}

// webLink is a remote link and, for a Confluence page on this site, its id
// (Page), which the panel reads in place.
type webLink struct {
	jira.WebLink
	Page string `json:",omitempty"`
}
