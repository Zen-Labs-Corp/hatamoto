package spam

import (
	"regexp"
	"strings"
	"testing"
)

func (n Names) all() []string { return []string{n.Body, n.Name, n.Email, n.Honeypot, n.Extra} }

func TestFieldNames(t *testing.T) {
	t.Parallel()
	key := FieldNameKey("secret")
	re := regexp.MustCompile(`^c_[0-9a-f]{10}[a-z]?$`)

	a := FieldNames(key, "tok-a")
	if a != FieldNames(key, "tok-a") {
		t.Fatal("same token must give the same names")
	}
	b := FieldNames(key, "tok-b")
	for i, n := range a.all() {
		if n == b.all()[i] {
			t.Fatalf("role %d name equal across tokens: %q", i, n)
		}
	}
	for _, names := range []Names{a, b} {
		seen := map[string]bool{}
		for _, n := range names.all() {
			if !re.MatchString(n) {
				t.Fatalf("name %q does not match pattern", n)
			}
			if strings.Contains(n, "web") || strings.Contains(n, "url") {
				t.Fatalf("name %q contains a telling word", n)
			}
			if seen[n] {
				t.Fatalf("duplicate name %q", n)
			}
			seen[n] = true
		}
	}
}
