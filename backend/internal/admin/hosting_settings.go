package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

const hostingPath = "/settings/hosting"

// ingestDocsURL explains how to send CDN or server access logs to Hatamoto.
const ingestDocsURL = "https://hatamoto.dev/docs/ai-seo/access-log-ingest"

func (a *Admin) mountHostingSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+hostingPath, a.handleHosting)
}

// hostingPage is the Settings > Hosting page. There is deliberately no secret
// key value anywhere in it.
type hostingPage struct {
	Errs    map[string]string
	Summary []ui.FieldError

	Static         bool // the saved mode
	ModeCards      []ui.RadioCardItem
	TargetOptions  []ui.Option
	ServiceOptions []ui.Option
	Form           HostingView // the saved values, or the posted ones on a refused save

	SiteURL string // "" links to the site address setting instead
	Islands ui.Table
	Stays   string

	IslandsWarning string
	CrawlersWarn   bool
	IngestDocs     string

	Last string
	Log  ui.Table
	Zone string
}

func (a *Admin) handleHosting(w http.ResponseWriter, r *http.Request) {
	a.renderHosting(w, r, http.StatusOK, nil, nil)
}

// renderHosting shows the page. form is the posted input to show again (nil
// shows the saved values); its secret key is never read.
func (a *Admin) renderHosting(w http.ResponseWriter, r *http.Request, status int, form *HostingInput, errs []ui.FieldError) {
	svc := a.deps.Hosting
	if svc == nil {
		a.unavailable(w, r, "Hosting")
		return
	}
	ctx := r.Context()
	saved, err := svc.View(ctx)
	if err == nil {
		var runs []HostingRun
		if runs, err = svc.Runs(ctx, 20); err == nil {
			live, stays := svc.Islands()
			v := a.NewView(w, r, "Hosting")
			v.Section, v.Tab, v.Width = "hosting", "hosting", "narrow"
			v.Data = a.hostingPage(r, saved, form, errs, runs, live, stays)
			a.Render(w, r, status, "settings/hosting", v)
			return
		}
	}
	a.logger().Error("hosting settings", "err", err)
	a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The hosting settings could not be loaded.")
}

func (a *Admin) hostingPage(r *http.Request, saved HostingView, form *HostingInput, errs []ui.FieldError, runs []HostingRun, live []HostingIsland, stays []string) hostingPage {
	loc := a.siteLocation(r.Context())
	f := saved
	if form != nil {
		f = HostingView{
			Mode: form.Mode, Target: form.Target, Dir: form.Dir, Service: form.Service, Endpoint: form.Endpoint,
			Bucket: form.Bucket, Prefix: form.Prefix, Region: form.Region, AccessKey: form.AccessKey,
			LiveOrigin: form.LiveOrigin, SecretSaved: saved.SecretSaved, SecretLost: saved.SecretLost,
		}
	}
	if f.Mode == "" {
		f.Mode = HostingServed
	}
	if f.Target == "" {
		f.Target = HostingTargetDir
	}
	if f.Service == "" {
		f.Service = OffsiteAWS
	}
	static := saved.Mode == HostingStatic
	p := hostingPage{
		Errs: map[string]string{}, Summary: errs, Static: static, Form: f,
		SiteURL: a.siteAddress(r), Zone: loc.String(), IngestDocs: ingestDocsURL,
		ModeCards: []ui.RadioCardItem{
			{Value: HostingServed, Title: "Served by Hatamoto", Description: "Pages are rendered by this server for every visitor.", Checked: f.Mode == HostingServed, Tag: &ui.Badge{Label: "Default", Kind: "neutral"}},
			{Value: HostingStatic, Title: "Static files + live islands", Description: "Pages are published as files. Forms, search and comments call this server.", Checked: f.Mode == HostingStatic},
		},
		TargetOptions: []ui.Option{
			{Value: HostingTargetDir, Label: "Local directory", Selected: f.Target == HostingTargetDir},
			{Value: HostingTargetS3, Label: "S3-compatible storage", Selected: f.Target == HostingTargetS3},
		},
		CrawlersWarn: static && !a.deps.AgentLogIngest,
	}
	for _, s := range offsiteServices {
		p.ServiceOptions = append(p.ServiceOptions, ui.Option{Value: s.v, Label: s.label, Selected: f.Service == s.v})
	}
	for _, e := range errs {
		p.Errs[e.ID] = e.Message
	}
	if static {
		p.IslandsWarning = saved.IslandsWarning
	}
	p.Islands = ui.Table{
		ID: "hosting-islands", Caption: "Live islands",
		Columns: []ui.Col{{Key: "island", Label: "Island", Role: "title"}, {Key: "route", Label: "Route"}, {Key: "status", Label: "Status"}},
		Empty:   ui.Empty{Icon: "layers", Title: "No live islands.", Compact: true},
	}
	for _, i := range live {
		p.Islands.Rows = append(p.Islands.Rows, ui.Row{ID: i.Route, Label: i.Label, Cells: []ui.Cell{
			{Text: i.Label}, {Text: i.Route, Mono: true}, {Badge: &ui.Badge{Label: i.Status, Kind: "success"}},
		}})
	}
	if len(stays) > 0 {
		p.Stays = "Stays on Hatamoto: " + strings.Join(stays, ", ") + "."
	}
	p.Log = a.hostingLog(runs, loc)
	if last, ok := lastFinished(runs); ok {
		p.Last = hostingSummary(last)
	}
	return p
}

// lastFinished is the newest run that is not still running; runs are newest first.
func lastFinished(runs []HostingRun) (HostingRun, bool) {
	for _, r := range runs {
		if r.Result != HostingRunRunning {
			return r, true
		}
	}
	return HostingRun{}, false
}

// hostingSummary is the Last export line: "214 pages · 38 MB · 9 s · 0 errors · 2 minutes ago".
func hostingSummary(r HostingRun) string {
	return strings.Join([]string{
		countNoun(r.Pages, "page"), backupSize(r.Bytes), strconv.Itoa(int(r.Duration.Round(time.Second)/time.Second)) + " s",
		countNoun(r.Errors, "error"), agoText(time.Since(r.Started)),
	}, " · ")
}

func hostingBadge(result string) ui.Badge {
	switch result {
	case HostingRunOK:
		return ui.Badge{Label: "Done", Kind: "success"}
	case HostingRunFailed:
		return ui.Badge{Label: "Failed", Kind: "danger"}
	}
	return ui.Badge{Label: "Running", Kind: "info"}
}

func (a *Admin) hostingLog(runs []HostingRun, loc *time.Location) ui.Table {
	t := ui.Table{
		ID: "hosting-log", Caption: "Export log",
		Columns: []ui.Col{
			{Key: "time", Label: "Time"}, {Key: "trigger", Label: "Trigger", Role: "title"},
			{Key: "pages", Label: "Pages", Role: "num"}, {Key: "changed", Label: "Changed", Role: "num"}, {Key: "result", Label: "Result"},
		},
		Empty: ui.Empty{Icon: "layers", Title: "No exports yet.", Compact: true},
	}
	for _, r := range runs {
		b := hostingBadge(r.Result)
		when := fmtDateTime(r.Started.In(loc))
		t.Rows = append(t.Rows, ui.Row{ID: r.Started.UTC().Format(time.RFC3339Nano), Label: "Export " + when, Cells: []ui.Cell{
			{Text: when, Nowrap: true},
			{Text: r.Trigger, Notes: hostingNotes(r.FirstError)},
			{Text: strconv.Itoa(r.Pages), Nowrap: true}, {Text: strconv.Itoa(r.Changed), Nowrap: true},
			{Badge: &b},
		}})
	}
	return t
}

func hostingNotes(msg string) []string {
	if msg == "" {
		return nil
	}
	return []string{msg}
}
