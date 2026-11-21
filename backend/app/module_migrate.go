package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/modhost"
)

// moduleTable is the goose version table of a module, apart from core's.
func moduleTable(module string) string { return "module_" + module + "_goose_db_version" }

// withModuleProvider runs fn with a goose provider over one module's
// migrations: its own version table, the same session locker as core, and
// the global goose registry out of the picture (SQL files only).
func withModuleProvider(pool *pgxpool.Pool, m modhost.Migrations, fn func(*goose.Provider) error) error {
	var db *sql.DB = stdlib.OpenDBFromPool(pool)
	defer db.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migration locker: %w", err)
	}
	prov, err := goose.NewProvider(goose.DialectPostgres, db, m.FS,
		goose.WithTableName(moduleTable(m.Module)),
		goose.WithSessionLocker(locker), goose.WithAllowOutofOrder(true),
		goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	return fn(prov)
}

// migrateModules applies every module's pending migrations after core. A
// failure is logged and recorded with modhost.SetMigrationErr; it never
// stops the caller (an optional module must not take the site down).
func migrateModules(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	for _, m := range modhost.AllMigrations() {
		err := withModuleProvider(pool, m, func(p *goose.Provider) error {
			res, err := p.Up(ctx)
			for _, r := range res {
				logger.Info("module migration applied", "module", m.Module, "version", r.Source.Version, "file", r.Source.Path, "took", r.Duration)
			}
			return err
		})
		if err != nil {
			err = fmt.Errorf("module %q: migrate up: %w", m.Module, err)
			logger.Error("module migration failed", "module", m.Module, "err", err)
		}
		modhost.SetMigrationErr(m.Module, err)
	}
}

// migrateModulesDirect is migrateModules on a direct connection, like
// migrateUpDirect. Nothing registered: nothing happens, not even a connection.
func migrateModulesDirect(ctx context.Context, db *database.DB, logger *slog.Logger) {
	if len(modhost.AllMigrations()) == 0 {
		return
	}
	pool, closeFn, err := db.Direct().Pool(ctx, 2)
	if err != nil {
		for _, m := range modhost.AllMigrations() {
			modhost.SetMigrationErr(m.Module, err)
		}
		logger.Error("module migrations skipped: no direct connection", "err", err)
		return
	}
	defer closeFn()
	migrateModules(ctx, pool, logger)
}

// migrateModulesStatusPool prints a "module <name>" heading and the
// migrateStatus rows for every registered module.
func migrateModulesStatusPool(ctx context.Context, pool *pgxpool.Pool, w io.Writer) error {
	for _, m := range modhost.AllMigrations() {
		fmt.Fprintf(w, "module %s\n", m.Module)
		err := withModuleProvider(pool, m, func(p *goose.Provider) error {
			st, err := p.Status(ctx)
			if err != nil {
				return fmt.Errorf("migrate status: module %q: %w", m.Module, err)
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
		if err != nil {
			return err
		}
	}
	return nil
}

// migrateModulesStatus is migrateModulesStatusPool on a direct connection.
func migrateModulesStatus(ctx context.Context, db *database.DB, w io.Writer) error {
	if len(modhost.AllMigrations()) == 0 {
		return nil
	}
	pool, closeFn, err := db.Direct().Pool(ctx, 2)
	if err != nil {
		return err
	}
	defer closeFn()
	return migrateModulesStatusPool(ctx, pool, w)
}
