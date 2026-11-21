package statichost

import (
	"slices"
	"strings"
	"testing"
)

func TestProxyPaths(t *testing.T) {
	t.Parallel()
	got := ProxyPaths("/admin", "/hm-api")
	want := []ProxyPath{
		{Path: "/-/", Prefix: true}, {Path: "/.well-known/", Prefix: true, Fallback: true}, {Path: "/admin"}, {Path: "/admin/", Prefix: true},
		{Path: "/api/", Prefix: true}, {Path: "/auth/", Prefix: true}, {Path: "/f/", Prefix: true},
		{Path: "/fonts/", Prefix: true, Fallback: true}, {Path: "/forms/", Prefix: true},
		{Path: "/hm-api"}, {Path: "/hm-api/", Prefix: true}, {Path: "/mcp"}, {Path: "/mcp/", Prefix: true}, {Path: "/media/", Prefix: true, Fallback: true}, {Path: "/p/", Prefix: true},
		{Path: "/plugin-ui/", Prefix: true}, {Path: "/preview/", Prefix: true},
		{Path: "/search"}, {Path: "/search", Locale: true}, {Path: "/theme/", Prefix: true, Fallback: true},
		{Path: "/theme/assets/hm-live.js"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ProxyPaths =\n%v\nwant\n%v", got, want)
	}
	if again := ProxyPaths("/admin", "/hm-api"); !slices.Equal(got, again) {
		t.Error("ProxyPaths is not deterministic")
	}
}

func TestProxyPathsEmptyAndDeduped(t *testing.T) {
	t.Parallel()
	base := ProxyPaths("", "")
	for _, p := range base {
		if p.Path == "" || p.Path == "/" || strings.Contains(p.Path, "admin") {
			t.Errorf("unexpected path %+v", p)
		}
	}
	// A trailing slash and a doubled argument add each path once.
	if got := ProxyPaths("/admin/", "/admin"); len(got) != len(base)+2 {
		t.Errorf("len = %d, want %d", len(got), len(base)+2)
	}
	if got := ProxyPaths("admin", " "); len(got) != len(base) {
		t.Errorf("a path without a leading slash must be ignored: %v", got)
	}
}

func TestLiveSegment(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"forms", "-", "f", "p", "api", "preview", "auth", "mcp", "plugin-ui"} {
		if !LiveSegment(s) {
			t.Errorf("%q must be live", s)
		}
	}
	for _, s := range []string{"", "blog", "search", "admin"} {
		if LiveSegment(s) {
			t.Errorf("%q must not be live", s)
		}
	}
}
