//go:build hatamoto_qa

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fixture page echoes the form query parameter, so it must be escaped.
func TestQAOpaquePageEscapesForm(t *testing.T) {
	s := &Server{renderHandlers: &renderHandlers{}}
	h := s.qaWrap(http.NotFoundHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, `/__qa/opaque?form="><script>alert(1)</script>`, nil))
	if strings.Contains(rec.Body.String(), "<script>alert(1)") {
		t.Fatalf("form parameter reflected unescaped: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "&lt;script&gt;alert(1)") {
		t.Fatalf("escaped form parameter missing: %s", rec.Body)
	}
}
