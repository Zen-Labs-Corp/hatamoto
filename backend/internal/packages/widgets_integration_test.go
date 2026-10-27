//go:build integration

package packages

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
)

func TestInstallValidatesWidgets(t *testing.T) {
	r := widgetRig(t)
	ctx := context.Background()
	install := func(files map[string][]byte) error {
		raw, err := PackFiles(files, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.mgr.Install(ctx, raw, InstallRequest{Source: SourceDev})
		return err
	}

	t.Run("a valid widget-only pack installs", func(t *testing.T) {
		if err := install(promoPack()); err != nil {
			t.Fatal(err)
		}
		row, err := r.mgr.store.Get(ctx, KindPlugin, "promo")
		if err != nil || row.State != StateInstalled {
			t.Fatalf("row = %+v, %v", row, err)
		}
	})

	for name, c := range map[string]struct {
		w    [4]string
		want string
	}{
		"template call":   {[4]string{wSchema, `<div {{.Attrs}}>{{call .P.x}}</div>`, wCSS, ""}, "widget.html: banned-function"},
		"script output":   {[4]string{wSchema, `<div {{.Attrs}}><script></script></div>`, wCSS, ""}, "banned-element"},
		"on handler":      {[4]string{wSchema, `<div {{.Attrs}} onclick="x()"></div>`, wCSS, ""}, "banned-attribute"},
		"unscoped css":    {[4]string{wSchema, wHTML, `body{margin:0}`, ""}, "css-selector-scope"},
		"css url":         {[4]string{wSchema, wHTML, `.hm-evil_w{background:url(x)}`, ""}, "css-unsafe"},
		"island eval":     {[4]string{wSchema, wHTML, `.hm-evil_w{color:red}`, `eval("1")`}, "island-banned"},
		"schema no label": {[4]string{`{"type":"object","additionalProperties":false,"x-hm":{},"properties":{}}`, wHTML, wCSS, ""}, "schema.json"},
	} {
		t.Run(name, func(t *testing.T) {
			err := install(widgetPack("evil", map[string][4]string{"w": c.w}))
			var pe *ParseError
			if !errors.As(err, &pe) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a ParseError containing %q", err, c.want)
			}
			if _, err := r.mgr.store.Get(ctx, KindPlugin, "evil"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("a refused pack was stored: %v", err)
			}
		})
	}

	t.Run("17 widgets", func(t *testing.T) {
		ws := map[string][4]string{}
		for i := 0; i < 17; i++ {
			n := "w" + string(rune('a'+i))
			ws[n] = [4]string{wSchema, wHTML, ".hm-many_" + n + "{color:red}", ""}
		}
		err := install(widgetPack("many", ws))
		if err == nil || !strings.Contains(err.Error(), "at most 16") {
			t.Fatalf("err = %v", err)
		}
	})
}

// --- runtime registration ---------------------------------------------------

func widgetRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	t.Cleanup(func() {
		_ = builder.SetPackKinds(nil)
		_ = render.SetPackWidgets(nil)
	})
	return r
}

func (r *rig) installWidgets(files map[string][]byte) {
	r.t.Helper()
	raw, err := PackFiles(files, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.mgr.Install(context.Background(), raw, InstallRequest{Source: SourceDev}); err != nil {
		r.t.Fatal(err)
	}
}

func hasKind(kind string) bool {
	for _, k := range builder.Kinds() {
		if k.Kind == kind {
			return true
		}
	}
	return false
}

// pageOf renders a doc holding one promo_banner node through the rig's engine.
func (r *rig) pageOf(canvas bool) string {
	r.t.Helper()
	doc := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "p", T: "promo_banner", P: map[string]any{"title": "Hello <b>"}}}}
	v := &render.PageView{BaseURL: "https://example.test", Path: "/", Site: render.SiteView{Name: "T", Language: "en"},
		Page: render.PageMeta{Name: "T", Title: "T"}, Doc: doc, Canvas: canvas}
	var sb strings.Builder
	if err := r.eng.RenderPage(&sb, v); err != nil {
		r.t.Fatal(err)
	}
	return sb.String()
}

func auditWidgets(t *testing.T, r *rig, action, target string) (n int, widgets float64) {
	t.Helper()
	err := r.pool.QueryRow(context.Background(),
		`SELECT count(*), COALESCE(max((detail->>'widgets')::float), -1) FROM hatamoto_admin_audit WHERE action = $1 AND target = $2`,
		action, target).Scan(&n, &widgets)
	if err != nil {
		t.Fatal(err)
	}
	return n, widgets
}

func TestWidgetPackLifecycleWithoutRestart(t *testing.T) {
	r := widgetRig(t)
	ctx := context.Background()
	if hasKind("promo_banner") {
		t.Fatal("kind registered before install")
	}

	r.installWidgets(promoPack())
	if !hasKind("promo_banner") {
		t.Fatal("install did not register the kind")
	}
	if pub := r.pageOf(false); !strings.Contains(pub, `<h3>Hello &lt;b&gt;</h3>`) || !strings.Contains(pub, ".hm-promo_banner{color:red}") {
		t.Fatalf("public page lacks the widget:\n%s", pub)
	}
	if n, w := auditWidgets(t, r, "package.install", "plugin:promo"); n != 1 || w != 1 {
		t.Errorf("install audit = %d rows, widgets %v", n, w)
	}
	if rt := r.rt.Statuses(); len(rt) != 0 {
		t.Errorf("a widget-only pack was loaded into the runtime: %+v", rt)
	}

	// Disable: the kind goes, the node becomes an orphan.
	if err := r.mgr.SetEnabled(ctx, "promo", false); err != nil {
		t.Fatal(err)
	}
	if hasKind("promo_banner") {
		t.Fatal("disable kept the kind")
	}
	if pub := r.pageOf(false); !strings.Contains(pub, "<!-- hm-widget: promo_banner not installed -->") || strings.Contains(pub, "<h3>") {
		t.Fatalf("disabled pack:\n%s", pub)
	}
	if can := r.pageOf(true); !strings.Contains(can, `data-hm-t="promo_banner">Widget &#34;promo_banner&#34; is not installed</div>`) {
		t.Fatalf("canvas lacks the placeholder box")
	}
	doc := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "p", T: "promo_banner", P: map[string]any{"title": "x"}}}}
	if err := doc.Validate(); err != nil {
		t.Fatalf("orphan does not validate: %v", err)
	}
	if _, err := doc.Apply(builder.Op{Op: "setProp", ID: "p", Key: "title", Value: []byte(`"y"`)}); !errors.Is(err, builder.ErrInvalid) {
		t.Fatalf("orphan prop edit: %v", err)
	}
	if err := r.mgr.SetEnabled(ctx, "promo", true); err != nil {
		t.Fatal(err)
	}
	if !hasKind("promo_banner") || !strings.Contains(r.pageOf(false), "<h3>") {
		t.Fatal("enable did not restore the widget")
	}
	if n, w := auditWidgets(t, r, "package.disable", "plugin:promo"); n != 1 || w != 1 {
		t.Errorf("disable audit = %d, widgets %v", n, w)
	}

	// Uninstall.
	if err := r.mgr.Uninstall(ctx, KindPlugin, "promo", false); err != nil {
		t.Fatal(err)
	}
	if hasKind("promo_banner") || !strings.Contains(r.pageOf(false), "not installed") {
		t.Fatal("uninstall kept the widget")
	}
	if n, w := auditWidgets(t, r, "package.uninstall", "plugin:promo"); n != 1 || w != 1 {
		t.Errorf("uninstall audit = %d, widgets %v", n, w)
	}

	// Reinstall: the stored props are intact.
	r.installWidgets(promoPack())
	if pub := r.pageOf(false); !strings.Contains(pub, `<h3>Hello &lt;b&gt;</h3>`) {
		t.Fatalf("reinstall did not restore the widget:\n%s", pub)
	}
}

func TestWidgetPackUpdateReplacesAtomically(t *testing.T) {
	r := widgetRig(t)
	r.installWidgets(promoPack())
	v2 := promoPack()
	v2[PluginManifest] = []byte(`{"name":"promo","version":"1.1.0","sdk":"^0.10","permissions":["builder.island"]}`)
	v2["widgets/banner/widget.css"] = []byte(`.hm-promo_banner{color:blue}`)
	v2["widgets/banner/widget.html"] = []byte(`<section {{.Attrs}}><h4>{{.P.title}}</h4></section>`)
	r.installWidgets(v2)
	pub := r.pageOf(false)
	if !strings.Contains(pub, "<h4>Hello &lt;b&gt;</h4>") || !strings.Contains(pub, "color:blue") || strings.Contains(pub, "color:red") {
		t.Fatalf("update not applied:\n%s", pub)
	}
}

func TestBrokenStoredWidgetPackIsSkipped(t *testing.T) {
	r := widgetRig(t)
	ctx := context.Background()
	r.installWidgets(promoPack())
	other := widgetPack("acme", map[string][4]string{"card": {wSchema, wHTML, `.hm-acme_card{color:green}`, ""}})
	r.installWidgets(other)
	if !hasKind("promo_banner") || !hasKind("acme_card") {
		t.Fatal("both packs should register")
	}
	// Patch the stored bytes of promo to a pack whose widget no longer validates
	// (what a rules change, or a damaged row, looks like at the next boot).
	bad, err := PackFiles(widgetPack("promo", map[string][4]string{"banner": {wSchema, `<div {{.Attrs}}>{{call .P.x}}</div>`, wCSS, ""}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE hatamoto_packages SET data = $1, sha256 = 'patched' WHERE kind = 'plugin' AND name = 'promo'`, bad); err != nil {
		t.Fatal(err)
	}
	r.mgr.Reconcile(ctx)
	if hasKind("promo_banner") {
		t.Fatal("a broken pack was registered")
	}
	if !hasKind("acme_card") {
		t.Fatal("the broken pack took the healthy one down")
	}
	// A fresh process (boot) also comes up.
	fresh := NewManager(Options{Pool: r.pool, Trust: r.mgr.Trust, AllowDev: true, Plugins: r.rt, Engine: r.eng})
	fresh.Reconcile(ctx)
	if hasKind("promo_banner") || !hasKind("acme_card") {
		t.Fatal("boot with a broken stored pack")
	}
}

func TestWidgetLimitAcrossPacks(t *testing.T) {
	r := widgetRig(t)
	pack := func(plugin string, n int) map[string][]byte {
		ws := map[string][4]string{}
		for i := 0; i < n; i++ {
			name := "w" + string(rune('a'+i))
			ws[name] = [4]string{wSchema, wHTML, ".hm-" + plugin + "_" + name + "{color:red}", ""}
		}
		return widgetPack(plugin, ws)
	}
	for _, p := range []string{"aa", "bb", "cc", "dd"} {
		r.installWidgets(pack(p, 16))
	}
	r.installWidgets(pack("ee", 6)) // 70 in all
	count := 0
	for _, k := range builder.Kinds() {
		if builder.IsPackKind(k.Kind) {
			count++
		}
	}
	if count != 64 {
		t.Fatalf("%d plugin widgets registered, want 64", count)
	}
	if hasKind("ee_wa") {
		t.Fatal("the pack over the limit was registered")
	}
}

func TestSecondReplicaConvergesOnWidgets(t *testing.T) {
	r := widgetRig(t)
	ctx := context.Background()
	cli := NewManager(Options{Pool: r.pool, Trust: r.mgr.Trust, AllowDev: true})
	raw, err := PackFiles(promoPack(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// The CLI has no runtime or engine; its reconcile registers into the same
	// process-wide registries, so reset them and let the server's Reconcile do it.
	if _, err := cli.Install(ctx, raw, InstallRequest{Source: SourceDev}); err != nil {
		t.Fatal(err)
	}
	_ = builder.SetPackKinds(nil)
	_ = render.SetPackWidgets(nil)
	r.mgr.Reconcile(ctx)
	if !hasKind("promo_banner") || !strings.Contains(r.pageOf(false), "<h3>") {
		t.Fatal("the server did not pick up the CLI's widget pack")
	}
	if err := cli.SetEnabled(ctx, "promo", false); err != nil {
		t.Fatal(err)
	}
	r.mgr.Reconcile(ctx)
	if hasKind("promo_banner") {
		t.Fatal("the server kept a pack another process disabled")
	}
}

// An island is served only while builder.island is granted; the widget
// renders either way.
func TestWidgetPackIslandFollowsGrant(t *testing.T) {
	r := widgetRig(t)
	ctx := context.Background()
	r.installWidgets(promoPack()) // no grant
	const island = "/theme/assets/builder/"
	if pub := r.pageOf(false); !strings.Contains(pub, "<h3>") || strings.Contains(pub, island) {
		t.Fatalf("ungranted pack: widget must render without an island:\n%s", pub)
	}
	if err := r.mgr.SetGrants(ctx, "promo", []string{"log"}); !errors.Is(err, ErrPermNotRequested) {
		t.Fatalf("unrequested grant: %v", err)
	}
	if err := r.mgr.SetGrants(ctx, "promo", []string{"builder.island"}); err != nil {
		t.Fatal(err)
	}
	if pub := r.pageOf(false); !strings.Contains(pub, island) {
		t.Fatalf("granted pack: island not served:\n%s", pub)
	}
	if err := r.mgr.SetGrants(ctx, "promo", nil); err != nil {
		t.Fatal(err)
	}
	if pub := r.pageOf(false); strings.Contains(pub, island) || !strings.Contains(pub, "<h3>") {
		t.Fatalf("revoked pack:\n%s", pub)
	}
}
