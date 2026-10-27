package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
)

const storedBlob = `{"colors":{"accent":"#778899"},"builder":{"tokens":{"space":{"4":"2rem"}},"presets":{"card":{"label":"Card","s":{"base":{"pt":"space-4","bg":"surface"},"sm":{"pt":"space-2"}}}}}}`

func presetsRig(t *testing.T) (*rig, themeSite, *int) {
	t.Helper()
	_, pk, fp := newPackageRig(t)
	site := themeSite{newFakeSite("Acme Site")}
	site.cfg.ThemeExtra = json.RawMessage(storedBlob)
	flushed := new(int)
	g := newRig(t, func(d *Deps) {
		d.Site = site
		d.Growth.Plugins, d.Growth.Packages = fp, pk
		d.Invalidate = func(context.Context) { *flushed++ }
	})
	return g, site, flushed
}

func design(t *testing.T, site themeSite) builder.Design {
	t.Helper()
	d, err := builder.ParseDesign(site.cfg.ThemeExtra)
	if err != nil {
		t.Fatalf("stored design does not parse cleanly: %v", err)
	}
	return d
}

func TestPresetsPageListsAndSavesWithoutTouchingOtherKeys(t *testing.T) {
	t.Parallel()

	g, site, flushed := presetsRig(t)
	rec := g.do("GET", "/admin/theme/presets", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="label.card"`) || !strings.Contains(body, `name="s.card.base.pt" value="space-4"`) ||
		!strings.Contains(body, `list="dl-pt"`) || !strings.Contains(body, `<datalist id="dl-pt">`) || !strings.Contains(body, `<option value="space-4">`) {
		t.Fatalf("page: %d\n%s", rec.Code, body)
	}
	a11yOK(t, body)
	compareGolden(t, "pages/settings-design-presets.html", normalizePage(body))

	form := url.Values{"label.card": {"Card v2"}, "s.card.base.pt": {"space-6"}, "s.card.md.pt": {"space-5"}, "s.card.base.shadow": {"md"}}
	if rec := g.do("POST", "/admin/theme/presets", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if *flushed != 1 {
		t.Errorf("purged %d times", *flushed)
	}
	d := design(t, site)
	p := d.Presets["card"]
	// A cell that is not on the form keeps its stored value; only an empty posted cell clears.
	if p.Label != "Card v2" || p.S["base"]["pt"] != "space-6" || p.S["md"]["pt"] != "space-5" || p.S["base"]["shadow"] != "md" || p.S["base"]["bg"] != "surface" || p.S["sm"]["pt"] != "space-2" {
		t.Fatalf("preset: %+v", p)
	}
	if rec := g.do("POST", "/admin/theme/presets", "owner-token", url.Values{"label.card": {"Card v2"}, "s.card.sm.pt": {""}}, nil); rec.Code != 303 {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	if p := design(t, site).Presets["card"]; p.S["sm"]["pt"] != "" || p.S["base"]["bg"] != "surface" || len(p.S["sm"]) != 0 {
		t.Fatalf("after clearing one cell: %+v", p)
	}
	if d.Tokens.Space["4"] != "2rem" || !strings.Contains(string(site.cfg.ThemeExtra), `"colors":{"accent":"#778899"}`) {
		t.Fatalf("tokens or other keys lost: %s", site.cfg.ThemeExtra)
	}
}

func TestPresetsSaveInvalidValueMapsToField(t *testing.T) {
	t.Parallel()

	g, site, flushed := presetsRig(t)
	before := string(site.cfg.ThemeExtra)
	form := url.Values{"label.card": {"Card"}, "s.card.base.pt": {"red;}</style>"}, "s.card.base.bg": {"surface"}}
	rec := g.do("POST", "/admin/theme/presets", "owner-token", form, nil)
	body := rec.Body.String()
	if rec.Code != 422 || strings.Count(body, "hm-field__error") != 1 || !strings.Contains(body, `<details open>`) {
		t.Fatalf("invalid: %d errors=%d", rec.Code, strings.Count(body, "hm-field__error"))
	}
	if strings.Contains(body, "red;}</style>") {
		t.Error("hostile value echoed unescaped")
	}
	a11yOK(t, body)
	// a preset with no style at all is refused too
	rec = g.do("POST", "/admin/theme/presets", "owner-token", url.Values{"label.card": {"Card"}, "s.card.base.pt": {""}, "s.card.base.bg": {""}, "s.card.sm.pt": {""}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "no style set") {
		t.Errorf("empty preset: %d", rec.Code)
	}
	if string(site.cfg.ThemeExtra) != before || *flushed != 0 {
		t.Error("an invalid save changed state")
	}
}

func TestPresetsAddAndDelete(t *testing.T) {
	t.Parallel()

	g, site, flushed := presetsRig(t)
	add := url.Values{"new_name": {"hero-band"}, "new_label": {"Hero band"}, "new_key": {"bg"}, "new_value": {"accent"}}
	if rec := g.do("POST", "/admin/theme/presets/new", "owner-token", add, nil); rec.Code != 303 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	d := design(t, site)
	if d.Presets["hero-band"].S["base"]["bg"] != "accent" || d.Presets["card"].Label != "Card" || *flushed != 1 {
		t.Fatalf("after add: %+v purges=%d", d.Presets, *flushed)
	}
	for what, f := range map[string]url.Values{
		"duplicate": {"new_name": {"card"}, "new_label": {"x"}, "new_key": {"pt"}, "new_value": {"space-1"}},
		"bad name":  {"new_name": {"Bad Name"}, "new_label": {"x"}, "new_key": {"pt"}, "new_value": {"space-1"}},
		"bad value": {"new_name": {"ok"}, "new_label": {"x"}, "new_key": {"pt"}, "new_value": {"nope;"}},
	} {
		rec := g.do("POST", "/admin/theme/presets/new", "owner-token", f, nil)
		if rec.Code != 422 || strings.Count(rec.Body.String(), "hm-field__error") != 1 {
			t.Errorf("%s: %d errors=%d", what, rec.Code, strings.Count(rec.Body.String(), "hm-field__error"))
		}
		a11yOK(t, rec.Body.String())
	}

	rec := g.do("GET", "/admin/theme/presets/hero-band/delete", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Pages that use it lose its styles") {
		t.Fatalf("confirm: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/theme/presets/hero-band/delete", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	d = design(t, site)
	if _, ok := d.Presets["hero-band"]; ok || d.Presets["card"].Label != "Card" || *flushed != 2 {
		t.Fatalf("after delete: %+v purges=%d", d.Presets, *flushed)
	}
	if rec := g.do("GET", "/admin/theme/presets/nope/delete", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown preset: %d", rec.Code)
	}
}

func TestPresetsPagesAreOwnerOnly(t *testing.T) {
	t.Parallel()

	g, _, _ := presetsRig(t)
	for _, c := range []struct{ m, p string }{
		{"GET", "/admin/theme/presets"}, {"POST", "/admin/theme/presets"}, {"POST", "/admin/theme/presets/new"},
		{"GET", "/admin/theme/presets/card/delete"}, {"POST", "/admin/theme/presets/card/delete"},
	} {
		if rec := g.do(c.m, c.p, "editor-token", url.Values{}, nil); rec.Code != 403 {
			t.Errorf("editor %s %s: %d", c.m, c.p, rec.Code)
		}
	}
}

// doBody posts a JSON body with the session and CSRF cookies of rig.do.
func (g *rig) doBody(method, target, token, body string, hdr map[string]string) *httptest.ResponseRecorder {
	g.t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	}
	g.addFetchMeta(req, token, hdr)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func TestEditorCreatesPresetOnly(t *testing.T) {
	t.Parallel()

	g, site, flushed := presetsRig(t)
	ok := `{"name":"promo","label":"Promo","s":{"base":{"pt":"space-3","bg":"accent"},"md":{"pt":"space-2"}}}`
	for _, token := range []string{"editor-token", "owner-token"} {
		name := "promo-" + token[:5]
		body := strings.Replace(ok, "promo", name, 1)
		rec := g.doBody("POST", "/admin/builder/presets", token, body, nil)
		if rec.Code != 201 {
			t.Fatalf("%s create: %d %s", token, rec.Code, rec.Body.String())
		}
		var got presetReply
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Name != name || got.Label != "Promo" || got.S["base"]["bg"] != "accent" {
			t.Fatalf("reply: %+v", got)
		}
	}
	if *flushed != 2 {
		t.Errorf("purged %d times", *flushed)
	}
	d := design(t, site)
	if len(d.Presets) != 3 || d.Tokens.Space["4"] != "2rem" || d.Presets["card"].Label != "Card" || !strings.Contains(string(site.cfg.ThemeExtra), `"colors"`) {
		t.Fatalf("merge lost data: %s", site.cfg.ThemeExtra)
	}
	// 409: the existing preset is untouched
	rec := g.doBody("POST", "/admin/builder/presets", "editor-token", `{"name":"card","label":"Hijack","s":{"base":{"pt":"space-1"}}}`, nil)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body.String())
	}
	if design(t, site).Presets["card"].Label != "Card" {
		t.Error("an editor overwrote a preset")
	}
	for what, body := range map[string]string{
		"bad name":  `{"name":"Bad_Name","s":{"base":{"pt":"space-1"}}}`,
		"bad value": `{"name":"x","s":{"base":{"bg":"red;}"}}}`,
		"no style":  `{"name":"x","s":{}}`,
		"long":      `{"name":"x","label":"` + strings.Repeat("a", 61) + `","s":{"base":{"pt":"space-1"}}}`,
	} {
		if rec := g.doBody("POST", "/admin/builder/presets", "editor-token", body, nil); rec.Code != 422 {
			t.Errorf("%s: %d", what, rec.Code)
		}
	}
	if rec := g.doBody("POST", "/admin/builder/presets", "editor-token", `{nope`, nil); rec.Code != 400 {
		t.Errorf("bad json: %d", rec.Code)
	}
	if rec := g.doBody("POST", "/admin/builder/presets", "editor-token", ok, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("no CSRF token: %d", rec.Code)
	}
	if rec := g.doBody("POST", "/admin/builder/presets", "", ok, nil); rec.Code == 201 {
		t.Error("anonymous create succeeded")
	}
}

func TestEditorPresetLimit(t *testing.T) {
	t.Parallel()

	g, site, _ := presetsRig(t)
	for i := 0; i < 47; i++ {
		name := "p" + strings.Repeat("a", i/10) + string(rune('a'+i%10))
		if rec := g.doBody("POST", "/admin/builder/presets", "owner-token", `{"name":"`+name+`","s":{"base":{"pt":"space-1"}}}`, nil); rec.Code != 201 {
			t.Fatalf("preset %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if len(design(t, site).Presets) != 48 {
		t.Fatalf("setup: %d presets", len(design(t, site).Presets))
	}
	if rec := g.doBody("POST", "/admin/builder/presets", "owner-token", `{"name":"overflow","s":{"base":{"pt":"space-1"}}}`, nil); rec.Code != 400 {
		t.Fatalf("49th: %d", rec.Code)
	}
}

func TestBuilderBootCarriesDesign(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	site := themeSite{newFakeSite("Acme")}
	site.cfg.ThemeExtra = json.RawMessage(storedBlob)
	r.a.deps.Site = site
	r.a.deps.BuilderTokens = func() bool { return true }
	r.ent.Attributes = json.RawMessage(builderOnlyDoc)
	rec := r.do("GET", r.path(), "owner-token", nil, nil)
	body := rec.Body.String()
	const open = `id="hm-builder-boot">`
	i := strings.Index(body, open)
	if rec.Code != 200 || i < 0 {
		t.Fatalf("builder screen: %d", rec.Code)
	}
	rest := body[i+len(open):]
	var boot struct {
		Design bootDesign        `json:"design"`
		URLs   map[string]string `json:"urls"`
	}
	if err := json.Unmarshal([]byte(rest[:strings.Index(rest, "</script>")]), &boot); err != nil {
		t.Fatal(err)
	}
	d := boot.Design
	if !d.Tokens || !d.CanCreate || len(d.Presets) != 1 || d.Presets[0].Name != "card" || d.Presets[0].Label != "Card" || d.Presets[0].S["base"]["pt"] != "space-4" {
		t.Fatalf("boot.design: %+v", d)
	}
	if d.Palette == nil || d.Palette.Space["4"] != "2rem" || d.Palette.Colors["surface"] == "" || d.Palette.FS["xs"] == "" {
		t.Errorf("boot.design.palette: %+v", d.Palette)
	}
	if boot.URLs["presets"] != "/admin/builder/presets" {
		t.Errorf("urls.presets = %q", boot.URLs["presets"])
	}
}

func TestPresetSetLineCountsFilledSettings(t *testing.T) {
	t.Parallel()

	two := presetEntry{name: "card", preset: builder.Preset{Label: "Card", S: map[string]map[string]string{"base": {"pt": "space-4", "radius": "8px"}, "md": {"pt": ""}}}}
	none := presetEntry{name: "empty", preset: builder.Preset{Label: "Empty"}}
	d := presetsView([]presetEntry{two, none}, nil, nil)
	if d.Presets[0].SetLine != "2 settings set" || d.Presets[1].SetLine != "0 settings set" {
		t.Errorf("set lines %q, %q", d.Presets[0].SetLine, d.Presets[1].SetLine)
	}
}

// A key cleared in a submit that fails elsewhere stays listed, so resubmitting still clears it,
// and the add row keeps what was typed.
func TestPresetsClearThenFailThenResubmit(t *testing.T) {
	t.Parallel()

	g, site, _ := presetsRig(t)
	bad := url.Values{"label.card": {"Card"}, "s.card.base.pt": {""}, "s.card.sm.pt": {""}, "s.card.base.bg": {"red;}"}, "add.card.key": {"shadow"}}
	rec := g.do("POST", "/admin/theme/presets", "owner-token", bad, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, `name="s.card.base.pt"`) {
		t.Fatalf("failed save should still list the cleared pt: %d\n%s", rec.Code, body)
	}
	if !strings.Contains(body, `<option value="shadow" selected`) {
		t.Errorf("add row lost its input:\n%s", body)
	}
	fixed := url.Values{"label.card": {"Card"}, "s.card.base.pt": {""}, "s.card.sm.pt": {""}, "s.card.base.bg": {"muted"}}
	if rec := g.do("POST", "/admin/theme/presets", "owner-token", fixed, nil); rec.Code != 303 {
		t.Fatalf("resubmit: %d %s", rec.Code, rec.Body.String())
	}
	if p := design(t, site).Presets["card"]; p.S["base"]["pt"] != "" || p.S["sm"]["pt"] != "" || p.S["base"]["bg"] != "muted" {
		t.Fatalf("the clear was undone: %+v", p)
	}
}

// State styles of a preset (base:hover ...) are edited in the builder: the page
// lists no row for them, notes them, and a save keeps them.
func TestPresetsPageNotesStatesAndKeepsThem(t *testing.T) {
	t.Parallel()

	g, site, _ := presetsRig(t)
	site.cfg.ThemeExtra = json.RawMessage(`{"builder":{"presets":{"card":{"label":"Card","s":{` +
		`"base":{"pt":"space-4"},"base:hover":{"ty":"-4px","shadow":"lg","bg":"accent"},"sm:focus":{"bc":"accent"}}}}}}`)
	if p := design(t, site).Presets["card"]; p.S["base:hover"]["ty"] != "-4px" {
		t.Fatalf("setup: %+v", p)
	}
	rec := g.do("GET", "/admin/theme/presets", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Also has state styles: Hover (3), Focus (1). Edit them in the page editor.") {
		t.Fatalf("no state note (%d)", rec.Code)
	}
	for _, phantom := range []string{`name="s.card.base.ty"`, `name="s.card.md.ty"`, `name="s.card.sm.ty"`, `name="s.card.base.bc"`, `name="s.card.sm.bc"`, "base:hover"} {
		if strings.Contains(body, phantom) {
			t.Errorf("page lists %s for a state-only key", phantom)
		}
	}
	if !strings.Contains(body, `name="s.card.base.pt" value="space-4"`) || !strings.Contains(body, "1 setting set") {
		t.Error("plain settings are not listed or counted")
	}
	save := url.Values{"label.card": {"Card 2"}, "s.card.base.pt": {"space-5"}}
	if rec := g.do("POST", "/admin/theme/presets", "owner-token", save, nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	p := design(t, site).Presets["card"]
	if p.Label != "Card 2" || p.S["base"]["pt"] != "space-5" || p.S["base:hover"]["ty"] != "-4px" || p.S["base:hover"]["bg"] != "accent" || p.S["sm:focus"]["bc"] != "accent" {
		t.Fatalf("save dropped a state style: %+v", p)
	}
}

// The editor's "Save as preset" carries states; parent-hover is refused.
func TestEditorPresetKeepsStatesRefusesParentHover(t *testing.T) {
	t.Parallel()

	g, site, _ := presetsRig(t)
	ok := `{"name":"lift","label":"Lift","s":{"base":{"shadow":"sm"},"base:hover":{"ty":"-4px","shadow":"lg"}}}`
	if rec := g.doBody("POST", "/admin/builder/presets", "editor-token", ok, nil); rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if p := design(t, site).Presets["lift"]; p.S["base:hover"]["ty"] != "-4px" {
		t.Errorf("states lost: %+v", p)
	}
	for what, body := range map[string]string{
		"parent-hover": `{"name":"ph","s":{"base:parent-hover":{"bg":"accent"}}}`,
		"bad state":    `{"name":"bs","s":{"base:visited":{"bg":"accent"}}}`,
		"layout key":   `{"name":"lk","s":{"base:hover":{"pt":"space-1"}}}`,
	} {
		if rec := g.doBody("POST", "/admin/builder/presets", "editor-token", body, nil); rec.Code != 422 {
			t.Errorf("%s: %d", what, rec.Code)
		}
	}
}

func TestBuilderPaletteCoversTheTableTokens(t *testing.T) {
	t.Parallel()

	site := newFakeSite("Acme")
	site.cfg.ThemeExtra = json.RawMessage(`{"colors":{"primary":"#112233","background":"#fafafa"},"builder":{"tokens":{"color":{"surface":"#010203"},"space":{"4":"2rem"},"fs":{"xl":"1.5rem"}}}}`)
	p := builderPalette(&site.cfg)
	for k, want := range map[string]string{"primary": "#112233", "bg": "#fafafa", "surface": "#010203", "muted": "#eef0f4", "accent": "#c8102e"} {
		if p.Colors[k] != want {
			t.Errorf("colour %s = %q, want %q", k, p.Colors[k], want)
		}
	}
	if p.Space["4"] != "2rem" || p.Space["0"] == "" || p.FS["xl"] != "1.5rem" {
		t.Errorf("token overrides missing: space %v fs %v", p.Space, p.FS)
	}
	for _, e := range builder.StyleSpecTable() {
		for name := range e.Tokens {
			switch e.Kind {
			case "color":
				if p.Colors[name] == "" {
					t.Errorf("%s: colour token %q has no palette value", e.Key, name)
				}
			case "space":
				if n, ok := strings.CutPrefix(name, "space-"); ok && p.Space[n] == "" {
					t.Errorf("%s: space token %q has no palette value", e.Key, name)
				}
			case "fs":
				if p.FS[name] == "" {
					t.Errorf("%s: font-size token %q has no palette value", e.Key, name)
				}
			}
		}
	}
	for n := 0; n <= 12; n++ {
		if p.Space[strconv.Itoa(n)] == "" {
			t.Errorf("space %d missing", n)
		}
	}
	for k, v := range p.Colors {
		if !render.ValidColor(v) {
			t.Errorf("colour %s = %q is not a valid colour", k, v)
		}
	}
}

func TestBuilderPaletteSkipsInvalidThemeColours(t *testing.T) {
	site := newFakeSite("Acme")
	site.cfg.ThemeExtra = json.RawMessage(`{"colors":{"primary":"","background":"red;x","text":"#abc123"}}`)
	p := builderPalette(&site.cfg)
	d := siteconfig.DefaultThemeConfig().Colors
	if p.Colors["bg"] != d.Background || p.Colors["text"] != "#abc123" || p.Colors["primary"] == "" {
		t.Errorf("invalid theme colours reached the palette: %+v", p.Colors)
	}
}

func TestBuilderPaletteWithoutSiteIsTheDefaults(t *testing.T) {
	p := builderPalette(nil)
	d := builder.DefaultTokens()
	if p.Colors["surface"] != d.Color["surface"] || p.Colors["primary"] == "" || p.Colors["bg"] == "" || p.Colors["text"] == "" || p.Space["4"] != d.Space["4"] || p.FS["2xl"] != d.FS["2xl"] {
		t.Errorf("defaults: %+v", p)
	}
}
