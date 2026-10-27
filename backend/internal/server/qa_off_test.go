//go:build !hatamoto_qa

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The default (production) build has no QA seam: no forced sandbox and no
// fixture route.
func TestQASeamAbsentInDefaultBuild(t *testing.T) {
	t.Setenv("HATAMOTO_QA_SANDBOX_ALL", "1")
	if qaSandboxForced() {
		t.Fatal("qaSandboxForced is true in the default build")
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	var s Server
	h := s.qaWrap(next)
	for _, p := range []string{"/__qa/opaque", "/__qa/seen"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusTeapot {
			t.Errorf("%s answered %d in the default build, want the wrapped handler", p, rec.Code)
		}
	}
}
