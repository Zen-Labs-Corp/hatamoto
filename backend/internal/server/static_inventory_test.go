package server

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/statichost"
)

// publicWriteRoute reports whether a write route at path is reachable by an
// anonymous visitor. Everything under /api/ except the public, track and auth
// subtrees is behind a token. The auth prefix ends in a slash on purpose, so
// /api/v1/authors is not mistaken for it.
func publicWriteRoute(p string) bool {
	return !strings.HasPrefix(p, "/api/") ||
		strings.HasPrefix(p, "/api/v1/public/") ||
		p == "/api/v1/track" ||
		strings.HasPrefix(p, "/api/v1/auth/")
}

// uncoveredIslandRoutes returns the public CORS or write routes of table that
// the static island inventory does not list (live or stays). A route missing
// from the inventory would silently break, or silently stay open, once the
// site is published as static files.
func uncoveredIslandRoutes(table []httpx.Route, inv []statichost.Island) []string {
	covered := map[string]bool{}
	paths := map[string]bool{}
	for _, it := range inv {
		for _, r := range it.Routes {
			covered[r] = true
			_, p, _ := strings.Cut(r, " ")
			paths[p] = true
		}
	}
	var out []string
	for _, rt := range table {
		method, path, _ := strings.Cut(rt.Pattern, " ")
		if path == "/admin" || strings.HasPrefix(path, "/admin/") || strings.Contains(rt.Handler, "unmatchedHandler") {
			continue
		}
		need := method == http.MethodOptions ||
			(slices.Contains([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}, method) && publicWriteRoute(path)) ||
			strings.HasPrefix(path, "/p/")
		if !need {
			continue
		}
		// An OPTIONS sibling is covered by the route that shares its path.
		if covered[rt.Pattern] || (method == http.MethodOptions && paths[path]) {
			continue
		}
		out = append(out, rt.Pattern)
	}
	return out
}

func TestStaticIslandInventoryComplete(t *testing.T) {
	t.Parallel()
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range uncoveredIslandRoutes(table, statichost.Inventory()) {
		t.Errorf("route %q is public but not in statichost inventory: add it as live or stays", p)
	}
}

func TestStaticIslandInventoryRoutesExist(t *testing.T) {
	t.Parallel()
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, rt := range table {
		have[rt.Pattern] = true
	}
	for _, it := range statichost.Inventory() {
		for _, r := range it.Routes {
			if !have[r] {
				t.Errorf("inventory %q lists %q, which is not in RouteTable()", it.Key, r)
			}
		}
	}
}

func TestStaticIslandInventoryFailsOnNewRoute(t *testing.T) {
	t.Parallel()
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	table = append(table, httpx.Route{Pattern: "POST /new-island"})
	got := uncoveredIslandRoutes(table, statichost.Inventory())
	if len(got) != 1 || got[0] != "POST /new-island" {
		t.Errorf("uncovered = %v, want exactly POST /new-island", got)
	}
}

func TestStaticIslandSearchRow(t *testing.T) {
	t.Parallel()
	for _, it := range statichost.Inventory() {
		if it.Path == "/search" {
			if it.Where != statichost.Live || len(it.Routes) != 0 {
				t.Errorf("search row = %+v, want Live with no routes", it)
			}
			return
		}
	}
	t.Error("no inventory row for /search")
}

// The live 301 sits in the public chain, so this goes through the whole
// dispatcher: pages on L move to S, search and export renders do not, and a
// plugin preflight from S is answered.
func TestStaticIslandDispatcherChain(t *testing.T) {
	t.Parallel()
	_, s, _ := swapServer(t)
	setStaticForTest(s)
	h := s.dispatcher()
	do := func(method, target string, mod func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.Host = "live.example.net"
		if mod != nil {
			mod(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do(http.MethodGet, "/about", nil)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != testStaticSite+"/about" {
		t.Errorf("GET /about on L = %d %q, want 301 to S", rec.Code, rec.Header().Get("Location"))
	}
	rec = do(http.MethodGet, "/about", func(r *http.Request) {
		*r = *r.WithContext(statichost.WithExport(r.Context()))
	})
	if rec.Code == http.StatusMovedPermanently {
		t.Errorf("export request was redirected to %q", rec.Header().Get("Location"))
	}
	if rec := do(http.MethodGet, "/search?q=x", nil); rec.Code == http.StatusMovedPermanently {
		t.Errorf("/search was redirected to %q", rec.Header().Get("Location"))
	}
	rec = do(http.MethodOptions, "/p/x/y", func(r *http.Request) { r.Header.Set("Origin", testStaticSite) })
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite {
		t.Errorf("OPTIONS /p/x/y = %d ACAO %q, want 204 for S", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials must never be allowed")
	}
}

// ProxyPaths is what a same-host web server passes to Hatamoto: every
// inventory island, live or staying, must be covered, or that route would
// answer from the static folder (404) instead.
func TestProxyPathsCoverInventory(t *testing.T) {
	t.Parallel()
	proxied := statichost.ProxyPaths("/admin", "")
	covers := func(p string) bool {
		for _, pp := range proxied {
			if (pp.Prefix && strings.HasPrefix(p, pp.Path)) || (!pp.Prefix && !pp.Locale && p == pp.Path) {
				return true
			}
		}
		return false
	}
	for _, it := range statichost.Inventory() {
		paths := []string{}
		for _, r := range it.Routes {
			_, p, _ := strings.Cut(r, " ")
			paths = append(paths, p)
		}
		if it.Path != "" {
			paths = append(paths, it.Path)
		}
		for _, p := range paths {
			if i := strings.Index(p, "{"); i >= 0 {
				p = p[:i]
			}
			if !covers(p) {
				t.Errorf("island %q path %q is not covered by statichost.ProxyPaths", it.Key, p)
			}
		}
	}
	if want := render.AssetPrefix + render.LiveScriptName; !covers(want) {
		t.Errorf("ProxyPaths misses the comment loader %q", want)
	}
}

// Every top-level prefix Hatamoto reserves is either passed by a same-host web
// server (served from the folder first for media, theme, fonts) or listed here
// as something the export folder holds or an operator probe the web server may
// leave out. A new reserved prefix must be sorted into one of the two lists.
func TestProxyPathsCoverReservedPrefixes(t *testing.T) {
	t.Parallel()
	proxied := statichost.ProxyPaths("/admin", "")
	covered := func(seg string) bool {
		for _, pp := range proxied {
			if pp.Prefix && pp.Path == "/"+seg+"/" {
				return true
			}
		}
		return false
	}
	// .well-known is answered by Hatamoto (never exported), so it is proxied.
	notProxied := map[string]string{
		"sitemap.xml": "exported", "sitemap": "exported", "robots.txt": "exported",
		"llms.txt": "exported", "ads.txt": "exported", "app-ads.txt": "exported", "feed": "exported",
		"feed.xml": "exported", "atom.xml": "exported", "favicon.ico": "exported",
		"health": "operator probe", "ready": "operator probe",
	}
	for _, seg := range adminpath.ReservedPrefixes {
		if _, ok := notProxied[seg]; ok {
			continue
		}
		if !covered(seg) {
			t.Errorf("reserved prefix /%s/ is neither in statichost.ProxyPaths nor in the not-proxied list", seg)
		}
	}
	for _, seg := range []string{"media", "theme", "fonts"} {
		fallback := false
		for _, pp := range proxied {
			fallback = fallback || pp.Prefix && pp.Fallback && pp.Path == "/"+seg+"/"
		}
		if !fallback {
			t.Errorf("/%s/ must be served from the folder with a fall back to Hatamoto", seg)
		}
	}
}

func TestStaticLivePathUsesLiveSegments(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/forms/x", "/-/comments/form/x", "/p/plug/x", "/preview/t", "/mcp", "/search", "/fr/search"} {
		if !staticLivePath(p, "") {
			t.Errorf("%q must be a live path", p)
		}
	}
	if staticLivePath("/about", "") || !staticLivePath("/hm-api/x", "/hm-api") {
		t.Error("static page or API prefix misclassified")
	}
}
