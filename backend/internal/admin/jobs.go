package admin

import (
	"context"
	"time"
)

// JobsService is the scheduled-jobs overview behind Settings > Jobs.
type JobsService interface {
	// Snapshot lists every job with its last run and next run.
	Snapshot(ctx context.Context) (JobsView, error)
	// RunNow runs a safe job once. It returns worker.ErrAlreadyRunning,
	// ErrNotSafe, ErrUnknownJob or ErrNotRunning when it refuses.
	RunNow(ctx context.Context, id string) (JobRunResult, error)
}

// JobsView is what the Jobs page shows.
type JobsView struct {
	WorkerHere bool // this process runs the worker
	Drain      bool // WORKER_DRAIN: no job runs
	Rows       []JobRow
}

// JobRow is one job. Last is zero for a job that never ran.
type JobRow struct {
	ID, Name, Schedule string
	Last               time.Time
	Took               time.Duration
	OK                 bool
	Error              string
	Next               time.Time // zero: not known on this process
	NextApprox         bool      // the schedule has jitter
	Running            bool
	Disabled           bool // turned off by server settings
	CanRun             bool
}

// JobRunResult is the outcome of Run now. Started is true when the job
// outlived the wait and is still running.
type JobRunResult struct {
	Name    string
	OK      bool
	Took    time.Duration
	Error   string
	Started bool
}
