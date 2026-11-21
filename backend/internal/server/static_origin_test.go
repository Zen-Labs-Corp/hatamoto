package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/spam"
)

const (
	staticSubmitJSON   = "/api/v1/public/forms/contact/submit"
	staticSubmitNative = "/forms/contact/submit"
	urlencodedForm     = "application/x-www-form-urlencoded"
	evilOrigin         = "https://evil.example"
)

func TestStaticOriginJSONFromSite(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	rec := corsDo(corsMux(srv), http.MethodPost, staticSubmitJSON, testStaticSite, "application/json",
		`{"name":"Ada","hm_ts":"`+tokenAgo(srv, 10*time.Second)+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite {
		t.Errorf("ACAO = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	assertNoCredentials(t, rec)
	if len(repo.subs) != 1 {
		t.Errorf("stored %d, want 1", len(repo.subs))
	}
}

func TestStaticOriginRefusesForeign(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	h := corsMux(srv)
	tok := tokenAgo(srv, 10*time.Second)

	rec := corsDo(h, http.MethodPost, staticSubmitJSON, evilOrigin, "application/json", `{"name":"Ada","hm_ts":"`+tok+`"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "origin not allowed") {
		t.Errorf("json: %d %s", rec.Code, rec.Body)
	}
	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("ACAO = %q", v)
	}
	rec = corsDo(h, http.MethodPost, staticSubmitNative, evilOrigin, urlencodedForm,
		url.Values{"name": {"Ada"}, "hm_ts": {tok}}.Encode())
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "form_error=1") {
		t.Errorf("native: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(repo.subs) != 0 {
		t.Errorf("stored %d, want 0", len(repo.subs))
	}
}

func TestStaticOriginSiteNeedsToken(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	h := corsMux(srv)

	for name, body := range map[string]string{
		"missing": `{"name":"Ada"}`,
		"expired": `{"name":"Ada","hm_ts":"` + tokenAgo(srv, 25*time.Hour) + `"}`,
	} {
		rec := corsDo(h, http.MethodPost, staticSubmitJSON, testStaticSite, "application/json", body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s token: %d", name, rec.Code)
		}
	}
	rec := corsDo(h, http.MethodPost, staticSubmitNative, testStaticSite, urlencodedForm, url.Values{"name": {"Ada"}}.Encode())
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "form_error=1") {
		t.Errorf("native: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(repo.subs) != 0 {
		t.Errorf("stored %d, want 0", len(repo.subs))
	}
}

func TestStaticOriginLimiterTripsForSite(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	mux := http.NewServeMux()
	limiter := middleware.NewRateLimiter(0.1, 5).ByIPv6Prefix()
	handleCORS(httpx.NewGroup(mux, "/api/v1/public"), &srv.hosting, http.MethodPost, "/forms/{slug}/submit",
		srv.handlePublicFormSubmit, limiter.Middleware)
	var last int
	for i := 0; i < 6; i++ {
		body := `{"name":"Ada","hm_ts":"` + tokenAgo(srv, 10*time.Second) + `"}`
		req := httptest.NewRequest(http.MethodPost, staticSubmitJSON, strings.NewReader(body))
		req.Header.Set("Origin", testStaticSite)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		last = rec.Code
		if i < 5 && last == http.StatusTooManyRequests {
			t.Fatalf("request %d limited early", i+1)
		}
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("6th status = %d, want 429", last)
	}
}

func TestStaticOriginSameOriginNeedsNoToken(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	rec := corsDo(corsMux(srv), http.MethodPost, staticSubmitJSON, "http://example.com", "application/json", `{"name":"Ada"}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d %s", rec.Code, rec.Body)
	}
}

func TestStaticOriginServedModeUnchanged(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, spam.Defaults(), nil)
	rec := corsDo(corsMux(srv), http.MethodPost, staticSubmitJSON, evilOrigin, "application/json", `{"name":"Ada"}`)
	if rec.Code != http.StatusCreated || len(repo.subs) != 1 {
		t.Errorf("status = %d, stored %d", rec.Code, len(repo.subs))
	}
}

func TestStaticOriginRefuseForeignMiddleware(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	stub := srv.refuseForeignOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	do := func(origin string) int {
		return corsDo(stub, http.MethodPost, "/api/v1/track", origin, "", "").Code
	}
	if got := do(evilOrigin); got != http.StatusNoContent {
		t.Errorf("served mode evil = %d, want 204", got)
	}
	setStaticForTest(srv)
	if got := do(evilOrigin); got != http.StatusForbidden {
		t.Errorf("static evil = %d, want 403", got)
	}
	for _, ok := range []string{"", "null", testStaticSite, testStaticLive, "http://example.com"} {
		if got := do(ok); got != http.StatusNoContent {
			t.Errorf("static origin %q = %d, want 204", ok, got)
		}
	}
}

func TestStaticOriginForeignComparesNormalised(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	stub := srv.refuseForeignOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	do := func(origin, host string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/track", nil)
		req.Host = host
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		stub.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := do(strings.ToUpper(testStaticSite), "x.test"); got != http.StatusNoContent {
		t.Errorf("upper-case S = %d, want 204", got)
	}
	if got := do("https://Blog.Example.org", "blog.example.org"); got != http.StatusNoContent {
		t.Errorf("own host, other case = %d, want 204", got)
	}
	if got := do("https://blog.example.org", "Blog.Example.org:443"); got != http.StatusNoContent {
		t.Errorf("own host with default port = %d, want 204", got)
	}
	if got := do("https://evil.example", "x.test"); got != http.StatusForbidden {
		t.Errorf("foreign = %d, want 403", got)
	}
	if got := do("https://a\"b", "a\"b"); got != http.StatusForbidden {
		t.Errorf("malformed origin = %d, want 403", got)
	}
}
