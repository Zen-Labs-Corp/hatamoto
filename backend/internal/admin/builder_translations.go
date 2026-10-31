package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/entity"
)

// translationsGate is builderGate for pages of a multi-locale site with copies
// on: a reuse block or theme part is not translated, and a single-locale site
// has no Translations at all.
func (a *Admin) translationsGate(w http.ResponseWriter, r *http.Request) (*entity.Entity, bool) {
	e, _, ok := a.builderGate(w, r)
	if !ok {
		return nil, false
	}
	_, multi := a.multiLocale(r.Context())
	if a.entityRole(r.Context(), e) != rolePage || !multi || e.Locale == entity.LocaleShared || a.deps.Content.Copier == nil {
		a.Error(w, r, http.StatusNotFound, "No translations", "This page has no translations.")
		return nil, false
	}
	return e, true
}

// handleBuilderTranslations is GET /entities/{id}/builder/translations: the
// Translations panel the builder's drawer shows. It writes nothing.
func (a *Admin) handleBuilderTranslations(w http.ResponseWriter, r *http.Request) {
	e, ok := a.translationsGate(w, r)
	if !ok {
		return
	}
	set, _ := a.multiLocale(r.Context())
	p, err := a.translationsPanel(r.Context(), r, e, set)
	if err != nil {
		a.serverError(w, r, "The translations could not be loaded", err)
		return
	}
	v := a.NewView(w, r, "Translations")
	v.Data = p
	a.Render(w, r, http.StatusOK, "entities/buildertranslations", v)
}
