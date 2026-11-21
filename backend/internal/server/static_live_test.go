package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/statichost"
)

const staticLiveHost = "live.example.net"

// staticLiveRig wraps a recording stub in staticLive. calls counts how often
// the stub (the rest of the chain) ran.
func staticLiveRig(static bool) (h http.Handler, calls *int) {
	s := &Server{cfg: &config.Config{}}
	if static {
		setStaticForTest(s)
	}
	calls = new(int)
	h = s.staticLive(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		w.WriteHeader(http.StatusOK)
	}))
	return h, calls
}

func staticLiveDo(h http.Handler, method, target, host, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+host+"/", nil)
	req.URL.Path, req.URL.RawQuery, _ = strings.Cut(target, "?")
	req.RequestURI = target
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStaticLiveRedirectsPages(t *testing.T) {
	t.Parallel()
	h, calls := staticLiveRig(true)
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		rec := staticLiveDo(h, m, "/about?x=1", staticLiveHost, "")
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != testStaticSite+"/about?x=1" {
			t.Errorf("%s: %d %q", m, rec.Code, rec.Header().Get("Location"))
		}
		if rec.Header().Get("Cache-Control") != "public, max-age=3600" {
			t.Errorf("%s Cache-Control = %q", m, rec.Header().Get("Cache-Control"))
		}
	}
	if *calls != 0 {
		t.Errorf("next ran %d times", *calls)
	}
}

func TestStaticLivePassesThrough(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ method, path, host string }{
		"post":        {http.MethodPost, "/about", staticLiveHost},
		"static host": {http.MethodGet, "/about", "static.example.net"},
		"search":      {http.MethodGet, "/search?q=a", staticLiveHost},
		"locale srch": {http.MethodGet, "/fr/search", staticLiveHost},
		"forms":       {http.MethodGet, "/forms/x/submit", staticLiveHost},
		"comments":    {http.MethodGet, "/-/comments/form/x", staticLiveHost},
		"preview":     {http.MethodGet, "/preview/x", staticLiveHost},
		"api":         {http.MethodGet, "/api/v1/public/x", staticLiveHost},
		"media":       {http.MethodGet, "/media/a.jpg", staticLiveHost},
		"theme":       {http.MethodGet, "/theme/assets/x.css", staticLiveHost},
		"plugin":      {http.MethodGet, "/p/plug/x", staticLiveHost},
		"admin":       {http.MethodGet, "/admin", staticLiveHost},
		"feed":        {http.MethodGet, "/feed.xml", staticLiveHost},
	} {
		h, calls := staticLiveRig(true)
		rec := staticLiveDo(h, c.method, c.path, c.host, "")
		if *calls != 1 || rec.Code != http.StatusOK {
			t.Errorf("%s: next calls=%d status=%d", name, *calls, rec.Code)
		}
	}
}

func TestStaticLiveExportNotRedirected(t *testing.T) {
	t.Parallel()
	h, calls := staticLiveRig(true)
	req := httptest.NewRequest(http.MethodGet, "http://"+staticLiveHost+"/about", nil)
	req = req.WithContext(statichost.WithExport(context.Background()))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if *calls != 1 {
		t.Errorf("export request was redirected")
	}
}

func TestStaticLiveNeverOpenRedirect(t *testing.T) {
	t.Parallel()
	h, _ := staticLiveRig(true)
	rec := staticLiveDo(h, http.MethodGet, "//evil.example/x", staticLiveHost, "")
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, testStaticSite+"/") {
		t.Errorf("Location = %q", loc)
	}
}

func TestStaticLivePluginCORS(t *testing.T) {
	t.Parallel()
	h, calls := staticLiveRig(true)
	rec := staticLiveDo(h, http.MethodOptions, "/p/plug/x", staticLiveHost, testStaticSite)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite ||
		!strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") || *calls != 0 {
		t.Errorf("preflight from S: %d %v calls=%d", rec.Code, rec.Header(), *calls)
	}
	assertNoCredentials(t, rec)

	rec = staticLiveDo(h, http.MethodOptions, "/p/plug/x", staticLiveHost, "https://evil.example")
	if *calls != 1 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight from evil: calls=%d %v", *calls, rec.Header())
	}

	rec = staticLiveDo(h, http.MethodGet, "/p/plug/x", staticLiveHost, testStaticSite)
	if *calls != 2 || rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite || rec.Header().Get("Vary") != "Origin" {
		t.Errorf("GET from S: calls=%d %v", *calls, rec.Header())
	}
	assertNoCredentials(t, rec)
}

func TestStaticLiveServedModeUntouched(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ method, path, origin string }{
		{http.MethodGet, "/about", ""},
		{http.MethodHead, "/about", ""},
		{http.MethodOptions, "/p/plug/x", testStaticSite},
		{http.MethodGet, "/p/plug/x", testStaticSite},
		{http.MethodGet, "/search", ""},
	} {
		h, calls := staticLiveRig(false)
		rec := staticLiveDo(h, c.method, c.path, staticLiveHost, c.origin)
		if *calls != 1 || rec.Code != http.StatusOK || len(rec.Header()) != 0 {
			t.Errorf("%s %s: calls=%d status=%d headers=%v", c.method, c.path, *calls, rec.Code, rec.Header())
		}
	}
}
