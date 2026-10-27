package server

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
)

// renderErrors counts error-level log records whose message starts with
// "render:" AND whose context is a smoke request's (see smokeContext). Visitor
// requests never reach it, so a pass counts exactly what its own requests
// caused. Release-level faults logged outside any request (theme fallback at
// boot) are render.BootFaults instead.
//
// Plugin template-func failures (render.NoteDegraded) carry no request
// context themselves; the handler logs "render: page degraded" with the
// request's, for smoke requests only, when the page came out degraded.
// Plugins that fail to load are caught by the smoke result's plugin_errors.
var renderErrors atomic.Int64

type smokeCtxKey struct{}

// smokeContext marks ctx as belonging to a smoke check request.
func smokeContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, smokeCtxKey{}, true)
}

func isSmokeRequest(ctx context.Context) bool { return ctx.Value(smokeCtxKey{}) != nil }

// RenderErrorCount is the render errors smoke requests have caused so far.
func RenderErrorCount() int64 { return renderErrors.Load() }

// NewCountingHandler wraps inner and counts the records RenderErrorCount reports.
func NewCountingHandler(inner slog.Handler) slog.Handler { return countingHandler{inner} }

type countingHandler struct{ inner slog.Handler }

func (h countingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h countingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError && strings.HasPrefix(r.Message, "render:") && ctx != nil && isSmokeRequest(ctx) {
		renderErrors.Add(1)
	}
	return h.inner.Handle(ctx, r)
}

func (h countingHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return countingHandler{h.inner.WithAttrs(a)}
}
func (h countingHandler) WithGroup(n string) slog.Handler {
	return countingHandler{h.inner.WithGroup(n)}
}
