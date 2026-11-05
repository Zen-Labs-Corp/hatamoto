package importjob

import (
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// nil Types means "all post types" and must stay absent in the stored column;
// an empty, non-nil list means "none" and must be written as [].
func TestStoredOptionsKeepTypesMeaning(t *testing.T) {
	all := DefaultOptions()
	b, err := jsonx.Marshal(stored{Kind: KindREST, Options: &all})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"kind":"rest","url":"","user":"","wizard":{"drafts":true,"media":true,"menus":true,"keep_patterns":false,"keep_edited":false,"no_seo_live":false}}`; string(b) != want {
		t.Fatalf("all types = %s\nwant %s", b, want)
	}
	none := Options{Types: []string{}}
	b, err = jsonx.Marshal(stored{Kind: KindREST, Options: &none})
	if err != nil {
		t.Fatal(err)
	}
	var back stored
	if err := jsonx.Unmarshal(b, &back, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	if back.Options == nil || back.Options.Types == nil || len(back.Options.Types) != 0 {
		t.Fatalf("none did not survive: %s -> %#v", b, back.Options)
	}
}
