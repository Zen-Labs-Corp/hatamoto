// Package migrations embeds the core goose migrations so the hatamoto binary
// can migrate its own database. See README.md for the numbering rules.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
