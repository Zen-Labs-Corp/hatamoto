package pluginhost

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

func (r *capaRig) loadAs(t *testing.T, name string, mutate func(*sdk.Manifest)) {
	t.Helper()
	r.load(t, func(m *sdk.Manifest) {
		m.Name = name
		if mutate != nil {
			mutate(m)
		}
	})
}

func TestPagePathsReservedRefusedAtLoad(t *testing.T) {
	r := newCapaRig(t)
	m := capaManifest(func(m *sdk.Manifest) { m.PagePaths = []string{"/members", "/admin"} })
	st := r.rt.Load(context.Background(), m, plugintest.Build(t, "capa"))
	if st.Loaded || !strings.Contains(st.Error, `"/admin"`) {
		t.Fatalf("loaded=%v error=%q", st.Loaded, st.Error)
	}
	if idx := r.rt.pageIdx.Load(); idx != nil && len(idx.byPlugin) != 0 {
		t.Errorf("a refused plugin is indexed: %v", idx.byPlugin)
	}
}

func TestPagePathsIndex(t *testing.T) {
	r := newCapaRig(t)
	r.loadAs(t, "capa", func(m *sdk.Manifest) { m.PagePaths = []string{"/members"} })
	idx := r.rt.pageIdx.Load()
	if idx == nil || len(idx.byPlugin["capa"].paths) != 1 || idx.siteWide != 0 || idx.union.paths["/members"] != 1 {
		t.Fatalf("index = %+v", idx)
	}
	for _, c := range []struct {
		path string
		n    int
		want bool
	}{{"/members", 1, true}, {"/about", 1, false}, {"/about", 2, true}, {"/about", 0, false}} {
		if got := r.rt.pageFiltered(c.path, c.n); got != c.want {
			t.Errorf("pageFiltered(%q, %d) = %v, want %v", c.path, c.n, got, c.want)
		}
	}

	r.loadAs(t, "capa2", func(m *sdk.Manifest) { m.PagePaths = []string{"/members"} })
	if got := r.rt.pageIdx.Load().union.paths["/members"]; got != 2 {
		t.Fatalf("shared path count = %d, want 2", got)
	}
	r.rt.Unload(context.Background(), "capa")
	if got := r.rt.pageIdx.Load().union.paths["/members"]; got != 1 {
		t.Fatalf("after unload = %d, want 1", got)
	}
	r.rt.Unload(context.Background(), "capa2")
	if idx := r.rt.pageIdx.Load(); len(idx.byPlugin) != 0 || len(idx.union.paths) != 0 {
		t.Fatalf("index not empty: %+v", idx)
	}
	if r.rt.pageFiltered("/members", 0) {
		t.Error("no filters registered but page is filtered")
	}
}

func TestPagePathsSiteWide(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	if idx := r.rt.pageIdx.Load(); idx == nil || idx.siteWide != 1 {
		t.Fatalf("index = %+v", idx)
	}
	if !r.rt.pageFiltered("/anything", 1) {
		t.Error("a plugin without page_paths must filter every page")
	}
	r.rt.Unload(context.Background(), "capa")
	if idx := r.rt.pageIdx.Load(); idx.siteWide != 0 {
		t.Fatalf("siteWide after unload = %d", idx.siteWide)
	}
}

func TestPagePathsRefusal(t *testing.T) {
	if err := PagePathsRefusal(sdk.Manifest{Name: "pager", PagePaths: []string{"/", "/members"}}); err != nil {
		t.Errorf("clean paths refused: %v", err)
	}
	for _, p := range []string{"/api/x", "/robots.txt", "/members/"} {
		if err := PagePathsRefusal(sdk.Manifest{Name: "pager", PagePaths: []string{p}}); err == nil {
			t.Errorf("%q not refused", p)
		}
	}
}
