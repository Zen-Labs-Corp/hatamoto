package server

import (
	"log/slog"

	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/middleware"
)

// newAgentLogHandlers returns the log-ingest handlers, or nil when ingest is
// off (the default) or has no secret. A flag without a secret is a
// misconfiguration worth a startup warning; the route stays unmounted.
func newAgentLogHandlers(cfg config.AgentAnalyticsConfig, rec agents.Recorder, logger *slog.Logger) *agents.LogHandlers {
	if cfg.LogIngestEnabled && cfg.LogIngestSecret == "" {
		logger.Warn("AGENT_LOG_INGEST is on but AGENT_LOG_INGEST_SECRET is empty: agent-logs endpoint disabled")
	}
	if !cfg.LogIngestActive() {
		return nil
	}
	return agents.NewLogHandlers(rec, cfg.LogIngestSecret)
}

// mountAgentLogRoute mounts POST /agent-logs behind its own rate limit. With
// nil handlers nothing is mounted, so the path is a plain 404.
func mountAgentLogRoute(r *httpx.Group, h *agents.LogHandlers) {
	if h == nil {
		return
	}
	limiter := middleware.NewRateLimiter(2, 10)
	r.Group("", limiter.Middleware).HandleFunc("POST /agent-logs", h.HandleIngest)
}
