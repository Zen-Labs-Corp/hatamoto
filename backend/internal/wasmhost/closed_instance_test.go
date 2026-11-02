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
	// The closed instance neither counted as a success nor a failure.
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

	// Whatever the storm left in the pool, a deadline-free call must work.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := h.Call(ctx, "stub", "echo", []byte("ok"))
	if err != nil || string(out) != "ok" {
		t.Fatalf("healthy call after the storm: %q %v", out, err)
	}
}
