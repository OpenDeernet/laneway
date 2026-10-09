package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cornedor/laneway/internal/safeterm"
)

// Attachment is one file on an issue.
type Attachment struct {
	ID       string
	Filename string
	MimeType string
	Size     int64
}

// IsImage reports whether the attachment is a picture the panel can draw.
func (a Attachment) IsImage() bool {
	return strings.HasPrefix(a.MimeType, "image/")
}

type apiAttachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

// mediaRef is the placeholder target adfToMarkdown writes for a media node;
// resolveMedia swaps it for attachment:<id>.
const mediaRef = "attachment"

// AttachmentScheme prefixes an attachment id in a markdown image target.
const AttachmentScheme = "attachment:"

// escapeMediaAlt keeps a file name from closing the markdown image early.
func escapeMediaAlt(s string) string {
	return strings.NewReplacer("[", "(", "]", ")", "\n", " ").Replace(s)
}

// mediaImage is a picture adfToMarkdown wrote: ![name](attachment#mediaID "size").
var mediaImage = regexp.MustCompile(`!\[([^\]\n]*)\]\(` + mediaRef + `(?:#([0-9a-fA-F-]{1,64}))?((?: "[^"\n]*")?)\)`)

// resolveMedia points each picture adfToMarkdown wrote at its attachment:
// the one of its media file (media, when known), else the one of its name.
// One that matches none keeps its name, ![name](attachment), or reads
// _[attachment]_ without one.
func resolveMedia(md string, atts []Attachment, media map[string]Attachment) string {
	if !strings.Contains(md, "]("+mediaRef) {
		return md
	}
	return mediaImage.ReplaceAllStringFunc(md, func(s string) string {
		m := mediaImage.FindStringSubmatch(s)
		name, id, title := m[1], m[2], m[3]
		at, ok := media[id]
		for _, a := range atts {
			if !ok && name != "" && escapeMediaAlt(a.Filename) == name {
				at, ok = a, true
			}
		}
		switch {
		case ok:
			return "![" + name + "](" + AttachmentScheme + at.ID + title + ")"
		case name == "":
			return "_[attachment]_"
		}
		return "![" + name + "](" + mediaRef + ")"
	})
}

// maxAttachmentBytes caps a download; bigger files are not worth drawing.
const maxAttachmentBytes = 16 << 20

// AttachmentContent downloads an attachment's bytes. Jira redirects to its
// media service, which the http client follows.
func (c *Client) AttachmentContent(ctx context.Context, id string) ([]byte, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	return c.download(ctx, "/rest/api/3/attachment/content/"+url.PathEscape(id), "attachment "+id)
}

// download fetches a file's bytes from path on the site, redirects followed,
// up to maxAttachmentBytes.
func (c *Client) download(ctx context.Context, path, what string) ([]byte, error) {
	reqCtx, moved, stop := c.stallGuard(ctx)
	defer stop()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if _, err := c.authorize(req); err != nil {
		return nil, err
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call jira: %w", stallCause(reqCtx, err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(progressReader{resp.Body, moved}, maxAttachmentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", stallCause(reqCtx, err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusError(resp.StatusCode, what, body, resp.Header.Get("Retry-After"))
	}
	if len(body) > maxAttachmentBytes {
		return nil, fmt.Errorf("%s is over %d MB", what, maxAttachmentBytes>>20)
	}
	return body, nil
}

// AttachmentURL is where a browser signed in to Jira downloads attachment id.
func (c *Client) AttachmentURL(id string) string {
	if c == nil || c.baseURL == "" {
		return ""
	}
	return c.baseURL + "/rest/api/3/attachment/content/" + url.PathEscape(id)
}

// DeleteAttachment deletes attachment id from key.
func (c *Client) DeleteAttachment(ctx context.Context, key, id string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	if err := c.do(ctx, http.MethodDelete, "/rest/api/3/attachment/"+url.PathEscape(id), key, nil, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// UploadAttachment attaches the file at path to key, streamed from disk.
func (c *Client) UploadAttachment(ctx context.Context, key, path string) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = c.UploadAttachmentFrom(ctx, key, filepath.Base(path), f)
	return err
}

// UploadAttachmentFrom attaches what f holds to key as name and returns the
// attachment Jira made (zero when it names none).
func (c *Client) UploadAttachmentFrom(ctx context.Context, key, name string, f io.Reader) (Attachment, error) {
	if !c.Enabled() {
		return Attachment{}, errNotConfigured
	}
	reqCtx, moved, stop := c.stallGuard(ctx)
	defer stop()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", name)
		if err == nil {
			_, err = io.Copy(part, progressReader{f, moved})
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/rest/api/3/issue/"+url.PathEscape(key)+"/attachments", pr)
	if err != nil {
		return Attachment{}, fmt.Errorf("build request: %w", err)
	}
	if _, err := c.authorize(req); err != nil {
		return Attachment{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "no-check") // Jira's CSRF guard for uploads
	c.writing.Add(1)
	defer c.writing.Add(-1)
	resp, err := c.transfer.Do(req)
	if err != nil {
		return Attachment{}, fmt.Errorf("call jira: %w", stallCause(reqCtx, err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Attachment{}, statusError(resp.StatusCode, "upload to "+key, body, resp.Header.Get("Retry-After"))
	}
	c.Invalidate(key)
	var made []apiAttachment
	if json.Unmarshal(body, &made) != nil || len(made) == 0 {
		return Attachment{}, nil
	}
	a := made[0]
	return Attachment{ID: a.ID, Filename: a.Filename, MimeType: a.MimeType, Size: a.Size}, nil
}

// mediaFile finds the Media Services file id in where Jira sends an
// attachment's content: …/file/<uuid>/binary?….
var mediaFile = regexp.MustCompile(`/file/([0-9a-fA-F-]{36})/`)

// MediaID is the Media Services file id an ADF media node needs to show
// attachment id. Jira has no API for it; its content redirect names it.
// Each one is asked once.
func (c *Client) MediaID(ctx context.Context, id string) (string, error) {
	if !c.Enabled() {
		return "", errNotConfigured
	}
	c.mu.Lock()
	m, ok := c.mediaIDs[id]
	c.mu.Unlock()
	if ok {
		return m, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/rest/api/3/attachment/content/"+url.PathEscape(id), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	if _, err := c.authorize(req); err != nil {
		return "", err
	}
	hc := *c.transfer
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("call jira: %w", err)
	}
	resp.Body.Close()
	f := mediaFile.FindStringSubmatch(resp.Header.Get("Location"))
	if f == nil {
		return "", fmt.Errorf("attachment %s: no media id (%s)", id, resp.Status)
	}
	c.mu.Lock()
	if c.mediaIDs == nil {
		c.mediaIDs = map[string]string{}
	}
	c.mediaIDs[id] = f[1]
	c.mu.Unlock()
	return f[1], nil
}

// mediaFiles maps the Media Services file id of each of atts to it, four
// asked at a time; one Jira won't name is left out.
func (c *Client) mediaFiles(ctx context.Context, atts []Attachment) map[string]Attachment {
	out := map[string]Attachment{}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 4)
	)
	for _, at := range atts {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if id, err := c.MediaID(ctx, at.ID); err == nil {
				mu.Lock()
				out[id] = at
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return out
}

// MediaAttachment is the attachment of key whose Media Services file is
// media, the file an ADF media node names.
func (c *Client) MediaAttachment(ctx context.Context, key, media string) (Attachment, error) {
	iss, err := c.Get(ctx, key)
	if err != nil {
		return Attachment{}, err
	}
	c.mu.Lock()
	for _, at := range iss.Attachments {
		if c.mediaIDs[at.ID] == media {
			c.mu.Unlock()
			return at, nil
		}
	}
	c.mu.Unlock()
	if at, ok := c.mediaFiles(ctx, iss.Attachments)[media]; ok {
		return at, nil
	}
	return Attachment{}, ErrNotFound
}

// issueMedia is what a's documents need to name their files: the link of
// each inline one by media id (files), and the attachment of each media id
// (media) when a picture's name alone doesn't tell its attachment. Both nil
// when names do; one Jira asks about is left out.
func (c *Client) issueMedia(ctx context.Context, a apiIssue) (files map[string]string, media map[string]Attachment) {
	if len(a.Fields.Attachment) == 0 {
		return nil, nil
	}
	docs := []json.RawMessage{a.Fields.Description}
	if cm := a.Fields.Comment; cm != nil {
		for _, ac := range cm.Comments {
			docs = append(docs, ac.Body)
		}
	}
	names := map[string]int{}
	for _, at := range a.Fields.Attachment {
		names[escapeMediaAlt(safeterm.Line(at.Filename))]++
	}
	inline, unnamed := false, false
	for _, d := range docs {
		walkMedia(d, func(typ, alt string) {
			inline = inline || typ == "mediaInline"
			unnamed = unnamed || typ == "media" && names[escapeMediaAlt(safeterm.Line(alt))] != 1
		})
	}
	if !inline && !unnamed {
		return nil, nil
	}
	atts := make([]Attachment, len(a.Fields.Attachment))
	for i, at := range a.Fields.Attachment {
		atts[i] = Attachment{ID: at.ID, Filename: safeterm.Line(at.Filename), MimeType: at.MimeType, Size: at.Size}
	}
	media = c.mediaFiles(ctx, atts)
	if inline {
		files = map[string]string{}
		for id, at := range media {
			files[id] = "[" + escapeMediaAlt(at.Filename) + "](" + c.AttachmentURL(at.ID) + ")"
		}
	}
	return files, media
}

// walkMedia calls fn with the type and alt of each media and mediaInline node of doc.
func walkMedia(doc json.RawMessage, fn func(typ, alt string)) {
	if !bytes.Contains(doc, []byte(`"media`)) {
		return
	}
	var n adfNode
	if json.Unmarshal(doc, &n) != nil {
		return
	}
	var walk func(n adfNode)
	walk = func(n adfNode) {
		if n.Type == "media" || n.Type == "mediaInline" {
			alt, _ := n.Attrs["alt"].(string)
			fn(n.Type, alt)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(n)
}

// imageLine is a markdown image alone on its line pointing at an attachment.
var imageLine = regexp.MustCompile(`^( *)!\[([^\]]*)\]\(` + AttachmentScheme + `(\d+)\)\s*$`)

// EmbedImages points each image line at an attachment (![name](attachment:ID),
// the web editor's pasted image) at its media file instead, so
// MarkdownToADF makes it a picture in the document. One whose media id
// can't be had stays as it is.
func (c *Client) EmbedImages(ctx context.Context, md string) string {
	if !strings.Contains(md, "]("+AttachmentScheme) {
		return md
	}
	lines := strings.Split(md, "\n")
	fence := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			fence = !fence
		}
		m := imageLine.FindStringSubmatch(ln)
		if fence || m == nil {
			continue
		}
		if id, err := c.MediaID(ctx, m[3]); err == nil {
			lines[i] = m[1] + "![" + m[2] + "](" + mediaScheme + id + ")"
		}
	}
	return strings.Join(lines, "\n")
}

// mediaScheme prefixes a Media Services file id in a markdown image target;
// MarkdownToADF makes such a line a mediaSingle.
const mediaScheme = "media:"

// DownloadAttachment saves attachment id as name in dir, never over an
// existing file ("a (1).png"), and returns the path written.
func (c *Client) DownloadAttachment(ctx context.Context, id, name, dir string) (string, error) {
	if !c.Enabled() {
		return "", errNotConfigured
	}
	reqCtx, moved, stop := c.stallGuard(ctx)
	defer stop()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.AttachmentURL(id), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	if _, err := c.authorize(req); err != nil {
		return "", err
	}
	resp, err := c.transfer.Do(req)
	if err != nil {
		return "", fmt.Errorf("call jira: %w", stallCause(reqCtx, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", statusError(resp.StatusCode, "attachment "+id, body, resp.Header.Get("Retry-After"))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, path, err := createFree(dir, filepath.Base(name))
	if err != nil {
		return "", err
	}
	if _, err = io.Copy(f, progressReader{resp.Body, moved}); err == nil {
		err = f.Close()
	} else {
		f.Close()
		err = stallCause(reqCtx, err)
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// errStalled is a transfer that moved nothing for the client's timeout.
var errStalled = errors.New("transfer stalled")

// stallGuard bounds a transfer by its progress, not its length: the context
// ends once moved went uncalled for c.timeout. stop releases it.
func (c *Client) stallGuard(ctx context.Context) (context.Context, func(), func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	t := time.AfterFunc(c.timeout, func() { cancel(fmt.Errorf("%w: nothing moved for %s", errStalled, c.timeout)) })
	return ctx, func() { t.Reset(c.timeout) }, func() { t.Stop(); cancel(nil) }
}

// stallCause is err, or why stallGuard ended ctx.
func stallCause(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); errors.Is(cause, errStalled) {
		return cause
	}
	return err
}

// progressReader calls moved on every read that got bytes.
type progressReader struct {
	r     io.Reader
	moved func()
}

func (p progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.moved()
	}
	return n, err
}

// createFree creates name in dir, or "name (n).ext" when taken.
func createFree(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 0; n < 1000; n++ {
		try := name
		if n > 0 {
			try = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		path := filepath.Join(dir, try)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, path, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("no free name for %s in %s", name, dir)
}
