package server

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/statichost"
)

func TestCommentFormLiveRoutesInTable(t *testing.T) {
	t.Parallel()
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for _, rt := range table {
		patterns = append(patterns, rt.Pattern)
	}
	for _, want := range []string{"GET /-/comments/form/{entityID}", "OPTIONS /-/comments/form/{entityID}"} {
		if !slices.Contains(patterns, want) {
			t.Errorf("route table lacks %q", want)
		}
	}
}

func TestCommentFormLiveMountCORSAndLimiter(t *testing.T) {
	t.Parallel()
	var hosting statichost.Holder
	hosting.Store(statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite})
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		hosting: &hosting,
		pages:   newRenderFixture(t).h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
		commentFormLive: stub("form-live"),
	})
	path := commentFormLivePrefix + uuid.NewString()
	get := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := get(testStaticSite)
	if rec.Body.String() != "form-live" || rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite || rec.Header().Get("Vary") != "Origin" {
		t.Errorf("GET from S: %q %v", rec.Body, rec.Header())
	}
	assertNoCredentials(t, rec)
	for i := 2; i <= 10; i++ {
		if code := get(testStaticSite).Code; code == http.StatusTooManyRequests {
			t.Fatalf("load %d was limited", i)
		}
	}
	if code := get(testStaticSite).Code; code != http.StatusTooManyRequests {
		t.Errorf("11th load = %d, want 429", code)
	}
}
