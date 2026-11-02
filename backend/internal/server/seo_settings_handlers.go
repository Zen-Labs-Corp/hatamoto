package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/seosvc"
)

// seoSettingsHandlers serves the SEO settings REST API: the same document the
// MCP seo tools read and change, for the owner and for API keys holding
// read:seo / write:seo.
type seoSettingsHandlers struct {
	svc    seosvc.Service
	logger *slog.Logger
}

func mountSEOSettings(r *httpx.Group, h *seoSettingsHandlers) {
	r = r.Group("/settings/seo", middleware.RequireOwnerOrKeyScope(auth.ScopeReadSEO, auth.ScopeWriteSEO))
	r.HandleFunc("GET /", h.handleGet)
	r.HandleFunc("PATCH /", h.handlePatch)
}

func (s *Server) mountSEOSettingsRoutes(r *httpx.Group) {
	h := &seoSettingsHandlers{logger: s.logger}
	// A nil *seoService inside the interface would not be a nil interface.
	if svc := s.newSEOService(); svc != nil {
		h.svc = svc
	}
	mountSEOSettings(r, h)
}

func (h *seoSettingsHandlers) handleGet(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "seo settings are not available")
		return
	}
	doc, err := h.svc.Get(r.Context())
	if err != nil {
		h.logger.Error("seo settings: get", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load SEO settings")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// handlePatch applies a partial update: top-level sections are merged, a
// refused patch answers 422 {"error","fields"} and changes nothing.
func (h *seoSettingsHandlers) handlePatch(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "seo settings are not available")
		return
	}
	var patch map[string]json.RawMessage
	if err := parseJSON(r, &patch); err != nil || patch == nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	doc, err := h.svc.Update(r.Context(), seoActor(r), patch)
	var verr *seosvc.ValidationError
	switch {
	case errors.As(err, &verr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation failed", "fields": verr.Fields})
	case err != nil:
		h.logger.Error("seo settings: update", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to save SEO settings")
	default:
		writeJSON(w, http.StatusOK, doc)
	}
}

// seoActor names the caller for the audit row: the API key, else the owner.
func seoActor(r *http.Request) seosvc.Actor {
	if key, ok := middleware.GetAPIKey(r.Context()); ok {
		return seosvc.Actor{Via: "api", KeyName: key.Name, KeyID: key.ID.String()}
	}
	a := seosvc.Actor{Via: "api"}
	if c, ok := middleware.GetClaims(r.Context()); ok {
		a.UserID, a.Email = c.UserID.String(), c.Email
	}
	return a
}
