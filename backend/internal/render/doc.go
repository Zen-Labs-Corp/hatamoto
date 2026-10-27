// Package render renders the public site from layered html/template themes.
//
// A theme is a directory tree (or fs.FS) holding theme.json, layouts/,
// partials/, sections/<type>.html, schemas/<type>.json and assets/. Files are
// resolved through a Chain: site override > disk theme > embedded default.
//
// The package is deliberately pure: it never imports the server package. The
// server builds a PageView from stored entities and hands it to the Engine.
package render
