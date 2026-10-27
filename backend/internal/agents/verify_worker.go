package agents

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// pendingMaxAge: a hit still undecided after this long becomes
	// unverifiable, so pending is never terminal (the IP is truncated at 30d).
	// Hits past verifyWindow are retired by SweepStalePending.
	pendingMaxAge     = 24 * time.Hour
	verifyConcurrency = 8
)

// PairVerifier decides one (agent, ip) pair. *Verifier satisfies it.
type PairVerifier interface {
	Verify(ctx context.Context, a *Agent, ip netip.Addr) State
}

// RangeRefresher downloads operator ranges. *Refresher satisfies it.
type RangeRefresher interface {
	Refresh(ctx context.Context) RefreshReport
}

// VerifyWorker flips pending hits to verified/spoofed/unverifiable and keeps
// the operator IP ranges fresh. Network failures are logged and never touch
// stored hits.
type VerifyWorker struct {
	repo      VerifyRepo
	reg       *Registry
	verifier  PairVerifier
	refresher RangeRefresher
	store     *RangeStore
	persist   RangePersister
	logger    *slog.Logger
	now       func() time.Time

	loaded    atomic.Bool // persisted ranges read into the store
	attempted atomic.Bool // a refresh ran to completion this process
}

// NewVerifyWorker wires the worker. store/persist may be nil (no persisted
// ranges, no readiness guard), as in tests with a fake verifier.
func NewVerifyWorker(repo VerifyRepo, reg *Registry, verifier PairVerifier, refresher RangeRefresher,
	store *RangeStore, persist RangePersister, logger *slog.Logger) *VerifyWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &VerifyWorker{repo: repo, reg: reg, verifier: verifier, refresher: refresher,
		store: store, persist: persist, logger: logger, now: time.Now}
}

// NoRangeRefresh tells the worker no range refresh will ever run (the switch
// HATAMOTO_AGENT_RANGES=off, or the job disabled), so waiting for one would
// leave every hit pending. Verification then runs on stored ranges and
// forward-confirmed reverse DNS only.
func (w *VerifyWorker) NoRangeRefresh() {
	if w != nil {
		w.attempted.Store(true)
	}
}

// ready loads persisted ranges once, and reports whether verification may run:
// either some ranges are loaded or a refresh has been attempted. Before that,
// "ranges not loaded" would wrongly turn every hit unverifiable.
func (w *VerifyWorker) ready(ctx context.Context) bool {
	if w.store == nil {
		return true
	}
	if !w.loaded.Load() && w.persist != nil {
		if err := w.store.Load(ctx, w.persist); err != nil {
			w.logger.Warn("agents: load persisted ip ranges failed", slog.Any("error", err))
		} else {
			w.loaded.Store(true)
		}
	}
	return w.store.Size() > 0 || w.attempted.Load()
}

// RunVerify processes one batch of pending (agent, ip) pairs.
func (w *VerifyWorker) RunVerify(ctx context.Context) error {
	if w == nil || w.repo == nil {
		return nil
	}
	// Hits that aged out of the verify window can no longer be decided; retire
	// them first. Independent of the ranges, so it runs even before they load.
	if n, err := w.repo.SweepStalePending(ctx); err != nil {
		if !isTransactionRollback(err) {
			w.logger.Warn("agents: sweep stale pending hits failed", slog.Any("error", err))
		}
	} else if n > 0 {
		w.logger.Info("agents: stale pending hits marked unverifiable", slog.Int64("hits", n))
	}
	if !w.ready(ctx) {
		w.logger.Debug("agents: verify skipped, ip ranges not loaded yet")
		return nil
	}
	pairs, err := w.repo.PendingPairs(ctx, PendingBatch)
	if err != nil {
		return fmt.Errorf("list pending hits: %w", err)
	}
	if len(pairs) == 0 {
		return nil
	}

	var flipped, failed, retry atomic.Int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, verifyConcurrency)
	for _, p := range pairs {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(p PendingPair) {
			defer wg.Done()
			defer func() { <-sem }()
			n, isPending, err := w.verifyPair(ctx, p)
			switch {
			case err != nil:
				failed.Add(1)
				w.logger.Warn("agents: apply verification failed", slog.String("agent", p.AgentID), slog.Any("error", err))
			case isPending:
				retry.Add(1)
			}
			flipped.Add(n)
		}(p)
	}
	wg.Wait()
	w.logger.Info("agents: verified pending hits",
		slog.Int("pairs", len(pairs)), slog.Int64("hits_flipped", flipped.Load()),
		slog.Int64("still_pending", retry.Load()), slog.Int64("errors", failed.Load()))
	return ctx.Err()
}

// verifyPair decides one pair and applies a final state. A pending verdict is
// left alone unless the pair's oldest hit is past pendingMaxAge, in which case
// only hits older than the cutoff become unverifiable. stillPending reports an
// undecided pair.
func (w *VerifyWorker) verifyPair(ctx context.Context, p PendingPair) (flipped int64, stillPending bool, err error) {
	state := StateUnverifiable
	if a := w.reg.ByID(p.AgentID); a != nil {
		state = w.verifier.Verify(ctx, a, p.IP)
	}
	var before time.Time
	if state == StatePending {
		cutoff := w.now().Add(-pendingMaxAge)
		if !p.OldestAt.Before(cutoff) {
			return 0, true, nil
		}
		state, before = StateUnverifiable, cutoff
	}
	flipped, err = w.repo.ApplyVerification(ctx, p, state, before)
	if err != nil && isTransactionRollback(err) {
		// Deadlock/serialization abort: the tx rolled back, the hits are still
		// pending and the next run retries them.
		return 0, true, nil
	}
	return flipped, false, err
}

// RunRefresh downloads the operator IP ranges. It always returns nil: a network
// failure keeps the old ranges, is logged, and must never fail the job or
// touch stored hits.
func (w *VerifyWorker) RunRefresh(ctx context.Context) error {
	if w == nil || w.refresher == nil {
		return nil
	}
	w.ready(ctx) // pull persisted ranges first so a failed fetch has something to keep
	rep := w.refresher.Refresh(ctx)
	for _, f := range rep.Failed {
		w.logger.Warn("agents: ip range refresh failed", slog.String("key", f.Key), slog.Any("error", f.Err))
	}
	w.logger.Info("agents: ip ranges refreshed", slog.Int("refreshed", len(rep.Refreshed)), slog.Int("failed", len(rep.Failed)))
	if ctx.Err() == nil {
		w.attempted.Store(true)
	}
	return nil
}
