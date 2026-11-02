package admin

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

const seoSitemapPath = "/settings/seo/sitemap"

// SEOSitemapPatch is one type's sitemap change.
type SEOSitemapPatch struct {
	ID    uuid.UUID
	Patch entity.SchemaSEOPatch
}

// seoSitemapRow is one content type of the Sitemap tab.
type seoSitemapRow struct {
	ID         uuid.UUID
	Slug, Name string
	// Include is false when the type is left out of the sitemap. Priority is
	// the typed text, "" for the type's own default.
	Include    bool
	Priority   string
	ChangeFreq string

	ChangeOptions []ui.Option

	storedInclude                bool
	storedPriority, storedChange string
}

// sitemapState is what the Sitemap tab edits, as stored or as submitted.
type sitemapState struct {
	Set   seo.Sitemap
	Types []seoSitemapRow
	// MaxURLs and DefaultPriority are the typed texts, kept so a rejected
	// value is shown back as typed.
	MaxURLs, DefaultPriority string
	Errs                     []seo.FieldError
	// RowErrs are problems of per-type values, by field id.
	RowErrs []ui.FieldError
}

type seoSitemapData struct {
	Summary       []ui.FieldError
	Errs          map[string]string
	State         sitemapState
	Paths         string
	MaxPaths      int
	DefaultOpts   []ui.Option
	DefaultMaxURL int
}

func formatPriority(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

// parsePriority reads a typed priority; "" is no value.
func parsePriority(text string) (*float64, bool) {
	if text == "" {
		return nil, true
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || f < 0 || f > 1 {
		return nil, false
	}
	return &f, true
}

func (a *Admin) seoSitemapStored(r *http.Request, set seo.Settings) sitemapState {
	st := sitemapState{
		Set: set.Sitemap, MaxURLs: strconv.Itoa(set.Sitemap.EffectiveMaxURLs()),
		DefaultPriority: formatPriority(set.Sitemap.DefaultPriority),
	}
	if a.deps.Content.Entities == nil {
		return st
	}
	schemas, err := a.deps.Content.Entities.ListSchemas(r.Context())
	if err != nil {
		a.logger().Error("seo settings: list types", "err", err)
		return st
	}
	for _, sc := range schemas {
		if entity.IsReuseSlug(sc.Slug) {
			continue
		}
		u, _ := sc.URLSettings()
		row := seoSitemapRow{
			ID: sc.ID, Slug: sc.Slug, Name: sc.Name,
			Include: !u.Sitemap.Exclude, Priority: formatPriority(u.Sitemap.Priority), ChangeFreq: u.Sitemap.ChangeFreq,
		}
		row.storedInclude, row.storedPriority, row.storedChange = row.Include, row.Priority, row.ChangeFreq
		st.Types = append(st.Types, row)
	}
	slices.SortFunc(st.Types, func(x, y seoSitemapRow) int { return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)) })
	return st
}

// seoSitemapFromForm applies the posted fields over stored. Fields the post
// does not carry keep their stored value (a switch posts "true" or "false").
func seoSitemapFromForm(form url.Values, stored sitemapState) sitemapState {
	next := sitemapState{Set: stored.Set, MaxURLs: stored.MaxURLs, DefaultPriority: stored.DefaultPriority}
	next.Set.ExcludePaths = slices.Clone(stored.Set.ExcludePaths)
	next.Types = slices.Clone(stored.Types)

	if form.Has("sitemap_author") {
		next.Set.NoAuthor = form.Get("sitemap_author") != "true"
	}
	if form.Has("sitemap_taxonomy") {
		next.Set.Taxonomy = form.Get("sitemap_taxonomy") == "true"
	}
	if form.Has("sitemap_images") {
		next.Set.Images = form.Get("sitemap_images") == "true"
	}
	if form.Has("sitemap_stats") {
		next.Set.NoStats = form.Get("sitemap_stats") != "true"
	}
	if form.Has("sitemap_max_urls") {
		next.MaxURLs = strings.TrimSpace(form.Get("sitemap_max_urls"))
		next.Set.MaxURLs = 0
		if next.MaxURLs != "" {
			n, err := strconv.Atoi(next.MaxURLs)
			if err != nil || n < 1 {
				next.Errs = append(next.Errs, seo.FieldError{Field: "sitemap[max_urls]", Message: "Enter a whole number from 1 to " + strconv.Itoa(seo.MaxSitemapURLs) + "."})
			} else if n != seo.MaxSitemapURLs {
				next.Set.MaxURLs = n // the limit itself is the default and stays unstored
			}
		}
	}
	if form.Has("sitemap_default_priority") {
		next.DefaultPriority = strings.TrimSpace(form.Get("sitemap_default_priority"))
		p, ok := parsePriority(next.DefaultPriority)
		next.Set.DefaultPriority = p
		if !ok {
			next.Errs = append(next.Errs, seo.FieldError{Field: "sitemap[default_priority]", Message: "Priority is a number from 0 to 1."})
		}
	}
	if form.Has("sitemap_default_changefreq") {
		next.Set.DefaultChangeFreq = form.Get("sitemap_default_changefreq")
	}
	if form.Has("sitemap_exclude_paths") {
		next.Set.ExcludePaths = nil
		for _, l := range strings.Split(form.Get("sitemap_exclude_paths"), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				next.Set.ExcludePaths = append(next.Set.ExcludePaths, l)
			}
		}
	}
	for i := range next.Types {
		row := &next.Types[i]
		p := "sitemap[" + row.Slug + "]"
		if !form.Has(p + "[include]") {
			continue
		}
		row.Include = form.Get(p+"[include]") == "true"
		row.Priority = strings.TrimSpace(form.Get(p + "[priority]"))
		row.ChangeFreq = form.Get(p + "[changefreq]")
		if _, ok := parsePriority(row.Priority); !ok {
			next.RowErrs = append(next.RowErrs, ui.FieldError{ID: "sitemap-" + row.Slug + "-priority", Label: row.Name + " priority", Message: "Priority is a number from 0 to 1."})
		}
		if !slices.Contains(seo.SitemapChangeFreqs, row.ChangeFreq) {
			next.RowErrs = append(next.RowErrs, ui.FieldError{ID: "sitemap-" + row.Slug + "-changefreq", Label: row.Name + " change", Message: "Choose one of the listed frequencies."})
		}
	}
	return next
}

// seoSitemapErrors lists the problems of a submitted state, each once.
func seoSitemapErrors(set seo.Settings, st sitemapState) []ui.FieldError {
	probe := set.Clone()
	probe.Sitemap = st.Set
	found := slices.Clone(st.Errs)
	for _, fe := range probe.Validate() {
		if strings.HasPrefix(fe.Field, "sitemap") {
			found = append(found, fe)
		}
	}
	var errs []ui.FieldError
	seen := map[string]bool{}
	for _, fe := range found {
		if !seen[fe.Field] {
			seen[fe.Field] = true
			errs = append(errs, ui.FieldError{ID: seoFieldID(fe.Field), Label: seoFieldLabel(fe.Field), Message: fe.Message})
		}
	}
	return append(errs, st.RowErrs...)
}

func (a *Admin) handleSEOSitemap(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEOSitemap(w, r, http.StatusOK, a.seoSitemapStored(r, set), nil)
}

func (a *Admin) handleSEOSitemapSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	next := seoSitemapFromForm(r.PostForm, a.seoSitemapStored(r, set))
	if errs := seoSitemapErrors(set, next); len(errs) > 0 {
		a.renderSEOSitemap(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}

	var patches []SEOSitemapPatch
	rowOf := map[uuid.UUID]seoSitemapRow{}
	for _, row := range next.Types {
		if row.Include == row.storedInclude && row.Priority == row.storedPriority && row.ChangeFreq == row.storedChange {
			continue
		}
		prio, _ := parsePriority(row.Priority)
		rowOf[row.ID] = row
		patches = append(patches, SEOSitemapPatch{ID: row.ID, Patch: entity.SchemaSEOPatch{
			Sitemap: &entity.SchemaSitemapPatch{Exclude: !row.Include, Priority: prio, ChangeFreq: row.ChangeFreq},
		}})
	}
	n := next.Set
	if err := a.deps.SEO.SaveSitemap(r.Context(), patches, func(s *seo.Settings) { s.Sitemap = n }); err != nil {
		var pe *SEOPatchError
		var se *entity.SettingsError
		if errors.As(err, &pe) && errors.As(err, &se) {
			row := rowOf[pe.ID]
			id := "sitemap-" + row.Slug + "-priority"
			if se.Field == "sitemap[changefreq]" {
				id = "sitemap-" + row.Slug + "-changefreq"
			}
			a.renderSEOSitemap(w, r, http.StatusUnprocessableEntity, next, []ui.FieldError{{ID: id, Label: row.Name + " sitemap", Message: se.Msg}})
			return
		}
		a.logger().Error("seo settings sitemap save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The SEO settings could not be saved.")
		return
	}
	a.seoSaved(r, map[string]any{"tab": "sitemap", "types": len(patches), "exclude_paths": len(n.ExcludePaths)})
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoSitemapPath, http.StatusSeeOther)
}

func changeFreqOptions(empty, selected string) []ui.Option {
	opts := make([]ui.Option, 0, len(seo.SitemapChangeFreqs))
	for _, f := range seo.SitemapChangeFreqs {
		label := f
		if f == "" {
			label = empty
		}
		opts = append(opts, ui.Option{Value: f, Label: label, Selected: f == selected})
	}
	return opts
}

func (a *Admin) renderSEOSitemap(w http.ResponseWriter, r *http.Request, status int, st sitemapState, errs []ui.FieldError) {
	d := seoSitemapData{
		Summary: errs, Errs: map[string]string{}, State: st, Paths: strings.Join(st.Set.ExcludePaths, "\n"),
		MaxPaths: seo.MaxSitemapExcludePaths, DefaultOpts: changeFreqOptions("Each type's own", st.Set.DefaultChangeFreq),
		DefaultMaxURL: seo.MaxSitemapURLs,
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	d.State.Types = slices.Clone(st.Types)
	for i := range d.State.Types {
		d.State.Types[i].ChangeOptions = changeFreqOptions("Default", d.State.Types[i].ChangeFreq)
	}
	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-sitemap"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/seo-sitemap", v)
}

func seoSitemapLabel(key, sub string) string {
	if sub != "" {
		return key + " " + sub
	}
	switch key {
	case "max_urls":
		return "Max URLs per file"
	case "default_priority":
		return "Default priority"
	case "default_changefreq":
		return "Default change"
	case "exclude_paths":
		return "Exclude paths"
	}
	return "Sitemap " + key
}
