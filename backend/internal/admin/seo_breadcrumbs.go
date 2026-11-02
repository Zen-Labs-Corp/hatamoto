package admin

import (
	"net/http"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
)

const seoBreadcrumbsPath = "/settings/seo/breadcrumbs"

type seoBreadcrumbsData struct {
	Summary    []ui.FieldError
	Errs       map[string]string
	State      seo.Breadcrumbs
	Separators []ui.Option
	Preview    string
	MaxLabel   int
}

// breadcrumbsFromForm applies the posted options over stored. A field the post
// does not carry keeps its stored value (a switch posts "true" or "false").
// The home crumb switch is inverted: the stored flag hides it.
func breadcrumbsFromForm(form url.Values, stored seo.Breadcrumbs) seo.Breadcrumbs {
	b := stored
	if form.Has("home_label") {
		b.HomeLabel = strings.TrimSpace(form.Get("home_label"))
	}
	if form.Has("separator") {
		b.Separator = strings.TrimSpace(form.Get("separator"))
	}
	if form.Has("show_home") {
		b.HideHome = form.Get("show_home") != "true"
	}
	if form.Has("hide_current") {
		b.HideCurrent = form.Get("hide_current") == "true"
	}
	if form.Has("show_term") {
		b.ShowTerm = form.Get("show_term") == "true"
	}
	return b
}

// breadcrumbsPreview shows a sample trail the way the options shape it.
func breadcrumbsPreview(b seo.Breadcrumbs) string {
	sep := b.Separator
	if sep == "" {
		sep = "/"
	}
	var parts []string
	if !b.HideHome {
		parts = append(parts, b.Home())
	}
	if b.ShowTerm {
		parts = append(parts, "Guides")
	}
	if !b.HideCurrent {
		parts = append(parts, "Install Hatamoto")
	}
	return strings.Join(parts, " "+sep+" ")
}

func breadcrumbSeparatorOptions(selected string) []ui.Option {
	opts := []ui.Option{{Value: "", Label: "Theme default", Selected: selected == ""}}
	for _, s := range seo.CrumbSeparators {
		opts = append(opts, ui.Option{Value: s, Label: s, Selected: s == selected})
	}
	return opts
}

func (a *Admin) handleSEOBreadcrumbs(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEOBreadcrumbs(w, r, http.StatusOK, set.Breadcrumbs, nil)
}

func (a *Admin) handleSEOBreadcrumbsSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	next := breadcrumbsFromForm(r.PostForm, set.Breadcrumbs)
	probe := set.Clone()
	probe.Breadcrumbs = next
	var errs []ui.FieldError
	for _, fe := range probe.Validate() {
		if strings.HasPrefix(fe.Field, "breadcrumbs[") {
			errs = append(errs, ui.FieldError{ID: seoFieldID(fe.Field), Label: seoFieldLabel(fe.Field), Message: fe.Message})
		}
	}
	if len(errs) > 0 {
		a.renderSEOBreadcrumbs(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	if !a.seoSave(w, r, func(s *seo.Settings) { s.Breadcrumbs = next }, map[string]any{"tab": "breadcrumbs"}) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoBreadcrumbsPath, http.StatusSeeOther)
}

func (a *Admin) renderSEOBreadcrumbs(w http.ResponseWriter, r *http.Request, status int, b seo.Breadcrumbs, errs []ui.FieldError) {
	d := seoBreadcrumbsData{
		Summary: errs, Errs: map[string]string{}, State: b, Separators: breadcrumbSeparatorOptions(b.Separator),
		Preview: breadcrumbsPreview(b), MaxLabel: seo.MaxCrumbHomeLabel,
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	view := a.NewView(w, r, "SEO")
	view.Section, view.Tab = "seo", "seo-breadcrumbs"
	view.Width = "narrow"
	view.Data = d
	a.Render(w, r, status, "settings/seo-breadcrumbs", view)
}
