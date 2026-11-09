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

// RecoveryOption tunes RecoveryLogger.
type RecoveryOption func(*recoveryOpts)

type recoveryOpts struct{ onPanic func() }

// OnPanic registers f, called once per recovered panic before the 500 is
// written. A nil f is a no-op.
func OnPanic(f func()) RecoveryOption {
	return func(o *recoveryOpts) { o.onPanic = f }
}

// RecoveryLogger recovers from panics and logs them. redact works as in
// RequestLogger.
func RecoveryLogger(logger *slog.Logger, redact func(path string) string, opts ...RecoveryOption) func(http.Handler) http.Handler {
	var o recoveryOpts
	for _, opt := range opts {
		opt(&o)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := redactPath(redact, r.URL.Path)
			defer func() {
				if err := recover(); err != nil {
					if o.onPanic != nil {
						o.onPanic()
					}
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

// CountServerErrors calls onErr once per response whose final status is 500 or
// higher. Place it outside RecoveryLogger so the 500 written for a recovered
// panic is seen too (that request counts as both a panic and a server error).
// A nil onErr makes it a pass-through.
func CountServerErrors(onErr func()) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if onErr == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := wrapResponseWriter(w)
			next.ServeHTTP(wrapped, r)
			if wrapped.status >= http.StatusInternalServerError {
				onErr()
			}
		})
	}
}
