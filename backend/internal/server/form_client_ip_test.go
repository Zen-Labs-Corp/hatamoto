package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/middleware"
)

func submitFrom(t *testing.T, remoteAddr, xff string) string {
	t.Helper()
	srv, repo := newFormPostServer(t)
	req := httptest.NewRequest(http.MethodPost, "/forms/contact/submit", strings.NewReader(url.Values{"name": {"Ada"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	req.Header.Set("X-Forwarded-For", xff)
	req.SetPathValue("slug", "contact")
	srv.handlePublicFormPost(httptest.NewRecorder(), req)
	if len(repo.subs) != 1 {
		t.Fatalf("submissions = %d", len(repo.subs))
	}
	return repo.subs[0].Metadata.IPAddress
}

// The stored submitter IP must come from the shared trusted-proxy helper, not
// the attacker-controlled first X-Forwarded-For entry.
func TestFormSubmission_StoredIPIgnoresForgedForwardedFor(t *testing.T) {
	middleware.SetTrustedProxies(nil)
	if got := submitFrom(t, "198.51.100.5:4000", "1.2.3.4, 9.9.9.9"); got != "198.51.100.5" {
		t.Errorf("untrusted peer: stored %q, want the peer address without a port", got)
	}
	if got := submitFrom(t, "127.0.0.1:4000", "1.2.3.4, 9.9.9.9"); got != "9.9.9.9" {
		t.Errorf("trusted peer: stored %q, want the rightmost untrusted hop", got)
	}
}

// Behind a trusted proxy every visitor gets their own limiter bucket.
func TestRateLimiter_KeysPerVisitorBehindTrustedProxy(t *testing.T) {
	middleware.SetTrustedProxies(nil)
	rl := middleware.NewRateLimiter(0.001, 1)
	t.Cleanup(rl.Stop)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	hit := func(xff string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if hit("203.0.113.1") != http.StatusOK || hit("203.0.113.2") != http.StatusOK {
		t.Fatal("two visitors behind one proxy must not share a bucket")
	}
	if hit("203.0.113.1") != http.StatusTooManyRequests {
		t.Error("the same visitor must be limited")
	}
}
