package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/importjob"
	"hatamoto.dev/core/internal/wpimport"
)

// ImportService is importjob.Manager as the WordPress import wizard uses it.
// Nothing on it can lift the engine's private-host guard: that is a flag of
// the command line only.
type ImportService interface {
	Begin(ctx context.Context, actor importjob.Actor, src importjob.Source) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*importjob.Run, error)
	Recent(ctx context.Context, limit int) ([]importjob.Run, error)
	Start(ctx context.Context, id uuid.UUID, actor importjob.Actor, host string, o importjob.Options) error
	Resume(ctx context.Context, id uuid.UUID, actor importjob.Actor, host string, cred *importjob.Source) error
	Cancel(ctx context.Context, id uuid.UUID, actor importjob.Actor) error
}

const (
	importBase = "/settings/import/wordpress"
	// importBodyLimit bounds an import form: the export file and a little more.
	importBodyLimit = importjob.MaxWXRBytes + 1<<20
	// pollEvery is how often the progress panel asks again (htmx), and how
	// often a browser without JS reloads the page.
	pollSeconds = 3
	listCap     = 200
)

var importSteps = []string{"Source", "Scan report", "Options", "Run", "Report"}

func importStepList(current int) []ui.Step {
	out := make([]ui.Step, len(importSteps))
	for i, l := range importSteps {
		out[i] = ui.Step{Label: l, Current: i == current, Done: i < current}
	}
	return out
}

// limitImportBody caps what an import form may upload before anything reads
// it (the CSRF check parses the body). A declared size over the cap is
// refused with a clear message.
func limitImportBody(prefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, prefix+importBase) {
				if r.ContentLength > importBodyLimit {
					http.Error(w, fmt.Sprintf("The file is larger than %d MB. Export fewer content types, or import from the site's address instead.", importjob.MaxWXRBytes>>20), http.StatusRequestEntityTooLarge)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, importBodyLimit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (a *Admin) mountImport(authed *httpx.Group) {
	owner := authed.Group("", a.RequireRole("owner"))
	// The tab URL and the wizard base show the same source page.
	owner.HandleFunc("GET /settings/import", a.handleImportSource)
	owner.HandleFunc("GET /settings/import/wordpress", a.handleImportSource)
	owner.HandleFunc("POST /settings/import/wordpress", a.handleImportBegin)
	owner.HandleFunc("GET /settings/import/wordpress/{id}", a.handleImportRun)
	owner.HandleFunc("GET /settings/import/wordpress/{id}/options", a.handleImportOptions)
	owner.HandleFunc("POST /settings/import/wordpress/{id}/start", a.handleImportStart)
	owner.HandleFunc("POST /settings/import/wordpress/{id}/resume", a.handleImportResume)
	owner.HandleFunc("GET /settings/import/wordpress/{id}/cancel", a.handleImportCancelForm)
	owner.HandleFunc("POST /settings/import/wordpress/{id}/cancel", a.handleImportCancel)
}

func (a *Admin) importService(w http.ResponseWriter, r *http.Request) (ImportService, bool) {
	if a.deps.Import == nil {
		a.unavailable(w, r, "The WordPress import")
		return nil, false
	}
	return a.deps.Import, true
}

func importActor(r *http.Request) importjob.Actor {
	u := userFrom(r.Context())
	if u == nil {
		return importjob.Actor{}
	}
	id, _ := uuid.Parse(u.ID)
	return importjob.Actor{ID: id, Email: u.Email}
}

// loadRun reads the run named in the path; it answers the error page itself.
func (a *Admin) loadRun(w http.ResponseWriter, r *http.Request) (ImportService, *importjob.Run, bool) {
	svc, ok := a.importService(w, r)
	if !ok {
		return nil, nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Import not found", "There is no import with that address.", a.adminPath+importBase, "Back to the import")
		return nil, nil, false
	}
	run, err := svc.Get(r.Context(), id)
	if errors.Is(err, importjob.ErrNotFound) {
		a.ErrorBack(w, r, http.StatusNotFound, "Import not found", "There is no import with that address.", a.adminPath+importBase, "Back to the import")
		return nil, nil, false
	}
	if err != nil {
		a.logger().Error("import: load run failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The import could not be loaded.")
		return nil, nil, false
	}
	return svc, run, true
}

func (a *Admin) runURL(id uuid.UUID) string { return a.adminPath + importBase + "/" + id.String() }

// ---- step 1: source ----

type importRunRow struct {
	Href, Source, Started string
	Status                ui.Badge
}

// importTrend is one block per earlier run, oldest first (runs arrive newest
// first), coloured by how the run ended.
func importTrend(rows []importRunRow, runs []importjob.Run) ui.Tracker {
	t := ui.Tracker{Label: "Earlier imports, oldest first"}
	for i := len(runs) - 1; i >= 0; i-- {
		kind := "neutral"
		switch runs[i].Status {
		case importjob.StatusCompleted:
			kind = "success"
		case importjob.StatusFailed:
			kind = "danger"
		case importjob.StatusCanceled, importjob.StatusInterrupted:
			kind = "warning"
		}
		t.Blocks = append(t.Blocks, ui.TrackerBlock{Kind: kind, Tip: rows[i].Source + ", " + rows[i].Started + ": " + rows[i].Status.Label})
	}
	return t
}

type importSourceData struct {
	Steps   []ui.Step
	Kind    string
	URL     string
	User    string
	Errors  map[string]string
	Summary []ui.FieldError
	Recent  []importRunRow
	Trend   ui.Tracker
	Kinds   []ui.RadioCardItem
	MaxMB   int
}

func importStatusBadge(status string) ui.Badge {
	switch status {
	case importjob.StatusCompleted:
		return ui.Badge{Label: "Completed", Kind: "success"}
	case importjob.StatusFailed:
		return ui.Badge{Label: "Failed", Kind: "danger"}
	case importjob.StatusCanceled:
		return ui.Badge{Label: "Stopped", Kind: "warning"}
	case importjob.StatusInterrupted:
		return ui.Badge{Label: "Interrupted", Kind: "warning"}
	case importjob.StatusRunning:
		return ui.Badge{Label: "Importing", Kind: "info"}
	case importjob.StatusScanning:
		return ui.Badge{Label: "Scanning", Kind: "info"}
	}
	return ui.Badge{Label: "Scanned", Kind: "info"}
}

func runSource(run importjob.Run) string {
	if run.Kind == importjob.KindWXR {
		if run.File != "" {
			return "File " + run.File
		}
		return "Export file"
	}
	return run.URL
}

func (a *Admin) renderImportSource(w http.ResponseWriter, r *http.Request, svc ImportService, status int, d importSourceData) {
	d.Steps, d.MaxMB = importStepList(0), importjob.MaxWXRBytes>>20
	if d.Kind == "" {
		d.Kind = importjob.KindREST
	}
	if runs, err := svc.Recent(r.Context(), 10); err == nil {
		for _, run := range runs {
			d.Recent = append(d.Recent, importRunRow{Href: a.runURL(run.ID), Source: runSource(run), Started: fmtDateTime(run.Started.UTC()), Status: importStatusBadge(run.Status)})
		}
		d.Trend = importTrend(d.Recent, runs)
	}
	d.Kinds = []ui.RadioCardItem{
		{Value: importjob.KindREST, Title: "From the site's address", Description: "Recommended. Includes menus, settings and, with an application password, drafts.", Checked: d.Kind == importjob.KindREST, Tag: &ui.Badge{Label: "Recommended", Kind: "info", Size: "sm"}},
		{Value: importjob.KindWXR, Title: "From an export file", Description: "A file from Tools, Export, All content in WordPress.", Checked: d.Kind == importjob.KindWXR},
	}
	v := a.NewView(w, r, "Import from WordPress")
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "import/source", v)
}

func (a *Admin) handleImportSource(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importService(w, r)
	if !ok {
		return
	}
	d := importSourceData{}
	// Re-sync: the address and user of an earlier run, never its password.
	if from, err := uuid.Parse(r.URL.Query().Get("from")); err == nil {
		if run, err := svc.Get(r.Context(), from); err == nil {
			d.Kind, d.URL, d.User = run.Kind, run.URL, run.User
		}
	}
	a.renderImportSource(w, r, svc, http.StatusOK, d)
}

func (a *Admin) handleImportBegin(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importService(w, r)
	if !ok {
		return
	}
	if err := parseImportUpload(w, r); err != nil {
		msg := uploadErrorMessage(err)
		a.renderImportSource(w, r, svc, http.StatusUnprocessableEntity, importSourceData{Errors: map[string]string{"wxr": msg}, Summary: []ui.FieldError{{ID: "wxr", Label: "Export file", Message: msg}}})
		return
	}
	src := importjob.Source{
		Kind: r.PostFormValue("kind"), URL: r.PostFormValue("url"), User: strings.TrimSpace(r.PostFormValue("user")),
		Password: r.PostFormValue("password"),
	}
	if src.Kind == importjob.KindWXR {
		if f, hdr, err := r.FormFile("wxr"); err == nil {
			defer f.Close()
			src.WXR, src.WXRName = f, hdr.Filename
		}
	}
	id, err := svc.Begin(r.Context(), importActor(r), src)
	var verr *importjob.ValidationError
	if errors.As(err, &verr) {
		// The password is never sent back to the browser.
		d := importSourceData{Kind: src.Kind, URL: src.URL, User: src.User, Errors: map[string]string{verr.Field: verr.Message}}
		d.Summary = []ui.FieldError{{ID: fieldID(verr.Field), Label: fieldLabel(verr.Field), Message: verr.Message}}
		a.renderImportSource(w, r, svc, http.StatusUnprocessableEntity, d)
		return
	}
	if err != nil {
		a.logger().Error("import: begin failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The import could not be started.")
		return
	}
	http.Redirect(w, r, a.runURL(id), http.StatusSeeOther)
}

// importUploadWindow is how long an upload may take: the server's read timeout
// is meant for ordinary requests, not for a 256 MB export on a slow line.
const importUploadWindow = 15 * time.Minute

// parseImportUpload reads the multipart form (a plain form is fine too).
func parseImportUpload(w http.ResponseWriter, r *http.Request) error {
	// Not every ResponseWriter can extend the deadline (tests); the default stays then.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(importUploadWindow))
	if err := r.ParseMultipartForm(8 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return err
	}
	return nil
}

// uploadErrorMessage tells "too large" from "could not be read".
func uploadErrorMessage(err error) string {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return "The file is larger than the limit. Export fewer content types, or import from the site's address instead."
	}
	return "The upload could not be read. Check the connection and try again."
}

func fieldID(f string) string {
	if f == "kind" {
		return "kind-0"
	}
	return f
}

func fieldLabel(f string) string {
	switch f {
	case "url":
		return "Site address"
	case "user":
		return "Username"
	case "password":
		return "Application password"
	case "wxr":
		return "Export file"
	}
	return "Source"
}

// ---- the run page: scanning, scan report, importing, report ----

type importProgressData struct {
	Steps    []ui.Step
	Run      *importjob.Run
	Scanning bool
	Title    string
	Phase    string
	Value    int
	Max      int
	Progress ui.Progress
	Tracker  ui.Tracker
	Log      []string
	PollURL  string
	CancelTo string
}

var phaseLabels = map[string]string{
	"read": "Reading the source", "settings": "Site settings", "schemas": "Content types", "media": "Media files", "authors": "Authors",
	"terms": "Categories and tags", "content": "Pages and posts", "links": "Internal links", "menus": "Menus", "redirects": "Redirects",
	"finish": "Final checks", "audit": "Checking every old URL",
}

func (a *Admin) handleImportRun(w http.ResponseWriter, r *http.Request) {
	svc, run, ok := a.loadRun(w, r)
	if !ok {
		return
	}
	_ = svc
	// A poll that finds the run in another state moves the whole page on.
	if poll := r.URL.Query().Get("poll"); poll != "" && IsHTMX(r) && poll != run.Status {
		Redirect(w, r, a.runURL(run.ID))
		return
	}
	switch run.Status {
	case importjob.StatusScanning, importjob.StatusRunning:
		a.renderImportProgress(w, r, run)
	case importjob.StatusScanned:
		a.renderImportScan(w, r, run)
	default:
		a.renderImportReport(w, r, run)
	}
}

func (a *Admin) renderImportProgress(w http.ResponseWriter, r *http.Request, run *importjob.Run) {
	d := importProgressData{Run: run, Scanning: run.Status == importjob.StatusScanning, Max: len(importjob.Phases), Value: run.PhaseIndex,
		PollURL: a.runURL(run.ID) + "?poll=" + run.Status, CancelTo: a.runURL(run.ID) + "/cancel"}
	d.Steps = importStepList(3)
	d.Title = "Importing your site"
	if d.Scanning {
		d.Steps = importStepList(1)
		d.Title = "Scanning the old site"
		d.Max, d.Value = 1, 0
	}
	d.Phase = phaseLabels[run.Phase]
	if d.Phase == "" {
		d.Phase = "Starting"
	}
	if !d.Scanning {
		d.Progress = ui.Progress{Label: "Import progress", Value: strconv.Itoa(d.Value) + " of " + strconv.Itoa(d.Max) + ": " + d.Phase, Kind: "info"}
		if d.Max > 0 {
			d.Progress.Pct = float64(d.Value) / float64(d.Max) * 100
		}
		d.Tracker = ui.Tracker{Label: "Import steps"}
		for i, p := range importjob.Phases {
			kind, state := "neutral", "waiting"
			switch {
			case i+1 < run.PhaseIndex:
				kind, state = "success", "done"
			case i+1 == run.PhaseIndex:
				kind, state = "info", "in progress"
			}
			d.Tracker.Blocks = append(d.Tracker.Blocks, ui.TrackerBlock{Kind: kind, Tip: phaseLabels[p] + ": " + state})
		}
	}
	if n := len(run.Progress); n > 30 {
		d.Log = run.Progress[n-30:]
	} else {
		d.Log = run.Progress
	}
	v := a.NewView(w, r, d.Title)
	v.Refresh = pollSeconds
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "import/progress", v)
}

// ---- step 2: scan report ----

type countRow struct {
	Label string
	Value int
}

type noteRow struct{ Title, Reason, Link, Type string }

type importScanData struct {
	Steps        []ui.Step
	Run          *importjob.Run
	Rep          *wpimport.Report
	Existing     bool
	ModeTitle    string
	ModeText     string
	Stats        []ui.Stat
	Fidelity     ui.CategoryBar
	FidelityPct  int    // the headline percent shown in the fidelity card
	FidelityText string // "N of M items import without changes"; empty when nothing was scanned
	Counts       []countRow
	Permalink    string
	Detected     []countRow // unused label/value pairs are rendered by name below
	SEO          string
	Shortcodes   []countRow
	Low          []noteRow
	MoreLow      int
	OptionsURL   string
	StartURL     string
}

func seoName(s string) string {
	switch s {
	case "yoast":
		return "Yoast SEO"
	case "rankmath":
		return "Rank Math"
	case "seopress":
		return "SEOPress"
	case "aioseo":
		return "All in One SEO"
	case "":
		return ""
	}
	return s
}

// scanStats are the headline counts; scanCounts is the
// long tail (custom types, drafts, private posts, old sitemap addresses).
func scanStats(rep *wpimport.Report) []ui.Stat {
	stats := []ui.Stat{{Label: "Posts", Value: fmtNum(int64(rep.Posts))}, {Label: "Pages", Value: fmtNum(int64(rep.Pages))},
		{Label: "Media files", Value: fmtNum(int64(rep.Media))}, {Label: "Categories and tags", Value: fmtNum(int64(rep.Terms))}, {Label: "Authors", Value: fmtNum(int64(rep.Authors))}}
	if rep.Menus > 0 {
		stats = append(stats, ui.Stat{Label: "Menus", Value: fmtNum(int64(rep.Menus))})
	}
	return stats
}

// scanFidelityText is the sentence next to the fidelity percent. It is empty
// for a scan that found no items, which has no fidelity to state.
func scanFidelityText(f wpimport.Fidelity) string {
	total := f.High + f.Medium + f.Low
	if total == 0 {
		return ""
	}
	return strconv.Itoa(f.High) + " of " + strconv.Itoa(total) + " items import without changes"
}

// scanFidelity splits the scanned items by how faithfully they import.
func scanFidelity(f wpimport.Fidelity) ui.CategoryBar {
	seg := func(label, kind string, n int) ui.CategorySegment {
		return ui.CategorySegment{Label: label, Value: strconv.Itoa(n), Kind: kind, N: float64(n)}
	}
	return ui.CategoryBar{Label: "Items by expected fidelity", Segments: []ui.CategorySegment{
		seg("Without changes", "success", f.High), seg("Small losses", "warning", f.Medium), seg("Need a look", "danger", f.Low)}}
}

func scanCounts(rep *wpimport.Report) []countRow {
	var rows []countRow
	var types []string
	for t := range rep.CustomTypes {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		rows = append(rows, countRow{"Custom type " + t, rep.CustomTypes[t]})
	}
	return append(rows, countRow{"Drafts", rep.Drafts}, countRow{"Private posts", rep.Private}, countRow{"Addresses in the old sitemap", rep.OldURLs})
}

func (a *Admin) renderImportScan(w http.ResponseWriter, r *http.Request, run *importjob.Run) {
	rep := run.Report
	if rep == nil {
		a.ErrorBack(w, r, http.StatusInternalServerError, "Scan report missing", "The scan finished without a report. Scan again.", a.adminPath+importBase, "Back to the import")
		return
	}
	d := importScanData{Steps: importStepList(1), Run: run, Rep: rep, Existing: rep.SiteMode == wpimport.ModeExisting, Stats: scanStats(rep), Fidelity: scanFidelity(rep.Fidelity), FidelityPct: rep.Fidelity.Percent, FidelityText: scanFidelityText(rep.Fidelity), Counts: scanCounts(rep),
		SEO: seoName(rep.SEOPlugin), OptionsURL: a.runURL(run.ID) + "/options"}
	if d.Existing {
		d.ModeTitle = "This site already has content"
	} else {
		d.ModeTitle = "This site is empty"
	}
	d.ModeText = importModeText(d.Existing)
	var names []string
	for n := range rep.Shortcodes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d.Shortcodes = append(d.Shortcodes, countRow{"[" + n + "]", rep.Shortcodes[n]})
	}
	for i, n := range rep.LowFidelity {
		if i >= listCap {
			d.MoreLow = len(rep.LowFidelity) - listCap
			break
		}
		d.Low = append(d.Low, noteRow{Title: n.Title, Reason: n.Reason, Type: n.Type})
	}
	v := a.NewView(w, r, "Scan report")
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "import/scan", v)
}

// importModeText explains the two ways an import treats the target site.
func importModeText(existing bool) string {
	if existing {
		return "Your Hatamoto site already has published content, so the import changes nothing about how it works: your URL patterns, site settings and home page stay as they are. " +
			"Imported pages and posts follow your patterns, and every old WordPress address that moves gets a permanent (301) redirect. " +
			"If an imported item's address is already taken by one of your pages, your page keeps it and the import is renamed (about-2)."
	}
	return "Your Hatamoto site has no content of its own yet (only the first-run home page and starter content), so the import can set the site up like the WordPress site: " +
		"its permalink structure becomes your URL pattern for posts and pages, and the title, tagline, time zone, language and home page are copied. Most old addresses keep working without a redirect."
}

// ---- step 3: options ----

type optionRow struct {
	ID, Name, Label, Value string
	Checked                bool
}

type importOptionsData struct {
	Steps    []ui.Step
	Run      *importjob.Run
	Types    []optionRow
	Drafts   bool
	Menus    bool
	Media    bool
	Edited   bool
	Existing bool
	Patterns []ui.RadioCardItem
	Pattern  string // the WordPress structure as a URL pattern ("" when none)
	Error    string
	Action   string
	Back     string
}

func (a *Admin) renderImportOptions(w http.ResponseWriter, r *http.Request, status int, run *importjob.Run, o importjob.Options, errMsg string) {
	rep := run.Report
	if rep == nil {
		a.ErrorBack(w, r, http.StatusConflict, "Scan first", "This import has no scan report yet.", a.runURL(run.ID), "Back to the import")
		return
	}
	existing := rep.SiteMode == wpimport.ModeExisting
	d := importOptionsData{Steps: importStepList(2), Run: run, Drafts: o.Drafts, Menus: o.Menus, Media: o.Media, Edited: o.KeepEdited,
		Existing: existing, Pattern: rep.PermalinkPattern, Error: errMsg, Action: a.runURL(run.ID) + "/start", Back: a.runURL(run.ID)}
	want := map[string]bool{}
	for _, t := range o.Types {
		want[t] = true
	}
	all := o.Types == nil
	add := func(slug, label string, n int) {
		if n == 0 {
			return
		}
		d.Types = append(d.Types, optionRow{ID: "type-" + slug, Name: "types", Label: fmt.Sprintf("%s (%d)", label, n), Value: slug, Checked: all || want[slug]})
	}
	add("post", "Posts", rep.Posts)
	add("page", "Pages", rep.Pages)
	var custom []string
	for t := range rep.CustomTypes {
		custom = append(custom, t)
	}
	sort.Strings(custom)
	for _, t := range custom {
		add(t, "Custom type "+t, rep.CustomTypes[t])
	}
	keep := o.KeepPatterns || existing || rep.PermalinkPattern == ""
	d.Patterns = []ui.RadioCardItem{
		{Value: "wordpress", Title: "Use the WordPress URL structure", Description: "Most old addresses keep working as they are.", Checked: !keep},
		{Value: "keep", Title: "Keep this site's URL patterns", Description: "Every moved address gets a 301 redirect.", Checked: keep},
	}
	v := a.NewView(w, r, "Import options")
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "import/options", v)
}

func (a *Admin) handleImportOptions(w http.ResponseWriter, r *http.Request) {
	_, run, ok := a.loadRun(w, r)
	if !ok {
		return
	}
	if run.Status != importjob.StatusScanned {
		http.Redirect(w, r, a.runURL(run.ID), http.StatusSeeOther)
		return
	}
	a.renderImportOptions(w, r, http.StatusOK, run, run.Options, "")
}

func (a *Admin) handleImportStart(w http.ResponseWriter, r *http.Request) {
	svc, run, ok := a.loadRun(w, r)
	if !ok {
		return
	}
	if run.Report == nil {
		http.Redirect(w, r, a.runURL(run.ID), http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	valid := map[string]bool{"post": run.Report.Posts > 0, "page": run.Report.Pages > 0}
	for t, n := range run.Report.CustomTypes {
		valid[t] = n > 0
	}
	types := []string{}
	for _, t := range r.PostForm["types"] {
		if valid[t] {
			types = append(types, t)
		}
	}
	existing := run.Report.SiteMode == wpimport.ModeExisting
	o := importjob.Options{
		Types: types, Drafts: r.PostFormValue("drafts") != "", Menus: r.PostFormValue("menus") != "", Media: r.PostFormValue("media") != "",
		KeepPatterns: r.PostFormValue("patterns") == "keep" || existing, KeepEdited: r.PostFormValue("keep_edited") != "",
	}
	if len(types) == 0 {
		a.renderImportOptions(w, r, http.StatusUnprocessableEntity, run, o, "Choose at least one kind of content to import.")
		return
	}
	err := svc.Start(r.Context(), run.ID, importActor(r), r.Host, o)
	if a.importActionFailed(w, r, run.ID, err) {
		return
	}
	http.Redirect(w, r, a.runURL(run.ID), http.StatusSeeOther)
}

// importActionFailed answers a Start, Resume or Cancel error; it reports
// whether it did.
func (a *Admin) importActionFailed(w http.ResponseWriter, r *http.Request, id uuid.UUID, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, importjob.ErrBusy), errors.Is(err, importjob.ErrState):
		// Someone (or another tab) already moved it on: show where it is.
		a.SetFlash(w, r, "info", "That import has already moved on. This is where it is now.")
		http.Redirect(w, r, a.runURL(id), http.StatusSeeOther)
	case errors.Is(err, importjob.ErrImportRunning):
		a.SetFlash(w, r, "error", "Another import is running. Wait for it to finish, then try again.")
		http.Redirect(w, r, a.runURL(id), http.StatusSeeOther)
	case errors.Is(err, importjob.ErrNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Import not found", "There is no import with that address.", a.adminPath+importBase, "Back to the import")
	default:
		var verr *importjob.ValidationError
		if errors.As(err, &verr) {
			a.SetFlash(w, r, "error", verr.Message)
			http.Redirect(w, r, a.runURL(id), http.StatusSeeOther)
			return true
		}
		a.logger().Error("import: action failed", "run", id, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The import could not be changed.")
	}
	return true
}

func (a *Admin) handleImportResume(w http.ResponseWriter, r *http.Request) {
	svc, run, ok := a.loadRun(w, r)
	if !ok {
		return
	}
	if err := parseImportUpload(w, r); err != nil {
		a.SetFlash(w, r, "error", uploadErrorMessage(err))
		http.Redirect(w, r, a.runURL(run.ID), http.StatusSeeOther)
		return
	}
	var cred *importjob.Source
	if pw := r.PostFormValue("password"); pw != "" {
		cred = &importjob.Source{Password: pw}
	}
	if f, hdr, err := r.FormFile("wxr"); err == nil {
		defer f.Close()
		if cred == nil {
			cred = &importjob.Source{}
		}
		cred.WXR, cred.WXRName = f, hdr.Filename
	}
	err := svc.Resume(r.Context(), run.ID, importActor(r), r.Host, cred)
	if a.importActionFailed(w, r, run.ID, err) {
		return
	}
	http.Redirect(w, r, a.runURL(run.ID), http.StatusSeeOther)
}

// handleImportCancelForm asks before stopping: the Stop button is a link here.
func (a *Admin) handleImportCancelForm(w http.ResponseWriter, r *http.Request) {
	_, run, ok := a.loadRun(w, r)
	if !ok {
		return
	}
	id := run.ID
	if run.Status != importjob.StatusRunning && run.Status != importjob.StatusScanning {
		http.Redirect(w, r, a.runURL(id), http.StatusSeeOther)
		return
	}
	a.renderConfirm(w, r, confirmData{Title: "Stop the import?", Message: "What was already imported stays, and you can resume it.",
		Post: a.runURL(id) + "/cancel", Label: "Stop the import", Back: a.runURL(id), Danger: true})
}

func (a *Admin) handleImportCancel(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.importService(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Import not found", "There is no import with that address.", a.adminPath+importBase, "Back to the import")
		return
	}
	if a.importActionFailed(w, r, id, svc.Cancel(r.Context(), id, importActor(r))) {
		return
	}
	a.SetFlash(w, r, "info", "Stopping the import. What was already imported stays, and you can resume it.")
	http.Redirect(w, r, a.runURL(id), http.StatusSeeOther)
}

// ---- step 5: report ----

type issueRow struct{ Kind, Item, Message string }

type linkRow struct {
	Path, Note string
	Link       string // a path on this site, "" when it is not safe to link
}

type importReportData struct {
	Steps     []ui.Step
	Run       *importjob.Run
	Res       *wpimport.Result
	Badge     ui.Badge
	Failed    bool // stopped before the end
	Resumable bool
	Title     string
	Summary   []countRow
	Stats     []ui.Stat
	AuditBar  ui.CategoryBar
	Errors    []issueRow
	MoreErrs  int
	Warnings  []string
	Conflicts []linkRow
	Edits     []string // overwritten or kept edits, with a lead-in
	EditsNote string
	Left      []string
	Redirects []linkRow
	Audit     *wpimport.AuditResult
	AuditBad  []linkRow
	MoreAudit int
	// AuditHidden are old author pages kept 404 until the person is public.
	AuditHidden []linkRow
	Low         []lowRow
	MoreLow     int
	SiteMode    string
	ResyncURL   string
	ResumeTo    string
	CSRFHint    bool
}

type lowRow struct {
	Title, Reason, Edit string
}

// sitePath returns p when it is a path on this site that is safe to link to.
func sitePath(p string) string {
	if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.ContainsAny(p, "\\\r\n") {
		return p
	}
	return ""
}

func (a *Admin) renderImportReport(w http.ResponseWriter, r *http.Request, run *importjob.Run) {
	d := importReportData{Steps: importStepList(4), Run: run, Res: run.Result, Badge: importStatusBadge(run.Status),
		ResyncURL: a.adminPath + importBase + "?from=" + run.ID.String(), ResumeTo: a.runURL(run.ID) + "/resume"}
	d.Failed = run.Status != importjob.StatusCompleted
	d.Resumable = d.Failed
	d.Title = "Import report"
	if res := run.Result; res != nil {
		d.SiteMode = string(res.SiteMode)
		d.Summary = []countRow{{"Pages and posts created", res.Created}, {"Pages and posts updated", res.Updated}, {"Pages and posts that failed", res.Failed},
			{"Categories and tags", res.Terms}, {"Authors", res.Authors}, {"Media files imported", res.MediaImported}, {"Media files reused", res.MediaReused},
			{"Media files that failed", res.MediaFailed}, {"Menus", res.Menus}, {"Redirects", res.Redirects}, {"Internal links rewritten", res.LinksRewritten}}
		d.Stats = []ui.Stat{{Label: "Created", Value: fmtNum(int64(res.Created))}, {Label: "Updated", Value: fmtNum(int64(res.Updated))},
			{Label: "Failed", Value: fmtNum(int64(res.Failed))}, {Label: "Redirects", Value: fmtNum(int64(res.Redirects))}}
		for i, e := range res.Errors {
			if i >= listCap {
				d.MoreErrs = len(res.Errors) - listCap
				break
			}
			item := e.Title
			if item == "" {
				item = e.ID
			}
			d.Errors = append(d.Errors, issueRow{Kind: e.Kind, Item: item, Message: e.Err})
		}
		d.Warnings = res.Warnings
		for _, c := range res.URLConflicts {
			d.Conflicts = append(d.Conflicts, linkRow{Path: c.URL, Link: sitePath(c.URL), Note: c.Reason})
		}
		switch {
		case len(res.OverwrittenEdits) > 0:
			d.Edits, d.EditsNote = res.OverwrittenEdits, "These items were edited here after the last import and were overwritten by this run:"
		case len(res.KeptEdits) > 0:
			d.Edits, d.EditsNote = res.KeptEdits, "These items were edited here after the last import and were left alone:"
		}
		d.Left = res.WPContentLeft
		d.Audit = res.Audit
		if au := res.Audit; au != nil {
			for _, f := range append(append([]wpimport.AuditFailure(nil), au.Failures...), au.Unresolved...) {
				if len(d.AuditBad) >= listCap {
					d.MoreAudit++
					continue
				}
				d.AuditBad = append(d.AuditBad, linkRow{Path: f.URL, Link: sitePath(f.URL), Note: f.Reason})
			}
			broken := float64(len(d.AuditBad) + d.MoreAudit)
			d.AuditBar = ui.CategoryBar{Label: "Old addresses by result", Segments: []ui.CategorySegment{
				{Label: "Answer directly", Value: fmtNum(int64(au.OK)), Kind: "success", N: float64(au.OK)},
				{Label: "One redirect", Value: fmtNum(int64(au.Redirected)), Kind: "info", N: float64(au.Redirected)},
				{Label: "Do not resolve", Value: fmtNum(int64(broken)), Kind: "danger", N: broken}}}
			for _, f := range au.Hidden {
				d.AuditHidden = append(d.AuditHidden, linkRow{Path: f.URL, Note: f.Reason})
			}
		}
		for _, rd := range run.Redirects {
			d.Redirects = append(d.Redirects, linkRow{Path: rd.From, Link: sitePath(rd.From), Note: fmt.Sprintf("%d to %s", rd.Code, rd.To)})
		}
		if rep := run.Report; rep != nil {
			for i, n := range rep.LowFidelity {
				if i >= listCap {
					d.MoreLow = len(rep.LowFidelity) - listCap
					break
				}
				row := lowRow{Title: n.Title, Reason: n.Reason}
				if id, ok := run.EntityIDs[n.ID]; ok {
					row.Edit = a.adminPath + "/entities/" + id.String() + "/edit"
				}
				d.Low = append(d.Low, row)
			}
		}
	}
	v := a.NewView(w, r, d.Title)
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "import/report", v)
}
