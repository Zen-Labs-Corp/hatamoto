package admin

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

// seoView is the value of ?view= that switches a list to its inline SEO columns.
const seoView = "seo"

// viewTabs is the Columns toggle. Both links keep every filter, the sort and the
// search; the page resets.
func (p listParams) viewTabs(prefix string) []ui.Tab {
	href := func(view string) string {
		q := p.query()
		q.Del("view")
		if view != "" {
			q.Set("view", view)
		}
		if len(q) == 0 {
			return prefix + "/entities"
		}
		return prefix + "/entities?" + q.Encode()
	}
	return []ui.Tab{
		{Label: "Default", Href: href(""), Active: p.view == ""},
		{Label: "SEO", Href: href(seoView), Active: p.view == seoView},
	}
}

// seoListContext is what the SEO view needs once per request, not per row: the
// SEO settings the placeholders resolve with, the site name and the entity the
// site serves as its homepage.
type seoListContext struct {
	set           *seo.Settings
	site, tagline string
	homeID        string
}

func (a *Admin) seoListContext(ctx context.Context) seoListContext {
	var lc seoListContext
	if a.deps.SEO != nil {
		if set, err := a.deps.SEO.Get(ctx); err == nil {
			lc.set = &set
		}
	}
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(ctx); err == nil && cfg != nil {
			lc.site, lc.tagline = cfg.CompanyName, cfg.Tagline
			if cfg.HomepageEntityID != uuid.Nil {
				lc.homeID = cfg.HomepageEntityID.String()
				return lc
			}
		}
	}
	if a.deps.Growth.Explain != nil {
		if ex, err := a.deps.Growth.Explain.ExplainURL(ctx, "/"); err == nil {
			lc.homeID = ex.Page.EntityID
		}
	}
	return lc
}

// placeholders are the title and description the templates give an entry that
// has none of its own.
func (lc seoListContext) placeholders(e *entity.Entity, sc *entity.EntitySchema, attrs map[string]any) (title, desc string) {
	if lc.set == nil {
		return "", ""
	}
	in := seoInput{Title: e.Name, Excerpt: str(attrs, "excerpt"), SEO: lc.set, SiteName: lc.site, Tagline: lc.tagline, Home: lc.homeID != "" && lc.homeID == e.ID.String()}
	if sc != nil {
		in.SchemaSlug = sc.Slug
	}
	r := seo.Resolve(lc.set, in.resolverInput())
	return strings.TrimSpace(r.Title), strings.TrimSpace(r.Description)
}

func entitiesSEOColumns() []ui.Col {
	return []ui.Col{
		{Key: "name", Label: "Name", Sortable: true},
		{Key: "seo_title", Label: "SEO title"},
		{Key: "seo_description", Label: "Description"},
		{Key: "seo_keyword", Label: "Keyword"},
		{Key: "seo_noindex", Label: "Noindex"},
		{Key: "seo_save", Label: "Save"},
	}
}

// entitiesSEORow is one row of the SEO view: the name, an edit cell per field and
// the row's own form with its save state. The controls belong to that form by the
// form attribute, so the row saves without scripts.
func (a *Admin) entitiesSEORow(e *entity.Entity, sc *entity.EntitySchema, lc seoListContext, edit, csrf, back string) ui.Row {
	attrs := attrsMap(e.Attributes)
	f := seoFieldsOf(attrs)
	id := e.ID.String()
	form := "seo-" + id
	phTitle, phDesc := lc.placeholders(e, sc, attrs)
	text := func(name, value, label, ph, field string, max int) ui.Cell {
		cell := &ui.EditCell{Name: name, Value: value, Orig: value, Placeholder: ph, Label: label + " for " + e.Name, Control: "text", Form: form}
		if max > 0 {
			cell.Max = max
			c := seoCounter(field, value)
			cell.Count = &ui.EditCount{Len: c.Len, Status: c.Status, Msg: c.Msg}
		}
		return ui.Cell{Edit: cell}
	}
	desc := text(seoFieldDescription, f.Description, "SEO description", phDesc, seoFieldDescription, 160)
	desc.Edit.Control = "textarea"
	keyword := ui.Cell{Text: "-"}
	if postEditorFor(sc) {
		keyword = text(seoFieldKeyword, f.Keyword, "Focus keyword", "", "", 0)
	}
	noindex := ""
	if f.NoIndex {
		noindex = "true"
	}
	cells := []ui.Cell{
		{Text: e.Name, Link: edit},
		text(seoFieldTitle, f.Title, "SEO title", phTitle, seoFieldTitle, 60),
		desc,
		keyword,
		{Edit: &ui.EditCell{Name: seoFieldNoIndex, Value: noindex, Orig: noindex, Label: "Noindex for " + e.Name, Control: "switch", Form: form}},
		{Edit: &ui.EditCell{Control: "save", Form: form, Action: a.adminPath + "/entities/" + id + "/seo", CSRF: csrf, Hidden: map[string]string{"back": back}}},
	}
	return ui.Row{ID: id, Label: e.Name, Cells: cells}
}

// renderEntitiesSEO finishes handleEntities for ?view=seo: t carries the filters
// of the page, this adds the SEO columns and one row per listed entity.
func (a *Admin) renderEntitiesSEO(w http.ResponseWriter, r *http.Request, p listParams, res *entity.OffsetPagedResult[*entity.Entity], byID map[uuid.UUID]*entity.EntitySchema, t ui.Table, types []typeTab, resolved *entity.EntitySchema) {
	ctx := r.Context()
	t.Columns = entitiesSEOColumns()
	lc := a.seoListContext(ctx)
	back := r.URL.RequestURI()
	if !strings.HasPrefix(back, a.adminPath+"/entities") {
		back = a.adminPath + "/entities?" + url.Values{"view": {seoView}}.Encode()
	}
	for _, e := range res.Items {
		sc := byID[e.SchemaID]
		visual := usesVisualEditor(sc, e)
		t.Rows = append(t.Rows, a.entitiesSEORow(e, sc, lc, a.entityEditHref(visual, e.ID.String()), t.CSRF, back))
	}
	a.renderEntities(w, r, p, t, res.Total, types, resolved)
}
