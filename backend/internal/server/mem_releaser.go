package server

import (
	"context"
	"net/http"
	"runtime/debug"
	"time"

	"hatamoto.dev/core/internal/memrelease"
)

// activityMiddleware stamps the request time before the handler runs.
func (s *Server) activityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastReq.Store(time.Now().UnixNano())
		next.ServeHTTP(w, r)
	})
}

// startMemReleaser gives heap memory back to the OS once the server has had
// no request for a while. It stops with ctx.
func (s *Server) startMemReleaser(ctx context.Context) {
	go memrelease.Run(ctx, memrelease.Config{
		LastActivity: func() time.Time { return time.Unix(0, s.lastReq.Load()) },
		Free:         debug.FreeOSMemory,
	})
}
