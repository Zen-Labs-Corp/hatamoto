package modhost

import (
	"fmt"
	"io/fs"
)

// Migrations is one module's embedded goose SQL migrations.
type Migrations struct {
	Module string
	FS     fs.FS
}

var (
	migrations    []Migrations
	migrationErrs = map[string]error{}
)

func init() {
	onReset(func() {
		migrations = nil
		migrationErrs = map[string]error{}
	})
}

// RegisterMigrations adds a module's migrations. It panics after Freeze, on
// an invalid or duplicate module name and on a nil filesystem.
func RegisterMigrations(module string, fsys fs.FS) {
	register("migrate: Register", func() {
		switch {
		case !ValidName(module):
			panic(fmt.Sprintf("migrate: invalid module name %q", module))
		case isNil(fsys):
			panic(fmt.Sprintf("migrate: module %q has a nil filesystem", module))
		}
		for _, m := range migrations {
			if m.Module == module {
				panic(fmt.Sprintf("migrate: module %q registered twice", module))
			}
		}
		migrations = append(migrations, Migrations{Module: module, FS: fsys})
	})
}

// AllMigrations returns the registered migrations in registration order.
func AllMigrations() []Migrations {
	mu.Lock()
	defer mu.Unlock()
	return append([]Migrations(nil), migrations...)
}

// SetMigrationErr records the result of a module's last migration run; core only.
func SetMigrationErr(module string, err error) {
	mu.Lock()
	defer mu.Unlock()
	if err == nil {
		delete(migrationErrs, module)
		return
	}
	migrationErrs[module] = err
}

// MigrationErr returns the last migration error of module, or nil.
func MigrationErr(module string) error {
	mu.Lock()
	defer mu.Unlock()
	return migrationErrs[module]
}
