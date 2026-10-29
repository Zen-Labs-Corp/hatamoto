package render

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func mapFile(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func embeddedFixture() fstest.MapFS {
	return fstest.MapFS{
		"theme.json":           mapFile(`{"name":"default","version":"1.0.0"}`),
		"layouts/base.html":    mapFile("embedded-layout"),
		"partials/header.html": mapFile("embedded-partial"),
		"sections/hero.html":   mapFile(`{{define "section-hero"}}embedded-hero{{end}}`),
		"sections/faq.html":    mapFile(`{{define "section-faq"}}faq{{end}}`),
		"schemas/hero.json":    mapFile(`{"x-hm-section":{"title":"Embedded"},"type":"object","properties":{}}`),
		"schemas/faq.json":     mapFile(`{"x-hm-section":{"title":"FAQ"},"type":"object","properties":{"heading":{"type":"string","x-hm-alias":["headline"],"x-hm-field":"plaintext"}}}`),
	}
}

func TestChainLookupOrder(t *testing.T) {
	t.Parallel()
	site := fstest.MapFS{
		"layouts/base.html":    mapFile("site-layout"),
		"partials/header.html": mapFile("site-partial"),
		"schemas/hero.json":    mapFile(`{"x-hm-section":{"title":"Site"},"type":"object","properties":{}}`),
	}
	disk := fstest.MapFS{
		"layouts/base.html":    mapFile("disk-layout"),
		"partials/header.html": mapFile("disk-partial"),
		"schemas/hero.json":    mapFile(`{"x-hm-section":{"title":"Disk"},"type":"object","properties":{}}`),
		"partials/footer.html": mapFile("disk-footer"),
	}
	emb := embeddedFixture()

	tests := []struct {
		name  string
		chain Chain
		file  string
		want  string
	}{
		{"layout site wins", Chain{site, disk, emb}, "layouts/base.html", "site-layout"},
		{"layout disk beats embedded", Chain{disk, emb}, "layouts/base.html", "disk-layout"},
		{"layout embedded fallback", Chain{emb}, "layouts/base.html", "embedded-layout"},
		{"partial site wins", Chain{site, disk, emb}, "partials/header.html", "site-partial"},
		{"partial disk beats embedded", Chain{disk, emb}, "partials/header.html", "disk-partial"},
		{"partial only on disk", Chain{site, disk, emb}, "partials/footer.html", "disk-footer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.chain.ReadFile(tt.file)
			if err != nil || string(got) != tt.want {
				t.Fatalf("got %q err=%v want %q", got, err, tt.want)
			}
		})
	}
	if _, err := (Chain{site, disk, emb}).ReadFile("nope.html"); err == nil {
		t.Fatal("expected not-exist error")
	}

	// Schema precedence through LoadTheme.
	th, err := LoadTheme(Chain{site, disk, emb})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.Sections["hero"].Meta.Title; got != "Site" {
		t.Fatalf("schema precedence: %q", got)
	}
	th, _ = LoadTheme(Chain{disk, emb})
	if got := th.Sections["hero"].Meta.Title; got != "Disk" {
		t.Fatalf("schema precedence: %q", got)
	}
	th, _ = LoadTheme(Chain{emb})
	if got := th.Sections["hero"].Meta.Title; got != "Embedded" {
		t.Fatalf("schema precedence: %q", got)
	}
}

func TestThemeSectionTemplateFromDiskDirs(t *testing.T) {
	t.Parallel()
	site := os.DirFS("testdata/themes/site")
	disk := os.DirFS("testdata/themes/override")
	emb := embeddedFixture()

	tests := []struct {
		name     string
		chain    Chain
		wantHero string
		wantName string
	}{
		{"site beats disk beats embedded", Chain{site, disk, emb}, "site-hero", "override"},
		{"disk beats embedded", Chain{disk, emb}, "disk-hero", "override"},
		{"embedded only", Chain{emb}, "embedded-hero", "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			th, err := LoadTheme(tt.chain)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(th.Sections["hero"].Template, tt.wantHero) {
				t.Fatalf("hero template: %q", th.Sections["hero"].Template)
			}
			if th.Name != tt.wantName {
				t.Fatalf("name %q", th.Name)
			}
			if th.Sections["faq"] == nil {
				t.Fatal("faq section from embedded layer missing")
			}
		})
	}
}

func TestSchemaAliasesAndMeta(t *testing.T) {
	t.Parallel()
	th, err := LoadTheme(Chain{embeddedFixture()})
	if err != nil {
		t.Fatal(err)
	}
	faq := th.Sections["faq"]
	if got := faq.Aliases["heading"]; len(got) != 1 || got[0] != "headline" {
		t.Fatalf("aliases: %v", faq.Aliases)
	}
	if faq.FieldKinds["heading"] != "plaintext" || faq.Meta.Title != "FAQ" {
		t.Fatalf("kinds=%v meta=%+v", faq.FieldKinds, faq.Meta)
	}
}

func TestValidateSchemaSubset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		schema  string
		wantErr bool
	}{
		{"ok", `{"type":"object","properties":{"a":{"type":"string","x-hm-field":"image"}}}`, false},
		{"property named oneOf is fine", `{"type":"object","properties":{"oneOf":{"type":"string"}}}`, false},
		{"ref", `{"$ref":"#/x"}`, true},
		{"nested oneOf", `{"type":"object","properties":{"a":{"oneOf":[{"type":"string"}]}}}`, true},
		{"anyOf in items", `{"type":"array","items":{"anyOf":[]}}`, true},
		{"allOf", `{"allOf":[]}`, true},
		{"patternProperties", `{"patternProperties":{}}`, true},
		{"bad field kind", `{"type":"object","properties":{"a":{"x-hm-field":"wysiwyg"}}}`, true},
		{"not an object", `[1]`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateSchemaSubset([]byte(tt.schema))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadThemeRejectsInvalid(t *testing.T) {
	t.Parallel()
	bad := fstest.MapFS{
		"sections/x.html": mapFile("x"),
		"schemas/x.json":  mapFile(`{"$ref":"#"}`),
	}
	if _, err := LoadTheme(Chain{bad}); err == nil {
		t.Fatal("expected schema subset error")
	}
	orphan := fstest.MapFS{"schemas/y.json": mapFile(`{"type":"object"}`)}
	if _, err := LoadTheme(Chain{orphan}); err == nil {
		t.Fatal("expected orphan schema error")
	}
	badSettings := fstest.MapFS{"theme.json": mapFile(`{"name":"n","settings_schema":{"oneOf":[]}}`)}
	if _, err := LoadTheme(Chain{badSettings}); err == nil {
		t.Fatal("expected settings_schema error")
	}
}

func TestVersionHash(t *testing.T) {
	t.Parallel()
	a := embeddedFixture()
	th1, _ := LoadTheme(Chain{a})
	th1b, _ := LoadTheme(Chain{embeddedFixture()})
	if th1.VersionHash() == "" || th1.VersionHash() != th1b.VersionHash() {
		t.Fatalf("hash not stable: %q vs %q", th1.VersionHash(), th1b.VersionHash())
	}
	b := embeddedFixture()
	b["partials/header.html"] = mapFile("changed")
	th2, _ := LoadTheme(Chain{b})
	if th1.VersionHash() == th2.VersionHash() {
		t.Fatal("hash must change when a file changes")
	}
	// A shadowed (lower-precedence) file change must not affect the hash.
	top := fstest.MapFS{"partials/header.html": mapFile("top")}
	c1, _ := LoadTheme(Chain{top, a})
	c2, _ := LoadTheme(Chain{top, b})
	if c1.VersionHash() != c2.VersionHash() {
		t.Fatal("shadowed file change should not alter the hash")
	}
}

func loadMenuDecl(t *testing.T, themeJSON string) MenuDecl {
	t.Helper()
	th, err := LoadTheme(Chain{fstest.MapFS{"theme.json": {Data: []byte(themeJSON)}}})
	if err != nil {
		t.Fatal(err)
	}
	return th.Menu
}

func TestMenuDeclParsesAndResolves(t *testing.T) {
	d := loadMenuDecl(t, `{"name":"t","menu":{
		"styles":[{"id":"button","label":"Button","class":"btn  btn-primary"}],
		"colors":[{"id":"red","label":"Red","class":"c-red","swatch":"#FF0000"}],
		"badges":[{"id":"new","label":"New","class":"b-new"}],
		"icons":[{"id":"arrow","label":"Arrow","class":"ignored"}],
		"classes":["footer-cta","footer-cta","x"]}}`)
	if got := d.ClassFor("style", "button"); got != "btn btn-primary" {
		t.Errorf("style class = %q", got)
	}
	if d.ClassFor("color", "red") != "c-red" || d.ClassFor("badge", "new") != "b-new" {
		t.Errorf("colour/badge class not resolved: %+v", d)
	}
	if d.ClassFor("style", "nope") != "" || d.ClassFor("icon", "arrow") != "" || d.ClassFor("other", "button") != "" {
		t.Error("unknown id or kind must resolve to nothing")
	}
	if len(d.Icons) != 1 || d.Icons[0].Class != "" {
		t.Errorf("icons = %+v, want one without a class", d.Icons)
	}
	if len(d.Classes) != 2 {
		t.Errorf("classes = %v, want duplicates dropped", d.Classes)
	}
	if d.IsZero() {
		t.Error("IsZero on a filled declaration")
	}
}

func TestMenuDeclDropsInvalidEntries(t *testing.T) {
	var many strings.Builder
	many.WriteString(`[`)
	for i := 0; i < 40; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"id":"s` + strings.Repeat("a", i%3) + string(rune('a'+i%26)) + `","label":"L","class":"c` + string(rune('a'+i%26)) + `"}`)
	}
	many.WriteString(`]`)
	d := loadMenuDecl(t, `{"name":"t","menu":{
		"styles":[
			{"id":"Bad Id","label":"x","class":"a"},
			{"id":"ok","label":"Ok","class":"a"},
			{"id":"ok","label":"Dup","class":"b"},
			{"id":"nolabel","label":" ","class":"a"},
			{"id":"noclass","label":"x"},
			{"id":"inject","label":"x","class":"a\" onclick=\"x"},
			{"id":"digit","label":"x","class":"1a"}],
		"colors":[
			{"id":"nosw","label":"x","class":"a"},
			{"id":"badsw","label":"x","class":"a","swatch":"red"},
			{"id":"short","label":"x","class":"a","swatch":"#fff"},
			{"id":"good","label":"x","class":"a","swatch":"#0a0B0c"}],
		"badges":`+many.String()+`,
		"classes":["ok","bad class","1x","a;b"]}}`)
	if len(d.Styles) != 1 || d.Styles[0].ID != "ok" || d.Styles[0].Label != "Ok" {
		t.Errorf("styles = %+v, want only the first ok", d.Styles)
	}
	if len(d.Colors) != 1 || d.Colors[0].ID != "good" {
		t.Errorf("colors = %+v, want only good", d.Colors)
	}
	if len(d.Badges) != maxMenuPresets {
		t.Errorf("badges = %d, want capped at %d", len(d.Badges), maxMenuPresets)
	}
	if len(d.Classes) != 1 || d.Classes[0] != "ok" {
		t.Errorf("classes = %v, want [ok]", d.Classes)
	}
}

func TestMenuDeclMalformedIsIgnored(t *testing.T) {
	for _, js := range []string{`{"name":"t"}`, `{"name":"t","menu":"x"}`, `{"name":"t","menu":{"styles":"x"}}`, `{"name":"t","menu":null}`} {
		if d := loadMenuDecl(t, js); !d.IsZero() {
			t.Errorf("%s: decl = %+v, want zero", js, d)
		}
	}
}

func TestEngineMenuDecl(t *testing.T) {
	def, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	d := def.MenuDecl()
	if d.ClassFor("style", "button") == "" || len(d.Colors) == 0 || len(d.Badges) == 0 || len(d.Icons) == 0 || len(d.Classes) == 0 {
		t.Errorf("default theme declares too little: %+v", d)
	}
	for _, c := range d.Colors {
		if c.Swatch == "" {
			t.Errorf("colour %s has no swatch", c.ID)
		}
	}

	over, err := New(Options{Layers: []fs.FS{fstest.MapFS{"theme.json": {Data: []byte(`{"name":"plain","version":"1.0.0"}`)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !over.MenuDecl().IsZero() {
		t.Errorf("a theme without menu declares %+v", over.MenuDecl())
	}
}
