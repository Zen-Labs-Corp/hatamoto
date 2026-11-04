package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/sdk"
)

// ErrRunNotCancellable means the run is not running (it ended, or never was).
var ErrRunNotCancellable = errors.New("run is not running")

// ReasonCancelled is the reason stored on a run the owner cancelled.
const ReasonCancelled = "cancelled"

// runCancels maps the id of a run whose driver is alive in this process to
// the cancel func of the driver's context. Run ids are unique, so one map
// serves every Runtime.
var runCancels sync.Map // uuid.UUID -> context.CancelFunc

// registerRunCancel derives the context the driver of runID runs its steps
// with and makes it cancellable by CancelRun until deregister is called.
func registerRunCancel(parent context.Context, runID uuid.UUID) (ctx context.Context, deregister func()) {
	ctx, cancel := context.WithCancel(parent)
	runCancels.Store(runID, context.CancelFunc(cancel))
	return ctx, func() {
		runCancels.Delete(runID)
		cancel()
	}
}

// CancelRun stops the running run id of plugin and discards its staged
// changeset. The run is failed first with reason "cancelled", so a step that
// is cut short cannot record another reason; then the driver's context is
// cancelled, which ends the step in flight. A run whose driver is gone (the
// process restarted) is failed and discarded the same way. A run of another
// plugin is plugrun.ErrNotFound, a run that is not running
// ErrRunNotCancellable.
func (r *Runtime) CancelRun(ctx context.Context, plugin string, id uuid.UUID) error {
	svc := r.caps.api.svc
	if svc.Runs == nil {
		return ErrRunsUnavailable
	}
	run, err := svc.Runs.Get(ctx, id)
	if err != nil {
		return err
	}
	if run.Plugin != plugin {
		return plugrun.ErrNotFound
	}
	if err := svc.Runs.Fail(ctx, id, ReasonCancelled); err != nil {
		if errors.Is(err, plugrun.ErrNotRunning) {
			return ErrRunNotCancellable
		}
		return fmt.Errorf("failed to cancel run: %w", err)
	}
	if cancel, ok := runCancels.Load(id); ok {
		cancel.(context.CancelFunc)()
	}
	if run.ChangesetID != nil && svc.Changesets != nil {
		if err := svc.Changesets.Discard(ctx, *run.ChangesetID); err != nil && !errors.Is(err, changeset.ErrNotStaged) {
			r.log.Error("run: failed to discard the changeset of a cancelled run",
				slog.String("run", id.String()), slog.Any("error", err))
			return fmt.Errorf("failed to discard the changeset: %w", err)
		}
	}
	r.log.Warn("run cancelled", slog.String("plugin", plugin), slog.String("run", id.String()))
	return nil
}

// RunLimits are the limits one step of a plugin's run is held to.
type RunLimits struct {
	StepTimeout time.Duration // the guest's time for one on_run_step call
	MemoryBytes uint64        // cap on the guest's linear memory
}

// RunLimitsFor returns the step limits of a loaded plugin, from its manifest.
func (r *Runtime) RunLimitsFor(plugin string) (RunLimits, bool) {
	m, ok := r.manifest(plugin)
	if !ok {
		return RunLimits{}, false
	}
	pages := uint64(m.Limits.MemoryPages)
	if pages == 0 {
		pages = sdk.DefaultMemoryPages
	}
	return RunLimits{StepTimeout: m.HookTimeout(sdk.RunStepExport), MemoryBytes: pages * 64 << 10}, true
}
