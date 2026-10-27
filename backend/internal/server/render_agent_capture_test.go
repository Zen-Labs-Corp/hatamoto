package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/agents"
)

func TestRenderRoutesCaptureAgentHits(t *testing.T) {
	s, st := newCaptureServer()
	f := newRenderFixture(t)
	f.next = okPage("About")
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), feed: stub("feed"), xsl: stub("xsl"), formPost: stub("form"),
		capture: s.agentCaptureMiddleware,
	})

	get := func(path, ua string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", ua)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", "20.171.207.1")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	for _, p := range []string{"/", "/about?utm=1", "/robots.txt", "/sitemap.xml", "/sitemap-en.xml", "/llms.txt", "/feed.xml", "/atom.xml"} {
		get(p, captureGPTUA)
	}
	// Not recorded: humans, static assets, reserved API/media paths.
	get("/about", "Mozilla/5.0 Firefox/120.0")
	get("/theme/assets/hm.js", captureGPTUA)
	get("/sitemap.xsl", captureGPTUA)
	get("/api/other", captureGPTUA)
	get("/media/a.png", captureGPTUA)
	s.agentWriter.Close()

	byPath := map[string]agents.Hit{}
	for _, h := range st.got() {
		byPath[h.Path] = h
	}
	if len(byPath) != 8 {
		t.Fatalf("recorded %d distinct paths, want 8: %+v", len(byPath), byPath)
	}
	for _, p := range []string{"/", "/about", "/robots.txt", "/sitemap.xml", "/sitemap-en.xml", "/llms.txt", "/feed.xml", "/atom.xml"} {
		h, ok := byPath[p]
		if !ok {
			t.Errorf("%s not recorded", p)
			continue
		}
		if h.AgentID != "gptbot" || h.Source != agents.SourceBackend || h.Status != 200 || h.Method != "GET" ||
			h.IP == nil || h.IP.String() != "20.171.207.1" {
			t.Errorf("%s: %+v", p, h)
		}
	}
	if byPath["/about"].Bytes == 0 {
		t.Error("page hit should carry response bytes")
	}
}

func TestRenderCaptureRecordsThemed404(t *testing.T) {
	s, st := newCaptureServer()
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{pages: f.h, capture: s.agentCaptureMiddleware, robots: stub(""), sitemap: stub(""),
		subSitemap: stub(""), llms: stub(""), xsl: stub(""), assets: stub(""), formPost: stub("")})
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	req.Header.Set("User-Agent", captureGPTUA)
	h.ServeHTTP(httptest.NewRecorder(), req)
	s.agentWriter.Close()
	hits := st.got()
	if len(hits) != 1 || hits[0].Status != 404 || hits[0].Path != "/missing" {
		t.Fatalf("hits: %+v", hits)
	}
}

func TestAgentCaptureWithoutWriter(t *testing.T) {
	called := false
	h := (&Server{}).agentCaptureMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("handler not called")
	}
}
