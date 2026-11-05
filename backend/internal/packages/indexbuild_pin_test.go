package packages

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const pinBuildWithMeta = `{
  "index": {
    "version": 1,
    "issued_at": "2029-12-31T23:59:00Z",
    "expires_at": "2030-01-01T23:59:00Z",
    "packages": [
      {
        "kind": "plugin",
        "name": "hello",
        "version": "1.0.0",
        "url": "p/hello.hmp",
        "sha256": "e458b5609f1d49fbf55b8e29e8852e6e41f56cdcb129381b633f17937c3fdc92",
        "size": 355,
        "title": "Hello \u0026 \u003cPlugin\u003e",
        "author": "acme",
        "source": "https://example.org/a?x=1\u0026y=2",
        "tier": "community",
        "about": "Line one\nLine two é",
        "features": [
          "fast",
          "tiny"
        ],
        "category": "SEO",
        "featured": true
      },
      {
        "kind": "theme",
        "name": "sunrise",
        "version": "1.2.3",
        "url": "p/sunrise.hmp",
        "sha256": "0669530d5dc015ea13f9280319d3a1f775d3d11a7c43d186cb3902e5f50959ca",
        "size": 598,
        "tier": "first-party",
        "site_type": "blog",
        "flags": [
          "dark"
        ]
      }
    ]
  },
  "signer": "6ca1d872077e600c",
  "algorithm": "ed25519",
  "signature": "338l7NDGvBwlahUzF+aQEiVILN7wd05fpibDhb/8hEL2YjN/uLF4tcMwPH3XB5g3wyyMVav6sclXOdohRo+GBQ=="
}`

const pinBuildNoMeta = `{
  "index": {
    "version": 1,
    "issued_at": "2029-12-31T23:59:00Z",
    "expires_at": "2030-01-01T23:59:00Z",
    "packages": [
      {
        "kind": "plugin",
        "name": "hello",
        "version": "1.0.0",
        "url": "p/hello.hmp",
        "sha256": "e458b5609f1d49fbf55b8e29e8852e6e41f56cdcb129381b633f17937c3fdc92",
        "size": 355
      },
      {
        "kind": "theme",
        "name": "sunrise",
        "version": "1.2.3",
        "url": "p/sunrise.hmp",
        "sha256": "0669530d5dc015ea13f9280319d3a1f775d3d11a7c43d186cb3902e5f50959ca",
        "size": 598
      }
    ]
  },
  "signer": "6ca1d872077e600c",
  "algorithm": "ed25519",
  "signature": "EsZMSC1XHuE5REUzEopSyM6bW682TRjzY89Iy6mtgfGLADEkTXRuLlw50YdpJj1KIK0hHT5K8YXUIHQancDyCw=="
}`

func pinBuildDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, files := range map[string]map[string][]byte{"hello.hmp": pluginFiles(), "sunrise.hmp": themeFiles()} {
		if err := os.WriteFile(filepath.Join(dir, name), packForMeta(t, files), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The published registry.json bytes for a fixed input: regenerating a registry
// after an encoder change must change nothing.
func TestIndexBuildBytesGolden(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	dir := pinBuildDir(t)
	meta := map[string]Meta{
		"hello":   {Title: "Hello & <Plugin>", Author: "acme", Source: "https://example.org/a?x=1&y=2", Tier: TierCommunity, About: "Line one\nLine two é", Features: []string{"fast", "tiny"}, Category: "SEO", Featured: true},
		"sunrise": {Tier: TierFirstParty, SiteType: SiteTypes[0], Flags: []string{ThemeFlags[0]}},
	}
	for _, c := range []struct {
		name string
		opts BuildOptions
		want string
	}{
		{"with meta", BuildOptions{Meta: meta}, pinBuildWithMeta},
		{"no meta", BuildOptions{}, pinBuildNoMeta},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := BuildIndexWith(dir, "p/", pinKey(), now, 24*time.Hour, c.opts)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("registry bytes changed:\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestParseMetaStrict(t *testing.T) {
	if m, err := ParseMeta([]byte(`{"hello":{"author":"a","tier":"community"}}`)); err != nil || m["hello"].Author != "a" {
		t.Fatalf("valid meta: %v %v", m, err)
	}
	for name, bad := range map[string]string{
		"unknown member": `{"hello":{"author":"a","tier":"community","bogus":1}}`,
		"trailing data":  `{"hello":{"tier":"community"}} {}`,
		"not an object":  `[1]`,
	} {
		if _, err := ParseMeta([]byte(bad)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
