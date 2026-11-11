package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/analytics/geotest"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// redirectServer is a server whose runtime hosts the request.redirect test
// plugin in the given mode, declaring /go and /go/ and the cookie pref.
type redirectServer struct {
	s     *Server
	asked atomic.Int32 // request.redirect chains started
	geo   string       // path of the country database
}

func newRedirectServer(t *testing.T, mode string) *redirectServer {
	t.Helper()
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	s.hooks = hooks.New(s.logger)
	rs := &redirectServer{s: s}
	// Runs first and sees every chain, whoever owns the path.
	s.hooks.AddFilter(sdk.RequestRedirect.Name, -1000, func(_ context.Context, v any) (any, error) {
		rs.asked.Add(1)
		return v, nil
	})
	rt, err := pluginhost.NewRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), s.hooks, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	rt.Configure(pluginhost.Config{})
	m := sdk.Manifest{
		Name: "rdr", Version: "0.1.0", SDK: "^0.22",
		Cookies: []string{"pref"}, RedirectPaths: []string{"/go", "/go/"},
		Config: map[string]string{"mode": mode},
	}
	if st := rt.Load(context.Background(), m, plugintest.Build(t, "redirect")); !st.Loaded {
		t.Fatalf("redirect plugin: %s", st.Error)
	}
	s.plugins = rt

	rs.geo = filepath.Join(t.TempDir(), "geo.mmdb")
	rs.swapGeo(t, "XX", "YY")
	h := analytics.NewGeoHolder(rs.geo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.Reload()
	s.geoHolder = h
	return rs
}

// swapGeo replaces the country database by an atomic rename.
func (rs *redirectServer) swapGeo(t *testing.T, low, high string) {
	t.Helper()
	tmp := rs.geo + ".new"
	if err := os.WriteFile(tmp, geotest.TinyMMDB(low, high), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, rs.geo); err != nil {
		t.Fatal(err)
	}
}

// chain puts the wrapper in front of a handler that records what reaches
// normal handling.
func (rs *redirectServer) chain() (h http.Handler, reached *atomic.Int32, uncached *atomic.Bool) {
	reached, uncached = new(atomic.Int32), new(atomic.Bool)
	h = rs.s.requestRedirect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		uncached.Store(uncachedRequest(r.Context()))
		_, _ = io.WriteString(w, "normal")
	}))
	return h, reached, uncached
}

// get serves a request from 1.2.3.4 (the low half of the fixture) behind a
// trusted proxy, with the given cookies as name=value strings.
func get(h http.Handler, method, target string, cookies ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	for _, c := range cookies {
		n, v, _ := strings.Cut(c, "=")
		req.AddCookie(&http.Cookie{Name: n, Value: v})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequestRedirectAnswersDeclaredPath(t *testing.T) {
	rs := newRedirectServer(t, "echo")
	h, reached, _ := rs.chain()
	rec := get(h, "GET", "/go?utm=x", "hmp_rdr_pref=v1", "hmp_rdr_hidden=b", "session=secret")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || loc.Path != "/echo" {
		t.Fatalf("location = %q, %v", rec.Header().Get("Location"), err)
	}
	q := loc.Query()
	if q.Get("path") != "/go" || q.Get("query") != "utm=x" || q.Get("country") != "XX" {
		t.Fatalf("plugin saw %v, want path /go, query utm=x, country XX", q)
	}
	if q.Get("cookies") != "pref" {
		t.Fatalf("plugin saw cookies %q, want only pref", q.Get("cookies"))
	}
	if strings.Contains(loc.RawQuery, "1.2.3.4") {
		t.Fatal("the client address reached the plugin")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	if reached.Load() != 0 {
		t.Fatal("a redirected request also reached normal handling")
	}
}

func TestRequestRedirectSetsNamespacedCookie(t *testing.T) {
	rs := newRedirectServer(t, "redirect")
	h, _, _ := rs.chain()
	rec := get(h, "GET", "/go", "hmp_rdr_pref=v1")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Path != "/go" || loc.Query().Get("c") != "XX" || loc.Query().Get("pref") != "v1" {
		t.Fatalf("location = %q", rec.Header().Get("Location"))
	}
	var got *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "hmp_rdr_pref" {
			got = c
		}
	}
	if got == nil || got.Value != "x" || !got.HttpOnly || got.SameSite != http.SameSiteLaxMode || got.MaxAge != 3600 {
		t.Fatalf("cookie = %+v", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestRequestRedirectMethodsAndPaths(t *testing.T) {
	rs := newRedirectServer(t, "echo")
	h, reached, _ := rs.chain()
	normal := 0
	for _, tc := range []struct {
		method, target string
		asked          bool
	}{
		{"GET", "/go", true},
		{"HEAD", "/go", true},
		{"GET", "/go/", true}, // the trailing slash is its own declared path
		{"POST", "/go", false},
		{"GET", "/other", false},
		{"GET", "/go/x", false},
		{"GET", "/GO", false},
		{"GET", "/", false},
	} {
		before := rs.asked.Load()
		rec := get(h, tc.method, tc.target)
		if asked := rs.asked.Load() > before; asked != tc.asked {
			t.Errorf("%s %s: plugin called = %v, want %v", tc.method, tc.target, asked, tc.asked)
		}
		if !tc.asked {
			normal++
			if rec.Body.String() != "normal" {
				t.Errorf("%s %s: not handled normally: %d %q", tc.method, tc.target, rec.Code, rec.Body.String())
			}
		}
	}
	if int(reached.Load()) != normal {
		t.Fatalf("normal handling ran %d times, want %d", reached.Load(), normal)
	}
}

func TestRequestRedirectWithoutRuntimeIsInert(t *testing.T) {
	rs := newRedirectServer(t, "echo")
	rs.s.plugins = nil
	h, reached, _ := rs.chain()
	if rec := get(h, "GET", "/go"); rec.Body.String() != "normal" || reached.Load() != 1 {
		t.Fatalf("no runtime: %d %q", rec.Code, rec.Body.String())
	}
	if rs.asked.Load() != 0 {
		t.Fatal("the filter ran without a runtime")
	}
}

func TestRequestRedirectCookieOnlyBypassesCache(t *testing.T) {
	rs := newRedirectServer(t, "cookie")
	h, reached, uncached := rs.chain()
	rec := get(h, "GET", "/go")
	if rec.Body.String() != "normal" || reached.Load() != 1 {
		t.Fatalf("cookie-only answer did not fall through: %d %q", rec.Code, rec.Body.String())
	}
	if !uncached.Load() {
		t.Fatal("the request reached normal handling without the uncached mark")
	}
	var set bool
	for _, c := range rec.Result().Cookies() {
		set = set || c.Name == "hmp_rdr_pref"
	}
	if !set {
		t.Fatal("the declared cookie was not set")
	}
}

func TestRequestRedirectFailsOpen(t *testing.T) {
	for _, mode := range []string{"trap", "empty", "badloc", "protoloc", "status301", "undeclared", "sl" + "eep"} {
		t.Run(mode, func(t *testing.T) {
			rs := newRedirectServer(t, mode)
			h, reached, uncached := rs.chain()
			rec := get(h, "GET", "/go")
			if rec.Code != http.StatusOK || rec.Body.String() != "normal" || reached.Load() != 1 {
				t.Fatalf("%s: %d %q, want normal handling", mode, rec.Code, rec.Body.String())
			}
			if uncached.Load() {
				t.Fatalf("%s: a failed or empty answer left the cache", mode)
			}
			if len(rec.Result().Cookies()) != 0 || rec.Header().Get("Cache-Control") != "" {
				t.Fatalf("%s: a dropped answer still wrote headers: %v", mode, rec.Header())
			}
		})
	}
}

func TestRequestRedirectRunsBeforeRouterInDispatcher(t *testing.T) {
	rs := newRedirectServer(t, "redirect")
	rs.s.setupRoutes()
	rec := get(rs.s.dispatcher(), "GET", "/go")
	if rec.Code != http.StatusFound || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("dispatcher: %d, %v", rec.Code, rec.Header())
	}
}

func TestRequestRedirectSkipsPublicAPIPrefix(t *testing.T) {
	rs := newRedirectServer(t, "echo")
	rs.s.cfg.PublicSite.APIPrefix = "/go"
	h, reached, _ := rs.chain()
	if rec := get(h, "GET", "/go"); rec.Body.String() != "normal" || reached.Load() != 1 {
		t.Fatalf("the public API prefix was answered by a plugin: %d %q", rec.Code, rec.Body.String())
	}
}

func TestRequestRedirectFollowsCountryReload(t *testing.T) {
	rs := newRedirectServer(t, "redirect")
	h, _, _ := rs.chain()
	country := func() string {
		loc, _ := url.Parse(get(h, "GET", "/go").Header().Get("Location"))
		return loc.Query().Get("c")
	}
	if got := country(); got != "XX" {
		t.Fatalf("country = %q, want XX", got)
	}
	rs.swapGeo(t, "YY", "XX")
	rs.s.geoHolder.Reload()
	if got := country(); got != "YY" {
		t.Fatalf("country after the file was replaced = %q, want YY", got)
	}
}

func TestRequestRedirectNoGeoDatabaseGivesEmptyCountry(t *testing.T) {
	rs := newRedirectServer(t, "redirect")
	rs.s.geoHolder = nil
	h, _, _ := rs.chain()
	loc, _ := url.Parse(get(h, "GET", "/go").Header().Get("Location"))
	if c, ok := loc.Query()["c"]; !ok || c[0] != "" {
		t.Fatalf("location = %q, want an empty country", loc)
	}
}

// Every path core serves itself, and every root icon, must be refused for
// declaration: the wrapper runs before the router and would shadow them.
func TestRedirectPathRefusalCoversCoreRoutes(t *testing.T) {
	for seg := range fixedRoutePrefixes() {
		if adminpath.RedirectPathRefusal("/"+seg) == "" {
			t.Errorf("core route /%s can be declared as a redirect path", seg)
		}
	}
	for _, icon := range render.RootIcons {
		if adminpath.RedirectPathRefusal("/"+icon) == "" {
			t.Errorf("root icon /%s can be declared as a redirect path", icon)
		}
	}
}
