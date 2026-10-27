//go:build integration

package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/sdk"
)

// B8 data sources across HTTP with the real cache and the real example
// plugin: a good source renders and is cached, a failing one degrades to the
// empty state and is not cached. Needs a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RenderExt
func TestRenderExt_Integration(t *testing.T) {
	r := newReuseIT(t)
	rt := r.srv.plugins
	if rt == nil {
		t.Fatal("no plugin runtime")
	}
	dir := t.TempDir()
	wasm := filepath.Join(dir, "plugin.wasm")
	src := "../../../examples/plugins/datasource-demo"
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasm, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, b)
	}
	code, err := os.ReadFile(wasm)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(src, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m sdk.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Unload(r.ctx, m.Name) })
	load := func(mode string) {
		t.Helper()
		mm := m
		mm.Config = map[string]string{"mode": mode, "temp": "19&C"}
		if st := rt.Load(r.ctx, mm, code); !st.Loaded {
			t.Fatalf("load %s: %s", mode, st.Error)
		}
		if _, err := rt.Grant(r.ctx, m.Name, []sdk.Permission{sdk.PermBuilderData}); err != nil {
			t.Fatal(err)
		}
	}

	doc := map[string]any{"v": 2, "nodes": []any{
		map[string]any{"id": "hd", "t": "heading", "p": map[string]any{"text": "B8-FALLBACK"}, "b": map[string]string{"text": "ext.datasource-demo.temp"}},
		map[string]any{"id": "lp", "t": "loop", "p": map[string]any{"from": "source", "source": "ext.datasource-demo.forecast", "limit": 3}, "c": []any{
			map[string]any{"id": "lh", "t": "heading", "b": map[string]string{"text": "loop.item.title"}},
			map[string]any{"id": "le", "t": "loopempty", "c": []any{map[string]any{"id": "lx", "t": "text", "p": map[string]any{"text": "B8-EMPTY"}}}},
		}},
	}}
	page := r.mk(r.pageSc, "B8 Ext Page", map[string]any{"page_doc": doc})
	r.publish(page)
	path := "/" + page.Slug

	load("trap")
	w := r.get(path)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, "B8-FALLBACK") || strings.Contains(body, "Day 1") || !strings.Contains(body, "B8-EMPTY") {
		t.Fatalf("failing source: code=%d body=%s", w.Code, body)
	}
	r.get(path)
	if r.cached(path) {
		t.Fatal("a degraded page was cached")
	}

	load("ok")
	w = r.get(path)
	body = w.Body.String()
	for _, want := range []string{"19&amp;C", "Day 1", "Day 3"} {
		if !strings.Contains(body, want) {
			t.Fatalf("good source: %q missing in %s", want, body)
		}
	}
	if strings.Contains(body, "Day 4") || strings.Contains(body, "B8-EMPTY") {
		t.Fatalf("limit 3 not honoured: %s", body)
	}
	r.eventually("the healthy page is cached", func() bool { r.get(path); return r.cached(path) })
	// The cached copy is served even when the source breaks afterwards.
	load("trap")
	if hit := r.get(path).Body.String(); !strings.Contains(hit, "Day 2") {
		t.Fatalf("cached page lost its items: %s", hit)
	}
}

// Both examples through the real package manager and the real page path: the
// widget pack's widgets reach the palette, the public page, the canvas and
// the outline form; its island is served; the data source feeds a binding of
// the pack widget and a loop; uninstalling degrades and reinstalling restores.
func TestExamples_Integration(t *testing.T) {
	r := newReuseIT(t)
	mgr := r.srv.packages
	if mgr == nil {
		t.Fatal("no package manager")
	}
	packDir := func(dir string) []byte {
		t.Helper()
		raw, err := packages.PackDir(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	// datasource-demo needs its wasm staged next to plugin.json, as `make plugin-datasource-demo` does.
	stage := t.TempDir()
	src := "../../../examples/plugins/datasource-demo"
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", filepath.Join(stage, "plugin.wasm"), ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, b)
	}
	manifest, err := os.ReadFile(filepath.Join(src, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "plugin.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	dsPack := packDir(stage)
	wpPack := packDir("../../../examples/plugins/widgetpack-demo")

	install := func(raw []byte, grant ...string) {
		t.Helper()
		if _, err := mgr.Install(r.ctx, raw, packages.InstallRequest{Source: packages.SourceDev, Grant: grant, Confirm: true}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = mgr.Uninstall(r.ctx, packages.KindPlugin, "widgetpack-demo", true)
		_ = mgr.Uninstall(r.ctx, packages.KindPlugin, "datasource-demo", true)
	})
	install(wpPack, "builder.island")
	install(dsPack, "builder.data")

	hasKind := func(kind string) (group string, ok bool) {
		for _, k := range builder.Kinds() {
			if k.Kind == kind {
				return k.Group, true
			}
		}
		return "", false
	}
	for _, k := range []string{"widgetpack-demo_promo", "widgetpack-demo_pricing"} {
		if g, ok := hasKind(k); !ok || g != "Plugin widgets" {
			t.Fatalf("palette: %s group=%q ok=%v", k, g, ok)
		}
	}

	doc := map[string]any{"v": 2, "nodes": []any{
		map[string]any{"id": "pr", "t": "widgetpack-demo_promo", "p": map[string]any{"text": "Free <b>shipping</b>", "href": "https://example.test/sale", "link_text": "Go"},
			"b": map[string]string{"title": "ext.datasource-demo.temp"}},
		map[string]any{"id": "pc", "t": "widgetpack-demo_pricing", "p": map[string]any{"plan": "Pro", "price": "$20"}, "c": []any{
			map[string]any{"id": "pt", "t": "text", "p": map[string]any{"text": "ten seats"}}}},
		map[string]any{"id": "lp", "t": "loop", "p": map[string]any{"from": "source", "source": "ext.datasource-demo.forecast", "limit": 2}, "c": []any{
			map[string]any{"id": "lh", "t": "heading", "b": map[string]string{"text": "loop.item.title"}}}},
	}}
	page := r.mk(r.pageSc, "B8 Examples", map[string]any{"page_doc": doc})
	r.publish(page)
	path := "/" + page.Slug

	body := r.get(path).Body.String()
	for _, want := range []string{"hm-widgetpack-demo_promo", "Free &lt;b&gt;shipping&lt;/b&gt;", `href="https://example.test/sale"`, "21°C", "hm-widgetpack-demo_pricing", "ten seats", "Day 1", "Day 2", `integrity="sha256-`} {
		if !strings.Contains(body, want) {
			t.Fatalf("public page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Day 3") || strings.Contains(body, "data-hm-node") || strings.Contains(body, "data-hm-t=") {
		t.Fatal("limit not honoured or canvas markers leaked into the public page")
	}
	m := regexp.MustCompile(`src="(/theme/assets/builder/widgetpack-demo_promo\.[0-9a-f]{8}\.js)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no island script tag:\n%s", body)
	}
	if w := r.get(m[1]); w.Code != 200 || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Body.String(), "Dismiss") {
		t.Fatalf("island: %d nosniff=%q", w.Code, w.Header().Get("X-Content-Type-Options"))
	}
	if w := r.get("/theme/assets/builder/widgetpack-demo_promo.00000000.js"); w.Code != 404 {
		t.Fatalf("wrong island hash: %d", w.Code)
	}

	canvas := r.canvas(page, doc)
	for _, want := range []string{`data-hm-node="pr"`, `data-hm-t="widgetpack-demo_promo"`, `data-hm-node="pc"`} {
		if !strings.Contains(canvas, want) {
			t.Fatalf("canvas lacks %q", want)
		}
	}
	if strings.Contains(canvas, "integrity=") {
		t.Fatal("canvas carries an island")
	}

	// outline form of the pack widget
	outline := r.do("owner", "GET", path2edit(page), nil).Body.String()
	for _, want := range []string{`name="n.pr.p.link_text"`, `name="n.pr.b.title"`, `ext.datasource-demo.temp`, `name="n.pc.p.plan"`} {
		if !strings.Contains(outline, want) {
			t.Fatalf("outline lacks %q", want)
		}
	}

	// uninstall degrades, the page keeps working and the doc still saves
	if err := mgr.Uninstall(r.ctx, packages.KindPlugin, "widgetpack-demo", true); err != nil {
		t.Fatal(err)
	}
	body = r.get(path).Body.String()
	if !strings.Contains(body, "<!-- hm-widget: widgetpack-demo_promo not installed -->") || strings.Contains(body, "hm-widgetpack-demo_promo\"") || !strings.Contains(body, "Day 1") {
		t.Fatalf("uninstalled page:\n%s", body)
	}
	if canvas := r.canvas(page, doc); !strings.Contains(canvas, "hm-missing") || !strings.Contains(canvas, "is not installed") {
		t.Fatal("canvas lacks the not-installed box")
	}
	r.setDoc(page, doc) // an orphan doc still saves

	install(wpPack, "builder.island")
	body = r.get(path).Body.String()
	if !strings.Contains(body, "Free &lt;b&gt;shipping&lt;/b&gt;") || !strings.Contains(body, "21°C") {
		t.Fatalf("reinstall did not restore the widget with its props:\n%s", body)
	}
}

func path2edit(e *entity.Entity) string {
	return "/admin/entities/" + e.ID.String() + "/edit?outline=1"
}
