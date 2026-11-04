//go:build integration

package pluginhost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/plugrun"
)

// stagedRun creates a running run with an open changeset and no driver.
func (r *driverRig) stagedRun() plugrun.Run {
	r.t.Helper()
	ctx := context.Background()
	run, err := r.runs.Create(ctx, plugrun.Run{Plugin: runnerPlugin, Name: "import"})
	if err != nil {
		r.t.Fatal(err)
	}
	cs, err := r.eng.Begin(ctx, run.ID, runnerPlugin)
	if err == nil {
		err = r.runs.SetChangeset(ctx, run.ID, cs)
	}
	if err != nil {
		r.t.Fatal(err)
	}
	run.ChangesetID = &cs
	return run
}

func TestCancelRun(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	run := r.stagedRun()

	if err := r.rt.CancelRun(ctx, "other-plugin", run.ID); !errors.Is(err, plugrun.ErrNotFound) {
		t.Fatalf("wrong plugin: %v", err)
	}
	if err := r.rt.CancelRun(ctx, runnerPlugin, uuid.New()); !errors.Is(err, plugrun.ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	// A live driver's context is cancelled.
	stepCtx, deregister := registerRunCancel(r.rt.runCtx, run.ID)
	defer deregister()
	if err := r.rt.CancelRun(ctx, runnerPlugin, run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stepCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("the driver context was not cancelled")
	}
	got, _ := r.runs.Get(ctx, run.ID)
	if got.State != plugrun.StateFailed || got.Reason != ReasonCancelled {
		t.Fatalf("run %s %q", got.State, got.Reason)
	}
	if s := r.changesetState(*run.ChangesetID); s != changeset.StateDiscarded {
		t.Fatalf("changeset %s", s)
	}
	if err := r.rt.CancelRun(ctx, runnerPlugin, run.ID); !errors.Is(err, ErrRunNotCancellable) {
		t.Fatalf("cancel twice: %v", err)
	}
}

func TestCancelFinishedRun(t *testing.T) {
	r := newDriverRig(t)
	run := r.finished("")
	if run.State != plugrun.StateFinished {
		t.Fatalf("run %s: %s", run.State, run.Reason)
	}
	if err := r.rt.CancelRun(context.Background(), runnerPlugin, run.ID); !errors.Is(err, ErrRunNotCancellable) {
		t.Fatalf("finished run: %v", err)
	}
	if s := r.changesetState(*run.ChangesetID); s != changeset.StateStaged {
		t.Fatalf("changeset %s", s)
	}
}

func TestRunLimitsFor(t *testing.T) {
	r := newDriverRig(t)
	l, ok := r.rt.RunLimitsFor(runnerPlugin)
	if !ok || l.StepTimeout <= 0 || l.MemoryBytes == 0 {
		t.Fatalf("limits %+v %v", l, ok)
	}
	if _, ok := r.rt.RunLimitsFor("nope"); ok {
		t.Fatal("limits for an unknown plugin")
	}
}
