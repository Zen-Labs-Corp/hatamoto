package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/sdk"
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

// maxRunInputValueBytes bounds one run input value; pluginhost checks the same after trimming.
const maxRunInputValueBytes = 4096

// runStartJSON is the answer of the JSON branch of the start route.
type runStartJSON struct {
	OK    bool   `json:"ok"`
	RunID string `json:"run_id,omitempty"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

// checkPageRunInputs refuses what a page could never send through a declared run:
// more inputs than a run may declare, or a value over the size cap.
func checkPageRunInputs(in map[string]string) string {
	if len(in) > sdk.MaxRunInputs {
		return fmt.Sprintf("more than %d inputs", sdk.MaxRunInputs)
	}
	for k, v := range in {
		if len(v) > maxRunInputValueBytes {
			return fmt.Sprintf("input %q is longer than %d bytes", k, maxRunInputValueBytes)
		}
	}
	return ""
}

// fixedRunHosts are the hosts a page-started run gets: the manifest's first
// allowed host, the one the Runs form always posts (runs_page.go). A page never picks hosts.
func fixedRunHosts(p PluginInfo) []string {
	if len(p.AllowedHosts) > 0 && p.AllowedHosts[0] != "" {
		return []string{p.AllowedHosts[0]}
	}
	return nil
}

// handlePluginRunStart starts a plugin run for the owner. It is a POST only:
// the review of what the run staged lives elsewhere, and a run is never
// started by a link, a plugin or a schedule. With Accept: application/json
// (a plugin page, through core's confirm) it answers JSON and the run gets only
// the manifest's first allowed host.
func (a *Admin) handlePluginRunStart(w http.ResponseWriter, r *http.Request) {
	if a.deps.Growth.PluginRuns == nil {
		a.unavailable(w, r, "Plugin runs")
		return
	}
	name, run := r.PathValue("name"), r.PathValue("run")
	js := wantsJSON(r)
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
	if msg := checkPageRunInputs(in.Inputs); msg != "" {
		if js {
			a.writeJSON(w, http.StatusBadRequest, runStartJSON{Error: msg})
			return
		}
		a.ErrorBack(w, r, http.StatusBadRequest, "Run not started", msg, back, "Back to plugins")
		return
	}
	if js {
		pa := a.deps.Growth.Plugins
		if pa == nil {
			a.writeJSON(w, http.StatusServiceUnavailable, runStartJSON{Error: "Plugin runs are unavailable."})
			return
		}
		p, ok := a.findPlugin(pa, name)
		if !ok || !slices.ContainsFunc(p.Runs, func(d RunDecl) bool { return d.Name == run }) {
			a.writeJSON(w, http.StatusNotFound, runStartJSON{Error: "That plugin or run does not exist."})
			return
		}
		in.Hosts, in.UploadID = fixedRunHosts(p), nil
	} else if raw := strings.TrimSpace(r.PostFormValue("upload_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			a.Error(w, r, http.StatusBadRequest, "Bad request", "The upload id is not valid.")
			return
		}
		in.UploadID = &id
	}
	runID, err := a.deps.Growth.PluginRuns.StartRun(r.Context(), name, run, in)
	var bad *RunInputError
	if js {
		a.answerRunStartJSON(w, name, run, runID, err)
		return
	}
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

// answerRunStartJSON maps StartRun's result to the JSON branch's answer, with
// the same statuses and sentences as the form branch.
func (a *Admin) answerRunStartJSON(w http.ResponseWriter, name, run string, runID uuid.UUID, err error) {
	var bad *RunInputError
	status, msg := http.StatusOK, ""
	switch {
	case err == nil:
		a.writeJSON(w, status, runStartJSON{OK: true, RunID: runID.String(), URL: a.pluginURL(name, "runs/"+runID.String())})
		return
	case errors.Is(err, ErrRunActive):
		status, msg = http.StatusConflict, "That plugin already has a run in progress. Wait for it to end before starting another."
	case errors.Is(err, ErrRunNotFound):
		status, msg = http.StatusNotFound, "That plugin or run does not exist."
	case errors.Is(err, ErrRunsUnavailable):
		status, msg = http.StatusServiceUnavailable, "Plugin runs are unavailable."
	case errors.As(err, &bad):
		status, msg = http.StatusBadRequest, bad.Msg
	default:
		a.logger().Error("start plugin run failed", "plugin", name, "run", run, "err", err)
		status, msg = http.StatusInternalServerError, "The run could not be started."
	}
	a.writeJSON(w, status, runStartJSON{Error: msg})
}
