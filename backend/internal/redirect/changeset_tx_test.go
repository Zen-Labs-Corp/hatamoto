//go:build integration

package redirect

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// rollbackTx opens a transaction that is always rolled back, so tests leave no rows.
func rollbackTx(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 2, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	tx, err := db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	return ctx, tx
}

func TestChangesetTxCreateUpdateRestore_Integration(t *testing.T) {
	ctx, tx := rollbackTx(t)

	res, err := ApplyChangesetTx(ctx, tx, RedirectOp{SourcePath: "/cs-old", Target: "/cs-new", Origin: "plugin:acme"})
	if err != nil || res.Conflict != "" || res.Before != nil {
		t.Fatalf("create = %+v, %v", res, err)
	}
	var status int
	var origin string
	if err := tx.QueryRow(ctx, `SELECT status_code, origin FROM hatamoto_redirects WHERE id = $1`, res.ID).Scan(&status, &origin); err != nil {
		t.Fatal(err)
	}
	if status != 301 || origin != "plugin:acme" {
		t.Errorf("stored status %d origin %q, want 301 plugin:acme", status, origin)
	}

	// Update returns the pre-image; origin is kept.
	up, err := ApplyChangesetTx(ctx, tx, RedirectOp{ID: &res.ID, Target: "/cs-other", Status: 302, BaseUpdatedAt: &res.UpdatedAt, Origin: "owner"})
	if err != nil || up.Conflict != "" || up.Before == nil {
		t.Fatalf("update = %+v, %v", up, err)
	}
	if b := up.Before; b.SourcePath != "/cs-old" || b.TargetURL != "/cs-new" || b.StatusCode != 301 || !b.IsActive || b.Origin != "plugin:acme" {
		t.Errorf("pre-image = %+v", b)
	}

	// Restoring the pre-image brings the old values back.
	rs, err := RestoreChangesetTx(ctx, tx, *up.Before)
	if err != nil || rs.Conflict != "" {
		t.Fatalf("restore = %+v, %v", rs, err)
	}
	var target string
	if err := tx.QueryRow(ctx, `SELECT target_url, status_code FROM hatamoto_redirects WHERE id = $1`, res.ID).Scan(&target, &status); err != nil {
		t.Fatal(err)
	}
	if target != "/cs-new" || status != 301 {
		t.Errorf("after restore %s %d, want /cs-new 301", target, status)
	}
}

func TestChangesetTxDeleteRestore_Integration(t *testing.T) {
	ctx, tx := rollbackTx(t)
	c, err := ApplyChangesetTx(ctx, tx, RedirectOp{SourcePath: "/cs-del", Target: "https://example.com/x", Status: 308})
	if err != nil {
		t.Fatal(err)
	}
	d, err := ApplyChangesetTx(ctx, tx, RedirectOp{ID: &c.ID, Delete: true})
	if err != nil || d.Before == nil || d.Before.StatusCode != 308 {
		t.Fatalf("delete = %+v, %v", d, err)
	}
	var n int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_redirects WHERE id = $1`, c.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("row still there after delete")
	}
	// A second delete finds nothing: gone, not an error.
	if g, err := ApplyChangesetTx(ctx, tx, RedirectOp{ID: &c.ID, Delete: true}); err != nil || g.Conflict != ConflictGone {
		t.Errorf("second delete = %+v, %v", g, err)
	}
	if rs, err := RestoreChangesetTx(ctx, tx, *d.Before); err != nil || rs.Conflict != "" {
		t.Fatalf("restore = %+v, %v", rs, err)
	}
	var source string
	if err := tx.QueryRow(ctx, `SELECT source_path FROM hatamoto_redirects WHERE id = $1`, c.ID).Scan(&source); err != nil || source != "/cs-del" {
		t.Errorf("restored source = %q, %v", source, err)
	}
}

func TestChangesetTxDuplicateSourceSkipped_Integration(t *testing.T) {
	ctx, tx := rollbackTx(t)
	first, err := ApplyChangesetTx(ctx, tx, RedirectOp{SourcePath: "/cs-dup", Target: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	dup, err := ApplyChangesetTx(ctx, tx, RedirectOp{SourcePath: "/cs-dup", Target: "/b"})
	if err != nil || dup.Conflict != ConflictSourceTaken {
		t.Fatalf("duplicate = %+v, %v; want %s", dup, err, ConflictSourceTaken)
	}
	// The tx is still usable and holds exactly one row, never a suffixed one.
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_redirects WHERE source_path LIKE '/cs-dup%'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d, %v; want 1", n, err)
	}
	// Renaming another redirect onto a taken source is skipped too.
	other, _ := ApplyChangesetTx(ctx, tx, RedirectOp{SourcePath: "/cs-other", Target: "/c"})
	if mv, err := ApplyChangesetTx(ctx, tx, RedirectOp{ID: &other.ID, SourcePath: "/cs-dup"}); err != nil || mv.Conflict != ConflictSourceTaken {
		t.Errorf("rename onto taken = %+v, %v", mv, err)
	}
	_ = first
}

func TestChangesetTxMovedBaseToken_Integration(t *testing.T) {
	ctx, tx := rollbackTx(t)
	c, err := ApplyChangesetTx(ctx, tx, RedirectOp{SourcePath: "/cs-moved", Target: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	stale := c.UpdatedAt.Add(-time.Hour)
	for _, op := range []RedirectOp{
		{ID: &c.ID, Target: "/b", BaseUpdatedAt: &stale},
		{ID: &c.ID, Delete: true, BaseUpdatedAt: &stale},
	} {
		if r, err := ApplyChangesetTx(ctx, tx, op); err != nil || r.Conflict != ConflictMoved {
			t.Errorf("op %+v = %+v, %v; want %s", op, r, err, ConflictMoved)
		}
	}
	var target string
	_ = tx.QueryRow(ctx, `SELECT target_url FROM hatamoto_redirects WHERE id = $1`, c.ID).Scan(&target)
	if target != "/a" {
		t.Errorf("target = %q, a conflict must change nothing", target)
	}
}

func TestChangesetTxRefusesInvalid_Integration(t *testing.T) {
	ctx, tx := rollbackTx(t)
	missing := uuid.New()
	cases := []struct {
		name string
		op   RedirectOp
		want error
	}{
		{"javascript target", RedirectOp{SourcePath: "/cs-bad", Target: "javascript:alert(1)"}, ErrInvalidTarget},
		{"protocol-relative target", RedirectOp{SourcePath: "/cs-bad", Target: "//evil.example"}, ErrInvalidTarget},
		{"empty target", RedirectOp{SourcePath: "/cs-bad"}, ErrInvalidTarget},
		{"external in demo", RedirectOp{SourcePath: "/cs-bad", Target: "https://example.com", SameHostOnly: true}, ErrInvalidTarget},
		{"relative source", RedirectOp{SourcePath: "cs-bad", Target: "/ok"}, ErrInvalidSourcePath},
		{"status 200", RedirectOp{SourcePath: "/cs-bad", Target: "/ok", Status: 200}, ErrInvalidStatusCode},
		{"catch-all source", RedirectOp{SourcePath: "/**", Target: "/ok"}, ErrUnsafePattern},
		{"update without id", RedirectOp{Target: "/ok", Delete: true}, ErrNoRedirectID},
	}
	for _, c := range cases {
		if _, err := ApplyChangesetTx(ctx, tx, c.op); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	// A missing id on update is a conflict (gone), not an error.
	if r, err := ApplyChangesetTx(ctx, tx, RedirectOp{ID: &missing, Target: "/ok"}); err != nil || r.Conflict != ConflictGone {
		t.Errorf("missing id = %+v, %v", r, err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/cs-bad'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("refused ops wrote %d rows, %v", n, err)
	}
}
