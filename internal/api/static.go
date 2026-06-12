package api

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func init() {
	// Go's built-in MIME table doesn't know .webmanifest; without this it would
	// be served as text/plain and some browsers would reject the manifest.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// staticHandler serves the embedded Svelte build with SPA fallback: a path that
// isn't a real file (and isn't an API/WS route, which are matched first) returns
// index.html so client-side routing works.
func (s *Server) staticHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		// The service worker must be served from the root with no caching so
		// updates take effect promptly across reloads.
		if name == "service-worker.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		// Vite content-hashes everything under assets/ (index-<hash>.js), so
		// those files are immutable: cache them hard. The embedded FS has zero
		// modtimes → http.FileServer emits no Last-Modified/ETag, and without
		// any caching signal browsers re-download the whole bundle on every
		// cold load — which matters on plain-HTTP LAN, where no service worker
		// can run (not a secure context) to absorb it.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if _, err := fs.Stat(s.dist, name); err != nil {
			s.serveIndex(w) // not a real file → SPA fallback
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndex writes index.html (the SPA entrypoint). When the frontend hasn't
// been built yet (e.g. a bare `go build` without `make web`), there is no
// index.html in the embedded FS, so we serve a friendly placeholder instead of
// a bare error. The canonical run paths (Docker, `make run`) always build the
// frontend first, so users normally never see the placeholder.
func (s *Server) serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	index, err := fs.ReadFile(s.dist, "index.html")
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, placeholderHTML)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}

// placeholderHTML is shown when the frontend hasn't been built. It is
// self-contained (no external assets) so it works from an otherwise-empty embed.
const placeholderHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>Setu</title>
<style>
  body{margin:0;min-height:100vh;display:grid;place-items:center;color:#fff;
    font-family:ui-sans-serif,system-ui,-apple-system,sans-serif;
    background:linear-gradient(135deg,#6366f1,#8b5cf6 50%,#ec4899)}
  .card{background:rgba(255,255,255,.12);backdrop-filter:blur(12px);
    border:1px solid rgba(255,255,255,.2);border-radius:1.25rem;
    padding:2.5rem 3rem;text-align:center;max-width:28rem;
    box-shadow:0 20px 60px rgba(0,0,0,.25)}
  h1{margin:0 0 .25rem;font-size:2.5rem;letter-spacing:-.02em}
  .sub{opacity:.85;margin:0 0 1.5rem}
  code{background:rgba(0,0,0,.25);padding:.2rem .5rem;border-radius:.4rem}
</style></head>
<body><div class="card">
  <h1>Setu&nbsp;सेतु</h1>
  <p class="sub">The bridge is running.</p>
  <p>The web UI isn't built yet. Build it with:</p>
  <p><code>make web</code></p>
</div></body></html>`
