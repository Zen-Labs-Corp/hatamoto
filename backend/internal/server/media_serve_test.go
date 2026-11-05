package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/media"
)

func serveMediaFrom(t *testing.T, dir, path string) *httptest.ResponseRecorder {
	t.Helper()
	svc := media.NewService(nil, media.Config{UploadDir: dir, MaxSizeMB: 1}, slog.Default())
	s := &Server{mediaService: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{rest...}", s.handleServeMedia)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/media/"+path, nil))
	return w
}

func TestServeMedia_HTMLOnDiskIsAnAttachment(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.html"), []byte("<!doctype html><script>alert(1)</script>"), 0o644)
	os.WriteFile(filepath.Join(dir, "p.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644)

	w := serveMediaFrom(t, dir, "x.html")
	h := w.Header()
	if w.Code != 200 || h.Get("Content-Type") != "application/octet-stream" ||
		h.Get("Content-Disposition") != "attachment" || h.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("html served unsafely: %d %v", w.Code, h)
	}

	w = serveMediaFrom(t, dir, "p.png")
	if w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Disposition") != "" {
		t.Errorf("an allowlisted image must stay inline: %v", w.Header())
	}
}

func TestServeMedia_RefusesTraversalAndSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.png"), []byte("\x89PNG\r\n\x1a\nsecret"), 0o644)
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(dir, "link.png")); err != nil {
		t.Skip("symlinks unavailable")
	}
	os.Symlink(outside, filepath.Join(dir, "d"))

	for _, p := range []string{"link.png", "d/secret.png", "../" + filepath.Base(outside) + "/secret.png", "%2e%2e/x"} {
		if w := serveMediaFrom(t, dir, p); w.Code == 200 {
			t.Errorf("GET /media/%s served a file outside the upload directory", p)
		}
	}
}

func TestServeMedia_AVIFCopyIsInline(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "p_large.avif"), []byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"), 0o644)
	w := serveMediaFrom(t, dir, "p_large.avif")
	h := w.Header()
	if w.Code != 200 || h.Get("Content-Type") != "image/avif" || h.Get("Content-Disposition") != "" || h.Get("Content-Security-Policy") != "" {
		t.Errorf("avif served wrong: %d %v", w.Code, h)
	}
}

func TestServeMedia_SVGHeadersAndRevalidatingCache(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`), 0o644)
	w := serveMediaFrom(t, dir, "a.svg")
	h := w.Header()
	if w.Code != 200 || h.Get("Content-Type") != "image/svg+xml" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Disposition") != "" {
		t.Errorf("svg served wrong: %d %v", w.Code, h)
	}
	if h.Get("Cache-Control") != "public, max-age=86400, must-revalidate" || h.Get("ETag") == "" {
		t.Errorf("cache headers: %v", h)
	}
}

// serveMediaThroughMiddleware runs the media handler inside the global
// security-headers middleware, as the real router does.
func serveMediaThroughMiddleware(t *testing.T, dir, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	svc := media.NewService(nil, media.Config{UploadDir: dir, MaxSizeMB: 1}, slog.Default())
	s := &Server{mediaService: svc, cfg: &config.Config{}}
	mux := http.NewServeMux()
	mux.Handle("GET /media/{rest...}", s.securityHeadersMiddleware(http.HandlerFunc(s.handleServeMedia)))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/media/"+path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	mux.ServeHTTP(w, r)
	return w
}

func TestServeMedia_PDFCanBeFramedBySameSiteOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF-1.4\n%abcdefghijklmnop"), 0o644)
	os.WriteFile(filepath.Join(dir, "p.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`), 0o644)
	os.WriteFile(filepath.Join(dir, "x.html"), []byte("<!doctype html>"), 0o644)

	h := serveMediaThroughMiddleware(t, dir, "a.pdf", nil).Header()
	if h.Get("Content-Type") != "application/pdf" || h.Get("X-Frame-Options") != "SAMEORIGIN" ||
		h.Get("Content-Security-Policy") != "frame-ancestors 'self'" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Disposition") != "" ||
		strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("pdf headers: %v", h)
	}

	for _, name := range []string{"p.png", "a.svg", "x.html"} {
		if got := serveMediaThroughMiddleware(t, dir, name, nil).Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q, want DENY", name, got)
		}
	}
}

func TestServeMedia_PDFRangeRequest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF-1.4\n%abcdefghijklmnop"), 0o644)
	w := serveMediaThroughMiddleware(t, dir, "a.pdf", map[string]string{"Range": "bytes=0-3"})
	if w.Code != http.StatusPartialContent || w.Body.String() != "%PDF" {
		t.Errorf("range: %d %q", w.Code, w.Body.String())
	}
}
