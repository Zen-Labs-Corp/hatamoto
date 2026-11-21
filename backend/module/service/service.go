package service

import (
	"context"

	"hatamoto.dev/core/internal/modhost"
)

// Register adds a background service for module. `hatamoto serve` starts run
// in its own goroutine once the site is up and cancels ctx at shutdown; a
// panic is recovered and logged, an error is logged, and run is not
// restarted. It never runs in CLI commands, and is skipped when the module's
// migrations failed. Call it from an init function: it panics after
// app.Main has started, on an invalid or duplicate module name and on a nil
// run.
func Register(module string, run func(ctx context.Context) error) {
	modhost.RegisterService(modhost.Service{Name: module, Run: run})
}
