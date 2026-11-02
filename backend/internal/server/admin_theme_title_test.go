package server

import "testing"

func TestActiveThemeTitleDefaultsWithoutPackages(t *testing.T) {
	t.Parallel()

	if got := (&Server{}).activeThemeTitle(); got != "Default" {
		t.Fatalf("title = %q, want Default", got)
	}
}
