package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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

// One IPv6 /64 is one client: a host cannot get a fresh bucket per address.
func TestSitemapChildRoute_SharesBucketAcrossIPv6Prefix(t *testing.T) {
	f := newRenderFixture(t)
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
	})

	get := func(addr string) int {
		req := httptest.NewRequest(http.MethodGet, "/sitemap-x.xml", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for i := range 40 {
		get("[2001:db8:1:2::" + strconv.Itoa(i+1) + "]:1234")
	}
	if code := get("[2001:db8:1:2:ffff::9]:1234"); code != http.StatusTooManyRequests {
		t.Errorf("a new address in the same /64 got %d, want 429", code)
	}
	if code := get("[2001:db8:1:3::1]:1234"); code != http.StatusOK {
		t.Errorf("another /64 got %d, want 200", code)
	}
}
