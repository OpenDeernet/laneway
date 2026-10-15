package web

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/ui"
)

// Drafts: a comment or description being written, kept in the state file
// where the TUI keeps its own (internal/ui/drafts.go), so a reload, or the
// terminal, brings it back. Ids are comment:KEY, desc:KEY,
// desc:KEY:comment:ID and desc:KEY:field:ID; create is the browser's
// create form, its text the form as JSON.

const draftPrefix = "jira_tab:draft:"

// docPrefix keeps a draft the visual editor wrote as ADF beside it, the
// markdown it was saved as first: the TUI reads and writes the markdown, so
// a draft whose markdown it changed since comes back as that markdown.
const docPrefix = "laneway:draftdoc:"

// docBase is a draft's Base: none, or a jira.DocBase.
var docBase = regexp.MustCompile(`^[0-9a-f]{0,64}$`)

var draftID = regexp.MustCompile(`^(comment|desc):([A-Z][A-Z0-9_]*-[0-9]+)(:comment:[0-9]+|:field:[A-Za-z0-9_]+)?$`)

// wikiDraftID is a wiki page's draft (its edit) or a comment's on it, new
// or a reply.
var wikiDraftID = regexp.MustCompile(`^wiki:[0-9]{1,18}(:comment:(?:new|[0-9]{1,18}))?$`)

func draftKey(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if id == "create" {
		return draftPrefix + id, nil
	}
	if wikiDraftID.MatchString(id) {
		return draftPrefix + id, nil
	}
	m := draftID.FindStringSubmatch(id)
	if m == nil || (m[1] == "comment" && m[3] != "") || !jira.ValidKey(m[2]) {
		return "", badRequest(i18n.T("bad draft id"))
	}
	return draftPrefix + id, nil
}

func init() {
	get("/drafts/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		k, err := draftKey(r)
		if err != nil {
			return nil, err
		}
		v, _, _ := s.opt.Store.GetMeta(k)
		d, ok := ui.DecodeDraft(v)
		if !ok {
			return map[string]any{"Text": ""}, nil
		}
		out := map[string]any{"Text": d.Text, "When": d.At, "Base": d.Base}
		if dv, _, _ := s.opt.Store.GetMeta(docPrefix + strings.TrimPrefix(k, draftPrefix)); dv != "" {
			if md, doc, ok := strings.Cut(dv, "\x00"); ok && md == d.Text {
				out["Doc"] = json.RawMessage(doc)
			}
		}
		return out, nil
	})
	put("/drafts/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		k, err := draftKey(r)
		if err != nil {
			return nil, err
		}
		// Base is the jira.DocBase of the document a desc: draft edits. Doc, the
		// visual editor's, is kept as it is; Text is then its markdown.
		b, err := Body[struct {
			Text, Base string
			Doc        json.RawMessage
		}](r)
		if err != nil {
			return nil, err
		}
		dk := docPrefix + strings.TrimPrefix(k, draftPrefix)
		var doc json.RawMessage
		if given(b.Doc) {
			if doc, err = jira.CheckDoc(b.Doc); err != nil {
				return nil, badRequest(err.Error())
			}
			b.Text = ""
			if doc != nil {
				b.Text = draftMarkdown(doc)
			}
		}
		if strings.TrimSpace(b.Text) == "" {
			_ = s.opt.Store.DeleteMeta(dk)
			return nil, s.opt.Store.DeleteMeta(k)
		}
		if !docBase.MatchString(b.Base) {
			return nil, badRequest(i18n.T("bad draft base"))
		}
		if doc != nil {
			if err := s.opt.Store.SetMeta(dk, b.Text+"\x00"+string(doc)); err != nil {
				return nil, err
			}
		} else {
			_ = s.opt.Store.DeleteMeta(dk)
		}
		return nil, s.opt.Store.SetMeta(k, ui.EncodeDraft(ui.Draft{Text: b.Text, Base: b.Base, At: time.Now()}))
	})
	del("/drafts/{id}", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		k, err := draftKey(r)
		if err != nil {
			return nil, err
		}
		_ = s.opt.Store.DeleteMeta(docPrefix + strings.TrimPrefix(k, draftPrefix))
		return nil, s.opt.Store.DeleteMeta(k)
	})
}

// draftMarkdown is doc as the TUI edits it: markdown with its placeholder
// lines when markdown can carry it, else as it reads.
func draftMarkdown(doc json.RawMessage) string {
	if ed, err := jira.EditableDescription(doc); err == nil && len(ed.Kept) == 0 {
		return ed.Markdown
	}
	return jira.DocMarkdown(doc)
}
