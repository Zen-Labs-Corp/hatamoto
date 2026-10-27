package render

import (
	"embed"
	"io/fs"
)

// defaultThemeFS is the built-in theme shipped inside the binary.
//
//go:embed all:themes/default
var defaultThemeFS embed.FS

// DefaultFS returns the embedded default theme rooted at its top directory
// (theme.json, layouts/, partials/, sections/, schemas/, assets/).
func DefaultFS() fs.FS {
	sub, err := fs.Sub(defaultThemeFS, "themes/default")
	if err != nil {
		panic("render: embedded default theme missing: " + err.Error()) // build-time invariant
	}
	return sub
}
