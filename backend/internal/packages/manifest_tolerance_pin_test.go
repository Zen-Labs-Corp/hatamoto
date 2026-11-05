package packages

import (
	"testing"
)

// External documents were read tolerantly: member names match without case,
// repeated names are accepted, unknown members are ignored and a bad UTF-8 byte
// becomes U+FFFD. Installing must keep working for packages built that way.
func TestExternalManifestsTolerant(t *testing.T) {
	t.Run("plugin.json", func(t *testing.T) {
		manifest := "{\"NAME\":\"hello\",\"name\":\"hello\",\"version\":\"1.0.0\",\"sdk\":\"^0.7\",\"description\":\"caf\xe9\",\"bogus\":1}"
		raw, err := PackFiles(map[string][]byte{PluginManifest: []byte(manifest), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		p := parse(t, raw)
		m, err := p.PluginManifest()
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "hello" || m.Version != "1.0.0" || m.Description != "caf\ufffd" {
			t.Fatalf("name %q manifest %+v", p.Name, m)
		}
	})
	t.Run("theme.json", func(t *testing.T) {
		manifest := "{\"Name\":\"sunrise\",\"version\":\"1.2.3\",\"VERSION\":\"1.2.3\",\"description\":\"x\xffy\",\"extra\":true}"
		raw, err := PackFiles(map[string][]byte{ThemeManifest: []byte(manifest), "layouts/base.html": []byte("<html></html>")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		p := parse(t, raw)
		m, err := p.ThemeMeta()
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "sunrise" || p.Version != "1.2.3" || m.Description != "x\ufffdy" {
			t.Fatalf("name %q version %q meta %+v", p.Name, p.Version, m)
		}
	})
	t.Run("signature.json", func(t *testing.T) {
		sig := "{\"SIGNER\":\"abc\",\"signer\":\"abc\",\"algorithm\":\"ed25519\",\"signature\":\"c2ln\",\"extra\":1}"
		files := themeFiles()
		files[SignatureFile] = []byte(sig)
		raw, err := zipFiles(files)
		if err != nil {
			t.Fatal(err)
		}
		p := parse(t, raw)
		if p.Signature == nil || p.Signature.Signer != "abc" || p.Signature.Signature != "c2ln" {
			t.Fatalf("signature = %+v", p.Signature)
		}
	})
}

// An entry with a wrongly typed site_type, flags or tags keeps its other fields.
func TestEntryTolerantDecode(t *testing.T) {
	const base = `"kind":"theme","name":"a","version":"1.0.0","url":"p/a.hmp","sha256":"0000000000000000000000000000000000000000000000000000000000000007"`
	var bad Entry
	if err := bad.UnmarshalJSON([]byte(`{` + base + `,"site_type":5,"flags":"dark","tags":{"a":1}}`)); err != nil {
		t.Fatal(err)
	}
	if bad.Name != "a" || bad.URL != "p/a.hmp" || bad.SiteType != "" || bad.Flags != nil || bad.Tags != nil {
		t.Fatalf("bad entry = %+v", bad)
	}
	var good Entry
	if err := good.UnmarshalJSON([]byte(`{` + base + `,"site_type":"blog","flags":["dark"],"tags":[{"name":"n","category":"analytics","hosts":["x.example"]}]}`)); err != nil {
		t.Fatal(err)
	}
	if good.SiteType != "blog" || len(good.Flags) != 1 || len(good.Tags) != 1 || good.Tags[0].Hosts[0] != "x.example" {
		t.Fatalf("good entry = %+v", good)
	}
}
