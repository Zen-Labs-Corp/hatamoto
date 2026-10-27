package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginCacheDir(t *testing.T) {
	for _, tc := range []struct{ data, cache, want string }{
		{"", "", filepath.Join("data", "cache", "wasm")},
		{"/srv/hm", "", "/srv/hm/cache/wasm"},
		{"/srv/hm", "/tmp/c", "/tmp/c"},
		{"/srv/hm", "off", ""},
	} {
		t.Setenv("HATAMOTO_DATA", tc.data)
		t.Setenv("HATAMOTO_PLUGIN_CACHE_DIR", tc.cache)
		if got := LoadPluginConfig().CacheDir; filepath.Clean(got) != filepath.Clean(tc.want) {
			t.Errorf("HATAMOTO_DATA=%q HATAMOTO_PLUGIN_CACHE_DIR=%q: %q, want %q", tc.data, tc.cache, got, tc.want)
		}
	}
}

func TestResolveDataDir(t *testing.T) {
	t.Setenv("HATAMOTO_DATA", "")
	got := ResolveDataDir()
	if !filepath.IsAbs(got) || filepath.Base(got) != "data" {
		t.Errorf("default = %q, want absolute .../data", got)
	}
	t.Setenv("HATAMOTO_DATA", "rel/dir")
	got = ResolveDataDir()
	if !filepath.IsAbs(got) || !strings.HasSuffix(got, filepath.Join("rel", "dir")) {
		t.Errorf("relative env = %q, want absolute", got)
	}
	abs := t.TempDir()
	t.Setenv("HATAMOTO_DATA", " "+abs+" ")
	if got := ResolveDataDir(); got != abs {
		t.Errorf("absolute env = %q, want %q", got, abs)
	}
}
