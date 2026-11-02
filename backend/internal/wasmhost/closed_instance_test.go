package wasmhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	extism "github.com/extism/go-sdk"
	"hatamoto.dev/sdk"
)

func TestClosedPooledInstanceIsReplaced(t *testing.T) {
	logs := &lockedBuf{}
	h, err := New(slog.New(slog.NewTextHandler(logs, nil)), cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	load(t, h, "stub", sdk.Limits{MaxInstances: 1})
	if out, err := call(h, "stub", "echo", "x"); err != nil || out != "x" {
		t.Fatalf("warm-up: %q %v", out, err)
	}

	// Poison the pooled instance the way a cancelled call leaves it.
	p := (*h.reg.Load())["stub"]
	inst := <-p.pool
	if err := inst.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.pool <- inst

	if out, err := call(h, "stub", "echo", "y"); err != nil || out != "y" {
		t.Fatalf("call on a closed pooled instance: %q %v", out, err)
	}
	if got := h.Discarded("stub"); got != 1 {
		t.Fatalf("discarded = %d, want 1", got)
	}
	if n := strings.Count(logs.String(), "wasmhost: discarded closed plugin instance"); n != 1 {
		t.Fatalf("warn logged %d times, want 1:\n%s", n, logs.String())
	}
	if l := logs.String(); !strings.Contains(l, "plugin=stub") || !strings.Contains(l, "export=echo") {
		t.Fatalf("warn lacks plugin/export attrs:\n%s", l)
	}
	if out, err := call(h, "stub", "echo", "z"); err != nil || out != "z" {
		t.Fatalf("follow-up: %q %v", out, err)
	}
}

func TestIsGuestErrorRejectsClosed(t *testing.T) {
	if !isGuestError(errors.New("boom")) {
		t.Error("a plain guest error must be a guest error")
	}
	if isGuestError(fmt.Errorf("module is closed")) {
		t.Error("a closed module is not a guest error")
	}
	for _, text := range []string{"reset", "module is closed"} {
		if isGuestError(errors.New(text)) || !isClosedErr(errors.New(text)) {
			t.Errorf("%q must be closed and not a guest error", text)
		}
	}
	if !isResetErr(errors.New("reset")) || isResetErr(errors.New("module is closed")) {
		t.Error("only the exact reset text is a reset error")
	}
	if isClosedErr(nil) || isClosedErr(errors.New("boom")) || !isClosedErr(errors.New("module is closed")) {
		t.Error("isClosedErr matches the extism text only")
	}
}

func TestCancellationStormThenHealthy(t *testing.T) {
	h := newHost(t)
	load(t, h, "stub", sdk.Limits{MaxInstances: 2, HookTimeoutsMS: timeouts(2000, "hostile_loop", "echo")})
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(1+i%5)*time.Millisecond)
			defer cancel()
			export := "echo"
			if i%4 == 0 {
				export = "hostile_loop"
			}
			_, _ = h.Call(ctx, "stub", export, []byte("x")) // outcome is irrelevant
		})
	}
	wg.Wait()

	// The storm's own cut calls may have opened echo's breaker; that is not
	// what is under test, so start the final call from a closed breaker.
	(*h.reg.Load())["stub"].succeeded("echo")

	// Whatever the storm left in the pool, a deadline-free call must work.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := h.Call(ctx, "stub", "echo", []byte("ok"))
	if err != nil || string(out) != "ok" {
		t.Fatalf("healthy call after the storm: %q %v", out, err)
	}
}

// Every instance is closed right before its call, so both attempts of Call
// see a closed module.
func TestClosedTwiceIsNeitherSuccessNorFailure(t *testing.T) {
	h := newHost(t)
	load(t, h, "stub", sdk.Limits{MaxInstances: 1})
	p := (*h.reg.Load())["stub"]
	p.breakers = map[string]*breaker{"echo": {fails: 2}}
	p.beforeCall = func(inst *extism.Plugin) { _ = inst.Close(context.Background()) }

	if _, err := call(h, "stub", "echo", "x"); err == nil || !strings.Contains(err.Error(), "module is closed") {
		t.Fatalf("want a closed-module error, got %v", err)
	}
	if b := p.breakers["echo"]; b == nil || b.fails != 2 {
		t.Fatalf("breaker = %+v, want untouched (2 fails): not a success, not a failure", b)
	}
	if got := h.Discarded("stub"); got != 2 {
		t.Fatalf("discarded = %d, want 2", got)
	}
}

// The retry's cold start must not be charged to the hook timeout: with a
// timeout far below the start time the retried call still succeeds.
func TestRetryGetsFreshTimeout(t *testing.T) {
	h := newHost(t)
	load(t, h, "stub", sdk.Limits{MaxInstances: 1, HookTimeoutsMS: timeouts(1000, "echo")})
	p := (*h.reg.Load())["stub"]
	first := true
	p.beforeCall = func(inst *extism.Plugin) {
		if first {
			first = false
			_ = inst.Close(context.Background())
			time.Sleep(1100 * time.Millisecond) // stands in for a slow cold start
		}
	}
	if out, err := call(h, "stub", "echo", "x"); err != nil || out != "x" {
		t.Fatalf("retried call: %q %v", out, err)
	}
}

// A rejection or guest error that returns with the deadline passed keeps its
// old result and is not charged, but its instance is never pooled.
func TestRejectWithDeadAfterCallDiscards(t *testing.T) {
	h := newHost(t)
	m := hookManifest("rej", sdk.Limits{MaxInstances: 1}, map[string]string{"mode": "reject"})
	if err := h.Load(context.Background(), m, hostileWasm); err != nil {
		t.Fatal(err)
	}
	p := (*h.reg.Load())["rej"]
	p.breakers = map[string]*breaker{"filter_entity_before_save": {fails: 2}}
	p.afterCall = func(cancel context.CancelFunc) { cancel() }

	_, err := call(h, "rej", "filter_entity_before_save", "{}")
	if err == nil || !strings.HasPrefix(err.Error(), sdk.RejectPrefix) {
		t.Fatalf("want the bare rejection, got %v", err)
	}
	if p.breakers["filter_entity_before_save"] != nil {
		t.Fatal("a rejection counts as a success: the breaker must be cleared")
	}
	if got := h.Discarded("rej"); got != 1 || len(p.pool) != 0 {
		t.Fatalf("discarded = %d, pooled = %d, want 1 and 0", got, len(p.pool))
	}
}

func TestGuestErrorWithDeadAfterCallDiscards(t *testing.T) {
	h := newHost(t)
	load(t, h, "stub", sdk.Limits{MaxInstances: 1})
	p := (*h.reg.Load())["stub"]
	p.breakers = map[string]*breaker{"guest_error": {fails: 2}}
	p.afterCall = func(cancel context.CancelFunc) { cancel() }

	_, err := call(h, "stub", "guest_error", "")
	if err == nil || !strings.HasPrefix(err.Error(), "stub.guest_error: ") || !strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), " after ") {
		t.Fatalf("want the plain guest error, got %v", err)
	}
	if p.breakers["guest_error"] != nil {
		t.Fatal("breaker must be cleared")
	}
	if got := h.Discarded("stub"); got != 1 || len(p.pool) != 0 {
		t.Fatalf("discarded = %d, pooled = %d, want 1 and 0", got, len(p.pool))
	}
}
