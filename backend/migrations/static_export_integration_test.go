//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestStaticExportMigration_SettingsSingleton(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_static_hosting_settings DEFAULT VALUES`); err != nil {
		t.Fatalf("insert default row: %v", err)
	}
	var mode string
	if err := db.QueryRowContext(ctx, `SELECT mode FROM hatamoto_static_hosting_settings`).Scan(&mode); err != nil || mode != "served" {
		t.Fatalf("mode = %q (%v), want served", mode, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_static_hosting_settings (id) VALUES (false)`); err == nil {
		t.Fatal("second row with id=false was accepted")
	}
}

func TestStaticExportMigration_OneRunningRun(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	const ins = `INSERT INTO hatamoto_static_export_runs (trigger, target, status) VALUES ('test', 'dir:/srv/site-static', $1)`
	if _, err := db.ExecContext(ctx, ins, "running"); err != nil {
		t.Fatalf("first running: %v", err)
	}
	_, err := db.ExecContext(ctx, ins, "running")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second running error = %v, want unique violation 23505", err)
	}
	if _, err := db.ExecContext(ctx, ins, "ok"); err != nil {
		t.Fatalf("ok run next to a running one: %v", err)
	}
}
