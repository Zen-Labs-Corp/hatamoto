package admin

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/httpx"
)

// ErrRunNotCancellable means the run has already ended.
var ErrRunNotCancellable = errors.New("run is not running")

// mountRunProgress registers the run progress page and its cancel action.
func (a *Admin) mountRunProgress(g *httpx.Group) {
	g.HandleFunc("GET /plugins/{name}/runs/{id}", a.handleRunProgress)
	g.HandleFunc("POST /plugins/{name}/runs/{id}/cancel", a.handleRunCancel)
}

// runRoom is one of the two rooms of a run: what it may reach and how far it is.
type runRoom struct {
	Title    string
	Badge    ui.Badge
	Active   bool
	Chips    []ui.Badge
	Progress ui.Progress
}

type runProgressData struct {
	Title    string
	Badge    ui.Badge
	Actions  []ui.Button
	Running  bool
	PollURL  string
	Fetch    runRoom
	Build    runRoom
	Message  string
	Limits   []runLimit
	Review   string // the way to the review page; "" until the run staged something
	RunsHref string
}

// runLimit is one line of the Limits card.
type runLimit struct{ Label, Value string }

// runLimits lists the limits that hold for a run, each from the value core
// enforces. The step limits come from the plugin runtime, so they are left out
// when the plugin is not loaded.
func (a *Admin) runLimits(rv RunView) []runLimit {
	var out []runLimit
	if rl := a.deps.Growth.RunLimits; rl != nil {
		if l, ok := rl.RunLimitsFor(rv.Plugin); ok {
			out = append(out,
				runLimit{"Each step", fmt.Sprintf("%s at most, then it is stopped", countNoun(int(l.StepTimeout.Round(time.Second)/time.Second), "second"))},
				runLimit{"Memory", fmt.Sprintf("%d MiB per instance", l.MemoryBytes>>20)})
		}
	}
	return append(out,
		runLimit{"Staged changes", thousands(rv.StagedItems) + " of " + thousands(changeset.MaxStagedItems)},
		runLimit{"Staging space", humanSize(rv.StagedBytes) + " of " + humanSize(changeset.MaxStagedBytes) + " for this run"},
		runLimit{"If abandoned", fmt.Sprintf("Deleted %s after it finishes if nobody publishes or discards it", countNoun(int(changeset.TTL/time.Hour), "hour"))})
}

// runPage loads the run named by the path for the plugin named by the path;
// any other plugin's run is "not found".
func (a *Admin) runPage(w http.ResponseWriter, r *http.Request) (RunView, bool) {
	if a.deps.Growth.RunReader == nil {
		a.unavailable(w, r, "Runs")
		return RunView{}, false
	}
	name := r.PathValue("name")
	back, label := a.pluginURL(name, "runs"), "Back to runs"
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Run not found", "That run does not exist.", back, label)
		return RunView{}, false
	}
	rv, err := a.deps.Growth.RunReader.Run(r.Context(), id)
	switch {
	case errors.Is(err, ErrRunNotFound), err == nil && rv.Plugin != name:
		a.ErrorBack(w, r, http.StatusNotFound, "Run not found", "That run does not exist.", back, label)
		return rv, false
	case err != nil:
		a.serverError(w, r, "The run could not be loaded", err)
		return rv, false
	}
	return rv, true
}

// handleRunProgress shows a run as two rooms: Fetch (the only room that may
// reach the internet) and Build (which sees the site, and nothing outside).
func (a *Admin) handleRunProgress(w http.ResponseWriter, r *http.Request) {
	rv, ok := a.runPage(w, r)
	if !ok {
		return
	}
	// A poll that finds the run in another state moves the whole page on.
	if poll := r.URL.Query().Get("poll"); poll != "" && IsHTMX(r) && poll != rv.State {
		Redirect(w, r, a.pluginURL(rv.Plugin, "runs/"+rv.ID.String()))
		return
	}
	var info PluginInfo
	if pa := a.deps.Growth.Plugins; pa != nil {
		info, _ = a.findPlugin(pa, rv.Plugin)
	}
	running := rv.State == "running"
	title := runTitle(info, rv.Name)
	if len(rv.Hosts) > 0 {
		title += " from " + rv.Hosts[0]
	}
	d := runProgressData{
		Title: title, Badge: runStateBadge(rv), Running: running, Message: rv.Message, RunsHref: a.pluginURL(rv.Plugin, "runs"),
		Limits: a.runLimits(rv),
	}
	runPath := a.pluginURL(rv.Plugin, "runs/"+rv.ID.String())
	if running {
		d.PollURL = runPath + "?poll=running"
		if a.deps.Growth.RunCancel != nil {
			d.Actions = []ui.Button{{Label: "Cancel run", Variant: "danger", Href: runPath, Attrs: map[string]string{
				"data-hm-confirm": "The run stops and everything it staged is thrown away. Nothing reaches the site.", "data-hm-confirm-label": "Cancel run",
				"data-hm-post": runPath + "/cancel", "data-hm-danger": "",
			}}}
		}
	}
	if rv.ChangesetID != 0 && !running && rv.State != "failed" {
		d.Review = a.changesetURL(rv.Plugin, rv.ChangesetID, "")
	}
	d.Fetch, d.Build = runRooms(rv)
	v := a.NewView(w, r, title)
	v.Width = "narrow"
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: rv.Plugin, Href: a.pluginURL(rv.Plugin, "settings")},
		{Label: "Runs", Href: d.RunsHref}, {Label: title}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/run", v)
}

// runRooms describes the two rooms of a run from its phase and progress.
func runRooms(rv RunView) (fetch, build runRoom) {
	running := rv.State == "running"
	inFetch := rv.Phase == "" || rv.Phase == "fetch"
	hosts := fmt.Sprintf("Internet: %d hosts", len(rv.Hosts))
	if len(rv.Hosts) == 1 {
		hosts = "Internet: 1 host"
	}
	fetch = runRoom{Title: "1. Fetch", Chips: []ui.Badge{{Label: hosts, Kind: "success"}, {Label: "Sees your site: no", Kind: "muted"}}}
	build = runRoom{Title: "2. Build", Chips: []ui.Badge{{Label: "Internet: none", Kind: "muted"}, {Label: "Sees your site: published only", Kind: "success"}, {Label: "Plugin notes: locked", Kind: "muted"}}}
	step := func(label string) ui.Progress {
		p := ui.Progress{Label: label, Thin: true, Kind: "info"}
		if rv.Total > 0 {
			p.Pct = float64(rv.Done) / float64(rv.Total) * 100
			p.Value = fmt.Sprintf("Step %s of about %s", thousands(rv.Done), thousands(rv.Total))
		} else {
			p.Value = "Starting"
		}
		return p
	}
	full := func(label string) ui.Progress {
		return ui.Progress{Label: label, Value: "Done", Pct: 100, Thin: true, Kind: "info"}
	}
	idle := func(label string) ui.Progress {
		return ui.Progress{Label: label, Value: "Waiting", Thin: true, Kind: "info"}
	}
	switch {
	case running && inFetch:
		fetch.Active, fetch.Badge, fetch.Progress = true, ui.Badge{Label: "Running", Kind: "info"}, step("Fetch progress")
		build.Badge, build.Progress = ui.Badge{Label: "Waiting", Kind: "muted"}, idle("Build progress")
	case running:
		fetch.Badge, fetch.Progress = ui.Badge{Label: "Done", Kind: "success"}, full("Fetch progress")
		build.Active, build.Badge, build.Progress = true, ui.Badge{Label: "Running", Kind: "info"}, step("Build progress")
	case rv.State == "failed":
		stopped := ui.Badge{Label: "Stopped", Kind: "muted"}
		if inFetch {
			fetch.Badge, fetch.Progress = stopped, step("Fetch progress")
			build.Badge, build.Progress = ui.Badge{Label: "Not started", Kind: "muted"}, idle("Build progress")
		} else {
			fetch.Badge, fetch.Progress = ui.Badge{Label: "Done", Kind: "success"}, full("Fetch progress")
			build.Badge, build.Progress = stopped, step("Build progress")
		}
	default:
		fetch.Badge, fetch.Progress = ui.Badge{Label: "Done", Kind: "success"}, full("Fetch progress")
		build.Badge, build.Progress = ui.Badge{Label: "Done", Kind: "success"}, full("Build progress")
	}
	return fetch, build
}

// handleRunCancel stops a running run for the owner. The run's own plugin is
// checked by the canceller; this only maps its answer.
func (a *Admin) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	if a.deps.Growth.RunCancel == nil {
		a.unavailable(w, r, "Runs")
		return
	}
	name := r.PathValue("name")
	back, label := a.pluginURL(name, "runs"), "Back to runs"
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Run not found", "That run does not exist.", back, label)
		return
	}
	runPath := back + "/" + id.String()
	switch err := a.deps.Growth.RunCancel.CancelRun(r.Context(), name, id); {
	case errors.Is(err, ErrRunNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Run not found", "That run does not exist.", back, label)
		return
	case errors.Is(err, ErrRunNotCancellable):
		a.SetFlash(w, r, "warning", "That run has already ended.")
		http.Redirect(w, r, runPath, http.StatusSeeOther)
		return
	case err != nil:
		a.serverError(w, r, "The run could not be cancelled", err)
		return
	}
	a.audit(r, "plugin.run.cancel", id.String(), map[string]any{"plugin": name})
	a.SetFlash(w, r, "success", "Cancelled the run. Nothing it staged was kept.")
	http.Redirect(w, r, runPath, http.StatusSeeOther)
}
