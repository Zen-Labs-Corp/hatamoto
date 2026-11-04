package tags_test

import (
	"testing"

	"hatamoto.dev/core/internal/consent"
	"hatamoto.dev/core/internal/tags"
)

// A declaration may use exactly the optional consent categories.
func TestPackCategoriesMatchConsent(t *testing.T) {
	for _, c := range consent.Categories {
		raw := `{"name":"A","id":{"label":"ID","pattern":"^[a-z]+$"},"script":"https://a.example/x.js","hosts":["a.example"],"category":"` + c + `"}`
		if _, err := tags.ParseDecl("pk", "a", []byte(raw)); err != nil {
			t.Fatalf("category %q refused: %v", c, err)
		}
	}
	raw := `{"name":"A","id":{"label":"ID","pattern":"^[a-z]+$"},"script":"https://a.example/x.js","hosts":["a.example"],"category":"necessary"}`
	if _, err := tags.ParseDecl("pk", "a", []byte(raw)); err == nil {
		t.Fatal("necessary accepted")
	}
}
