package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/spam"
)

// prefixServer boots the real route mounts with fake form collaborators.
func prefixServer(t *testing.T, prefix string) (http.Handler, *Server, *fakeFormRepo) {
	t.Helper()
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	fs, repo := newFormPostServer(t)
	s.formService = fs.formService
	s.spam = &spam.Pipeline{Settings: fixedSettings{spam.Defaults()}, TimingKey: spam.TimingKey("test-secret"),
		Now: func() time.Time { return spamNow }}
	s.cfg.PublicSite.APIPrefix = prefix
	s.hooks = hooks.New(s.logger)
	s.setupRoutes()
	return s.buildHandler(), s, repo
}

func prefixServe(h http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPublicAPIPrefixMounts(t *testing.T) {
	h, s, repo := prefixServer(t, "/x/v1")
	for _, base := range []string{"/x/v1", "/api/v1/public"} {
		if rec := prefixServe(h, "GET", base+"/forms/contact/token", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"token"`) {
			t.Errorf("%s token: %d %s", base, rec.Code, rec.Body)
		}
		before := len(repo.subs)
		if rec := prefixServe(h, "POST", base+"/forms/contact/submit", `{"name":"Ada"}`, "Content-Type", "application/json"); rec.Code != http.StatusCreated || len(repo.subs) != before+1 {
			t.Errorf("%s submit: %d %s", base, rec.Code, rec.Body)
		}
	}
	// /track is not under /public: the default is /api/v1/track, the extra is <prefix>/track.
	// An empty body is rejected by the handler itself, which proves it was reached.
	for _, path := range []string{"/x/v1/track", "/api/v1/track"} {
		if rec := prefixServe(h, "POST", path, ""); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_payload") {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	// Opaque origins can use the extra mount too.
	if rec := prefixServe(h, "OPTIONS", "/x/v1/forms/contact/submit", "", "Origin", "null"); rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "null" {
		t.Errorf("preflight: %d %v", rec.Code, rec.Header())
	}
	// Unknown paths under the prefix are the JSON 404, and a wrong method is a 405 with Allow.
	if rec := prefixServe(h, "GET", "/x/v1/nope", ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("/x/v1/nope: %d %s", rec.Code, rec.Body)
	}
	if rec := prefixServe(h, "PUT", "/x/v1/forms/contact/submit", ""); rec.Code != 405 || !strings.Contains(rec.Header().Get("Allow"), "POST") {
		t.Errorf("PUT submit: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	if !s.isCatchAll("GET /x/v1/{rest...}") || s.isCatchAll("POST /x/v1/forms/{slug}/submit") {
		t.Error("isCatchAll misclassifies the prefix mount")
	}
}

// Without the setting nothing is mounted at the extra path, and the table is
// the default one.
func TestPublicAPIPrefixOffByDefault(t *testing.T) {
	_, s, _ := prefixServer(t, "")
	for _, r := range s.routes.Routes() {
		if strings.Contains(r.Pattern, "/x/v1") {
			t.Errorf("unexpected route %s", r.Pattern)
		}
	}
	with, s2, _ := prefixServer(t, "/x/v1")
	_ = with
	if got, base := len(s2.routes.Routes()), len(s.routes.Routes()); got <= base {
		t.Errorf("prefix added no routes: %d vs %d", got, base)
	}
}
