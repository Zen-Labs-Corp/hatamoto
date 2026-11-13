package analytics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/analytics/geotest"
)

func TestOpenGeoLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")
	if err := os.WriteFile(path, geotest.TinyMMDB("DE", "fr"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := OpenGeo(path)
	if err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]string{
		"1.2.3.4":        "DE",
		"200.1.1.1":      "FR",
		"not-an-ip":      "",
		"":               "",
		"::ffff:1.2.3.4": "DE",
	} {
		if got := g.Country(ip); got != want {
			t.Errorf("Country(%q) = %q, want %q", ip, got, want)
		}
	}
}

func TestOpenGeoErrors(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.mmdb")
	if err := os.WriteFile(junk, []byte("TOPSECRET not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"missing":   filepath.Join(dir, "nope.mmdb"),
		"directory": dir,
		"not mmdb":  junk,
	} {
		g, err := OpenGeo(path)
		if err == nil || g != nil {
			t.Errorf("%s: want error, got %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), "TOPSECRET") || strings.Contains(err.Error(), dir) {
			t.Errorf("%s: error leaks detail: %v", name, err)
		}
	}
}

func TestNilGeoDBCountry(t *testing.T) {
	var g *GeoDB
	if got := g.Country("1.2.3.4"); got != "" {
		t.Errorf("nil GeoDB Country = %q, want empty", got)
	}
}

func TestOpenGeoMappedMatchesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")
	if err := os.WriteFile(path, geotest.TinyMMDB("DE", "fr"), 0o600); err != nil {
		t.Fatal(err)
	}
	def, err := OpenGeo(path)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := OpenGeoMapped(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"1.2.3.4", "200.1.1.1", "::ffff:1.2.3.4", "not-an-ip", ""} {
		if a, b := def.Country(ip), mapped.Country(ip); a != b {
			t.Errorf("Country(%q): default %q, mapped %q", ip, a, b)
		}
	}
	if mapped.Country("1.2.3.4") != "DE" {
		t.Error("mapped lookup did not return DE")
	}
}

func TestOpenGeoMappedErrors(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.mmdb")
	if err := os.WriteFile(junk, []byte("TOPSECRET not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.mmdb")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"missing":   filepath.Join(dir, "nope.mmdb"),
		"directory": dir,
		"not mmdb":  junk,
		"empty":     empty,
	} {
		g, err := OpenGeoMapped(path)
		if err == nil || g != nil {
			t.Errorf("%s: want error, got %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), "TOPSECRET") || strings.Contains(err.Error(), dir) {
			t.Errorf("%s: error leaks detail: %v", name, err)
		}
	}
}
