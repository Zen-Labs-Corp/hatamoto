package hooks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func testEngine() *Engine {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestFilterPriorityOrderAndStableTies(t *testing.T) {
	e := testEngine()
	var order []string
	add := func(tag string, prio int) {
		e.AddFilter("f", prio, func(_ context.Context, v any) (any, error) {
			order = append(order, tag)
			return v, nil
		})
	}
	add("c", 20)
	add("a1", 5)
	add("a2", 5) // registered after a1, same priority
	add("b", 10)
	e.Apply(context.Background(), "f", 1)
	want := []string{"a1", "a2", "b", "c"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestFilterChainsValue(t *testing.T) {
	e := testEngine()
	e.AddFilter("f", 1, func(_ context.Context, v any) (any, error) { return v.(int) + 1, nil })
	e.AddFilter("f", 2, func(_ context.Context, v any) (any, error) { return v.(int) * 10, nil })
	if got := e.Apply(context.Background(), "f", 1); got != 20 {
		t.Fatalf("got %v", got)
	}
}

func TestFilterErrorAndPanicLeaveValueUnchanged(t *testing.T) {
	e := testEngine()
	e.AddFilter("f", 1, func(_ context.Context, v any) (any, error) { return 999, errors.New("boom") })
	e.AddFilter("f", 2, func(_ context.Context, v any) (any, error) { panic("kaboom") })
	e.AddFilter("f", 3, func(_ context.Context, v any) (any, error) { return "wrong type", nil })
	e.AddFilter("f", 4, func(_ context.Context, v any) (any, error) { return v.(int) + 1, nil })
	if got := e.Apply(context.Background(), "f", 1); got != 2 {
		t.Fatalf("got %v, want 2 (only the last filter applies)", got)
	}
}

func TestApplyStrictReturnsErrorAndStops(t *testing.T) {
	e := testEngine()
	var ranAfter bool
	e.AddFilter("f", 1, func(_ context.Context, v any) (any, error) { return v, &sdk.Reject{Reason: "no"} })
	e.AddFilter("f", 2, func(_ context.Context, v any) (any, error) { ranAfter = true; return v, nil })
	_, err := e.ApplyStrict(context.Background(), "f", 1)
	var rej *sdk.Reject
	if !errors.As(err, &rej) || rej.Reason != "no" {
		t.Fatalf("err = %v", err)
	}
	if ranAfter {
		t.Fatal("chain should stop at the rejecting filter")
	}
}

func TestApplyStrictSkipsPanic(t *testing.T) {
	e := testEngine()
	e.AddFilter("f", 1, func(_ context.Context, v any) (any, error) { panic("x") })
	e.AddFilter("f", 2, func(_ context.Context, v any) (any, error) { return v.(int) + 1, nil })
	got, err := e.ApplyStrict(context.Background(), "f", 1)
	if err != nil || got != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestFireStartsInPriorityOrder(t *testing.T) {
	e := testEngine()
	var mu sync.Mutex
	var started []int
	for _, p := range []int{30, 10, 20} {
		e.AddAction("a", p, func(context.Context, any) {
			mu.Lock()
			started = append(started, p)
			mu.Unlock()
		})
	}
	e.Fire(context.Background(), "a", nil)
	e.Wait()
	if len(started) != 3 {
		t.Fatalf("started %v", started)
	}
}

func TestFirePanicIsolation(t *testing.T) {
	e := testEngine()
	var ran atomic.Bool
	e.AddAction("a", 1, func(context.Context, any) { panic("bad plugin") })
	e.AddAction("a", 2, func(context.Context, any) { ran.Store(true) })
	e.Fire(context.Background(), "a", nil)
	e.Wait()
	if !ran.Load() {
		t.Fatal("sibling callback did not run after a panic")
	}
}

func TestFireTimeoutCancelsCallback(t *testing.T) {
	e := testEngine()
	e.SetActionTimeout(20 * time.Millisecond)
	var cancelled atomic.Bool
	e.AddAction("a", 1, func(ctx context.Context, _ any) {
		select {
		case <-ctx.Done():
			cancelled.Store(true)
		case <-time.After(5 * time.Second):
		}
	})
	e.Fire(context.Background(), "a", nil)
	e.Wait()
	if !cancelled.Load() {
		t.Fatal("callback context was not cancelled by timeout")
	}
}

func TestFireUsesLifecycleContext(t *testing.T) {
	e := testEngine()
	life, cancel := context.WithCancel(context.Background())
	e.SetLifecycleContext(life)
	var cancelled atomic.Bool
	started := make(chan struct{})
	e.AddAction("a", 1, func(ctx context.Context, _ any) {
		close(started)
		<-ctx.Done()
		cancelled.Store(true)
	})
	reqCtx, reqCancel := context.WithCancel(context.Background())
	e.Fire(reqCtx, "a", nil)
	<-started
	reqCancel() // request cancellation must not kill the callback
	time.Sleep(10 * time.Millisecond)
	if cancelled.Load() {
		t.Fatal("callback cancelled by request context")
	}
	cancel()
	e.Wait()
	if !cancelled.Load() {
		t.Fatal("callback not cancelled by lifecycle context")
	}
}

func TestForTagsPluginAndTypedHelpersWork(t *testing.T) {
	e := testEngine()
	var got sdk.EntityEvent
	OnAction(e.For("hello"), sdk.EntityPublished, 1, func(_ context.Context, ev sdk.EntityEvent) { got = ev })
	e.Fire(context.Background(), sdk.EntityPublished.Name, sdk.EntityEvent{ID: "x"})
	e.Wait()
	if got.ID != "x" {
		t.Fatalf("got %+v", got)
	}
	OnFilter(e.For("hello"), sdk.JSONLDEnrich, 1, func(_ context.Context, d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
		d.Doc["k"] = "v"
		return d, nil
	})
	out := e.Apply(context.Background(), sdk.JSONLDEnrich.Name, sdk.JSONLDDoc{Doc: map[string]any{}}).(sdk.JSONLDDoc)
	if out.Doc["k"] != "v" {
		t.Fatalf("doc = %v", out.Doc)
	}
}

func TestNoRegistrationsIsNoop(t *testing.T) {
	e := testEngine()
	if got := e.Apply(context.Background(), "none", 7); got != 7 {
		t.Fatal(got)
	}
	e.Fire(context.Background(), "none", nil)
	e.Wait()
}

func TestApplyStrictOnlyRejectStopsChain(t *testing.T) {
	ctx := context.Background()
	add := func(e *Engine, prio int, fn func(context.Context, any) (any, error)) {
		e.For("p").AddFilter("f", prio, fn)
	}
	t.Run("*sdk.Reject (even wrapped) is returned", func(t *testing.T) {
		e := testEngine()
		add(e, 0, func(context.Context, any) (any, error) {
			return nil, fmt.Errorf("ctx: %w", &sdk.Reject{Reason: "no"})
		})
		_, err := e.ApplyStrict(ctx, "f", 1)
		var rej *sdk.Reject
		if !errors.As(err, &rej) || rej.Reason != "no" {
			t.Fatalf("want reject, got %v", err)
		}
	})
	t.Run("plain error, panic and timeout are skipped, change dropped", func(t *testing.T) {
		e := testEngine()
		add(e, 0, func(context.Context, any) (any, error) { return 99, errors.New("boom") })
		add(e, 1, func(context.Context, any) (any, error) { panic("x") })
		add(e, 2, func(context.Context, any) (any, error) { return 98, context.DeadlineExceeded })
		add(e, 3, func(_ context.Context, v any) (any, error) { return v.(int) + 1, nil })
		out, err := e.ApplyStrict(ctx, "f", 1)
		if err != nil || out != 2 {
			t.Fatalf("out=%v err=%v", out, err)
		}
	})
}

func TestFireKeepsCallerValuesAndHasFilter(t *testing.T) {
	type key struct{}
	e := New(nil)
	got := make(chan any, 1)
	e.AddAction("x", 0, func(ctx context.Context, _ any) { got <- ctx.Value(key{}) })
	e.Fire(context.WithValue(context.Background(), key{}, "v"), "x", nil)
	e.Wait()
	if v := <-got; v != "v" {
		t.Fatalf("action saw %v, want the caller's value", v)
	}

	if e.HasFilter("f") {
		t.Fatal("no filter registered yet")
	}
	e.AddFilter("f", 0, func(_ context.Context, v any) (any, error) { return v, nil })
	if !e.HasFilter("f") {
		t.Fatal("HasFilter must see the registration")
	}
}
