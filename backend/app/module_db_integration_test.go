//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/modhost"
	moddb "hatamoto.dev/core/module/db"
)

func TestModuleDBPool_Integration(t *testing.T) {
	t.Cleanup(modhost.Reset)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, scratchDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	clear := publishModulePool(pool)

	got := make(chan int, 1)
	modhost.RegisterService(modhost.Service{Name: "pool_query", Run: func(ctx context.Context) error {
		p, err := moddb.Pool()
		if err != nil {
			return err
		}
		var n int
		if err := p.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
			return err
		}
		got <- n
		<-ctx.Done()
		return nil
	}})
	svcCtx, cancel := context.WithCancel(ctx)
	wait := startModuleServices(svcCtx, quietLogger())
	select {
	case n := <-got:
		if n != 1 {
			t.Fatalf("service query = %d", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("service did not report a query result")
	}
	cancel()
	wait(5 * time.Second)

	p, err := moddb.Pool()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := p.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
		t.Fatalf("query = %d, %v", n, err)
	}
	clear()
}
