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
	go func() { time.Sleep(100 * time.Millisecond); close(release) }()
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
	var sawBound atomic.Bool
	load := func(ctx context.Context) (int, error) {
		sawBound.Store(AcquireBound(ctx) != 0)
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
	time.Sleep(20 * time.Millisecond)
	if sawBound.Load() {
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
