package admin

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/importjob"
	"hatamoto.dev/core/internal/wpimport"
)

func init() {
	areaPages["import"] = []string{"import/source", "import/scan", "import/options", "import/progress", "import/report"}
}

// fakeImport is an ImportService over fixed runs.
type fakeImport struct {
	mu      sync.Mutex
	runs    map[uuid.UUID]*importjob.Run
	began   []importjob.Source
	started []importjob.Options
	resumed []*importjob.Source
	cancels int
	err     error
}

func (f *fakeImport) Begin(_ context.Context, _ importjob.Actor, src importjob.Source) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return uuid.Nil, f.err
	}
	f.began = append(f.began, src)
	return fixedID(1), nil
}
func (f *fakeImport) Get(_ context.Context, id uuid.UUID) (*importjob.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.runs[id]; ok {
		c := *r
		return &c, nil
	}
	return nil, importjob.ErrNotFound
}
func (f *fakeImport) Recent(context.Context, int) ([]importjob.Run, error) {
	var out []importjob.Run
	for _, r := range f.runs {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}
func (f *fakeImport) Start(_ context.Context, _ uuid.UUID, _ importjob.Actor, _ string, o importjob.Options) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, o)
	return nil
}
func (f *fakeImport) Resume(_ context.Context, _ uuid.UUID, _ importjob.Actor, _ string, c *importjob.Source) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, c)
	return nil
}
func (f *fakeImport) Cancel(context.Context, uuid.UUID, importjob.Actor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels++
	return nil
}

func importReport(mode wpimport.SiteMode) *wpimport.Report {
	return &wpimport.Report{
		Source: "https://old.example.com", Site: wpimport.Site{URL: "https://old.example.com", Title: "Old"},
		Posts: 12, Pages: 5, CustomTypes: map[string]int{"portfolio": 2}, Drafts: 2, Private: 1, Media: 30, Terms: 8, Authors: 2, Menus: 2, OldURLs: 40,
		SiteMode: mode, Permalink: "/%year%/%monthnum%/%postname%/", PermalinkPattern: "/{year}/{month}/{slug}", PermalinkKept: true,
		SEOPlugin: "yoast", PageBuilders: []string{"Elementor"}, Forms: []string{"Contact Form 7"}, Multilingual: []string{"WPML"}, DefaultLang: "en",
		UnsupportedPlugins: []string{"acme/v1"}, Shortcodes: map[string]int{"acme_pricing": 2},
		Fidelity:    wpimport.Fidelity{High: 14, Medium: 3, Low: 1, Percent: 78},
		LowFidelity: []wpimport.ItemNote{{ID: "77", Type: "page", Title: "Landing", Reason: "page builder (Elementor): layout is not kept, review the text"}},
	}
}

func importFixtures() (map[uuid.UUID]*importjob.Run, map[string]uuid.UUID) {
	stamp := time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)
	ids := map[string]uuid.UUID{"scanning": fixedID(1), "scanned": fixedID(2), "running": fixedID(3), "done": fixedID(4), "failed": fixedID(5), "existing": fixedID(6), "interrupted": fixedID(7)}
	base := func(id uuid.UUID, status string) *importjob.Run {
		return &importjob.Run{ID: id, Status: status, Kind: importjob.KindREST, URL: "https://old.example.com", User: "admin", Started: stamp, Options: importjob.DefaultOptions()}
	}
	runs := map[uuid.UUID]*importjob.Run{}
	runs[ids["scanning"]] = base(ids["scanning"], importjob.StatusScanning)
	runs[ids["scanning"]].Phase = "read"
	sc := base(ids["scanned"], importjob.StatusScanned)
	sc.Report = importReport(wpimport.ModeEmpty)
	runs[ids["scanned"]] = sc
	ex := base(ids["existing"], importjob.StatusScanned)
	ex.Report = importReport(wpimport.ModeExisting)
	runs[ids["existing"]] = ex
	rn := base(ids["running"], importjob.StatusRunning)
	rn.Phase, rn.PhaseIndex, rn.Report = "content", 7, importReport(wpimport.ModeEmpty)
	rn.Progress = []string{"05:06:01 media: start", "05:06:09 content: start"}
	runs[ids["running"]] = rn
	dn := base(ids["done"], importjob.StatusCompleted)
	rep := importReport(wpimport.ModeEmpty)
	dn.Report = rep
	dn.Result = &wpimport.Result{RunID: ids["done"], Created: 19, Updated: 0, Terms: 8, Authors: 2, MediaImported: 29, MediaReused: 1, Menus: 2, Redirects: 2, LinksRewritten: 6,
		SiteMode: wpimport.ModeEmpty, Report: rep,
		Errors:       []wpimport.ItemError{{Kind: "media", ID: "9", Title: "https://old.example.com/wp-content/uploads/gone.jpg", Err: "HTTP 404"}},
		Warnings:     []string{"site title is Old here, Acme in WordPress"},
		URLConflicts: []wpimport.URLConflict{{URL: "/about", Target: "/about-2", Reason: "a live page answers at /about"}},
		Audit:        &wpimport.AuditResult{Checked: 40, OK: 30, Redirected: 8, Failures: []wpimport.AuditFailure{{URL: "/old/missing", Status: 404, Reason: "answers 404"}}}}
	dn.EntityIDs = map[string]uuid.UUID{"77": fixedID(8)}
	dn.Redirects = []importjob.RedirectRow{{From: "/2024/05/hello", To: "/blog/hello", Code: 301}}
	runs[ids["done"]] = dn
	fl := base(ids["failed"], importjob.StatusFailed)
	fl.Error, fl.NeedsCredentials = "media: connection reset", true
	fl.Report = rep
	fl.Result = &wpimport.Result{RunID: ids["failed"], Created: 4, Failed: 1, SiteMode: wpimport.ModeEmpty, Report: rep}
	runs[ids["failed"]] = fl
	it := base(ids["interrupted"], importjob.StatusInterrupted)
	it.Report = rep
	runs[ids["interrupted"]] = it
	return runs, ids
}

func importRig(t *testing.T) (*rig, *fakeImport, map[string]uuid.UUID) {
	runs, ids := importFixtures()
	f := &fakeImport{runs: runs}
	return newRig(t, func(d *Deps) { d.Import = f }), f, ids
}

func TestImportWizardPagesGoldenAndA11y(t *testing.T) {
	g, _, ids := importRig(t)
	pages := []struct{ name, path string }{
		{"source", "/admin/settings/import/wordpress"},
		{"source-resync", "/admin/settings/import/wordpress?from=" + ids["done"].String()},
		{"scanning", "/admin/settings/import/wordpress/" + ids["scanning"].String()},
		{"scan-empty", "/admin/settings/import/wordpress/" + ids["scanned"].String()},
		{"scan-existing", "/admin/settings/import/wordpress/" + ids["existing"].String()},
		{"options", "/admin/settings/import/wordpress/" + ids["scanned"].String() + "/options"},
		{"options-existing", "/admin/settings/import/wordpress/" + ids["existing"].String() + "/options"},
		{"running", "/admin/settings/import/wordpress/" + ids["running"].String()},
		{"report", "/admin/settings/import/wordpress/" + ids["done"].String()},
		{"report-failed", "/admin/settings/import/wordpress/" + ids["failed"].String()},
		{"report-interrupted", "/admin/settings/import/wordpress/" + ids["interrupted"].String()},
	}
	for _, p := range pages {
		rec := g.do("GET", p.path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", p.name, rec.Code, rec.Body.String())
		}
		body := normalizePage(rec.Body.String())
		compareGolden(t, "pages/import-"+p.name+".html", body)
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %s", p.name, strings.Join(issues, "; "))
		}
	}
}

func TestImportReadPathsNeedNoJavaScript(t *testing.T) {
	g, _, ids := importRig(t)
	// A running page reloads itself without JS (a <noscript> meta refresh) and
	// still has a plain link; with JS htmx polls the same address.
	body := g.do("GET", "/admin/settings/import/wordpress/"+ids["running"].String(), "owner-token", nil, nil).Body.String()
	for _, want := range []string{`<noscript><meta http-equiv="refresh" content="3"></noscript>`, `hx-trigger="every 3s"`, `hm-meter`, `hm-tracker`} {
		if !strings.Contains(body, want) {
			t.Errorf("running page lacks %q", want)
		}
	}
	if strings.Contains(body, "<progress") || strings.Contains(body, "This page refreshes") {
		t.Error("running page keeps the old progress bar or refresh line")
	}
	// Stop leads to a confirm page, and only its POST cancels.
	if !strings.Contains(body, `href="/admin/settings/import/wordpress/`+ids["running"].String()+`/cancel"`) || !strings.Contains(body, "hm-btn--danger") {
		t.Error("Stop the import is not a danger link to the confirm page")
	}
	if b := g.do("GET", "/admin/settings/import/wordpress/"+ids["running"].String()+"/cancel", "owner-token", nil, nil).Body.String(); !strings.Contains(b, "Stop the import?") || !strings.Contains(b, `<form method="post"`) {
		t.Error("cancel confirm page")
	}
	// Every mutation is a real form post (no JS needed).
	for _, p := range []string{"/admin/settings/import/wordpress", "/admin/settings/import/wordpress/" + ids["scanned"].String() + "/options", "/admin/settings/import/wordpress/" + ids["failed"].String()} {
		if b := g.do("GET", p, "owner-token", nil, nil).Body.String(); !strings.Contains(b, `<form method="post"`) {
			t.Errorf("%s has no form", p)
		}
	}
	// A page that is finished is not refreshed.
	if b := g.do("GET", "/admin/settings/import/wordpress/"+ids["done"].String(), "owner-token", nil, nil).Body.String(); strings.Contains(b, "http-equiv") {
		t.Error("report page refreshes")
	}
	// A poll that finds the run in another state moves the whole page.
	rec := g.do("GET", "/admin/settings/import/wordpress/"+ids["done"].String()+"?poll=running", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "import-progress"})
	if rec.Header().Get("HX-Redirect") != "/admin/settings/import/wordpress/"+ids["done"].String() {
		t.Errorf("poll of a finished run: %d %v", rec.Code, rec.Header())
	}
	rec = g.do("GET", "/admin/settings/import/wordpress/"+ids["running"].String()+"?poll=running", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "import-progress"})
	if rec.Code != 200 || rec.Header().Get("HX-Redirect") != "" || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("poll of a running run answers the fragment: %d", rec.Code)
	}
}

func TestImportReportLinks(t *testing.T) {
	g, _, ids := importRig(t)
	body := g.do("GET", "/admin/settings/import/wordpress/"+ids["done"].String(), "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		`href="/admin/entities/` + fixedID(8).String() + `/edit"`, // low-fidelity page links to its edit screen
		`href="/old/missing"`, // an audit failure links to the address
		`href="/2024/05/hello"`,
		`/admin/settings/import/wordpress?from=` + ids["done"].String(), // re-sync
		"HTTP 404", "answers 404",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}

func TestImportIsOwnerOnlyAndNeedsAService(t *testing.T) {
	g, _, ids := importRig(t)
	for _, p := range []string{"/admin/settings/import/wordpress", "/admin/settings/import/wordpress/" + ids["done"].String(), "/admin/settings/import/wordpress/" + ids["scanned"].String() + "/options"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != http.StatusForbidden {
			t.Errorf("editor GET %s = %d", p, rec.Code)
		}
		if rec := g.do("GET", p, "", nil, nil); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
			t.Errorf("signed out GET %s = %d", p, rec.Code)
		}
	}
	for _, p := range []string{"/admin/settings/import/wordpress", "/admin/settings/import/wordpress/" + ids["scanned"].String() + "/start", "/admin/settings/import/wordpress/" + ids["failed"].String() + "/resume", "/admin/settings/import/wordpress/" + ids["running"].String() + "/cancel"} {
		if rec := g.do("POST", p, "editor-token", url.Values{"kind": {"rest"}}, nil); rec.Code != http.StatusForbidden {
			t.Errorf("editor POST %s = %d", p, rec.Code)
		}
	}
	none := newRig(t)
	if rec := none.do("GET", "/admin/settings/import/wordpress", "owner-token", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without a service: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/settings/import/wordpress/not-an-id", "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("bad id: %d", rec.Code)
	}
}

func TestImportSourceNeverEchoesThePassword(t *testing.T) {
	g, f, _ := importRig(t)
	f.err = &importjob.ValidationError{Field: "url", Message: "Enter a web address such as https://old-site.example.com."}
	const pw = "abcd EFGH 1234 ijkl"
	rec := g.do("POST", "/admin/settings/import/wordpress", "owner-token", url.Values{"kind": {"rest"}, "url": {"nope"}, "user": {"admin"}, "password": {pw}}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, pw) || strings.Contains(body, "EFGH") {
		t.Fatal("the password is in the response")
	}
	if !strings.Contains(body, "Enter a web address") || !strings.Contains(body, `value="admin"`) {
		t.Errorf("error or username missing")
	}
	f.err = nil
	rec = g.do("POST", "/admin/settings/import/wordpress", "owner-token", url.Values{"kind": {"rest"}, "url": {"https://old.example.com"}, "user": {"admin"}, "password": {pw}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/settings/import/wordpress/"+fixedID(1).String() {
		t.Fatalf("begin: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.began) != 1 || f.began[0].Password != pw || f.began[0].URL != "https://old.example.com" {
		t.Errorf("source: %+v", f.began)
	}
}

func TestImportPrivateHostOverrideIsNotOnTheWeb(t *testing.T) {
	// Neither what the form can send nor what the service takes names it.
	for _, v := range []any{importjob.Source{}, importjob.Options{}} {
		typ := reflect.TypeOf(v)
		for i := range typ.NumField() {
			n := strings.ToLower(typ.Field(i).Name)
			if strings.Contains(n, "private") || strings.Contains(n, "allow") || strings.Contains(n, "ssrf") {
				t.Errorf("%s.%s looks like the private-host override", typ.Name(), typ.Field(i).Name)
			}
		}
	}
	for _, m := range []string{"AllowPrivate", "AllowPrivateHosts"} {
		if _, ok := reflect.TypeOf((*ImportService)(nil)).Elem().MethodByName(m); ok {
			t.Errorf("ImportService has %s", m)
		}
	}
	g, f, ids := importRig(t)
	g.do("POST", "/admin/settings/import/wordpress", "owner-token", url.Values{"kind": {"rest"}, "url": {"http://127.0.0.1"}, "allow_private": {"1"}, "allow_private_hosts": {"true"}}, nil)
	g.do("POST", "/admin/settings/import/wordpress/"+ids["scanned"].String()+"/start", "owner-token", url.Values{"types": {"post"}, "allow_private": {"1"}}, nil)
	if len(f.began) != 1 || len(f.started) != 1 {
		t.Fatalf("began %d started %d", len(f.began), len(f.started))
	}
}

func TestImportStartOptions(t *testing.T) {
	g, f, ids := importRig(t)
	rec := g.do("POST", "/admin/settings/import/wordpress/"+ids["scanned"].String()+"/start", "owner-token",
		url.Values{"types": {"post", "page", "bogus"}, "menus": {"1"}, "media": {"1"}, "patterns": {"keep"}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", rec.Code)
	}
	want := importjob.Options{Types: []string{"post", "page"}, Menus: true, Media: true, KeepPatterns: true}
	if len(f.started) != 1 || !reflect.DeepEqual(f.started[0], want) {
		t.Errorf("options %+v, want %+v", f.started, want)
	}
	// Nothing chosen: the form comes back.
	rec = g.do("POST", "/admin/settings/import/wordpress/"+ids["scanned"].String()+"/start", "owner-token", url.Values{"menus": {"1"}}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Choose at least one kind of content") {
		t.Errorf("empty choice: %d", rec.Code)
	}
	// A site with content never takes WordPress's structure, whatever is posted.
	g.do("POST", "/admin/settings/import/wordpress/"+ids["existing"].String()+"/start", "owner-token", url.Values{"types": {"post"}, "patterns": {"wordpress"}}, nil)
	if got := f.started[len(f.started)-1]; !got.KeepPatterns {
		t.Errorf("existing site options %+v", got)
	}
}

func TestImportResumeAndCancel(t *testing.T) {
	g, f, ids := importRig(t)
	if rec := g.do("POST", "/admin/settings/import/wordpress/"+ids["running"].String()+"/cancel", "owner-token", url.Values{}, nil); rec.Code != http.StatusSeeOther || f.cancels != 1 {
		t.Errorf("cancel: %d %d", rec.Code, f.cancels)
	}
	// Resume with a re-entered password (multipart, as the page posts it).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_csrf", csrfValue) // first part, as the form's hidden field sends it
	_ = mw.WriteField("password", "new-secret-pass")
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/admin/settings/import/wordpress/"+ids["failed"].String()+"/resume", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""}) // the token is the first body part
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || len(f.resumed) != 1 || f.resumed[0] == nil || f.resumed[0].Password != "new-secret-pass" {
		t.Errorf("resume: %d %+v", rec.Code, f.resumed)
	}
	// An oversized upload is refused before anything reads it.
	req = httptest.NewRequest("POST", "/admin/settings/import/wordpress", strings.NewReader("x"))
	req.ContentLength = importBodyLimit + 1
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""})
	rec = httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: %d", rec.Code)
	}
}

func TestSetupContentCanChooseWordPress(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	body := g.do("GET", "/admin/setup/content", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `value="wordpress"`) || !strings.Contains(body, "Import from WordPress") {
		t.Fatalf("the onboarding choice is missing:\n%s", body)
	}
	rec := g.do("POST", "/admin/setup/content", "owner-token", url.Values{"content": {"wordpress"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/settings/import/wordpress" || g.site.cfg.SetupCompletedAt == nil {
		t.Errorf("choice: %d %s done=%v", rec.Code, rec.Header().Get("Location"), g.site.cfg.SetupCompletedAt != nil)
	}
	if g.starter.opts.SiteName != "" {
		t.Error("starter content was installed")
	}
}

func TestImportIsReachableFromSettingsForOwnersOnly(t *testing.T) {
	g, _, _ := importRig(t)
	if b := g.do("GET", "/admin/profile", "owner-token", nil, nil).Body.String(); !strings.Contains(b, `href="/admin/settings"`) {
		t.Error("owner sidebar lacks the Settings link")
	}
	if b := g.do("GET", "/admin/profile", "editor-token", nil, nil).Body.String(); strings.Contains(b, `/admin/settings/import/wordpress`) {
		t.Error("editor sidebar shows the import link")
	}
}

// Whatever the old site says is text: hostile strings never become markup or
// an off-site link on the scan and report pages.
func TestImportPagesEscapeHostileSourceStrings(t *testing.T) {
	g, f, ids := importRig(t)
	const evil = `<script>alert(1)</script>`
	for _, id := range []uuid.UUID{ids["scanned"], ids["done"]} {
		r := f.runs[id]
		rep := *r.Report
		rep.Site.Title = evil
		rep.UnsupportedPlugins = []string{evil}
		rep.Shortcodes = map[string]int{evil: 1}
		rep.PageBuilders = []string{evil}
		rep.LowFidelity = []wpimport.ItemNote{{ID: "77", Type: evil, Title: evil, Reason: evil}}
		r.Report = &rep
		r.Error = evil
		if r.Result != nil {
			res := *r.Result
			res.Report = &rep
			res.Errors = []wpimport.ItemError{{Kind: evil, ID: "9", Title: evil, Err: evil}}
			res.URLConflicts = []wpimport.URLConflict{{URL: "javascript:alert(1)", Target: "//evil.example/x", Reason: evil}}
			res.Audit = &wpimport.AuditResult{Checked: 1, Failures: []wpimport.AuditFailure{{URL: "//evil.example/x", Status: 404, Reason: evil}}}
			r.Result = &res
		}
		body := g.do("GET", "/admin/settings/import/wordpress/"+id.String(), "owner-token", nil, nil).Body.String()
		if strings.Contains(body, evil) {
			t.Errorf("run %s: an unescaped <script> reached the page", id)
		}
		for _, bad := range []string{`href="javascript:`, `href="//evil`, `src="javascript:`} {
			if strings.Contains(body, bad) {
				t.Errorf("run %s: the page links to %s", id, bad)
			}
		}
	}
}

// The import wizard's multipart forms never take the CSRF token from the URL.
func TestImportWizardCSRFNeverFromQuery(t *testing.T) {
	g, _, ids := importRig(t)
	post := func(target string, tokenFirst bool) int {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if tokenFirst {
			_ = mw.WriteField("_csrf", csrfValue)
		}
		_ = mw.WriteField("password", "new-secret-pass")
		_ = mw.Close()
		req := httptest.NewRequest("POST", target, &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
		g.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""})
		rec := httptest.NewRecorder()
		g.h.ServeHTTP(rec, req)
		return rec.Code
	}
	resume := "/admin/settings/import/wordpress/" + ids["failed"].String() + "/resume"
	if got := post(resume+"?_csrf="+csrfValue, false); got != http.StatusForbidden {
		t.Errorf("token in the query string: %d, want 403", got)
	}
	if got := post(resume, true); got != http.StatusSeeOther {
		t.Errorf("token as the first part: %d, want 303", got)
	}
	body := g.do("GET", "/admin/settings/import/wordpress", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "_csrf=") {
		t.Error("the import form puts the token in its action URL")
	}
}

func TestImportScanShowsStatsAndFidelityBar(t *testing.T) {
	g, _, ids := importRig(t)
	body := g.do("GET", "/admin/settings/import/wordpress/"+ids["existing"].String(), "owner-token", nil, nil).Body.String()
	for _, want := range []string{"hm-statgrid", "hm-catbar", "Expected fidelity", "Without changes", "Need a look"} {
		if !strings.Contains(body, want) {
			t.Errorf("scan page lacks %q", want)
		}
	}
	// The percent lives in the fidelity card, and no statcard sits inside a card.
	fid := body[strings.Index(body, `id="fidelity"`):]
	if strings.Contains(fid, "hm-ring") || strings.Contains(body, "hm-statcard--ring") {
		t.Error("the fidelity percent belongs in the fidelity card as text above the bar, not as a ring or in the stats row")
	}
	if i := strings.Index(body, `id="counts"`); i < strings.Index(body, "hm-statgrid") {
		t.Error("the stats row comes before, not inside, the counts card")
	}
	if scanFidelityText(wpimport.Fidelity{}) != "" {
		t.Error("an empty scan has no fidelity sentence, so no 0% is shown")
	}
	if bar := scanFidelity(wpimport.Fidelity{}); len(bar.Segs()) != 0 || len(bar.Segments) != 3 {
		t.Errorf("empty fidelity: %d segments on the bar, %d in the legend", len(bar.Segs()), len(bar.Segments))
	}
}

func TestImportReportLeadsWithStatsAndFailures(t *testing.T) {
	g, _, ids := importRig(t)
	body := g.do("GET", "/admin/settings/import/wordpress/"+ids["done"].String(), "owner-token", nil, nil).Body.String()
	order := []string{"hm-statgrid", `id="errors"`, `id="audit"`, "hm-catbar", `id="redirects"`, `id="low"`, `id="summary"`}
	last := -1
	for _, want := range order {
		i := strings.Index(body, want)
		if i < 0 || i < last {
			t.Fatalf("%q out of order or missing (at %d, after %d)", want, i, last)
		}
		last = i
	}
	if strings.Contains(body, "answer directly,") {
		t.Error("the audit prose sentence is back")
	}
}

func TestImportSourceCardFootsAndTrend(t *testing.T) {
	g, _, _ := importRig(t)
	body := g.do("GET", "/admin/settings/import/wordpress", "owner-token", nil, nil).Body.String()
	if n := strings.Count(body, `<footer class="hm-card__foot`); n != 2 {
		t.Errorf("%d footers, want 2: one inside each source card, of which one shows", n)
	}
	for _, want := range []string{"hm-tracker", `id="kind-0"`, `id="kind-1"`, `id="rest"`, `id="wxr-card"`, "hm-import__source"} {
		if !strings.Contains(body, want) {
			t.Errorf("source page lacks %q", want)
		}
	}
	// Both cards are in the markup: with JS or :has() missing, nothing is hidden.
	if strings.Contains(body, "display: none") {
		t.Error("the page hides a card inline")
	}
}

func TestImportOptionsAreGroupedIntoCards(t *testing.T) {
	g, _, ids := importRig(t)
	for _, id := range []string{"scanned", "existing"} {
		body := g.do("GET", "/admin/settings/import/wordpress/"+ids[id].String()+"/options", "owner-token", nil, nil).Body.String()
		for _, title := range []string{"Content", "Files", "URLs", "Re-import"} {
			if !strings.Contains(body, `<h2 class="hm-card__title" id="opt-`) || !strings.Contains(body, ">"+title+"</h2>") {
				t.Errorf("%s: no %q card", id, title)
			}
		}
		if n := strings.Count(body, `<footer class="hm-card__foot`); n != 1 {
			t.Errorf("%s: %d footers, want 1", id, n)
		}
	}
}

// The Stop confirm only asks about a run that can be stopped, and asking
// never stops it.
func TestImportCancelConfirmChecksTheRun(t *testing.T) {
	g, f, ids := importRig(t)
	base := "/admin/settings/import/wordpress/"
	rec := g.do("GET", base+ids["running"].String()+"/cancel", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Stop the import?") {
		t.Errorf("running run: %d", rec.Code)
	}
	if f.cancels != 0 || f.runs[ids["running"]].Status != importjob.StatusRunning {
		t.Errorf("GET must leave the run running: cancels=%d status=%s", f.cancels, f.runs[ids["running"]].Status)
	}
	rec = g.do("GET", base+ids["done"].String()+"/cancel", "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != base+ids["done"].String() {
		t.Errorf("finished run: %d to %q, want a redirect to the run", rec.Code, rec.Header().Get("Location"))
	}
	if rec := g.do("GET", base+uuid.NewString()+"/cancel", "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown run: %d, want 404", rec.Code)
	}
}
