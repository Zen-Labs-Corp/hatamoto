package fonts

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func listBurst(s *Store, n int) ([][]Font, []error) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	out, errs := make([][]Font, n), make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			out[i], errs[i] = s.List(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	return out, errs
}

func TestListConcurrentCallersShareOneQuery(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	release := make(chan struct{})
	s := &Store{list: func(context.Context) ([]Font, error) {
		calls.Add(1)
		<-release
		return []Font{{Family: "Inter", Weight: 400}}, nil
	}}
	go func() { time.Sleep(100 * time.Millisecond); close(release) }()
	out, errs := listBurst(s, 50)
	for i := range out {
		if errs[i] != nil || len(out[i]) != 1 || out[i][0].Family != "Inter" {
			t.Fatalf("caller %d: %v %v", i, out[i], errs[i])
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("query ran %d times, want 1", n)
	}
	out[0][0].Family = "mutated"
	if out[1][0].Family != "Inter" {
		t.Fatal("callers must not share one slice")
	}
}

func TestListFailedQueryIsShared(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	boom := errors.New("db down")
	s := &Store{list: func(context.Context) ([]Font, error) {
		calls.Add(1)
		time.Sleep(30 * time.Millisecond)
		return nil, boom
	}}
	_, errs := listBurst(s, 50)
	for _, err := range errs {
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("query ran %d times, want 1", n)
	}
}
