package config

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/runenv"
)

func TestLoadRunMode(t *testing.T) {
	for env, want := range map[string]runenv.Mode{
		"":         runenv.ModeProduction,
		"staging":  runenv.ModeStaging,
		"Staging ": runenv.ModeStaging,
	} {
		t.Setenv("HATAMOTO_ENV", env)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", env, err)
		}
		if cfg.RunMode != want {
			t.Errorf("%q: RunMode = %q; want %q", env, cfg.RunMode, want)
		}
	}
}

func TestLoadRejectsUnknownRunMode(t *testing.T) {
	t.Setenv("HATAMOTO_ENV", "stagin")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "HATAMOTO_ENV") {
		t.Fatalf("err = %v; want an error naming HATAMOTO_ENV", err)
	}
}
