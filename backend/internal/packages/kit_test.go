package packages

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// kitExampleJSON is the docs/KITS.md example, verbatim.
const kitExampleJSON = `{
  "name": "community",
  "version": "1.0.0",
  "kit": 1,
  "sdk": "^0.27",
  "title": "Community",
  "description": "A community site with ranked stories and threaded comments.",
  "requires": ["comment.policy", "comment.rank", "plugin.entity-write", "comment.signin"],
  "plugins": [{"name": "example-board", "range": "^1", "grant": "all"}],
  "themes": [{"name": "example-theme", "range": "^1", "activate": false}],
  "schemas": [
    {
      "slug": "story",
      "name": "Stories",
      "schema": {"type": "object"},
      "settings": {
        "url_pattern": "/item/{slug}",
        "comments": {
          "mode": "open",
          "order": "rank",
          "policy": {"enabled": true, "max_depth": 32, "edit_window_minutes": 120, "slow_mode_seconds": 30}
        }
      }
    }
  ],
  "pages": [
    {
      "slug": "latest",
      "name": "Latest",
      "schema": "page",
      "blocks": [{"type": "rich-text", "props": {}}],
      "homepage": false
    }
  ],
  "menus": [
    {"location": "header", "name": "Header", "items": [{"label": "Latest", "url": "/latest"}]}
  ],
  "switches": {"comments": true, "comment_policy": ["story"], "homepage": "latest"}
}
`

const kitMinJSON = `{"name":"community","version":"1.0.0","kit":1,"sdk":"^0.27"}`

func kitFiles(manifest string) map[string][]byte {
	return map[string][]byte{KitManifest: []byte(manifest)}
}

func TestKitParsesTheDocumentedExample(t *testing.T) {
	k, err := parseKit([]byte(kitExampleJSON))
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "community" || k.KitFormat != 1 || k.SDK != "^0.27" {
		t.Fatalf("kit = %+v", k)
	}
	if len(k.Plugins) != 1 || !k.Plugins[0].Grant.All || k.Plugins[0].Grant.Perms != nil {
		t.Fatalf("plugins = %+v", k.Plugins)
	}
	if len(k.Themes) != 1 || k.Themes[0].Activate {
		t.Fatalf("themes = %+v", k.Themes)
	}
	if len(k.Schemas) != 1 || k.Schemas[0].Slug != "story" || len(k.Schemas[0].Settings) == 0 {
		t.Fatalf("schemas = %+v", k.Schemas)
	}
	if len(k.Pages) != 1 || k.Pages[0].Blocks[0].Type != "rich-text" {
		t.Fatalf("pages = %+v", k.Pages)
	}
	if len(k.Menus) != 1 || k.Menus[0].Items[0].URL != "/latest" {
		t.Fatalf("menus = %+v", k.Menus)
	}
	if !k.Switches.Comments || k.Switches.CommentPolicy[0] != "story" || k.Switches.Homepage != "latest" {
		t.Fatalf("switches = %+v", k.Switches)
	}
	if len(k.Requires) != 4 {
		t.Fatalf("requires = %v", k.Requires)
	}
}

func TestKitGrantForms(t *testing.T) {
	wrap := func(g string) string {
		return `{"name":"community","version":"1.0.0","kit":1,"sdk":"^0.27","plugins":[{"name":"example-board","range":"^1"` + g + `}]}`
	}
	k, err := parseKit([]byte(wrap(`,"grant":["settings","log"]`)))
	if err != nil {
		t.Fatal(err)
	}
	if g := k.Plugins[0].Grant; g.All || len(g.Perms) != 2 || g.Perms[1] != "log" {
		t.Fatalf("grant = %+v", g)
	}
	k, err = parseKit([]byte(wrap(``)))
	if err != nil {
		t.Fatal(err)
	}
	if g := k.Plugins[0].Grant; g.All || g.Perms != nil {
		t.Fatalf("absent grant = %+v", g)
	}
	var empty KitGrant
	if err := jsonx.Unmarshal([]byte(`[]`), &empty); err != nil || empty.Perms == nil {
		t.Fatalf("an empty list must stay distinguishable from an absent grant: %+v, %v", empty, err)
	}
	for _, bad := range []string{`,"grant":"db"`, `,"grant":3`, `,"grant":{}`} {
		if _, err := parseKit([]byte(wrap(bad))); err == nil {
			t.Errorf("%s: want refusal", bad)
		}
	}
}

func TestKitGrantRoundTrips(t *testing.T) {
	for _, in := range []string{`"all"`, `["settings","log"]`} {
		var g KitGrant
		if err := jsonx.Unmarshal([]byte(in), &g); err != nil {
			t.Fatal(err)
		}
		out, err := jsonx.Marshal(g)
		if err != nil || string(out) != in {
			t.Fatalf("%s -> %s, %v", in, out, err)
		}
	}
}

func TestKitDisplayTitle(t *testing.T) {
	k, err := parseKit([]byte(kitMinJSON))
	if err != nil {
		t.Fatal(err)
	}
	if k.DisplayTitle() != "community" {
		t.Fatalf("title = %q", k.DisplayTitle())
	}
	k.Title = "Community"
	if k.DisplayTitle() != "Community" {
		t.Fatalf("title = %q", k.DisplayTitle())
	}
}

func TestKitPackageParsesAndVerifies(t *testing.T) {
	k := newKey(t)
	raw, err := PackFiles(kitFiles(kitExampleJSON), &k)
	if err != nil {
		t.Fatal(err)
	}
	p := parse(t, raw)
	if p.Kind != KindKit || p.Name != "community" || p.Version != "1.0.0" {
		t.Fatalf("package = %s %s %s", p.Kind, p.Name, p.Version)
	}
	kit, err := p.Kit()
	if err != nil || kit.Description == "" || len(kit.Plugins) != 1 {
		t.Fatalf("Kit() = %+v, %v", kit, err)
	}
	if err := trust(t, k).Verify(p); err != nil {
		t.Fatal(err)
	}
	if _, err := parse(t, mustPack(t, themeFiles())).Kit(); err == nil {
		t.Fatal("Kit() of a theme must fail")
	}
}

func mustPack(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	raw, err := PackFiles(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestKitSignatureRefusals(t *testing.T) {
	k, other := newKey(t), newKey(t)
	signed, err := PackFiles(kitFiles(kitMinJSON), &k)
	if err != nil {
		t.Fatal(err)
	}
	if err := trust(t, other).Verify(parse(t, signed)); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("err = %v", err)
	}
	sig := parse(t, signed).Signature
	files := kitFiles(strings.Replace(kitMinJSON, "1.0.0", "1.0.1", 1))
	files[SignatureFile] = []byte(`{"signer":"` + sig.Signer + `","algorithm":"ed25519","signature":"` + sig.Signature + `"}`)
	forged, err := zipFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if err := trust(t, k).Verify(parse(t, forged)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v", err)
	}
}

func TestKitParseRefusals(t *testing.T) {
	cases := map[string]map[string][]byte{
		"plugin and kit": {PluginManifest: []byte(pluginJSON), PluginWasm: []byte("\x00asm"), KitManifest: []byte(kitMinJSON)},
		"theme and kit":  {ThemeManifest: []byte(themeJSON), KitManifest: []byte(kitMinJSON)},
		"plugin.wasm":    {KitManifest: []byte(kitMinJSON), PluginWasm: []byte("\x00asm")},
		"kit.wasm":       {KitManifest: []byte(kitMinJSON), "kit.wasm": []byte("\x00asm")},
		"assets file":    {KitManifest: []byte(kitMinJSON), "assets/a.css": []byte("a{}")},
		"widgets":        {KitManifest: []byte(kitMinJSON), "widgets/w.json": []byte("{}")},
		"tags":           {KitManifest: []byte(kitMinJSON), "tags/t.html": []byte("x")},
		"unknown key":    kitFiles(`{"name":"community","version":"1.0.0","kit":1,"sdk":"^0.27","hooks":[]}`),
		"permissions":    kitFiles(`{"name":"community","version":"1.0.0","kit":1,"sdk":"^0.27","permissions":["db"]}`),
		"bad name":       kitFiles(`{"name":"Community","version":"1.0.0","kit":1,"sdk":"^0.27"}`),
		"bad version":    kitFiles(`{"name":"community","version":"one","kit":1,"sdk":"^0.27"}`),
		"not json":       kitFiles(`{`),
	}
	for name, files := range cases {
		raw, err := zipFiles(files)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Parse(raw, DefaultLimits)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("%s: err = %v, want a ParseError", name, err)
		}
	}
}
