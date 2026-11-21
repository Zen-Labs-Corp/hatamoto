package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
	go func() { wait(); close(done) }()
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
