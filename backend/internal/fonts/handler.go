package fonts

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Source is where Handler reads font bytes from.
type Source interface {
	Data(ctx context.Context, hash string) ([]byte, error)
}

var fileRe = regexp.MustCompile(`^([0-9a-f]{64})\.woff2$`)

// Handler serves GET /fonts/{file} where file is "<sha256>.woff2". The name is
// the content hash, so the answer never changes and caches for a year.
func Handler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		m := fileRe.FindStringSubmatch(r.PathValue("file"))
		if m == nil {
			http.NotFound(w, r)
			return
		}
		data, err := src.Data(r.Context(), m[1])
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		etag := `"` + m[1] + `"`
		h := w.Header()
		h.Set("ETag", etag)
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("X-Content-Type-Options", "nosniff")
		// The sandboxed editor canvas has an opaque origin and needs CORS.
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Content-Type", "font/woff2")
		if strings.Contains(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}
}
