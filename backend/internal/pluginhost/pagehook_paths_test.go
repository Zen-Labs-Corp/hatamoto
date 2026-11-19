package pluginhost

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

func pathsBlocks() []sdk.Block {
	return []sdk.Block{{ID: "intro", Type: "hero", Title: "Hi"}, {ID: "secret", Type: "text", Title: "Members only"}}
}

// applyAt runs the whole render.page chain on the engine for path.
func applyAt(r *capaRig, path string) sdk.PageContext {
	out, _ := r.engine.Apply(context.Background(), sdk.RenderPage.Name, sdk.PageContext{Path: path, Blocks: pathsBlocks()}).(sdk.PageContext)
	return out
}

func TestRenderPageDeclaredPathsOnly(t *testing.T) {
	r := newCapaRig(t)
	r.loadAs(t, "capa", func(m *sdk.Manifest) { m.PagePaths = []string{"/members"} })
	pf := NewPageFilter(r.rt, r.engine)
	if !pf.Applies("/members") || pf.Applies("/about") {
		t.Fatalf("Applies(/members)=%v Applies(/about)=%v", pf.Applies("/members"), pf.Applies("/about"))
	}
	out := pf.Apply(context.Background(), sdk.PageContext{Path: "/members", Blocks: pathsBlocks()})
	if len(out.Blocks) != 1 || len(out.SetCookies) != 1 {
		t.Fatalf("declared path: blocks=%+v cookies=%+v", out.Blocks, out.SetCookies)
	}
	// The chain runs through the adapter even when another plugin makes the
	// page filtered; the undeclaring adapter must skip the call.
	out = applyAt(r, "/about")
	if len(out.Blocks) != 2 || len(out.SetCookies) != 0 {
		t.Fatalf("undeclared path: the plugin was called: blocks=%+v cookies=%+v", out.Blocks, out.SetCookies)
	}
}

func TestRenderPageUndeclaredRunsEverywhere(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	pf := NewPageFilter(r.rt, r.engine)
	for _, path := range []string{"/", "/about", "/members/x"} {
		if !pf.Applies(path) {
			t.Errorf("Applies(%q) = false", path)
		}
		out := pf.Apply(context.Background(), sdk.PageContext{Path: path, Blocks: pathsBlocks()})
		if len(out.Blocks) != 1 || len(out.SetCookies) != 1 {
			t.Errorf("%s: blocks=%+v cookies=%+v", path, out.Blocks, out.SetCookies)
		}
	}
}

func TestRenderPageMixed(t *testing.T) {
	r := newCapaRig(t)
	r.loadAs(t, "capa", func(m *sdk.Manifest) { m.PagePaths = []string{"/members"} })
	r.loadAs(t, "capa2", nil)
	if !NewPageFilter(r.rt, r.engine).Applies("/about") {
		t.Fatal("a site-wide plugin must make every page filtered")
	}
	out := applyAt(r, "/about")
	if len(out.SetCookies) != 1 || out.SetCookies[0].Name != "hmp_capa2_visited" {
		t.Fatalf("cookies = %+v, want only the site-wide plugin's", out.SetCookies)
	}
}

func TestPageFilterUnloadRestores(t *testing.T) {
	r := newCapaRig(t)
	pf := NewPageFilter(r.rt, r.engine)
	r.load(t, func(m *sdk.Manifest) { m.PagePaths = []string{"/members"} })
	if !pf.Applies("/members") {
		t.Fatal("declared path not filtered")
	}
	r.rt.Unload(context.Background(), "capa")
	if pf.Applies("/members") {
		t.Error("declared path still filtered after unload")
	}
	r.load(t, nil)
	if !pf.Applies("/x") {
		t.Fatal("site-wide plugin does not filter")
	}
	r.rt.Unload(context.Background(), "capa")
	if pf.Applies("/x") {
		t.Error("page still filtered after the site-wide plugin unloaded")
	}
}

func TestPageFilterUnknownFilterIsSiteWide(t *testing.T) {
	spy := func(_ context.Context, p sdk.PageContext) (sdk.PageContext, error) { return p, nil }
	r := newCapaRig(t)
	r.load(t, func(m *sdk.Manifest) { m.PagePaths = []string{"/members"} })
	hooks.OnFilter(r.engine.For("spy"), sdk.RenderPage, 10, spy)
	if !NewPageFilter(r.rt, r.engine).Applies("/about") {
		t.Error("a filter the runtime has no manifest for must count as site-wide")
	}

	engine := hooks.New(r.rt.log)
	hooks.OnFilter(engine.For("spy"), sdk.RenderPage, 10, spy)
	if !NewPageFilter(nil, engine).Applies("/about") {
		t.Error("engine-only filter must count as site-wide")
	}
}

func TestPageFilterNilSafe(t *testing.T) {
	var p *PageFilter
	if p.Applies("/") {
		t.Error("nil filter applies")
	}
	if NewPageFilter(nil, nil) != nil {
		t.Error("nil engine must give a nil filter")
	}
}
