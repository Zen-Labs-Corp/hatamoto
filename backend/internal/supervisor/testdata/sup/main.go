// Command sup runs supervisor.Run with timings from SUP_* environment
// variables so tests can drive a real supervisor process with real signals.
//
//	SUP_BOOTSTRAP      bootstrap binary (default: this executable)
//	SUP_VERSION        bootstrap version stamp
//	SUP_READY_URL      child /ready URL
//	SUP_STOP_GRACE, SUP_READY_TIMEOUT, SUP_OBSERVE, SUP_SMOKE_POLL,
//	SUP_BOOT_WINDOW, SUP_BACKOFF   durations ("2s", "300ms")
//	SUP_TRUST_KEYS     base64 public keys that staged releases must be signed by
//
// The data dir comes from HATAMOTO_DATA as in production.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/supervisor"
)

func dur(key string) time.Duration {
	d, _ := time.ParseDuration(os.Getenv(key))
	return d
}

func main() {
	boot := os.Getenv("SUP_BOOTSTRAP")
	if boot == "" {
		boot, _ = os.Executable()
	}
	var ro release.Options
	if keys := os.Getenv("SUP_TRUST_KEYS"); keys != "" {
		ts, err := packages.ParseTrustSet(keys)
		if err != nil {
			slog.Error("bad SUP_TRUST_KEYS", "err", err)
			os.Exit(1)
		}
		ro.Release = ts
	}
	err := supervisor.Run(context.Background(), supervisor.Config{
		Data:             config.ResolveDataDir(),
		Bootstrap:        boot,
		BootstrapVersion: os.Getenv("SUP_VERSION"),
		ReadyURL:         os.Getenv("SUP_READY_URL"),
		StopGrace:        dur("SUP_STOP_GRACE"),
		ReadyTimeout:     dur("SUP_READY_TIMEOUT"),
		ObserveWindow:    dur("SUP_OBSERVE"),
		SmokePoll:        dur("SUP_SMOKE_POLL"),
		BootWindow:       dur("SUP_BOOT_WINDOW"),
		BackoffBase:      dur("SUP_BACKOFF"),
		Release:          ro,
		Log:              slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if err != nil {
		slog.Error("supervisor exited with error", "err", err)
		os.Exit(1)
	}
}
