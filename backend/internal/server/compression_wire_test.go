package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func compressedGet(h http.Handler, path, ae string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if ae != "" {
		r.Header.Set("Accept-Encoding", ae)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func unzstd(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// The compression middleware is in the global chain: a page answers zstd on
// request, plain when the switch is off or the client does not ask.
func TestCompressionWired(t *testing.T) {
	h, s := wiredHandler(t, false, tablePrefix)
	plain := compressedGet(h, "/", "")
	if plain.Code != 200 || plain.Header().Get("Content-Encoding") != "" || plain.Body.Len() < 1024 {
		t.Fatalf("fixture page must be a plain 200 over the threshold: %d %d", plain.Code, plain.Body.Len())
	}

	// Test shells start with the switch off.
	if rec := compressedGet(h, "/", "zstd, gzip"); rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Vary") != "" {
		t.Fatalf("switch off must leave the response alone: %v", rec.Header())
	}

	s.compressOn.Store(true)
	rec := compressedGet(h, "/", "zstd, gzip")
	if rec.Header().Get("Content-Encoding") != "zstd" || !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("want zstd + Vary, got %v", rec.Header())
	}
	if unzstd(t, rec.Body.Bytes()) != plain.Body.String() {
		t.Fatal("decoded body differs from the plain page")
	}
	if got := compressedGet(h, "/", "gzip").Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("gzip client got %q", got)
	}
	if got := compressedGet(h, "/", "").Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("no Accept-Encoding got %q", got)
	}
}

// The admin address stays indistinguishable from any other 404 with
// compression on, for clients that accept it and for those that do not.
func TestCompressionKeepsNotFoundIdentical(t *testing.T) {
	h, s := wiredHandler(t, false, tablePrefix)
	s.compressOn.Store(true)
	for _, ae := range []string{"", "zstd, gzip", "gzip"} {
		probe := compressedGet(h, "/wp-admin/setup-config.php", ae)
		random := compressedGet(h, "/zz-no-such-page-7", ae)
		if probe.Code != 404 || random.Code != 404 {
			t.Fatalf("AE %q: codes %d %d", ae, probe.Code, random.Code)
		}
		for _, k := range []string{"Content-Encoding", "Vary", "Content-Type", "Content-Length"} {
			if probe.Header().Get(k) != random.Header().Get(k) {
				t.Errorf("AE %q: %s differs: %q vs %q", ae, k, probe.Header().Get(k), random.Header().Get(k))
			}
		}
	}
}

// Compression sits outside the demo banner, so the banner is injected into
// plain bytes and then compressed with the page.
func TestCompressionDemoBannerSurvives(t *testing.T) {
	h, s := wiredHandler(t, true, "/admin")
	s.compressOn.Store(true)
	rec := compressedGet(h, "/", "zstd")
	if rec.Header().Get("Content-Encoding") != "zstd" {
		t.Fatalf("demo page not compressed: %v", rec.Header())
	}
	if !strings.Contains(unzstd(t, rec.Body.Bytes()), demoBannerHTML) {
		t.Fatal("demo banner missing from the compressed page")
	}
}
