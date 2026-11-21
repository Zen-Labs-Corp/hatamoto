//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

const jobClaimsVersion = 202611220100

// The table appears with the migration, rejects a second row for the same job
// and survives a repeated Up.
func TestJobClaims_Up(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, jobClaimsVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	if regclass(t, db, "hatamoto_job_claims") != nil {
		t.Fatal("hatamoto_job_claims exists before the migration")
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if regclass(t, db, "hatamoto_job_claims") == nil {
		t.Fatal("hatamoto_job_claims missing after up")
	}
	const ins = `INSERT INTO hatamoto_job_claims (job_id, claimed_until) VALUES ('module:x:y', now())`
	if _, err := db.ExecContext(ctx, ins); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.ExecContext(ctx, ins); err == nil {
		t.Fatal("second insert of the same job_id succeeded")
	}
	if _, err := db.ExecContext(ctx, ins+` ON CONFLICT (job_id) DO NOTHING`); err != nil {
		t.Fatalf("on conflict: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("second up: %v", err)
	}
}
