package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/database"
)

// DrainMode reports whether the WORKER_DRAIN env var instructs the scheduler
// to skip starting background worker loops on this process. Used during
// destructive DB migrations (e.g. Path B multi-tenancy strip) to prevent
// the worker fleet from racing the DDL by writing to tables mid-ALTER.
//
// Accepted truthy values: "1", "true", "yes", "on" (case-insensitive).
// Anything else (including unset) returns false. Read once at Start();
// to toggle, set the WORKER_DRAIN env var in the deployment's env file and
// restart the API process.
func DrainMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WORKER_DRAIN"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Scheduler manages background job scheduling.
//
// The scheduler is a small core-only host for core maintenance jobs
// (publishing, retention, pruning, agent-hit upkeep). Plugin workloads run
// in the plugin via the plugin.Starter contract, not here.
type Scheduler struct {
	db                         *database.DB
	publishWorker              *PublishWorker
	retentionWorker            *RetentionPurgeWorker
	agentHitsPartitionWorker   *PartitionWorker
	agentHitsMaintenanceWorker *AgentHitsMaintenanceWorker
	agentRangesRefreshWorker   *agents.VerifyWorker
	agentVerifyWorker          *agents.VerifyWorker
	pruneWorker                *PruneWorker
	versionWorker              *EntityVersionWorker
	versionEvery               time.Duration // 0: daily
	logger                     *slog.Logger

	// Tickers for different job types
	publishTicker *time.Ticker

	// Control channels
	stopCh chan struct{}
	wg     sync.WaitGroup

	// Jobs registered at run time (plugin cron). They start with the
	// scheduler, or at once when it is already running.
	dynMu   sync.Mutex
	dyn     map[string]*dynJob
	runCtx  context.Context
	stopped bool
}

type dynJob struct {
	every  time.Duration
	jitter time.Duration // > 0: next wait = every + uniform(-jitter, +jitter)
	first  time.Duration // delay of the first run (ScheduleJitter jobs)
	timed  bool          // set by ScheduleJitter
	fn     func(context.Context) error
	cancel context.CancelFunc
}

// minDynamicInterval is the shortest period of a job added with Schedule
// (a variable so tests need not wait a minute).
var minDynamicInterval = time.Minute

// minJitterInterval is the shortest period of a job added with ScheduleJitter
// (seconds-level so an end-to-end test can drive it).
var minJitterInterval = time.Second

// jitterRand returns a duration in [0, n); a variable so tests can fix it.
var jitterRand = func(n int64) int64 { return rand.Int64N(n) }

// NewScheduler creates a new scheduler instance.
func NewScheduler(db *database.DB, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		db:     db,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// SetPublishWorker registers the scheduled-publish worker (optional). When
// set, the scheduler promotes due `scheduled` entities once a minute.
func (s *Scheduler) SetPublishWorker(worker *PublishWorker) {
	s.publishWorker = worker
}

// SetRetentionPurgeWorker registers the daily session + remember-token purge.
func (s *Scheduler) SetRetentionPurgeWorker(worker *RetentionPurgeWorker) {
	s.retentionWorker = worker
}

// SetAgentHitsPartitionWorker registers the hatamoto_agent_hits partition job.
func (s *Scheduler) SetAgentHitsPartitionWorker(worker *PartitionWorker) {
	s.agentHitsPartitionWorker = worker
}

// SetAgentHitsMaintenanceWorker registers the 30-day client-IP truncation job.
func (s *Scheduler) SetAgentHitsMaintenanceWorker(worker *AgentHitsMaintenanceWorker) {
	s.agentHitsMaintenanceWorker = worker
}

// SetAgentRangesRefreshWorker registers the daily operator IP range download.
func (s *Scheduler) SetAgentRangesRefreshWorker(worker *agents.VerifyWorker) {
	s.agentRangesRefreshWorker = worker
}

// SetAgentVerifyWorker registers the 5-minute pending-hit verification job.
func (s *Scheduler) SetAgentVerifyWorker(worker *agents.VerifyWorker) {
	s.agentVerifyWorker = worker
}

// SetPruneWorker registers the batched high-churn-table prune job.
func (s *Scheduler) SetPruneWorker(worker *PruneWorker) {
	s.pruneWorker = worker
}

// SetEntityVersionWorker registers the entity-version coalesce/retention job.
func (s *Scheduler) SetEntityVersionWorker(worker *EntityVersionWorker) {
	s.versionWorker = worker
}

// SetEntityVersionInterval makes the entity-version job run every d (after a
// minute) instead of daily. The public demo uses it so old versions cannot pile
// up between two resets.
func (s *Scheduler) SetEntityVersionInterval(d time.Duration) {
	s.versionEvery = d
}

// Schedule runs fn every `every` (at least a minute; the first run is one
// period from now) under id, replacing a job with the same id. Jobs added
// before Start begin with it; in drain mode they never run. It is how plugin
// cron registrations reach the scheduler.
func (s *Scheduler) Schedule(id string, every time.Duration, fn func(context.Context) error) error {
	if every < minDynamicInterval {
		return fmt.Errorf("scheduler: %s: interval %s is below the %s minimum", id, every, minDynamicInterval)
	}
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	if s.stopped {
		return errors.New("scheduler: stopped")
	}
	if s.dyn == nil {
		s.dyn = map[string]*dynJob{}
	}
	if old := s.dyn[id]; old != nil && old.cancel != nil {
		old.cancel()
	}
	j := &dynJob{every: every, fn: fn}
	s.dyn[id] = j
	if s.runCtx != nil {
		s.startDynLocked(id, j)
	}
	return nil
}

// ScheduleJitter is Schedule with a first run after firstDelay and a wait
// between runs of every plus a uniform offset in [-jitter, +jitter]. A job with
// the same id is replaced. The 10 minute cap per run still applies.
func (s *Scheduler) ScheduleJitter(id string, every, jitter, firstDelay time.Duration, fn func(context.Context) error) error {
	if every < minJitterInterval {
		return fmt.Errorf("scheduler: %s: interval %s is below the %s minimum", id, every, minJitterInterval)
	}
	if jitter < 0 || jitter >= every {
		return fmt.Errorf("scheduler: %s: jitter %s must be within [0, %s)", id, jitter, every)
	}
	if firstDelay < 0 {
		firstDelay = 0
	}
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	if s.stopped {
		return errors.New("scheduler: stopped")
	}
	if s.dyn == nil {
		s.dyn = map[string]*dynJob{}
	}
	if old := s.dyn[id]; old != nil && old.cancel != nil {
		old.cancel()
	}
	j := &dynJob{every: every, jitter: jitter, first: firstDelay, timed: true, fn: fn}
	s.dyn[id] = j
	if s.runCtx != nil {
		s.startDynLocked(id, j)
	}
	return nil
}

// Unschedule stops every job added with Schedule whose id starts with prefix.
func (s *Scheduler) Unschedule(prefix string) {
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	for id, j := range s.dyn {
		if strings.HasPrefix(id, prefix) {
			if j.cancel != nil {
				j.cancel()
			}
			delete(s.dyn, id)
		}
	}
}

func (s *Scheduler) startDynLocked(id string, j *dynJob) {
	ctx, cancel := context.WithCancel(s.runCtx)
	j.cancel = cancel
	s.wg.Add(1)
	if j.timed {
		go s.runJittered(ctx, j, id)
		return
	}
	go s.runEvery(ctx, j.every, j.every, id, j.fn)
}

// runJittered runs fn after j.first, then after every+offset each time, until
// ctx/stopCh. Runs never overlap; errors are logged, never fatal.
func (s *Scheduler) runJittered(ctx context.Context, j *dynJob, name string) {
	defer s.wg.Done()
	wait := j.first
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.stopCh:
			t.Stop()
			return
		case <-t.C:
			s.runJob(ctx, name, j.fn)
		}
		wait = j.every
		if j.jitter > 0 {
			wait += time.Duration(jitterRand(int64(2*j.jitter)+1)) - j.jitter
		}
	}
}

// runDaily runs fn once after `first`, then every 24h, until ctx/stopCh. Errors
// are logged, never fatal. Each run gets a 10-minute timeout.
func (s *Scheduler) runDaily(ctx context.Context, first time.Duration, name string, fn func(context.Context) error) {
	s.runEvery(ctx, first, 24*time.Hour, name, fn)
}

// runEvery runs fn once after `first`, then every `interval`, until ctx/stopCh.
// A run that outlives the interval delays the next tick instead of overlapping
// it. Errors are logged, never fatal. Each run gets a 10-minute timeout.
func (s *Scheduler) runEvery(ctx context.Context, first, interval time.Duration, name string, fn func(context.Context) error) {
	defer s.wg.Done()
	select {
	case <-ctx.Done():
		return
	case <-s.stopCh:
		return
	case <-time.After(first):
		s.runJob(ctx, name, fn)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-t.C:
			s.runJob(ctx, name, fn)
		}
	}
}

type jobParentKey struct{}

// WithJobParent records parent, the scheduler's uncapped context, on a job's
// context. A job that must outlive the 10 minute cap but still stop on
// shutdown follows JobParent instead of its own (capped) context.
func WithJobParent(ctx, parent context.Context) context.Context {
	return context.WithValue(ctx, jobParentKey{}, parent)
}

// JobParent returns the uncapped context behind a job context, or ctx itself.
func JobParent(ctx context.Context) context.Context {
	if p, ok := ctx.Value(jobParentKey{}).(context.Context); ok {
		return p
	}
	return ctx
}

func (s *Scheduler) runJob(ctx context.Context, name string, fn func(context.Context) error) {
	jobCtx, cancel := context.WithTimeout(WithJobParent(ctx, ctx), 10*time.Minute)
	defer cancel()
	if err := fn(jobCtx); err != nil {
		s.logger.Error("scheduled job failed", slog.String("job", name), slog.Any("error", err))
	}
}

// Start begins the scheduler loops.
//
// When WORKER_DRAIN=true in the environment, all worker loops are skipped.
// The scheduler comes up but does nothing. This is the safety hook used
// before running destructive DB migrations: set the env, restart the API
// process, wait for any in-flight work (claimed before drain) to finish,
// verify the queue is empty, then run the migration.
func (s *Scheduler) Start(ctx context.Context) {
	if DrainMode() {
		s.logger.Warn("scheduler started in DRAIN mode — no worker loops will run (WORKER_DRAIN=true)")
		return
	}

	s.logger.Info("starting scheduler")

	// Promote due scheduled entities every minute.
	if s.publishWorker != nil {
		s.wg.Add(1)
		go s.runPublishLoop(ctx)
	}

	// Purge expired sessions + revoked remember-tokens once per day.
	if s.retentionWorker != nil {
		s.wg.Add(1)
		go s.runDaily(ctx, 6*time.Minute, "retention-purge", s.retentionWorker.Run)
	}

	// Agent hit partitions (ahead + ~90d retention) and the 30-day IP truncation.
	if s.agentHitsPartitionWorker != nil {
		s.wg.Add(1)
		go s.runDaily(ctx, 45*time.Second, "agent-hits-partition", s.agentHitsPartitionWorker.Run)
	}
	if s.agentHitsMaintenanceWorker != nil {
		s.wg.Add(1)
		go s.runDaily(ctx, 12*time.Minute, "agent-hits-maintenance", s.agentHitsMaintenanceWorker.Run)
	}

	// Agent verification: download operator IP ranges (first run soon after
	// start so ranges exist, then daily) and flip pending hits every 5 minutes.
	// Ranges are also loaded from the database before the first verify.
	if s.agentRangesRefreshWorker != nil {
		s.wg.Add(1)
		go s.runDaily(ctx, 90*time.Second, "agent-ranges-refresh", s.agentRangesRefreshWorker.RunRefresh)
	}
	if s.agentVerifyWorker != nil {
		s.wg.Add(1)
		go s.runEvery(ctx, 2*time.Minute, 5*time.Minute, "agent-verify", s.agentVerifyWorker.RunVerify)
	}

	// Batched prune of high-churn tables (the 404 log).
	if s.pruneWorker != nil {
		s.wg.Add(1)
		go s.runDaily(ctx, 8*time.Minute, "prune", s.pruneWorker.Run)
	}

	// Coalesce + keep-newest-20 entity versions once per day.
	if s.versionWorker != nil {
		s.wg.Add(1)
		if s.versionEvery > 0 {
			go s.runEvery(ctx, min(time.Minute, s.versionEvery), s.versionEvery, "entity-versions", s.versionWorker.Run)
		} else {
			go s.runDaily(ctx, 10*time.Minute, "entity-versions", s.versionWorker.Run)
		}
	}

	s.dynMu.Lock()
	s.runCtx = ctx
	for id, j := range s.dyn {
		s.startDynLocked(id, j)
	}
	s.dynMu.Unlock()

	s.logger.Info("scheduler started")
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop() {
	s.logger.Info("stopping scheduler")
	s.dynMu.Lock()
	s.stopped = true
	s.dynMu.Unlock()
	close(s.stopCh)

	if s.publishTicker != nil {
		s.publishTicker.Stop()
	}

	s.wg.Wait()
	s.logger.Info("scheduler stopped")
}

// runPublishLoop promotes scheduled entities whose published_at has fallen
// due. The cadence is one minute: the query is a partial-index lookup over
// the handful of pending rows (idx_entities_scheduled_due), so it costs
// almost nothing, and a coarser tick would stack on top of the frontend's
// 300s ISR window and push a post's real appearance well past its slot.
//
// First sweep fires after 30 seconds so a deploy that lands just after a
// slot does not wait a full interval to catch up.
func (s *Scheduler) runPublishLoop(ctx context.Context) {
	defer s.wg.Done()

	select {
	case <-ctx.Done():
		return
	case <-s.stopCh:
		return
	case <-time.After(30 * time.Second):
		s.processScheduledPublishes(ctx)
	}

	s.publishTicker = time.NewTicker(1 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-s.publishTicker.C:
			s.processScheduledPublishes(ctx)
		}
	}
}

func (s *Scheduler) processScheduledPublishes(ctx context.Context) {
	jobCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	if _, err := s.publishWorker.Run(jobCtx); err != nil {
		s.logger.Error("scheduled publish failed", slog.Any("error", err))
	}
}
