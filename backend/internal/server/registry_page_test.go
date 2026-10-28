package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/render"
)

// registryRig serves /plugins and /themes pages with the given sections, a CMS
// map for exact paths, and the registry rig's index. It mirrors the order of
// resolveForRender: CMS page, then the registry page.
type registryRig struct {
	f        *renderFixture
	dir      *dirRig
	s        *Server
	cms      map[string]*routeResult
	listing  map[string]string // list path -> directory section type, "" for a page without one
	hostHits int
	hidden   bool // the listing page's directory section is not visible
}

func hostPage(sectionType string) *routeResult {
	attrs := `{"page_sections":[]}`
	if sectionType != "" {
		attrs = `{"page_sections":[{"id":"s1","type":"` + sectionType + `","content":{}}]}`
	}
	return &routeResult{Kind: resolveKindEntity, Page: &PublicPageResponse{
		Entity: &entity.Entity{ID: uuid.New(), Name: "Host", Attributes: json.RawMessage(attrs)},
		Meta:   PageMeta{Title: "Host"},
	}}
}

func newRegistryRig(t *testing.T, entries ...packages.Entry) *registryRig {
	t.Helper()
	d := newDirRig(t)
	if len(entries) > 0 {
		d.publish(24*time.Hour, entries...)
	}
	g := &registryRig{f: newRenderFixture(t), dir: d, s: directoryServer(d), cms: map[string]*routeResult{},
		listing: map[string]string{"/plugins": "plugin_directory", "/themes": "theme_directory"}}
	g.f.h.resolve = func(ctx context.Context, path string, _ *http.Request) (*routeResult, pageOutcome, error) {
		if rr, ok := g.cms[path]; ok {
			return rr, pageOutcome{Status: 200}, nil
		}
		reg, err := g.s.resolveRegistryWith(ctx, path, func(listPath string) (*routeResult, pageOutcome, error) {
			g.hostHits++
			sec, ok := g.listing[listPath]
			if !ok {
				return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
			}
			rr := hostPage(sec)
			if g.hidden {
				rr.Page.Entity.Attributes = json.RawMessage(`{"page_sections":[{"id":"s1","type":"` + sec + `","isVisible":false,"content":{}}]}`)
			}
			return rr, pageOutcome{Status: 200}, nil
		})
		if err != nil || reg == nil {
			return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, err
		}
		return &routeResult{Registry: reg}, pageOutcome{Status: 200}, nil
	}
	g.f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v := pageViewFromRoute(rr, "https://example.test", requestPath(r))
		v.Site = render.SiteView{Name: "Fixture Site"}
		v.Uncacheable = new(bool)
		if rr.Registry != nil { // the other kinds need the database
			if err := g.s.fillView(ctx, r, rr, v); err != nil {
				return nil, err
			}
		}
		return v, nil
	}
	return g
}

var registryEntries = []packages.Entry{
	{Kind: packages.KindPlugin, Name: "tagger", Version: "0.2.0", Title: "Tagger", Description: "Tags every page.", Tier: packages.TierFirstParty,
		Source: "https://example.test/tagger", Permissions: []string{"settings", "site.scripts"},
		About: "First paragraph.\nSecond <b>paragraph</b>.", Features: []string{"Fast", "Small"}},
	{Kind: packages.KindTheme, Name: "boreal", Version: "1.0.0", Description: "A calm theme.", Tier: packages.TierCommunity, Author: "ada", Source: "https://example.test/boreal"},
}

func TestRegistryPluginPage(t *testing.T) {
	g := newRegistryRig(t, registryEntries...)
	w := g.f.get("/plugins/tagger")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"<title>Tagger plugin", `name="description" content="Tags every page."`, `rel="canonical" href="https://example.test/plugins/tagger"`,
		"BreadcrumbList", `data-hm-directory`, "Fixture Site", "v0.2.0", "First-party", `href="https://example.test/tagger"`,
		"Store its own settings", "Warning:", "Permissions (2)", "<p>First paragraph.</p>", "&lt;b&gt;paragraph&lt;/b&gt;", "<li>Fast</li>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	if strings.Contains(body, "<dt>Author</dt>") {
		t.Error("first-party entry shows an Author row")
	}
	if cc := w.Header().Get("Cache-Control"); cc != cachePublicDirectory {
		t.Errorf("Cache-Control = %q, want %q", cc, cachePublicDirectory)
	}
	if len(g.f.cache.ttls) != 1 || g.f.cache.ttls[0] != pluginDirectoryCacheAge {
		t.Errorf("cache TTLs %v, want [%s]", g.f.cache.ttls, pluginDirectoryCacheAge)
	}
}

func TestRegistryThemePage(t *testing.T) {
	g := newRegistryRig(t, registryEntries...)
	w := g.f.get("/themes/boreal")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "<title>boreal theme") || !strings.Contains(body, "<dt>Author</dt><dd>ada</dd>") {
		t.Fatalf("status %d, body:\n%s", w.Code, body)
	}
	if strings.Contains(body, "Permissions") {
		t.Error("theme page shows a Permissions box")
	}
	if g.f.get("/plugins/boreal").Code != 404 || g.f.get("/themes/tagger").Code != 404 {
		t.Error("an entry is served under the wrong kind")
	}
}

func TestRegistryPageNotFound(t *testing.T) {
	cases := map[string]func(g *registryRig){
		"no host page":            func(g *registryRig) { delete(g.listing, "/plugins") },
		"host page lacks section": func(g *registryRig) { g.listing["/plugins"] = "" },
		"host has the other kind": func(g *registryRig) { g.listing["/plugins"] = "theme_directory" },
		"hidden section":          func(g *registryRig) { g.hidden = true },
		"unknown name":            func(g *registryRig) {},
		"registry failing":        func(g *registryRig) { g.dir.fail() },
		"registry off": func(g *registryRig) {
			g.dir.dir.registry = func() *packages.Registry { return &packages.Registry{URL: "off"} }
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRegistryRig(t, registryEntries...)
			setup(g)
			path := "/plugins/tagger"
			if name == "unknown name" {
				path = "/plugins/nope"
			}
			w := g.f.get(path)
			if w.Code != 404 {
				t.Fatalf("status %d, want 404", w.Code)
			}
			if cc := w.Header().Get("Cache-Control"); strings.Contains(cc, "public") {
				t.Errorf("404 Cache-Control %q is shared-cacheable", cc)
			}
			if len(g.f.cache.sets) != 0 {
				t.Error("a 404 was page-cached")
			}
		})
	}
	t.Run("empty index", func(t *testing.T) {
		g := newRegistryRig(t)
		g.dir.publish(time.Hour)
		if w := g.f.get("/plugins/tagger"); w.Code != 404 {
			t.Fatalf("status %d, want 404", w.Code)
		}
	})
}

// A name that cannot be a registry name is refused before any page lookup.
func TestRegistryPageBadNameSkipsLookup(t *testing.T) {
	g := newRegistryRig(t, registryEntries...)
	long := "/plugins/a" + strings.Repeat("b", 40)
	for _, p := range []string{long, "/plugins/a", "/plugins/9x", "/plugins/Tagger", "/plugins/-x", "/plugins/a_b", "/plugins/a/b", "/plugins", "/other/tagger"} {
		if w := g.f.get(p); w.Code != 404 {
			t.Errorf("%s: status %d, want 404", p, w.Code)
		}
	}
	if g.hostHits != 0 {
		t.Errorf("host page looked up %d times for bad names", g.hostHits)
	}
}

// A published CMS page at the exact path wins, also after the registry page
// was cached (a content change purges every cached page).
func TestRegistryPageCMSPageWins(t *testing.T) {
	g := newRegistryRig(t, registryEntries...)
	if w := g.f.get("/plugins/tagger"); w.Code != 200 || !strings.Contains(w.Body.String(), "Tagger") {
		t.Fatalf("registry page: %d", w.Code)
	}
	if len(g.f.cache.sets) != 1 {
		t.Fatalf("registry page cached %d times, want 1", len(g.f.cache.sets))
	}
	g.f.cache.data, g.f.cache.gen = nil, g.f.cache.gen+1 // what invalidateRendered does
	g.cms["/plugins/tagger"] = &routeResult{Page: pageResponse("Own page"), Kind: resolveKindEntity}
	w := g.f.get("/plugins/tagger")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Own page") || strings.Contains(w.Body.String(), "hm-registry") {
		t.Fatalf("CMS page did not win: %d\n%s", w.Code, w.Body.String())
	}
}

// With the directory warm, an unknown name is refused before the host page
// is loaded; a known name still resolves through it.
func TestRegistryPageUnknownNameSkipsPageLoad(t *testing.T) {
	g := newRegistryRig(t, registryEntries...)
	g.s.pluginDirectory().Views(context.Background())
	if w := g.f.get("/plugins/nope"); w.Code != 404 {
		t.Fatalf("status %d, want 404", w.Code)
	}
	if g.hostHits != 0 {
		t.Errorf("host page loaded %d times for an unknown name", g.hostHits)
	}
	if w := g.f.get("/plugins/tagger"); w.Code != 200 || g.hostHits != 1 {
		t.Errorf("known name: status %d, host loads %d", w.Code, g.hostHits)
	}
}
