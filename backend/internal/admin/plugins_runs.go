package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// What RunStarter.StartRun answers when the owner's start cannot go ahead.
var (
	// ErrRunActive means the plugin already has a run in progress.
	ErrRunActive = errors.New("plugin already has an active run")
	// ErrRunNotFound means the plugin is not loaded or does not declare the run.
	ErrRunNotFound = errors.New("plugin or run not found")
	// ErrRunsUnavailable means this server cannot run plugin runs.
	ErrRunsUnavailable = errors.New("runs are not available")
)

// RunInputError says which of the owner's inputs was refused.
type RunInputError struct{ Msg string }

func (e *RunInputError) Error() string { return e.Msg }

// StartRunInput is what the owner supplies when starting a run.
type StartRunInput struct {
	// Inputs are the declared inputs of the run, by input name.
	Inputs map[string]string
	// Hosts are the hosts the run may fetch from.
	Hosts []string
	// UploadID is the owner's uploaded file, nil when none.
	UploadID *uuid.UUID
}

// RunStarter starts a plugin run. The plugin runtime implements it; the
// admin only validates who may call it.
type RunStarter interface {
	StartRun(ctx context.Context, plugin, run string, in StartRunInput) (uuid.UUID, error)
}

// runInputPrefix names the form fields that carry the run's declared inputs.
const runInputPrefix = "input_"

// handlePluginRunStart starts a plugin run for the owner. It is a POST only:
// the review of what the run staged lives elsewhere, and a run is never
// started by a link, a plugin or a schedule.
func (a *Admin) handlePluginRunStart(w http.ResponseWriter, r *http.Request) {
	if a.deps.Growth.PluginRuns == nil {
		a.unavailable(w, r, "Plugin runs")
		return
	}
	name, run := r.PathValue("name"), r.PathValue("run")
	back := a.adminPath + "/plugins"
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	in := StartRunInput{Inputs: map[string]string{}, Hosts: r.PostForm["hosts"]}
	for k, v := range r.PostForm {
		if field, ok := strings.CutPrefix(k, runInputPrefix); ok && len(v) > 0 {
			in.Inputs[field] = v[0]
		}
	}
	if raw := strings.TrimSpace(r.PostFormValue("upload_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			a.Error(w, r, http.StatusBadRequest, "Bad request", "The upload id is not valid.")
			return
		}
		in.UploadID = &id
	}
	runID, err := a.deps.Growth.PluginRuns.StartRun(r.Context(), name, run, in)
	var bad *RunInputError
	switch {
	case errors.Is(err, ErrRunActive):
		a.ErrorBack(w, r, http.StatusConflict, "Run already in progress", "That plugin already has a run in progress. Wait for it to end before starting another.", back, "Back to plugins")
		return
	case errors.Is(err, ErrRunNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Run not found", "That plugin or run does not exist.", back, "Back to plugins")
		return
	case errors.Is(err, ErrRunsUnavailable):
		a.unavailable(w, r, "Plugin runs")
		return
	case errors.As(err, &bad):
		a.ErrorBack(w, r, http.StatusBadRequest, "Run not started", bad.Msg, back, "Back to plugins")
		return
	case err != nil:
		a.logger().Error("start plugin run failed", "plugin", name, "run", run, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The run could not be started.")
		return
	}
	a.SetFlash(w, r, "success", "Started the "+run+" run of "+name+".")
	http.Redirect(w, r, a.pluginURL(name, "runs/"+runID.String()), http.StatusSeeOther)
}
