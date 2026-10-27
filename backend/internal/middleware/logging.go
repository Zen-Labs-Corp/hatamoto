package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	size        int
}

func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w, status: http.StatusOK}
}

// Unwrap lets http.ResponseController reach the real writer, so streaming
// handlers (MCP's server-sent events) can flush through the logger.
func (rw *responseWriter) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

// RequestLogger logs HTTP requests with structured logging. redact maps the
// request path to the string that is logged (nil logs it unchanged); it runs
// before the handler so a prefix swapped mid-request is still the one redacted.
func RequestLogger(logger *slog.Logger, redact func(path string) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			path := redactPath(redact, r.URL.Path)

			wrapped := wrapResponseWriter(w)
			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)

			// Log request
			logger.Info("http request",
				slog.String("method", r.Method),
				slog.String("path", path),
				slog.String("remote_addr", r.RemoteAddr),
				slog.Int("status", wrapped.status),
				slog.Int("size", wrapped.size),
				slog.Duration("duration", duration),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}

// redactPath applies redact to p; a nil redact leaves p unchanged.
func redactPath(redact func(string) string, p string) string {
	if redact == nil {
		return p
	}
	return redact(p)
}

// RecoveryLogger recovers from panics and logs them. redact works as in
// RequestLogger.
func RecoveryLogger(logger *slog.Logger, redact func(path string) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := redactPath(redact, r.URL.Path)
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered",
						slog.Any("error", err),
						slog.String("method", r.Method),
						slog.String("path", path),
					)
					http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
