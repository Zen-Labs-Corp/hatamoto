package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/sectiontemplate"
)

// handleListSectionTemplates returns section templates with optional filtering.
// GET /api/v1/section-templates?section_type=X&search=Y&limit=N&offset=N
func (s *Server) handleListSectionTemplates(w http.ResponseWriter, r *http.Request) {
	filter := &sectiontemplate.ListFilter{
		SectionType: r.URL.Query().Get("section_type"),
		Search:      r.URL.Query().Get("search"),
	}

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	result, err := s.sectionTemplateService.List(r.Context(), filter, limit, offset)
	if err != nil {
		s.logger.Error("failed to list section templates", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list section templates")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleCreateSectionTemplate creates a new section template.
// POST /api/v1/section-templates
func (s *Server) handleCreateSectionTemplate(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var input sectiontemplate.CreateInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tmpl, err := s.sectionTemplateService.Create(r.Context(), &claims.UserID, &input)
	if err != nil {
		var validationErr *sectiontemplate.ValidationError
		if errors.As(err, &validationErr) {
			writeError(w, http.StatusBadRequest, validationErr.Error())
			return
		}
		s.logger.Error("failed to create section template", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create section template")
		return
	}

	writeJSON(w, http.StatusCreated, tmpl)
}

// handleDeleteSectionTemplate deletes a section template by ID.
// DELETE /api/v1/section-templates/:id
func (s *Server) handleDeleteSectionTemplate(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template ID")
		return
	}

	err = s.sectionTemplateService.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, sectiontemplate.ErrTemplateNotFound) {
			writeError(w, http.StatusNotFound, "section template not found")
			return
		}
		s.logger.Error("failed to delete section template", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete section template")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "section template deleted"})
}

// handleRecordSectionTemplateUsage increments the usage count for a template.
// POST /api/v1/section-templates/:id/use
func (s *Server) handleRecordSectionTemplateUsage(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template ID")
		return
	}

	err = s.sectionTemplateService.RecordUsage(r.Context(), id)
	if err != nil {
		if errors.Is(err, sectiontemplate.ErrTemplateNotFound) {
			writeError(w, http.StatusNotFound, "section template not found")
			return
		}
		s.logger.Error("failed to record section template usage", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record usage")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "usage recorded"})
}
