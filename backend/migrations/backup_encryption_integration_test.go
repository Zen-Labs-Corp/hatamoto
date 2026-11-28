//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

const backupEncryptionVersion = 202611281200

func TestBackupEncryptionUpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	hasTable := func() bool {
		t.Helper()
		var ok bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass('hatamoto_backup_encryption') IS NOT NULL`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !hasTable() {
		t.Fatal("table missing after up")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_backup_encryption (recipient) VALUES ('age1x')`); err != nil {
		t.Fatal(err)
	}
	var pg *pgconn.PgError
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_backup_encryption (recipient) VALUES ('age1y')`); !errors.As(err, &pg) || pg.Code != "23505" {
		t.Errorf("second row: err = %v, want unique violation", err)
	}
	if _, err := p.DownTo(ctx, backupEncryptionVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if hasTable() {
		t.Fatal("table still present after down")
	}
	if _, err := p.UpTo(ctx, backupEncryptionVersion); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !hasTable() {
		t.Fatal("table missing after re-up")
	}
}
