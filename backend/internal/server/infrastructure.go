// Package server provides infrastructure initialization.
package server

import (
	"log/slog"
	"net/http"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/middleware"
)

// Infrastructure holds the in-process cache components.
type Infrastructure struct {
	Cache            *cache.Cache
	CacheInvalidator *cache.CacheInvalidator
}

// NewInfrastructure builds the in-process cache and its invalidator.
func NewInfrastructure(logger *slog.Logger) *Infrastructure {
	c := cache.New(logger)
	return &Infrastructure{Cache: c, CacheInvalidator: cache.NewCacheInvalidator(c, logger)}
}

// Shutdown releases infrastructure components. The in-process cache needs no
// teardown.
func (i *Infrastructure) Shutdown() error { return nil }

// handleFlushCache flushes all cache entries. Post-Path-B the cache is
// singleton, so this is equivalent to a full wipe of our keyspace.
func (s *Server) handleFlushCache(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if s.infra == nil || s.infra.CacheInvalidator == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "cache not enabled"})
		return
	}

	if err := s.infra.CacheInvalidator.InvalidateTenant(r.Context()); err != nil {
		s.logger.Error("failed to flush cache", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to flush cache")
		return
	}

	s.logger.Info("cache flushed manually", "user_id", claims.UserID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
