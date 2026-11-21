package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/modhost"
)

func TestStartModuleServices(t *testing.T) {
	t.Cleanup(modhost.Reset)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	var skipped, ran atomic.Bool
	stopped := make(chan struct{})

	modhost.RegisterService(modhost.Service{Name: "pan", Run: func(context.Context) error { panic("boom") }})
	modhost.RegisterService(modhost.Service{Name: "failing", Run: func(context.Context) error { return errors.New("bad things") }})
	modhost.RegisterService(modhost.Service{Name: "waits", Run: func(ctx context.Context) error {
		ran.Store(true)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}})
	modhost.RegisterService(modhost.Service{Name: "broken", Run: func(context.Context) error { skipped.Store(true); return nil }})
	modhost.SetMigrationErr("broken", errors.New("migration failed"))

	ctx, cancel := context.WithCancel(context.Background())
	wait := startModuleServices(ctx, logger)
	cancel()
	done := make(chan struct{})
	go func() { wait(5 * time.Second); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("services did not stop on cancel")
	}
	<-stopped
	if skipped.Load() {
		t.Error("service of a module with failed migrations was started")
	}
	out := buf.String()
	for _, want := range []string{"module service panicked", "boom", "bad things", "not started"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "context canceled") {
		t.Errorf("cancellation was logged as an error:\n%s", out)
	}
}

func TestModuleServicesWaitDeadline(t *testing.T) {
	t.Cleanup(modhost.Reset)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	modhost.RegisterService(modhost.Service{Name: "stuck", Run: func(context.Context) error { <-release; return nil }})
	modhost.RegisterService(modhost.Service{Name: "polite", Run: func(ctx context.Context) error { <-ctx.Done(); return nil }})

	ctx, cancel := context.WithCancel(context.Background())
	wait := startModuleServices(ctx, logger)
	cancel()
	start := time.Now()
	wait(300 * time.Millisecond)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("wait took %v, want about the deadline", d)
	}
	out := buf.String()
	if !strings.Contains(out, "did not stop in time") || !strings.Contains(out, "stuck") || strings.Contains(out, "polite") {
		t.Errorf("log should name only the stuck service:\n%s", out)
	}
}

func TestStartModuleServicesNoneRegistered(t *testing.T) {
	t.Cleanup(modhost.Reset)
	before := runtime.NumGoroutine()
	wait := startModuleServices(context.Background(), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	start := time.Now()
	wait(time.Hour)
	if time.Since(start) > time.Second {
		t.Error("wait blocked with no services registered")
	}
	// Only growth is a leak; unrelated goroutines from earlier tests may exit.
	after := runtime.NumGoroutine()
	for i := 0; i < 20 && after > before; i++ {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before {
		t.Errorf("goroutines %d -> %d with no services", before, after)
	}
}
