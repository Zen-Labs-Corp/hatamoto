package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// BenchmarkPublic404: a public 404 through the full handler chain (real
// engine and middleware, stub chrome). Repeated paths are the common bot and
// stale-link case; unique paths are the worst case for any reuse.
func BenchmarkPublic404(b *testing.B) {
	full, _ := bootThemed404(b)
	for _, tc := range []struct{ name, path string }{
		{"missing_slug", "/no-such-page-q7x2"},
		{"probe_path", "/wp-login.php"},
	} {
		for _, gz := range []bool{false, true} {
			name := tc.name
			if gz {
				name += "_gzip"
			}
			b.Run(name, func(b *testing.B) {
				req := httptest.NewRequest(http.MethodGet, tc.path, nil)
				if gz {
					req.Header.Set("Accept-Encoding", "gzip")
				}
				check404(b, full, req, gz)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					full.ServeHTTP(httptest.NewRecorder(), req)
				}
				b.StopTimer()
				check404(b, full, req, gz) // still a themed 404, not a throttle answer
			})
		}
	}
	b.Run("unique_path", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			full.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing-"+strconv.Itoa(i), nil))
		}
	})
}

func check404(b *testing.B, h http.Handler, req *http.Request, gz bool) {
	b.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		b.Fatalf("status %d, want 404", rec.Code)
	}
	if gz {
		if rec.Header().Get("Content-Encoding") != "gzip" {
			b.Logf("compression is off in this shell: Content-Encoding %q", rec.Header().Get("Content-Encoding"))
		}
		return
	}
	if !strings.Contains(rec.Body.String(), "<html") {
		b.Fatalf("not a themed 404: %.200s", rec.Body.String())
	}
}
