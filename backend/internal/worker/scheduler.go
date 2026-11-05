package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	unpublishWorker            *UnpublishWorker
	retentionWorker            *RetentionPurgeWorker
	agentHitsPartitionWorker   *PartitionWorker
	agentHitsMaintenanceWorker *AgentHitsMaintenanceWorker
	agentRangesRefreshWorker   *agents.VerifyWorker
	agentVerifyWorker          *agents.VerifyWorker
	pruneWorker                *PruneWorker
	versionWorker              *EntityVersionWorker
	versionEvery               time.Duration // 0: daily
	logger                     *slog.Logger

	recorder Recorder // last-run bookkeeping; nil records nothing

	// Control channels
	stopCh chan struct{}
	wg     sync.WaitGroup

	// Jobs registered at run time (plugin cron). They start with the
	// scheduler, or at once when it is already running.
	dynMu   sync.Mutex
	dyn     map[string]*dynJob
	runCtx  context.Context
	stopped bool

	// Every job the scheduler knows, fixed and dynamic, by id.
	jobsMu sync.Mutex
	jobs   map[string]*jobState
}

// jobState is the bookkeeping of one job id. It outlives a re-Schedule of the
// same id, so the run lock keeps two runs from overlapping across it.
type jobState struct {
	every, jitter time.Duration
	fn            func(context.Context) error
	manual        func(context.Context) error // Run now variant, nil: fn
	next          time.Time                   // zero: not waiting yet
	run           sync.Mutex                  // held for the length of a run
	running       atomic.Bool
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

// SetUnpublishWorker registers the scheduled-unpublish worker (optional). It
// runs on the publish tick, so it only runs when the publish worker is set.
func (s *Scheduler) SetUnpublishWorker(worker *UnpublishWorker) {
	s.unpublishWorker = worker
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
	s.register(id, every, 0, fn)
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
	s.register(id, every, jitter, fn)
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
			s.jobsMu.Lock()
			delete(s.jobs, id)
			s.jobsMu.Unlock()
		}
	}
}

func (s *Scheduler) startDynLocked(id string, j *dynJob) {
	ctx, cancel := context.WithCancel(s.runCtx)
	j.cancel = cancel
	first := j.every
	if j.timed {
		first = j.first
	}
	s.setNext(id, time.Now().Add(first)) // visible at once, before the loop runs
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
		s.setNext(name, time.Now().Add(wait))
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

// runEvery runs fn once after `first`, then every `interval`, until ctx/stopCh.
// A run that outlives the interval delays the next tick instead of overlapping
// it. Errors are logged, never fatal. Each run gets a 10-minute timeout.
func (s *Scheduler) runEvery(ctx context.Context, first, interval time.Duration, name string, fn func(context.Context) error) {
	defer s.wg.Done()
	s.setNext(name, time.Now().Add(first))
	select {
	case <-ctx.Done():
		return
	case <-s.stopCh:
		return
	case <-time.After(first):
		s.setNext(name, time.Now().Add(interval))
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
			s.setNext(name, time.Now().Add(interval))
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

// runJob runs fn as job name under the per-job lock (a run of the same job
// waits for the one in progress), then records the outcome.
func (s *Scheduler) runJob(ctx context.Context, name string, fn func(context.Context) error) {
	st := s.state(name)
	st.run.Lock()
	defer st.run.Unlock()
	s.execute(ctx, st, name, fn)
}

// execute runs fn under the 10 minute cap, logs a failure and records the run.
// The caller holds st.run.
func (s *Scheduler) execute(ctx context.Context, st *jobState, name string, fn func(context.Context) error) Run {
	st.running.Store(true)
	defer st.running.Store(false)
	jobCtx, cancel := context.WithTimeout(WithJobParent(ctx, ctx), 10*time.Minute)
	defer cancel()
	r := Run{ID: name, Started: time.Now()}
	err := fn(jobCtx)
	r.Finished = time.Now()
	r.OK = err == nil
	r.Err = cleanError(err)
	if err != nil {
		s.logger.Error("scheduled job failed", slog.String("job", name), slog.Any("error", err))
	}
	if s.recorder != nil {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer rcancel()
		if rerr := s.recorder.Record(rctx, r); rerr != nil {
			s.logger.Warn("job status not recorded", slog.String("job", name), slog.Any("error", rerr))
		}
	}
	return r
}

// state returns the bookkeeping of id, creating it on first use.
func (s *Scheduler) state(id string) *jobState {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.jobs == nil {
		s.jobs = map[string]*jobState{}
	}
	st := s.jobs[id]
	if st == nil {
		st = &jobState{}
		s.jobs[id] = st
	}
	return st
}

// register records what a job runs and how often.
func (s *Scheduler) register(id string, every, jitter time.Duration, fn func(context.Context) error) {
	st := s.state(id)
	s.jobsMu.Lock()
	st.every, st.jitter, st.fn = every, jitter, fn
	st.next = time.Time{}
	s.jobsMu.Unlock()
}

func (s *Scheduler) setNext(id string, t time.Time) {
	st := s.state(id)
	s.jobsMu.Lock()
	st.next = t
	s.jobsMu.Unlock()
}

// SetRecorder sets where the outcome of each run is stored (optional).
func (s *Scheduler) SetRecorder(r Recorder) { s.recorder = r }

// SetManual sets the function Run now uses for id instead of the scheduled
// one (the update check, which must only check when run by hand).
func (s *Scheduler) SetManual(id string, fn func(context.Context) error) {
	st := s.state(id)
	s.jobsMu.Lock()
	st.manual = fn
	s.jobsMu.Unlock()
}

// JobInfo is the live state of one registered job.
type JobInfo struct {
	ID      string
	Every   time.Duration
	Jitter  time.Duration // > 0: Next is only about right
	Next    time.Time     // zero: unknown
	Running bool
}

// Jobs lists the jobs registered on this scheduler, sorted by id.
func (s *Scheduler) Jobs() []JobInfo {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	out := make([]JobInfo, 0, len(s.jobs))
	now := time.Now()
	for id, st := range s.jobs {
		if st.fn == nil {
			continue
		}
		next := st.next
		// The loop sets next before a run starts, so while a run is under way
		// (or queued behind Run now) it is already past: show the tick after.
		if !next.IsZero() && st.every > 0 && !next.After(now) {
			next = next.Add((now.Sub(next)/st.every + 1) * st.every)
		}
		out = append(out, JobInfo{ID: id, Every: st.every, Jitter: st.jitter, Next: next, Running: st.running.Load()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Running reports whether the scheduler has started and is not draining.
func (s *Scheduler) Running() bool {
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	return s.runCtx != nil && !s.stopped && !DrainMode()
}

// Why RunNow refused.
var (
	ErrUnknownJob     = errors.New("scheduler: unknown job")
	ErrNotSafe        = errors.New("scheduler: job cannot be run by hand")
	ErrAlreadyRunning = errors.New("scheduler: job is already running")
	ErrNotRunning     = errors.New("scheduler: this process does not run the job")
	// ErrNoManualRun: a ManualOnly job has no by-hand variant on this process.
	ErrNoManualRun = errors.New("scheduler: job has no by-hand variant here")
	// ErrStillRunning: ctx ended while the run goes on in the background.
	ErrStillRunning = errors.New("scheduler: job still running")
)

// RunNow runs a Safe catalogue job once, now, sharing the per-job lock with the
// schedule: it fails with ErrAlreadyRunning instead of waiting. The run belongs
// to the scheduler, not to ctx: if ctx ends first RunNow returns
// ErrStillRunning and the job finishes (and is recorded) on its own.
func (s *Scheduler) RunNow(ctx context.Context, id string) (Run, error) {
	if IsPluginJob(id) {
		return Run{}, ErrNotSafe
	}
	def, ok := Lookup(id)
	if !ok {
		return Run{}, ErrUnknownJob
	}
	if !def.Safe {
		return Run{}, ErrNotSafe
	}
	s.dynMu.Lock()
	if s.runCtx == nil || s.stopped || DrainMode() {
		s.dynMu.Unlock()
		return Run{}, ErrNotRunning
	}
	runCtx := s.runCtx
	s.jobsMu.Lock()
	st := s.jobs[id]
	var fn func(context.Context) error
	if st != nil {
		if fn = st.manual; fn == nil && !def.ManualOnly {
			fn = st.fn
		}
	}
	s.jobsMu.Unlock()
	if fn == nil || !st.run.TryLock() {
		s.dynMu.Unlock()
		switch {
		case fn == nil && st != nil && def.ManualOnly:
			return Run{}, ErrNoManualRun
		case fn == nil:
			return Run{}, ErrNotRunning
		}
		return Run{}, ErrAlreadyRunning
	}
	s.wg.Add(1) // under dynMu: Stop marks stopped there before it waits
	s.dynMu.Unlock()
	done := make(chan Run, 1)
	go func() {
		defer s.wg.Done()
		defer st.run.Unlock()
		done <- s.execute(runCtx, st, id, fn)
	}()
	select {
	case r := <-done:
		return r, nil
	case <-ctx.Done():
		return Run{}, ErrStillRunning
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
		s.startFixed(ctx, "publish", 30*time.Second, time.Minute, s.publishAndUnpublish)
	}

	// Purge expired sessions + revoked remember-tokens once per day.
	if s.retentionWorker != nil {
		s.startFixed(ctx, "retention_purge", 6*time.Minute, 24*time.Hour, s.retentionWorker.Run)
	}

	// Agent hit partitions (ahead + ~90d retention) and the 30-day IP truncation.
	if s.agentHitsPartitionWorker != nil {
		s.startFixed(ctx, "agent_hits_partition", 45*time.Second, 24*time.Hour, s.agentHitsPartitionWorker.Run)
	}
	if s.agentHitsMaintenanceWorker != nil {
		s.startFixed(ctx, "agent_hits_maintenance", 12*time.Minute, 24*time.Hour, s.agentHitsMaintenanceWorker.Run)
	}

	// Agent verification: download operator IP ranges (first run soon after
	// start so ranges exist, then daily) and flip pending hits every 5 minutes.
	// Ranges are also loaded from the database before the first verify.
	if s.agentRangesRefreshWorker != nil {
		s.startFixed(ctx, "agent_ranges_refresh", 90*time.Second, 24*time.Hour, s.agentRangesRefreshWorker.RunRefresh)
	}
	if s.agentVerifyWorker != nil {
		s.startFixed(ctx, "agent_verify", 2*time.Minute, 5*time.Minute, s.agentVerifyWorker.RunVerify)
	}

	// Batched prune of high-churn tables (the 404 log).
	if s.pruneWorker != nil {
		s.startFixed(ctx, "prune", 8*time.Minute, 24*time.Hour, s.pruneWorker.Run)
	}

	// Coalesce + keep-newest-20 entity versions once per day.
	if s.versionWorker != nil {
		if s.versionEvery > 0 {
			s.startFixed(ctx, "entity_versions", min(time.Minute, s.versionEvery), s.versionEvery, s.versionWorker.Run)
		} else {
			s.startFixed(ctx, "entity_versions", 10*time.Minute, 24*time.Hour, s.versionWorker.Run)
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

	s.wg.Wait()
	s.logger.Info("scheduler stopped")
}

// publishAndUnpublish is the publish job: promote scheduled entities whose
// published_at has fallen due, then retire the ones past their unpublish time.
// The cadence is one minute: the query is a partial-index lookup over the
// handful of pending rows (idx_entities_scheduled_due), so it costs almost
// nothing, and a coarser tick would stack on top of the frontend's 300s ISR
// window and push a post's real appearance well past its slot. The first run
// is 30 seconds after start so a deploy that lands just after a slot does not
// wait a full interval. A run is capped at one minute.
func (s *Scheduler) publishAndUnpublish(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	_, err := s.publishWorker.Run(ctx)
	if err != nil {
		err = fmt.Errorf("publish: %w", err)
	}
	if s.unpublishWorker != nil {
		if _, uerr := s.unpublishWorker.Run(ctx); uerr != nil {
			err = errors.Join(err, fmt.Errorf("unpublish: %w", uerr))
		}
	}
	return err
}

// startFixed registers a core job under its canonical id and starts its loop.
func (s *Scheduler) startFixed(ctx context.Context, id string, first, every time.Duration, fn func(context.Context) error) {
	s.register(id, every, 0, fn)
	s.setNext(id, time.Now().Add(first))
	s.wg.Add(1)
	go s.runEvery(ctx, first, every, id, fn)
}
