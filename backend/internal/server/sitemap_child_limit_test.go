package server

import (
	"net/http"
	"testing"
)

// Sitemap child files are throttled per client; the index and pages are not.
func TestSitemapChildRoute_IsRateLimited(t *testing.T) {
	f := newRenderFixture(t)
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
	})

	limited := 0
	for range 40 {
		if getPath(h, "/sitemap-x.xml").Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Error("40 rapid sitemap child requests were never limited")
	}
	if w := getPath(h, "/sitemap.xml"); w.Code != http.StatusOK || w.Body.String() != "sitemap" {
		t.Errorf("/sitemap.xml: %d %q", w.Code, w.Body)
	}
	f.next = okPage("About")
	if w := getPath(h, "/about"); w.Code != http.StatusOK {
		t.Errorf("/about: %d", w.Code)
	}
}
