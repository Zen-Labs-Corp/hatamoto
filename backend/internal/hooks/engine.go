// Package hooks is the host-side engine behind the SDK's typed actions and
// filters (see pkg/sdk/hooks.go).
//
// Actions are fire-and-forget: every callback runs in its own goroutine with
// panic recovery and a timeout derived from the server lifecycle context
// (the analytics dispatchListeners model). Filters run synchronously in
// ascending priority order; a failing filter leaves the value unchanged.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"hatamoto.dev/sdk"
)

// DefaultActionTimeout bounds each action callback.
const DefaultActionTimeout = 10 * time.Second

type actionFn func(ctx context.Context, payload any)
type filterFn func(ctx context.Context, v any) (any, error)

type actionEntry struct {
	plugin string
	prio   int
	seq    uint64
	fn     actionFn
}

type filterEntry struct {
	plugin string
	prio   int
	seq    uint64
	fn     filterFn
}

// Engine stores hook registrations and dispatches them.
type Engine struct {
	log *slog.Logger

	mu      sync.RWMutex
	actions map[string][]actionEntry
	filters map[string][]filterEntry
	seq     uint64
	timeout time.Duration
	lifeCtx context.Context

	wg sync.WaitGroup
}

// Registrar is the untyped hook registration surface. Lower priority runs
// first; ties run in registration order.
type Registrar interface {
	AddAction(name string, priority int, fn func(ctx context.Context, payload any))
	AddFilter(name string, priority int, fn func(ctx context.Context, v any) (any, error))
}

var _ Registrar = (*Engine)(nil)

// OnAction registers fn for action a. A payload of an unexpected type is
// skipped.
func OnAction[T any](h Registrar, a sdk.Action[T], priority int, fn func(context.Context, T)) {
	h.AddAction(a.Name, priority, func(ctx context.Context, payload any) {
		v, ok := payload.(T)
		if !ok {
			return
		}
		fn(ctx, v)
	})
}

// OnFilter registers fn for filter f. A value of an unexpected type is passed
// through unchanged.
func OnFilter[T any](h Registrar, f sdk.Filter[T], priority int, fn func(context.Context, T) (T, error)) {
	h.AddFilter(f.Name, priority, func(ctx context.Context, v any) (any, error) {
		t, ok := v.(T)
		if !ok {
			return v, nil
		}
		return fn(ctx, t)
	})
}

// New returns an empty engine. A nil logger logs to slog.Default().
func New(l *slog.Logger) *Engine {
	if l == nil {
		l = slog.Default()
	}
	return &Engine{
		log:     l,
		actions: map[string][]actionEntry{},
		filters: map[string][]filterEntry{},
		timeout: DefaultActionTimeout,
	}
}

// SetLifecycleContext sets the parent context of action callbacks; cancelling
// it (server shutdown) cancels in-flight callbacks.
func (e *Engine) SetLifecycleContext(ctx context.Context) {
	e.mu.Lock()
	e.lifeCtx = ctx
	e.mu.Unlock()
}

// SetActionTimeout overrides the per-callback action timeout.
func (e *Engine) SetActionTimeout(d time.Duration) {
	e.mu.Lock()
	e.timeout = d
	e.mu.Unlock()
}

// AddAction implements Registrar for host-owned (untagged) registrations.
func (e *Engine) AddAction(name string, priority int, fn func(ctx context.Context, payload any)) {
	e.addAction("", name, priority, fn)
}

// AddFilter implements Registrar for host-owned (untagged) registrations.
func (e *Engine) AddFilter(name string, priority int, fn func(ctx context.Context, v any) (any, error)) {
	e.addFilter("", name, priority, fn)
}

// For returns a registrar that tags registrations with the plugin name so
// log lines identify the offender.
func (e *Engine) For(plugin string) Registrar { return pluginHooks{e: e, plugin: plugin} }

type pluginHooks struct {
	e      *Engine
	plugin string
}

func (p pluginHooks) AddAction(name string, priority int, fn func(context.Context, any)) {
	p.e.addAction(p.plugin, name, priority, fn)
}

func (p pluginHooks) AddFilter(name string, priority int, fn func(context.Context, any) (any, error)) {
	p.e.addFilter(p.plugin, name, priority, fn)
}

func (e *Engine) addAction(plugin, name string, prio int, fn actionFn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	list := append(e.actions[name], actionEntry{plugin, prio, e.seq, fn})
	sort.SliceStable(list, func(i, j int) bool { return list[i].prio < list[j].prio })
	e.actions[name] = list
}

func (e *Engine) addFilter(plugin, name string, prio int, fn filterFn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	list := append(e.filters[name], filterEntry{plugin, prio, e.seq, fn})
	sort.SliceStable(list, func(i, j int) bool { return list[i].prio < list[j].prio })
	e.filters[name] = list
}

// RemovePlugin drops every action and filter registered under the plugin
// name. The host calls it when a plugin's Wire fails so the half-wired plugin
// does not keep running callbacks.
func (e *Engine) RemovePlugin(plugin string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for name, list := range e.actions {
		e.actions[name] = slices.DeleteFunc(list, func(a actionEntry) bool { return a.plugin == plugin })
	}
	for name, list := range e.filters {
		e.filters[name] = slices.DeleteFunc(list, func(f filterEntry) bool { return f.plugin == plugin })
	}
}

// HasAction reports whether any callback is registered for the action.
func (e *Engine) HasAction(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.actions[name]) > 0
}

// Fire starts every callback registered for name, in ascending priority, each
// in its own goroutine, and returns immediately. Callbacks do not inherit the
// caller's cancellation: they run under the lifecycle context with a timeout,
// but see the caller's context values.
func (e *Engine) Fire(caller context.Context, name string, payload any) {
	e.mu.RLock()
	list := append([]actionEntry(nil), e.actions[name]...)
	parent := e.lifeCtx
	timeout := e.timeout
	e.mu.RUnlock()
	if parent == nil {
		parent = context.Background()
	}
	if caller != nil {
		parent = valuesOf{Context: parent, vals: caller}
	}
	for _, ent := range list {
		e.wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					e.log.Error("hooks: action panicked",
						slog.String("plugin", ent.plugin), slog.String("hook", name), slog.Any("panic", r))
				}
			}()
			ctx, cancel := context.WithTimeout(parent, timeout)
			defer cancel()
			ent.fn(ctx, payload)
			if ctx.Err() == context.DeadlineExceeded {
				e.log.Warn("hooks: action exceeded timeout",
					slog.String("plugin", ent.plugin), slog.String("hook", name), slog.Duration("timeout", timeout))
			}
		})
	}
}

// valuesOf is a context with the lifecycle parent's deadline and
// cancellation and the caller's values: an action runs on its own lifecycle
// but still knows what request-scoped state (such as the plugin write depth)
// triggered it.
type valuesOf struct {
	context.Context
	vals context.Context
}

func (v valuesOf) Value(key any) any {
	if x := v.vals.Value(key); x != nil {
		return x
	}
	return v.Context.Value(key)
}

// HasFilter reports whether any callback is registered for the filter.
func (e *Engine) HasFilter(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.filters[name]) > 0
}

// Apply runs the filter chain for name over v and returns the result. A
// filter that errors, panics or changes the value's type is logged and
// skipped; the value stays as it was before that filter.
func (e *Engine) Apply(ctx context.Context, name string, v any) any {
	out, _ := e.apply(ctx, name, v, false)
	return out
}

// ApplyStrict is Apply, except a *sdk.Reject RETURNED by a filter (matched
// with errors.As) stops the chain and is returned to the caller (used by
// entity.before_save to reject a save). Any other error, and panics, are
// logged and skipped like in Apply: the filter's change is dropped.
func (e *Engine) ApplyStrict(ctx context.Context, name string, v any) (any, error) {
	return e.apply(ctx, name, v, true)
}

func (e *Engine) apply(ctx context.Context, name string, v any, strict bool) (any, error) {
	e.mu.RLock()
	list := append([]filterEntry(nil), e.filters[name]...)
	e.mu.RUnlock()
	for i, ent := range list {
		next, err, panicked := e.callFilter(ctx, ent, v, len(list)-i)
		switch {
		case panicked:
			e.log.Error("hooks: filter panicked",
				slog.String("plugin", ent.plugin), slog.String("hook", name), slog.Any("panic", err))
			continue
		case err != nil:
			var rej *sdk.Reject
			if strict && errors.As(err, &rej) {
				return v, err
			}
			e.log.Error("hooks: filter failed",
				slog.String("plugin", ent.plugin), slog.String("hook", name), slog.Any("error", err))
			continue
		}
		if v != nil && reflect.TypeOf(next) != reflect.TypeOf(v) {
			e.log.Error("hooks: filter returned wrong payload type; ignored",
				slog.String("plugin", ent.plugin), slog.String("hook", name),
				slog.String("want", fmt.Sprintf("%T", v)), slog.String("got", fmt.Sprintf("%T", next)))
			continue
		}
		v = next
	}
	return v, nil
}

// callFilter runs one filter. When ctx has a deadline, the filter gets only
// its fair share of what is left (remaining time / filters still to run, this
// one included), so one that spins until its deadline cannot starve the ones
// after it; time a filter does not use rolls over to the rest.
func (e *Engine) callFilter(ctx context.Context, ent filterEntry, v any, remaining int) (out any, err error, panicked bool) {
	if dl, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Until(dl)/time.Duration(remaining))
		defer cancel()
	}
	defer func() {
		if r := recover(); r != nil {
			out, err, panicked = nil, fmt.Errorf("%v", r), true
		}
	}()
	out, err = ent.fn(ctx, v)
	return out, err, false
}

// Wait blocks until every in-flight action callback has finished. Used by
// tests and graceful shutdown.
func (e *Engine) Wait() { e.wg.Wait() }
