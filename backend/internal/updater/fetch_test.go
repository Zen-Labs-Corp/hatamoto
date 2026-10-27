package updater

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is an httptest server that remembers every request it saw.
type recorder struct {
	*httptest.Server
	mu   sync.Mutex
	seen []*http.Request
}

func newRecorder(t *testing.T, h http.HandlerFunc) *recorder {
	t.Helper()
	r := &recorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.seen = append(r.seen, req.Clone(context.Background()))
		r.mu.Unlock()
		h(w, req)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *recorder) requests() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seen)
}

func okBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }
}

func testFetcher(t *testing.T, base string) *Fetcher {
	t.Helper()
	f, err := newFetcher(base+"/releases", true)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestManifestIsOneAnonymousGet(t *testing.T) {
	srv := newRecorder(t, okBody(`{"channel":"stable"}`))
	f := testFetcher(t, srv.URL)
	b, err := f.Manifest(context.Background(), "stable")
	if err != nil || string(b) != `{"channel":"stable"}` {
		t.Fatalf("%q, %v", b, err)
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want exactly 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.URL.Path != "/releases/channels/stable.json" || r.URL.RawQuery != "" || r.URL.Fragment != "" {
		t.Errorf("request %s %s", r.Method, r.URL)
	}
	allowed := []string{"User-Agent", "Accept-Encoding"}
	for k := range r.Header {
		if !slices.Contains(allowed, k) {
			t.Errorf("unexpected header %s: %v", k, r.Header[k])
		}
	}
	if r.Header.Get("Cookie") != "" || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		t.Errorf("cookie or body sent: %v", r.Header)
	}
	if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "Go-http-client/") {
		t.Errorf("User-Agent %q", ua)
	}
}

func TestManifestCanaryPathAndUnknownChannel(t *testing.T) {
	srv := newRecorder(t, okBody(`{"channel":"canary"}`))
	f := testFetcher(t, srv.URL)
	if _, err := f.Manifest(context.Background(), "canary"); err != nil {
		t.Fatal(err)
	}
	if reqs := srv.requests(); len(reqs) != 1 || reqs[0].URL.Path != "/releases/channels/canary.json" {
		t.Fatalf("requests %v", reqs)
	}
	if _, err := f.Manifest(context.Background(), "nightly"); err == nil {
		t.Error("nightly accepted")
	}
	if len(srv.requests()) != 1 {
		t.Error("nightly must not reach the network")
	}
}

func TestRecordAndDownloadRequestsAreAnonymousToo(t *testing.T) {
	srv := newRecorder(t, okBody("payload"))
	f := testFetcher(t, srv.URL)
	rec, sig, err := f.Record(context.Background(), "v1.0.1")
	if err != nil || string(rec) != "payload" || string(sig) != "payload" {
		t.Fatalf("%q %q %v", rec, sig, err)
	}
	dst := filepath.Join(t.TempDir(), "bin")
	if err := f.Download(context.Background(), "v1.0.1", "linux-amd64", dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "payload" {
		t.Fatalf("file %q", b)
	}
	var paths []string
	for _, r := range srv.requests() {
		paths = append(paths, r.URL.Path)
		if r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || len(r.Header) > 2 {
			t.Errorf("%s: query %q headers %v", r.URL.Path, r.URL.RawQuery, r.Header)
		}
	}
	want := []string{"/releases/v1.0.1/release.json", "/releases/v1.0.1/release.json.sig", "/releases/v1.0.1/hatamoto-linux-amd64"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths %v, want %v", paths, want)
	}
}

func TestFetcherRefusesBadArguments(t *testing.T) {
	srv := newRecorder(t, okBody("x"))
	f := testFetcher(t, srv.URL)
	ctx := context.Background()
	if _, err := f.Manifest(ctx, "../etc"); err == nil {
		t.Error("bad channel accepted")
	}
	if _, _, err := f.Record(ctx, "v1/../../x"); err == nil {
		t.Error("bad version accepted")
	}
	if err := f.Download(ctx, "v1.0.1", "plan9-386", filepath.Join(t.TempDir(), "x"), 10); err == nil {
		t.Error("bad platform accepted")
	}
	if n := len(srv.requests()); n != 0 {
		t.Errorf("%d requests made for refused arguments", n)
	}
}

func TestNon200IsAnError(t *testing.T) {
	srv := newRecorder(t, func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	f := testFetcher(t, srv.URL)
	if _, err := f.Manifest(context.Background(), "stable"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("%v", err)
	}
	dst := filepath.Join(t.TempDir(), "bin")
	if err := f.Download(context.Background(), "v1.0.1", "linux-amd64", dst, 10); err == nil {
		t.Fatal("404 download succeeded")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("file left behind")
	}
}

func TestCrossHostRedirectIsRefusedWithoutAFollowUpRequest(t *testing.T) {
	other := newRecorder(t, okBody("elsewhere"))
	srv := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	})
	f := testFetcher(t, srv.URL)
	if _, err := f.Manifest(context.Background(), "stable"); err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("err = %v", err)
	}
	if n := len(other.requests()); n != 0 {
		t.Fatalf("%d requests reached the other host", n)
	}
	if err := f.Download(context.Background(), "v1.0.1", "linux-amd64", filepath.Join(t.TempDir(), "b"), 10); err == nil {
		t.Fatal("artifact redirect followed")
	}
}

func TestSameHostRedirectIsFollowed(t *testing.T) {
	srv := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/channels/stable.json" {
			http.Redirect(w, r, "/releases/channels/moved.json", http.StatusFound)
			return
		}
		w.Write([]byte("moved"))
	})
	f := testFetcher(t, srv.URL)
	if b, err := f.Manifest(context.Background(), "stable"); err != nil || string(b) != "moved" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestRedirectGuardRefusesSchemeDowngrade(t *testing.T) {
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	guard := redirectGuard(nil)
	if err := guard(req("http://example.test/x"), []*http.Request{req("https://example.test/y")}); err == nil || !strings.Contains(err.Error(), "https to http") {
		t.Errorf("downgrade: %v", err)
	}
	if err := guard(req("https://example.test/x"), []*http.Request{req("https://example.test/y")}); err != nil {
		t.Errorf("same host https: %v", err)
	}
	if err := guard(req("https://evil.test/x"), []*http.Request{req("https://example.test/y")}); err == nil {
		t.Error("cross host allowed")
	}
	if err := guard(req("https://example.test:8443/x"), []*http.Request{req("https://example.test/y")}); err == nil {
		t.Error("other port allowed")
	}
}

func TestDownloadRejectsOversize(t *testing.T) {
	big := strings.Repeat("a", 1000)
	for name, h := range map[string]http.HandlerFunc{
		"announced": okBody(big),
		"streamed": func(w http.ResponseWriter, _ *http.Request) {
			for i := 0; i < 10; i++ { // chunked: no Content-Length
				w.Write([]byte(big[:100]))
				w.(http.Flusher).Flush()
			}
		},
	} {
		srv := newRecorder(t, h)
		f := testFetcher(t, srv.URL)
		dst := filepath.Join(t.TempDir(), "bin")
		err := f.Download(context.Background(), "v1.0.1", "linux-amd64", dst, 500)
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: err = %v", name, err)
		}
		if _, err := os.Stat(dst); err == nil {
			t.Errorf("%s: file left behind", name)
		}
	}
	// Exactly the cap is fine.
	srv := newRecorder(t, okBody(big))
	if err := testFetcher(t, srv.URL).Download(context.Background(), "v1.0.1", "linux-amd64", filepath.Join(t.TempDir(), "b"), 1000); err != nil {
		t.Errorf("at the cap: %v", err)
	}
}

func TestDownloadStallAborts(t *testing.T) {
	srv := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("start"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // then deliver nothing
	})
	f := testFetcher(t, srv.URL)
	f.stall = 150 * time.Millisecond
	dst := filepath.Join(t.TempDir(), "bin")
	begin := time.Now()
	err := f.Download(context.Background(), "v1.0.1", "linux-amd64", dst, 1<<20)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(begin) > 5*time.Second {
		t.Fatal("stall guard too slow")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("partial file left behind")
	}
}

func TestDownloadStallGuardResetsOnProgress(t *testing.T) {
	srv := newRecorder(t, func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < 5; i++ { // 5 x 100ms, longer than one stall window in total
			w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	f := testFetcher(t, srv.URL)
	f.stall = 400 * time.Millisecond
	if err := f.Download(context.Background(), "v1.0.1", "linux-amd64", filepath.Join(t.TempDir(), "b"), 100); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadIgnoresCallerDeadline(t *testing.T) {
	// The scheduler's job context carries a deadline; the download must not
	// inherit it (it has its own stall and total limits).
	srv := newRecorder(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("late"))
	})
	f := testFetcher(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := f.Download(ctx, "v1.0.1", "linux-amd64", filepath.Join(t.TempDir(), "b"), 100); err != nil {
		t.Fatal(err)
	}
}

func TestNewFetcherURLRules(t *testing.T) {
	for _, tc := range []struct {
		url      string
		loopback bool
		ok       bool
	}{
		{"", false, true},
		{"https://mirror.example/releases", false, true},
		{"http://mirror.example/releases", false, false},
		{"http://127.0.0.1:8080/releases", false, false},
		{"http://127.0.0.1:8080/releases", true, true},
		{"http://localhost:8080/releases", true, true},
		{"http://[::1]:8080/releases", true, true},
		{"http://10.0.0.5/releases", true, false},
		{"http://mirror.example/releases", true, false},
		{"ftp://mirror.example/releases", true, false},
		{"https://user:pw@mirror.example/releases", false, false},
		{"https://mirror.example/releases?x=1", false, false},
		{"https:///releases", false, false},
	} {
		_, err := newFetcher(tc.url, tc.loopback)
		if (err == nil) != tc.ok {
			t.Errorf("%q loopback=%v: err = %v", tc.url, tc.loopback, err)
		}
	}
}
