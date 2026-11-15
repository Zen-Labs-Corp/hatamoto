package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/idle"
)

func TestActivityMiddlewareProbesDoNotWakeIdleGate(t *testing.T) {
	now := time.Unix(1_000, 0)
	s := &Server{idle: idle.New(time.Minute, 5*time.Minute, func() time.Time { return now })}
	h := s.activityMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	now = now.Add(2 * time.Minute)
	if !s.idle.Idle() {
		t.Fatal("gate should be idle after 2m without a request")
	}
	for _, p := range []string{"/health", "/ready"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
		if !s.idle.Idle() {
			t.Fatalf("probe %s counted as activity", p)
		}
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if s.idle.Idle() {
		t.Fatal("a normal request must end the idle spell")
	}
}

func TestNewIdleGateUsesPoolIdleTime(t *testing.T) {
	cfg := &config.Config{}
	cfg.Database.MaxConnIdleTime = 2 * time.Minute
	if g := newIdleGate(cfg); g == nil || g.Idle() {
		t.Fatal("gate must exist and start active")
	}
	cfg.Database.MaxConnIdleTime = 0
	if newIdleGate(cfg).Idle() {
		t.Fatal("a zero idle time disables idling")
	}
}
