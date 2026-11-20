//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
)

const siteIdentityVersion = 202611201500

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestSiteIdentity_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)

	exists := func() bool {
		t.Helper()
		var reg sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT to_regclass('hatamoto_site_identity')::text`).Scan(&reg); err != nil {
			t.Fatal(err)
		}
		return reg.Valid
	}
	rows := func() int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_site_identity`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	siteID := func() string {
		t.Helper()
		var id string
		if err := db.QueryRowContext(ctx, `SELECT site_id::text FROM hatamoto_site_identity WHERE id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !exists() {
		t.Fatal("hatamoto_site_identity missing after up")
	}
	if n := rows(); n != 1 {
		t.Fatalf("rows after up = %d, want 1", n)
	}
	first, second := siteID(), siteID()
	if first == "" || first != second {
		t.Fatalf("site id unstable or empty: %q vs %q", first, second)
	}
	if !uuidShape.MatchString(first) {
		t.Errorf("site id %q is not a UUID", first)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_identity (id) VALUES (true)`); err == nil {
		t.Error("second row accepted, want primary key conflict")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_site_identity (id) VALUES (false)`); err == nil {
		t.Error("id=false accepted, want check violation")
	}

	if _, err := p.UpTo(ctx, siteIdentityVersion); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if got := siteID(); got != first {
		t.Errorf("site id changed on repeated up: %q -> %q", first, got)
	}

	if _, err := p.DownTo(ctx, siteIdentityVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if exists() {
		t.Fatal("hatamoto_site_identity still present after down")
	}
	if _, err := p.UpTo(ctx, siteIdentityVersion); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	if !exists() {
		t.Fatal("hatamoto_site_identity missing after re-up")
	}
	if n := rows(); n != 1 {
		t.Fatalf("rows after re-up = %d, want 1", n)
	}
}
