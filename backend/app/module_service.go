package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"hatamoto.dev/core/internal/modhost"
)

// moduleServiceStopTimeout bounds how long shutdown waits for module
// services that ignore their context.
const moduleServiceStopTimeout = 10 * time.Second

// startModuleServices runs each registered module service in its own
// goroutine. A panic is recovered and logged, a non-nil error other than
// context cancellation is logged, and a service of a module whose
// migrations failed is skipped. Nothing restarts. The returned func waits
// for every started service, up to the timeout, then logs the names still
// running and returns; cancel ctx first.
func startModuleServices(ctx context.Context, logger *slog.Logger) (wait func(timeout time.Duration)) {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		running = map[string]bool{}
	)
	svcs := modhost.Services()
	if len(svcs) == 0 {
		// Nothing registered: no goroutine, no timer (idle budget).
		return func(time.Duration) {}
	}
	for _, s := range svcs {
		if err := modhost.MigrationErr(s.Name); err != nil {
			logger.Error("module service not started: its migrations failed", "module", s.Name, "err", err)
			continue
		}
		wg.Add(1)
		mu.Lock()
		running[s.Name] = true
		mu.Unlock()
		go func() {
			defer wg.Done()
			defer func() {
				mu.Lock()
				delete(running, s.Name)
				mu.Unlock()
			}()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("module service panicked", "module", s.Name, "panic", fmt.Sprint(r))
				}
			}()
			if err := s.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("module service stopped", "module", s.Name, "err", err)
			}
		}()
	}
	return func(timeout time.Duration) {
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(timeout):
			mu.Lock()
			names := slices.Sorted(maps.Keys(running))
			mu.Unlock()
			logger.Error("module services did not stop in time", "timeout", timeout, "running", names)
		}
	}
}
