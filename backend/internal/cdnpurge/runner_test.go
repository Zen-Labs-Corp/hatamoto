package cdnpurge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRes struct{}

func (fakeRes) URLs(_ context.Context, it Item) []string {
	out := append([]string(nil), it.Paths...)
	for i := range it.IDs {
		out = append(out, fmt.Sprintf("https://example.com/p/%05d", i))
	}
	return out
}

type fakeTarget struct {
	name string
	fn   func(call int, r Request) error
	mu   sync.Mutex
	got  []Request
}

func (f *fakeTarget) Name() string { return f.name }
func (f *fakeTarget) Send(_ context.Context, r Request) error {
	f.mu.Lock()
	f.got = append(f.got, r)
	n := len(f.got)
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(n, r)
	}
	return nil
}
func (f *fakeTarget) calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.got...)
}

func newRunner(targets ...Target) *Runner {
	return New(fakeRes{}, targets, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{Debounce: 10 * time.Millisecond, Backoff: []time.Duration{0, 0}})
}

func TestFlushDedupesAndSorts(t *testing.T) {
	tg := &fakeTarget{name: "a"}
	r := newRunner(tg)
	r.Notify(Item{Paths: []string{"https://example.com/b", "https://example.com/a"}})
	r.Notify(Item{Paths: []string{"https://example.com/a", "https://example.com/c"}})
	r.Flush(context.Background())
	c := tg.calls()
	if len(c) != 1 || fmt.Sprint(c[0].URLs) != "[https://example.com/a https://example.com/b https://example.com/c]" || c[0].All {
		t.Errorf("calls = %+v", c)
	}
}

func TestFlushBatches(t *testing.T) {
	tg := &fakeTarget{name: "a"}
	r := newRunner(tg)
	r.Notify(Item{IDs: make([]uuid.UUID, 1201)})
	r.Flush(context.Background())
	var sizes []int
	for _, c := range tg.calls() {
		sizes = append(sizes, len(c.URLs))
	}
	if fmt.Sprint(sizes) != "[500 500 201]" {
		t.Errorf("sizes = %v", sizes)
	}
}

func TestFlushAll(t *testing.T) {
	tg := &fakeTarget{name: "a"}
	r := newRunner(tg)
	r.Notify(Item{All: true})
	r.Flush(context.Background())
	c := tg.calls()
	if len(c) != 1 || !c[0].All || c[0].URLs == nil || len(c[0].URLs) != 0 {
		t.Errorf("calls = %+v", c)
	}
}

func TestRetryThenSuccess(t *testing.T) {
	tg := &fakeTarget{name: "a", fn: func(n int, _ Request) error {
		if n < 3 {
			return Retryable{errors.New("busy")}
		}
		return nil
	}}
	r := newRunner(tg)
	r.Notify(Item{Paths: []string{"https://example.com/a"}})
	r.Flush(context.Background())
	if n := len(tg.calls()); n != 3 {
		t.Errorf("calls = %d, want 3", n)
	}
}

func TestRetryGivesUpAfterThree(t *testing.T) {
	tg := &fakeTarget{name: "a", fn: func(int, Request) error { return Retryable{errors.New("busy")} }}
	r := newRunner(tg)
	r.Notify(Item{Paths: []string{"https://example.com/a"}})
	r.Flush(context.Background())
	if n := len(tg.calls()); n != 3 {
		t.Errorf("calls = %d, want 3", n)
	}
}

func TestNonRetryableOnce(t *testing.T) {
	tg := &fakeTarget{name: "a", fn: func(int, Request) error { return errors.New("no") }}
	r := newRunner(tg)
	r.Notify(Item{Paths: []string{"https://example.com/a"}})
	r.Flush(context.Background())
	if n := len(tg.calls()); n != 1 {
		t.Errorf("calls = %d, want 1", n)
	}
}

func TestPanickingTargetRecovered(t *testing.T) {
	bad := &fakeTarget{name: "bad", fn: func(int, Request) error { panic("boom") }}
	good := &fakeTarget{name: "good"}
	r := newRunner(bad, good)
	r.Notify(Item{Paths: []string{"https://example.com/a"}})
	r.Flush(context.Background())
	if n := len(good.calls()); n != 1 {
		t.Errorf("good calls = %d, want 1", n)
	}
}

func TestNotifyFullQueueDoesNotBlock(t *testing.T) {
	r := newRunner(&fakeTarget{name: "a"})
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueCap+1; i++ {
			r.Notify(Item{All: true})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Notify blocked on a full queue")
	}
}

func TestRunFlushesOnCancel(t *testing.T) {
	tg := &fakeTarget{name: "a"}
	r := newRunner(tg)
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	for !r.started.Load() {
		time.Sleep(time.Millisecond)
	}
	r.Notify(Item{Paths: []string{"https://example.com/a"}})
	cancel()
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wcancel()
	r.Wait(wctx)
	if wctx.Err() != nil {
		t.Fatal("Wait timed out")
	}
	if n := len(tg.calls()); n != 1 {
		t.Errorf("calls = %d, want 1", n)
	}
}

func TestHungTargetIsBoundedByTimeout(t *testing.T) {
	blocker := blockTarget{}
	ok := &fakeTarget{name: "ok"}
	r := New(fakeRes{}, []Target{blocker, ok}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{Backoff: []time.Duration{}, SendTimeout: 20 * time.Millisecond})
	r.Notify(Item{Paths: []string{"https://example.com/a"}})
	done := make(chan struct{})
	go func() { r.Flush(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Flush stalled on a purger that waits for its context")
	}
	if len(ok.calls()) != 1 {
		t.Errorf("second target got %d calls, want 1", len(ok.calls()))
	}
}

type blockTarget struct{}

func (blockTarget) Name() string { return "block" }
func (blockTarget) Send(ctx context.Context, _ Request) error {
	<-ctx.Done()
	return ctx.Err()
}
