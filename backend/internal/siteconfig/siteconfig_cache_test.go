package siteconfig

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func seamStore(load func(context.Context) (*Config, error)) *Store {
	return &Store{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), loader: load}
}

func getBurst(s *Store, n int) ([]*Config, []error) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	cfgs, errs := make([]*Config, n), make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cfgs[i], errs[i] = s.Get(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	return cfgs, errs
}

func TestGetColdBurstSharesOneLoad(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	release := make(chan struct{})
	s := seamStore(func(context.Context) (*Config, error) {
		calls.Add(1)
		<-release
		return &Config{CompanyName: "Example"}, nil
	})
	go func() { time.Sleep(100 * time.Millisecond); close(release) }()
	cfgs, errs := getBurst(s, 50)
	for i := range cfgs {
		if errs[i] != nil || cfgs[i].CompanyName != "Example" {
			t.Fatalf("caller %d: %v %v", i, cfgs[i], errs[i])
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("loader ran %d times, want 1", n)
	}
	// Warm now: no further load.
	if _, err := s.Get(context.Background()); err != nil || calls.Load() != 1 {
		t.Fatalf("warm Get reloaded: err=%v calls=%d", err, calls.Load())
	}
}

func TestGetFailedLoadIsShared(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	boom := errors.New("db down")
	s := seamStore(func(context.Context) (*Config, error) {
		calls.Add(1)
		time.Sleep(30 * time.Millisecond)
		return nil, boom
	})
	_, errs := getBurst(s, 50)
	for _, err := range errs {
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("loader ran %d times, want 1", n)
	}
}

func TestInvalidateDuringLoadIsNotOverwritten(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	release := make(chan struct{})
	s := seamStore(func(context.Context) (*Config, error) {
		if calls.Add(1) == 1 {
			<-release
			return &Config{CompanyName: "old"}, nil
		}
		return &Config{CompanyName: "new"}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); _, _ = s.Get(context.Background()) }()
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	s.Invalidate()
	close(release)
	<-done
	time.Sleep(20 * time.Millisecond)

	cfg, err := s.Get(context.Background())
	if err != nil || cfg.CompanyName != "new" {
		t.Fatalf("got %+v %v; the older load must not be cached over the invalidation", cfg, err)
	}
}

func TestGetServesStaleOnError(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	s := seamStore(func(context.Context) (*Config, error) {
		if fail.Load() {
			return nil, errors.New("db down")
		}
		return &Config{CompanyName: "cached"}, nil
	})
	if _, err := s.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	s.mu.Lock()
	s.cachedAt = time.Now().Add(-2 * cacheTTL)
	s.mu.Unlock()
	cfg, err := s.Get(context.Background())
	if err != nil || cfg.CompanyName != "cached" {
		t.Fatalf("got %+v %v, want stale copy", cfg, err)
	}
}
