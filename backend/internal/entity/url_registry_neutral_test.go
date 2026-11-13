package entity

import "testing"

func TestURLSnapshot_PathIsLocaleNeutral(t *testing.T) {
	t.Parallel()
	snap, err := BuildURLSnapshot([]*EntitySchema{
		schemaWith("person", `{"url_pattern":"/people/{slug}","locale_neutral":true}`),
		schemaWith("guide", `{"url_pattern":"/guides/{slug}"}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"/people/x":  true,
		"/guides/x":  false,
		"/nothing/x": false,
	} {
		if got := snap.PathIsLocaleNeutral(path); got != want {
			t.Errorf("PathIsLocaleNeutral(%q) = %v, want %v", path, got, want)
		}
	}
	var nilSnap *URLSnapshot
	if nilSnap.PathIsLocaleNeutral("/people/x") {
		t.Error("nil snapshot must not report neutral")
	}
}
