package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/localeconv"
	"hatamoto.dev/core/internal/siteconfig"
)

const (
	// convertMaxPlan is the longest plan, in bytes, the page accepts. It is
	// checked right after the form is read and before the plan is parsed. The
	// request body is capped at convertBodyLimit by limitConvertBody before
	// the CSRF guard reads the form.
	convertMaxPlan = 1 << 20
	// convertBodyLimit is the plan plus room for the other form fields and
	// the multipart framing.
	convertBodyLimit = convertMaxPlan + 1<<20

	convertApplyPhrase  = "convert"
	convertRevertPhrase = "revert"

	// convertReviewTTL and convertReviewCap bound the reviewed plans kept in
	// memory between a passing dry run and the confirm page.
	convertReviewTTL = 30 * time.Minute
	convertReviewCap = 16
)

// convertReviewStore remembers the plan text and the data fingerprint of a dry
// run that passed, by the plan hash of its report. The confirm page is a plain
// GET behind sudo (a sudo prompt never replays a POST), and a plan of up to
// 1 MiB cannot ride in a URL, so the link carries only the hash. The
// fingerprint comes from here, never from the link.
type convertReviewStore struct {
	mu    sync.Mutex
	items map[string]convertReview
}

type convertReview struct {
	plan, fp string
	at       time.Time
}

func (s *convertReviewStore) put(sha, plan, fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.items == nil {
		s.items = map[string]convertReview{}
	}
	for k, v := range s.items {
		if now.Sub(v.at) > convertReviewTTL {
			delete(s.items, k)
		}
	}
	for len(s.items) >= convertReviewCap {
		oldest := ""
		for k, v := range s.items {
			if oldest == "" || v.at.Before(s.items[oldest].at) {
				oldest = k
			}
		}
		delete(s.items, oldest)
	}
	s.items[sha] = convertReview{plan: plan, fp: fp, at: now}
}

func (s *convertReviewStore) get(sha string) (plan, fp string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[sha]
	if !ok || time.Since(v.at) > convertReviewTTL {
		return "", "", false
	}
	return v.plan, v.fp, true
}

// convertData is the Convert page: the plan, its report and the history.
type convertData struct {
	Plan    string
	Summary []ui.FieldError
	// PlanError is the field error of the plan textarea.
	PlanError string
	// Issues are blocking problems of the last action, Warnings advice.
	Issues, Warnings []convertIssue
	Report           *convertReport
	History          []convertHistoryRow
	// ReportURL and SuggestURL are the two form targets.
	ReportURL, SuggestURL string
}

// convertHistoryRow is one past conversion; RevertHref is empty once reverted.
type convertHistoryRow struct {
	Name, Detail, RevertHref string
	Status                   ui.Badge
}

// convertIssue is one report or validation message.
type convertIssue struct{ Code, Message string }

// convertReport is the dry-run report as the page shows it.
//
// Locales and Checks are rendered hm/datatable partials: the page is a plan
// editor, not a list with a create form, so it must not trip UI-10 by naming
// the table next to its form.
type convertReport struct {
	OK              bool
	Locales, Checks template.HTML
	ApplyHref       string
}

const (
	convertApplyDescription  = "Confirm this change to your region pages. You can revert it afterwards."
	convertRevertDescription = "Confirm that you want to undo this conversion."
)

// convertConfirmData is the typed confirm page of Apply and Revert.
type convertConfirmData struct {
	// Description is the line under the page title.
	Description string
	Danger      ui.DangerConfirm
	// Issues are what refused the last attempt.
	Issues []convertIssue
}

func (a *Admin) convertURL(parts ...string) string {
	return a.url(append([]string{"settings", "general", "locales", "convert"}, parts...)...)
}

// limitConvertBody caps what a Convert form may post before anything reads it
// (the CSRF check parses the body). base is the Convert page URL. A declared
// size over the cap is refused with a clear message.
func limitConvertBody(base string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, base) {
				if r.ContentLength > convertBodyLimit {
					http.Error(w, "The plan is larger than 1 MiB. Split it into smaller plans.", http.StatusRequestEntityTooLarge)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, convertBodyLimit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// mountLocaleConvert mounts the Convert page: owners only, and the role check
// runs first so an editor gets 403 and never a password prompt. The page, the
// suggestion and the dry run change nothing; the confirm pages and everything
// that writes sit behind sudo, the confirm itself being a GET so the prompt
// can come back to it.
func (a *Admin) mountLocaleConvert(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/general/locales/convert", a.handleConvert)
	owner.HandleFunc("POST /settings/general/locales/convert/suggest", a.handleConvertSuggest)
	owner.HandleFunc("POST /settings/general/locales/convert/report", a.handleConvertReport)
	sudo := owner.Group("", a.RequireSudo)
	sudo.HandleFunc("GET /settings/general/locales/convert/apply", a.handleConvertApplyConfirm)
	sudo.HandleFunc("POST /settings/general/locales/convert/apply", a.handleConvertApply)
	sudo.HandleFunc("GET /settings/general/locales/convert/history/{id}/revert", a.handleConvertRevertConfirm)
	sudo.HandleFunc("POST /settings/general/locales/convert/history/{id}/revert", a.handleConvertRevert)
}

// convertReady answers 503 when there is no conversion service.
func (a *Admin) convertReady(w http.ResponseWriter, r *http.Request) bool {
	if a.deps.LocaleConvert == nil {
		a.unavailable(w, r, "Converting region pages")
		return false
	}
	return true
}

func (a *Admin) renderConvert(w http.ResponseWriter, r *http.Request, status int, d convertData) {
	hist, err := a.deps.LocaleConvert.History(r.Context())
	if err != nil {
		a.logger().Error("locale conversion history failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The conversion history could not be loaded.")
		return
	}
	d.History = a.convertHistory(hist)
	d.ReportURL, d.SuggestURL = a.convertURL("report"), a.convertURL("suggest")
	if d.PlanError != "" {
		d.Summary = append(d.Summary, ui.FieldError{ID: "plan", Label: "Plan", Message: d.PlanError})
	}
	v := a.NewView(w, r, "Convert region pages")
	v.Section, v.Tab = "general", "general"
	v.Crumbs = a.areaCrumbs("general", Crumb{Label: "Convert regions"})
	v.Data = d
	a.Render(w, r, status, "settings/locale-convert", v)
}

func (a *Admin) handleConvert(w http.ResponseWriter, r *http.Request) {
	if a.convertReady(w, r) {
		a.renderConvert(w, r, http.StatusOK, convertData{})
	}
}

// parseConvertForm reads the posted form, urlencoded or multipart. The body is
// already capped by limitConvertBody; a read past the cap is a
// *http.MaxBytesError.
func parseConvertForm(r *http.Request) error {
	if err := r.ParseMultipartForm(convertBodyLimit); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return err
	}
	return nil
}

func isTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}

// convertFormError answers a form that could not be read: 413 when it ran past
// the body cap, 400 for anything else (a malformed body).
func (a *Admin) convertFormError(w http.ResponseWriter, r *http.Request, err error, tooLarge string) {
	if isTooLarge(err) {
		a.Error(w, r, http.StatusRequestEntityTooLarge, "Too large", tooLarge)
		return
	}
	a.Error(w, r, http.StatusBadRequest, "Bad request", "The request could not be read.")
}

// convertPlanText reads the posted plan. It reports false, with the page already
// answered, when the plan is too large.
func (a *Admin) convertPlanText(w http.ResponseWriter, r *http.Request) (string, bool) {
	if err := parseConvertForm(r); err != nil {
		if isTooLarge(err) {
			a.renderConvert(w, r, http.StatusRequestEntityTooLarge, convertData{PlanError: "The plan is larger than 1 MiB"})
		} else {
			a.renderConvert(w, r, http.StatusBadRequest, convertData{PlanError: "The request could not be read"})
		}
		return "", false
	}
	plan := strings.TrimSpace(r.PostFormValue("plan"))
	if len(plan) > convertMaxPlan {
		a.renderConvert(w, r, http.StatusRequestEntityTooLarge, convertData{PlanError: "The plan is larger than 1 MiB"})
		return "", false
	}
	return plan, true
}

func (a *Admin) handleConvertSuggest(w http.ResponseWriter, r *http.Request) {
	if !a.convertReady(w, r) {
		return
	}
	// Suggest ignores the posted plan, but reading the form applies the body
	// cap and answers a malformed body like the other routes.
	if _, ok := a.convertPlanText(w, r); !ok {
		return
	}
	plan, warnings, err := a.deps.LocaleConvert.Suggest(r.Context())
	d := convertData{Warnings: convertIssues(warnings)}
	if errors.Is(err, localeconv.ErrNoRegionTree) {
		d.PlanError = "No region tree found: this site has no group of top-level pages with children to turn into locales."
		a.renderConvert(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	if err != nil {
		if a.convertFailed(w, r, err) {
			return
		}
		d.PlanError = err.Error()
		var be *localeconv.BlockingError
		if errors.As(err, &be) {
			d.PlanError, d.Issues = "", convertIssues(be.Issues)
		}
		a.renderConvert(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	text, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		a.logger().Error("locale conversion suggestion could not be shown", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The suggestion could not be shown.")
		return
	}
	d.Plan = string(text)
	a.renderConvert(w, r, http.StatusOK, d)
}

func (a *Admin) handleConvertReport(w http.ResponseWriter, r *http.Request) {
	if !a.convertReady(w, r) {
		return
	}
	text, ok := a.convertPlanText(w, r)
	if !ok {
		return
	}
	d := convertData{Plan: text}
	if text == "" {
		d.PlanError = "Paste a plan, or use Suggest to start from your pages"
		a.renderConvert(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	plan, err := localeconv.ParsePlan(strings.NewReader(text))
	if err != nil {
		d.PlanError = sentence(err.Error())
		a.renderConvert(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	rep, err := a.deps.LocaleConvert.DryRun(r.Context(), plan)
	var be *localeconv.BlockingError
	switch {
	case errors.As(err, &be):
		d.Issues = convertIssues(be.Issues)
		if be.Report != nil {
			rep = be.Report
		}
	case err != nil:
		if !a.convertFailed(w, r, err) {
			a.renderConvert(w, r, http.StatusUnprocessableEntity, d)
		}
		return
	}
	if rep != nil {
		if d.Report, err = a.convertReportView(rep); err != nil {
			a.logger().Error("locale conversion report could not be shown", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The report could not be shown.")
			return
		}
		if be == nil {
			d.Issues = convertIssues(rep.Errors)
		}
		d.Warnings = convertIssues(rep.Warnings)
		if rep.OK && len(d.Issues) == 0 && rep.DataFingerprint == "" {
			d.Issues = []convertIssue{{Message: "The report has no data fingerprint, so the apply cannot be tied to it. Run the dry run again"}}
		}
		if rep.OK && len(d.Issues) == 0 {
			a.convertReviews.put(rep.PlanSHA256, text, rep.DataFingerprint)
			d.Report.ApplyHref = a.convertURL("apply") + "?" + url.Values{"plan": {rep.PlanSHA256}}.Encode()
		} else {
			d.Report.OK = false
		}
	}
	a.renderConvert(w, r, http.StatusOK, d)
}

// convertFailed answers a service failure that is not a plain refusal: 503
// when the service is not there yet, 500 otherwise. It reports whether it
// answered.
func (a *Admin) convertFailed(w http.ResponseWriter, r *http.Request, err error) bool {
	var be *localeconv.BlockingError
	if errors.As(err, &be) {
		return false
	}
	if errors.Is(err, localeconv.ErrNotImplemented) {
		a.unavailable(w, r, "Converting region pages")
		return true
	}
	a.logger().Error("locale conversion failed", "err", err)
	a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The conversion could not run. Nothing changed.")
	return true
}

func convertIssues(in []localeconv.Issue) []convertIssue {
	var out []convertIssue
	for _, i := range in {
		out = append(out, convertIssue{Code: i.Code, Message: i.Message})
	}
	return out
}

func (a *Admin) convertReportView(rep *localeconv.Report) (*convertReport, error) {
	schemas := map[string]bool{}
	for _, l := range rep.Locales {
		for s := range l.CountsBySchema {
			schemas[s] = true
		}
	}
	for s := range rep.Shared.CountsBySchema {
		schemas[s] = true
	}
	names := make([]string, 0, len(schemas))
	for s := range schemas {
		names = append(names, s)
	}
	sort.Strings(names)
	counts := func(m map[string]int, total int) []string {
		var cells []string
		for _, s := range names {
			cells = append(cells, strconv.Itoa(m[s]))
		}
		return append(cells, strconv.Itoa(total))
	}
	cols := append(append([]string{"Locale", "Home found"}, names...), "Total")
	t := ui.DataTable{Caption: "Pages by locale and type", Columns: cols}
	for _, l := range rep.Locales {
		home := l.Home.OldPath
		if l.Home.NewPath != "" && l.Home.NewPath != l.Home.OldPath {
			home += " to " + l.Home.NewPath
		}
		if l.Home.ID != "" {
			home += " (id " + shortID(l.Home.ID) + ")"
		}
		t.Rows = append(t.Rows, ui.DataRow{Label: l.Tag, Cells: append([]string{home}, counts(l.CountsBySchema, l.Total)...)})
	}
	t.Rows = append(t.Rows, ui.DataRow{Label: "shared (und)", Cells: append([]string{"none"}, counts(rep.Shared.CountsBySchema, rep.Shared.Total)...)})
	locales, err := a.RenderPartial("hm/datatable", t)
	if err != nil {
		return nil, err
	}
	checks, err := a.RenderPartial("hm/datatable", convertChecks(rep))
	if err != nil {
		return nil, err
	}
	return &convertReport{OK: true, Locales: locales, Checks: checks}, nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// convertChecks is the summary table under the locale table.
func convertChecks(rep *localeconv.Report) ui.DataTable {
	sample := func(items []string, total int) string {
		if len(items) == 0 {
			return "none"
		}
		s := strings.Join(items, ", ")
		if total > len(items) {
			s += ", and more"
		}
		return s
	}
	var paths, renamed, overrides, redirects, unmatched []string
	for i, p := range rep.PathChanges.Items {
		if i < 3 {
			paths = append(paths, p.Old+" to "+p.New)
		}
	}
	for i, c := range rep.RenamedCollisions {
		if i < 3 {
			renamed = append(renamed, c.Schema+": "+c.OldSlug+" to "+c.NewSlug)
		}
	}
	for _, o := range rep.Overrides.SitemapGroupRemoved {
		overrides = append(overrides, o.Schema)
	}
	for i, p := range rep.Redirects.ItemsSample {
		if i < 3 {
			redirects = append(redirects, p)
		}
	}
	for _, u := range rep.UnmatchedValues {
		unmatched = append(unmatched, fmt.Sprintf("%s=%s (%d)", u.Attribute, u.Value, u.Count))
	}
	naming, namingReason := rep.SiteSettings.SitemapNaming[1], "one file per locale and group"
	switch naming {
	case localeconv.NamingLegacy:
		namingReason = "the conversion removed per-locale sitemap groups; keeps the previous file names"
	case "":
		// A report stored before the setting existed.
		naming, namingReason = "not recorded", "this conversion ran before the setting existed"
	}
	groups := fmt.Sprintf("%d singletons, %d conflicts", rep.Groups.Singletons, len(rep.Groups.Conflicts))
	return ui.DataTable{
		Caption: "Checks",
		Columns: []string{"Check", "Count", "Detail"},
		Rows: []ui.DataRow{
			{Label: "Path changes", Cells: []string{strconv.Itoa(rep.PathChanges.Count), sample(paths, rep.PathChanges.Count)}},
			{Label: "Renamed collisions", Cells: []string{strconv.Itoa(len(rep.RenamedCollisions)), sample(renamed, len(rep.RenamedCollisions))}},
			{Label: "Groups formed", Cells: []string{strconv.Itoa(rep.Groups.Formed), groups}},
			{Label: "Overrides updated (sitemap group dropped)", Cells: []string{strconv.Itoa(len(rep.Overrides.SitemapGroupRemoved)), sample(overrides, len(overrides))}},
			{Label: "Sitemap file names", Cells: []string{naming, namingReason}},
			{Label: "Redirects under segments", Cells: []string{strconv.Itoa(rep.Redirects.Count), "for information; " + sample(redirects, rep.Redirects.Count)}},
			{Label: "Unmatched values", Cells: []string{strconv.Itoa(len(rep.UnmatchedValues)), sample(unmatched, len(unmatched))}},
		},
	}
}

func (a *Admin) convertHistory(items []localeconv.HistoryItem) []convertHistoryRow {
	var rows []convertHistoryRow
	for _, h := range items {
		id := strconv.FormatInt(h.ID, 10)
		row := convertHistoryRow{
			Name:   "Conversion #" + id,
			Detail: fmt.Sprintf("Applied %s by %s. %s, %d pages.", fmtDateTime(h.AppliedAt), h.AppliedBy, strings.Join(h.Locales, ", "), h.Rows),
			Status: ui.Badge{Label: "Applied", Kind: "success"},
		}
		if h.Status == "reverted" {
			row.Status = ui.Badge{Label: "Reverted"}
		} else {
			row.RevertHref = a.convertURL("history", id, "revert")
		}
		rows = append(rows, row)
	}
	return rows
}

// convertApplyDanger is the typed confirm of Apply; it re-posts the reviewed
// plan and the report hashes.
func (a *Admin) convertApplyDanger(r *http.Request, plan, sha, fp string) ui.DangerConfirm {
	return ui.DangerConfirm{
		ID:     "dc-convert",
		Title:  "Convert region pages to locales",
		Phrase: convertApplyPhrase,
		Prompt: "To convert, type " + convertApplyPhrase + " below.",
		Action: a.convertURL("apply"),
		Back:   a.convertURL(),
		Label:  "Convert now",
		CSRF:   csrfToken(r.Context()),
		Hidden: []ui.ConfirmHidden{{Name: "plan", Value: plan}, {Name: "plan_sha256", Value: sha}, {Name: "data_fingerprint", Value: fp}},
		Body: []ui.DangerText{
			{Parts: []ui.DangerPart{{Text: "This retags your pages, groups translations, moves region children to the top level and saves the Locales settings, all at once. Public addresses stay the same."}}},
			{Parts: []ui.DangerPart{{Text: "Editors who save a page while it runs wait a few seconds. You can undo it from the history on the previous page. Servers running the old settings pick the change up within a minute."}}},
		},
	}
}

func (a *Admin) renderConvertConfirm(w http.ResponseWriter, r *http.Request, status int, dc ui.DangerConfirm, issues []convertIssue, title, description string) {
	v := a.NewView(w, r, title)
	v.Section, v.Tab = "general", "general"
	v.Width = "compact"
	v.Crumbs = a.areaCrumbs("general", Crumb{Label: "Convert regions", Href: a.convertURL()}, Crumb{Label: "Confirm"})
	v.Data = convertConfirmData{Description: description, Danger: dc, Issues: issues}
	a.Render(w, r, status, "settings/locale-convert-confirm", v)
}

func (a *Admin) handleConvertApplyConfirm(w http.ResponseWriter, r *http.Request) {
	if !a.convertReady(w, r) {
		return
	}
	sha := r.URL.Query().Get("plan")
	plan, fp, ok := a.convertReviews.get(sha)
	if !ok || fp == "" {
		a.SetFlash(w, r, "warning", "That dry run is no longer available. Run it again.")
		http.Redirect(w, r, a.convertURL(), http.StatusSeeOther)
		return
	}
	a.renderConvertConfirm(w, r, http.StatusOK, a.convertApplyDanger(r, plan, sha, fp), nil, "Convert region pages", convertApplyDescription)
}

func (a *Admin) handleConvertApply(w http.ResponseWriter, r *http.Request) {
	if !a.convertReady(w, r) {
		return
	}
	if err := parseConvertForm(r); err != nil {
		a.convertFormError(w, r, err, "The plan is larger than 1 MiB.")
		return
	}
	text, sha, fp := r.PostFormValue("plan"), r.PostFormValue("plan_sha256"), r.PostFormValue("data_fingerprint")
	if len(text) > convertMaxPlan {
		a.Error(w, r, http.StatusRequestEntityTooLarge, "Too large", "The plan is larger than 1 MiB.")
		return
	}
	dc := a.convertApplyDanger(r, text, sha, fp)
	dc, ok := CheckDangerPhrase(r, dc)
	if !ok {
		a.renderConvertConfirm(w, r, http.StatusUnprocessableEntity, dc, nil, "Convert region pages", convertApplyDescription)
		return
	}
	plan, err := localeconv.ParsePlan(strings.NewReader(text))
	if err != nil {
		a.renderConvert(w, r, http.StatusUnprocessableEntity, convertData{Plan: text, PlanError: "The plan could not be read. Run the dry run again"})
		return
	}
	if fp == "" || localeconv.PlanSHA256(plan) != sha {
		a.renderConvert(w, r, http.StatusUnprocessableEntity, convertData{Plan: text, PlanError: "This plan is not the one that was reviewed. Run the dry run again"})
		return
	}
	rec, rep, err := a.deps.LocaleConvert.Apply(r.Context(), plan, localeconv.ApplyOpts{By: userLabel(userFrom(r.Context())), Fingerprint: fp})
	var be *localeconv.BlockingError
	if errors.As(err, &be) {
		status := http.StatusUnprocessableEntity
		for _, i := range be.Issues {
			if i.Code == localeconv.CodeStaleReport {
				status = http.StatusConflict
			}
		}
		a.renderConvert(w, r, status, convertData{Plan: text, Issues: convertIssues(be.Issues)})
		return
	}
	if err != nil {
		a.convertFailed(w, r, err)
		return
	}
	a.audit(r, "locales.convert.apply", strconv.FormatInt(rec.ID, 10), map[string]any{"rows": rec.Rows, "plan_sha256": sha})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	msg := fmt.Sprintf("Converted %d pages to locales.", rec.Rows)
	if rep != nil && len(rep.Warnings) > 0 {
		msg += fmt.Sprintf(" %d warnings, see the report.", len(rep.Warnings))
	}
	a.SetFlash(w, r, "success", msg)
	http.Redirect(w, r, a.convertURL(), http.StatusSeeOther)
}

// convertRevertDanger is the typed confirm of Revert. force carries the
// owner's choice to revert past refusals.
func (a *Admin) convertRevertDanger(r *http.Request, id int64, force bool) ui.DangerConfirm {
	n := strconv.FormatInt(id, 10)
	dc := ui.DangerConfirm{
		ID:     "dc-convert-revert",
		Title:  "Revert conversion #" + n,
		Phrase: convertRevertPhrase,
		Prompt: "To revert, type " + convertRevertPhrase + " below.",
		Action: a.convertURL("history", n, "revert"),
		Back:   a.convertURL(),
		Label:  "Revert conversion",
		CSRF:   csrfToken(r.Context()),
		Body: []ui.DangerText{
			{Parts: []ui.DangerPart{{Text: "This puts back the locales, groups, parents, slugs and Locales settings the conversion recorded."}}},
		},
	}
	if force {
		dc.Label = "Revert anyway"
		dc.Hidden = []ui.ConfirmHidden{{Name: "force", Value: "1"}}
		dc.Body = append(dc.Body, ui.DangerText{Parts: []ui.DangerPart{{Text: "Pages changed since the conversion keep your later edits wherever they can; the ones listed above are overwritten.", Strong: true}}})
	}
	return dc
}

func convertID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (a *Admin) handleConvertRevertConfirm(w http.ResponseWriter, r *http.Request) {
	if !a.convertReady(w, r) {
		return
	}
	id, ok := convertID(r)
	if !ok {
		a.Error(w, r, http.StatusNotFound, "Not found", "There is no such conversion.")
		return
	}
	a.renderConvertConfirm(w, r, http.StatusOK, a.convertRevertDanger(r, id, false), nil, "Revert conversion", convertRevertDescription)
}

func (a *Admin) handleConvertRevert(w http.ResponseWriter, r *http.Request) {
	if !a.convertReady(w, r) {
		return
	}
	id, ok := convertID(r)
	if !ok {
		a.Error(w, r, http.StatusNotFound, "Not found", "There is no such conversion.")
		return
	}
	if err := parseConvertForm(r); err != nil {
		a.convertFormError(w, r, err, "The request is larger than 1 MiB.")
		return
	}
	force := r.PostFormValue("force") == "1"
	dc, ok := CheckDangerPhrase(r, a.convertRevertDanger(r, id, force))
	if !ok {
		a.renderConvertConfirm(w, r, http.StatusUnprocessableEntity, dc, nil, "Revert conversion", convertRevertDescription)
		return
	}
	res, err := a.deps.LocaleConvert.Revert(r.Context(), id, force, userLabel(userFrom(r.Context())))
	var be *localeconv.BlockingError
	if errors.As(err, &be) {
		// Only conflicts can be overridden; a conversion that is already
		// reverted or has a newer one on top cannot.
		canForce := !force
		for _, i := range be.Issues {
			if i.Code != localeconv.CodeRevertConflict {
				canForce = false
			}
		}
		a.renderConvertConfirm(w, r, http.StatusConflict, a.convertRevertDanger(r, id, canForce), convertIssues(be.Issues), "Revert conversion", convertRevertDescription)
		return
	}
	if err != nil {
		a.convertFailed(w, r, err)
		return
	}
	a.audit(r, "locales.convert.revert", strconv.FormatInt(id, 10), map[string]any{"restored": res.Restored, "forced": len(res.Forced), "stranded": res.Stranded})
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	msg := fmt.Sprintf("Reverted conversion #%d: %d pages restored.", id, res.Restored)
	if res.Stranded > 0 {
		msg += fmt.Sprintf(" %d pages made since then keep their locale.", res.Stranded)
	}
	a.SetFlash(w, r, "success", msg)
	http.Redirect(w, r, a.convertURL(), http.StatusSeeOther)
}

// convertCandidates is true when the Locales tab should point an owner at the
// conversion: the site has no Locales list yet and its pages look like a
// region tree. A failed check shows no notice rather than a broken page.
func (a *Admin) convertCandidates(r *http.Request, cfg *siteconfig.Config) bool {
	if a.deps.LocaleConvert == nil || a.deps.LocaleConvertCandidates == nil || localeSetOf(cfg).Multi() {
		return false
	}
	if u := userFrom(r.Context()); u == nil || u.Role != "owner" {
		return false
	}
	found, err := a.deps.LocaleConvertCandidates(r.Context())
	if err != nil {
		a.logger().Warn("region page check failed", "err", err)
		return false
	}
	return found
}
