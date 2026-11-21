// Package cdnpurge tells an edge cache which public URLs changed. Producers
// call Notify (never blocks); one runner debounces, resolves items to
// absolute URLs and sends them in batches to every target, retrying a failed
// batch a few times. A failure is logged and never reaches the writer.
package cdnpurge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	queueCap        = 1024
	defaultDebounce = 2 * time.Second
	defaultMaxItems = 100
	// BatchCap is the most URLs one Request carries.
	BatchCap   = 500
	finalFlush = 5 * time.Second
	warnEvery  = time.Minute
)

// Item is one thing that changed. Only the Resolver interprets Kind, ID,
// Entity and IDs.
type Item struct {
	Kind   string
	ID     uuid.UUID
	Entity any
	IDs    []uuid.UUID
	Paths  []string // site paths ("/a") the resolver turns absolute
	All    bool     // purge everything
}

// Request is what a Target receives.
type Request struct {
	URLs []string
	All  bool
}

// Resolver is implemented by the server.
type Resolver interface {
	// URLs returns absolute URLs for it; may return none.
	URLs(ctx context.Context, it Item) []string
}

// Target receives purge requests.
type Target interface {
	Name() string
	// Send delivers r. A Retryable error is retried.
	Send(ctx context.Context, r Request) error
}

// Options tunes a Runner; the zero value is production.
type Options struct {
	Debounce   time.Duration // quiet period before a flush (default 2s)
	MaxPending int           // flush at this many queued items (default 100)
	// Backoff lists the waits between attempts (default 2s, 10s: three
	// attempts in total). Tests pass {0, 0}.
	Backoff []time.Duration
	// Sleep waits d or until ctx ends; test seam.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Runner is the debounced purger.
type Runner struct {
	res     Resolver
	targets []Target
	log     *slog.Logger
	opt     Options
	ch      chan Item
	mu      sync.Mutex // guards pend and flush
	pend    []Item

	started  atomic.Bool
	done     chan struct{}
	lastWarn atomic.Int64
}

// New builds a Runner. log may be nil.
func New(res Resolver, targets []Target, log *slog.Logger, opt Options) *Runner {
	if log == nil {
		log = slog.Default()
	}
	if opt.Debounce <= 0 {
		opt.Debounce = defaultDebounce
	}
	if opt.MaxPending <= 0 {
		opt.MaxPending = defaultMaxItems
	}
	if opt.Backoff == nil {
		opt.Backoff = []time.Duration{2 * time.Second, 10 * time.Second}
	}
	if opt.Sleep == nil {
		opt.Sleep = sleep
	}
	return &Runner{res: res, targets: targets, log: log, opt: opt, ch: make(chan Item, queueCap), done: make(chan struct{})}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Notify queues it without blocking or I/O. A full queue drops it and warns
// (rate limited).
func (r *Runner) Notify(it Item) {
	select {
	case r.ch <- it:
	default:
		now := time.Now().UnixNano()
		last := r.lastWarn.Load()
		if now-last > int64(warnEvery) && r.lastWarn.CompareAndSwap(last, now) {
			r.log.Warn("cdnpurge: queue full, dropping purges")
		}
	}
}

// Run processes the queue until ctx ends, then flushes once more.
func (r *Runner) Run(ctx context.Context) {
	r.started.Store(true)
	defer close(r.done)
	timer := time.NewTimer(r.opt.Debounce)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalFlush)
			r.Flush(fctx)
			cancel()
			return
		case it := <-r.ch:
			n := r.add(it)
			if n >= r.opt.MaxPending {
				timer.Stop()
				r.Flush(ctx)
				continue
			}
			timer.Reset(r.opt.Debounce)
		case <-timer.C:
			r.Flush(ctx)
		}
	}
}

// Wait blocks until Run has returned (or ctx ends). It returns at once when
// Run was never started.
func (r *Runner) Wait(ctx context.Context) {
	if !r.started.Load() {
		return
	}
	select {
	case <-r.done:
	case <-ctx.Done():
	}
}

func (r *Runner) add(it Item) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pend = append(r.pend, it)
	return len(r.pend)
}

// Flush resolves and sends everything queued so far, synchronously.
func (r *Runner) Flush(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for drained := false; !drained; {
		select {
		case it := <-r.ch:
			r.pend = append(r.pend, it)
		default:
			drained = true
		}
	}
	items := r.pend
	r.pend = nil
	if len(items) == 0 {
		return
	}
	var urls []string
	all := false
	for _, it := range items {
		all = all || it.All
		urls = append(urls, r.res.URLs(ctx, it)...)
	}
	slices.Sort(urls)
	urls = slices.Compact(urls)
	for _, t := range r.targets {
		for rest := urls; len(rest) > 0; {
			n := min(len(rest), BatchCap)
			r.send(ctx, t, Request{URLs: slices.Clone(rest[:n])})
			rest = rest[n:]
		}
		if all {
			r.send(ctx, t, Request{All: true, URLs: []string{}})
		}
	}
}

// send delivers req to t, retrying a Retryable error. It never returns an
// error: a failure is logged.
func (r *Runner) send(ctx context.Context, t Target, req Request) {
	attempts := len(r.opt.Backoff) + 1
	var err error
	n := 0
	for n < attempts {
		n++
		if err = r.try(ctx, t, req); err == nil {
			r.log.Info("cdnpurge: sent", "target", t.Name(), "urls", len(req.URLs), "all", req.All)
			return
		}
		var re Retryable
		if !errors.As(err, &re) || n == attempts {
			break
		}
		if r.opt.Sleep(ctx, r.opt.Backoff[n-1]) != nil {
			break
		}
	}
	r.log.Warn("cdnpurge: send failed", "target", t.Name(), "urls", len(req.URLs), "all", req.All, "attempts", n, "error", err)
}

// try calls t.Send once and turns a panic into an error.
func (r *Runner) try(ctx context.Context, t Target, req Request) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("cdnpurge: target panicked", "target", t.Name(), "panic", fmt.Sprint(p))
			err = fmt.Errorf("target panicked: %v", p)
		}
	}()
	return t.Send(ctx, req)
}
