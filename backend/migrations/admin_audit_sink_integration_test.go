//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

// A previous binary inserts only the original five columns; the widened
// columns must default so it keeps working during a rolling upgrade.
func TestAdminAuditSinkMigration_OldInsertStillWorks(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_admin_audit (actor_id, actor, action, target, detail)
		VALUES (NULL, 'cli', 'test.old_insert', 'site', '{}'::jsonb)`); err != nil {
		t.Fatalf("old-shape insert: %v", err)
	}
	var kind, ip, result string
	if err := db.QueryRowContext(ctx, `SELECT actor_kind, ip, result FROM hatamoto_admin_audit WHERE action = 'test.old_insert'`).Scan(&kind, &ip, &result); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if kind != "" || ip != "" || result != "ok" {
		t.Fatalf("defaults = %q %q %q, want '' '' ok", kind, ip, result)
	}
}
