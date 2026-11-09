package app

import (
	"regexp"
	"testing"
)

// The built-in theme has no package row, so its version comes from the
// embedded manifest; it must be a plain semver the stats receiver accepts.
func TestBuiltinThemeInfo(t *testing.T) {
	th, err := builtinTheme()
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "default" || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(th.Version) {
		t.Fatalf("theme = %+v", th)
	}
}
