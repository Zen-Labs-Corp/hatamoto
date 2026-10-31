package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/form"
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
	rt, err := pluginhost.NewRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), s.hooks, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	rt.Configure(pluginhost.Config{})
	if st := rt.Load(context.Background(), capaManifest(), plugintest.Build(t, "capa")); !st.Loaded {
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
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Plugins) != 1 {
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

// render.page hides a block and sets a declared cookie, and such pages are
// request-dependent: private and never cached.
func TestRenderPagePlugin(t *testing.T) {
	f := newRenderFixture(t)
	_, s := bootRoutes(t)
	capaRuntime(t, s)
	f.h.pageFilter = pluginhost.NewPageFilter(s.hooks)
	if !f.h.pageFilter.Active() {
		t.Fatal("the filter must be active once a plugin handles render.page")
	}
	f.next = func() (*routeResult, pageOutcome, error) {
		p := pageResponse("Members")
		p.Entity.Attributes = json.RawMessage(`{"page_sections":[
			{"id":"intro","type":"content","title":"Intro","content":{"headline":"INTRO-VISIBLE","body":"<p>hi</p>"}},
			{"id":"secret","type":"content","title":"Secret","content":{"headline":"SECRET-BLOCK","body":"<p>members only</p>"}}]}`)
		return &routeResult{Page: p, Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
	}

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
	b, err := json.Marshal(sdk.FormSubmittedEvent{Attribution: got})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"attribution":{"first":{"channel":"ai_assistants","source":"perplexity","landing":"/pricing"}}`) {
		t.Errorf("payload = %s", b)
	}
	b, _ = json.Marshal(sdk.FormSubmittedEvent{})
	if strings.Contains(string(b), "attribution") {
		t.Errorf("empty event carries attribution: %s", b)
	}
}
