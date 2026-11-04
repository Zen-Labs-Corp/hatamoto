//go:build integration

package migrations_test

import (
	"context"
	"testing"
)

const changesetDecisionsVersion = 202611031200

var (
	decisionItemCols     = []string{"risk", "risk_reason", "risk_ref", "decision"}
	decisionProposalCols = []string{"decision", "before", "decided_at"}
)

// Rows written before the migration keep working and read NULL in every new column.
func TestChangesetDecisionsMigration_OldRowsAreNull(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, changesetDecisionsVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_runs (plugin) VALUES ('a') RETURNING id`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var csID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_changesets (run_id, plugin, expires_at) VALUES ($1, 'a', now() + interval '1 day') RETURNING id`, runID).Scan(&csID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_changeset_items (changeset_id, seq, kind, op, payload) VALUES ($1, 1, 'entity', 'upsert', '{}')`, csID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_run_proposals (run_id, kind, payload) VALUES ($1, 'home_page', '{}')`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var risk, reason, decision *string
	var ref *int
	if err := db.QueryRowContext(ctx, `SELECT risk, risk_reason, risk_ref, decision FROM hatamoto_changeset_items`).Scan(&risk, &reason, &ref, &decision); err != nil {
		t.Fatal(err)
	}
	if risk != nil || reason != nil || ref != nil || decision != nil {
		t.Errorf("item columns not NULL: %v %v %v %v", risk, reason, ref, decision)
	}
	var pdec, before, decidedAt *string
	if err := db.QueryRowContext(ctx, `SELECT decision, before::text, decided_at::text FROM hatamoto_run_proposals`).Scan(&pdec, &before, &decidedAt); err != nil {
		t.Fatal(err)
	}
	if pdec != nil || before != nil || decidedAt != nil {
		t.Errorf("proposal columns not NULL: %v %v %v", pdec, before, decidedAt)
	}
}

// Down drops the new columns and keeps the tables; Up again works.
func TestChangesetDecisionsMigration_Down(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := p.DownTo(ctx, changesetDecisionsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, c := range decisionItemCols {
		if columnExists(t, db, "hatamoto_changeset_items", c) {
			t.Errorf("hatamoto_changeset_items.%s still exists after down", c)
		}
	}
	for _, c := range decisionProposalCols {
		if columnExists(t, db, "hatamoto_run_proposals", c) {
			t.Errorf("hatamoto_run_proposals.%s still exists after down", c)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	for _, c := range decisionItemCols {
		if !columnExists(t, db, "hatamoto_changeset_items", c) {
			t.Errorf("hatamoto_changeset_items.%s missing after re-up", c)
		}
	}
}
