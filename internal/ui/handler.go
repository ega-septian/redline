// Package ui serves the Redline single-page app and its static assets.
package ui

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

// New serves files from dist at the root. Unknown paths without a file extension
// fall back to index.html so client-side routes work; missing assets stay 404.
func New(files fs.FS, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return &handler{files: files, log: log}
}

type handler struct {
	files fs.FS
	log   *slog.Logger
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	if info, err := fs.Stat(h.files, name); err == nil && !info.IsDir() {
		h.serveFile(w, r, name)
		return
	}

	// Client-side route: /ui/failures/abc must return the app shell.
	if path.Ext(name) == "" {
		if _, err := fs.Stat(h.files, "index.html"); err != nil {
			h.notBuilt(w)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h.serveFile(w, r, "index.html")
		return
	}

	if name == "index.html" {
		h.notBuilt(w)
		return
	}

	http.NotFound(w, r)
}

func (h *handler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}

	// http.FileServer redirects /index.html to ./; serve the shell directly so
	// both / and client-side routes return 200.
	if name == "index.html" {
		data, err := fs.ReadFile(h.files, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
		return
	}

	// Rewrite the path for http.FileServer, which resolves it against the FS root.
	clone := r.Clone(r.Context())
	clone.URL.Path = "/" + name
	http.FileServer(http.FS(h.files)).ServeHTTP(w, clone)
}

// notBuilt is what a fresh checkout sees before `npm run build`: a clear
// instruction instead of a blank page or a failed `go build`.
func (h *handler) notBuilt(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprint(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Redline UI not built</title>
<style>body{background:#0d1117;color:#e6edf3;font:15px/1.6 system-ui;max-width:42rem;margin:10vh auto;padding:0 1.5rem}
code{background:#161b22;border:1px solid #30363d;padding:.15rem .4rem;border-radius:4px}</style></head>
<body><h1>Redline UI is not built</h1>
<p>Build the React app, then restart the server:</p>
<pre><code>cd web &amp;&amp; npm install &amp;&amp; npm run build</code></pre>
<p>For UI development, run <code>npm run dev</code> in <code>web/</code>; Vite proxies the API to port 8787.</p>
</body></html>`)
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set(
		"Content-Security-Policy",
		"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
			"script-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'",
	)
}
