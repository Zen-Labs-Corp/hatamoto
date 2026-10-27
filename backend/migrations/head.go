package migrations

import (
	"database/sql"
	"errors"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Head is the newest migration version this binary carries, counting the
// embedded SQL files and the Go migrations registered by this package. It
// needs no database: goose only lists sources, and the *sql.DB is never used.
func Head() (int64, error) { return headOf(FS) }

// headOf is Head's seam: tests pass their own sources and extra Go migrations.
func headOf(fsys fs.FS, opts ...goose.ProviderOption) (int64, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, &sql.DB{}, fsys, opts...)
	if err != nil {
		return 0, err
	}
	var head int64
	for _, s := range p.ListSources() {
		head = max(head, s.Version)
	}
	if head == 0 {
		return 0, errors.New("migrations: no migrations found")
	}
	return head, nil
}
