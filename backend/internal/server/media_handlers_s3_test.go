package server

import (
	"crypto/md5"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

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

func countGets(fake *s3test.Server) int {
	n := 0
	for _, r := range fake.Requests() {
		if r.Method == http.MethodGet && strings.Contains(r.Path, "site/") && !strings.Contains(r.Path, "list-type") {
			n++
		}
	}
	return n
}

// HEAD is answered from the lookup and a plain GET streams: neither spools the
// object (no temporary file) and HEAD downloads nothing.
func TestServeMediaObjectStorageHeadAndGetDoNotSpool(t *testing.T) {
	h, fake, _ := s3MediaServer(t)
	png := "\x89PNG\r\n\x1a\n0123456789"
	fake.Put("site/p.png", []byte(png), nil)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	before := countGets(fake)
	req := httptest.NewRequest(http.MethodHead, "/media/p.png", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Length") != "18" {
		t.Errorf("HEAD = %d %v %q", w.Code, w.Header(), w.Body.String())
	}
	if got := countGets(fake); got != before {
		t.Errorf("HEAD downloaded the object (%d GETs)", got-before)
	}
	if w = getMedia(h, "p.png", nil); w.Code != 200 || w.Body.String() != png || w.Header().Get("Content-Length") != "18" {
		t.Errorf("GET = %d %q", w.Code, w.Body.String())
	}
	if countGets(fake) != before+1 {
		t.Errorf("GETs = %d, want one download for the plain GET", countGets(fake)-before)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("GET spooled to disk: %v", left)
	}
}

// Range requests spool behind a semaphore: when every slot is taken the
// answer is 503 with Retry-After, and a free slot is released afterwards.
func TestServeMediaObjectStorageRangeSpoolIsBounded(t *testing.T) {
	h, fake, _ := s3MediaServer(t)
	fake.Put("site/p.png", []byte("\x89PNG\r\n\x1a\n0123456789"), nil)
	old := mediaSpools
	mediaSpools = make(chan struct{}, 1)
	defer func() { mediaSpools = old }()

	mediaSpools <- struct{}{}
	w := getMedia(h, "p.png", map[string]string{"Range": "bytes=0-0"})
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Errorf("busy = %d %v", w.Code, w.Header())
	}
	<-mediaSpools
	if w = getMedia(h, "p.png", map[string]string{"Range": "bytes=8-9"}); w.Code != http.StatusPartialContent || w.Body.String() != "01" {
		t.Errorf("free = %d %q", w.Code, w.Body.String())
	}
	if len(mediaSpools) != 0 {
		t.Error("the spool slot was not released")
	}
}

// A bucket that never answers does not hold the request open past the bound.
func TestServeMediaObjectStorageTimesOut(t *testing.T) {
	hang := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	defer slow.Close()
	defer close(hang)
	store, err := media.NewS3Store(media.S3Target{Endpoint: slow.URL, Region: s3test.Region,
		Bucket: s3test.Bucket, AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mediaService: media.NewService(nil, media.Config{UploadDir: t.TempDir(), MaxSizeMB: 1, Store: store}, slog.Default())}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{rest...}", s.handleServeMedia)
	old := mediaStoreTimeout
	mediaStoreTimeout = 200 * time.Millisecond
	defer func() { mediaStoreTimeout = old }()
	start := time.Now()
	if w := getMedia(mux, "p.png", nil); w.Code != http.StatusBadGateway || time.Since(start) > 5*time.Second {
		t.Errorf("hung bucket = %d after %v", w.Code, time.Since(start))
	}
}

// An object replaced between the lookup and the download is served with the
// size and validator of the bytes actually sent.
func TestServeMediaObjectStorageGetUsesTheDownloadsDetails(t *testing.T) {
	fake := s3test.NewServer(t)
	target, _ := url.Parse(fake.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	older := []byte("\x89PNG\r\n\x1a\nold")
	newer := []byte("\x89PNG\r\n\x1a\nthe replacement is longer")
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fake.Put("site/p.png", newer, nil) // replaced after the lookup
		}
		proxy.ServeHTTP(w, r)
	}))
	defer front.Close()
	fake.Put("site/p.png", older, nil)
	store, err := media.NewS3Store(media.S3Target{Endpoint: front.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mediaService: media.NewService(nil, media.Config{UploadDir: t.TempDir(), MaxSizeMB: 1, Store: store}, slog.Default())}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{rest...}", s.handleServeMedia)

	w := getMedia(mux, "p.png", nil)
	sum := md5.Sum(newer)
	if w.Body.String() != string(newer) || w.Header().Get("Content-Length") != strconv.Itoa(len(newer)) ||
		w.Header().Get("ETag") != `"`+hex.EncodeToString(sum[:])+`"` {
		t.Errorf("GET = %q %v", w.Body.String(), w.Header())
	}
}
