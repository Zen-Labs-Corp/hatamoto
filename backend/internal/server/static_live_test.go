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

func TestStaticLiveThemePreviewNotRedirected(t *testing.T) {
	t.Parallel()
	h, calls := staticLiveRig(true)
	rec := staticLiveDo(h, http.MethodGet, "/?hm_theme_preview=tok", staticLiveHost, "")
	if rec.Code != http.StatusOK {
		t.Errorf("preview start: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+staticLiveHost+"/about", nil)
	req.AddCookie(&http.Cookie{Name: "hm_theme_preview", Value: "tok"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || *calls != 2 {
		t.Errorf("preview cookie: %d, next ran %d times", rec.Code, *calls)
	}
}

// staticLiveRigSameHost is staticLiveRig for same host: static mode, no live origin.
func staticLiveRigSameHost() (h http.Handler, calls *int) {
	s := &Server{cfg: &config.Config{}}
	s.setStaticHosting(statichost.StaticHosting{Static: true, SameHost: true, Site: testStaticSite})
	calls = new(int)
	h = s.staticLive(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		w.WriteHeader(http.StatusOK)
	}))
	return h, calls
}

// Same host never redirects: the web server in front sends only island paths
// here, and a page that does arrive is rendered, whatever its Host says.
func TestStaticLiveSameHostNoRedirect(t *testing.T) {
	t.Parallel()
	h, calls := staticLiveRigSameHost()
	n := 0
	for _, host := range []string{"example.com", "www.example.com", "", staticLiveHost} {
		for _, m := range []string{http.MethodGet, http.MethodHead} {
			for _, p := range []string{"/", "/blog/x", "/about?x=1"} {
				rec := staticLiveDo(h, m, p, host, "")
				n++
				if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
					t.Errorf("%s %s host %q: %d %q", m, p, host, rec.Code, rec.Header().Get("Location"))
				}
			}
		}
	}
	if *calls != n {
		t.Errorf("next ran %d of %d times", *calls, n)
	}
}

func TestStaticLiveSameHostIslandsAnswer(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ method, path string }{
		"form post":    {http.MethodPost, "/forms/x/submit"},
		"search":       {http.MethodGet, "/search?q=a"},
		"locale srch":  {http.MethodGet, "/fr/search"},
		"plugin":       {http.MethodGet, "/p/plug/x"},
		"preview":      {http.MethodGet, "/preview/tok"},
		"comment form": {http.MethodGet, commentFormLivePrefix + "11111111-1111-1111-1111-111111111111"},
		"track":        {http.MethodPost, "/api/v1/track"},
		"consent":      {http.MethodPost, "/api/v1/consent"},
		"admin":        {http.MethodGet, "/admin"},
	} {
		h, calls := staticLiveRigSameHost()
		rec := staticLiveDo(h, c.method, c.path, "example.com", "")
		if *calls != 1 || rec.Code != http.StatusOK {
			t.Errorf("%s: next calls=%d status=%d", name, *calls, rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Location") != "" {
			t.Errorf("%s: CORS or redirect headers set: %v", name, rec.Header())
		}
	}
	// A plugin preflight from the site is not answered with CORS: same origin needs none.
	h, calls := staticLiveRigSameHost()
	rec := staticLiveDo(h, http.MethodOptions, "/p/plug/x", "example.com", testStaticSite)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" || *calls != 1 {
		t.Errorf("same-host plugin preflight: calls=%d headers=%v", *calls, rec.Header())
	}
}

// The admin address is served by the dispatcher before the static chain, so
// it answers for the site's own Host on the same host.
func TestStaticLiveSameHostAdminReachable(t *testing.T) {
	t.Parallel()
	s := &Server{cfg: &config.Config{}, mux: http.NewServeMux()}
	s.setStaticHosting(statichost.StaticHosting{Static: true, SameHost: true, Site: testStaticSite})
	s.adminBundle.Store(&adminBundle{prefix: "/hq-x7k", h: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})})
	rec := staticLiveDo(s.dispatcher(), http.MethodGet, "/hq-x7k/login", "example.com", "")
	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "" {
		t.Errorf("admin: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
