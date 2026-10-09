package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// /sw.js is static/sw.js with the shell's version (a hash of every embedded
// file, so a new binary means a new cache) and file list filled in.
var (
	swOnce sync.Once
	swBody []byte
)

func serviceWorker() []byte {
	swOnce.Do(func() {
		root, _ := fs.Sub(staticFS, "static")
		src, err := fs.ReadFile(root, "sw.js")
		if err != nil {
			return
		}
		sum := sha256.New()
		var files []string
		_ = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			b, _ := fs.ReadFile(root, p)
			sum.Write([]byte(p))
			sum.Write(b)
			// fonts and the terminal's vendor/ are cached on first use
			if p != "sw.js" && p != "index.html" && !strings.HasPrefix(p, "fonts/") && !strings.HasPrefix(p, "vendor/") {
				files = append(files, "/"+p)
			}
			return nil
		})
		sort.Strings(files)
		list, _ := json.Marshal(append([]string{"/"}, files...))
		out := strings.ReplaceAll(string(src), "__VERSION__", hex.EncodeToString(sum.Sum(nil)[:8]))
		swBody = []byte(strings.ReplaceAll(out, "__FILES__", string(list)))
	})
	return swBody
}

func serveWorker(w http.ResponseWriter, r *http.Request) {
	b := serviceWorker()
	if b == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

func init() {
	handle("GET /sw.js", func(s *Server, w http.ResponseWriter, r *http.Request) { serveWorker(w, r) })
}

// Static serves the app's own files and its service worker, the same for
// everyone: laneway web for many people hands them out before a sign-in,
// so the worker can keep the shell, and a page from it signs in (api.js).
func Static() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	files := assets(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sw.js" {
			serveWorker(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
