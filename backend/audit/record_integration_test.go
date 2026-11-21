//go:build integration

package audit_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/audit"
)

type failingSink struct{}

func (failingSink) Record(context.Context, pgx.Tx, audit.Event) error { return errors.New("refused") }

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func rows(t *testing.T, p *pgxpool.Pool, target string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_admin_audit WHERE target = $1`, target).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordThroughTx(t *testing.T) {
	reset(t)
	p := testPool(t)
	ctx := context.Background()
	target := "t-" + uuid.NewString()
	ev := audit.Event{Actor: "mod", ActorKind: audit.KindPlugin, Action: "test.record", Target: target}

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Record(ctx, tx, ev); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rows(t, p, target); n != 0 {
		t.Fatalf("rows after rollback = %d, want 0", n)
	}

	tx, err = p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Record(ctx, tx, ev); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var kind, result string
	if err := p.QueryRow(ctx, `SELECT actor_kind, result FROM hatamoto_admin_audit WHERE target = $1`, target).Scan(&kind, &result); err != nil {
		t.Fatal(err)
	}
	if kind != audit.KindPlugin || result != audit.ResultOK {
		t.Fatalf("kind/result = %q/%q", kind, result)
	}
}

func TestRecordSinkErrorRollsBack(t *testing.T) {
	reset(t)
	p := testPool(t)
	ctx := context.Background()
	audit.Register(failingSink{})
	target := "t-" + uuid.NewString()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = audit.Record(ctx, tx, audit.Event{Actor: "mod", Action: "test.sink", Target: target})
	if !errors.Is(err, audit.ErrSink) {
		t.Fatalf("err = %v, want ErrSink", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rows(t, p, target); n != 0 {
		t.Fatalf("rows = %d, want 0", n)
	}
}
