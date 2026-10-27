package app

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"hatamoto.dev/core/migrations"
)

// migrationsFS is the migration source; tests swap it.
var migrationsFS fs.FS = migrations.FS

// withProvider runs fn with a goose provider over the embedded core
// migrations. It takes a Postgres session-level advisory lock for the whole
// run, so concurrent boots (replicas, `hatamoto migrate` next to `serve`)
// migrate one at a time and the second finds nothing to do.
//
// Out-of-order is allowed: versions 001-017 are sequential and later ones are
// timestamps, so two branches merged in any order must both still apply (see
// migrations/README.md).
func withProvider(pool *pgxpool.Pool, fn func(*goose.Provider) error) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migration locker: %w", err)
	}
	// The Go migrations of package migrations register themselves globally and
	// belong to the embedded set; a test source swapped in has none of them.
	_, embedded := migrationsFS.(embed.FS)
	prov, err := goose.NewProvider(goose.DialectPostgres, db, migrationsFS,
		goose.WithSessionLocker(locker), goose.WithAllowOutofOrder(true),
		goose.WithDisableGlobalRegistry(!embedded))
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	return fn(prov)
}

// migrateUp applies every pending core migration and logs each one.
func migrateUp(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	return withProvider(pool, func(p *goose.Provider) error {
		res, err := p.Up(ctx)
		for _, r := range res {
			logger.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "took", r.Duration)
		}
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		if len(res) == 0 {
			logger.Info("database schema is up to date")
		}
		return nil
	})
}

// migrateStatus prints "VERSION  STATE  FILE" per migration.
func migrateStatus(ctx context.Context, pool *pgxpool.Pool, w io.Writer) error {
	return withProvider(pool, func(p *goose.Provider) error {
		st, err := p.Status(ctx)
		if err != nil {
			return fmt.Errorf("migrate status: %w", err)
		}
		for _, s := range st {
			state := "pending"
			if s.State == goose.StateApplied {
				state = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(w, "%-14d %-28s %s\n", s.Source.Version, state, s.Source.Path)
		}
		return nil
	})
}
