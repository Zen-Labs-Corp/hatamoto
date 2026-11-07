package server

import (
	"bytes"
	"net/http"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/render"
)

// formPagePrefix is where a form's hosted page lives: /f/{slug}. The prefix is
// reserved in adminpath.ReservedPrefixes.
const formPagePrefix = "/f/"

// handleFormPage serves GET /f/{slug}: the form on a page of its own, inside
// the site theme, for sharing as a link.
//
// The URL registry stays in charge of paths: when the path resolves to an
// entity, a redirect or any other page, the usual page handler answers and the
// form route never shadows it. Otherwise an active form is rendered and a
// paused, archived or unknown one gets the themed 404. The page is noindex.
func (s *Server) handleFormPage(w http.ResponseWriter, r *http.Request) {
	h := s.renderHandlers
	if h == nil || s.formService == nil {
		http.NotFound(w, r)
		return
	}
	h, done := h.forRequest(w, r)
	if done {
		return
	}
	ctx := r.Context()
	path := requestPath(r)
	rr, _, err := h.resolve(ctx, path, r)
	if err != nil {
		h.writeFailure(w, r, err)
		return
	}
	if rr != nil && (rr.Redirect != nil || rr.Page != nil || rr.Archive != nil || rr.Listing != nil || rr.Registry != nil) {
		h.handleRenderCatchAll(w, r)
		return
	}

	w.Header().Set("X-Robots-Tag", "noindex")
	f, err := s.formService.GetFormBySlug(ctx, r.PathValue("slug"))
	if err != nil || f == nil || f.Status != form.StatusActive {
		h.writeNotFound(w, r, path)
		return
	}
	v, err := h.buildView(ctx, r, &routeResult{NotFound: true})
	if err != nil {
		h.logger.ErrorContext(ctx, "form page: build view", "error", err, "path", path)
		h.writeError(w, r, http.StatusInternalServerError, nil)
		return
	}
	v.Kind = render.KindPage
	v.Page = render.PageMeta{Name: f.Name, Title: f.Name, NoIndex: true}
	// One contact_form section: the theme wraps it in its standard container
	// like any page section; the form name is the page's h1.
	content := map[string]any{"form_slug": f.Slug, "heading": f.Name, "heading_level": 1}
	if f.Description != nil && *f.Description != "" {
		content["subheading"] = *f.Description
	}
	v.Doc = nil
	v.Sections = []render.Section{{ID: "form-page", Type: "contact_form", Content: content}}
	v.Forms = s.formsForSections(ctx, formSections(v.Sections, nil))
	v.Ctx = ctx
	var buf bytes.Buffer
	if err := h.engine.RenderPage(&buf, v); err != nil {
		h.logger.ErrorContext(ctx, "form page: render", "error", err, "path", path)
		h.writeError(w, r, http.StatusInternalServerError, v)
		return
	}
	// Private: the page changes with ?submitted and ?form_error, and a paused
	// form must stop being served at once.
	h.writeHTML(w, r, http.StatusOK, buf.Bytes(), true)
}
