package admin

import (
	"net/http"
	"net/url"
	"path/filepath"
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
	owner.HandleFunc("POST "+hostingPath, a.handleHostingSave)
	owner.HandleFunc("GET "+hostingPath+"/mode", a.handleHostingModeConfirm)
	owner.HandleFunc("POST "+hostingPath+"/export", a.handleHostingExport)
	// Test export is not behind the password, like Export now: it runs in the
	// background and never changes what visitors get.
	owner.HandleFunc("POST "+hostingPath+"/test", a.handleHostingTest)
	// Switching the mode and rebuilding everything ask for the password. A POST
	// is never replayed after that page: the owner clicks again on the page
	// they came from.
	sudo := owner.Group("", a.RequireSudo)
	sudo.HandleFunc("POST "+hostingPath+"/mode", a.handleHostingMode)
	sudo.HandleFunc("POST "+hostingPath+"/rebuild", a.handleHostingRebuild)
	// The host key is probed on a GET (it writes nothing); pinning it asks for the password.
	owner.HandleFunc("GET "+hostingPath+"/host-key", a.handleHostingHostKeyConfirm)
	sudo.HandleFunc("POST "+hostingPath+"/host-key", a.handleHostingTrustHostKey)
}

// hostingPage is the Settings > Hosting page. There is deliberately no secret
// key value anywhere in it.
type hostingPage struct {
	Errs    map[string]string
	Summary []ui.FieldError

	Static        bool // the saved mode
	ModeCards     []ui.RadioCardItem
	TargetOptions []ui.Option
	AuthOptions   []ui.Option // SFTP: password or private key
	// Live islands choice: one radio each, in one group (name live_mode).
	LiveSameOptions     []ui.Option
	LiveSeparateOptions []ui.Option
	// AdvancedOpen opens the separate live origin disclosure: it is chosen or its field has an error.
	AdvancedOpen   bool
	ServiceOptions []ui.Option
	Form           HostingView // the saved values, or the posted ones on a refused save

	SiteURL string // "" links to the site address setting instead
	Islands ui.Table
	Stays   string
	// IslandsDesc is the islands card text: in served mode nothing is live yet.
	IslandsDesc string

	IslandsWarning string
	// S3SameHost warns that a bucket cannot pass island paths to Hatamoto.
	S3SameHost   bool
	CrawlersWarn bool
	IngestDocs   string

	Setup hostingSetupView

	Last string
	Log  ui.Table
	// TestWhy and ExportWhy say why a button is disabled; "" means it is enabled.
	TestWhy   string
	ExportWhy string
	// TrustWhy says why Trust host key is disabled; "" means it is enabled.
	TrustWhy string
	Zone     string
}

func (a *Admin) handleHosting(w http.ResponseWriter, r *http.Request) {
	a.renderHosting(w, r, http.StatusOK, nil, nil)
}

// hostingTestStaticWhy is why Test export is off in static mode: it would write
// the live target, which is what Export now does and records as a live run.
const hostingTestStaticWhy = "In static mode, use Export now."

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
		var runs, liveRuns []HostingRun
		if runs, err = svc.Runs(ctx, 20); err == nil {
			liveRuns, err = svc.LiveRuns(ctx, 20)
		}
		if err == nil {
			live, stays := svc.Islands()
			// A setup that cannot be read is not a page error: the card says so.
			setup, serr := svc.Setup(ctx)
			if serr != nil {
				a.logger().Error("hosting setup", "err", serr)
				setup = HostingSetup{Saved: true, GenError: "The web server setup could not be loaded."}
			}
			v := a.NewView(w, r, "Hosting")
			v.Section, v.Tab, v.Width = "hosting", "hosting", "narrow"
			v.Data = a.hostingPage(r, saved, form, errs, runs, liveRuns, live, stays, setup)
			a.Render(w, r, status, "settings/hosting", v)
			return
		}
	}
	a.logger().Error("hosting settings", "err", err)
	a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The hosting settings could not be loaded.")
}

func (a *Admin) hostingPage(r *http.Request, saved HostingView, form *HostingInput, errs []ui.FieldError, runs, liveRuns []HostingRun, live []HostingIsland, stays []string, setup HostingSetup) hostingPage {
	loc := a.siteLocation(r.Context())
	f := saved
	if form != nil {
		f = HostingView{
			Mode: form.Mode, Target: form.Target, Dir: form.Dir, Service: form.Service, Endpoint: form.Endpoint,
			Bucket: form.Bucket, Prefix: form.Prefix, Region: form.Region, AccessKey: form.AccessKey,
			LiveOrigin: form.LiveOrigin, SecretSaved: saved.SecretSaved, SecretLost: saved.SecretLost,
			SFTPHost: form.SFTPHost, SFTPPort: form.SFTPPort, SFTPUser: form.SFTPUser, SFTPAuth: form.SFTPAuth,
			SFTPDir: form.SFTPDir, SFTPHTAccess: form.SFTPHTAccess,
			SFTPSecretSaved: saved.SFTPSecretSaved, SFTPSecretLost: saved.SFTPSecretLost, SFTPHostKey: saved.SFTPHostKey,
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
	if f.SFTPPort == "" {
		f.SFTPPort = "22"
	}
	if f.SFTPDir == "" {
		f.SFTPDir = "public_html"
	}
	if f.SFTPAuth == "" {
		f.SFTPAuth = HostingSFTPAuthPassword
	}
	static := saved.Mode == HostingStatic
	separate := f.LiveOrigin != ""
	if form != nil {
		separate = form.LiveMode == HostingLiveSeparate
	}
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
			{Value: HostingTargetSFTP, Label: "SFTP (cPanel / shared hosting)", Selected: f.Target == HostingTargetSFTP},
		},
		AuthOptions: []ui.Option{
			{Value: HostingSFTPAuthPassword, Label: "Password", Selected: f.SFTPAuth == HostingSFTPAuthPassword},
			{Value: HostingSFTPAuthKey, Label: "Private key", Selected: f.SFTPAuth == HostingSFTPAuthKey},
		},
		LiveSameOptions:     []ui.Option{{Value: HostingLiveSame, Label: "Same host", Selected: !separate}},
		LiveSeparateOptions: []ui.Option{{Value: HostingLiveSeparate, Label: "Use a separate live origin", Selected: separate}},
		CrawlersWarn:        static && !a.deps.AgentLogIngest,
		S3SameHost:          f.Mode == HostingStatic && f.Target == HostingTargetS3 && !separate,
	}
	for _, s := range offsiteServices {
		p.ServiceOptions = append(p.ServiceOptions, ui.Option{Value: s.v, Label: s.label, Selected: f.Service == s.v})
	}
	for _, e := range errs {
		p.Errs[e.ID] = e.Message
	}
	p.AdvancedOpen = separate || p.Errs["hosting-live-origin"] != ""
	if static {
		p.IslandsWarning = saved.IslandsWarning
	}
	switch {
	case static:
		p.TestWhy = hostingTestStaticWhy
	case !hostingTargetSaved(saved):
		p.TestWhy = "Save a target first."
	}
	switch {
	case saved.WorkerOff:
		p.ExportWhy = "The export worker is off on this server."
	case !static:
		p.ExportWhy = "Needs static mode. Use Test export first."
	}
	if saved.Target != HostingTargetSFTP || saved.SFTPHost == "" {
		p.TrustWhy = "Save an SFTP target first."
	}
	p.IslandsDesc = "These apply once the site is in static mode."
	p.Islands = ui.Table{
		ID: "hosting-islands", Caption: "Live islands",
		Columns: []ui.Col{{Key: "island", Label: "Island", Role: "title"}, {Key: "route", Label: "Route"}},
		Empty:   ui.Empty{Icon: "layers", Title: "No live islands.", Compact: true},
	}
	if static {
		p.IslandsDesc = "These parts of the site still call this server."
		p.Islands.Columns = append(p.Islands.Columns, ui.Col{Key: "status", Label: "Status"})
	}
	for _, i := range live {
		cells := []ui.Cell{{Text: i.Label}, {Text: i.Route, Mono: true}}
		if static {
			cells = append(cells, ui.Cell{Badge: &ui.Badge{Label: i.Status, Kind: "success"}})
		}
		p.Islands.Rows = append(p.Islands.Rows, ui.Row{ID: i.Route, Label: i.Label, Cells: cells})
	}
	if len(stays) > 0 {
		p.Stays = "Stays on Hatamoto: " + strings.Join(stays, ", ") + "."
	}
	p.Setup = hostingSetupFrom(setup, r.URL.Query().Get("setup"), a.adminPath, loc)
	p.Log = a.hostingLog(runs, loc)
	p.Last = hostingLast(liveRuns)
	return p
}

// hostingLast is the Last export line (without the trailing period). Runs are
// newest first; running and Test export runs are skipped. A failed newest run
// never reports its own size.
func hostingLast(runs []HostingRun) string {
	var newest *HostingRun
	for i := range runs {
		r := &runs[i]
		if r.Result == HostingRunRunning || r.Trigger == HostingTriggerTest {
			continue
		}
		if newest == nil {
			if r.Result == HostingRunOK {
				return hostingSummary(*r)
			}
			newest = r
			continue
		}
		if r.Result == HostingRunOK {
			return hostingFailed(*newest) + " · last good: " + hostingSummary(*r)
		}
	}
	if newest == nil {
		return ""
	}
	return hostingFailed(*newest)
}

func hostingFailed(r HostingRun) string {
	s := "Failed " + agoText(time.Since(r.Started))
	if e := strings.TrimRight(clip(r.FirstError, 160), ". "); e != "" {
		s += " · " + e
	}
	return s
}

func hostingTriggerLabel(trigger string) string {
	if trigger == HostingTriggerTest {
		return "Test export"
	}
	return trigger
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
			{Text: hostingTriggerLabel(r.Trigger), Notes: hostingNotes(r.FirstError)},
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

// hostingInputFrom reads the posted form. The secret key is taken as typed.
func hostingInputFrom(form url.Values) HostingInput {
	typed := func(k string) string { return strings.TrimSpace(form.Get(k)) }
	in := HostingInput{
		Mode: typed("mode"), Target: typed("target"), Dir: typed("dir"), Service: typed("service"),
		Endpoint: typed("endpoint"), Bucket: typed("bucket"), Prefix: typed("prefix"), Region: typed("region"),
		AccessKey: typed("access_key"), SecretKey: form.Get("secret_key"), LiveOrigin: typed("live_origin"),
		LiveMode: typed("live_mode"),
		SFTPHost: typed("sftp_host"), SFTPPort: typed("sftp_port"), SFTPUser: typed("sftp_user"),
		SFTPAuth: typed("sftp_auth"), SFTPDir: typed("sftp_dir"),
		// The password and key are taken as typed; the adapter normalises line endings.
		SFTPPassword: form.Get("sftp_password"), SFTPKey: form.Get("sftp_key"),
		// The switch posts "on" when checked and its OffValue otherwise.
		SFTPHTAccess: form.Get("sftp_htaccess") == "on",
	}
	if in.LiveMode != HostingLiveSame && in.LiveMode != HostingLiveSeparate {
		// A form without the choice (an older page) keeps its meaning: an origin means separate.
		in.LiveMode = HostingLiveSame
		if in.LiveOrigin != "" {
			in.LiveMode = HostingLiveSeparate
		}
	}
	if in.LiveMode == HostingLiveSame {
		in.LiveOrigin = "" // a stale hidden value is never saved
	}
	return in
}

// hostingValidate is the admin-side check against the requested mode. The
// adapter adds the engine's checks (live origin format, target guards).
func hostingValidate(in HostingInput, siteURL string) []ui.FieldError {
	var errs []ui.FieldError
	add := func(id, label, msg string) { errs = append(errs, ui.FieldError{ID: id, Label: label, Message: msg}) }
	if in.Mode != HostingServed && in.Mode != HostingStatic {
		add("hosting-mode", "Mode", "Choose how pages are served.")
	}
	if in.Target != "" && in.Target != HostingTargetDir && in.Target != HostingTargetS3 && in.Target != HostingTargetSFTP {
		add("hosting-target", "Publish to", "Choose a local directory, S3-compatible storage or SFTP.")
	}
	if in.Service != "" {
		known := false
		for _, s := range offsiteServices {
			known = known || s.v == in.Service
		}
		if !known {
			add("hosting-service", "Storage service", "Choose one of the listed services.")
		}
	}
	if in.Mode != HostingStatic {
		return errs
	}
	if siteURL == "" {
		add("hosting-mode", "Mode", "Set the site address in Site settings first.")
	}
	switch in.Target {
	case HostingTargetDir:
		if !filepath.IsAbs(in.Dir) {
			add("hosting-dir", "Directory", "Enter an absolute path, for example /var/www/site.")
		}
	case HostingTargetS3:
		if in.Endpoint == "" && in.Service != OffsiteAWS {
			add("hosting-endpoint", "Endpoint", "Enter the endpoint of the storage service.")
		}
		if in.Bucket == "" {
			add("hosting-bucket", "Bucket", "Enter a bucket name.")
		}
	case HostingTargetSFTP:
		hostingValidateSFTP(in, add)
	case "":
		add("hosting-target", "Publish to", "Choose where the files go.")
	}
	if in.LiveMode == HostingLiveSeparate && in.LiveOrigin == "" {
		add("hosting-live-origin", "Live origin", "Enter the address where the live islands answer.")
	}
	return errs
}

// hostingValidateSFTP checks the shape of the SFTP fields. The engine does the
// remote folder and host rules.
func hostingValidateSFTP(in HostingInput, add func(id, label, msg string)) {
	if in.SFTPHost == "" {
		add("hosting-sftp-host", "Host", "Enter the SFTP host name.")
	}
	if in.SFTPUser == "" {
		add("hosting-sftp-user", "User", "Enter the SFTP user name.")
	}
	if in.SFTPDir == "" {
		add("hosting-sftp-dir", "Remote folder", "Enter a folder on the host, for example public_html.")
	}
	if port, err := strconv.Atoi(in.SFTPPort); err != nil || port < 1 || port > 65535 {
		add("hosting-sftp-port", "Port", "Enter a port from 1 to 65535.")
	}
	if in.SFTPAuth != HostingSFTPAuthPassword && in.SFTPAuth != HostingSFTPAuthKey {
		add("hosting-sftp-auth", "Sign in with", "Choose a password or a private key.")
	}
}

func (a *Admin) handleHostingSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Hosting
	if svc == nil {
		a.unavailable(w, r, "Hosting")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	in := hostingInputFrom(r.PostForm)
	cur, err := svc.View(r.Context())
	if err != nil {
		a.logger().Error("hosting settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The hosting settings could not be loaded.")
		return
	}
	errs := hostingValidate(in, a.siteAddress(r))
	if len(errs) == 0 && hostingNeedsSudo(cur, in) {
		if sess := sessionFrom(r.Context()); sess == nil || !a.sudo.store.Active(sess.ID) {
			a.toSudo(w, r)
			return
		}
	}
	if len(errs) == 0 {
		if errs, err = svc.Save(r.Context(), in); err != nil {
			a.logger().Error("hosting settings save", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The hosting settings could not be saved.")
			return
		}
	}
	if len(errs) > 0 {
		in.SecretKey, in.SFTPPassword, in.SFTPKey = "", "", ""
		a.renderHosting(w, r, http.StatusUnprocessableEntity, &in, errs)
		return
	}
	a.audit(r, "hosting.update", "hosting", map[string]any{"changed": hostingChanged(cur, in), "secret_changed": in.SecretKey != "" || in.SFTPPassword != "" || in.SFTPKey != ""})
	if in.Mode != cur.Mode {
		a.SetFlash(w, r, "info", "Settings saved. Confirm the switch.")
		http.Redirect(w, r, a.adminPath+hostingPath+"/mode?to="+url.QueryEscape(in.Mode), http.StatusSeeOther)
		return
	}
	a.SetFlash(w, r, "success", "Hosting settings saved.")
	http.Redirect(w, r, a.adminPath+hostingPath, http.StatusSeeOther)
}

// hostingNeedsSudo: a static site publishes from these settings, so changing
// where or how it publishes asks for the password. Served saves never do, and
// the mode switch has its own confirm and sudo.
func hostingNeedsSudo(cur HostingView, in HostingInput) bool {
	if cur.Mode != HostingStatic {
		return false
	}
	if in.SecretKey != "" || in.SFTPPassword != "" || in.SFTPKey != "" {
		return true
	}
	for _, f := range hostingChanged(cur, in) {
		if f != "mode" {
			return true
		}
	}
	return false
}

// hostingChanged names the fields that differ from the stored ones; values
// stay out of the audit log.
func hostingChanged(v HostingView, in HostingInput) []string {
	changed := []string{}
	for _, c := range []struct {
		name string
		diff bool
	}{
		{"mode", v.Mode != in.Mode}, {"target", v.Target != in.Target}, {"dir", v.Dir != in.Dir},
		{"service", v.Service != in.Service}, {"endpoint", v.Endpoint != in.Endpoint}, {"bucket", v.Bucket != in.Bucket},
		{"prefix", v.Prefix != in.Prefix}, {"region", v.Region != in.Region}, {"access_key", v.AccessKey != in.AccessKey},
		{"live_origin", v.LiveOrigin != in.LiveOrigin},
		{"sftp_host", v.SFTPHost != in.SFTPHost}, {"sftp_port", v.SFTPPort != in.SFTPPort}, {"sftp_user", v.SFTPUser != in.SFTPUser},
		{"sftp_auth", v.SFTPAuth != in.SFTPAuth}, {"sftp_dir", v.SFTPDir != in.SFTPDir}, {"sftp_htaccess", v.SFTPHTAccess != in.SFTPHTAccess},
	} {
		if c.diff {
			changed = append(changed, c.name)
		}
	}
	return changed
}
