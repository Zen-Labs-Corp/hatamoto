package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgUndefinedTable is the SQLSTATE of a query on a table that does not exist.
const pgUndefinedTable = "42P01"

// errEpochUnavailable is returned by a contentEpochPoller's read when the epoch
// can never be read (the migration is not applied). The poller then stops
// instead of logging every tick.
var errEpochUnavailable = errors.New("content epoch unavailable")

// contentEpochPoller notices content writes made by other processes (the CLI,
// the worker, another replica). Every transaction that changes content appends
// a row to hatamoto_content_changes; changed reports whether a new committed
// row appeared since the last call (the first successful call only seeds and
// reports false). The page cache is per process, so this is the only way a
// write made elsewhere reaches pages already cached here.
//
// Purges are coalesced. The first change after a quiet period purges at once.
// While changes keep arriving, at most one purge per gap runs, and one more
// follows on the first tick with no new change (the trailing purge), so a
// bulk job does not keep the cache cold and the final state is always purged.
// A change made by this process is seen too and purges a second time, up to a
// tick after the hook did.
type contentEpochPoller struct {
	changed  func(ctx context.Context) (bool, error)
	onChange func(ctx context.Context)
	every    time.Duration
	gap      time.Duration
	now      func() time.Time // time.Now when nil
	logger   *slog.Logger
}

// Run polls every interval until ctx ends. A read error never purges and is
// retried on the next tick. If reads failed before the first success, a change
// may have landed unseen in the meantime, so the first success purges once.
func (p *contentEpochPoller) Run(ctx context.Context) {
	now := p.now
	if now == nil {
		now = time.Now
	}
	var lastPurge time.Time
	seeded, failedBeforeSeed, pending, warned := false, false, false, false

	// check returns false when the poller must stop for good.
	check := func() bool {
		changed, err := p.changed(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return true
			}
			if errors.Is(err, errEpochUnavailable) {
				p.logger.Warn("content epoch: table missing, cross-process cache refresh disabled (apply migrations)", "error", err)
				return false
			}
			if !warned {
				p.logger.Warn("content epoch: read failed, will retry", "error", err)
				warned = true
			}
			failedBeforeSeed = failedBeforeSeed || !seeded
			return true
		}
		warned = false
		if !seeded {
			seeded = true
			if failedBeforeSeed {
				lastPurge = now()
				p.onChange(ctx)
			}
			return true
		}
		pending = pending || changed
		if pending && (!changed || now().Sub(lastPurge) >= p.gap) {
			pending = false
			lastPurge = now()
			p.onChange(ctx)
		}
		return true
	}

	if !check() {
		return
	}
	t := time.NewTicker(p.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !check() {
				return
			}
		}
	}
}

// contentEpochEvery is how often the server polls the content change log. A page
// published by the CLI shows in already cached pages within about this long;
// development polls faster, like packageReconcileEvery.
func (s *Server) contentEpochEvery() time.Duration {
	if s.cfg.Environment != "production" {
		return time.Second
	}
	return 3 * time.Second
}

// contentChangeReader tells whether a new committed row appeared in
// hatamoto_content_changes since the previous call.
//
// Transactions commit in any order, so "max id grew" would miss a row that
// commits late with a lower id. Instead each poll asks for every row whose txid
// is at or above the previous poll's snapshot xmin: every transaction below
// that xmin had finished by then, so its row was either seen or does not exist.
// The ids seen at or above the watermark are remembered, a row not in that set
// is new.
type contentChangeReader struct {
	db interface {
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
		Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	}
	logger    *slog.Logger
	seeded    bool
	watermark int64
	seen      map[int64]int64 // id -> txid, for txid >= watermark
	lastPrune time.Time
}

// contentChangeRetention is how long log rows are kept. A transaction that
// stays open longer than this and then commits is not noticed.
const contentChangeRetention = "24 hours"

const contentChangePruneEvery = 10 * time.Minute

// Changed reports whether a new row appeared. The first successful call seeds
// and reports false. A missing table is errEpochUnavailable.
func (r *contentChangeReader) Changed(ctx context.Context) (bool, error) {
	var xmin int64
	var ids, txids []int64
	err := r.db.QueryRow(ctx, `
		SELECT pg_snapshot_xmin(pg_current_snapshot())::text::bigint,
		       coalesce(array_agg(id ORDER BY id), '{}'),
		       coalesce(array_agg(txid ORDER BY id), '{}')
		FROM hatamoto_content_changes WHERE txid >= $1`, r.watermark).Scan(&xmin, &ids, &txids)
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgUndefinedTable {
			return false, fmt.Errorf("%w: %w", errEpochUnavailable, err)
		}
		return false, err
	}
	if r.seen == nil {
		r.seen = map[int64]int64{}
	}
	changed := false
	for i, id := range ids {
		if _, ok := r.seen[id]; !ok {
			r.seen[id] = txids[i]
			changed = true
		}
	}
	for id, txid := range r.seen {
		if txid < xmin {
			delete(r.seen, id)
		}
	}
	r.watermark = xmin
	r.prune(ctx)
	first := !r.seeded
	r.seeded = true
	return changed && !first, nil
}

// prune drops log rows past the retention, now and then. It touches only the
// log table, so it logs nothing itself. Several replicas may prune at once.
func (r *contentChangeReader) prune(ctx context.Context) {
	if time.Since(r.lastPrune) < contentChangePruneEvery {
		return
	}
	r.lastPrune = time.Now()
	if _, err := r.db.Exec(ctx, `DELETE FROM hatamoto_content_changes WHERE at < now() - interval '`+contentChangeRetention+`'`); err != nil && ctx.Err() == nil {
		r.logger.Warn("content epoch: pruning the change log failed", "error", err)
	}
}

// runContentEpochPoller purges the rendered-page cache whenever another
// process (or this one) writes content. It returns when ctx ends.
func (s *Server) runContentEpochPoller(ctx context.Context) {
	reader := &contentChangeReader{db: s.db.Pool(), logger: s.logger}
	every := s.contentEpochEvery()
	p := &contentEpochPoller{
		changed:  reader.Changed,
		onChange: s.invalidateRendered,
		every:    every,
		gap:      5 * every,
		logger:   s.logger,
	}
	p.Run(ctx)
}
