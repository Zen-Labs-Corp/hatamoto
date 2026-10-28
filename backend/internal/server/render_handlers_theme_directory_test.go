package server

import (
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/render"
)

var themeDirectorySection = []render.Section{{ID: "t1", Type: "theme_directory", Content: map[string]any{}}}

func TestThemeDirectoryWiring(t *testing.T) {
	rig := newDirRig(t)
	rig.publish(24*time.Hour,
		packages.Entry{Kind: packages.KindPlugin, Name: "zeta", Version: "1.0.0"},
		packages.Entry{Kind: packages.KindTheme, Name: "zen", Version: "1.0.0"})
	s := directoryServer(rig)
	ctx := context.Background()

	v := &render.PageView{Sections: themeDirectorySection, Uncacheable: new(bool)}
	s.applyPluginDirectory(ctx, v)
	if v.PluginDirectory != nil || v.ThemeDirectory == nil || len(v.ThemeDirectory.Cards) != 1 || v.ThemeDirectory.Cards[0].Name != "zen" {
		t.Fatalf("theme-only page: plugins=%+v themes=%+v", v.PluginDirectory, v.ThemeDirectory)
	}
	if v.MaxCacheAge != pluginDirectoryCacheAge || *v.Uncacheable {
		t.Errorf("MaxCacheAge %v, Uncacheable %v", v.MaxCacheAge, *v.Uncacheable)
	}

	// Both sections (one in the doc) cost one fetch.
	rig.hits.Store(0)
	rig.advance(directoryTTL + time.Second)
	doc := builder.FromSections([]builder.Section{{ID: "d1", Type: "plugin_directory", Content: map[string]any{}}})
	v = &render.PageView{Sections: themeDirectorySection, Doc: doc, Uncacheable: new(bool)}
	s.applyPluginDirectory(ctx, v)
	if v.PluginDirectory == nil || v.ThemeDirectory == nil || v.PluginDirectory.Cards[0].Name != "zeta" {
		t.Fatalf("both: %+v %+v", v.PluginDirectory, v.ThemeDirectory)
	}
	if n := rig.hits.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}

	// No directory section: no fetch, no views.
	rig.hits.Store(0)
	rig.advance(directoryTTL + time.Second)
	v = &render.PageView{Sections: []render.Section{{Type: "hero"}}, Uncacheable: new(bool)}
	s.applyPluginDirectory(ctx, v)
	if v.ThemeDirectory != nil || v.PluginDirectory != nil || v.MaxCacheAge != 0 || rig.hits.Load() != 0 {
		t.Fatalf("hero page touched the directory: hits=%d", rig.hits.Load())
	}
}

func TestThemeDirectoryUnavailableIsUncacheable(t *testing.T) {
	rig := newDirRig(t)
	rig.fail()
	v := &render.PageView{Sections: themeDirectorySection, Uncacheable: new(bool)}
	directoryServer(rig).applyPluginDirectory(context.Background(), v)
	if v.ThemeDirectory == nil || v.ThemeDirectory.State != render.PluginDirectoryUnavailable || !*v.Uncacheable {
		t.Fatalf("themes=%+v uncacheable=%v", v.ThemeDirectory, *v.Uncacheable)
	}
}

func TestThemeDirectoryDocComponentDetected(t *testing.T) {
	s := directoryServer(newDirRig(t))
	doc := builder.FromSections([]builder.Section{{ID: "d1", Type: "theme_directory", Content: map[string]any{}}})
	pd, td := s.directoriesFor(context.Background(), nil, doc)
	if pd != nil || td == nil {
		t.Fatalf("pd=%+v td=%+v", pd, td)
	}
}

// Card titles link to the detail pages only on the page that serves them:
// the plugin directory on /plugins, the theme directory on /themes.
func TestDirectoryLinkedOnlyOnServingPath(t *testing.T) {
	rig := newDirRig(t)
	rig.publish(24*time.Hour,
		packages.Entry{Kind: packages.KindPlugin, Name: "zeta", Version: "1.0.0"},
		packages.Entry{Kind: packages.KindTheme, Name: "zen", Version: "1.0.0"})
	s := directoryServer(rig)
	both := []render.Section{{ID: "a", Type: "plugin_directory"}, {ID: "b", Type: "theme_directory"}}
	for _, c := range []struct {
		path            string
		plugins, themes bool
	}{{"/plugins", true, false}, {"/themes", false, true}, {"/extensions", false, false}, {"/", false, false}} {
		v := &render.PageView{Path: c.path, Sections: both, Uncacheable: new(bool)}
		s.applyPluginDirectory(context.Background(), v)
		if v.PluginDirectory.Linked != c.plugins || v.ThemeDirectory.Linked != c.themes {
			t.Errorf("%s: plugins linked=%v themes linked=%v, want %v %v", c.path, v.PluginDirectory.Linked, v.ThemeDirectory.Linked, c.plugins, c.themes)
		}
	}
}
