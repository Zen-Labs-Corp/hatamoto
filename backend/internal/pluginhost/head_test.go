package pluginhost

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHeadFilterCarriesReplaceSiteGraph(t *testing.T) {
	h := hooks.New(quietLog())
	hooks.OnFilter(h.For("pl19r"), sdk.RenderHead, 10, func(_ context.Context, c sdk.HeadContext) (sdk.HeadContext, error) {
		c.ReplaceSiteGraph = true
		return c, nil
	})
	out := HeadFilter(h)(context.Background(), render.HeadContext{Path: "/"})
	if !out.ReplaceSiteGraph {
		t.Fatal("adapter dropped ReplaceSiteGraph")
	}
	h2 := hooks.New(quietLog())
	if HeadFilter(h2)(context.Background(), render.HeadContext{Path: "/"}).ReplaceSiteGraph {
		t.Fatal("flag must stay false when no filter sets it")
	}
}

func TestHeadScriptSrcNeedsSiteScripts(t *testing.T) {
	rt, _, _ := newRuntime(t)
	add := func(_ context.Context, v any) (any, error) {
		c := v.(sdk.HeadContext)
		c.Tags = append(c.Tags,
			sdk.HeadTag{Kind: "script-src", Attrs: map[string]string{"src": "/p/gtm/assets/plugin.js"}},
			sdk.HeadTag{Kind: "meta", Attrs: map[string]string{"name": "robots", "content": "all"}})
		return c, nil
	}
	f := rt.headScriptGate("gtm", add)
	kinds := func() []string {
		out, err := f(context.Background(), sdk.HeadContext{})
		if err != nil {
			t.Fatal(err)
		}
		var ks []string
		for _, tg := range out.(sdk.HeadContext).Tags {
			ks = append(ks, tg.Kind)
		}
		return ks
	}
	if got := kinds(); len(got) != 1 || got[0] != "meta" {
		t.Fatalf("without the grant want only meta, got %v", got)
	}
	if !rt.ScriptsDropped("gtm") {
		t.Error("the dropped script was not noted for the admin banner")
	}
	rt.Unload(context.Background(), "gtm")
	if rt.ScriptsDropped("gtm") {
		t.Error("the dropped-script note outlived the plugin's removal")
	}
	if err := rt.Grants().Set(context.Background(), "gtm", []sdk.Permission{sdk.PermSiteScripts}); err != nil {
		t.Fatal(err)
	}
	// A grant row alone does not count: the manifest must still request it.
	rt.record(Status{Name: "gtm", Loaded: true, Manifest: sdk.Manifest{Name: "gtm"}})
	if got := kinds(); len(got) != 1 {
		t.Fatalf("a stale grant (manifest no longer asks) must not count, got %v", got)
	}
	rt.record(Status{Name: "gtm", Loaded: true, Manifest: sdk.Manifest{Name: "gtm", Permissions: []sdk.Permission{sdk.PermSiteScripts}}})
	if got := kinds(); len(got) != 2 {
		t.Fatalf("with the grant want both tags, got %v", got)
	}
}

func TestHeadScriptsDenyHook(t *testing.T) {
	rt, _, _ := newRuntime(t)
	if err := rt.Grants().Set(context.Background(), "gtm", []sdk.Permission{sdk.PermSiteScripts}); err != nil {
		t.Fatal(err)
	}
	rt.record(Status{Name: "gtm", Loaded: true, Manifest: sdk.Manifest{Name: "gtm", Permissions: []sdk.Permission{sdk.PermSiteScripts}}})
	if !rt.scriptsAllowed("gtm") {
		t.Fatal("granted and requested scripts must be allowed")
	}
	rt.SetScriptsDeny(func(p string) bool { return p == "gtm" })
	if rt.scriptsAllowed("gtm") {
		t.Fatal("the deny hook must win over a stored grant")
	}
}

func TestHeadFilterCarriesLocaleAndSegment(t *testing.T) {
	h := hooks.New(quietLog())
	var got sdk.HeadContext
	hooks.OnFilter(h.For("pl19r"), sdk.RenderHead, 10, func(_ context.Context, c sdk.HeadContext) (sdk.HeadContext, error) {
		got = c
		return c, nil
	})
	HeadFilter(h)(context.Background(), render.HeadContext{Path: "/", Locale: "en-XX", Segment: "xx"})
	if got.Locale != "en-XX" || got.Segment != "xx" {
		t.Fatalf("locale/segment not passed: %+v", got)
	}
	raw, err := json.Marshal(sdk.HeadContext{Path: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "locale") || strings.Contains(string(raw), "segment") {
		t.Fatalf("one-language payload must omit locale and segment: %s", raw)
	}
}
