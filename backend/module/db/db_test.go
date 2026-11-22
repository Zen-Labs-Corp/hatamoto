package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/module/db"
)

var _ func() (*pgxpool.Pool, error) = db.Pool

func TestPool(t *testing.T) {
	t.Cleanup(db.ResetForTest)
	if p, err := db.Pool(); p != nil || !errors.Is(err, db.ErrNotReady) {
		t.Fatalf("unset: Pool() = %v, %v", p, err)
	}
	p, err := pgxpool.New(context.Background(), "postgres://x@127.0.0.1:1/x") // lazy: does not dial
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	db.SetPoolForTest(p)
	if got, err := db.Pool(); got != p || err != nil {
		t.Fatalf("set: Pool() = %v, %v", got, err)
	}
	db.SetPoolForTest(nil)
	if _, err := db.Pool(); !errors.Is(err, db.ErrNotReady) {
		t.Fatalf("after clear: err = %v", err)
	}
}
