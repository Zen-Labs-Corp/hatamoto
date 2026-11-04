package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/pluginhost"
)

const tagManifest = `{"name":"acme","version":"1.0.0","sdk":"^0.11"}`

const tagDecl = `{"name":"Acme","id":{"label":"Site ID","pattern":"^[0-9]{4,9}$"},"script":"https://cdn.acme.example/{id}.js","hosts":["cdn.acme.example"],"category":"analytics"}`

func tagPackFiles() map[string][]byte {
	return map[string][]byte{PluginManifest: []byte(tagManifest), "tags/acme.json": []byte(tagDecl)}
}

func TestTagPackExamplesValidate(t *testing.T) {
	want := map[string]struct {
		key, label, category string
		hosts                int
	}{
		"hotjar":    {"hotjar.hotjar", "Hotjar", "analytics", 2},
		"plausible": {"plausible.plausible", "Plausible Analytics", "analytics", 1},
		"reddit":    {"reddit.reddit", "Reddit Pixel", "marketing", 3},
	}
	for name, w := range want {
		t.Run(name, func(t *testing.T) {
			raw, err := PackDir(filepath.Join("testdata", "tagpacks", name), nil)
			if err != nil {
				t.Fatal(err)
			}
			p, err := Parse(raw, DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if p.Kind != KindPlugin || p.Name != name || p.Has(PluginWasm) {
				t.Fatalf("package = %s %s", p.Kind, p.Name)
			}
			m, err := p.PluginManifest()
			if err != nil {
				t.Fatal(err)
			}
			if err := pluginhost.SDKRefusal(m); err != nil {
				t.Fatal(err)
			}
			if err := validateTagPack(p); err != nil {
				t.Fatal(err)
			}
			vs, err := p.TagVendors()
			if err != nil || len(vs) != 1 {
				t.Fatalf("vendors = %+v, %v", vs, err)
			}
			v := vs[0]
			if v.ID != w.key || v.Label != w.label || v.Category != w.category || v.Boot != "generic" {
				t.Errorf("vendor = %+v", v)
			}
			if got := len(v.Script); got != w.hosts {
				t.Errorf("hosts = %d, want %d", got, w.hosts)
			}
			line, err := TagSummaryLine(p)
			if err != nil || !strings.HasPrefix(line, "1 tag: "+name+", hosts ") {
				t.Errorf("summary = %q, %v", line, err)
			}
		})
	}
}

func TestTagPackParsesAndHasNoSummaryWithoutTags(t *testing.T) {
	raw, err := PackFiles(tagPackFiles(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := p.TagVendors(); err != nil || len(vs) != 1 || vs[0].ID != "acme.acme" {
		t.Fatalf("vendors = %+v, %v", vs, err)
	}
	wasm, _ := PackFiles(map[string][]byte{PluginManifest: []byte(tagManifest), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")}, nil)
	p2, err := Parse(wasm, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if line, err := TagSummaryLine(p2); line != "" || err != nil {
		t.Fatalf("summary = %q, %v", line, err)
	}
}

func TestTagPackRefusesMixedContent(t *testing.T) {
	m, tag := hdr(PluginManifest, tagManifest), hdr("tags/acme.json", tagDecl)
	for _, tc := range []struct{ file, body, found string }{
		{PluginWasm, "\x00asm", "plugin.wasm"},
		{"widgets/x/schema.json", "{}", "widgets/"},
		{"widgets/", "", "widgets/"},
		{"ui/index.html", "x", "ui/"},
		{"assets/a.png", "x", "assets/"},
		{"sections/a.html", "x", "sections/"},
		{"schemas/a.json", "{}", "schemas/"},
		{"layouts/a.html", "x", "layouts/"},
		{"partials/a.html", "x", "partials/"},
		{"migrations/1.sql", "x", "migrations/"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			mustRefuse(t, rawZip(t, m, tag, hdr(tc.file, tc.body)), "a tag pack holds only plugin.json and tags/*.json (found "+tc.found+")")
		})
	}
	// README and LICENSE next to the pack are fine.
	if _, err := Parse(rawZip(t, m, tag, hdr("README.md", "x"), hdr("LICENSE", "x")), DefaultLimits); err != nil {
		t.Fatal(err)
	}
}

func TestTagPackRefusesCapabilityManifest(t *testing.T) {
	tag := hdr("tags/acme.json", tagDecl)
	for field, body := range map[string]string{
		"Permissions":    `"permissions":["kv"]`,
		"AllowedHosts":   `"allowed_hosts":["a.example"]`,
		"CSP":            `"csp":{"script_src":["https://evil.example"]}`,
		"Routes":         `"routes":[{"path":"/x","methods":["GET"]}]`,
		"Cron":           `"cron":[{"name":"x","every":"1h"}]`,
		"Cookies":        `"cookies":["a"]`,
		"RequestHeaders": `"request_headers":["X-A"]`,
		"Funcs":          `"funcs":["f"]`,
		"Config":         `"config":{"a":"b"}`,
		"Limits":         `"limits":{"memory_pages":10}`,
	} {
		t.Run(field, func(t *testing.T) {
			manifest := `{"name":"acme","version":"1.0.0","sdk":"^0.11",` + body + `}`
			mustRefuse(t, rawZip(t, hdr(PluginManifest, manifest), tag), "declares only name, version, sdk and description (found "+field+")")
		})
	}
	// Empty lists are not capabilities.
	ok := `{"name":"acme","version":"1.0.0","sdk":"^0.11","description":"d","permissions":[]}`
	if _, err := Parse(rawZip(t, hdr(PluginManifest, ok), tag), DefaultLimits); err != nil {
		t.Fatal(err)
	}
}

func TestTagPackLayoutRules(t *testing.T) {
	m := hdr(PluginManifest, tagManifest)
	mustRefuse(t, rawZip(t, m, hdr("tags/", "")), "holds no tag files")
	mustRefuse(t, rawZip(t, m, hdr("tags/acme.txt", "x")), "a tag pack holds only tags/<name>.json")
	mustRefuse(t, rawZip(t, m, hdr("tags/Acme.json", tagDecl)), "a tag pack holds only tags/<name>.json")
	mustRefuse(t, rawZip(t, m, hdr("tags/sub/a.json", tagDecl)), "a tag pack holds only tags/<name>.json")
	mustRefuse(t, rawZip(t, m, hdr("tags/a.json", tagDecl), hdr("tags/b.json", tagDecl), hdr("tags/c.json", tagDecl), hdr("tags/d.json", tagDecl), hdr("tags/e.json", tagDecl)), "at most 4 tag files")
	mustRefuse(t, rawZip(t, hdr(ThemeManifest, themeJSON), hdr("layouts/base.html", "x"), hdr("tags/a.json", tagDecl)), "theme package cannot hold tags/")
	// Four files parse (the vendors are validated later).
	if _, err := Parse(rawZip(t, m, hdr("tags/a.json", tagDecl), hdr("tags/b.json", tagDecl), hdr("tags/c.json", tagDecl), hdr("tags/d.json", tagDecl)), DefaultLimits); err != nil {
		t.Fatal(err)
	}
}

// Each rule of the declaration surfaces as "tags/<file>: <rule>" from validation.
func TestTagPackValidationErrorsNameTheFile(t *testing.T) {
	for name, tc := range map[string]struct{ decl, want string }{
		"foreign script host": {strings.Replace(tagDecl, "https://cdn.acme.example/", "https://evil.example/", 1), `tags/acme.json: script host "evil.example" is not in hosts`},
		"http script":         {strings.Replace(tagDecl, "https://cdn", "http://cdn", 1), "tags/acme.json: script must be an https URL"},
		"necessary":           {strings.Replace(tagDecl, "analytics", "necessary", 1), "tags/acme.json: category must be one of"},
		"bad pattern":         {strings.Replace(tagDecl, "^[0-9]{4,9}$", "[0-9]+", 1), "tags/acme.json: id.pattern must start with ^"},
		"unknown key":         {strings.Replace(tagDecl, `"name":"Acme",`, `"name":"Acme","js":"x",`, 1), "tags/acme.json: invalid JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			files := tagPackFiles()
			files["tags/acme.json"] = []byte(tc.decl)
			raw, err := PackFiles(files, nil)
			if err != nil {
				t.Fatal(err) // layout is fine; the rules run in validateTagPack
			}
			p, err := Parse(raw, DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateTagPack(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTagPackTooManyTagsFailsValidation(t *testing.T) {
	files := tagPackFiles()
	for _, n := range []string{"b", "c", "d", "e"} {
		files["tags/"+n+".json"] = []byte(tagDecl)
	}
	if _, err := PackFiles(files, nil); err == nil || !strings.Contains(err.Error(), "at most 4 tag files") {
		t.Fatalf("err = %v", err)
	}
}

func TestPackDirSkipsStrayFilesUnderTags(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		PluginManifest: tagManifest, "tags/acme.json": tagDecl, "tags/notes.txt": "x", "tags/sub/other.json": tagDecl,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := PackDir(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has("tags/acme.json") || p.Has("tags/notes.txt") || p.Has("tags/sub/other.json") {
		t.Fatal("PackDir packed the wrong tag files")
	}
}

// A tag pack carries no permission or CSP, so community policy has nothing to
// refuse: it is eligible for community authors.
func TestTagPackCommunityAllowed(t *testing.T) {
	raw, err := PackDir(filepath.Join("testdata", "tagpacks", "hotjar"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	p.Signature = &Signature{Signer: FirstPartyKeyIDs[0]}
	if got := RefusedPermissions(p); len(got) != 0 {
		t.Fatalf("community refused = %v", got)
	}
	if err := CheckCommunityCSP(p); err != nil {
		t.Fatal(err)
	}
}
