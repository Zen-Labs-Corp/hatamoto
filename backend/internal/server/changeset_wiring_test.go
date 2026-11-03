package server

import (
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/worker"
)

func changesetTestServer(withScheduler bool) *Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{logger: log, changesets: &changeset.Engine{}}
	if withScheduler {
		s.scheduler = worker.NewScheduler(nil, log)
	}
	return s
}

func TestChangesetSweepJobIsKnown(t *testing.T) {
	for _, j := range knownWorkerJobs {
		if j == changesetSweepJob {
			return
		}
	}
	t.Fatalf("knownWorkerJobs missing %q", changesetSweepJob)
}

func TestChangesetSweepIsScheduled(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	if !changesetTestServer(true).scheduleChangesetSweep() {
		t.Fatal("sweep not scheduled with the worker on")
	}
}

func TestChangesetSweepHonoursDisableList(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "Changeset_Sweep")
	if changesetTestServer(true).scheduleChangesetSweep() {
		t.Fatal("changeset_sweep is disabled but scheduled")
	}
	if got := disabledWorkerJobs(); len(got) != 1 || got[0] != changesetSweepJob {
		t.Fatalf("disabled: %v", got)
	}
}

func TestChangesetSweepNeedsAScheduler(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	if changesetTestServer(false).scheduleChangesetSweep() { // HATAMOTO_WORKER=off
		t.Fatal("no scheduler, yet the sweep is scheduled")
	}
}
