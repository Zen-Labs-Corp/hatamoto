package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/siteaccess"
)

type gateFixture struct {
	f      *renderFixture
	set    siteaccess.Settings
	signed bool
	g      http.Handler
	reach  int // requests that reached the site behind the gate
	gate   *siteAccessGate
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	x := &gateFixture{f: newRenderFixture(t), set: siteaccess.Settings{Mode: siteaccess.ModeOpen, RetryAfter: 900, Epoch: 1, PasswordHash: "pw-hash"}}
	x.f.next = okPage("About")
	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x.reach++
		switch r.URL.Path {
		case "/":
			x.f.h.handleRenderHome(w, r)
		case "/health", "/api/v1/ping":
			w.WriteHeader(http.StatusNoContent)
		default:
			x.f.h.handleRenderCatchAll(w, r)
		}
	})
	x.gate = &siteAccessGate{
		settings: func(context.Context) siteaccess.Settings { return x.set },
		signedIn: func(*http.Request) bool { return x.signed },
		verify:   func(_ context.Context, pw, hash string) bool { return pw == "secret" && hash == "pw-hash" },
		pages:    func() *renderHandlers { return x.f.h },
		key:      []byte("test-key"),
		limiter:  middleware.NewRateLimiter(0.2, 5),
		now:      time.Now,
	}
	x.g = x.gate.wrap(site)
	return x
}

func (x *gateFixture) do(method, target string, body string, mut ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.RemoteAddr = "203.0.113.7:1234"
	for _, m := range mut {
		m(r)
	}
	w := httptest.NewRecorder()
	x.g.ServeHTTP(w, r)
	return w
}

func TestSiteAccessGateModes(t *testing.T) {
	x := newGateFixture(t)

	// Open: pages serve and fill the page cache.
	if w := x.do("GET", "/about", ""); w.Code != 200 || len(x.f.cache.sets) == 0 {
		t.Fatalf("open: %d, cache sets %v", w.Code, x.f.cache.sets)
	}
	cached := len(x.f.cache.data)

	for _, c := range []struct {
		mode   siteaccess.Mode
		status int
		retry  string
	}{
		{siteaccess.ModeComingSoon, 200, ""},
		{siteaccess.ModeMaintenance, 503, "900"},
		{siteaccess.ModePassword, 401, ""},
	} {
		x.set.Mode = c.mode
		before := x.reach
		for _, p := range []string{"/", "/about"} {
			w := x.do("GET", p, "")
			if w.Code != c.status {
				t.Fatalf("%s %s: status %d", c.mode, p, w.Code)
			}
			hd := w.Header()
			if hd.Get("X-Robots-Tag") != "noindex" || hd.Get("Cache-Control") != "no-store" || hd.Get("Retry-After") != c.retry {
				t.Errorf("%s %s: headers %v", c.mode, p, hd)
			}
			if hd.Get("WWW-Authenticate") != "" {
				t.Errorf("%s: WWW-Authenticate set", c.mode)
			}
			if !strings.Contains(w.Body.String(), `name="robots" content="noindex`) || strings.Contains(w.Body.String(), "About") {
				t.Errorf("%s %s: body %q", c.mode, p, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "<form") != (c.mode == siteaccess.ModePassword) {
				t.Errorf("%s: form markup wrong", c.mode)
			}
		}
		if x.reach != before {
			t.Errorf("%s: gate let a request through", c.mode)
		}
	}
	if len(x.f.cache.data) != cached {
		t.Errorf("a gate response reached the page cache: %v", x.f.cache.sets)
	}
}

func TestSiteAccessGateMachineFilesAndRobots(t *testing.T) {
	x := newGateFixture(t)
	paths := []string{"/sitemap.xml", "/sitemap-posts.xml", "/sitemap.xsl", "/feed.xml", "/atom.xml", "/llms.txt"}
	for mode, want := range map[siteaccess.Mode]int{siteaccess.ModeComingSoon: 404, siteaccess.ModePassword: 404, siteaccess.ModeMaintenance: 503} {
		x.set.Mode = mode
		for _, p := range paths {
			w := x.do("GET", p, "")
			if w.Code != want || (want == 503) != (w.Header().Get("Retry-After") == "900") {
				t.Errorf("%s %s: %d %v", mode, p, w.Code, w.Header())
			}
		}
		w := x.do("GET", "/robots.txt", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Disallow: /") {
			t.Errorf("%s robots: %d %q", mode, w.Code, w.Body.String())
		}
	}
	x.set.Mode = siteaccess.ModeOpen
	if w := x.do("GET", "/robots.txt", ""); w.Code != 200 || x.reach == 0 {
		t.Errorf("open robots must reach the site")
	}
}

func TestSiteAccessGateBypassAndSignedIn(t *testing.T) {
	x := newGateFixture(t)
	for _, mode := range []siteaccess.Mode{siteaccess.ModeComingSoon, siteaccess.ModeMaintenance, siteaccess.ModePassword} {
		x.set.Mode = mode
		for _, p := range []string{"/health", "/api/v1/ping"} {
			if w := x.do("GET", p, ""); w.Code != http.StatusNoContent {
				t.Errorf("%s %s: %d", mode, p, w.Code)
			}
		}
		// A form POST is gated like a page.
		if w := x.do("POST", "/forms/contact/submit", "a=b"); w.Code == http.StatusNoContent || w.Code == http.StatusSeeOther {
			t.Errorf("%s: form post passed: %d", mode, w.Code)
		}
		x.signed = true
		w := x.do("GET", "/about", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "About") || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s signed in: %d %q %v", mode, w.Code, w.Header().Get("Cache-Control"), w.Body.Len())
		}
		x.signed = false
	}
}

func submitBody(pw string) string {
	return url.Values{"password": {pw}, "next": {"/about"}}.Encode()
}

func TestSiteAccessGatePassword(t *testing.T) {
	x := newGateFixture(t)
	x.set.Mode = siteaccess.ModePassword

	w := x.do("POST", siteaccess.SubmitPath, submitBody("nope"))
	if w.Code != 401 || !strings.Contains(w.Body.String(), `role="alert"`) || len(w.Result().Cookies()) != 0 {
		t.Fatalf("wrong password: %d %v", w.Code, w.Result().Cookies())
	}

	w = x.do("POST", siteaccess.SubmitPath, submitBody("secret"))
	if w.Code != 303 || w.Header().Get("Location") != "/about" {
		t.Fatalf("right password: %d %v", w.Code, w.Header())
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteLaxMode || cs[0].MaxAge != 30*24*3600 || cs[0].Secure {
		t.Fatalf("cookie %+v", cs)
	}
	withCookie := func(c *http.Cookie) func(*http.Request) { return func(r *http.Request) { r.AddCookie(c) } }
	if w := x.do("GET", "/about", "", withCookie(cs[0])); w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("with cookie: %d %v", w.Code, w.Header())
	}

	// Tampered, and issued before a password change (epoch bump): gate again.
	bad := *cs[0]
	bad.Value = strings.Replace(bad.Value, "v1.", "v1.9", 1)
	if w := x.do("GET", "/about", "", withCookie(&bad)); w.Code != 401 {
		t.Errorf("tampered cookie: %d", w.Code)
	}
	x.set.Epoch++
	if w := x.do("GET", "/about", "", withCookie(cs[0])); w.Code != 401 {
		t.Errorf("old epoch cookie: %d", w.Code)
	}

	// https sets Secure; an off-site next never redirects away.
	w = x.do("POST", siteaccess.SubmitPath, url.Values{"password": {"secret"}, "next": {"//evil.test"}}.Encode(), func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https"); r.TLS = nil })
	if w.Header().Get("Location") != "/" {
		t.Errorf("unsafe next: %q", w.Header().Get("Location"))
	}

	// The submit path is dead outside password mode.
	x.set.Mode = siteaccess.ModeMaintenance
	if w := x.do("POST", siteaccess.SubmitPath, submitBody("secret")); w.Code != 503 {
		t.Errorf("submit in maintenance: %d", w.Code)
	}
}

func TestSiteAccessGateRateLimit(t *testing.T) {
	x := newGateFixture(t)
	x.set.Mode = siteaccess.ModePassword
	var last *httptest.ResponseRecorder
	for range 7 {
		last = x.do("POST", siteaccess.SubmitPath, submitBody("nope"))
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") == "" {
		t.Fatalf("after burst: %d %v", last.Code, last.Header())
	}
}

func TestSiteAccessPausesIndexNow(t *testing.T) {
	set := seo.Defaults()
	set.IndexNow.Enabled, set.IndexNow.Key = true, "k"
	paused := false
	r := &indexNowResolver{
		settings: func(context.Context) *seo.Settings { return &set },
		baseURL:  func(context.Context) string { return "https://example.com" },
		paused:   func(context.Context) bool { return paused },
	}
	if _, ok := r.Config(context.Background()); !ok {
		t.Fatal("open: pings must be on")
	}
	paused = true
	if _, ok := r.Config(context.Background()); ok {
		t.Fatal("a held-back site must not ping")
	}
}

// The anonymous public API, through the real mux and dispatcher, must follow
// the mode: it lists every published entity, so it may not answer a visitor.
func TestSiteAccessGatesPublicAPIOnRealMux(t *testing.T) {
	h, s := wiredHandler(t, false, "/admin")
	var set siteaccess.Settings
	s.accessSettings = func(context.Context) (siteaccess.Settings, error) { return set, nil }

	paths := []string{
		"/api/v1/public/llms.txt", "/api/v1/public/sitemap.xml", "/api/v1/public/sitemap-posts.xml",
		"/api/v1/public/knowledge-graph?page=1", "/api/v1/public/terms/tag/news/entities",
		"/api/v1/public/forms/contact",
	}
	for mode, want := range map[siteaccess.Mode]int{
		siteaccess.ModeComingSoon: 404, siteaccess.ModePassword: 404, siteaccess.ModeMaintenance: 503,
	} {
		set = siteaccess.Settings{Mode: mode, RetryAfter: 900, Epoch: 1, PasswordHash: "x"}
		for _, p := range paths {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "{") {
				t.Errorf("%s GET %s: %d %q %v", mode, p, w.Code, w.Body.String(), w.Header())
			}
		}
		// The authenticated API still reaches its own guard (401), not the gate.
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/entities", nil))
		if w.Code != 401 {
			t.Errorf("%s: admin API answered %d, want its own 401", mode, w.Code)
		}
	}
}

func TestSiteAccessPublicAPIPrefixAndPass(t *testing.T) {
	x := newGateFixture(t)
	x.gate.apiPrefix = "/api/feed"
	x.set.Mode = siteaccess.ModePassword
	for _, p := range []string{"/api/feed/llms.txt", "/api/v1/public/llms.txt", "/api/other"} {
		if w := x.do("GET", p, ""); w.Code != 404 || x.reach != 0 {
			t.Errorf("%s: %d, reached %d", p, w.Code, x.reach)
		}
	}
	// A signed-in admin and a valid visitor cookie still get through.
	x.signed = true
	x.do("GET", "/api/v1/public/llms.txt", "")
	x.signed = false
	cookie := &http.Cookie{Name: siteaccess.CookieName, Value: siteaccess.Sign(x.gate.key, x.set.Epoch, time.Now())}
	x.do("GET", "/api/feed/llms.txt", "", func(r *http.Request) { r.AddCookie(cookie) })
	if x.reach != 2 {
		t.Errorf("admin and cookie requests reached %d, want 2", x.reach)
	}
}
