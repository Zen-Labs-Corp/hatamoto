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

// busyConverter makes a public route that built its answer after a bounded
// acquire gave up (database.PoolBusyHit) answer 503 with Retry-After instead
// of whatever it produced: a degraded 200 (robots.txt without the owner's
// crawler policy, sitemap or feed with fallback URLs) or a generic 500. It
// sits innermost, so the agent-hit capture records the real 503. It does not
// touch an answer that is already a 503.
func busyConverter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bw := &busyWriter{ResponseWriter: w, r: r}
		next.ServeHTTP(bw, r)
		if !bw.decided {
			bw.decide(http.StatusOK, true)
		}
	})
}

type busyWriter struct {
	http.ResponseWriter
	r       *http.Request
	decided bool
	swallow bool
}

// decide runs once, at the first header or body write (or at the end of the
// request). implicit is true when the handler did not call WriteHeader, so the
// status is left to the underlying writer on a pass-through.
func (b *busyWriter) decide(code int, implicit bool) {
	b.decided = true
	if code == http.StatusServiceUnavailable || !database.PoolBusyHit(b.r.Context()) {
		if !implicit {
			b.ResponseWriter.WriteHeader(code)
		}
		return
	}
	b.swallow = true
	h := b.Header()
	for _, k := range []string{"Etag", "Last-Modified", "Content-Encoding", "Content-Length", "Content-Language"} {
		h.Del(k)
	}
	h.Set("Retry-After", busyRetryAfter)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	b.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
	_, _ = b.ResponseWriter.Write([]byte("service temporarily unavailable\n"))
}

func (b *busyWriter) WriteHeader(code int) {
	if !b.decided {
		b.decide(code, false)
	}
}

func (b *busyWriter) Write(p []byte) (int, error) {
	if !b.decided {
		b.decide(http.StatusOK, true)
	}
	if b.swallow {
		return len(p), nil
	}
	return b.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (b *busyWriter) Unwrap() http.ResponseWriter { return b.ResponseWriter }

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
