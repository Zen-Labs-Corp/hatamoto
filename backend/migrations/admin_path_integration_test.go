//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
)

const adminPathVersion = 202610261900

type adminPathRow struct {
	path, ackBy sql.NullString
	ackAt       sql.NullTime
}

func readAdminPath(t *testing.T, db *sql.DB) adminPathRow {
	t.Helper()
	var r adminPathRow
	err := db.QueryRowContext(context.Background(),
		`SELECT admin_path, admin_path_ack_by, admin_path_ack_at FROM hatamoto_site_config WHERE id = TRUE`).
		Scan(&r.path, &r.ackBy, &r.ackAt)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// An install that already has a site config row keeps /admin, with no acknowledgement.
func TestAdminPathUpgradeKeepsAdmin(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, adminPathVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Example')`); err != nil {
		t.Fatalf("insert row: %v", err)
	}
	if _, err := p.UpTo(ctx, adminPathVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	r := readAdminPath(t, db)
	if !r.path.Valid || r.path.String != "/admin" || r.ackBy.Valid || r.ackAt.Valid {
		t.Errorf("row = %+v, want /admin with NULL acks", r)
	}
}

// A fresh install has no row after migrating: boot creates it, and the column stays NULL (pending).
func TestAdminPathFreshNoRow(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_site_config`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows = %d (%v), want 0", n, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Example')`); err != nil {
		t.Fatal(err)
	}
	if r := readAdminPath(t, db); r.path.Valid {
		t.Errorf("new row admin_path = %q, want NULL (pending)", r.path.String)
	}
}

func TestAdminPathUpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	hasCol := func() bool {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_name = 'hatamoto_site_config' AND column_name IN ('admin_path','admin_path_ack_by','admin_path_ack_at')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 3
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !hasCol() {
		t.Fatal("columns missing after up")
	}
	if _, err := p.DownTo(ctx, adminPathVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if hasCol() {
		t.Fatal("columns still present after down")
	}
	if _, err := p.UpTo(ctx, adminPathVersion); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !hasCol() {
		t.Fatal("columns missing after re-up")
	}
}
