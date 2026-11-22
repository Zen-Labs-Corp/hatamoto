package modhost

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPool(t *testing.T) {
	t.Cleanup(Reset)
	if p, err := Pool(); p != nil || !errors.Is(err, ErrNotReady) {
		t.Fatalf("unset: Pool() = %v, %v", p, err)
	}
	p, err := pgxpool.New(context.Background(), "postgres://x@127.0.0.1:1/x") // lazy: does not dial
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	SetPool(p)
	if got, err := Pool(); got != p || err != nil {
		t.Fatalf("set: Pool() = %v, %v", got, err)
	}
	SetPool(nil)
	if _, err := Pool(); !errors.Is(err, ErrNotReady) {
		t.Fatalf("after SetPool(nil): err = %v", err)
	}
	SetPool(p)
	Reset()
	if _, err := Pool(); !errors.Is(err, ErrNotReady) {
		t.Fatalf("after Reset: err = %v", err)
	}
}
