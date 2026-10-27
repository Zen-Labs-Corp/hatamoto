// Package web holds the files the Go admin ships inside the binary: templates,
// CSS and JS (admin/) and the pinned third-party assets (vendor/, see
// tools/vendor). It lives under backend/ because go:embed cannot climb out of
// its package directory.
package web

import (
	"embed"
	"io/fs"
)

//go:embed admin vendor
var content embed.FS

// Admin is the admin tree: layouts/, partials/, pages/, css/, js/.
func Admin() fs.FS { return sub("admin") }

// Vendor is the vendored third-party tree (vendor.json plus the files it pins).
func Vendor() fs.FS { return sub("vendor") }

func sub(dir string) fs.FS {
	f, err := fs.Sub(content, dir)
	if err != nil {
		panic(err) // dir is a compile-time constant that go:embed guarantees
	}
	return f
}
