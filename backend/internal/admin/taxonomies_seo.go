package admin

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/taxonomy"
)

// termSEOFields reads the values the inline term view edits. A term has no focus
// keyword.
func termSEOFields(s *taxonomy.TermSEO) seoFields {
	if s == nil {
		return seoFields{}
	}
	return seoFields{Title: strings.TrimSpace(s.MetaTitle), Description: strings.TrimSpace(s.MetaDescription), NoIndex: s.NoIndex}
}

// termsSEOTable turns the term list t into the SEO view: one edit cell per field
// and a row form that saves without scripts. Placeholders are what the taxonomy
// template gives a term with no title or description of its own.
func (a *Admin) termsSEOTable(ctx context.Context, t ui.Table, nodes []termNode, kind string) ui.Table {
	lc := a.seoListContext(ctx, false)
	back := a.taxView(kind, seoView)
	t.Caption = "SEO of terms of kind " + kind
	t.Columns = []ui.Col{{Key: "name", Label: "Term"}, {Key: "seo_title", Label: "SEO title"}, {Key: "seo_description", Label: "Description"}, {Key: "seo_noindex", Label: "Noindex"}, {Key: "seo_save", Label: "Save"}}
	t.Rows = nil
	for _, n := range nodes {
		if n.Kind != kind {
			continue
		}
		id := n.ID.String()
		f := termSEOFields(n.SEO)
		form := "seo-" + id
		var phTitle, phDesc string
		if lc.set != nil {
			in := seoInput{Title: n.Name, Kind: seo.KindTaxonomy, TermKind: n.Kind, Term: n.Name, SEO: lc.set, SiteName: lc.site, Tagline: lc.tagline}
			r := seo.Resolve(lc.set, in.ResolverInput())
			phTitle, phDesc = strings.TrimSpace(r.Title), strings.TrimSpace(r.Description)
		}
		edit := func(name, value, label, ph, control string, max int) ui.Cell {
			c := seoCounter(name, value)
			return ui.Cell{Edit: &ui.EditCell{Name: name, Value: value, Orig: value, Placeholder: ph, Label: label + " for " + n.Name, Control: control, Form: form, Max: max, Count: &ui.EditCount{Len: c.Len, Status: c.Status, Msg: c.Msg}}}
		}
		noindex := ""
		if f.NoIndex {
			noindex = "true"
		}
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: n.Name, Cells: []ui.Cell{
			{Text: n.Name, Depth: min(n.Depth, 8), Link: a.adminPath + "/settings/taxonomies/terms/" + id},
			edit(seoFieldTitle, f.Title, "SEO title", phTitle, "text", 60),
			edit(seoFieldDescription, f.Description, "SEO description", phDesc, "textarea", 160),
			{Edit: &ui.EditCell{Name: seoFieldNoIndex, Value: noindex, Orig: noindex, Label: "Noindex for " + n.Name, Control: "switch", Form: form}},
			{Edit: &ui.EditCell{Control: "save", Form: form, Action: a.adminPath + "/settings/taxonomies/terms/" + id + "/seo", CSRF: t.CSRF, Hidden: map[string]string{"back": back}}},
		}})
	}
	return t
}

// handleTermSEOInline is POST /settings/taxonomies/terms/{id}/seo: it writes the
// SEO title, description and noindex a row changed and nothing else. The
// canonical address, social image, name, slug and parent stay as stored.
//
// The term is read, patched and written without a version check (terms have
// none), so an edit saved between the read and the write is overwritten for the
// fields this row changed. Every other field, and every field the row did not
// change, is carried over from the fresh read.
func (a *Admin) handleTermSEOInline(w http.ResponseWriter, r *http.Request) {
	t, ok := a.loadTerm(w, r)
	if !ok {
		return
	}
	ed := readSEOEdit(r)
	back := backTo(r, a.adminPath+"/settings/taxonomies", a.taxView(t.Kind, seoView))
	reply := func(status int, v seoInlineReply) {
		if wantsJSON(r) {
			a.writeJSON(w, status, v)
			return
		}
		kind := "success"
		if v.Status != "saved" {
			kind = "error"
		}
		a.SetFlash(w, r, kind, v.Message)
		Redirect(w, r, back)
	}
	cur := termSEOFields(t.SEO)
	next := taxonomy.TermSEO{}
	if t.SEO != nil {
		next = *t.SEO
	}
	conflicts := map[string]string{}
	var changed []string
	for _, field := range []string{seoFieldTitle, seoFieldDescription, seoFieldNoIndex} {
		if !ed.Has[field] || ed.New.text(field) == ed.Orig.text(field) {
			continue
		}
		if cur.text(field) != ed.Orig.text(field) {
			conflicts[field] = cur.text(field)
			continue
		}
		switch field {
		case seoFieldTitle:
			next.MetaTitle = ed.New.Title
		case seoFieldDescription:
			next.MetaDescription = ed.New.Description
		default:
			next.NoIndex = ed.New.NoIndex
		}
		changed = append(changed, field)
	}
	if len(changed) > 0 {
		in := taxonomy.UpdateTermInput{ID: t.ID, Name: t.Name, Slug: t.Slug, SEO: &next}
		if _, err := a.deps.Content.Terms.UpdateTerm(r.Context(), in); err != nil {
			f := termForm{Errors: map[string]string{}}
			switch {
			case termFieldError(&f, err):
				msg := "The SEO values could not be saved."
				for _, m := range f.Errors {
					msg = sentence(m)
				}
				reply(http.StatusUnprocessableEntity, seoInlineReply{Status: "error", Message: msg, Values: cur, Counters: seoCounters(cur)})
			case errors.Is(err, taxonomy.ErrTermNotFound):
				a.notFound(w, r, "Term", a.adminPath+"/settings/taxonomies", "Back to taxonomies")
			default:
				a.serverError(w, r, "The term could not be saved", err)
			}
			return
		}
		cur = termSEOFields(&next)
		a.audit(r, "term.seo_update", t.ID.String(), map[string]any{"name": t.Name, "slug": t.Slug, "kind": t.Kind, "fields": changed})
	}
	out := seoInlineReply{Status: "saved", Values: cur, Counters: seoCounters(cur), Message: "Saved SEO for " + t.Name + "."}
	status := http.StatusOK
	if len(conflicts) > 0 {
		out.Status, out.Conflicts, status = "conflict", conflicts, http.StatusConflict
		out.Message = "Changed elsewhere: " + strings.Join(slices.Sorted(maps.Keys(conflicts)), ", ") + ". Review the current value."
	}
	reply(status, out)
}
