//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

func TestAuditLogPageMigration_DefaultsAndIndexes(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_config (id, company_name) VALUES (TRUE, 'Test') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed site config: %v", err)
	}
	var admin, agent int
	if err := db.QueryRowContext(ctx, `SELECT audit_retention_days, agent_audit_retention_days FROM hatamoto_site_config WHERE id = TRUE`).Scan(&admin, &agent); err != nil {
		t.Fatalf("read retention: %v", err)
	}
	if admin != 365 || agent != 90 {
		t.Fatalf("retention defaults = %d/%d, want 365/90", admin, agent)
	}
	for _, idx := range []string{"idx_hatamoto_mcp_audit_tool_at", "idx_hatamoto_mcp_audit_key_at"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE tablename = 'hatamoto_mcp_audit' AND indexname = $1`, idx).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s: count=%d err=%v", idx, n, err)
		}
	}
}

// A previous binary inserts only the original columns of an agent call.
func TestAuditLogPageMigration_OldAgentInsertStillWorks(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_mcp_audit (api_key_name, tool, ok) VALUES ('k', 'entities_list', TRUE)`); err != nil {
		t.Fatalf("old-shape insert: %v", err)
	}
}
