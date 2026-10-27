package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/render"
)

// directoryServer is a Server whose plugin directory reads the rig's registry.
func directoryServer(r *dirRig) *Server {
	s := &Server{}
	s.directoryOnce.Do(func() { s.directory = r.dir })
	return s
}

// withDirectory makes the fixture's views carry sections and run the same
// directory wiring buildPageView does.
func withDirectory(f *renderFixture, s *Server, sections []render.Section, doc *builder.Doc) {
	f.h.buildView = func(ctx context.Context, r *http.Request, rr *routeResult) (*render.PageView, error) {
		v := pageViewFromRoute(rr, "https://x.io", requestPath(r))
		v.Site = render.SiteView{Name: "Fixture Site"}
		v.Sections, v.Doc = sections, doc
		v.Uncacheable = new(bool)
		s.applyPluginDirectory(ctx, v)
		return v, nil
	}
}

var directorySection = []render.Section{{ID: "s1", Type: "plugin_directory", Content: map[string]any{"heading": "Plugins"}}}

func TestPluginDirectoryPageCacheCap(t *testing.T) {
	rig := newDirRig(t)
	f := newRenderFixture(t)
	f.next = okPage("Plugins")
	f.h.cacheTTL = 30 * 24 * time.Hour
	withDirectory(f, directoryServer(rig), directorySection, nil)

	w := f.get("/plugins")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Zeta Tool") {
		t.Fatalf("status %d, body lacks the card", w.Code)
	}
	if len(f.cache.ttls) != 1 || f.cache.ttls[0] != pluginDirectoryCacheAge {
		t.Fatalf("stored with TTL %v, want %s despite the 30d setting", f.cache.ttls, pluginDirectoryCacheAge)
	}
}

func TestPluginDirectoryUnavailableIsServedNotCached(t *testing.T) {
	rig := newDirRig(t)
	rig.fail()
	f := newRenderFixture(t)
	f.next = okPage("Plugins")
	withDirectory(f, directoryServer(rig), directorySection, nil)

	w := f.get("/plugins")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "The plugin directory is unavailable right now.") {
		t.Fatalf("status %d, want 200 with the empty state", w.Code)
	}
	if len(f.cache.sets) != 0 {
		t.Fatalf("unavailable page was cached: %v", f.cache.sets)
	}
	if cc := w.Header().Get("Cache-Control"); strings.Contains(cc, "public") {
		t.Errorf("Cache-Control %q must not be shared-cacheable", cc)
	}
}

func TestPluginDirectoryAbsentKeepsConfiguredTTL(t *testing.T) {
	rig := newDirRig(t)
	f := newRenderFixture(t)
	f.next = okPage("About")
	f.h.cacheTTL = 6 * time.Hour
	withDirectory(f, directoryServer(rig), []render.Section{{ID: "s1", Type: "hero"}}, nil)

	f.get("/about")
	if len(f.cache.ttls) != 1 || f.cache.ttls[0] != 6*time.Hour {
		t.Fatalf("TTL %v, want 6h", f.cache.ttls)
	}
	if rig.hits.Load() != 0 {
		t.Error("a page without the section fetched the registry")
	}
}

func TestPluginDirectoryDetectedInDocComponent(t *testing.T) {
	rig := newDirRig(t)
	s := directoryServer(rig)
	doc := builder.FromSections([]builder.Section{{ID: "d1", Type: "plugin_directory", Content: map[string]any{}}})
	if pd := s.pluginDirectoryFor(context.Background(), nil, doc); pd == nil || pd.State != render.PluginDirectoryOK {
		t.Fatalf("doc component not detected: %+v", pd)
	}
	if pd := s.pluginDirectoryFor(context.Background(), directorySection, nil); pd == nil {
		t.Fatal("legacy section not detected")
	}
	if pd := s.pluginDirectoryFor(context.Background(), []render.Section{{Type: "hero"}}, nil); pd != nil {
		t.Fatal("hero section detected as a directory")
	}
}

// The shared-cache lifetime of a directory page is no longer than the page
// cap, on a fresh render and on a page-cache hit; other pages keep s-maxage=300.
func TestPluginDirectorySharedCacheLifetime(t *testing.T) {
	rig := newDirRig(t)
	f := newRenderFixture(t)
	f.next = okPage("Plugins")
	withDirectory(f, directoryServer(rig), directorySection, nil)

	if cc := f.get("/plugins").Header().Get("Cache-Control"); cc != "public, max-age=60, s-maxage=60" {
		t.Errorf("fresh render Cache-Control = %q", cc)
	}
	if cc := f.get("/plugins").Header().Get("Cache-Control"); cc != "public, max-age=60, s-maxage=60" {
		t.Errorf("cache hit Cache-Control = %q", cc)
	}

	other := newRenderFixture(t)
	other.next = okPage("About")
	if cc := other.get("/about").Header().Get("Cache-Control"); cc != cachePublic {
		t.Errorf("other page Cache-Control = %q, want %q", cc, cachePublic)
	}
}

// With no registry configured the page says so, is cacheable and is not
// counted as degraded; a failing registry stays uncacheable.
func TestPluginDirectoryRegistryOffIsNeutralAndCacheable(t *testing.T) {
	rig := newDirRig(t)
	rig.dir.registry = func() *packages.Registry { return &packages.Registry{URL: "off"} }
	f := newRenderFixture(t)
	f.next = okPage("Plugins")
	withDirectory(f, directoryServer(rig), directorySection, nil)

	w := f.get("/plugins")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No plugin registry is configured.") {
		t.Fatalf("status %d, body lacks the off state", w.Code)
	}
	if strings.Contains(w.Body.String(), "unavailable") {
		t.Error("off state reads as a failure")
	}
	if len(f.cache.sets) != 1 {
		t.Errorf("off page cached %d times, want 1", len(f.cache.sets))
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "public") {
		t.Errorf("Cache-Control %q must be cacheable", cc)
	}
}

// hatamotoPluginsDoc is shaped like the live hatamoto.dev/plugins page: a
// native (not v1-shaped) container holding one plugin_directory component, so
// the section partial and its data-hm-type marker are bypassed.
func hatamotoPluginsDoc() *builder.Doc {
	return &builder.Doc{V: 2, Nodes: []*builder.Node{{
		ID: "plugins-wrap", T: "container", P: map[string]any{"tag": "div"},
		C: []*builder.Node{{ID: "plugin-directory", T: "component", P: map[string]any{
			"type":    "plugin_directory",
			"content": map[string]any{"eyebrow": "Plugins", "heading": "Plugin directory"},
		}}},
	}}}
}

// A page_doc v2 directory page carries no section marker, yet must still get
// the directory shared-cache lifetime, fresh and from the page cache.
func TestPluginDirectoryBuilderPageSharedCacheLifetime(t *testing.T) {
	rig := newDirRig(t)
	f := newRenderFixture(t)
	f.next = okPage("Plugins")
	withDirectory(f, directoryServer(rig), nil, hatamotoPluginsDoc())

	for _, pass := range []string{"fresh render", "page-cache hit"} {
		w := f.get("/plugins")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Zeta Tool") {
			t.Fatalf("%s: status %d, body lacks the card", pass, w.Code)
		}
		if strings.Contains(w.Body.String(), `data-hm-type="plugin_directory"`) {
			t.Fatalf("%s: builder output unexpectedly carries a section marker", pass)
		}
		if !strings.Contains(w.Body.String(), "data-hm-directory") {
			t.Errorf("%s: template did not emit data-hm-directory", pass)
		}
		if cc := w.Header().Get("Cache-Control"); cc != cachePublicDirectory {
			t.Errorf("%s: Cache-Control = %q, want %q", pass, cc, cachePublicDirectory)
		}
	}
	if len(f.cache.sets) != 1 {
		t.Errorf("page cached %d times, want 1 (second request must be a hit)", len(f.cache.sets))
	}
}
