package staticexport

import (
	"bytes"
	"slices"
	"testing"
)

func TestDiffManifests(t *testing.T) {
	e := func(sha string, size int64) Entry { return Entry{SHA256: sha, Size: size} }
	prev := Manifest{Files: map[string]Entry{
		"same.html": e("a", 1), "size.html": e("b", 1), "sha.html": e("c", 1), "gone.html": e("d", 1), "also-gone.css": e("e", 1),
	}}
	next := Manifest{Files: map[string]Entry{
		"same.html": e("a", 1), "size.html": e("b", 2), "sha.html": e("x", 1), "new.html": e("n", 1),
	}}
	d := DiffManifests(prev, next)
	check := func(name string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("changed", d.Changed, []string{"new.html", "sha.html", "size.html"})
	check("unchanged", d.Unchanged, []string{"same.html"})
	check("removed", d.Removed, []string{"also-gone.css", "gone.html"})
}

func TestManifestRoundTrip(t *testing.T) {
	m := Manifest{Files: map[string]Entry{
		"b.html": {SHA256: "bb", Size: 2, Type: "text/html; charset=utf-8"},
		"a.css":  {SHA256: "aa", Size: 1, Type: "text/css"},
	}}
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Index(b, []byte("a.css")) > bytes.Index(b, []byte("b.html")) {
		t.Errorf("keys not sorted:\n%s", b)
	}
	got, err := ReadManifest(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != manifestVersion || got.Files["a.css"] != m.Files["a.css"] || len(got.Files) != 2 {
		t.Errorf("round trip = %+v", got)
	}
}

func TestReadManifestRejectsUnknownVersion(t *testing.T) {
	if _, err := ReadManifest(bytes.NewReader([]byte(`{"version":99,"files":{}}`))); err == nil {
		t.Fatal("want error for version 99")
	}
}
