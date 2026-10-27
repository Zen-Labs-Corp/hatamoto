package migrations

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

func TestHeadIncludesGoMigrations(t *testing.T) {
	head, err := Head()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var newestSQL int64
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, err := goose.NumericComponent(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		newestSQL = max(newestSQL, v)
	}
	if newestSQL == 0 {
		t.Fatal("found no SQL migrations")
	}
	if head < newestSQL {
		t.Fatalf("head %d is older than the newest SQL migration %d", head, newestSQL)
	}
	// The Go migration 202610171205_backfill_authors.go registers itself in
	// init. It is older than the newest SQL file, so head >= its version proves
	// nothing; the provider must list it as a Go source, and Head must be the
	// maximum over SQL and Go sources together.
	const goMigration = 202610171205
	p, err := goose.NewProvider(goose.DialectPostgres, &sql.DB{}, FS)
	if err != nil {
		t.Fatal(err)
	}
	var foundGo bool
	var maxAll int64
	for _, src := range p.ListSources() {
		maxAll = max(maxAll, src.Version)
		if src.Type == goose.TypeGo && src.Version == goMigration {
			foundGo = true
		}
	}
	if !foundGo {
		t.Fatalf("goose does not list the Go migration %d as a Go source", goMigration)
	}
	if head != maxAll {
		t.Fatalf("head %d, but the newest of all sources is %d", head, maxAll)
	}
}

// A Go migration newer than every SQL file must decide the head; a SQL-only
// Head would return the SQL version instead.
func TestHeadOfCountsHigherGoMigration(t *testing.T) {
	fsys := fstest.MapFS{"202001010000_old.sql": {Data: []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n")}}
	const goVersion = 209912312359
	noop := &goose.GoFunc{RunTx: func(context.Context, *sql.Tx) error { return nil }}
	head, err := headOf(fsys, goose.WithGoMigrations(goose.NewGoMigration(goVersion, noop, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if head != goVersion {
		t.Fatalf("head %d, want the Go migration %d", head, goVersion)
	}
}
