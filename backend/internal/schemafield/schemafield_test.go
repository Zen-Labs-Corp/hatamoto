package schemafield

import "testing"

func TestIsURIField(t *testing.T) {
	for _, c := range []struct {
		format, xHm string
		want        bool
	}{
		{"uri", "", true}, {"", "url", true}, {"", "image", true}, {"", "", false},
		{"uri", "richtext", false}, {"uri", "code", false}, {"text", "", false},
	} {
		if got := IsURIField(c.format, c.xHm); got != c.want {
			t.Errorf("IsURIField(%q, %q) = %v, want %v", c.format, c.xHm, got, c.want)
		}
	}
}
