package admin

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
)

const seoSchemaPath = "/settings/seo/schema"

// SEOSchemaPatch is one type's schema type, article subtype and FAQ change.
type SEOSchemaPatch struct {
	ID    uuid.UUID
	Patch entity.SchemaSEOPatch
}

// legacySchemaTypes is the @type the built-in generator gives a type that
// stores no schema type, by slug; any other slug gets Thing.
var legacySchemaTypes = map[string]string{
	"page": "WebPage", "blog": "Article", "post": "Article", "article": "Article", "blog_post": "Article",
	"team_member": "Person", "person": "Person", "organization": "Organization", "company": "Organization",
	"product": "Product",
}

// seoSchemaRow is one content type of the Schema tab.
type seoSchemaRow struct {
	ID         uuid.UUID
	Slug, Name string
	// Type and Subtype are "" when nothing is stored; FAQ is on unless
	// stored as off.
	Type, Subtype string
	FAQ           bool
	// Legacy is the @type the type gets while Type is empty; Rich is true when
	// that comes from a dedicated builder an explicit type replaces.
	Legacy string
	Rich   bool
	// ReadOnly rows are shown, never saved; Note says why.
	ReadOnly bool
	Note     string

	TypeOptions    []ui.Option
	SubtypeOptions []ui.Option
	// ShowSubtype is true when the effective @type is an Article.
	ShowSubtype bool

	storedType, storedSubtype string
	storedFAQ                 bool
}

type seoSchemaData struct {
	Summary []ui.FieldError
	Errs    map[string]string
	Types   []seoSchemaRow
}

func (a *Admin) seoSchemaStored(r *http.Request) []seoSchemaRow {
	if a.deps.Content.Entities == nil {
		return nil
	}
	schemas, err := a.deps.Content.Entities.ListSchemas(r.Context())
	if err != nil {
		a.logger().Error("seo settings: list types", "err", err)
		return nil
	}
	var rows []seoSchemaRow
	for _, sc := range schemas {
		if entity.IsReuseSlug(sc.Slug) {
			continue
		}
		st, _ := sc.URLSettings()
		legacy, rich := legacySchemaTypes[sc.Slug]
		if !rich {
			legacy = "Thing"
		}
		row := seoSchemaRow{
			ID: sc.ID, Slug: sc.Slug, Name: sc.Name, Legacy: legacy, Rich: rich,
			Type: st.SchemaOrgType, Subtype: st.ArticleType, FAQ: st.FAQEnabled(),
		}
		switch {
		case sc.IsBuiltin && sc.Slug != entity.PersonSchemaSlug:
			row.ReadOnly, row.Note = true, "Built in: its schema type cannot be changed."
		case entity.SchemaFixesJSONLDType(sc):
			row.ReadOnly, row.Note = true, "Set by the type's schema."
		}
		row.storedType, row.storedSubtype, row.storedFAQ = row.Type, row.Subtype, row.FAQ
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(x, y seoSchemaRow) int { return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)) })
	return rows
}

// seoSchemaFromForm applies the posted fields over stored. A row the page did
// not list, or a read-only one, keeps what is stored; a subtype that was not
// posted (the select only shows for Article types) is kept too.
func seoSchemaFromForm(form url.Values, stored []seoSchemaRow) []seoSchemaRow {
	next := make([]seoSchemaRow, 0, len(stored))
	for _, row := range stored {
		p := "schema[" + row.Slug + "]"
		if !row.ReadOnly && form.Has(p+"[type]") {
			row.Type = strings.TrimSpace(form.Get(p + "[type]"))
			if form.Has(p + "[subtype]") {
				row.Subtype = strings.TrimSpace(form.Get(p + "[subtype]"))
			}
			row.FAQ = form.Get(p+"[faq]") == "true"
		}
		next = append(next, row)
	}
	return next
}

func seoSchemaErrors(rows []seoSchemaRow) []ui.FieldError {
	var errs []ui.FieldError
	for _, row := range rows {
		if row.Type != "" && !entity.IsAllowedSchemaOrgType(row.Type) {
			errs = append(errs, ui.FieldError{ID: "schema-" + row.Slug + "-type", Label: row.Name + " schema type", Message: "Choose one of the listed schema types."})
		}
		if row.Subtype != "" && !slices.Contains(entity.AllowedArticleTypes, row.Subtype) {
			errs = append(errs, ui.FieldError{ID: "schema-" + row.Slug + "-subtype", Label: row.Name + " article type", Message: "Choose one of the listed article types."})
		}
	}
	return errs
}

func (a *Admin) handleSEOSchema(w http.ResponseWriter, r *http.Request) {
	if a.deps.SEO == nil {
		a.unavailable(w, r, "SEO settings")
		return
	}
	a.renderSEOSchema(w, r, http.StatusOK, a.seoSchemaStored(r), nil)
}

func (a *Admin) handleSEOSchemaSave(w http.ResponseWriter, r *http.Request) {
	if a.deps.SEO == nil {
		a.unavailable(w, r, "SEO settings")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and changes nothing
	next := seoSchemaFromForm(r.PostForm, a.seoSchemaStored(r))
	if errs := seoSchemaErrors(next); len(errs) > 0 {
		a.renderSEOSchema(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	var patches []SEOSchemaPatch
	rowOf := map[uuid.UUID]seoSchemaRow{}
	for _, row := range next {
		if row.ReadOnly {
			continue
		}
		var p entity.SchemaSEOPatch
		changed := false
		if row.Type != row.storedType {
			p.SchemaOrgType, changed = &row.Type, true
		}
		if row.Subtype != row.storedSubtype {
			p.ArticleType, changed = &row.Subtype, true
		}
		if row.FAQ != row.storedFAQ {
			p.FAQFromContent, changed = &row.FAQ, true
		}
		if changed {
			rowOf[row.ID] = row
			patches = append(patches, SEOSchemaPatch{ID: row.ID, Patch: p})
		}
	}
	if len(patches) > 0 {
		if err := a.deps.SEO.SaveSchema(r.Context(), patches); err != nil {
			var pe *SEOPatchError
			var se *entity.SettingsError
			if errors.As(err, &pe) && errors.As(err, &se) {
				row := rowOf[pe.ID]
				id := "schema-" + row.Slug + "-type"
				if se.Field == "article_type" {
					id = "schema-" + row.Slug + "-subtype"
				}
				a.renderSEOSchema(w, r, http.StatusUnprocessableEntity, next, []ui.FieldError{{ID: id, Label: row.Name + " schema", Message: se.Msg}})
				return
			}
			a.logger().Error("seo settings schema save", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The SEO settings could not be saved.")
			return
		}
		a.seoSaved(r, map[string]any{"tab": "schema", "types": len(patches)})
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoSchemaPath, http.StatusSeeOther)
}

func (a *Admin) renderSEOSchema(w http.ResponseWriter, r *http.Request, status int, rows []seoSchemaRow, errs []ui.FieldError) {
	d := seoSchemaData{Summary: errs, Errs: map[string]string{}}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	for i := range rows {
		row := &rows[i]
		row.TypeOptions = []ui.Option{{Value: "", Label: "Default (" + row.Legacy + ")", Selected: row.Type == ""}}
		for _, t := range entity.AllowedSchemaOrgTypes {
			row.TypeOptions = append(row.TypeOptions, ui.Option{Value: t, Label: t, Selected: row.Type == t})
		}
		row.SubtypeOptions = []ui.Option{{Value: "", Label: "Default (Article)", Selected: row.Subtype == ""}}
		for _, t := range entity.AllowedArticleTypes {
			row.SubtypeOptions = append(row.SubtypeOptions, ui.Option{Value: t, Label: t, Selected: row.Subtype == t})
		}
		effective := firstNonEmptyString(row.Type, row.Legacy)
		row.ShowSubtype = !row.ReadOnly && slices.Contains(entity.AllowedArticleTypes, effective)
	}
	d.Types = rows
	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-schema"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/seo-schema", v)
}
