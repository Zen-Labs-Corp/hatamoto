package server

import (
	"net/http"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/idle"
)

// newIdleGate builds the gate that stretches background loops once the install
// has served no request for the pool idle time (DATABASE_MAX_CONN_IDLE_TIME).
// The beat always exceeds that time, so a pooled connection can close between
// beats. It returns nil, which leaves every loop on its normal schedule, unless
// the operator lets the pool drain (DATABASE_MIN_CONNECTIONS=0): with warm
// connections kept anyway, backing off would only delay work.
func newIdleGate(cfg *config.Config) *idle.Gate {
	if cfg.Database.MinConnections != 0 {
		return nil
	}
	after := cfg.Database.MaxConnIdleTime
	return idle.New(after, idle.Beat(after), nil)
}

// isProbe reports whether r is a health or readiness probe, which says nothing
// about whether anyone is using the site.
func isProbe(r *http.Request) bool {
	switch r.URL.Path {
	case "/health", "/ready":
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	return false
}
