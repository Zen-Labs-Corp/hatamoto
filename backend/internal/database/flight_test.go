package database

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const flightCallers = 50

// runCallers starts n goroutines that call fn behind a barrier and returns
// their errors once all are done.
func runCallers(n int, fn func() error) []error {
	var wg, ready sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		ready.Add(1)
		go func(i int) {
			defer wg.Done()
			ready.Done()
			<-start
			errs[i] = fn()
		}(i)
	}
	ready.Wait()
	close(start)
	wg.Wait()
	return errs
}

func TestFlightSharesOneLoad(t *testing.T) {
	t.Parallel()
	var f Flight[int]
	var calls atomic.Int32
	release := make(chan struct{})
	load := func(context.Context) (int, error) {
		calls.Add(1)
		<-release
		return 7, nil
	}
	go func() {
		if !f.WaitForWaiters(flightCallers, 30*time.Second) {
			t.Error("not every caller joined the flight")
		}
		close(release)
	}()
	errs := runCallers(flightCallers, func() error {
		v, err := f.Do(context.Background(), load)
		if err == nil && v != 7 {
			return errors.New("wrong value")
		}
		return err
	})
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("loader ran %d times, want 1", n)
	}
}

func TestFlightSharesError(t *testing.T) {
	t.Parallel()
	var f Flight[int]
	var calls atomic.Int32
	boom := errors.New("boom")
	load := func(context.Context) (int, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return 0, boom
	}
	errs := runCallers(flightCallers, func() error {
		_, err := f.Do(context.Background(), load)
		return err
	})
	for _, err := range errs {
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	}
	// Late callers inside the window do not retry either.
	if _, err := f.Do(context.Background(), load); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("loader ran %d times, want 1", n)
	}
}

func TestFlightRetriesAfterErrWindow(t *testing.T) {
	t.Parallel()
	var f Flight[int]
	now := time.Unix(1000, 0)
	var mu sync.Mutex
	f.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	var calls atomic.Int32
	load := func(context.Context) (int, error) {
		if calls.Add(1) == 1 {
			return 0, errors.New("first fails")
		}
		return 2, nil
	}
	if _, err := f.Do(context.Background(), load); err == nil {
		t.Fatal("want error")
	}
	mu.Lock()
	now = now.Add(2 * time.Second)
	mu.Unlock()
	v, err := f.Do(context.Background(), load)
	if err != nil || v != 2 || calls.Load() != 2 {
		t.Fatalf("v=%d err=%v calls=%d", v, err, calls.Load())
	}
}

func TestFlightForgetDuringFlightDropsResult(t *testing.T) {
	t.Parallel()
	var f Flight[int]
	var committed []int
	var cmu sync.Mutex
	commit := func(v int) { cmu.Lock(); committed = append(committed, v); cmu.Unlock() }

	oldStarted, oldRelease := make(chan struct{}), make(chan struct{})
	oldDone := make(chan int, 1)
	go func() {
		v, _ := f.DoCommit(context.Background(), func(context.Context) (int, error) {
			close(oldStarted)
			<-oldRelease
			return 1, nil
		}, commit)
		oldDone <- v
	}()
	<-oldStarted
	f.Forget()
	v, err := f.DoCommit(context.Background(), func(context.Context) (int, error) { return 2, nil }, commit)
	if err != nil || v != 2 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	close(oldRelease)
	<-oldDone
	cmu.Lock()
	defer cmu.Unlock()
	if len(committed) != 1 || committed[0] != 2 {
		t.Fatalf("committed %v, the older result must not be committed", committed)
	}
}

func TestFlightWaiterCancelReturnsWhileFlightContinues(t *testing.T) {
	t.Parallel()
	var f Flight[int]
	release := make(chan struct{})
	var finished atomic.Bool
	load := func(context.Context) (int, error) {
		<-release
		finished.Store(true)
		return 3, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := f.Do(ctx, load); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if finished.Load() {
		t.Fatal("flight must outlive the cancelled waiter")
	}
	close(release)
	v, err := f.Do(context.Background(), load)
	if err != nil || v != 3 {
		t.Fatalf("v=%d err=%v", v, err)
	}
}

func TestFlightBoundedWaiterGetsPoolBusy(t *testing.T) {
	t.Parallel()
	var f Flight[int]
	release := make(chan struct{})
	defer close(release)
	bounded := make(chan bool, 1)
	load := func(ctx context.Context) (int, error) {
		bounded <- AcquireBound(ctx) != 0
		<-release
		return 1, nil
	}
	ctx := WithAcquireBound(context.Background(), 30*time.Millisecond)
	start := time.Now()
	_, err := f.Do(ctx, load)
	if !errors.Is(err, ErrPoolBusy) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("bound not honoured")
	}
	if <-bounded {
		t.Fatal("the shared load must not inherit the acquire bound")
	}
}

func TestFlightRecoversLoaderPanic(t *testing.T) {
	t.Parallel()
	var f Flight[int]
	_, err := f.Do(context.Background(), func(context.Context) (int, error) { panic("kaput") })
	if err == nil {
		t.Fatal("want error from panic")
	}
}

// blockUntilDone is a loader that only ends when its context does.
func blockUntilDone(entered chan<- struct{}, calls *atomic.Int32) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		if calls.Add(1) == 1 && entered != nil {
			close(entered)
		}
		if calls.Load() > 1 {
			return 42, nil
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
}

// The cap belongs to bounded (public) callers: an unbounded caller that shared
// a capped load must lead its own instead of inheriting the cap failure.
func TestFlightCapFailureIsNotHandedToUnboundedCaller(t *testing.T) {
	t.Parallel()
	f := Flight[int]{Cap: 40 * time.Millisecond}
	var calls atomic.Int32
	entered := make(chan struct{})
	load := blockUntilDone(entered, &calls)

	boundedErr := make(chan error, 1)
	go func() {
		_, err := f.Do(WithAcquireBound(context.Background(), time.Minute), load)
		boundedErr <- err
	}()
	<-entered
	v, err := f.Do(context.Background(), load)
	if err != nil || v != 42 {
		t.Fatalf("unbounded caller: v=%d err=%v, want its own successful load", v, err)
	}
	<-boundedErr
}

// A cap expiry must not poison the next callers for the error window.
func TestFlightCapFailureIsNotRemembered(t *testing.T) {
	t.Parallel()
	f := Flight[int]{Cap: 20 * time.Millisecond, ErrWindow: time.Hour}
	var calls atomic.Int32
	load := blockUntilDone(nil, &calls)
	if _, err := f.Do(WithAcquireBound(context.Background(), time.Minute), load); err == nil {
		t.Fatal("want the cap error for the bounded starter")
	}
	v, err := f.Do(context.Background(), load)
	if err != nil || v != 42 || calls.Load() != 2 {
		t.Fatalf("v=%d err=%v calls=%d, want a fresh load", v, err, calls.Load())
	}
}

// A load started by an unbounded caller (admin, API, gate) is not capped.
func TestFlightUnboundedStarterHasNoCap(t *testing.T) {
	t.Parallel()
	f := Flight[int]{Cap: 10 * time.Millisecond}
	v, err := f.Do(context.Background(), func(ctx context.Context) (int, error) {
		select {
		case <-time.After(80 * time.Millisecond):
			return 5, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	if err != nil || v != 5 {
		t.Fatalf("v=%d err=%v", v, err)
	}
}
