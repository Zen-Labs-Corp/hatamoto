package packages

import "testing"

func TestSchemaSlugPattern(t *testing.T) {
	for slug, ok := range map[string]bool{"product": true, "a": true, "my-type2": true, "a--b": false, "a-": false, "-a": false, "1a": false, "A": false, "a_b": false} {
		if schemaSlugRe.MatchString(slug) != ok {
			t.Errorf("%q: match = %v, want %v", slug, !ok, ok)
		}
	}
}
