package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/seo"
)

const (
	seoGeneralPath = "/settings/seo/general"
	seoTitlesPath  = "/settings/seo/titles"
	// seoBlankVariableRows are the empty name/value rows offered below the
	// saved custom variables.
	seoBlankVariableRows = 3
)

// SEOService is the SEO settings store and the schema archive values the
// Titles & meta tab edits next to it.
type SEOService interface {
	Get(ctx context.Context) (seo.Settings, error)
	// Merge changes the keys a tab owns on a fresh read of the stored settings.
	Merge(ctx context.Context, fn func(*seo.Settings)) error
	// SaveTitles writes the archive title and description of each patched type
	// into the schema settings (the values the schema editor shows) and merges
	// the templates, all in one transaction: either everything is saved or
	// nothing is. A failing patch comes back as a *SEOPatchError.
	SaveTitles(ctx context.Context, patches []SEOTitlePatch, fn func(*seo.Settings)) error
	// SaveSchema writes the schema type, article subtype and FAQ flag of each
	// patched type into the schema settings, all in one transaction. A failing
	// patch comes back as a *SEOPatchError.
	SaveSchema(ctx context.Context, patches []SEOSchemaPatch) error
}

// SEOTitlePatch is one type's archive title and description change.
type SEOTitlePatch struct {
	ID    uuid.UUID
	Patch entity.SchemaSEOPatch
}

// SEOPatchError names the type whose archive patch made SaveTitles fail.
type SEOPatchError struct {
	ID  uuid.UUID
	Err error
}

func (e *SEOPatchError) Error() string { return "seo archive patch: " + e.Err.Error() }
func (e *SEOPatchError) Unwrap() error { return e.Err }

// seoVarHelp describes the built-in variables, in the order they are listed.
var seoVarHelp = []struct{ Name, Desc string }{
	{"title", "The page or entry title."},
	{"sitename", "The site name from Settings > General."},
	{"tagline", "The tagline from Settings > General."},
	{"sep", "The separator chosen under Titles & meta."},
	{"page", "The page number of a listing, from page 2."},
	{"pagetotal", "The number of pages of a listing."},
	{"currentyear", "The current year."},
	{"excerpt", "The entry's excerpt."},
	{"author", "The author's name."},
	{"date", "The publish date of an entry."},
	{"term", "The name of a category, tag or other term."},
	{"searchphrase", "What the visitor searched for."},
	{"archive", "The heading of a content archive."},
}

func (a *Admin) mountSEOSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+seoGeneralPath, a.handleSEOGeneral)
	owner.HandleFunc("POST "+seoGeneralPath, a.handleSEOGeneralSave)
	owner.HandleFunc("GET "+seoTitlesPath, a.handleSEOTitles)
	owner.HandleFunc("POST "+seoTitlesPath, a.handleSEOTitlesSave)
	owner.HandleFunc("POST "+seoTitlesPath+"/preview", a.handleSEOTitlesPreview)
	owner.HandleFunc("GET "+seoSocialPath, a.handleSEOSocial)
	owner.HandleFunc("POST "+seoSocialPath, a.handleSEOSocialSave)
	owner.HandleFunc("POST "+seoSocialPath+"/preview", a.handleSEOSocialPreview)
	owner.HandleFunc("GET "+seoSchemaPath, a.handleSEOSchema)
	owner.HandleFunc("POST "+seoSchemaPath, a.handleSEOSchemaSave)
}

// seoFieldID turns a validation field name ("type[doc][title]") into the id of
// the input that carries it ("type-doc-title").
func seoFieldID(field string) string {
	return strings.NewReplacer("[", "-", "]", "").Replace(field)
}

func (a *Admin) seoLoad(w http.ResponseWriter, r *http.Request) (seo.Settings, bool) {
	svc := a.deps.SEO
	if svc == nil {
		a.unavailable(w, r, "SEO settings")
		return seo.Settings{}, false
	}
	set, err := svc.Get(r.Context())
	if err != nil {
		a.logger().Error("seo settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The SEO settings could not be loaded.")
		return seo.Settings{}, false
	}
	return set.Clone(), true
}

// seoSave merges one tab's keys, then clears the page cache so the new titles
// show at once.
func (a *Admin) seoSave(w http.ResponseWriter, r *http.Request, fn func(*seo.Settings), audit map[string]any) bool {
	ctx := r.Context()
	if err := a.deps.SEO.Merge(ctx, fn); err != nil {
		a.logger().Error("seo settings save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The SEO settings could not be saved.")
		return false
	}
	a.seoSaved(r, audit)
	return true
}

// seoSaved runs after a committed save: audit, then clear the page cache.
func (a *Admin) seoSaved(r *http.Request, audit map[string]any) {
	a.audit(r, "seo.settings", "seo", audit)
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
}

// ---- General ----------------------------------------------------------------

type seoVarRow struct {
	Index       int
	Name, Value string
}

type seoGeneralData struct {
	Summary  []ui.FieldError
	Errs     map[string]string
	Rows     []seoVarRow
	Builtins []struct{ Name, Desc string }
	SiteName string
	Tagline  string
}

func (a *Admin) handleSEOGeneral(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEOGeneral(w, r, http.StatusOK, seoVarRows(set.Variables), nil)
}

func seoVarRows(vars map[string]string) []seoVarRow {
	var rows []seoVarRow
	for _, name := range seoSortedKeys(vars) {
		rows = append(rows, seoVarRow{Name: name, Value: vars[name]})
	}
	return rows
}

func seoSortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (a *Admin) renderSEOGeneral(w http.ResponseWriter, r *http.Request, status int, rows []seoVarRow, errs []ui.FieldError) {
	for i := 0; i < seoBlankVariableRows && len(rows) < seo.MaxVariables; i++ {
		rows = append(rows, seoVarRow{})
	}
	for i := range rows {
		rows[i].Index = i
	}
	d := seoGeneralData{Summary: errs, Errs: map[string]string{}, Rows: rows, Builtins: seoVarHelp}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(r.Context()); err == nil && cfg != nil {
			d.SiteName, d.Tagline = cfg.CompanyName, cfg.Tagline
		}
	}
	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-general"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/seo-general", v)
}

func (a *Admin) handleSEOGeneralSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	names, values := r.PostForm["variable_name"], r.PostForm["variable_value"]
	vars := map[string]string{}
	var rows []seoVarRow
	var errs []ui.FieldError
	rowOf := map[string]int{}
	for i, name := range names {
		name = strings.TrimSpace(name)
		value := ""
		if i < len(values) {
			value = strings.TrimSpace(values[i])
		}
		if name == "" && value == "" {
			continue
		}
		rows = append(rows, seoVarRow{Name: name, Value: value})
		if _, dup := vars[name]; dup {
			errs = append(errs, ui.FieldError{ID: seoRowID(len(rows)-1, "name"), Label: "Variable name", Message: "Each variable needs its own name."})
			continue
		}
		vars[name] = value
		rowOf[name] = len(rows) - 1
	}
	set.Variables = vars
	for _, fe := range set.Validate() {
		if name, ok := strings.CutPrefix(strings.TrimSuffix(fe.Field, "]"), "variable["); ok {
			errs = append(errs, ui.FieldError{ID: seoRowID(rowOf[name], "name"), Label: "Variable " + name, Message: fe.Message})
			continue
		}
		errs = append(errs, ui.FieldError{ID: "custom", Label: "Custom variables", Message: fe.Message})
	}
	if len(errs) > 0 {
		a.renderSEOGeneral(w, r, http.StatusUnprocessableEntity, rows, errs)
		return
	}
	if !a.seoSave(w, r, func(s *seo.Settings) { s.Variables = vars }, map[string]any{"tab": "general", "variables": len(vars)}) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoGeneralPath, http.StatusSeeOther)
}

func seoRowID(i int, part string) string { return "variable-" + strconv.Itoa(i) + "-" + part }

// ---- Titles & meta -----------------------------------------------------------

// seoTypeRow is one content type of the Titles & meta tab.
type seoTypeRow struct {
	ID                                        uuid.UUID
	Slug, Name                                string
	Title, Description                        string
	ArchiveTitle, ArchiveDescription          string
	ArchiveReadOnly                           bool
	storedArchiveTitle, storedArchiveDescript string
}

type seoTermRow struct{ Kind, Label, Title string }

type seoArchiveRow struct{ Key, Label, Value string }

type seoPreviewLine struct{ Label, Title, Description string }

type seoTitlesData struct {
	Summary      []ui.FieldError
	Errs         map[string]string
	Separators   []ui.Option
	Home         string
	Archives     []seoArchiveRow
	Types        []seoTypeRow
	Terms        []seoTermRow
	Preview      []seoPreviewLine
	DefaultTitle string
	MaxTemplate  int
}

// titlesState is everything the tab edits, as stored or as submitted.
type titlesState struct {
	Set   seo.Settings
	Types []seoTypeRow
	Kinds []string
}

var seoArchiveKeys = []struct{ Key, Label string }{
	{"archive", "Content archives"}, {"author", "Author pages"}, {"date", "Date archives"},
	{"search", "Search results"}, {"notfound", "Page not found"},
}

func archiveValue(a seo.ArchiveTemplates, key string) string {
	switch key {
	case "archive":
		return a.Archive
	case "author":
		return a.Author
	case "date":
		return a.Date
	case "search":
		return a.Search
	default:
		return a.NotFound
	}
}

func setArchiveValue(a *seo.ArchiveTemplates, key, v string) {
	switch key {
	case "archive":
		a.Archive = v
	case "author":
		a.Author = v
	case "date":
		a.Date = v
	case "search":
		a.Search = v
	default:
		a.NotFound = v
	}
}

// seoTypes lists the content types that get a row: every type except the
// builder's blocks and theme parts, which are never public.
func (a *Admin) seoTypes(ctx context.Context, set seo.Settings) []seoTypeRow {
	if a.deps.Content.Entities == nil {
		return nil
	}
	schemas, err := a.deps.Content.Entities.ListSchemas(ctx)
	if err != nil {
		a.logger().Error("seo settings: list types", "err", err)
		return nil
	}
	var rows []seoTypeRow
	for _, sc := range schemas {
		if entity.IsReuseSlug(sc.Slug) {
			continue
		}
		st, _ := sc.URLSettings()
		t := set.Types[sc.Slug]
		rows = append(rows, seoTypeRow{
			ID: sc.ID, Slug: sc.Slug, Name: sc.Name, Title: t.Title, Description: t.Description,
			ArchiveTitle: st.ArchiveTitle, ArchiveDescription: st.ArchiveDescription,
			// A built-in type other than person refuses a settings-only write.
			ArchiveReadOnly:       sc.IsBuiltin && sc.Slug != entity.PersonSchemaSlug,
			storedArchiveTitle:    st.ArchiveTitle,
			storedArchiveDescript: st.ArchiveDescription,
		})
	}
	slices.SortFunc(rows, func(x, y seoTypeRow) int { return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)) })
	return rows
}

// seoKinds lists the term kinds that get a row: tag and category, the kinds
// that have terms, and any kind that already has a template.
func (a *Admin) seoKinds(ctx context.Context, set seo.Settings) []string {
	nodes, _ := a.termNodes(ctx) // no term service leaves the two built-in kinds
	kinds := kindsOf(nodes)
	for k := range set.Taxonomy {
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	slices.Sort(kinds)
	return kinds
}

func (a *Admin) seoTitlesStored(ctx context.Context, set seo.Settings) titlesState {
	return titlesState{Set: set, Types: a.seoTypes(ctx, set), Kinds: a.seoKinds(ctx, set)}
}

func (a *Admin) handleSEOTitles(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEOTitles(w, r, http.StatusOK, a.seoTitlesStored(r.Context(), set), nil)
}

// seoTitlesFromForm applies the posted fields over stored. Types and kinds the
// page did not list are left as they are.
func seoTitlesFromForm(form url.Values, stored titlesState) titlesState {
	next := titlesState{Set: stored.Set.Clone(), Kinds: stored.Kinds}
	set := &next.Set
	set.Separator = form.Get("separator")
	set.Home = seoTemplateOrDefault(form.Get("home"))
	for _, k := range seoArchiveKeys {
		setArchiveValue(&set.Archives, k.Key, seoTemplateOrDefault(form.Get("archives["+k.Key+"]")))
	}
	for _, row := range stored.Types {
		p := "type[" + row.Slug + "]"
		if !form.Has(p + "[title]") {
			// The type was not on the page that was posted (created or
			// renamed since): keep what is stored.
			next.Types = append(next.Types, row)
			continue
		}
		row.Title = strings.TrimSpace(form.Get(p + "[title]"))
		row.Description = strings.TrimSpace(form.Get(p + "[description]"))
		if !row.ArchiveReadOnly {
			row.ArchiveTitle = strings.TrimSpace(form.Get(p + "[archive_title]"))
			row.ArchiveDescription = strings.TrimSpace(form.Get(p + "[archive_description]"))
		}
		if row.Title == "" && row.Description == "" {
			delete(set.Types, row.Slug)
		} else {
			set.Types[row.Slug] = seo.TypeTemplates{Title: row.Title, Description: row.Description}
		}
		next.Types = append(next.Types, row)
	}
	for _, kind := range stored.Kinds {
		if v := strings.TrimSpace(form.Get("taxonomy[" + kind + "]")); v != "" {
			set.Taxonomy[kind] = v
		} else {
			delete(set.Taxonomy, kind)
		}
	}
	return next
}

func seoTemplateOrDefault(v string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return seo.DefaultTitle
}

// seoTitlesErrors validates the submitted tab: the template rules plus the
// archive length limits the schema editor enforces.
func seoTitlesErrors(st titlesState) []ui.FieldError {
	var errs []ui.FieldError
	for _, fe := range st.Set.Validate() {
		errs = append(errs, ui.FieldError{ID: seoFieldID(fe.Field), Label: seoFieldLabel(fe.Field), Message: fe.Message})
	}
	for _, row := range st.Types {
		if n := utf8.RuneCountInString(row.ArchiveTitle); n > entity.MaxArchiveTitleLen {
			errs = append(errs, ui.FieldError{ID: "type-" + row.Slug + "-archive-title", Label: row.Name + " archive title", Message: "The archive title can be at most " + strconv.Itoa(entity.MaxArchiveTitleLen) + " characters."})
		}
		if n := utf8.RuneCountInString(row.ArchiveDescription); n > entity.MaxArchiveDescriptionLen {
			errs = append(errs, ui.FieldError{ID: "type-" + row.Slug + "-archive-description", Label: row.Name + " archive description", Message: "The archive description can be at most " + strconv.Itoa(entity.MaxArchiveDescriptionLen) + " characters."})
		}
	}
	return errs
}

var seoFieldRE = regexp.MustCompile(`^(\w+)\[([^\]]*)\](?:\[(\w+)\])?$`)

func seoFieldLabel(field string) string {
	switch field {
	case "separator":
		return "Separator"
	case "home":
		return "Homepage title"
	}
	m := seoFieldRE.FindStringSubmatch(field)
	if m == nil {
		return field
	}
	switch m[1] {
	case "social":
		return seoSocialLabel(m[2], m[3])
	case "social_taxonomy":
		return kindLabel(m[2]) + " social title"
	case "type":
		return m[2] + " " + m[3]
	case "schema":
		return m[2] + " schema " + m[3]
	case "taxonomy":
		return kindLabel(m[2]) + " title"
	case "archives":
		for _, k := range seoArchiveKeys {
			if k.Key == m[2] {
				return k.Label + " title"
			}
		}
	}
	return field
}

func (a *Admin) handleSEOTitlesSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	_ = r.ParseForm()
	stored := a.seoTitlesStored(ctx, set)
	next := seoTitlesFromForm(r.PostForm, stored)
	if errs := seoTitlesErrors(next); len(errs) > 0 {
		a.renderSEOTitles(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}

	var patches []SEOTitlePatch
	rowOf := map[uuid.UUID]seoTypeRow{}
	for _, row := range next.Types {
		if row.ArchiveReadOnly || (row.ArchiveTitle == row.storedArchiveTitle && row.ArchiveDescription == row.storedArchiveDescript) {
			continue
		}
		rowOf[row.ID] = row
		patches = append(patches, SEOTitlePatch{ID: row.ID, Patch: entity.SchemaSEOPatch{ArchiveTitle: &row.ArchiveTitle, ArchiveDescription: &row.ArchiveDescription}})
	}

	n := next.Set
	apply := func(s *seo.Settings) {
		s.Separator, s.Home, s.Archives = n.Separator, n.Home, n.Archives
		for _, row := range next.Types {
			if t, ok := n.Types[row.Slug]; ok {
				s.Types[row.Slug] = t
			} else {
				delete(s.Types, row.Slug)
			}
		}
		for _, kind := range next.Kinds {
			if t, ok := n.Taxonomy[kind]; ok {
				s.Taxonomy[kind] = t
			} else {
				delete(s.Taxonomy, kind)
			}
		}
	}
	if err := a.deps.SEO.SaveTitles(ctx, patches, apply); err != nil {
		var pe *SEOPatchError
		var se *entity.SettingsError
		if errors.As(err, &pe) && errors.As(err, &se) {
			row := rowOf[pe.ID]
			id := "type-" + row.Slug + "-" + strings.ReplaceAll(se.Field, "_", "-")
			a.renderSEOTitles(w, r, http.StatusUnprocessableEntity, next, []ui.FieldError{{ID: id, Label: row.Name + " archive", Message: se.Msg}})
			return
		}
		a.logger().Error("seo settings titles save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The SEO settings could not be saved.")
		return
	}
	a.seoSaved(r, map[string]any{"tab": "titles", "separator": n.Separator, "types": len(n.Types)})
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoTitlesPath, http.StatusSeeOther)
}

// handleSEOTitlesPreview re-renders the tab with the typed values so the
// preview lines show them before anything is saved. The Update preview button
// swaps just the preview region in (htmx).
func (a *Admin) handleSEOTitlesPreview(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	next := seoTitlesFromForm(r.PostForm, a.seoTitlesStored(r.Context(), set))
	a.renderSEOTitles(w, r, http.StatusOK, next, nil)
}

func (a *Admin) renderSEOTitles(w http.ResponseWriter, r *http.Request, status int, st titlesState, errs []ui.FieldError) {
	set := st.Set
	d := seoTitlesData{
		Summary: errs, Errs: map[string]string{}, Home: set.Home,
		Types: st.Types, DefaultTitle: seo.DefaultTitle, MaxTemplate: seo.MaxTemplateLen,
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	for _, sep := range seo.Separators {
		d.Separators = append(d.Separators, ui.Option{Value: sep, Label: sep, Selected: set.Separator == sep})
	}
	for _, k := range seoArchiveKeys {
		d.Archives = append(d.Archives, seoArchiveRow{Key: k.Key, Label: k.Label, Value: archiveValue(set.Archives, k.Key)})
	}
	for _, kind := range st.Kinds {
		d.Terms = append(d.Terms, seoTermRow{Kind: kind, Label: kindLabel(kind), Title: set.Taxonomy[kind]})
	}
	d.Preview = a.seoPreview(r.Context(), st)

	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-titles"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/seo-titles", v)
}

// seoPreview resolves a sample page of each kind through seo.Resolve, the same
// function the public head calls, so the lines match the live output.
func (a *Admin) seoPreview(ctx context.Context, st titlesState) []seoPreviewLine {
	site, tagline := "", ""
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(ctx); err == nil && cfg != nil {
			site, tagline = cfg.CompanyName, cfg.Tagline
		}
	}
	vars := seo.Vars{Title: "Sample title", SiteName: site, Tagline: tagline, Term: "Sample term", SearchPhrase: "sample", Archive: "Sample archive"}
	set := st.Set
	line := func(label string, in seo.Input) seoPreviewLine {
		if in.Vars.Title == "" {
			in.Vars = vars
		}
		r := seo.Resolve(&set, in)
		return seoPreviewLine{Label: label, Title: r.Title, Description: r.Description}
	}
	lines := []seoPreviewLine{line("Homepage", seo.Input{Kind: seo.KindHome, Vars: seo.Vars{Title: firstNonEmptyString(site, "Home"), SiteName: site, Tagline: tagline}})}
	for _, row := range st.Types {
		lines = append(lines, line(row.Name, seo.Input{Kind: seo.KindSingle, SchemaSlug: row.Slug}))
	}
	for _, kind := range st.Kinds {
		lines = append(lines, line(kindLabel(kind), seo.Input{Kind: seo.KindTaxonomy, TermKind: kind}))
	}
	for _, k := range seoArchiveKeys {
		lines = append(lines, line(k.Label, seo.Input{Kind: seo.Kind(k.Key)}))
	}
	return lines
}

func firstNonEmptyString(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// seoSocialLabel names a Social tab field: a fixed one ("card") or a content
// type's ("doc", "title").
func seoSocialLabel(name, part string) string {
	if part != "" {
		return name + " social " + part
	}
	switch name {
	case "card":
		return "X card"
	case "twitter_site":
		return "X handle"
	case "fb_app_id":
		return "Facebook app ID"
	case "fb_page":
		return "Facebook page"
	case "image":
		return "Default share image"
	}
	return name
}
