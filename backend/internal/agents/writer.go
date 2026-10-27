package agents

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	queueSize     = 2000
	flushInterval = time.Second
	flushBatch    = 100
	insertTimeout = 5 * time.Second
	resolveTimout = 2 * time.Second
	drainTimeout  = 5 * time.Second // default Writer.drainTimeout
	warnEvery     = 30 * time.Second
)

// Store persists classified hits (hits table + daily rollup).
type Store interface {
	InsertHits(ctx context.Context, hits []Hit) error
}

// Recorder is the write side of the Writer, so handlers can be tested without one.
type Recorder interface {
	// Record reports false only when the hit was refused (queue full or
	// writer closed). Hits that are intentionally ignored (unknown agent)
	// report true: there is nothing to retry.
	Record(RawHit) bool
}

// WriterStats are process-lifetime counters.
type WriterStats struct {
	Queued  int
	Dropped uint64
	Written uint64
	Failed  uint64
}

type queued struct {
	hit     Hit
	resolve bool
	hint    int // 3xx fallback when the resolver says 404 (or is absent)
}

// Writer persists agent hits off the request path. Record never blocks: a full
// queue drops the hit and counts it. The data channel is never closed, so a
// Record racing Close cannot panic; at worst one hit is lost.
type Writer struct {
	store   Store
	reg     *Registry
	resolve ResolveFunc
	logger  *slog.Logger

	ch     chan queued
	stop   chan struct{}
	done   chan struct{}
	closed atomic.Bool

	dropped, written, failed atomic.Uint64
	lastWarnNanos            atomic.Int64
	lastWarnDropped          atomic.Uint64

	observer atomic.Pointer[func([]Hit)]
	closeMu  sync.Once

	drainTimeout time.Duration // bounds the whole shutdown drain, flushes included
}

// NewWriter starts the background flush goroutine. resolve may be nil (page
// hits then keep status 0 and no entity).
func NewWriter(store Store, reg *Registry, resolve ResolveFunc, logger *slog.Logger) *Writer {
	w := &Writer{
		store:   store,
		reg:     reg,
		resolve: resolve,
		logger:  logger,
		ch:      make(chan queued, queueSize),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),

		drainTimeout: drainTimeout,
	}
	go w.run()
	return w
}

// SetObserver installs a callback invoked from the flush goroutine with each
// successfully stored batch (nil removes it). It must not block.
func (w *Writer) SetObserver(fn func([]Hit)) {
	if fn == nil {
		w.observer.Store(nil)
		return
	}
	w.observer.Store(&fn)
}

// Stats returns the current counters.
func (w *Writer) Stats() WriterStats {
	return WriterStats{
		Queued:  len(w.ch),
		Dropped: w.dropped.Load(),
		Written: w.written.Load(),
		Failed:  w.failed.Load(),
	}
}

// Record classifies and enqueues a hit. Unknown user agents are ignored.
// Never blocks, never panics, safe after Close. It returns false when the hit
// was refused (writer closed or queue full), true otherwise.
func (w *Writer) Record(h RawHit) bool {
	if w.closed.Load() {
		return false
	}
	agent := w.reg.Match(h.UA)
	if agent == nil {
		return true
	}
	at := h.At
	if at.IsZero() {
		at = time.Now()
	}
	method := cleanText(h.Method, maxMethod)
	hit := Hit{
		At:           at.UTC(),
		AgentID:      agent.ID,
		Operator:     agent.Operator,
		Purpose:      agent.Purpose,
		Verification: VerificationPending,
		IP:           parseIP(h.IP),
		Method:       method,
		Path:         cleanPath(h.Path),
		Status:       h.Status,
		Bytes:        int64(h.Bytes),
		DurationMS:   h.DurationMS,
		Source:       h.Source,
	}
	it := queued{hit: hit}
	switch {
	case h.Status != 0:
		// explicit status (robots.txt route, log ingest): trust it, skip the resolver
	case h.Source == SourceMiddleware:
		it.resolve = w.resolve != nil
		it.hint = h.RedirectHint
		if !it.resolve {
			it.hit.Status = it.hint
		}
	default:
		it.hit.Status = 200 // backend handler that never wrote a status
	}
	select {
	case w.ch <- it:
		return true
	default:
		w.noteDrop()
		return false
	}
}

func (w *Writer) noteDrop() {
	n := w.dropped.Add(1)
	now := time.Now().UnixNano()
	last := w.lastWarnNanos.Load()
	if now-last < int64(warnEvery) || !w.lastWarnNanos.CompareAndSwap(last, now) {
		return
	}
	prev := w.lastWarnDropped.Swap(n)
	w.logger.Warn("agent hit queue full, dropping hits",
		slog.Uint64("dropped_since_last_warning", n-prev),
		slog.Uint64("dropped_total", n),
	)
}

// Close stops accepting hits, flushes what is buffered (bounded) and waits for
// the goroutine. Idempotent.
func (w *Writer) Close() {
	w.closeMu.Do(func() {
		w.closed.Store(true)
		close(w.stop)
	})
	<-w.done
}

func (w *Writer) run() {
	defer close(w.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	batch := make([]queued, 0, flushBatch)
	ctx := context.Background()
	flush := func() {
		if len(batch) > 0 {
			w.flush(ctx, batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case it := <-w.ch:
			batch = append(batch, it)
			if len(batch) >= flushBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-w.stop:
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.Background(), w.drainTimeout)
			defer cancel()
			for {
				select {
				case it := <-w.ch:
					batch = append(batch, it)
					if len(batch) >= flushBatch {
						flush()
					}
				case <-ctx.Done():
					flush()
					return
				default:
					flush()
					return
				}
			}
		}
	}
}

func (w *Writer) flush(parent context.Context, batch []queued) {
	hits := make([]Hit, len(batch))
	for i, it := range batch {
		h := it.hit
		if it.resolve {
			ctx, cancel := context.WithTimeout(parent, resolveTimout)
			h.EntityID, h.Status = w.resolve(ctx, h.Path)
			cancel()
			if h.Status == 404 && it.hint != 0 {
				h.Status = it.hint
			}
		}
		hits[i] = h
	}
	w.store1(parent, hits)
}

// rowSpecific reports whether err was caused by the data (SQLSTATE class 22
// data exception or 23 integrity violation), so retrying smaller batches can
// isolate the offending row. Anything else (timeouts, pool exhaustion, DB
// down) affects every row and bisecting only amplifies the load.
func rowSpecific(err error) bool {
	class := sqlStateClass(err)
	return class == "22" || class == "23"
}

// sqlStateClass returns the two-character SQLSTATE class of err, or "".
func sqlStateClass(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || len(pgErr.Code) < 2 {
		return ""
	}
	return pgErr.Code[:2]
}

// isTransactionRollback reports SQLSTATE class 40 (40P01 deadlock, 40001
// serialization failure): the transaction was aborted and is safe to rerun.
func isTransactionRollback(err error) bool { return sqlStateClass(err) == "40" }

// store1 inserts hits. A row-specific failure bisects the batch so one bad row
// cannot take the rest down; a transaction rollback (class 40) is retried once; any other failure
// fails the whole batch.
// parent bounds the work (the shutdown drain deadline).
func (w *Writer) store1(parent context.Context, hits []Hit) {
	ctx, cancel := context.WithTimeout(parent, insertTimeout)
	err := w.store.InsertHits(ctx, hits)
	cancel()
	if err != nil && isTransactionRollback(err) {
		// Deadlock/serialization abort: nothing was committed, rerun the whole batch once.
		ctx, cancel = context.WithTimeout(parent, insertTimeout)
		err = w.store.InsertHits(ctx, hits)
		cancel()
	}
	if err == nil {
		w.written.Add(uint64(len(hits)))
		if obs := w.observer.Load(); obs != nil {
			(*obs)(hits)
		}
		return
	}
	if len(hits) == 1 || !rowSpecific(err) {
		w.failed.Add(uint64(len(hits)))
		w.logger.Error("failed to store agent hits",
			slog.Int("count", len(hits)),
			slog.String("first_agent", hits[0].AgentID),
			slog.String("error", err.Error()))
		return
	}
	mid := len(hits) / 2
	w.store1(parent, hits[:mid])
	w.store1(parent, hits[mid:])
}
