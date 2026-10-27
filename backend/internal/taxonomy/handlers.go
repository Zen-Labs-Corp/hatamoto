package taxonomy

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Handlers exposes the HTTP surface for the taxonomy package.
type Handlers struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandlers wires Handlers around a Service.
func NewHandlers(svc *Service, logger *slog.Logger) *Handlers {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handlers{svc: svc, logger: logger}
}

// --- admin: terms CRUD ---

type termRequest struct {
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug,omitempty"`
	Description *string    `json:"description,omitempty"`
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	// ClearParent (update only) makes the term a root.
	ClearParent bool `json:"clear_parent,omitempty"`
}

// writeTermError maps a service error to a response; false when err is not a
// parent/slug/validation error the caller should handle itself.
func writeTermError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrTermSlugConflict):
		writeJSONError(w, http.StatusConflict, "slug_conflict")
	case errors.Is(err, ErrTermParentNotFound):
		writeJSONError(w, http.StatusBadRequest, "parent_not_found")
	case errors.Is(err, ErrTermParentInvalid):
		writeJSONError(w, http.StatusBadRequest, "invalid_parent")
	case strings.HasPrefix(err.Error(), "invalid_"):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		return false
	}
	return true
}

// ListTerms handles GET /api/v1/admin/terms.
// Query params: kind (tag|category), search, page, per_page.
func (h *Handlers) ListTerms(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	search := q.Get("search")
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	res, err := h.svc.ListTerms(r.Context(), kind, search, page, perPage)
	if err != nil {
		if strings.HasPrefix(err.Error(), "invalid_") {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.logger.Warn("taxonomy: list terms failed", slog.String("error", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// CreateTerm handles POST /api/v1/admin/terms.
func (h *Handlers) CreateTerm(w http.ResponseWriter, r *http.Request) {
	var body termRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	desc := ""
	if body.Description != nil {
		desc = *body.Description
	}
	t, err := h.svc.CreateTerm(r.Context(), CreateTermInput{
		Kind: body.Kind, Name: body.Name, Slug: body.Slug, Description: desc, ParentID: body.ParentID,
	})
	if err != nil {
		if !writeTermError(w, err) {
			h.logger.Warn("taxonomy: create term failed", slog.String("error", err.Error()))
			writeJSONError(w, http.StatusInternalServerError, "create_failed")
		}
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// UpdateTerm handles PUT /api/v1/admin/terms/{id}.
func (h *Handlers) UpdateTerm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_id")
		return
	}
	var body termRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	t, err := h.svc.UpdateTerm(r.Context(), UpdateTermInput{
		ID: id, Name: body.Name, Slug: body.Slug,
		Description: body.Description, ParentID: body.ParentID, ClearParent: body.ClearParent,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrTermNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
		case writeTermError(w, err):
		default:
			h.logger.Warn("taxonomy: update term failed", slog.String("error", err.Error()))
			writeJSONError(w, http.StatusInternalServerError, "update_failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// DeleteTerm handles DELETE /api/v1/admin/terms/{id}. Idempotent — a
// missing ID still returns 204 because cascade deletion semantics make
// "didn't exist" indistinguishable from "we just removed it."
func (h *Handlers) DeleteTerm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_id")
		return
	}
	if err := h.svc.DeleteTerm(r.Context(), id); err != nil {
		h.logger.Warn("taxonomy: delete term failed", slog.String("error", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "delete_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- admin: entity term attachments ---

type entityTermsRequest struct {
	TermIDs []uuid.UUID `json:"term_ids"`
}

type entityTermsResponse struct {
	Terms []*Term `json:"terms"`
}

// GetEntityTerms handles GET /api/v1/admin/entities/{entity_id}/terms.
func (h *Handlers) GetEntityTerms(w http.ResponseWriter, r *http.Request) {
	entityID, err := uuid.Parse(r.PathValue("entity_id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_entity_id")
		return
	}
	terms, err := h.svc.ListTermsForEntity(r.Context(), entityID)
	if err != nil {
		h.logger.Warn("taxonomy: list entity terms failed", slog.String("error", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	writeJSON(w, http.StatusOK, entityTermsResponse{Terms: terms})
}

// ReplaceEntityTerms handles PUT /api/v1/admin/entities/{entity_id}/terms.
// Set-replace semantics — the supplied term_ids list is the new full set
// of attachments for the entity. Empty list detaches everything.
func (h *Handlers) ReplaceEntityTerms(w http.ResponseWriter, r *http.Request) {
	entityID, err := uuid.Parse(r.PathValue("entity_id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_entity_id")
		return
	}
	var body entityTermsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	if err := h.svc.ReplaceEntityTerms(r.Context(), entityID, body.TermIDs); err != nil {
		switch {
		case strings.HasPrefix(err.Error(), "unknown_term"):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		case strings.HasPrefix(err.Error(), "too_many_terms"):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		case strings.HasPrefix(err.Error(), "missing_"):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		default:
			h.logger.Warn("taxonomy: replace entity terms failed",
				slog.String("entity_id", entityID.String()),
				slog.String("error", err.Error()))
			writeJSONError(w, http.StatusInternalServerError, "replace_failed")
		}
		return
	}
	// Re-read so the client gets the canonical post-state in one call.
	terms, err := h.svc.ListTermsForEntity(r.Context(), entityID)
	if err != nil {
		h.logger.Warn("taxonomy: post-replace list failed", slog.String("error", err.Error()))
		// Still 200 — the write succeeded.
		writeJSON(w, http.StatusOK, entityTermsResponse{Terms: []*Term{}})
		return
	}
	writeJSON(w, http.StatusOK, entityTermsResponse{Terms: terms})
}

// --- public: term archive ---

// PublicTermArchive handles GET /api/v1/public/terms/{kind}/{slug}/entities.
// Query params: page, per_page.
func (h *Handlers) PublicTermArchive(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(r.PathValue("kind"))
	slug := strings.ToLower(r.PathValue("slug"))
	if !ValidKind(kind) {
		writeJSONError(w, http.StatusNotFound, "invalid_kind")
		return
	}
	if !ValidSlug(slug) {
		writeJSONError(w, http.StatusNotFound, "invalid_slug")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("per_page"))

	res, err := h.svc.ListEntitiesForTerm(r.Context(), kind, slug, page, perPage)
	if err != nil {
		if errors.Is(err, ErrTermNotFound) {
			writeJSONError(w, http.StatusNotFound, "term_not_found")
			return
		}
		h.logger.Warn("taxonomy: public archive failed", slog.String("error", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "archive_failed")
		return
	}
	// Cache aggressively at the edge — term archives change rarely and
	// the matching Next.js page revalidates every 60s, so a 60s
	// browser/proxy TTL with a longer shared cache TTL is safe.
	// Admin term-write endpoints are unaffected; they live under /admin.
	w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=300")
	writeJSON(w, http.StatusOK, res)
}

// --- response helpers (kept local to avoid a server-package import) ---

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
