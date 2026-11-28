package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var publicContentPaths = []string{"/content/post", "/content/post/hello", "/terms/tag"}

func publicContentDo(h http.Handler, method, path, remote string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if remote != "" {
		req.RemoteAddr = remote
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// With the switch off (the default) the three routes answer like a route that
// does not exist, under both public mounts, and never carry a CORS header.
func TestPublicContentAPIOffAnswersNotFound(t *testing.T) {
	h, _, _ := prefixServer(t, "/x/v1")
	for _, base := range []string{"/api/v1/public", "/x/v1"} {
		for _, p := range publicContentPaths {
			for _, method := range []string{"GET", "HEAD"} {
				rec := publicContentDo(h, method, base+p, "", "Origin", "https://example.com")
				if rec.Code != 404 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Errorf("%s %s%s: %d acao=%q", method, base, p, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
				}
				if method == "GET" && strings.TrimSpace(rec.Body.String()) != `{"error":"not found"}` {
					t.Errorf("GET %s%s body = %q", base, p, rec.Body)
				}
			}
		}
	}
}

// There is no write route and no preflight: another method is a 404 or 405.
func TestPublicContentAPIReadOnly(t *testing.T) {
	h, _, _ := prefixServer(t, "/x/v1")
	for _, p := range publicContentPaths {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			rec := publicContentDo(h, method, "/api/v1/public"+p, "", "Origin", "https://example.com", "Access-Control-Request-Method", "GET")
			if (rec.Code != 404 && rec.Code != 405) || rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s %s: %d acao=%q", method, p, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
			}
		}
	}
}

// One client gets a burst of 30 and then 429; another client is unaffected.
func TestPublicContentAPIRateLimit(t *testing.T) {
	h, _, _ := prefixServer(t, "")
	const path = "/api/v1/public/content/post"
	for i := 1; i <= 30; i++ {
		if rec := publicContentDo(h, "GET", path, "192.0.2.10:4000"); rec.Code != 404 {
			t.Fatalf("request %d = %d, want 404", i, rec.Code)
		}
	}
	rec := publicContentDo(h, "GET", path, "192.0.2.10:4000")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("request 31 = %d retry-after=%q, want 429", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := publicContentDo(h, "GET", path, "192.0.2.11:4000"); rec.Code != 404 {
		t.Errorf("other client = %d, want 404", rec.Code)
	}
}

func TestPublicContentGate(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
	for _, on := range []bool{true, false} {
		s := &Server{logger: slog.Default(), publicContentAPIOn: func(context.Context) bool { return on }}
		reached = false
		rec := httptest.NewRecorder()
		s.publicContentGate(next).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
		switch {
		case on && (!reached || rec.Code != 200):
			t.Errorf("on: reached=%v code=%d", reached, rec.Code)
		case !on && (reached || rec.Code != 404 || strings.TrimSpace(rec.Body.String()) != `{"error":"not found"}`):
			t.Errorf("off: reached=%v code=%d body=%q", reached, rec.Code, rec.Body)
		}
	}
	if (&Server{}).publicContentAPIEnabled(context.Background()) {
		t.Error("a server without site config must read as off")
	}
}

// A huge page would wrap (page-1)*perPage into a negative SQL offset and answer
// 500; it is refused with 400 before any query runs.
func TestPublicPagingRefusesHugePage(t *testing.T) {
	for _, q := range []string{"page=9223372036854775807&per_page=100", "page=100001"} {
		rec := httptest.NewRecorder()
		_, _, ok := publicPaging(rec, httptest.NewRequest("GET", "/content/post?"+q, nil))
		if ok || rec.Code != 400 {
			t.Errorf("?%s: ok=%v code=%d, want 400", q, ok, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	if page, _, ok := publicPaging(rec, httptest.NewRequest("GET", "/x?page=100000", nil)); !ok || page != 100000 {
		t.Errorf("page=100000: page=%d ok=%v", page, ok)
	}
}
