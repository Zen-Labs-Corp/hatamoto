package migrate

import (
	"io/fs"

	"hatamoto.dev/core/internal/modhost"
)

// Register adds a module's migrations: goose SQL files at the root of fsys,
// typically an embed.FS. module is lower case ([a-z][a-z0-9_]{0,30}). Call
// it from an init function: it panics after app.Main has started, on an
// invalid or duplicate name and on a nil filesystem.
func Register(module string, fsys fs.FS) { modhost.RegisterMigrations(module, fsys) }

// Err returns the result of the module's last migration run in this process:
// nil when it succeeded (or has not run), the error otherwise. A module
// whose migrations failed has its background services skipped.
func Err(module string) error { return modhost.MigrationErr(module) }
