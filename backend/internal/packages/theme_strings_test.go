package packages

import (
	"context"
	"testing"
)

func TestThemeStringsBuiltIn(t *testing.T) {
	m := &Manager{}
	for _, name := range []string{"", DefaultTheme} {
		decl, ok := m.ThemeStrings(context.Background(), name)
		if !ok || len(decl) == 0 {
			t.Fatalf("ThemeStrings(%q) = %d keys, ok=%v; want the built-in theme's strings", name, len(decl), ok)
		}
	}
}
