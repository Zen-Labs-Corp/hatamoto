package render

import (
	"io/fs"
	"os"
	"testing"
)

// The same theme falling back with different error wording is one fault
// identifier, so a later release is not judged to have a new fault.
func TestBootFaultIDsIgnoreErrorText(t *testing.T) {
	for _, field := range []string{"Missing", "AlsoMissing"} {
		dir := t.TempDir()
		writeFile(t, dir, "partials/header.html", `{{define "header"}}<header>{{.Site.Nav.`+field+`}}</header>{{end}}`)
		newTestEngine(t, Options{ThemeName: "blog", Layers: []fs.FS{os.DirFS(dir)}})
	}
	n := 0
	for _, f := range BootFaults() {
		if f == "theme-fallback:blog" {
			n++
		}
		if len(f) > 0 && (f[0] == 't' || f[0] == 'p') && f != "theme-fallback:blog" && len(f) > 40 {
			t.Errorf("fault id looks like error text: %q", f)
		}
	}
	if n != 1 {
		t.Fatalf("theme-fallback:blog recorded %d times, want 1: %v", n, BootFaults())
	}
}
