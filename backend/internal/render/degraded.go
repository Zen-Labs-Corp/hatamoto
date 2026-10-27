package render

import (
	"bytes"
	"context"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

// degraded counts render-time plugin failures: a template func or a
// render-time filter that failed, timed out, exceeded its output cap or was
// skipped by a circuit breaker. Template funcs get no per-render context, so
// the signal is process-wide: the page handler samples the counter around a
// render and, if it moved, serves the page but does not cache it. A failure
// in a concurrent render can therefore also keep a healthy page out of the
// cache; that errs on the safe side and only lasts while plugins misbehave.
var degraded atomic.Int64

// NoteDegraded records that a plugin failed during a render. Besides the
// process-wide counter it marks the DegradeScope bound to the calling
// goroutine, if any: template funcs run synchronously on the render's own
// goroutine, so that is the one request whose page lost the output.
func NoteDegraded() {
	degraded.Add(1)
	if scopes.active.Load() == 0 {
		return
	}
	if s, ok := scopes.byGoroutine.Load(goroutineID()); ok {
		s.(*DegradeScope).n.Add(1)
	}
}

// NoteDegradedCtx is NoteDegraded for callers holding the request context: it
// marks the context's DegradeScope, else the goroutine's.
func NoteDegradedCtx(ctx context.Context) {
	if s, ok := ctx.Value(scopeKey{}).(*DegradeScope); ok {
		degraded.Add(1)
		s.n.Add(1)
		return
	}
	NoteDegraded()
}

// DegradeScope counts the plugin render failures of ONE request, immune to
// failures that concurrent visitor renders cause. Bind it (BindDegradeScope)
// on the goroutine that renders the page.
type DegradeScope struct{ n atomic.Int64 }

// Count is the failures this scope's request has seen.
func (s *DegradeScope) Count() int64 { return s.n.Load() }

type scopeKey struct{}

var scopes struct {
	active      atomic.Int64
	byGoroutine sync.Map // goroutine id -> *DegradeScope
}

// BindDegradeScope returns a scope for the calling goroutine's render and a
// context carrying it. Call release when the render is done.
func BindDegradeScope(ctx context.Context) (context.Context, *DegradeScope, func()) {
	s := &DegradeScope{}
	id := goroutineID()
	scopes.byGoroutine.Store(id, s)
	scopes.active.Add(1)
	return context.WithValue(ctx, scopeKey{}, s), s, func() {
		scopes.byGoroutine.Delete(id)
		scopes.active.Add(-1)
	}
}

// goroutineID parses the id out of the stack header ("goroutine 123 [...").
// It only runs on a failure or when a scope is bound, never on the hot path.
func goroutineID() uint64 {
	var b [32]byte
	s := bytes.TrimPrefix(b[:runtime.Stack(b[:], false)], []byte("goroutine "))
	id, _ := strconv.ParseUint(string(s[:bytes.IndexByte(s, ' ')]), 10, 64)
	return id
}

// DegradedCount is the number of plugin render failures so far.
func DegradedCount() int64 { return degraded.Load() }
