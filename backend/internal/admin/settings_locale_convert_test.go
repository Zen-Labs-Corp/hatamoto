package admin

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/localeconv"
	"hatamoto.dev/core/internal/siteconfig"
)

// fakeConvert is a localeconv.Service that records its calls.
type fakeConvert struct {
	mu       sync.Mutex
	suggest  func() (*localeconv.Plan, []localeconv.Issue, error)
	dryRun   func(*localeconv.Plan) (*localeconv.Report, error)
	apply    func(*localeconv.Plan, localeconv.ApplyOpts) (*localeconv.Record, *localeconv.Report, error)
	revert   func(id int64, force bool) (*localeconv.RevertResult, error)
	history  []localeconv.HistoryItem
	applied  []localeconv.ApplyOpts
	reverted []bool
}

func (f *fakeConvert) Suggest(context.Context) (*localeconv.Plan, []localeconv.Issue, error) {
	return f.suggest()
}

func (f *fakeConvert) DryRun(_ context.Context, p *localeconv.Plan) (*localeconv.Report, error) {
	return f.dryRun(p)
}

func (f *fakeConvert) Apply(_ context.Context, p *localeconv.Plan, o localeconv.ApplyOpts) (*localeconv.Record, *localeconv.Report, error) {
	f.mu.Lock()
	f.applied = append(f.applied, o)
	f.mu.Unlock()
	return f.apply(p, o)
}

func (f *fakeConvert) Revert(_ context.Context, id int64, force bool, _ string) (*localeconv.RevertResult, error) {
	f.mu.Lock()
	f.reverted = append(f.reverted, force)
	f.mu.Unlock()
	return f.revert(id, force)
}

func (f *fakeConvert) History(context.Context) ([]localeconv.HistoryItem, error) {
	return f.history, nil
}

const convertPlanJSON = `{"locales":[{"tag":"en-XX","segment":"xx","root_slug":"xx"},{"tag":"en-ZZ","segment":"zz","root_slug":"zz"}],"default":"en-XX","locale_from":{"attribute":"region"}}`

// convertPlanSHA is the plan hash a report carries for convertPlanJSON.
var convertPlanSHA = func() string {
	p, err := localeconv.ParsePlan(strings.NewReader(convertPlanJSON))
	if err != nil {
		panic(err)
	}
	return localeconv.PlanSHA256(p)
}()

func convertReportFixture() *localeconv.Report {
	return &localeconv.Report{
		PlanSHA256: convertPlanSHA, DataFingerprint: "fp-1", OK: true,
		Warnings: []localeconv.Issue{{Code: localeconv.CodeUnmatchedValue, Message: "3 pages have region qq, which matches no locale"}},
		Locales: []localeconv.LocaleReport{
			{Tag: "en-XX", Segment: "xx", Home: localeconv.HomeReport{ID: "11111111-2222-3333-4444-555555555555", OldPath: "/xx", NewPath: "/xx"}, CountsBySchema: map[string]int{"page": 40, "service": 60}, Total: 100},
			{Tag: "en-ZZ", Segment: "zz", Home: localeconv.HomeReport{ID: "66666666-2222-3333-4444-555555555555", OldPath: "/zz", NewPath: "/zz"}, CountsBySchema: map[string]int{"page": 38}, Total: 38},
		},
		Shared:            localeconv.SharedReport{CountsBySchema: map[string]int{"post": 120}, Total: 120},
		RenamedCollisions: []localeconv.RenamedRow{{ID: "x", Schema: "page", OldSlug: "home", NewSlug: "home-old-1"}},
		Groups:            localeconv.GroupsReport{Formed: 18, Singletons: 2},
		Overrides:         localeconv.OverridesReport{SitemapGroupRemoved: []localeconv.OverrideChange{{Schema: "page"}, {Schema: "service"}}},
		Redirects:         localeconv.RedirectsReport{Count: 44, ItemsSample: []string{"/xx/home"}},
	}
}

func newConvertRig(t *testing.T) (*rig, *fakeConvert, *[]AuditEvent, *int) {
	t.Helper()
	fc := &fakeConvert{
		suggest: func() (*localeconv.Plan, []localeconv.Issue, error) {
			return &localeconv.Plan{Version: 1, Locales: []localeconv.LocaleSpec{{Tag: "en-XX", Segment: "xx", RootSlug: "xx"}, {Tag: "en-ZZ", Segment: "zz", RootSlug: "zz"}}, Default: "en-XX", LocaleFrom: localeconv.LocaleFrom{Attribute: "region"}},
				[]localeconv.Issue{{Code: "missing_tag", Message: "No language found on the page zz"}}, nil
		},
		dryRun: func(*localeconv.Plan) (*localeconv.Report, error) { return convertReportFixture(), nil },
		apply: func(*localeconv.Plan, localeconv.ApplyOpts) (*localeconv.Record, *localeconv.Report, error) {
			return &localeconv.Record{ID: 7, Rows: 258}, nil, nil
		},
		revert: func(int64, bool) (*localeconv.RevertResult, error) {
			return &localeconv.RevertResult{ConversionID: 7, Restored: 258}, nil
		},
		history: []localeconv.HistoryItem{
			{ID: 2, AppliedAt: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), AppliedBy: "cli", Status: "reverted", Locales: []string{"en-XX", "en-ZZ"}, Rows: 258},
			{ID: 3, AppliedAt: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC), AppliedBy: "owner@example.com", Status: "applied", Locales: []string{"en-XX", "en-ZZ"}, Rows: 258},
		},
	}
	var events []AuditEvent
	flushed := 0
	g := newSudoRig(t, func(d *Deps) {
		d.LocaleConvert = fc
		d.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
		d.Invalidate = func(context.Context) { flushed++ }
	})
	return g, fc, &events, &flushed
}

func (g *rig) elevate(token string) {
	g.t.Helper()
	g.do("POST", g.a.sudoPath(), token, url.Values{"password": {"correct-horse-battery"}, "next": {""}}, nil)
	if !g.elevated(token) {
		g.t.Fatal("not elevated")
	}
}

const convertBase = "/admin/settings/general/locales/convert"

func TestLocaleConvertAuthz(t *testing.T) {
	t.Parallel()

	g, fc, _, _ := newConvertRig(t)
	routes := []struct{ method, path string }{
		{"GET", convertBase}, {"POST", convertBase + "/suggest"}, {"POST", convertBase + "/report"},
		{"GET", convertBase + "/apply"}, {"POST", convertBase + "/apply"},
		{"GET", convertBase + "/history/3/revert"}, {"POST", convertBase + "/history/3/revert"},
	}
	for _, rt := range routes {
		if rec := g.do(rt.method, rt.path, "editor-token", url.Values{}, nil); rec.Code != http.StatusForbidden {
			t.Errorf("editor %s %s = %d, want 403 (before any sudo prompt)", rt.method, rt.path, rec.Code)
		}
		if rec := g.do(rt.method, rt.path, "", url.Values{}, nil); rec.Code != http.StatusSeeOther {
			t.Errorf("anonymous %s %s = %d, want a redirect to login", rt.method, rt.path, rec.Code)
		}
	}
	for _, rt := range routes[3:] {
		rec := g.do(rt.method, rt.path, "owner-token", url.Values{"confirm_phrase": {"convert"}}, nil)
		if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), g.a.sudoPath()) {
			t.Errorf("owner without sudo %s %s = %d %q, want the sudo prompt", rt.method, rt.path, rec.Code, rec.Header().Get("Location"))
		}
	}
	if len(fc.applied)+len(fc.reverted) != 0 {
		t.Error("the service ran without sudo")
	}
	for _, p := range []string{convertBase, convertBase + "/history/3/revert"} {
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code == http.StatusForbidden {
			t.Errorf("owner GET %s = 403", p)
		}
	}
}

func TestLocaleConvertUnavailableWithoutService(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if rec := g.do("GET", convertBase, "owner-token", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}

func TestLocaleConvertSuggestFillsThePlan(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newConvertRig(t)
	rec := g.do("POST", convertBase+"/suggest", "owner-token", url.Values{"plan": {""}}, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{`&#34;tag&#34;: &#34;en-XX&#34;`, "No language found on the page zz"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestLocaleConvertSuggestFailureIsShown(t *testing.T) {
	t.Parallel()

	g, fc, _, _ := newConvertRig(t)
	fc.suggest = func() (*localeconv.Plan, []localeconv.Issue, error) {
		return nil, nil, &localeconv.BlockingError{Issues: []localeconv.Issue{{Code: "no_region_tree", Message: "No region tree found"}}}
	}
	rec := g.do("POST", convertBase+"/suggest", "owner-token", url.Values{}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "No region tree found") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestLocaleConvertReportPage(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newConvertRig(t)
	rec := g.do("POST", convertBase+"/report", "owner-token", url.Values{"plan": {convertPlanJSON}}, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{
		"Step 1: Plan", "Step 2: Report", "Step 3: Apply", "OK to apply", "/xx (id 11111111)", "shared (und)",
		"Overrides updated (sitemap group dropped)", "Renamed collisions", "Redirects under segments", "Unmatched values",
		"page: home to home-old-1", "3 pages have region qq", "Conversion #3", "Revert",
		`href="` + convertBase + `/apply?plan=` + convertPlanSHA + `"`, convertBase + "/history/3/revert",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, convertBase+"/history/2/revert") {
		t.Error("a reverted conversion offers Revert")
	}
	if strings.ContainsAny(body, "\u2013\u2014") {
		t.Error("em or en dash on the page")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/settings-locale-convert.html", normalizePage(body))
	page := normalizePage(g.do("GET", convertBase, "owner-token", nil, nil).Body.String())
	compareGolden(t, "pages/settings-locale-convert-start.html", page)
}

func TestLocaleConvertBlockingReportOffersNoApply(t *testing.T) {
	t.Parallel()

	g, fc, _, _ := newConvertRig(t)
	fc.dryRun = func(*localeconv.Plan) (*localeconv.Report, error) {
		r := convertReportFixture()
		r.OK = false
		r.Errors = []localeconv.Issue{{Code: localeconv.CodePathChanges, Message: "12 pages would change address"}}
		return r, &localeconv.BlockingError{Issues: r.Errors, Report: r}
	}
	body := g.do("POST", convertBase+"/report", "owner-token", url.Values{"plan": {convertPlanJSON}}, nil).Body.String()
	if !strings.Contains(body, "12 pages would change address") || !strings.Contains(body, "Not OK to apply") {
		t.Error("the blocking error is not shown")
	}
	if strings.Contains(body, "Step 3: Apply") {
		t.Error("a blocked report offers Apply")
	}
	if strings.Count(body, "12 pages would change address") != 1 {
		t.Error("the error is listed twice")
	}
}

func TestLocaleConvertPlanInput(t *testing.T) {
	t.Parallel()

	g, fc, _, _ := newConvertRig(t)
	called := false
	fc.dryRun = func(*localeconv.Plan) (*localeconv.Report, error) { called = true; return convertReportFixture(), nil }
	for name, plan := range map[string]string{
		"empty":         "",
		"not json":      "locales: yes",
		"unknown field": `{"locales":[],"bogus":1}`,
	} {
		rec := g.do("POST", convertBase+"/report", "owner-token", url.Values{"plan": {plan}}, nil)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `hm-field--invalid`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	big := `{"x":"` + strings.Repeat("a", convertMaxPlan) + `"}`
	if rec := g.do("POST", convertBase+"/report", "owner-token", url.Values{"plan": {big}}, nil); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "larger than 1 MiB") {
		t.Errorf("large plan: %d", rec.Code)
	}
	if called {
		t.Error("a bad plan reached the dry run")
	}
}

func TestLocaleConvertApplyFlow(t *testing.T) {
	t.Parallel()

	g, fc, events, flushed := newConvertRig(t)
	g.do("POST", convertBase+"/report", "owner-token", url.Values{"plan": {convertPlanJSON}}, nil)
	g.elevate("owner-token")

	confirm := g.do("GET", convertBase+"/apply?plan="+convertPlanSHA+"&fp=ignored", "owner-token", nil, nil)
	body := confirm.Body.String()
	if confirm.Code != http.StatusOK || !strings.Contains(body, `name="data_fingerprint" value="fp-1"`) || !strings.Contains(body, `name="plan_sha256" value="`+convertPlanSHA+`"`) || !strings.Contains(body, `data-hm-danger-phrase="convert"`) {
		t.Fatalf("confirm page: %d %s", confirm.Code, body)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/settings-locale-convert-confirm.html", normalizePage(body))

	form := url.Values{"plan": {convertPlanJSON}, "plan_sha256": {convertPlanSHA}, "data_fingerprint": {"fp-1"}}
	form.Set("confirm_phrase", "Convert")
	if rec := g.do("POST", convertBase+"/apply", "owner-token", form, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong phrase: %d, want 422", rec.Code)
	}
	if len(fc.applied) != 0 {
		t.Fatal("a wrong phrase applied the plan")
	}
	form.Set("confirm_phrase", "convert")
	rec := g.do("POST", convertBase+"/apply", "owner-token", form, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != convertBase {
		t.Fatalf("apply: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(fc.applied) != 1 || fc.applied[0].Fingerprint != "fp-1" || fc.applied[0].By != "Owner Olga" {
		t.Errorf("apply opts %+v", fc.applied)
	}
	if len(*events) != 1 || (*events)[0].Action != "locales.convert.apply" || (*events)[0].Target != "7" {
		t.Errorf("audit %+v", *events)
	}
	if *flushed != 1 {
		t.Errorf("site cache flushed %d times, want 1", *flushed)
	}
}

func TestLocaleConvertApplyIsBoundToTheDryRun(t *testing.T) {
	t.Parallel()

	g, fc, events, _ := newConvertRig(t)
	g.do("POST", convertBase+"/report", "owner-token", url.Values{"plan": {convertPlanJSON}}, nil)
	g.elevate("owner-token")

	// The confirm page takes the fingerprint from the dry run, not from the link.
	body := g.do("GET", convertBase+"/apply?plan="+convertPlanSHA+"&fp=forged", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `name="data_fingerprint" value="fp-1"`) || strings.Contains(body, "forged") {
		t.Error("the confirm page used the fingerprint of the link")
	}

	other := strings.Replace(convertPlanJSON, `"default":"en-XX"`, `"default":"en-ZZ"`, 1)
	for name, form := range map[string]url.Values{
		"another plan than the reviewed one": {"plan": {other}, "plan_sha256": {convertPlanSHA}, "data_fingerprint": {"fp-1"}},
		"no hash":                            {"plan": {convertPlanJSON}, "data_fingerprint": {"fp-1"}},
		"no fingerprint":                     {"plan": {convertPlanJSON}, "plan_sha256": {convertPlanSHA}},
	} {
		form.Set("confirm_phrase", "convert")
		if rec := g.do("POST", convertBase+"/apply", "owner-token", form, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422", name, rec.Code)
		}
	}
	if len(fc.applied) != 0 || len(*events) != 0 {
		t.Errorf("a plan that was not reviewed ran: %d applies, %d audit entries", len(fc.applied), len(*events))
	}
	// The audit entry names the hash of the plan that ran.
	form := url.Values{"plan": {convertPlanJSON}, "plan_sha256": {convertPlanSHA}, "data_fingerprint": {"fp-1"}, "confirm_phrase": {"convert"}}
	g.do("POST", convertBase+"/apply", "owner-token", form, nil)
	if len(*events) != 1 || (*events)[0].Detail["plan_sha256"] != convertPlanSHA {
		t.Errorf("audit %+v", *events)
	}
}

func TestLocaleConvertApplyRefusesALargePlanBeforeParsingIt(t *testing.T) {
	t.Parallel()

	g, fc, _, _ := newConvertRig(t)
	g.elevate("owner-token")
	// A valid plan padded past the limit: only the size check can refuse it.
	big := convertPlanJSON + strings.Repeat(" ", convertMaxPlan)
	form := url.Values{"plan": {big}, "plan_sha256": {convertPlanSHA}, "data_fingerprint": {"fp-1"}, "confirm_phrase": {"convert"}}
	if rec := g.do("POST", convertBase+"/apply", "owner-token", form, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large plan: %d, want 413", rec.Code)
	}
	if len(fc.applied) != 0 {
		t.Error("a large plan was applied")
	}
}

func TestLocaleConvertReportWithoutAFingerprintOffersNoApply(t *testing.T) {
	t.Parallel()

	g, fc, _, _ := newConvertRig(t)
	fc.dryRun = func(*localeconv.Plan) (*localeconv.Report, error) {
		r := convertReportFixture()
		r.DataFingerprint = ""
		return r, nil
	}
	body := g.do("POST", convertBase+"/report", "owner-token", url.Values{"plan": {convertPlanJSON}}, nil).Body.String()
	if strings.Contains(body, "Step 3: Apply") || !strings.Contains(body, "no data fingerprint") {
		t.Error("a report without a fingerprint offers Apply")
	}
}

func TestLocaleConvertApplyConfirmNeedsAReviewedPlan(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newConvertRig(t)
	g.elevate("owner-token")
	rec := g.do("GET", convertBase+"/apply?plan=unknown&fp=x", "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != convertBase {
		t.Errorf("%d %q, want a redirect to the page", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLocaleConvertStaleFingerprintIsRefused(t *testing.T) {
	t.Parallel()

	g, fc, events, flushed := newConvertRig(t)
	fc.apply = func(*localeconv.Plan, localeconv.ApplyOpts) (*localeconv.Record, *localeconv.Report, error) {
		return nil, nil, &localeconv.BlockingError{Issues: []localeconv.Issue{{Code: localeconv.CodeStaleReport, Message: "data changed since the dry run, run it again"}}}
	}
	g.elevate("owner-token")
	form := url.Values{"plan": {convertPlanJSON}, "plan_sha256": {convertPlanSHA}, "data_fingerprint": {"old"}, "confirm_phrase": {"convert"}}
	rec := g.do("POST", convertBase+"/apply", "owner-token", form, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "data changed since the dry run, run it again") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(*events) != 0 || *flushed != 0 {
		t.Error("a refused apply was audited or flushed the cache")
	}
	if !strings.Contains(rec.Body.String(), "bogus") && !strings.Contains(rec.Body.String(), "en-XX") {
		t.Error("the plan was lost on refusal")
	}
}

func TestLocaleConvertApplyFailureChangesNothingVisible(t *testing.T) {
	t.Parallel()

	g, fc, events, _ := newConvertRig(t)
	fc.apply = func(*localeconv.Plan, localeconv.ApplyOpts) (*localeconv.Record, *localeconv.Report, error) {
		return nil, nil, errors.New("connection reset")
	}
	g.elevate("owner-token")
	form := url.Values{"plan": {convertPlanJSON}, "plan_sha256": {convertPlanSHA}, "data_fingerprint": {"fp-1"}, "confirm_phrase": {"convert"}}
	rec := g.do("POST", convertBase+"/apply", "owner-token", form, nil)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "connection reset") || len(*events) != 0 {
		t.Errorf("%d, audit %d, leaked error %v", rec.Code, len(*events), strings.Contains(rec.Body.String(), "connection reset"))
	}
}

func TestLocaleConvertRevertFlow(t *testing.T) {
	t.Parallel()

	g, fc, events, flushed := newConvertRig(t)
	g.elevate("owner-token")
	path := convertBase + "/history/3/revert"
	if rec := g.do("GET", path, "owner-token", nil, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Revert conversion #3") {
		t.Fatalf("confirm: %d", rec.Code)
	}
	if rec := g.do("POST", path, "owner-token", url.Values{"confirm_phrase": {"nope"}}, nil); rec.Code != http.StatusUnprocessableEntity || len(fc.reverted) != 0 {
		t.Fatalf("wrong phrase: %d", rec.Code)
	}
	if rec := g.do("GET", convertBase+"/history/x/revert", "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("bad id: %d", rec.Code)
	}

	// A conflict is listed and can be forced in a second, typed step.
	fc.revert = func(_ int64, force bool) (*localeconv.RevertResult, error) {
		if force {
			return &localeconv.RevertResult{ConversionID: 3, Restored: 250, Forced: []localeconv.Issue{{Code: localeconv.CodeRevertConflict}}}, nil
		}
		return nil, &localeconv.BlockingError{Issues: []localeconv.Issue{{Code: localeconv.CodeRevertConflict, Message: "The page Pricing was edited after the conversion"}}}
	}
	rec := g.do("POST", path, "owner-token", url.Values{"confirm_phrase": {"revert"}}, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusConflict || !strings.Contains(body, "The page Pricing was edited after the conversion") || !strings.Contains(body, `name="force" value="1"`) || !strings.Contains(body, "Revert anyway") {
		t.Fatalf("conflict: %d %s", rec.Code, body)
	}
	if len(*events) != 0 {
		t.Error("a refused revert was audited")
	}
	rec = g.do("POST", path, "owner-token", url.Values{"confirm_phrase": {"revert"}, "force": {"1"}}, nil)
	if rec.Code != http.StatusSeeOther || len(fc.reverted) != 2 || !fc.reverted[1] {
		t.Fatalf("forced: %d %v", rec.Code, fc.reverted)
	}
	if len(*events) != 1 || (*events)[0].Action != "locales.convert.revert" || *flushed != 1 {
		t.Errorf("audit %+v flushed %d", *events, *flushed)
	}
}

func TestLocaleConvertRevertCannotForceAnAlreadyRevertedConversion(t *testing.T) {
	t.Parallel()

	g, fc, _, _ := newConvertRig(t)
	fc.revert = func(int64, bool) (*localeconv.RevertResult, error) {
		return nil, &localeconv.BlockingError{Issues: []localeconv.Issue{{Code: localeconv.CodeAlreadyReverted, Message: "Conversion 2 was already reverted"}}}
	}
	g.elevate("owner-token")
	rec := g.do("POST", convertBase+"/history/2/revert", "owner-token", url.Values{"confirm_phrase": {"revert"}}, nil)
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), `name="force"`) {
		t.Errorf("%d: force is offered for a conversion that cannot be reverted", rec.Code)
	}
}

func TestLocalesTabLinksToTheConversion(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newConvertRig(t)
	found := true
	g.a.deps.LocaleConvertCandidates = func(context.Context) (bool, error) { return found, nil }
	shown := func() bool {
		body := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil).Body.String()
		return strings.Contains(body, "Moving from region page trees?") && strings.Contains(body, `href="`+convertBase+`"`)
	}
	if !shown() {
		t.Error("a single-locale site with region pages lacks the conversion link")
	}
	if strings.Contains(g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(), "Region pages found") {
		t.Error("General still shows the region page notice")
	}
	found = false
	if shown() {
		t.Error("the link shows on a site without region pages")
	}
	found = true
	g.a.deps.Site.(*fakeSite).cfg.Locales = []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}}
	if shown() {
		t.Error("the link shows on a multi-locale site")
	}
}

// convertMultipart posts a multipart form with one oversized field.
func convertMultipart(g *rig, path string, fields map[string]string) *httptest.ResponseRecorder {
	g.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(req, "owner-token", nil)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func TestLocaleConvertCapsTheBodyBeforeItIsRead(t *testing.T) {
	t.Parallel()

	g, fc, _, _ := newConvertRig(t)
	g.elevate("owner-token")
	huge := strings.Repeat("a", convertBodyLimit+1)
	for _, path := range []string{"/report", "/suggest", "/apply", "/history/3/revert"} {
		if rec := convertMultipart(g, convertBase+path, map[string]string{"plan": huge}); rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("multipart %s: %d, want 413", path, rec.Code)
		}
	}
	if len(fc.applied) != 0 {
		t.Error("an oversized body was applied")
	}

	// A declared size over the cap is refused before the body is touched.
	req := httptest.NewRequest("POST", convertBase+"/report", strings.NewReader("x"))
	req.ContentLength = convertBodyLimit + 1
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(req, "owner-token", nil)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "larger than 1 MiB") {
		t.Errorf("declared size: %d", rec.Code)
	}

	// A normal plan still passes.
	if rec := g.do("POST", convertBase+"/report", "owner-token", url.Values{"plan": {convertPlanJSON}}, nil); rec.Code != http.StatusOK {
		t.Errorf("normal plan: %d", rec.Code)
	}
}
