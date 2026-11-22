package web

import (
	"io/fs"
	"strings"
	"testing"
)

// droppedFromModuleZip mirrors isVendoredPackage in golang.org/x/mod/zip (the
// go >= 1.24 form; backend/go.mod says go 1.27): a file in a subdirectory of a
// vendor directory is left out of the module zip, so a module that depends on
// core would embed an incomplete tree. name is relative to the module root.
func droppedFromModuleZip(name string) bool {
	var i int
	if strings.HasPrefix(name, "vendor/") {
		i = len("vendor/")
	} else if j := strings.Index(name, "/vendor/"); j >= 0 {
		i = j + len("/vendor/")
	} else {
		return false
	}
	return strings.Contains(name[i:], "/")
}

func TestDroppedFromModuleZip(t *testing.T) {
	for name, want := range map[string]bool{
		"web/vendor/htmx.min.js":            false,
		"web/vendor/tiptap-3/@tiptap/x.mjs": true,
		"vendor/a/b.go":                     true,
		"tools/vendor/main.go":              false,
		"web/assets/tiptap-3/@tiptap/x.mjs": false,
	} {
		if got := droppedFromModuleZip(name); got != want {
			t.Errorf("droppedFromModuleZip(%q) = %v, want %v", name, got, want)
		}
	}
}

// Every embedded file must ship in the module zip; a "vendor" path segment is
// refused outright (stricter than the zip rule, so no future rule change bites).
func TestEmbeddedFilesShipInModuleZip(t *testing.T) {
	n := 0
	err := fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == "vendor" {
				t.Errorf("embedded path %s has a vendor segment; Go module zips drop such trees", p)
			}
		}
		if !d.IsDir() {
			n++
			if droppedFromModuleZip("web/" + p) {
				t.Errorf("web/%s would be left out of the module zip", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("embedded FS is empty")
	}
}

func TestPinnedEditorIsEmbedded(t *testing.T) {
	m, err := fs.Glob(Vendor(), "tiptap-*/@tiptap/core@*/es2022/core.mjs")
	if err != nil || len(m) != 1 {
		t.Fatalf("tiptap core.mjs in Vendor(): %v, %v", m, err)
	}
	if _, err := fs.Stat(Vendor(), "vendor.json"); err != nil {
		t.Fatal(err)
	}
}
