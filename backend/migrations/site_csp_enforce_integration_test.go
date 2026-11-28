//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
)

const cspEnforceVersion = 202611281000

func cspEnforceValue(t *testing.T, db *sql.DB) sql.NullBool {
	t.Helper()
	var v sql.NullBool
	if err := db.QueryRowContext(context.Background(), `SELECT csp_enforce FROM hatamoto_site_config`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSiteCSPEnforce_PinsExistingRow(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, cspEnforceVersion-1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Existing')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, cspEnforceVersion); err != nil {
		t.Fatal(err)
	}
	if v := cspEnforceValue(t, db); !v.Valid || v.Bool {
		t.Fatalf("csp_enforce = %+v, want false", v)
	}
}

func TestSiteCSPEnforce_FreshRowNull(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, cspEnforceVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Fresh')`); err != nil {
		t.Fatal(err)
	}
	if v := cspEnforceValue(t, db); v.Valid {
		t.Fatalf("csp_enforce = %+v, want NULL", v)
	}
}

func TestSiteCSPEnforce_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, cspEnforceVersion-1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Existing')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, cspEnforceVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, cspEnforceVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.UpTo(ctx, cspEnforceVersion); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if v := cspEnforceValue(t, db); !v.Valid || v.Bool {
		t.Fatalf("csp_enforce = %+v, want false", v)
	}
	if _, err := db.ExecContext(ctx, `UPDATE hatamoto_site_config SET csp_enforce = false WHERE csp_enforce IS NULL`); err != nil {
		t.Fatal(err)
	}
	if v := cspEnforceValue(t, db); !v.Valid || v.Bool {
		t.Fatalf("after rerun csp_enforce = %+v, want false", v)
	}
}
