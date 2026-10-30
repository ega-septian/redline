// Package ui serves the Redline single-page app and its static assets.
package ui

import (
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"

	"redline/internal/auth"
)

// New serves files from dist at the root. Unknown paths without a file extension
// fall back to index.html so client-side routes work; missing assets stay 404.
// When sessions are enabled, HTML routes require a login cookie; assets stay
// public so the login page and the app can load.
func New(files fs.FS, log *slog.Logger, sessions *auth.Sessions) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	if sessions == nil {
		sessions = auth.New("")
	}
	return &handler{files: files, log: log, sessions: sessions}
}

type handler struct {
	files    fs.FS
	log      *slog.Logger
	sessions *auth.Sessions
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	switch {
	case name == "login":
		h.handleLogin(w, r)
		return
	case name == "logout":
		h.handleLogout(w, r)
		return
	case strings.HasPrefix(name, "assets/"):
		// Public: the login page and the authenticated app both need the bundle.
	case h.sessions.Enabled() && !h.sessions.Valid(r):
		target := "/ui/login?next=" + url.QueryEscape(r.URL.Path)
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	if h.sessions.Enabled() && h.sessions.Valid(r) {
		h.sessions.EnsureCSRF(w, r)
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

func (h *handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.sessions.Enabled() || h.sessions.Valid(r) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
		return
	}
	if r.Method == http.MethodGet {
		h.loginPage(w, http.StatusOK, auth.NextPath(r.URL.Query().Get("next")), "")
		return
	}

	_ = r.ParseForm()
	next := auth.NextPath(r.PostFormValue("next"))
	if !h.sessions.CheckKey(r.PostFormValue("key")) {
		h.loginPage(w, http.StatusUnauthorized, next, "That key does not match this server.")
		return
	}
	h.sessions.SetCookie(w, r)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.sessions.ClearCookie(w, r)
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}

func (h *handler) loginPage(w http.ResponseWriter, status int, next, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	errorBlock := ""
	if errMsg != "" {
		errorBlock = `<p class="error">` + html.EscapeString(errMsg) + `</p>`
	}
	fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sign in · Redline</title>
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0d1117;color:#e6edf3;
font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Inter,Roboto,sans-serif}
.card{width:min(380px,calc(100vw - 32px));border:1px solid #30363d;border-radius:12px;background:#161b22;
padding:26px 24px;box-shadow:0 20px 60px #0006}
.brand{display:flex;align-items:center;gap:10px;margin-bottom:20px}
.logo{width:28px;height:28px;border-radius:8px;background:linear-gradient(135deg,#f85149,#bc8cff 55%%,#58a6ff)}
h1{font-size:17px;margin:0 0 4px}
p{margin:0;color:#8b949e;font-size:12.5px}
label{display:block;margin:18px 0 6px;font-size:12px;color:#8b949e}
input{width:100%%;height:36px;border:1px solid #30363d;border-radius:7px;background:#0d1117;color:#e6edf3;
padding:0 10px;font:13px ui-monospace,SFMono-Regular,Menlo,monospace;outline:none}
input:focus{border-color:#58a6ff66;box-shadow:0 0 0 3px #58a6ff1f}
button{width:100%%;height:36px;margin-top:16px;border:0;border-radius:7px;background:#1f6feb;color:#fff;
font:600 13px inherit;cursor:pointer}
button:hover{background:#388bfd}
.error{margin-top:14px;color:#ff7b72;font-size:12px}
.note{margin-top:18px;padding-top:14px;border-top:1px solid #21262d;font-size:11px;color:#6e7681}
code{font:11px ui-monospace,SFMono-Regular,Menlo,monospace;background:#0d1117;border:1px solid #30363d;
padding:1px 4px;border-radius:4px}
</style></head>
<body><form class="card" method="post" action="/ui/login">
<div class="brand"><div class="logo"></div><div><strong>Redline</strong><br><span style="font-size:11px;color:#6e7681">failure triage</span></div></div>
<h1>Sign in</h1>
<p>Use the server's <code>REDLINE_API_KEY</code>.</p>
%s
<label for="key">API key</label>
<input id="key" name="key" type="password" autocomplete="current-password" autofocus required>
<input type="hidden" name="next" value="%s">
<button type="submit">Continue</button>
<div class="note">The key stays on the server as a signed, HttpOnly cookie. It is never stored in JavaScript.</div>
</form></body></html>`, errorBlock, html.EscapeString(next))
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
