package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"hatamoto.dev/core/internal/modhost"
)

// startModuleServices runs each registered module service in its own
// goroutine. A panic is recovered and logged, a non-nil error other than
// context cancellation is logged, and a service of a module whose
// migrations failed is skipped. Nothing restarts. The returned func waits
// for every started service; cancel ctx first.
func startModuleServices(ctx context.Context, logger *slog.Logger) (wait func()) {
	var wg sync.WaitGroup
	for _, s := range modhost.Services() {
		if err := modhost.MigrationErr(s.Name); err != nil {
			logger.Error("module service not started: its migrations failed", "module", s.Name, "err", err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
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
	return wg.Wait
}
