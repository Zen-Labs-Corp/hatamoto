package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/core/internal/wasmhost"
	"hatamoto.dev/sdk"
)

// StaleRunAfter is how long a running run may go without a step before
// StartRun treats it as left behind by a crash and fails it, so one lost
// run cannot lock its plugin out for good.
const StaleRunAfter = time.Hour

// maxRunInputBytes bounds one owner-supplied run input; maxRunHosts the
// hosts the owner confirms for one run.
const (
	maxRunInputBytes = 4096
	maxRunHosts      = 32
)

// Errors StartRun returns for a start the owner can correct.
var (
	ErrRunsUnavailable = errors.New("runs are not available")
	ErrPluginNotLoaded = errors.New("plugin is not loaded")
	ErrUnknownRun      = errors.New("plugin does not declare this run")
	// ErrActiveRun means the plugin already has a run in progress.
	ErrActiveRun = plugrun.ErrActiveRun
)

// InvalidRunInputError says why the owner's inputs were refused.
type InvalidRunInputError struct{ Msg string }

func (e *InvalidRunInputError) Error() string { return e.Msg }

// StartRunInput is what the owner supplies when starting a run.
type StartRunInput struct {
	// Inputs are the values of the run's declared inputs, by input name.
	Inputs map[string]string
	// Hosts are the hosts the run may fetch from (replacing the manifest's
	// allowed_hosts for the run's fetch phase).
	Hosts []string
	// UploadID is the owner's uploaded file for upload.read.
	UploadID *uuid.UUID
}

// StartRun starts the run name of plugin and returns its id. The run row is
// inserted first (the database allows one running run per plugin), then the
// changeset is opened once, then a driver goroutine calls on_run_step until
// the run ends. The driver never uses the caller's context.
func (r *Runtime) StartRun(ctx context.Context, plugin, name string, in StartRunInput) (uuid.UUID, error) {
	svc := r.caps.api.svc
	if svc.Runs == nil || svc.Changesets == nil {
		return uuid.Nil, ErrRunsUnavailable
	}
	m, ok := r.manifest(plugin)
	if !ok {
		return uuid.Nil, ErrPluginNotLoaded
	}
	i := slices.IndexFunc(m.Runs, func(d sdk.Run) bool { return d.Name == name })
	if i < 0 {
		return uuid.Nil, ErrUnknownRun
	}
	params, err := checkRunInputs(m.Runs[i], in.Inputs)
	if err != nil {
		return uuid.Nil, err
	}
	hosts, err := checkRunHosts(in.Hosts)
	if err != nil {
		return uuid.Nil, err
	}
	if in.UploadID != nil {
		up, err := svc.Runs.Upload(ctx, *in.UploadID)
		if err != nil || up.Plugin != plugin {
			return uuid.Nil, &InvalidRunInputError{"the upload does not exist"}
		}
	}
	if err := r.failStale(ctx, plugin); err != nil {
		return uuid.Nil, err
	}
	run, err := svc.Runs.Create(ctx, plugrun.Run{Plugin: plugin, Name: name, Params: params, Hosts: hosts, UploadID: in.UploadID})
	if err != nil {
		return uuid.Nil, err
	}
	csID, err := svc.Changesets.Begin(ctx, run.ID, plugin)
	if err == nil {
		err = svc.Runs.SetChangeset(ctx, run.ID, csID)
	}
	if err != nil {
		r.failRun(run.ID, "could not open the changeset: "+err.Error())
		return uuid.Nil, fmt.Errorf("failed to start run: %w", err)
	}
	r.runWG.Add(1)
	go func() {
		defer r.runWG.Done()
		r.drive(run.ID)
	}()
	return run.ID, nil
}

// WaitRuns blocks until every driver started by StartRun has returned.
func (r *Runtime) WaitRuns() { r.runWG.Wait() }

// RecoverRuns fails every run still marked running and discards its
// changeset. Call it once at startup, before runs can start: a run found
// then was left by the previous process and no driver owns it.
func (r *Runtime) RecoverRuns(ctx context.Context) error {
	runs := r.caps.api.svc.Runs
	if runs == nil {
		return nil
	}
	stale, err := runs.StaleRunning(ctx, 0)
	if err != nil {
		return err
	}
	for _, run := range stale {
		r.failRunCtx(ctx, run, "interrupted by a restart")
	}
	return nil
}

// failStale fails the plugin's running run when it has not moved for
// StaleRunAfter.
func (r *Runtime) failStale(ctx context.Context, plugin string) error {
	runs := r.caps.api.svc.Runs
	stale, err := runs.StaleRunning(ctx, StaleRunAfter)
	if err != nil {
		return err
	}
	for _, run := range stale {
		if run.Plugin == plugin {
			r.failRunCtx(ctx, run, "no step for over an hour; assumed lost")
		}
	}
	return nil
}

func checkRunInputs(d sdk.Run, given map[string]string) (json.RawMessage, error) {
	known := map[string]sdk.RunInput{}
	for _, in := range d.Inputs {
		known[in.Name] = in
	}
	for k := range given {
		if _, ok := known[k]; !ok {
			return nil, &InvalidRunInputError{fmt.Sprintf("unknown input %q", k)}
		}
	}
	out := map[string]any{}
	for _, in := range d.Inputs {
		v := strings.TrimSpace(given[in.Name])
		if v == "" {
			if in.Required {
				return nil, &InvalidRunInputError{fmt.Sprintf("%s is required", in.Label)}
			}
			continue
		}
		if len(v) > maxRunInputBytes {
			return nil, &InvalidRunInputError{fmt.Sprintf("%s is too long", in.Label)}
		}
		switch in.Type {
		case "url":
			if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, &InvalidRunInputError{fmt.Sprintf("%s must be an http or https URL", in.Label)}
			}
			out[in.Name] = v
		case "number":
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, &InvalidRunInputError{fmt.Sprintf("%s must be a number", in.Label)}
			}
			out[in.Name] = n
		case "bool":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, &InvalidRunInputError{fmt.Sprintf("%s must be true or false", in.Label)}
			}
			out[in.Name] = b
		case "select":
			if !slices.Contains(in.Options, v) {
				return nil, &InvalidRunInputError{fmt.Sprintf("%s is not one of the options", in.Label)}
			}
			out[in.Name] = v
		default:
			out[in.Name] = v
		}
	}
	return json.Marshal(out)
}

// checkRunHosts validates the hosts the owner confirmed: bare host names
// (optionally with a port or a leading "*."), never URLs.
func checkRunHosts(hosts []string) ([]string, error) {
	if len(hosts) > maxRunHosts {
		return nil, &InvalidRunInputError{fmt.Sprintf("more than %d hosts", maxRunHosts)}
	}
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if len(h) > 253 || strings.ContainsAny(h, "/\\@?# \t") {
			return nil, &InvalidRunInputError{fmt.Sprintf("%q is not a host name", h)}
		}
		out = append(out, h)
	}
	return out, nil
}

// drive runs the steps of one run: it calls on_run_step with the run's phase
// and cursor until the plugin finishes the transform phase or the run fails.
// A step that errors, a tripped circuit breaker, run.fail and the step cap
// all fail the run; a failed step is never retried. Every step runs on a
// cold instance that is closed afterwards (ADR 0004 inv. 4): guest memory
// must not carry data across phases, runs or into ordinary hooks. The cost
// is one module instantiation per step, small against a step's own work
// (steps are network or batch sized, seconds apart).
func (r *Runtime) drive(runID uuid.UUID) {
	runs := r.caps.api.svc.Runs
	bg := context.Background()
	for {
		run, err := runs.Get(bg, runID)
		if err != nil || run.State != plugrun.StateRunning {
			return // ended elsewhere (recovery) or the row is gone
		}
		m, ok := r.manifest(run.Plugin)
		if !ok {
			r.failRunCtx(bg, run, "the plugin was unloaded")
			return
		}
		n, err := runs.BumpSteps(bg, runID)
		if err != nil {
			return
		}
		if n > plugrun.MaxRunSteps {
			r.failRunCtx(bg, run, fmt.Sprintf("more than %d steps", plugrun.MaxRunSteps))
			return
		}
		sig := &RunSignal{}
		rc := RunCtx{RunID: runID, Plugin: run.Plugin, Name: run.Name, Phase: run.Phase, Hosts: run.Hosts, Signal: sig}
		if i := slices.IndexFunc(m.Runs, func(d sdk.Run) bool { return d.Name == run.Name }); i >= 0 {
			rc.Proposes = m.Runs[i].Proposes
		}
		in, _ := json.Marshal(sdk.RunStep{RunID: runID.String(), Phase: run.Phase, Cursor: run.Cursor})
		if _, err := r.host.Call(wasmhost.WithFreshInstance(WithRun(bg, rc)), run.Plugin, sdk.RunStepExport, in); err != nil {
			reason := "step failed: " + err.Error()
			if errors.Is(err, wasmhost.ErrCircuitOpen) {
				reason = "plugin disabled after repeated failures: " + err.Error()
			}
			r.failRunCtx(bg, run, reason)
			return
		}
		finish, fail, summary := sig.Get()
		switch {
		case fail != "":
			r.failRunCtx(bg, run, fail)
			return
		case finish && run.Phase == plugrun.PhaseFetch:
			err = runs.AdvancePhase(bg, runID, plugrun.PhaseTransform)
		case finish:
			if err := runs.Finish(bg, runID, summary); err != nil {
				r.log.Error("run: failed to finish", slog.String("run", runID.String()), slog.Any("error", err))
			}
			return
		}
		if err != nil {
			return
		}
	}
}

// failRun marks a run failed and discards its changeset.
func (r *Runtime) failRun(runID uuid.UUID, reason string) {
	bg := context.Background()
	run, err := r.caps.api.svc.Runs.Get(bg, runID)
	if err != nil {
		return
	}
	r.failRunCtx(bg, run, reason)
}

func (r *Runtime) failRunCtx(ctx context.Context, run plugrun.Run, reason string) {
	svc := r.caps.api.svc
	log := r.log.With(slog.String("plugin", run.Plugin), slog.String("run", run.ID.String()))
	if err := svc.Runs.Fail(ctx, run.ID, reason); err != nil && !errors.Is(err, plugrun.ErrNotRunning) {
		log.Error("run: failed to record failure", slog.Any("error", err))
	}
	if run.ChangesetID != nil && svc.Changesets != nil {
		if err := svc.Changesets.Discard(ctx, *run.ChangesetID); err != nil && !errors.Is(err, changeset.ErrNotStaged) {
			log.Error("run: failed to discard the changeset", slog.Any("error", err))
		}
	}
	log.Warn("run failed", slog.String("reason", reason))
}
