package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/seo"
)

const (
	seo404Path       = "/settings/seo/404-monitor"
	seo404DeletePath = seo404Path + "/delete-all"
	// seo404Phrase is typed to confirm clearing the whole 404 log.
	seo404Phrase = "delete 404 log"
)

type seo404Data struct {
	Summary    []ui.FieldError
	Errs       map[string]string
	State      seo.Monitor404
	Retention  []ui.Option
	Patterns   string
	MaxPattern int
	Logged     string // "1,811 missing addresses logged.", empty without a store
	LogHref    string
	DeleteHref string
}

// monitor404FromForm applies the posted options over stored. A field the post
// does not carry keeps its stored value (a switch posts "true" or "false").
// The form says "log requests", the stored flag is its inverse.
func monitor404FromForm(form url.Values, stored seo.Monitor404) seo.Monitor404 {
	m := stored
	m.IgnorePatterns = append([]string(nil), stored.IgnorePatterns...)
	if form.Has("enabled") {
		m.Disabled = form.Get("enabled") != "true"
	}
	if form.Has("retention_days") {
		// A value that is not a number is kept as -1 so validation refuses it.
		days, err := strconv.Atoi(strings.TrimSpace(form.Get("retention_days")))
		if err != nil {
			days = -1
		}
		m.RetentionDays = days
	}
	if form.Has("ignore_patterns") {
		m.IgnorePatterns = nil
		for _, l := range strings.Split(form.Get("ignore_patterns"), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				m.IgnorePatterns = append(m.IgnorePatterns, l)
			}
		}
	}
	return m
}

func retention404Options(m seo.Monitor404) []ui.Option {
	opts := make([]ui.Option, 0, len(seo.Retention404Options))
	for _, d := range seo.Retention404Options {
		opts = append(opts, ui.Option{Value: strconv.Itoa(d), Label: strconv.Itoa(d) + " days", Selected: d == m.Days()})
	}
	return opts
}

func (a *Admin) handleSEO404(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEO404(w, r, http.StatusOK, set.Monitor404, nil)
}

func (a *Admin) handleSEO404Save(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	next := monitor404FromForm(r.PostForm, set.Monitor404)
	probe := set.Clone()
	probe.Monitor404 = next
	var errs []ui.FieldError
	for _, fe := range probe.Validate() {
		if strings.HasPrefix(fe.Field, "monitor404[") {
			errs = append(errs, ui.FieldError{ID: seoFieldID(fe.Field), Label: seoFieldLabel(fe.Field), Message: fe.Message})
		}
	}
	if len(errs) > 0 {
		a.renderSEO404(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	// The patterns stay out of the audit entry.
	if !a.seoSave(w, r, func(s *seo.Settings) { s.Monitor404 = next }, map[string]any{"tab": "404-monitor"}) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seo404Path, http.StatusSeeOther)
}

func (a *Admin) renderSEO404(w http.ResponseWriter, r *http.Request, status int, m seo.Monitor404, errs []ui.FieldError) {
	d := seo404Data{
		Summary: errs, Errs: map[string]string{}, State: m,
		Retention: retention404Options(m), Patterns: strings.Join(m.IgnorePatterns, "\n"), MaxPattern: seo.MaxIgnorePatterns404,
		LogHref: a.adminPath + "/settings/redirects/404", DeleteHref: a.adminPath + seo404DeletePath,
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	if rs := a.deps.Growth.Redirects; rs != nil {
		if _, total, err := rs.List404Entries(r.Context(), redirect.NotFoundListParams{Page: 1, PerPage: 1}); err == nil {
			d.Logged = thousands(total) + " missing " + map[bool]string{true: "address", false: "addresses"}[total == 1] + " logged."
		}
	}
	view := a.NewView(w, r, "SEO")
	view.Section, view.Tab = "seo", "seo-404"
	view.Width = "narrow"
	view.Data = d
	a.Render(w, r, status, "settings/seo-404", view)
}

func (a *Admin) seo404Danger(r *http.Request) ui.DangerConfirm {
	return ui.DangerConfirm{
		ID:     "dc-seo-404",
		Title:  "Delete all 404 entries?",
		Phrase: seo404Phrase,
		Prompt: "To delete the whole 404 log, type " + seo404Phrase + " below.",
		Action: a.adminPath + seo404DeletePath,
		Back:   a.adminPath + seo404Path,
		Label:  "Delete all entries",
		CSRF:   csrfToken(r.Context()),
		Body: []ui.DangerText{
			{Parts: []ui.DangerPart{{Text: "Every missing address in the log is removed, with its hit count. Redirects you made from it stay. Addresses that are still missing are logged again the next time they are requested."}}},
			{Parts: []ui.DangerPart{{Text: "This cannot be undone."}}},
		},
	}
}

type seo404DeleteData struct{ Danger ui.DangerConfirm }

func (a *Admin) renderSEO404Delete(w http.ResponseWriter, r *http.Request, status int, check bool) {
	dc := a.seo404Danger(r)
	if check {
		dc, _ = CheckDangerPhrase(r, dc)
	}
	v := a.NewView(w, r, "Delete all 404 entries")
	v.Section, v.Tab = "seo", "seo-404"
	v.Width = "compact"
	v.Crumbs = a.areaCrumbs("seo-404", Crumb{Label: "Delete all"})
	v.Data = seo404DeleteData{Danger: dc}
	a.Render(w, r, status, "settings/seo-404-delete", v)
}

func (a *Admin) handleSEO404Delete(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.redirectStore(w, r); ok {
		a.renderSEO404Delete(w, r, http.StatusOK, false)
	}
}

func (a *Admin) handleSEO404DeleteSave(w http.ResponseWriter, r *http.Request) {
	rs, ok := a.redirectStore(w, r)
	if !ok {
		return
	}
	if _, ok := CheckDangerPhrase(r, a.seo404Danger(r)); !ok {
		a.renderSEO404Delete(w, r, http.StatusUnprocessableEntity, true)
		return
	}
	n, err := rs.DeleteAll404Entries(r.Context())
	if err != nil {
		a.logger().Error("404 log clear", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The 404 log could not be cleared. Nothing changed.")
		return
	}
	// The count only: the paths are not audit material.
	a.audit(r, "404log.cleared", "404log", map[string]any{"deleted": n})
	a.SetFlash(w, r, "success", "404 log cleared.")
	http.Redirect(w, r, a.adminPath+seo404Path, http.StatusSeeOther)
}
