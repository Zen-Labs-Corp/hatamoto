//go:build integration

package server

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/admin"
)

// The audit reader's SQL against a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run AdminAuditLog
func TestAdminAuditLogRecentIntegration(t *testing.T) {
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs Postgres)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, scratchMigratedDB(t, dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := adminAuditLog{pool: pool}

	if rows, total, err := log.Recent(ctx, 10, 0); err != nil || len(rows) != 0 || total != 0 {
		t.Fatalf("empty = %v %d %v", rows, total, err)
	}
	for _, a := range []string{"first", "second", "third"} {
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_admin_audit (actor, action, detail) VALUES ('o@example.com', $1, '{"name":"Home"}')`, a); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := log.Recent(ctx, 2, 0)
	if err != nil || total != 3 || len(rows) != 2 || rows[0].Action != "third" || rows[1].Action != "second" {
		t.Fatalf("page 1 = %+v total=%d err=%v", rows, total, err)
	}
	if rows[0].Detail["name"] != "Home" || rows[0].Target != "" || rows[0].Actor != "o@example.com" {
		t.Errorf("row = %+v", rows[0])
	}
	if rows, _, _ := log.Recent(ctx, 2, 2); len(rows) != 1 || rows[0].Action != "first" {
		t.Errorf("page 2 = %+v", rows)
	}
	if rows, err := log.Latest(ctx, 2); err != nil || len(rows) != 2 || rows[0].Action != "third" {
		t.Errorf("latest = %+v %v", rows, err)
	}
	var _ admin.AuditReader = log
}
