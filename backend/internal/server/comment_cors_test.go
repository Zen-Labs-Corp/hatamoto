package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/statichost"
)

// commentCORSRouter mounts the real render routes with the given hosting snapshot.
func commentCORSRouter(t *testing.T, hs statichost.StaticHosting) http.Handler {
	t.Helper()
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	var holder statichost.Holder
	holder.Store(hs)
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
		commentPost: stub("post"), commentEdit: stub("edit"), commentDelete: stub("delete"),
		hosting: &holder,
	})
	return h
}

func commentCORSDo(h http.Handler, method, path, origin string, acrh bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if acrh {
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "hx-request, hx-current-url, hx-target, hx-trigger")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCommentPostCORS(t *testing.T) {
	t.Parallel()
	static := statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite}
	path := "/-/comments/" + uuid.NewString()

	t.Run("site preflight", func(t *testing.T) {
		rec := commentCORSDo(commentCORSRouter(t, static), http.MethodOptions, path, testStaticSite, true)
		if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite {
			t.Fatalf("%d ACAO=%q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "POST, OPTIONS" {
			t.Errorf("Allow-Methods = %q", got)
		}
		allowed := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers"))
		for _, name := range []string{"hx-request", "hx-current-url", "hx-target", "hx-trigger"} {
			if !strings.Contains(allowed, name) {
				t.Errorf("Allow-Headers %q lacks %s", allowed, name)
			}
		}
		assertNoCredentials(t, rec)
	})

	for _, origin := range []string{"null", "https://evil.example", testStaticLive} {
		t.Run("preflight from "+origin, func(t *testing.T) {
			rec := commentCORSDo(commentCORSRouter(t, static), http.MethodOptions, path, origin, true)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d", rec.Code)
			}
			for _, k := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
				if v := rec.Header().Get(k); v != "" {
					t.Errorf("%s = %q", k, v)
				}
			}
			if rec.Header().Get("Vary") != "Origin" {
				t.Errorf("Vary = %q", rec.Header().Get("Vary"))
			}
			assertNoCredentials(t, rec)
		})
	}

	t.Run("served mode preflight", func(t *testing.T) {
		rec := commentCORSDo(commentCORSRouter(t, statichost.StaticHosting{}), http.MethodOptions, path, testStaticSite, true)
		if rec.Code != http.StatusNoContent || len(rec.Header()) != 0 {
			t.Errorf("%d headers %v, want a bare 204", rec.Code, rec.Header())
		}
	})

	t.Run("post from site", func(t *testing.T) {
		rec := commentCORSDo(commentCORSRouter(t, static), http.MethodPost, path, testStaticSite, false)
		if rec.Body.String() != "post" || rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite {
			t.Errorf("body %q ACAO=%q", rec.Body.String(), rec.Header().Get("Access-Control-Allow-Origin"))
		}
		assertNoCredentials(t, rec)
	})

	for _, origin := range []string{"null", "https://evil.example"} {
		t.Run("post from "+origin, func(t *testing.T) {
			rec := commentCORSDo(commentCORSRouter(t, static), http.MethodPost, path, origin, false)
			if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
				t.Errorf("ACAO = %q", v)
			}
			assertNoCredentials(t, rec)
		})
	}

	t.Run("edit and delete have no preflight", func(t *testing.T) {
		for _, p := range []string{"/-/comments/edit/" + uuid.NewString(), "/-/comments/delete/" + uuid.NewString()} {
			rec := commentCORSDo(commentCORSRouter(t, static), http.MethodOptions, p, testStaticSite, true)
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("OPTIONS %s = %d", p, rec.Code)
			}
			if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
				t.Errorf("OPTIONS %s ACAO = %q", p, v)
			}
		}
	})
}
