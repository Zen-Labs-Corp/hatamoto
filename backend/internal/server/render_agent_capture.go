package server

import (
	"net/http"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
)

// agentCaptureMiddleware records requests from known AI agents on the
// Go-rendered public routes (pages, robots.txt, sitemaps, llms.txt). On
// Go-served installs these never reach the Next.js middleware that normally
// feeds the agent-hit ingest, so the in-process writer is used directly. The
// hit is recorded after the handler ran with its real status, byte count and
// duration. /api and /media paths are not pages and are not recorded.
func (s *Server) agentCaptureMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		middleware.Trace(r, "capture")
		if s.agentWriter == nil || isReservedPath(requestPath(r)) {
			next.ServeHTTP(w, r)
			return
		}
		ww := httpx.NewRecorder(w)
		start := time.Now()
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK // handler never wrote a status
		}
		s.agentWriter.Record(agents.RawHit{
			At:         start,
			UA:         r.UserAgent(),
			IP:         middleware.GetClientIP(r),
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     status,
			Bytes:      ww.BytesWritten(),
			DurationMS: int(time.Since(start).Milliseconds()),
			Source:     agents.SourceBackend,
		})
	})
}
