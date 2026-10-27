package render

import (
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
