package server

import (
	"errors"
	"net/http"
	"time"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/render"
)

// busyRetryAfter is the Retry-After (seconds) of a 503 answered because the
// connection pool stayed busy for the public acquire bound.
const busyRetryAfter = "2"

// publicAcquireBound marks public page requests so that a database connection
// acquire gives up after d with database.ErrPoolBusy instead of queueing
// behind a starved pool. d <= 0 disables the bound. Admin, API and background
// work never pass through it.
func publicAcquireBound(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(database.WithAcquireBound(r.Context(), d)))
		})
	}
}

// writeBusy answers 503 with Retry-After and the themed error page (marked
// private by writeHTML, so never shared-cached). The page
// is built without any database access (no buildView), since the pool is what
// is busy.
func (h *renderHandlers) writeBusy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", busyRetryAfter)
	h.writeError(w, r, http.StatusServiceUnavailable, &render.PageView{
		BaseURL: determineBaseURL(r),
		Path:    requestPath(r),
	})
}

// logLoadFailure logs a failed public load; a busy pool is expected while the
// site warms up, so it is not an error.
func (s *Server) logLoadFailure(msg string, err error, attrs ...any) {
	attrs = append([]any{"error", err}, attrs...)
	if errors.Is(err, database.ErrPoolBusy) {
		s.logger.Debug(msg+": pool busy", attrs...)
		return
	}
	s.logger.Error(msg, attrs...)
}
