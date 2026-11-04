package admin

import (
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// runsRecent is how many recent runs the Runs tab lists.
const runsRecent = 10

// mountRunPages registers the Runs tab of a plugin.
func (a *Admin) mountRunPages(g *httpx.Group) {
	g.HandleFunc("GET /plugins/{name}/runs", a.handlePluginRuns)
}

// runHost is one tick of the "Shown by core" card.
type runHost struct {
	Host string
	// Fixed is the plugin's own host: always on, never untickable.
	Fixed bool
	// Unusual is a host the plugin's manifest does not list.
	Unusual bool
}

// runForm is the core-rendered start form of one declared run.
type runForm struct {
	Run        RunDecl
	Action     string
	InputNames string // space separated, read by the sealed frame's inputs op
	StartLabel string
	Hosts      []runHost
	Cancel     string
}

type pluginRunsData struct {
	pluginHead
	UI    *pluginUIData
	Form  *runForm
	Table ui.Table
}

// handlePluginRuns is the Runs tab: the plugin's sealed page, the hosts the
// owner lets the run reach (ticked by core, outside the frame) and the recent
// runs.
func (a *Admin) handlePluginRuns(w http.ResponseWriter, r *http.Request) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return
	}
	if a.deps.Growth.RunReader == nil {
		a.unavailable(w, r, "Runs")
		return
	}
	p, found := a.findPlugin(pa, r.PathValue("name"))
	if !found || len(p.Runs) == 0 {
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed, or it declares no runs.", a.adminPath+"/plugins", "Back to plugins")
		return
	}
	recent, err := a.deps.Growth.RunReader.Recent(r.Context(), p.Name, runsRecent)
	if err != nil {
		a.serverError(w, r, "The runs could not be loaded", err)
		return
	}
	d := pluginRunsData{pluginHead: a.newPluginHead(p, "runs", true), UI: a.pluginUIFor(r, p, "owner")}
	d.Form = a.runForm(p, recent)
	d.Table = a.runsTable(r, p, recent)
	v := a.NewView(w, r, p.Name+" runs")
	v.Width = "narrow"
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: p.Name, Href: a.pluginURL(p.Name, "settings")}, {Label: "Runs"}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/runs", v)
}

// runForm builds the start form of the plugin's first declared run. Its hosts
// are the manifest's allowed hosts (the first is fixed) plus any host an
// earlier run was given that the manifest does not list, shown as unusual.
func (a *Admin) runForm(p PluginInfo, recent []RunView) *runForm {
	run := p.Runs[0]
	f := &runForm{Run: run, Action: a.pluginURL(p.Name, "runs/"+run.Name+"/start"), Cancel: a.pluginURL(p.Name, "settings"),
		StartLabel: "Start " + strings.ToLower(run.Title)}
	if run.Title == "" {
		f.StartLabel = "Start run"
	}
	names := make([]string, 0, len(run.Inputs))
	for _, in := range run.Inputs {
		names = append(names, in.Name)
	}
	f.InputNames = strings.Join(names, " ")
	var seen []string
	add := func(h string, unusual bool) {
		if h == "" || slices.Contains(seen, h) {
			return
		}
		seen = append(seen, h)
		f.Hosts = append(f.Hosts, runHost{Host: h, Fixed: len(f.Hosts) == 0 && !unusual, Unusual: unusual})
	}
	for _, h := range p.AllowedHosts {
		add(h, false)
	}
	for _, rv := range recent {
		for _, h := range rv.Hosts {
			add(h, !slices.Contains(p.AllowedHosts, h))
		}
	}
	return f
}

// runStateBadge is the badge of a run's state.
func runStateBadge(v RunView) ui.Badge {
	switch {
	case v.State == "running":
		return ui.Badge{Label: "Running", Kind: "info"}
	case v.Reason == "cancelled":
		return ui.Badge{Label: "Cancelled", Kind: "muted"}
	case v.State == "failed":
		return ui.Badge{Label: "Failed", Kind: "danger"}
	}
	return ui.Badge{Label: "Finished", Kind: "success"}
}

// runTitle names a run for people: the plugin's title for it, else its name.
func runTitle(p PluginInfo, name string) string {
	for _, d := range p.Runs {
		if d.Name == name && d.Title != "" {
			return d.Title
		}
	}
	return name
}

func (a *Admin) runsTable(r *http.Request, p PluginInfo, recent []RunView) ui.Table {
	loc := a.siteLocation(r.Context())
	t := ui.Table{
		ID: "runs", Caption: "Recent runs of " + p.Name,
		Columns: []ui.Col{{Key: "run", Label: "Run", Role: "title"}, {Key: "state", Label: "State"}, {Key: "started", Label: "Started"}},
		Empty:   ui.Empty{Icon: "clock", Title: "No runs yet.", Body: "Runs you start here are listed with their state."},
		BaseURL: a.pluginURL(p.Name, "runs"),
	}
	for _, rv := range recent {
		b := runStateBadge(rv)
		href := a.pluginURL(p.Name, "runs/"+rv.ID.String())
		row := ui.Row{ID: rv.ID.String(), Label: runTitle(p, rv.Name), Cells: []ui.Cell{
			{Text: runTitle(p, rv.Name), Link: href}, {Badge: &b}, {Text: fmtDateTime(rv.CreatedAt.In(loc)), Nowrap: true},
		}}
		if rv.ChangesetID != 0 && rv.State != "running" && rv.State != "failed" {
			row.Actions = []ui.Action{{Label: "Review", Href: a.changesetURL(p.Name, rv.ChangesetID, ""), Button: true}}
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}
