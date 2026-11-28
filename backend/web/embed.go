// Package web holds the files the Go admin ships inside the binary: templates,
// CSS and JS (admin/) and the pinned third-party assets (assets/, see
// tools/vendor). The asset directory must not be named vendor: Go module zips
// drop every file in a subdirectory of a vendor directory, so modules that
// depend on core would embed an incomplete tree. It lives under backend/
// because go:embed cannot climb out of its package directory.
package web

import (
	"embed"
	"io/fs"
)

// The noble hashes graph has files starting with an underscore, which a plain
// directory pattern skips, so its directory is embedded with the all: prefix.
//
//go:embed admin assets all:assets/noble-curves-2.4.0
var content embed.FS

// Admin is the admin tree: layouts/, partials/, pages/, css/, js/.
func Admin() fs.FS { return sub("admin") }

// Vendor is the vendored third-party tree under assets/ (vendor.json plus the files it pins).
func Vendor() fs.FS { return sub("assets") }

func sub(dir string) fs.FS {
	f, err := fs.Sub(content, dir)
	if err != nil {
		panic(err) // dir is a compile-time constant that go:embed guarantees
	}
	return f
}
