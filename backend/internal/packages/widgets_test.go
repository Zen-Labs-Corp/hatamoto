package packages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

const (
	wSchema = `{"type":"object","additionalProperties":false,
		"x-hm":{"label":"Promo banner","children":true},
		"properties":{"title":{"type":"string","x-hm-bind":"text"}}}`
	wHTML = `<div {{.Attrs}}><h3>{{.P.title}}</h3>{{.Children}}</div>`
	wCSS  = `.hm-promo_banner{color:red}`
)

// widgetPack returns the files of a widget-only plugin named plugin holding
// the widgets (name -> schema, html, css, island).
func widgetPack(plugin string, ws map[string][4]string) map[string][]byte {
	files := map[string][]byte{PluginManifest: []byte(`{"name":"` + plugin + `","version":"1.0.0","sdk":"^0.10","permissions":["builder.island"]}`)}
	for name, w := range ws {
		files["widgets/"+name+"/schema.json"] = []byte(w[0])
		files["widgets/"+name+"/widget.html"] = []byte(w[1])
		if w[2] != "" {
			files["widgets/"+name+"/widget.css"] = []byte(w[2])
		}
		if w[3] != "" {
			files["widgets/"+name+"/island.js"] = []byte(w[3])
		}
	}
	return files
}

func promoPack() map[string][]byte {
	return widgetPack("promo", map[string][4]string{"banner": {wSchema, wHTML, wCSS, `document.title;`}})
}

func TestWidgetOnlyPackParsesAndValidates(t *testing.T) {
	raw, err := PackFiles(promoPack(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if p.Kind != KindPlugin || p.Name != "promo" {
		t.Fatalf("pack = %s %s", p.Kind, p.Name)
	}
	if err := validateWidgets(p); err != nil {
		t.Fatal(err)
	}
	ws, err := p.Widgets()
	if err != nil || len(ws.Items) != 1 || ws.Items[0].Kind != "promo_banner" || string(ws.Items[0].CSS) != wCSS || string(ws.Items[0].Island) != "document.title;" {
		t.Fatalf("widgets = %+v, %v", ws, err)
	}
}

func TestPluginNeedsWasmUnlessItHasWidgets(t *testing.T) {
	raw, err := PackFiles(map[string][]byte{PluginManifest: []byte(pluginJSON)}, nil)
	if err == nil {
		t.Fatalf("a plugin with neither wasm nor widgets packed: %d bytes", len(raw))
	}
	if !strings.Contains(err.Error(), "plugin.wasm") {
		t.Fatalf("err = %v", err)
	}
	// With wasm and widgets both is fine.
	files := promoPack()
	files[PluginWasm] = []byte("\x00asm\x01\x00\x00\x00")
	if _, err := PackFiles(files, nil); err != nil {
		t.Fatal(err)
	}
	// An empty widgets/ directory is not widgets.
	mustRefuse(t, rawZip(t, hdr(PluginManifest, pluginJSON), hdr("widgets/", "")), "needs plugin.wasm")
}

func TestThemeCannotHoldWidgets(t *testing.T) {
	files := themeFiles()
	files["widgets/x/schema.json"] = []byte(wSchema)
	_, err := PackFiles(files, nil)
	if err == nil || !strings.Contains(err.Error(), "theme package cannot hold widgets/") {
		t.Fatalf("err = %v", err)
	}
	mustRefuse(t, rawZip(t, hdr(ThemeManifest, themeJSON), hdr("layouts/base.html", "x"), hdr("widgets/", "")), "theme package cannot hold widgets/")
}

func TestWidgetLayoutIsClosed(t *testing.T) {
	m := hdr(PluginManifest, pluginJSON)
	for name, want := range map[string]string{
		"widgets/x/other.txt":          "a widget holds only",
		"widgets/Bad_Name/schema.json": "widget directory names",
		"widgets/bad_name/schema.json": "widget directory names",
		"widgets/1x/schema.json":       "widget directory names",
		"widgets/x/sub/schema.json":    "a widget holds only",
		"widgets/schema.json":          "widget directory names",
	} {
		mustRefuse(t, rawZip(t, m, hdr(PluginWasm, "\x00asm"), hdr(name, "{}")), want)
	}
}

func TestValidateWidgetsLimits(t *testing.T) {
	ws := map[string][4]string{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p"} {
		ws[n] = [4]string{wSchema, strings.ReplaceAll(wHTML, "x", "x"), `.hm-promo_` + n + `{color:red}`, ""}
	}
	raw, err := PackFiles(widgetPack("promo", ws), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWidgets(parse(t, raw)); err != nil {
		t.Fatalf("16 widgets: %v", err)
	}
	q := ws["a"]
	q[2] = `.hm-promo_q{color:red}`
	ws["q"] = q
	raw, err = PackFiles(widgetPack("promo", ws), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = validateWidgets(parse(t, raw))
	var pe *ParseError
	if !asParseError(err, &pe) || !strings.Contains(err.Error(), "at most 16") {
		t.Fatalf("17 widgets: %v", err)
	}
}

func TestValidateWidgetsRules(t *testing.T) {
	cases := []struct {
		name string
		w    [4]string
		want string
	}{
		{"no label", [4]string{`{"type":"object","additionalProperties":false,"x-hm":{},"properties":{}}`, wHTML, wCSS, ""}, "widgets/banner/schema.json"},
		{"array prop", [4]string{`{"type":"object","additionalProperties":false,"x-hm":{"label":"L"},"properties":{"a":{"type":"array"}}}`, wHTML, wCSS, ""}, "array"},
		{"call", [4]string{wSchema, `<div {{.Attrs}}>{{call .P.x}}</div>`, wCSS, ""}, "widgets/banner/widget.html: banned-function"},
		{"script", [4]string{wSchema, `<div {{.Attrs}}><script></script></div>`, wCSS, ""}, "banned-element"},
		{"no attrs", [4]string{wSchema, `<div></div>`, wCSS, ""}, "attrs-required"},
		{"unscoped css", [4]string{wSchema, wHTML, `body{margin:0}`, ""}, "widgets/banner/widget.css"},
		{"css import", [4]string{wSchema, wHTML, `@import "x";`, ""}, "css-unsafe"},
		{"eval island", [4]string{wSchema, wHTML, wCSS, `eval("1")`}, "widgets/banner/island.js: island-banned"},
		{"big island", [4]string{wSchema, wHTML, wCSS, "//" + strings.Repeat("a", 33000)}, "island.js: size"},
	}
	for _, c := range cases {
		raw, err := PackFiles(widgetPack("promo", map[string][4]string{"banner": c.w}), nil)
		if err != nil {
			t.Fatalf("%s: pack: %v", c.name, err)
		}
		err = validateWidgets(parse(t, raw))
		var pe *ParseError
		if !asParseError(err, &pe) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// The render corpus of hostile packs is refused through validateWidgets, too.
func TestValidateWidgetsRenderCorpus(t *testing.T) {
	dirs, _ := filepath.Glob("../render/testdata/packs/*")
	if len(dirs) < 28 {
		t.Fatalf("corpus: %d cases", len(dirs))
	}
	schema := strings.ReplaceAll(wSchema, "Promo banner", "W")
	for _, d := range dirs {
		read := func(f string) string { b, _ := os.ReadFile(filepath.Join(d, f)); return string(b) }
		want := strings.TrimSpace(read("want"))
		raw, err := PackFiles(widgetPack("pp", map[string][4]string{"w": {schema, read("widget.html"), read("widget.css"), read("island.js")}}), nil)
		if err != nil {
			t.Fatalf("%s: pack: %v", d, err)
		}
		err = validateWidgets(parse(t, raw))
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: good pack refused: %v", filepath.Base(d), err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: err = %v, want %q", filepath.Base(d), err, want)
		}
	}
}

func TestPackDirWidgetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for n, b := range promoPack() {
		write(n, string(b))
	}
	write("widgets/banner/NOTES.txt", "not packed")
	write("widgets/banner/.hidden", "not packed")
	raw, err := PackDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if p.Has("widgets/banner/NOTES.txt") || !p.Has("widgets/banner/widget.html") {
		t.Fatal("PackDir included the wrong widget files")
	}
	if err := validateWidgets(p); err != nil {
		t.Fatal(err)
	}
}

func TestSignatureCoversWidgets(t *testing.T) {
	k := newKey(t)
	raw, err := PackFiles(promoPack(), &k)
	if err != nil {
		t.Fatal(err)
	}
	if err := trust(t, k).Verify(parse(t, raw)); err != nil {
		t.Fatal(err)
	}
	// Same signature, one CSS byte changed: the digest differs.
	tampered := promoPack()
	tampered["widgets/banner/widget.css"] = []byte(strings.Replace(wCSS, "red", "rez", 1))
	other, err := PackFiles(tampered, nil)
	if err != nil {
		t.Fatal(err)
	}
	orig, evil := parse(t, raw), parse(t, other)
	if orig.Digest == evil.Digest {
		t.Fatal("digest ignores widgets/")
	}
	evil.Signature = orig.Signature
	if err := trust(t, k).Verify(evil); err == nil {
		t.Fatal("tampered widget css verified")
	}
}

func asParseError(err error, target **ParseError) bool { return errors.As(err, target) }

// A package that ships an island must ask for builder.island.
func TestIslandNeedsPermission(t *testing.T) {
	files := promoPack()
	files[PluginManifest] = []byte(`{"name":"promo","version":"1.0.0","sdk":"^0.10"}`)
	raw, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWidgets(parse(t, raw)); err == nil || !strings.Contains(err.Error(), "builder.island") {
		t.Fatalf("island without the permission: %v", err)
	}
	delete(files, "widgets/banner/island.js")
	raw, _ = PackFiles(files, nil)
	if err := validateWidgets(parse(t, raw)); err != nil {
		t.Fatalf("no island, no permission: %v", err)
	}
}

// An update that newly asks for builder.island is widened access.
func TestWidenedAccessIsland(t *testing.T) {
	raw, err := PackFiles(promoPack(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	_, _, island, _ := widenedAccess([]byte(`{"name":"promo","version":"1.0.0","sdk":"^0.10"}`), p)
	if !island {
		t.Error("a newly asked builder.island is not widened access")
	}
	_, _, island, _ = widenedAccess([]byte(`{"name":"promo","version":"1.0.0","sdk":"^0.10","permissions":["builder.island"]}`), p)
	if island {
		t.Error("an unchanged builder.island is widened access")
	}
}

// fakeGrants is an in-memory pluginhost.Grants.
type fakeGrants map[string]bool

func (g fakeGrants) Has(plugin string, perm sdk.Permission) bool {
	return g[plugin] && perm == sdk.PermBuilderIsland
}
func (g fakeGrants) Granted(string) []sdk.Permission { return nil }
func (g fakeGrants) Set(context.Context, string, []sdk.Permission) error {
	return nil
}

// reconcileWidgets serves an island only while builder.island is granted, and
// takes it away when the grant is revoked (no database).
func TestReconcileWidgetsIslandFollowsGrant(t *testing.T) {
	raw, err := PackFiles(promoPack(), nil)
	if err != nil {
		t.Fatal(err)
	}
	schemas, views, err := buildWidgets(parse(t, raw))
	if err != nil || !islandOf(views) {
		t.Fatalf("buildWidgets: island=%v, %v", islandOf(views), err)
	}
	t.Cleanup(func() { builder.SetPackKinds(nil); render.SetPackWidgets(nil) })

	grants := fakeGrants{}
	m := &Manager{Logger: slog.Default(), Grants: grants}
	m.widgets.cache = map[string]widgetEntry{"promo": {sha: "h1", schemas: schemas, views: views}}
	rows := []Row{{Kind: KindPlugin, Name: "promo", SHA256: "h1", Enabled: true, State: StateInstalled,
		Manifest: json.RawMessage(`{"name":"promo","version":"1.0.0","permissions":["builder.island"]}`)}}

	step := func(granted, wantChange, wantIsland bool) {
		t.Helper()
		grants["promo"] = granted
		if got := m.reconcileWidgets(context.Background(), rows); got != wantChange {
			t.Fatalf("granted=%v: changed = %v, want %v", granted, got, wantChange)
		}
		if len(m.widgets.views) != 1 || islandOf(m.widgets.views) != wantIsland {
			t.Fatalf("granted=%v: views = %+v, want island=%v", granted, m.widgets.views, wantIsland)
		}
	}
	step(false, true, false) // widget renders, no island
	step(true, true, true)   // grant: island served
	step(true, false, true)  // nothing changed
	step(false, true, false) // revoke: island gone
}

// A community package gets no island, even with a grant stored before the policy.
func TestReconcileWidgetsDropsCommunityIsland(t *testing.T) {
	raw, err := PackFiles(promoPack(), nil)
	if err != nil {
		t.Fatal(err)
	}
	schemas, views, err := buildWidgets(parse(t, raw))
	if err != nil || !islandOf(views) {
		t.Fatalf("buildWidgets: island=%v, %v", islandOf(views), err)
	}
	t.Cleanup(func() { builder.SetPackKinds(nil); render.SetPackWidgets(nil) })

	m := &Manager{Logger: slog.Default(), Grants: fakeGrants{"promo": true}}
	m.widgets.cache = map[string]widgetEntry{"promo": {sha: "h1", schemas: schemas, views: views}}
	rows := []Row{{Kind: KindPlugin, Name: "promo", SHA256: "h1", Enabled: true, State: StateInstalled,
		SignerKeyID: "adea8e00f5ac74ca",
		Manifest:    json.RawMessage(`{"name":"promo","version":"1.0.0","permissions":["builder.island"]}`)}}
	m.refreshOrigins(rows)
	if m.originOf("promo") != OriginCommunity {
		t.Fatal("fixture is not a community package")
	}
	m.reconcileWidgets(context.Background(), rows)
	if len(m.widgets.views) != 1 || islandOf(m.widgets.views) {
		t.Fatalf("community island served: %+v", m.widgets.views)
	}
}

func TestWidenedAccessScripts(t *testing.T) {
	raw, err := PackFiles(map[string][]byte{
		PluginManifest: []byte(`{"name":"promo","version":"1.1.0","sdk":"^0.12","permissions":["site.scripts"]}`),
		PluginWasm:     []byte("\x00asm"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if _, _, _, s := widenedAccess([]byte(`{"name":"promo","version":"1.0.0","sdk":"^0.12"}`), p); !s {
		t.Error("a newly asked site.scripts is not widened access")
	}
	if _, _, _, s := widenedAccess([]byte(`{"name":"promo","version":"1.0.0","sdk":"^0.12","permissions":["site.scripts"]}`), p); s {
		t.Error("an already asked site.scripts is not new")
	}
}

const ownSchema = `{"type":"object","additionalProperties":false,
	"x-hm":{"label":"Promo banner","children":true},
	"properties":{"title":{"type":"string"},"anchor":{"type":"string"}}}`

// A package that declared "anchor" before the builder did stays usable on
// upgrade (reconcile logs and skips the setting for the widget); a new install
// of such a package is refused.
func TestPackOwningAnElementSettingStaysUsableButIsNotInstalled(t *testing.T) {
	raw, err := PackFiles(widgetPack("promo", map[string][4]string{"banner": {ownSchema, wHTML, "", ""}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if err := validateWidgets(p); err == nil || !strings.Contains(err.Error(), `"anchor"`) {
		t.Fatalf("install: err = %v, want a refusal naming anchor", err)
	}
	var logs bytes.Buffer
	schemas, views, err := buildWidgets(p)
	if err != nil || len(schemas) != 1 {
		t.Fatalf("boot build: %d schemas, %v", len(schemas), err)
	}
	t.Cleanup(func() { builder.SetPackKinds(nil); render.SetPackWidgets(nil) })
	m := &Manager{Logger: slog.New(slog.NewTextHandler(&logs, nil)), Grants: fakeGrants{}}
	m.widgets.cache = map[string]widgetEntry{"promo": {sha: "h1", schemas: schemas, views: views}}
	rows := []Row{{Kind: KindPlugin, Name: "promo", SHA256: "h1", Enabled: true, State: StateInstalled,
		Manifest: json.RawMessage(`{"name":"promo","version":"1.0.0"}`)}}
	if !m.reconcileWidgets(context.Background(), rows) || len(m.widgets.schemas) != 1 {
		t.Fatalf("the widget was dropped: %d schemas", len(m.widgets.schemas))
	}
	warnOwned(slog.New(slog.NewTextHandler(&logs, nil)), "promo", schemas)
	if got := logs.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "promo_banner") || !strings.Contains(got, "anchor") {
		t.Errorf("no warning naming the widget and prop: %q", got)
	}
	if !builder.PackOwns("promo_banner", "anchor") {
		t.Error("the kind does not own anchor")
	}
}
