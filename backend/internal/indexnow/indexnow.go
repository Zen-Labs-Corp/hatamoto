// Package indexnow batches published-URL pings for the IndexNow protocol.
// Producers call Notify (never blocks); a single runner debounces the items,
// asks the Resolver for public URLs and submits them in capped batches.
package indexnow

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	queueCap        = 1024
	defaultDebounce = 10 * time.Second
	defaultMaxItems = 100
	finalFlush      = 5 * time.Second
	warnEvery       = time.Minute
)

// Item is one thing that changed. The package does not interpret it.
type Item struct {
	Kind   string
	ID     uuid.UUID
	Entity any
	IDs    []uuid.UUID
}

// Config is the live IndexNow configuration.
type Config struct {
	Key      string // verification key
	Endpoint string // bare search engine host
	Base     string // public site base URL, e.g. https://example.com
}

// Resolver is implemented by the server.
type Resolver interface {
	// URLs returns the public absolute URLs for it; may return none.
	URLs(ctx context.Context, it Item) []string
	// Config reports the live settings; ok is false when IndexNow is off.
	Config(ctx context.Context) (cfg Config, ok bool)
}

// Options tunes a Runner; the zero value is production.
type Options struct {
	Debounce   time.Duration // quiet period before a flush (default 10s)
	MaxPending int           // flush at this many queued items (default 100)
	Client     *http.Client  // test hook; default is a safefetch client
	// EndpointURL builds the submit URL from the endpoint host; test hook.
	EndpointURL func(host string) string
}

// Runner is the debounced submitter.
type Runner struct {
	res  Resolver
	log  *slog.Logger
	opt  Options
	ch   chan Item
	cli  *http.Client
	mu   sync.Mutex // guards pending and flush
	pend []Item

	started  atomic.Bool
	done     chan struct{}
	lastWarn atomic.Int64
}

// New builds a Runner. log may be nil.
func New(res Resolver, log *slog.Logger, opt Options) *Runner {
	if log == nil {
		log = slog.Default()
	}
	if opt.Debounce <= 0 {
		opt.Debounce = defaultDebounce
	}
	if opt.MaxPending <= 0 {
		opt.MaxPending = defaultMaxItems
	}
	if opt.EndpointURL == nil {
		opt.EndpointURL = func(h string) string { return "https://" + h + "/indexnow" }
	}
	cli := opt.Client
	if cli == nil {
		cli = newClient()
	}
	return &Runner{res: res, log: log, opt: opt, ch: make(chan Item, queueCap), cli: cli, done: make(chan struct{})}
}

// Notify queues it without blocking. A full queue drops it and warns (rate
// limited).
func (r *Runner) Notify(it Item) {
	select {
	case r.ch <- it:
	default:
		now := time.Now().UnixNano()
		last := r.lastWarn.Load()
		if now-last > int64(warnEvery) && r.lastWarn.CompareAndSwap(last, now) {
			r.log.Warn("indexnow: queue full, dropping pings")
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

// Flush resolves and submits everything queued so far, synchronously.
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
	cfg, ok := r.res.Config(ctx)
	if !ok {
		return
	}
	base, err := url.Parse(cfg.Base)
	if err != nil || base.Host == "" || cfg.Key == "" {
		return
	}
	urls := r.collect(ctx, items, base.Host)
	if len(urls) == 0 {
		return
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "api.indexnow.org"
	}
	keyLoc := base.Scheme + "://" + base.Host + "/" + cfg.Key + ".txt"
	for len(urls) > 0 {
		n := min(len(urls), BatchCap)
		batch := urls[:n]
		urls = urls[n:]
		status, err := submit(ctx, r.cli, r.opt.EndpointURL(endpoint), base.Host, cfg.Key, keyLoc, batch)
		if err != nil {
			r.log.Warn("indexnow: submit failed", "urls", len(batch), "endpoint", endpoint, "status", status, "error", err)
			continue
		}
		r.log.Info("indexnow: submitted", "urls", len(batch), "endpoint", endpoint, "status", status)
	}
}

// collect resolves items to a deduplicated URL list on host.
func (r *Runner) collect(ctx context.Context, items []Item, host string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, it := range items {
		for _, u := range r.res.URLs(ctx, it) {
			pu, err := url.Parse(u)
			if err != nil || pu.Host != host {
				continue
			}
			if _, dup := seen[u]; dup {
				continue
			}
			seen[u] = struct{}{}
			out = append(out, u)
		}
	}
	return out
}
