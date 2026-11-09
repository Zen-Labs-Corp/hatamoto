package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

func s3MediaServer(t *testing.T) (http.Handler, *s3test.Server, string) {
	t.Helper()
	fake := s3test.NewServer(t)
	local := t.TempDir()
	store, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := media.NewService(nil, media.Config{UploadDir: local, MaxSizeMB: 1, Store: store}, slog.Default())
	s := &Server{mediaService: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{rest...}", s.handleServeMedia)
	return mux, fake, local
}

func getMedia(h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/media/"+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestServeMediaFromObjectStorage(t *testing.T) {
	h, fake, local := s3MediaServer(t)
	png := "\x89PNG\r\n\x1a\n0123456789"
	fake.Put("site/dir/p.png", []byte(png), map[string]string{"sha256": "abc"})

	w := getMedia(h, "dir/p.png", nil)
	if w.Code != 200 || w.Body.String() != png {
		t.Fatalf("GET = %d %q", w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if etag == "" || w.Header().Get("Cache-Control") != "public, max-age=86400, must-revalidate" ||
		w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Length") != "18" {
		t.Errorf("headers = %v", w.Header())
	}

	w = getMedia(h, "dir/p.png", map[string]string{"If-None-Match": etag})
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 || w.Header().Get("ETag") != etag {
		t.Errorf("conditional GET = %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if w = getMedia(h, "dir/p.png", map[string]string{"If-None-Match": `"other"`}); w.Code != 200 {
		t.Errorf("stale validator = %d", w.Code)
	}

	w = getMedia(h, "dir/p.png", map[string]string{"Range": "bytes=8-11"})
	if w.Code != http.StatusPartialContent || w.Body.String() != "0123" {
		t.Errorf("range = %d %q", w.Code, w.Body.String())
	}

	if w = getMedia(h, "dir/none.png", nil); w.Code != http.StatusNotFound {
		t.Errorf("missing = %d", w.Code)
	}
	if w = getMedia(h, "%2e%2e/x", nil); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently {
		t.Errorf("traversal = %d", w.Code)
	}
	if left, _ := os.ReadDir(local); len(left) != 0 {
		t.Errorf("local dir not empty: %v", left)
	}
}

func TestServeMediaObjectStorageDownIs502(t *testing.T) {
	fake := s3test.NewServer(t)
	store, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region,
		Bucket: s3test.Bucket, AccessKey: s3test.AccessKey, SecretKey: "wrong-secret"}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := media.NewService(nil, media.Config{UploadDir: t.TempDir(), MaxSizeMB: 1, Store: store}, slog.Default())
	s := &Server{mediaService: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{rest...}", s.handleServeMedia)
	// Wrong credentials: the bucket answers 403, which must not look like a
	// forbidden path of the site.
	w := getMedia(mux, "p.png", nil)
	if w.Code != http.StatusBadGateway {
		t.Errorf("bad credentials = %d, want 502", w.Code)
	}
}

func TestServeMediaUnreachableObjectStorageIs502(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	endpoint := down.URL
	down.Close()
	store, err := media.NewS3Store(media.S3Target{Endpoint: endpoint, Region: s3test.Region,
		Bucket: s3test.Bucket, AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mediaService: media.NewService(nil, media.Config{UploadDir: t.TempDir(), MaxSizeMB: 1, Store: store}, slog.Default())}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{rest...}", s.handleServeMedia)
	if w := getMedia(mux, "p.png", nil); w.Code != http.StatusBadGateway {
		t.Errorf("unreachable bucket = %d, want 502", w.Code)
	}
}
