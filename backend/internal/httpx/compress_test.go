package httpx

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

var bigBody = strings.Repeat("<p>compress me please</p>\n", 100) // ~2.6 KB

func serve(t *testing.T, enabled bool, h http.HandlerFunc, method, ae string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/", nil)
	if ae != "" {
		r.Header.Set("Accept-Encoding", ae)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	Compress(func() bool { return enabled })(h).ServeHTTP(rec, r)
	return rec
}

func htmlHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	b := rec.Body.Bytes()
	switch rec.Header().Get("Content-Encoding") {
	case "zstd":
		zr, _ := zstd.NewReader(bytes.NewReader(b))
		out, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(gr)
		return string(out)
	}
	return string(b)
}

func TestCompressNegotiated(t *testing.T) {
	cases := []struct{ ae, want string }{
		{"zstd, gzip", "zstd"},
		{"gzip", "gzip"},
		{"gzip, zstd;q=0", "gzip"},
		{"identity", ""},
		{"", ""},
	}
	for _, c := range cases {
		rec := serve(t, true, htmlHandler(bigBody), "GET", c.ae)
		if got := rec.Header().Get("Content-Encoding"); got != c.want {
			t.Errorf("AE %q: Content-Encoding %q, want %q", c.ae, got, c.want)
		}
		if rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Errorf("AE %q: Vary %q", c.ae, rec.Header().Get("Vary"))
		}
		if decode(t, rec) != bigBody {
			t.Errorf("AE %q: body mismatch", c.ae)
		}
		if c.want != "" && rec.Header().Get("Content-Length") != "" {
			t.Errorf("AE %q: Content-Length must be dropped", c.ae)
		}
	}
}

func TestCompressSkips(t *testing.T) {
	write := func(ct, body string, status int, extra ...string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			for i := 0; i+1 < len(extra); i += 2 {
				w.Header().Set(extra[i], extra[i+1])
			}
			if status != 0 {
				w.WriteHeader(status)
			}
			_, _ = io.WriteString(w, body)
		}
	}
	cases := []struct {
		name   string
		h      http.HandlerFunc
		method string
		hdr    []string
		vary   bool
	}{
		{"image", write("image/png", bigBody, 0), "GET", nil, false},
		{"video", write("video/mp4", bigBody, 0), "GET", nil, false},
		{"font", write("font/woff2", bigBody, 0), "GET", nil, false},
		{"event stream", write("text/event-stream", bigBody, 0), "GET", nil, false},
		{"already encoded", write("text/html", bigBody, 0, "Content-Encoding", "br"), "GET", nil, true},
		{"range request", htmlHandler(bigBody), "GET", []string{"Range", "bytes=0-9"}, false},
		{"partial", write("text/html", bigBody, http.StatusPartialContent, "Content-Range", "bytes 0-9/100"), "GET", nil, false},
		{"small body", htmlHandler("tiny"), "GET", nil, true},
		{"not modified", write("text/html", "", http.StatusNotModified), "GET", nil, false},
		{"no content", write("", "", http.StatusNoContent), "GET", nil, false},
	}
	for _, c := range cases {
		rec := serve(t, true, c.h, c.method, "zstd, gzip", c.hdr...)
		got := rec.Header().Get("Content-Encoding")
		if c.name == "already encoded" {
			if got != "br" {
				t.Errorf("%s: Content-Encoding %q changed", c.name, got)
			}
		} else if got != "" {
			t.Errorf("%s: compressed (%q)", c.name, got)
		}
		if has := rec.Header().Get("Vary") != ""; has != c.vary {
			t.Errorf("%s: Vary present=%v, want %v", c.name, has, c.vary)
		}
	}
	if rec := serve(t, false, htmlHandler(bigBody), "GET", "zstd"); rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Vary") != "" {
		t.Error("disabled middleware must not touch the response")
	}
	if rec := serve(t, true, write("text/html", bigBody, http.StatusOK, "Content-Length", "10"), "GET", "zstd"); rec.Header().Get("Content-Encoding") != "" {
		t.Error("declared small Content-Length stays plain")
	}
}

func TestCompressHead(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", "5000")
		w.WriteHeader(http.StatusOK)
	}
	rec := serve(t, true, h, "HEAD", "zstd")
	if rec.Header().Get("Content-Encoding") != "zstd" || rec.Header().Get("Content-Length") != "" || rec.Body.Len() != 0 {
		t.Errorf("HEAD headers: %v", rec.Header())
	}
}

func TestCompressVaryMerge(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Vary", "Cookie")
		_, _ = io.WriteString(w, bigBody)
	}
	rec := serve(t, true, h, "GET", "gzip")
	if got := strings.Join(rec.Header().Values("Vary"), ", "); got != "Cookie, Accept-Encoding" {
		t.Errorf("Vary = %q", got)
	}
	h2 := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Vary", "accept-encoding")
		_, _ = io.WriteString(w, bigBody)
	}
	if got := serve(t, true, h2, "GET", "gzip").Header().Values("Vary"); len(got) != 1 {
		t.Errorf("duplicate Vary: %v", got)
	}
}

func TestCompressSniffsMissingContentType(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "<html>"+bigBody) }
	rec := serve(t, true, h, "GET", "gzip")
	if rec.Header().Get("Content-Encoding") != "gzip" || decode(t, rec) != "<html>"+bigBody {
		t.Errorf("sniffed html not compressed: %v", rec.Header())
	}
}

func TestCompressMultipleSmallWrites(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for range 300 {
			_, _ = io.WriteString(w, `{"a":"bcdefg"},`)
		}
	}
	rec := serve(t, true, h, "GET", "zstd")
	if rec.Header().Get("Content-Encoding") != "zstd" || decode(t, rec) != strings.Repeat(`{"a":"bcdefg"},`, 300) {
		t.Error("chunked writes must compress once the threshold is crossed")
	}
}

func TestCompressFlushBeforeThresholdStreamsPlain(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, "a,b\n")
		http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, bigBody)
	}
	rec := serve(t, true, h, "GET", "zstd")
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "a,b\n"+bigBody {
		t.Error("a flushed stream must stay plain")
	}
	if !rec.Flushed {
		t.Error("flush must reach the client")
	}
}

func TestCompressFlushAfterThreshold(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, bigBody)
		http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, "tail")
	}
	rec := serve(t, true, h, "GET", "gzip")
	if rec.Header().Get("Content-Encoding") != "gzip" || decode(t, rec) != bigBody+"tail" || !rec.Flushed {
		t.Errorf("flush after threshold: %v flushed=%v", rec.Header(), rec.Flushed)
	}
}

// A real server: the client sees a streamed chunk before the handler returns.
func TestCompressStreamReachesClientBeforeEnd(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(Compress(func() bool { return true })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: 1\n\n")
		http.NewResponseController(w).Flush()
		<-release
	})))
	defer srv.Close()
	defer close(release)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 9)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "data: 1\n\n" {
		t.Fatalf("stream chunk: %q %v", buf, err)
	}
}

func TestCompressETagAndConditional(t *testing.T) {
	content := []byte(bigBody)
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("ETag", `"abc"`)
		http.ServeContent(w, r, "a.css", time.Time{}, bytes.NewReader(content))
	}
	for _, ae := range []string{"zstd", "gzip", ""} {
		rec := serve(t, true, h, "GET", ae)
		et := rec.Header().Get("ETag")
		want := `W/"abc"`
		if ae == "" {
			want = `"abc"`
		}
		if rec.Code != 200 || et != want {
			t.Fatalf("AE %q: code %d etag %q", ae, rec.Code, et)
		}
		if ae != "" && rec.Header().Get("Accept-Ranges") != "" {
			t.Errorf("AE %q: Accept-Ranges must be dropped", ae)
		}
		rec2 := serve(t, true, h, "GET", ae, "If-None-Match", et)
		if rec2.Code != http.StatusNotModified || rec2.Body.Len() != 0 {
			t.Errorf("AE %q: If-None-Match %q gave %d", ae, et, rec2.Code)
		}
	}
}
