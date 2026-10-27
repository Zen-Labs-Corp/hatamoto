package app

import (
	"context"
	"log/slog"
	"os"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/supervisor"
)

// superviseConfig is the supervisor's view of this process: the data dir the
// child resolves (made absolute here so it cannot drift), this binary as the
// bootstrap, and the child's /ready on its own port. Timings take the defaults.
func superviseConfig(logger *slog.Logger) (supervisor.Config, error) {
	exe, err := os.Executable()
	if err != nil {
		return supervisor.Config{}, err
	}
	version, _ := buildInfo()
	return supervisor.Config{
		Data:             config.ResolveDataDir(),
		Bootstrap:        exe,
		BootstrapVersion: version,
		ReadyURL:         "http://127.0.0.1:" + getPort() + "/ready",
		Log:              logger,
	}, nil
}

// runSupervise runs the supervisor until SIGTERM/SIGINT; Run owns its signals.
func runSupervise(ctx context.Context, logger *slog.Logger) error {
	cfg, err := superviseConfig(logger)
	if err != nil {
		return err
	}
	return supervisor.Run(ctx, cfg)
}
