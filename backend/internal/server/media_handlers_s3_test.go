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
	"sync"
	"sync/atomic"
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

// A single range is answered 206 straight from the bucket with a ranged
// request: nothing is spooled to disk and no slot limits it. The type is
// sniffed from the start of the object even when the range starts later.
func TestServeMediaObjectStorageSingleRangeComesFromTheBucket(t *testing.T) {
	h, fake, _ := s3MediaServer(t)
	png := "\x89PNG\r\n\x1a\n0123456789"
	fake.Put("site/p.png", []byte(png), nil)
	fake.Put("site/page.png", []byte("<html><script>alert(1)</script>0123456789"), nil)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	full := getMedia(h, "p.png", nil)
	etag := full.Header().Get("ETag")
	for _, rng := range []string{"bytes=8-11", "bytes=10-", "bytes=-4"} {
		w := getMedia(h, "p.png", map[string]string{"Range": rng})
		want := map[string]string{"bytes=8-11": "0123", "bytes=10-": "23456789", "bytes=-4": "6789"}[rng]
		if w.Code != http.StatusPartialContent || w.Body.String() != want || w.Header().Get("Content-Length") != strconv.Itoa(len(want)) ||
			!strings.HasSuffix(w.Header().Get("Content-Range"), "/18") || w.Header().Get("ETag") != etag ||
			w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("Content-Type") != "image/png" {
			t.Errorf("%s = %d %q %v", rng, w.Code, w.Body.String(), w.Header())
		}
	}
	if w := getMedia(h, "p.png", map[string]string{"Range": "bytes=0-0"}); w.Header().Get("Content-Range") != "bytes 0-0/18" || w.Body.String() != "\x89" || w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("first byte = %v %q", w.Header(), w.Body.String())
	}
	// A probe that starts at 0 but is shorter than the sniff window still gets the real type.
	fake.Put("site/v.mp4", append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), make([]byte, 600)...), nil)
	if w := getMedia(h, "v.mp4", map[string]string{"Range": "bytes=0-1"}); w.Code != http.StatusPartialContent ||
		w.Header().Get("Content-Type") != "video/mp4" || w.Header().Get("Content-Disposition") != "" {
		t.Errorf("mp4 probe = %d %v", w.Code, w.Header())
	}
	// Not a media type: the same download fallback as a plain GET, from a range that starts late.
	w := getMedia(h, "page.png", map[string]string{"Range": "bytes=10-"})
	if w.Code != http.StatusPartialContent || w.Header().Get("Content-Type") != "application/octet-stream" ||
		w.Header().Get("Content-Disposition") != "attachment" || w.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("html range = %d %v", w.Code, w.Header())
	}
	var ranged int
	for _, r := range fake.Requests() {
		if r.Method == http.MethodGet && r.Header.Get("Range") != "" {
			ranged++
		}
	}
	if ranged < 5 {
		t.Errorf("only %d ranged requests reached the bucket", ranged)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("a range spooled to disk: %v", left)
	}
}

func TestServeMediaObjectStorageRangeEdgeCases(t *testing.T) {
	h, fake, _ := s3MediaServer(t)
	png := "\x89PNG\r\n\x1a\n0123456789"
	fake.Put("site/p.png", []byte(png), nil)
	etag := getMedia(h, "p.png", nil).Header().Get("ETag")

	w := getMedia(h, "p.png", map[string]string{"Range": "bytes=18-"})
	if w.Code != http.StatusRequestedRangeNotSatisfiable || w.Header().Get("Content-Range") != "bytes */18" {
		t.Errorf("unsatisfiable = %d %v", w.Code, w.Header())
	}
	for name, hdr := range map[string]map[string]string{
		"multi-range":    {"Range": "bytes=0-1,4-5"},
		"other unit":     {"Range": "items=0-1"},
		"stale If-Range": {"Range": "bytes=8-9", "If-Range": `"other"`},
		"weak If-Range":  {"Range": "bytes=8-9", "If-Range": "W/" + etag},
	} {
		if w := getMedia(h, "p.png", hdr); w.Code != 200 || w.Body.String() != png {
			t.Errorf("%s = %d %q, want the whole object", name, w.Code, w.Body.String())
		}
	}
	if w := getMedia(h, "p.png", map[string]string{"Range": "bytes=8-9", "If-Range": etag}); w.Code != http.StatusPartialContent || w.Body.String() != "01" {
		t.Errorf("matching If-Range = %d %q", w.Code, w.Body.String())
	}
	if w := getMedia(h, "p.png", map[string]string{"Range": "bytes=8-9", "If-None-Match": etag}); w.Code != http.StatusNotModified {
		t.Errorf("Range with a current validator = %d", w.Code)
	}
}

// Many slow readers of ranges do not block each other or refuse anyone: the
// bucket is read as the client takes the bytes, with no global slot.
func TestServeMediaObjectStorageManyRangesDoNotRefuse(t *testing.T) {
	h, fake, _ := s3MediaServer(t)
	fake.Put("site/v.png", []byte("\x89PNG\r\n\x1a\n0123456789"), nil)
	var wg sync.WaitGroup
	var refused atomic.Int32
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := getMedia(h, "v.png", map[string]string{"Range": "bytes=0-"}); w.Code != http.StatusPartialContent {
				refused.Add(1)
			}
		}()
	}
	wg.Wait()
	if refused.Load() != 0 {
		t.Errorf("%d concurrent range requests were refused", refused.Load())
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

// A bucket that stalls in the middle of an object does not pin the request.
func TestServeMediaObjectStorageStalledBodyIsCut(t *testing.T) {
	hang := make(chan struct{})
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nsome bytes"))
		w.(http.Flusher).Flush()
		<-hang
	}))
	defer stall.Close()
	defer close(hang)
	store, err := media.NewS3Store(media.S3Target{Endpoint: stall.URL, Region: s3test.Region,
		Bucket: s3test.Bucket, AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mediaService: media.NewService(nil, media.Config{UploadDir: t.TempDir(), MaxSizeMB: 1, Store: store}, slog.Default())}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{rest...}", s.handleServeMedia)
	old := mediaIdleTimeout
	mediaIdleTimeout = 200 * time.Millisecond
	defer func() { mediaIdleTimeout = old }()
	done := make(chan struct{})
	go func() { getMedia(mux, "p.png", nil); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled body held the request open")
	}
}

// serveFrontedByS3 serves /media from the store at endpoint.
func serveFrontedByS3(t *testing.T, endpoint, prefix string) http.Handler {
	t.Helper()
	store, err := media.NewS3Store(media.S3Target{Endpoint: endpoint, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: prefix, AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mediaService: media.NewService(nil, media.Config{UploadDir: t.TempDir(), MaxSizeMB: 1, Store: store}, slog.Default())}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{rest...}", s.handleServeMedia)
	return mux
}

// A ranged request validated against the old object is never answered with
// bytes of a replacement that landed between the lookup and the download.
func TestServeMediaObjectStorageReplaceDuringRangedGet(t *testing.T) {
	older := []byte("\x89PNG\r\n\x1a\n0123456789abcdef")
	newer := []byte("\x89PNG\r\n\x1a\nNEWNEWNEWNEWNEWNEW-and-longer")
	oldSum, newSum := md5.Sum(older), md5.Sum(newer)
	oldETag, newETag := `"`+hex.EncodeToString(oldSum[:])+`"`, `"`+hex.EncodeToString(newSum[:])+`"`

	for _, tc := range []struct {
		name string
		hdr  map[string]string
	}{
		{"if-range", map[string]string{"Range": "bytes=8-11", "If-Range": oldETag}},
		{"no if-range", map[string]string{"Range": "bytes=8-11"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := s3test.NewServer(t)
			target, _ := url.Parse(fake.URL)
			proxy := httputil.NewSingleHostReverseProxy(target)
			var gets atomic.Int32
			front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && gets.Add(1) == 1 {
					fake.Put("site/p.png", newer, nil) // replaced before the first download
				}
				proxy.ServeHTTP(w, r)
			}))
			defer front.Close()
			fake.Put("site/p.png", older, nil)
			w := getMedia(serveFrontedByS3(t, front.URL, "site/"), "p.png", tc.hdr)
			switch w.Code {
			case http.StatusOK:
				if w.Body.String() != string(newer) || w.Header().Get("ETag") != newETag {
					t.Errorf("200 = %q %v", w.Body.String(), w.Header())
				}
			case http.StatusPartialContent:
				if w.Header().Get("ETag") != newETag && w.Header().Get("ETag") != tc.hdr["If-Range"] ||
					w.Header().Get("ETag") == newETag && w.Body.String() != string(newer[8:12]) ||
					w.Header().Get("ETag") == oldETag && w.Body.String() != string(older[8:12]) {
					t.Errorf("206 = %q %v", w.Body.String(), w.Header())
				}
				if tc.name == "if-range" && w.Header().Get("ETag") != oldETag {
					t.Errorf("206 under If-Range with ETag %s", w.Header().Get("ETag"))
				}
			default:
				t.Errorf("status = %d", w.Code)
			}
			if tc.name == "no if-range" && w.Code != http.StatusOK {
				t.Errorf("a ranged request during a replace = %d, want a coherent 200", w.Code)
			}
		})
	}
}

// A body that dies in the first bytes is a store failure (502), not a file
// served as an octet-stream download.
func TestServeMediaObjectStorageFirstBytesReadErrorIs502(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rng   string
		abort func(r *http.Request) bool
	}{
		{"whole object", "", func(*http.Request) bool { return true }},
		{"sniff request of a range", "bytes=5-9", func(r *http.Request) bool { return strings.HasPrefix(r.Header.Get("Range"), "bytes=0-") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			png := "\x89PNG\r\n\x1a\nsome bytes"
			front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.Header().Set("Content-Length", "100000")
					return
				}
				if !tc.abort(r) {
					w.Header().Set("Content-Range", "bytes 5-9/100000")
					w.Header().Set("Content-Length", "5")
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write([]byte(png[5:10]))
					return
				}
				w.Header().Set("Content-Length", "100000")
				_, _ = w.Write([]byte(png))
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler)
			}))
			defer front.Close()
			hdr := map[string]string{}
			if tc.rng != "" {
				hdr["Range"] = tc.rng
			}
			w := getMedia(serveFrontedByS3(t, front.URL, ""), "p.png", hdr)
			if w.Code != http.StatusBadGateway || w.Header().Get("Content-Disposition") != "" {
				t.Errorf("status = %d, headers %v", w.Code, w.Header())
			}
		})
	}
}

// The whole-object refetch after a replacement found while sniffing waits for
// the store under the same bound as every other call.
func TestServeMediaObjectStorageSniffRefetchIsBounded(t *testing.T) {
	older := []byte("\x89PNG\r\n\x1a\n0123456789abcdef0123456789abcdef")
	newer := []byte("\x89PNG\r\n\x1a\nNEWNEWNEWNEWNEWNEW-and-longer-than-the-old-one")
	fake := s3test.NewServer(t)
	target, _ := url.Parse(fake.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	hang := make(chan struct{})
	var gets atomic.Int32
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			switch gets.Add(1) {
			case 2:
				fake.Put("site/p.png", newer, nil) // replaced between the range and the sniff fetch
			case 3:
				<-hang
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer front.Close()
	defer close(hang)
	fake.Put("site/p.png", older, nil)
	old := mediaStoreTimeout
	mediaStoreTimeout = 300 * time.Millisecond
	defer func() { mediaStoreTimeout = old }()
	start := time.Now()
	w := getMedia(serveFrontedByS3(t, front.URL, "site/"), "p.png", map[string]string{"Range": "bytes=20-25"})
	if w.Code != http.StatusBadGateway || time.Since(start) > 10*time.Second {
		t.Errorf("hung refetch = %d after %v", w.Code, time.Since(start))
	}
}
