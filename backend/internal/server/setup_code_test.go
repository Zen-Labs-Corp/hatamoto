package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/user"
)

type countingRepo struct {
	user.Repository
	n int
}

func (c countingRepo) Count(context.Context) (int, error) { return c.n, nil }

func setupCodeServer(users int, code string) (*Server, *bytes.Buffer) {
	var log bytes.Buffer
	return &Server{
		cfg:      &config.Config{Admin: config.AdminConfig{SetupCode: code}},
		userRepo: countingRepo{n: users},
		logger:   slog.New(slog.NewTextHandler(&log, nil)),
	}, &log
}

// An upgraded install (it has users) whose env file still holds a short code
// must start: the code is unused, so it only gets a warning.
func TestSetupCodeUpgradedInstallWithShortCodeStarts(t *testing.T) {
	s, log := setupCodeServer(1, "short-8ch")
	code, err := s.setupCode()
	if err != nil || code != nil {
		t.Fatalf("code=%v err=%v, want no code and no error", code, err)
	}
	if !strings.Contains(log.String(), "level=WARN") || !strings.Contains(log.String(), "at least 12 characters") {
		t.Errorf("no warning logged: %s", log.String())
	}
}

// A fresh install is refused with the reason.
func TestSetupCodeFreshInstallWithShortCodeIsRefused(t *testing.T) {
	s, _ := setupCodeServer(0, "short-8ch")
	if _, err := s.setupCode(); err == nil || !strings.Contains(err.Error(), "HATAMOTO_SETUP_CODE must be at least 12 characters") {
		t.Fatalf("err = %v", err)
	}
	s, _ = setupCodeServer(0, "MyLaunch-Code-26")
	if code, err := s.setupCode(); err != nil || code == nil || !code.Verify("mylaunchcode26") {
		t.Fatalf("a good pinned code: code=%v err=%v", code, err)
	}
}
