package analytics

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mmdbStr(s string) []byte { return append([]byte{byte(0x40 + len(s))}, s...) }

// countryRecord encodes {"country": {"iso_code": code}} in the mmdb data format.
func countryRecord(code string) []byte {
	var b bytes.Buffer
	b.WriteByte(0xE1)
	b.Write(mmdbStr("country"))
	b.WriteByte(0xE1)
	b.Write(mmdbStr("iso_code"))
	b.Write(mmdbStr(code))
	return b.Bytes()
}

// tinyMMDB builds a one-node IPv4 database: addresses whose first bit is 0 map
// to "DE", the others to "fr" (lower case, to check normalisation).
func tinyMMDB() []byte {
	de, fr := countryRecord("DE"), countryRecord("fr")
	rec := func(off int) []byte {
		v := 1 + 16 + off // node_count + data section separator + offset
		return []byte{byte(v >> 16), byte(v >> 8), byte(v)}
	}
	var b bytes.Buffer
	b.Write(rec(0))
	b.Write(rec(len(de)))
	b.Write(make([]byte, 16))
	b.Write(de)
	b.Write(fr)
	b.WriteString("\xab\xcd\xefMaxMind.com")
	b.WriteByte(0xE6)
	for _, kv := range []struct {
		k string
		v []byte
	}{
		{"node_count", []byte{0xC1, 1}},
		{"record_size", []byte{0xA1, 24}},
		{"ip_version", []byte{0xA1, 4}},
		{"binary_format_major_version", []byte{0xA1, 2}},
		{"binary_format_minor_version", []byte{0xA0}},
		{"database_type", mmdbStr("test")},
	} {
		b.Write(mmdbStr(kv.k))
		b.Write(kv.v)
	}
	return b.Bytes()
}

func TestOpenGeoLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")
	if err := os.WriteFile(path, tinyMMDB(), 0o600); err != nil {
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
