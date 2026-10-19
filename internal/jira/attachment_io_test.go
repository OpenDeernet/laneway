package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadAttachment(t *testing.T) {
	var name, content, token string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("X-Atlassian-Token")
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		b, _ := io.ReadAll(f)
		name, content = h.Filename, string(b)
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "log.txt")
	os.WriteFile(path, []byte("hello"), 0o600)
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	if err := c.UploadAttachment(context.Background(), "A-1", path); err != nil {
		t.Fatal(err)
	}
	if name != "log.txt" || content != "hello" || token != "no-check" {
		t.Errorf("name %q content %q token %q", name, content, token)
	}
}

// TestDownloadAttachment: saved under its name, a taken name numbered.
func TestDownloadAttachment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "PNG")
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	dir := t.TempDir()
	for _, want := range []string{"shot.png", "shot (1).png"} {
		path, err := c.DownloadAttachment(context.Background(), "10", "../shot.png", dir)
		if err != nil || path != filepath.Join(dir, want) {
			t.Fatalf("path %q, %v", path, err)
		}
		if b, _ := os.ReadFile(path); string(b) != "PNG" {
			t.Errorf("content %q", b)
		}
	}
}

// TestAttachmentSlowNotStalled: a download that takes longer than the
// timeout still lands while bytes keep moving; one that stops fails.
func TestAttachmentSlowNotStalled(t *testing.T) {
	stall := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := range 5 {
			if stall && i == 2 {
				time.Sleep(900 * time.Millisecond)
			}
			io.WriteString(w, "chunk")
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond) // 500ms in all: past the timeout, each gap well inside it
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", Timeout: 300 * time.Millisecond})
	path, err := c.DownloadAttachment(context.Background(), "1", "a.txt", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); len(b) != 25 {
		t.Errorf("saved %d bytes", len(b))
	}
	if b, err := c.AttachmentContent(context.Background(), "1"); err != nil || len(b) != 25 {
		t.Errorf("content: %d bytes, %v", len(b), err)
	}
	stall = true
	if _, err := c.DownloadAttachment(context.Background(), "1", "b.txt", t.TempDir()); !errors.Is(err, errStalled) {
		t.Errorf("stalled download: %v", err)
	}
}

// TestEmbedImages: an image line at an attachment becomes a mediaSingle of
// the media file its content redirect names; one in a code block, or one
// without a media id, stays text.
func TestEmbedImages(t *testing.T) {
	const uuid = "c4684650-1111-2222-3333-444455556666"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/attachment/content/10" {
			http.Redirect(w, r, "https://api.media.atlassian.com/file/"+uuid+"/binary?token=x", http.StatusSeeOther)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	md := c.EmbedImages(context.Background(), "Look:\n![shot.png](attachment:10)\n```\n![a](attachment:10)\n```\n![b](attachment:11)")
	want := "Look:\n![shot.png](media:" + uuid + ")\n```\n![a](attachment:10)\n```\n![b](attachment:11)"
	if md != want {
		t.Fatalf("got %q", md)
	}
	doc := MarkdownToADF(md)
	b, _ := json.Marshal(doc["content"].([]any)[1])
	if got := string(b); got != `{"attrs":{"layout":"center"},"content":[{"attrs":{"alt":"shot.png","collection":"","id":"`+uuid+`","type":"file"},"type":"media"}],"type":"mediaSingle"}` {
		t.Errorf("block %s", got)
	}
}

// TestInlineFileNamed: a file inline in a comment, which ADF names only by
// media id, shows as its attachment's name linked; one Jira can't place
// stays _[file]_.
func TestInlineFileNamed(t *testing.T) {
	const uuid = "f12051de-4615-4686-925d-8142367018b1"
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/attachment/content/120631":
			asked++
			http.Redirect(w, r, "https://api.media.atlassian.com/file/"+uuid+"/binary?token=x", http.StatusSeeOther)
		case "/rest/api/3/issue/JB-1":
			w.Write([]byte(`{"key":"JB-1","fields":{"summary":"S","attachment":[{"id":"120631","filename":"Re [FW] feed.eml"}],` +
				`"comment":{"total":1,"comments":[{"id":"1","body":{"type":"doc","content":[{"type":"paragraph","content":[` +
				`{"type":"mediaInline","attrs":{"id":"` + uuid + `","type":"file"}},{"type":"text","text":" and "},{"type":"mediaInline","attrs":{"id":"gone"}}]}]}}]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok", FlatReplies: true})
	for range 2 {
		c.Invalidate("JB-1")
		iss, err := c.Get(context.Background(), "JB-1")
		if err != nil {
			t.Fatal(err)
		}
		if want := "[Re (FW) feed.eml](" + srv.URL + "/rest/api/3/attachment/content/120631) and _[file]_"; iss.Comments[0].Body != want {
			t.Errorf("body %q\nwant %q", iss.Comments[0].Body, want)
		}
	}
	if asked != 1 {
		t.Errorf("asked Jira for the media id %d times, want once", asked)
	}
}

// TestOpenAttachmentMediaURL: a range goes straight to the media link Jira
// last redirected to, and back through Jira once that link fails.
func TestOpenAttachmentMediaURL(t *testing.T) {
	var jiraHits, mediaHits int
	expired := false
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaHits++
		if expired {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader("0123456789"))
	}))
	defer media.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jiraHits++
		expired = false
		http.Redirect(w, r, media.URL+"/file", http.StatusSeeOther)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Email: "me@x.test", APIToken: "tok"})
	read := func(rng string) string {
		t.Helper()
		s, err := c.OpenAttachment(context.Background(), "7", rng)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		b, _ := io.ReadAll(s)
		return string(b) + "|" + s.Range
	}
	if got := read("bytes=2-4"); got != "234|bytes 2-4/10" {
		t.Errorf("first: %q", got)
	}
	if got := read("bytes=5-"); got != "56789|bytes 5-9/10" || jiraHits != 1 {
		t.Errorf("cached: %q, %d jira hits", got, jiraHits)
	}
	expired = true
	if got := read(""); got != "0123456789|" || jiraHits != 2 {
		t.Errorf("expired link: %q, %d jira hits", got, jiraHits)
	}
}
