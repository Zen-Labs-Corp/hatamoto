package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
