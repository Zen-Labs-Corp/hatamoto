package pluginhost

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/sdk"
)

// StandingGate is what the runtime asks about runs a cron job starts. The
// server implements it (a standing approval store plus the changeset
// engine); the runtime never publishes anything itself.
type StandingGate interface {
	// Active reports whether the owner has an approval for this plugin's run
	// that is still in force. A tick with no active approval starts nothing.
	Active(ctx context.Context, plugin, run string) (bool, error)
	// AutoPublish is called once a cron started run has finished. It decides
	// whether the staged changeset is inside the approved rule and publishes
	// it if so. A refusal returns nil and leaves the changeset staged for
	// review; an error is only logged.
	AutoPublish(ctx context.Context, plugin, run string, changesetID int64) error
}

// SetStandingGate sets the gate for cron started runs. With none, a run bound
// to a cron job never starts.
func (r *Runtime) SetStandingGate(g StandingGate) { r.gate.Store(&g) }

func (r *Runtime) standingGate() StandingGate {
	if p := r.gate.Load(); p != nil {
		return *p
	}
	return nil
}

// cronRun returns the run a cron job starts, if the manifest binds one.
func cronRun(m sdk.Manifest, job string) (sdk.Run, bool) {
	i := slices.IndexFunc(m.Runs, func(d sdk.Run) bool { return d.Cron == job && d.StandingApproval != nil })
	if i < 0 {
		return sdk.Run{}, false
	}
	return m.Runs[i], true
}

// tickRun is the cron tick of a job bound to a run: it starts the run through
// StartRun (the same phases and changeset as an owner's run) when an approval
// is active, and does nothing otherwise.
func (r *Runtime) tickRun(ctx context.Context, plugin string, run sdk.Run) error {
	g := r.standingGate()
	if g == nil {
		return nil
	}
	ok, err := g.Active(ctx, plugin, run.Name)
	if err != nil || !ok {
		return err
	}
	_, err = r.StartRun(ctx, plugin, run.Name, StartRunInput{Trigger: plugrun.TriggerCron})
	if errors.Is(err, ErrActiveRun) {
		return nil // the previous run is still going: skip this tick
	}
	return err
}

// autoPublish hands a finished cron started run to the gate. The gate's
// refusal leaves the changeset staged; nothing here publishes.
func (r *Runtime) autoPublish(ctx context.Context, run plugrun.Run) {
	g := r.standingGate()
	if g == nil || run.Trigger != plugrun.TriggerCron || run.ChangesetID == nil {
		return
	}
	if err := g.AutoPublish(ctx, run.Plugin, run.Name, *run.ChangesetID); err != nil {
		r.log.Error("automatic publish failed; the changes wait for review", slog.String("plugin", run.Plugin), slog.String("run", run.Name), slog.Any("error", err))
	}
}
