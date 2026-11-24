package server

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

func capaManifest() sdk.Manifest {
	return sdk.Manifest{Name: "capa", Version: "0.1.0", SDK: "^0.6",
		Permissions: []string{sdk.PermKV, sdk.PermFormsRead},
		Cookies:     []string{"unlock", "visited"},
		Routes: []sdk.Route{
			{Method: "GET", Path: "/echo"},
			{Method: "GET", Path: "/who/{name}", Public: true},
		}}
}

func capaRuntime(t *testing.T, s *Server) *pluginhost.Runtime {
	t.Helper()
	return capaRuntimeWith(t, s, nil)
}

// capaRuntimeWith is capaRuntime with a change to the manifest.
func capaRuntimeWith(t *testing.T, s *Server, mutate func(*sdk.Manifest)) *pluginhost.Runtime {
	t.Helper()
	rt, err := pluginhost.NewRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), s.hooks, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	rt.Configure(pluginhost.Config{ClientKeys: clientkey.New("test-secret")}) // a plugin still needs the client.key grant
	m := capaManifest()
	if mutate != nil {
		mutate(&m)
	}
	if st := rt.Load(context.Background(), m, plugintest.Build(t, "capa")); !st.Loaded {
		t.Fatalf("capa: %s", st.Error)
	}
	s.plugins = rt
	return rt
}

func doReq(h http.Handler, method, path, token string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func tokenFor(t *testing.T, s *Server, role string) string {
	t.Helper()
	return "Bearer test-session-" + role
}

func TestPluginRoutesMounts(t *testing.T) {
	h, s := bootRoutes(t)
	rt := capaRuntime(t, s)
	owner, editor := tokenFor(t, s, "owner"), tokenFor(t, s, "editor")

	// Admin mount: auth first, then the route's roles.
	if rec := doReq(h, "GET", "/api/v1/p/capa/echo", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rec.Code)
	}
	if rec := doReq(h, "GET", "/api/v1/p/capa/echo", editor); rec.Code != http.StatusForbidden {
		t.Fatalf("editor on an owner-only route = %d", rec.Code)
	}
	rec := doReq(h, "GET", "/api/v1/p/capa/echo", owner)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"role":"owner"`) {
		t.Fatalf("owner = %d %s", rec.Code, rec.Body)
	}
	// Public mount: anonymous, and only the routes declared public.
	if rec := doReq(h, "GET", "/p/capa/who/ada", ""); rec.Code != 200 || rec.Body.String() != "ada" {
		t.Fatalf("public = %d %q", rec.Code, rec.Body)
	}
	if rec := doReq(h, "GET", "/p/capa/echo", ""); rec.Code != 404 {
		t.Fatalf("an admin route on the public mount = %d", rec.Code)
	}
	if rec := doReq(h, "GET", "/p/nobody/x", ""); rec.Code != 404 {
		t.Fatalf("unknown plugin = %d", rec.Code)
	}

	// Core routes are fixed and are never shadowed: a plugin cannot answer
	// /api/v1/me or /health whatever it declares.
	if code, _ := hit(h, "GET", "/api/v1/me", "Authorization", owner); code == 201 {
		t.Fatal("a plugin shadowed a core route")
	}
	if rec := doReq(h, "GET", "/health", ""); rec.Code == 201 {
		t.Fatal("a plugin shadowed /health")
	}

	// Routes are swapped at run time, no restart.
	m := capaManifest()
	m.Routes = []sdk.Route{{Method: "GET", Path: "/echo"}, {Method: "GET", Path: "/new"}}
	if st := rt.Load(context.Background(), m, plugintest.Build(t, "capa")); !st.Loaded {
		t.Fatal(st.Error)
	}
	if rec := doReq(h, "GET", "/api/v1/p/capa/new", owner); rec.Code != 404 && rec.Code != 200 {
		t.Fatalf("new route = %d", rec.Code) // the test plugin answers 404 for paths it does not know: the router matched
	}
	if rec := doReq(h, "GET", "/p/capa/who/ada", ""); rec.Code != 404 {
		t.Fatalf("a dropped public route still answers: %d", rec.Code)
	}
	rt.Unload(context.Background(), "capa")
	if rec := doReq(h, "GET", "/api/v1/p/capa/echo", owner); rec.Code != 404 {
		t.Fatalf("an unloaded plugin answers: %d", rec.Code)
	}
}

func TestPublicPluginRoutesAreRateLimited(t *testing.T) {
	h, s := bootRoutes(t)
	capaRuntime(t, s)
	limited := false
	for range 80 {
		if rec := doReq(h, "GET", "/p/capa/who/ada", ""); rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("public plugin routes are not rate limited")
	}
}

func TestPluginGrantAPI(t *testing.T) {
	h, s := bootRoutes(t)
	capaRuntime(t, s)
	owner, editor := tokenFor(t, s, "owner"), tokenFor(t, s, "editor")

	list := func() map[string]any {
		rec := doReq(h, "GET", "/api/v1/plugins/", owner)
		var body struct {
			Plugins []map[string]any `json:"plugins"`
		}
		if err := jsonx.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Plugins) != 1 {
			t.Fatalf("list = %d %s", rec.Code, rec.Body)
		}
		return body.Plugins[0]
	}
	p := list()
	if got := p["permissions"].([]any); len(got) != 2 {
		t.Fatalf("permissions = %v", got)
	}
	if got := p["granted"].([]any); len(got) != 0 {
		t.Fatalf("nothing is granted by default: %v", got)
	}

	grant := func(token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/plugins/capa/grant", strings.NewReader(body))
		req.Header.Set("Authorization", token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := grant(editor, `{"permissions":["kv"]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("an editor granted permissions: %d", rec.Code)
	}
	if rec := grant(owner, `{"permissions":["http.fetch"]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("granting what the manifest did not request = %d %s", rec.Code, rec.Body)
	}
	if rec := grant(owner, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a body without permissions = %d", rec.Code)
	}
	rec := grant(owner, `{"permissions":["kv"]}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"granted":["kv"]`) {
		t.Fatalf("grant = %d %s", rec.Code, rec.Body)
	}
	if got := list()["granted"].([]any); len(got) != 1 || got[0] != "kv" {
		t.Fatalf("granted after = %v", got)
	}
	if rec := doReq(h, "POST", "/api/v1/plugins/ghost/grant", owner); rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("grant to an unknown plugin = %d", rec.Code)
	}
}

// membersPage is a page with a visible intro block and a secret one.
func membersPage() (*routeResult, pageOutcome, error) {
	p := pageResponse("Members")
	p.Entity.Attributes = jsontext.Value(`{"page_sections":[
		{"id":"intro","type":"content","title":"Intro","content":{"headline":"INTRO-VISIBLE","body":"<p>hi</p>"}},
		{"id":"secret","type":"content","title":"Secret","content":{"headline":"SECRET-BLOCK","body":"<p>members only</p>"}}]}`)
	return &routeResult{Page: p, Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
}

// render.page hides a block and sets a declared cookie, and such pages are
// request-dependent: private and never cached.
func TestRenderPagePlugin(t *testing.T) {
	f := newRenderFixture(t)
	_, s := bootRoutes(t)
	capaRuntime(t, s)
	f.h.pageFilter = pluginhost.NewPageFilter(s.plugins, s.hooks)
	if !f.h.pageFilter.Active() {
		t.Fatal("the filter must be active once a plugin handles render.page")
	}
	f.next = membersPage

	w := f.get("/members")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "INTRO-VISIBLE") || strings.Contains(body, "SECRET-BLOCK") {
		t.Fatalf("hidden block: %d\n%s", w.Code, body)
	}
	var visited *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "hmp_capa_visited" {
			visited = c
		}
		if c.Name == "evil" {
			t.Fatal("an undeclared cookie was set")
		}
	}
	if visited == nil || visited.Value != "1" || !visited.HttpOnly || visited.Path != "/" || visited.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie = %+v", visited)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") || w.Header().Get("Vary") != "Cookie" {
		t.Fatalf("a request-dependent page must be private: Cache-Control=%q Vary=%q", cc, w.Header().Get("Vary"))
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("the page was cached: %v", f.cache.sets)
	}

	// The declared unlock cookie keeps the block; the answer is per request.
	w = f.get("/members", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "hmp_capa_unlock", Value: "1"}) })
	if !strings.Contains(w.Body.String(), "SECRET-BLOCK") {
		t.Fatalf("the unlocked page lost its block:\n%s", w.Body)
	}
	if len(f.cache.sets) != 0 {
		t.Fatal("an unlocked page was cached for everyone")
	}

	// The session and CSRF cookies of an owner browsing the site never reach
	// a plugin: it reports every cookie it was given as a block title.
	w = f.get("/echo-cookies", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "SESSION-TOKEN-XYZ"})
		r.AddCookie(&http.Cookie{Name: "hatamoto_csrf", Value: "CSRF-TOKEN-XYZ"})
		r.AddCookie(&http.Cookie{Name: "hmp_capa_unlock", Value: "MINE-ABC"})
	})
	if b := w.Body.String(); strings.Contains(b, "SESSION-TOKEN-XYZ") || strings.Contains(b, "CSRF-TOKEN-XYZ") || !strings.Contains(b, "MINE-ABC") {
		t.Fatalf("the plugin must get its own cookie and nothing else:\n%s", b)
	}
}

// Granting or revoking through the API drops the cached pages like the admin
// form does: a revoked script link must not outlive the revoke in the cache.
func TestPluginGrantAPIClearsRenderedPages(t *testing.T) {
	h, s := bootRoutes(t)
	capaRuntime(t, s)
	owner := tokenFor(t, s, "owner")
	probe := newRenderProbe(s.logger)
	s.infra = &Infrastructure{Cache: probe.c, CacheInvalidator: cache.NewCacheInvalidator(probe.c, s.logger)}
	for _, body := range []string{`{"permissions":["kv"]}`, `{"permissions":[]}`} {
		probe.prime()
		req := httptest.NewRequest("POST", "/api/v1/plugins/capa/grant", strings.NewReader(body))
		req.Header.Set("Authorization", owner)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("grant %s = %d %s", body, rec.Code, rec.Body)
		}
		if !probe.cleared() {
			t.Errorf("grant %s did not clear the rendered pages", body)
		}
	}
}

// A refused site.scripts grant (community package) answers 403 and stores nothing.
func TestPluginGrantAPIRefusesCommunityScripts(t *testing.T) {
	h, s := bootRoutes(t)
	rt := capaRuntime(t, s)
	rt.SetGrantPolicy(func(_ string, perms []sdk.Permission) error {
		for _, p := range perms {
			if p == sdk.PermSiteScripts {
				return pluginhost.ErrCommunityScripts
			}
		}
		return nil
	})
	m := capaManifest()
	m.Permissions = append(m.Permissions, sdk.PermSiteScripts)
	if st := rt.Load(context.Background(), m, plugintest.Build(t, "capa")); !st.Loaded {
		t.Fatalf("capa: %s", st.Error)
	}
	req := httptest.NewRequest("POST", "/api/v1/plugins/capa/grant", strings.NewReader(`{"permissions":["kv","site.scripts"]}`))
	req.Header.Set("Authorization", tokenFor(t, s, "owner"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("grant = %d %s", rec.Code, rec.Body)
	}
	if g := rt.Grants().Granted("capa"); len(g) != 0 {
		t.Fatalf("a refused grant changed the grants: %v", g)
	}
}

func TestFormAttributionEvent(t *testing.T) {
	t.Parallel()
	if formAttributionEvent(nil) != nil {
		t.Fatal("nil attribution must stay nil so the key is omitted")
	}
	got := formAttributionEvent(&form.Attribution{
		First: &form.Touch{Channel: "ai_assistants", Source: "perplexity", Landing: "/pricing"},
	})
	b, err := jsonx.Marshal(sdk.FormSubmittedEvent{Attribution: got})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"attribution":{"first":{"channel":"ai_assistants","source":"perplexity","landing":"/pricing"}}`) {
		t.Errorf("payload = %s", b)
	}
	b, _ = jsonx.Marshal(sdk.FormSubmittedEvent{})
	if strings.Contains(string(b), "attribution") {
		t.Errorf("empty event carries attribution: %s", b)
	}
}

// A render.page plugin that declares page_paths makes only those pages
// request-dependent; every other page is cached and public.
func TestRenderPagePluginDeclaredPaths(t *testing.T) {
	f := newRenderFixture(t)
	_, s := bootRoutes(t)
	rt := capaRuntimeWith(t, s, func(m *sdk.Manifest) { m.PagePaths = []string{"/members"} })
	f.h.pageFilter = pluginhost.NewPageFilter(rt, s.hooks)
	f.next = membersPage

	w := f.get("/about")
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=60, s-maxage=300" {
		t.Fatalf("undeclared path Cache-Control = %q", cc)
	}
	if strings.Contains(w.Header().Get("Vary"), "Cookie") {
		t.Errorf("undeclared path Vary = %q", w.Header().Get("Vary"))
	}
	if !strings.Contains(w.Body.String(), "SECRET-BLOCK") {
		t.Errorf("the filter ran on an undeclared path:\n%s", w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "hmp_capa_visited" {
			t.Error("an undeclared path set the plugin cookie")
		}
	}
	if len(f.cache.sets) != 1 {
		t.Fatalf("undeclared page stored %d times, want 1", len(f.cache.sets))
	}
	resolves := f.resolves
	f.get("/about")
	if f.resolves != resolves {
		t.Fatal("the second request was not a cache hit")
	}

	w = f.get("/members")
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") || w.Header().Get("Vary") != "Cookie" {
		t.Fatalf("declared path must be private: Cache-Control=%q Vary=%q", cc, w.Header().Get("Vary"))
	}
	if strings.Contains(w.Body.String(), "SECRET-BLOCK") {
		t.Error("the filter did not run on the declared path")
	}
	var visited bool
	for _, c := range w.Result().Cookies() {
		visited = visited || c.Name == "hmp_capa_visited"
	}
	if !visited {
		t.Error("the declared path did not get the plugin cookie")
	}
	if len(f.cache.sets) != 1 {
		t.Fatalf("declared page was stored: %d sets", len(f.cache.sets))
	}

	rt.Unload(context.Background(), "capa")
	w = f.get("/members")
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=60, s-maxage=300" || !strings.Contains(w.Body.String(), "SECRET-BLOCK") {
		t.Fatalf("after unload: Cache-Control=%q\n%s", cc, w.Body)
	}
	if len(f.cache.sets) != 2 {
		t.Fatalf("after unload %d sets, want 2", len(f.cache.sets))
	}
}

// serve drops the trailing slash, so a declared /members covers /members/:
// private, filtered, never stored.
func TestRenderPagePluginDeclaredPathTrailingSlash(t *testing.T) {
	f := newRenderFixture(t)
	_, s := bootRoutes(t)
	rt := capaRuntimeWith(t, s, func(m *sdk.Manifest) { m.PagePaths = []string{"/members"} })
	f.h.pageFilter = pluginhost.NewPageFilter(rt, s.hooks)
	f.next = membersPage

	w := f.get("/members/")
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") || w.Header().Get("Vary") != "Cookie" {
		t.Fatalf("Cache-Control=%q Vary=%q, want private", cc, w.Header().Get("Vary"))
	}
	if strings.Contains(w.Body.String(), "SECRET-BLOCK") {
		t.Error("the filter did not run on /members/")
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("/members/ was stored: %d sets", len(f.cache.sets))
	}
}

// Disabling a site-wide render.page plugin restores the page cache.
func TestRenderPagePluginUnloadRestoresCache(t *testing.T) {
	f := newRenderFixture(t)
	_, s := bootRoutes(t)
	rt := capaRuntime(t, s)
	f.h.pageFilter = pluginhost.NewPageFilter(rt, s.hooks)
	f.next = okPage("About")

	w := f.get("/about")
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || len(f.cache.sets) != 0 {
		t.Fatalf("site-wide plugin: Cache-Control=%q sets=%d", w.Header().Get("Cache-Control"), len(f.cache.sets))
	}
	rt.Unload(context.Background(), "capa")
	w = f.get("/about")
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=60, s-maxage=300" || len(f.cache.sets) != 1 {
		t.Fatalf("after unload: Cache-Control=%q sets=%d", cc, len(f.cache.sets))
	}
}

// A prefix makes every page under it request-dependent: private, filtered on
// each request, never stored. The bare prefix and look-alikes stay public.
func TestPluginPrefixPageUncached(t *testing.T) {
	f := newRenderFixture(t)
	_, s := bootRoutes(t)
	rt := capaRuntimeWith(t, s, func(m *sdk.Manifest) { m.PagePathPrefixes = []string{"/item/"} })
	f.h.pageFilter = pluginhost.NewPageFilter(rt, s.hooks)
	f.next = membersPage

	resolves := f.resolves
	for i := 0; i < 2; i++ {
		w := f.get("/item/x")
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") || w.Header().Get("Vary") != "Cookie" {
			t.Fatalf("request %d: Cache-Control=%q Vary=%q", i, cc, w.Header().Get("Vary"))
		}
		if strings.Contains(w.Body.String(), "SECRET-BLOCK") {
			t.Errorf("request %d: the filter did not run", i)
		}
	}
	if f.resolves != resolves+2 {
		t.Errorf("resolves advanced by %d, want 2", f.resolves-resolves)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("a prefix page was stored: %d sets", len(f.cache.sets))
	}
	for i, path := range []string{"/item", "/items/x"} {
		w := f.get(path)
		if !strings.Contains(w.Body.String(), "SECRET-BLOCK") {
			t.Errorf("%s: the filter ran outside the prefix", path)
		}
		if len(f.cache.sets) != i+1 {
			t.Fatalf("%s: %d sets, want %d", path, len(f.cache.sets), i+1)
		}
	}
}

// The first value of a declared key reaches the plugin; undeclared keys and
// later values do not.
func TestPluginPageQueryFirstValue(t *testing.T) {
	f := newRenderFixture(t)
	_, s := bootRoutes(t)
	rt := capaRuntimeWith(t, s, func(m *sdk.Manifest) {
		m.PagePaths, m.PageQuery = []string{"/echo-query"}, []string{"p"}
	})
	f.h.pageFilter = pluginhost.NewPageFilter(rt, s.hooks)
	f.next = membersPage

	w := f.get("/echo-query?p=2&p=3&x=1")
	body := w.Body.String()
	if !strings.Contains(body, "q:p=2") || strings.Contains(body, "q:x") || strings.Contains(body, "=3") {
		t.Fatalf("body:\n%s", body)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q", cc)
	}
}

func TestPageQueryHelper(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x?p=2&p=3&x=1", nil)
	if got := pageQuery(req, nil); got != nil {
		t.Errorf("no keys: %v", got)
	}
	if got := pageQuery(req, []string{"p", "q"}); len(got) != 1 || got["p"] != "2" {
		t.Errorf("got %v, want only p=2", got)
	}
	if got := pageQuery(httptest.NewRequest(http.MethodGet, "/x?x=1", nil), []string{"p"}); len(got) != 0 {
		t.Errorf("absent key: %v", got)
	}
}
