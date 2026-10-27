package packages

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

func init() {
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".mjs", "text/javascript")
}

// ServeFile writes one file of the package (GET and HEAD). name is a slash path
// inside the package; anything that is not a regular file is a 404. Responses
// are marked nosniff and, as untrusted content, sandboxed: an SVG or HTML file
// opened directly runs without script and without the site's origin, unless
// the caller passes its own csp.
func ServeFile(w http.ResponseWriter, r *http.Request, p *Package, name, cacheControl, csp string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !fs.ValidPath(name) || strings.HasSuffix(name, "/") {
		http.NotFound(w, r)
		return
	}
	fi, err := fs.Stat(p.FS(), name)
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	b, err := p.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("Cache-Control", cacheControl)
	h.Set("X-Content-Type-Options", "nosniff")
	if csp == "" {
		csp = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:"
	}
	h.Set("Content-Security-Policy", csp)
	// Public static files; a sandboxed iframe has an opaque origin and needs
	// CORS to load module scripts and fonts.
	h.Set("Access-Control-Allow-Origin", "*")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}
