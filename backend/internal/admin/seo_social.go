package admin

import (
	"net/http"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
)

const seoSocialPath = "/settings/seo/social"

// seoSocialTypeRow is one content type of the Social tab.
type seoSocialTypeRow struct {
	Slug, Name   string
	Title, Image string
}

type seoSocialPreviewLine struct{ Label, Title, Image string }

// socialState is everything the Social tab edits, as stored or as submitted.
type socialState struct {
	Set   seo.Social
	Types []seoSocialTypeRow
	Kinds []string
	// Rest is the stored settings the preview resolves templates against.
	Rest seo.Settings
}

type seoSocialData struct {
	Summary     []ui.FieldError
	Errs        map[string]string
	Social      seo.Social
	Cards       []ui.Option
	Types       []seoSocialTypeRow
	Terms       []seoTermRow
	Preview     []seoSocialPreviewLine
	DefaultText string
	MaxTemplate int
	MaxURL      int
}

func (a *Admin) seoSocialStored(r *http.Request, set seo.Settings) socialState {
	st := socialState{Set: set.Social, Kinds: a.seoKinds(r.Context(), set), Rest: set}
	for _, row := range a.seoTypes(r.Context(), set) {
		t := set.Social.Types[row.Slug]
		st.Types = append(st.Types, seoSocialTypeRow{Slug: row.Slug, Name: row.Name, Title: t.Title, Image: t.Image})
	}
	return st
}

// seoSocialFromForm applies the posted fields over stored. Types and kinds the
// page did not list are left as they are.
func seoSocialFromForm(form url.Values, stored socialState) socialState {
	next := socialState{Kinds: stored.Kinds, Rest: stored.Rest}
	so := seo.Settings{Social: stored.Set}.Clone().Social
	so.Image = strings.TrimSpace(form.Get("image"))
	so.TwitterSite = strings.TrimSpace(form.Get("twitter_site"))
	so.Card = form.Get("card")
	so.FBAppID = strings.TrimSpace(form.Get("fb_app_id"))
	so.FBPage = strings.TrimSpace(form.Get("fb_page"))
	so.SiteTags = form.Get("site_tags") == "true"
	so.Articles = form.Get("articles") == "true"
	if so.Types == nil {
		so.Types = map[string]seo.SocialType{}
	}
	if so.Taxonomy == nil {
		so.Taxonomy = map[string]string{}
	}
	for _, row := range stored.Types {
		p := "social[" + row.Slug + "]"
		if form.Has(p + "[title]") {
			row.Title = strings.TrimSpace(form.Get(p + "[title]"))
			row.Image = strings.TrimSpace(form.Get(p + "[image]"))
			if row.Title == "" && row.Image == "" {
				delete(so.Types, row.Slug)
			} else {
				so.Types[row.Slug] = seo.SocialType{Title: row.Title, Image: row.Image}
			}
		}
		next.Types = append(next.Types, row)
	}
	for _, kind := range stored.Kinds {
		name := "social_taxonomy[" + kind + "]"
		if !form.Has(name) {
			continue
		}
		if v := strings.TrimSpace(form.Get(name)); v != "" {
			so.Taxonomy[kind] = v
		} else {
			delete(so.Taxonomy, kind)
		}
	}
	if len(so.Types) == 0 {
		so.Types = nil
	}
	if len(so.Taxonomy) == 0 {
		so.Taxonomy = nil
	}
	next.Set = so
	return next
}

func (a *Admin) handleSEOSocial(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEOSocial(w, r, http.StatusOK, a.seoSocialStored(r, set), nil)
}

func (a *Admin) handleSEOSocialSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	next := seoSocialFromForm(r.PostForm, a.seoSocialStored(r, set))
	probe := set.Clone()
	probe.Social = next.Set
	var errs []ui.FieldError
	for _, fe := range probe.Validate() {
		if strings.HasPrefix(fe.Field, "social") {
			errs = append(errs, ui.FieldError{ID: seoFieldID(fe.Field), Label: seoFieldLabel(fe.Field), Message: fe.Message})
		}
	}
	if len(errs) > 0 {
		a.renderSEOSocial(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	n := next.Set
	if !a.seoSave(w, r, func(s *seo.Settings) { s.Social = n }, map[string]any{"tab": "social", "types": len(n.Types)}) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoSocialPath, http.StatusSeeOther)
}

// handleSEOSocialPreview re-renders the tab with the typed values so the
// preview shows them before anything is saved (htmx swaps just the preview).
func (a *Admin) handleSEOSocialPreview(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	a.renderSEOSocial(w, r, http.StatusOK, seoSocialFromForm(r.PostForm, a.seoSocialStored(r, set)), nil)
}

func (a *Admin) renderSEOSocial(w http.ResponseWriter, r *http.Request, status int, st socialState, errs []ui.FieldError) {
	d := seoSocialData{
		Summary: errs, Errs: map[string]string{}, Social: st.Set, Types: st.Types,
		DefaultText: seo.DefaultTitle, MaxTemplate: seo.MaxTemplateLen, MaxURL: 2048,
		Cards: []ui.Option{
			{Value: "large", Label: "Large image", Selected: st.Set.Card != "summary"},
			{Value: "summary", Label: "Summary", Selected: st.Set.Card == "summary"},
		},
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	for _, kind := range st.Kinds {
		d.Terms = append(d.Terms, seoTermRow{Kind: kind, Label: kindLabel(kind), Title: st.Set.Taxonomy[kind]})
	}
	d.Preview = seoSocialPreview(st)

	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-social"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/seo-social", v)
}

// seoSocialPreview resolves the share title and image of each content type
// through seo.SocialTitle, the function the public head calls.
func seoSocialPreview(st socialState) []seoSocialPreviewLine {
	set := st.Rest.Clone()
	set.Social = st.Set
	var lines []seoSocialPreviewLine
	for _, row := range st.Types {
		in := seo.Input{Kind: seo.KindSingle, SchemaSlug: row.Slug, Vars: seo.Vars{Title: "Sample title"}}
		title := seo.SocialTitle(&set, in)
		if title == "" {
			title = "Sample title"
		}
		image := firstNonEmptyString(seo.SocialImage(&set, in), set.Social.Image)
		lines = append(lines, seoSocialPreviewLine{Label: row.Name, Title: title, Image: image})
	}
	return lines
}
