package analytics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The sandboxed-page pageview is a fetch with a string body, which the browser
// labels text/plain. Track reads the body whatever its Content-Type.
func TestTrackAcceptsTextPlainBody(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo)
	req := httptest.NewRequest(http.MethodPost, "/track", strings.NewReader(`{"events":[{"type":"page_view"}]}`))
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()
	NewHandlers(svc).Track(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
	if calls := flushed(t, svc, repo); len(calls) != 1 || calls[0].kind != KindPageView || calls[0].n != 1 {
		t.Fatalf("calls = %+v", calls)
	}
}
