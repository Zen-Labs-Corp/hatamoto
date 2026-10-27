package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"

	"hatamoto.dev/core/internal/builder"
)

var (
	styleSpecOnce sync.Once
	styleSpecBody []byte
	styleSpecTag  string
)

// styleSpecJSON is the builder style vocabulary as JSON with a weak ETag, built
// once: the table is fixed for the life of the process.
func styleSpecJSON() ([]byte, string) {
	styleSpecOnce.Do(func() {
		styleSpecBody, _ = json.Marshal(builder.StyleSpecTable())
		sum := sha256.Sum256(styleSpecBody)
		styleSpecTag = `W/"` + hex.EncodeToString(sum[:8]) + `"`
	})
	return styleSpecBody, styleSpecTag
}

// handleStyleSpec serves the style vocabulary the canvas host resolves inline
// previews from. The compiler stays in Go; this is the data it compiles from.
func (a *Admin) handleStyleSpec(w http.ResponseWriter, r *http.Request) {
	body, tag := styleSpecJSON()
	h := w.Header()
	h.Set("ETag", tag)
	h.Set("Cache-Control", "private, max-age=3600")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
