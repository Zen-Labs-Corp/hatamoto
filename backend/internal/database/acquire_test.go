package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type blockingAcquirer struct{}

func (blockingAcquirer) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestAcquireBoundHitReturnsPoolBusy(t *testing.T) {
	ctx := WithAcquireBound(context.Background(), 50*time.Millisecond)
	start := time.Now()
	_, err := acquire(ctx, blockingAcquirer{})
	if !errors.Is(err, ErrPoolBusy) {
		t.Fatalf("err = %v, want ErrPoolBusy", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestAcquireParentCancelIsNotPoolBusy(t *testing.T) {
	ctx, cancel := context.WithCancel(WithAcquireBound(context.Background(), time.Minute))
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := acquire(ctx, blockingAcquirer{})
	if err == nil || errors.Is(err, ErrPoolBusy) {
		t.Fatalf("err = %v, want non-ErrPoolBusy", err)
	}
}

func TestAcquireParentDeadlineIsNotPoolBusy(t *testing.T) {
	ctx, cancel := context.WithTimeout(WithAcquireBound(context.Background(), time.Minute), 30*time.Millisecond)
	defer cancel()
	_, err := acquire(ctx, blockingAcquirer{})
	if err == nil || errors.Is(err, ErrPoolBusy) {
		t.Fatalf("err = %v, want non-ErrPoolBusy", err)
	}
}

func TestAcquireWithoutBoundWaitsForParent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := acquire(ctx, blockingAcquirer{})
	if errors.Is(err, ErrPoolBusy) || err == nil {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) < 140*time.Millisecond {
		t.Fatal("returned before parent deadline")
	}
}

func TestWithoutAcquireBound(t *testing.T) {
	ctx := WithAcquireBound(context.Background(), time.Second)
	if AcquireBound(ctx) != time.Second || AcquireBound(WithoutAcquireBound(ctx)) != 0 {
		t.Fatal("bound marker not set/cleared")
	}
	if AcquireBound(WithAcquireBound(context.Background(), 0)) != 0 {
		t.Fatal("zero bound must be a no-op")
	}
}

func TestPoolBusyHitFlagsSwallowedBoundedAcquire(t *testing.T) {
	ctx := WithAcquireBound(context.Background(), 20*time.Millisecond)
	if PoolBusyHit(ctx) {
		t.Fatal("flag set before any acquire")
	}
	if _, err := acquire(ctx, blockingAcquirer{}); !errors.Is(err, ErrPoolBusy) {
		t.Fatal(err)
	}
	if !PoolBusyHit(ctx) {
		t.Fatal("flag not set after ErrPoolBusy")
	}
	if PoolBusyHit(context.Background()) || PoolBusyHit(WithoutAcquireBound(ctx)) {
		t.Fatal("flag leaked to an unbounded ctx")
	}
}
