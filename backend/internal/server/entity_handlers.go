package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
)

// --- Schema Handlers ---

// handleListSchemas returns all entity schemas for the tenant.
func (s *Server) handleListSchemas(w http.ResponseWriter, r *http.Request) {
	schemas, err := s.entityService.ListSchemas(r.Context())
	if err != nil {
		s.logger.Error("failed to list schemas", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list schemas")
		return
	}

	// Ensure empty array instead of null
	if schemas == nil {
		schemas = []*entity.EntitySchema{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"schemas": schemas,
		"total":   len(schemas),
	})
}

// handleCreateSchema creates a new entity schema.
func (s *Server) handleCreateSchema(w http.ResponseWriter, r *http.Request) {

	var input entity.CreateSchemaInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if len(input.Schema) == 0 {
		writeError(w, http.StatusBadRequest, "schema is required")
		return
	}

	schema, err := s.entityService.CreateSchema(r.Context(), &input)
	if err != nil {
		if errors.Is(err, entity.ErrSlugAlreadyExists) {
			writeError(w, http.StatusConflict, "schema slug already exists")
			return
		}
		if errors.Is(err, entity.ErrInvalidSchema) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("failed to create schema", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create schema")
		return
	}

	writeJSON(w, http.StatusCreated, schema)
}

// handleGetSchema retrieves a schema by ID.
func (s *Server) handleGetSchema(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid schema ID")
		return
	}

	schema, err := s.entityService.GetSchema(r.Context(), id)
	if err != nil {
		if errors.Is(err, entity.ErrSchemaNotFound) {
			writeError(w, http.StatusNotFound, "schema not found")
			return
		}
		s.logger.Error("failed to get schema", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get schema")
		return
	}

	writeJSON(w, http.StatusOK, schema)
}

// handleUpdateSchema modifies a schema.
func (s *Server) handleUpdateSchema(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid schema ID")
		return
	}

	var input entity.UpdateSchemaInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	schema, err := s.entityService.UpdateSchema(r.Context(), id, &input)
	if err != nil {
		if errors.Is(err, entity.ErrSchemaNotFound) {
			writeError(w, http.StatusNotFound, "schema not found")
			return
		}
		if errors.Is(err, entity.ErrCannotModifyBuiltin) {
			writeError(w, http.StatusForbidden, "cannot modify built-in schema")
			return
		}
		if errors.Is(err, entity.ErrInvalidSchema) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("failed to update schema", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update schema")
		return
	}

	writeJSON(w, http.StatusOK, schema)
}

// handleDeleteSchema removes a schema.
func (s *Server) handleDeleteSchema(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid schema ID")
		return
	}

	err = s.entityService.DeleteSchema(r.Context(), id)
	if err != nil {
		if errors.Is(err, entity.ErrSchemaNotFound) {
			writeError(w, http.StatusNotFound, "schema not found")
			return
		}
		if errors.Is(err, entity.ErrCannotDeleteBuiltin) {
			writeError(w, http.StatusForbidden, "cannot delete built-in schema")
			return
		}
		if errors.Is(err, entity.ErrSchemaInUse) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.logger.Error("failed to delete schema", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete schema")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "schema deleted"})
}

// --- Entity Handlers ---

// handleListEntities returns entities with filtering and pagination.
// Supports both offset pagination (page param) and keyset pagination (cursor param).
func (s *Server) handleListEntities(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters for filtering
	filter := &entity.ListEntitiesFilter{}

	if schemaID := r.URL.Query().Get("schema_id"); schemaID != "" {
		id, err := uuid.Parse(schemaID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid schema_id")
			return
		}
		filter.SchemaID = &id
	}

	if status := r.URL.Query().Get("status"); status != "" {
		s, err := entity.ParseStatus(status)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		filter.Status = &s
	}

	filter.Search = r.URL.Query().Get("search")

	// Check if using offset pagination (page param present)
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		pageNum, err := strconv.Atoi(pageStr)
		if err != nil || pageNum < 1 {
			writeError(w, http.StatusBadRequest, "invalid page number")
			return
		}

		offsetPage := &entity.OffsetPage{
			Page:  pageNum,
			Limit: 20,
		}

		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			limit, err := strconv.Atoi(limitStr)
			if err == nil && limit > 0 && limit <= 100 {
				offsetPage.Limit = limit
			}
		}

		result, err := s.entityService.ListEntitiesWithOffset(r.Context(), filter, offsetPage)
		if err != nil {
			s.logger.Error("failed to list entities", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list entities")
			return
		}

		writeJSON(w, http.StatusOK, result)
		return
	}

	// Use keyset pagination (existing behavior)
	page := &entity.KeysetPage{
		Limit:     20,
		Direction: "next",
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err == nil && limit > 0 && limit <= 100 {
			page.Limit = limit
		}
	}

	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		page.Cursor = &cursor
	}

	if direction := r.URL.Query().Get("direction"); direction == "prev" {
		page.Direction = "prev"
	}

	result, err := s.entityService.ListEntities(r.Context(), filter, page)
	if err != nil {
		s.logger.Error("failed to list entities", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list entities")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// apiKeySummary prefixes a change summary with the API key name so versions
// written through a key are attributable in the history.
func apiKeySummary(keyName string, in *string) *string {
	out := "[api-key:" + keyName + "]"
	if in != nil && *in != "" {
		out += " " + *in
	}
	return &out
}

// logAPIKeyWrite records which API key performed an entity write. It is a
// no-op for session requests.
func (s *Server) logAPIKeyWrite(r *http.Request, op string, entityID string) {
	if k, ok := middleware.GetAPIKey(r.Context()); ok {
		s.logger.Info("entity write via api key",
			"api_key_id", k.ID, "api_key_name", k.Name, "op", op, "entity_id", entityID)
	}
}

// handleCreateEntity creates a new entity.
func (s *Server) handleCreateEntity(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	var input entity.CreateEntityInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if input.SchemaID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "schema_id is required")
		return
	}

	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	s.logAPIKeyWrite(r, "create", input.Name)

	ent, err := s.entityService.CreateEntity(r.Context(), &claims.UserID, &input)
	if err != nil {
		if errors.Is(err, entity.ErrSchemaNotFound) {
			writeError(w, http.StatusBadRequest, "schema not found")
			return
		}
		if writeEntityRefError(w, err) {
			return
		}
		if errors.Is(err, entity.ErrValidationFailed) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, entity.ErrRejectedByPlugin) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		s.logger.Error("failed to create entity", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create entity")
		return
	}

	writeJSON(w, http.StatusCreated, ent)
}

// handleGetEntity retrieves an entity by ID.
func (s *Server) handleGetEntity(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity ID")
		return
	}

	ent, err := s.entityService.GetEntity(r.Context(), id)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			writeError(w, http.StatusNotFound, "entity not found")
			return
		}
		s.logger.Error("failed to get entity", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get entity")
		return
	}

	writeJSON(w, http.StatusOK, ent)
}

// handleUpdateEntity modifies an entity.
func (s *Server) handleUpdateEntity(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity ID")
		return
	}

	var input entity.UpdateEntityInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if k, ok := middleware.GetAPIKey(r.Context()); ok {
		input.ChangeSummary = apiKeySummary(k.Name, input.ChangeSummary)
	}
	s.logAPIKeyWrite(r, "update", idStr)

	ent, err := s.entityService.UpdateEntity(r.Context(), id, &claims.UserID, &input)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			writeError(w, http.StatusNotFound, "entity not found")
			return
		}
		if errors.Is(err, entity.ErrValidationFailed) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if writeEntityRefError(w, err) {
			return
		}
		if errors.Is(err, entity.ErrRejectedByPlugin) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		s.logger.Error("failed to update entity", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update entity")
		return
	}

	writeJSON(w, http.StatusOK, ent)
}

// handleUpdateEntityStatus changes an entity's status.
func (s *Server) handleUpdateEntityStatus(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity ID")
		return
	}

	var input entity.UpdateStatusInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	s.logAPIKeyWrite(r, "status", idStr)

	ent, err := s.entityService.UpdateEntityStatus(r.Context(), id, &claims.UserID, input.Status)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			writeError(w, http.StatusNotFound, "entity not found")
			return
		}
		if errors.Is(err, entity.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		s.logger.Error("failed to update entity status", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update entity status")
		return
	}

	writeJSON(w, http.StatusOK, ent)
}

// handleDeleteEntity removes an entity.
func (s *Server) handleDeleteEntity(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity ID")
		return
	}

	err = s.entityService.DeleteEntity(r.Context(), id)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			writeError(w, http.StatusNotFound, "entity not found")
			return
		}
		if writeEntityRefError(w, err) {
			return
		}
		s.logger.Error("failed to delete entity", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete entity")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "entity deleted"})
}

// --- Version Handlers ---

// handleListEntityVersions returns version history for an entity.
func (s *Server) handleListEntityVersions(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity ID")
		return
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	versions, err := s.entityService.ListVersions(r.Context(), id, limit)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			writeError(w, http.StatusNotFound, "entity not found")
			return
		}
		s.logger.Error("failed to list entity versions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list versions")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"versions": versions,
		"total":    len(versions),
	})
}

// handleGetEntityVersion retrieves a specific version.
func (s *Server) handleGetEntityVersion(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity ID")
		return
	}

	versionStr := r.PathValue("version")
	version, err := strconv.Atoi(versionStr)
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "invalid version number")
		return
	}

	ver, err := s.entityService.GetVersion(r.Context(), id, version)
	if err != nil {
		if errors.Is(err, entity.ErrVersionNotFound) {
			writeError(w, http.StatusNotFound, "version not found")
			return
		}
		s.logger.Error("failed to get entity version", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get version")
		return
	}

	writeJSON(w, http.StatusOK, ver)
}

// handleRestoreEntityVersion restores an entity to a previous version.
func (s *Server) handleRestoreEntityVersion(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid entity ID")
		return
	}

	versionStr := r.PathValue("version")
	version, err := strconv.Atoi(versionStr)
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "invalid version number")
		return
	}

	// The service sets the restore summary itself, so a key restore is
	// attributed through this log line (id+name) and the version's changed_by.
	s.logAPIKeyWrite(r, "restore", idStr)

	ent, err := s.entityService.RestoreVersion(r.Context(), id, &claims.UserID, version)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			writeError(w, http.StatusNotFound, "entity not found")
			return
		}
		if errors.Is(err, entity.ErrVersionNotFound) {
			writeError(w, http.StatusNotFound, "version not found")
			return
		}
		if writeEntityRefError(w, err) {
			return
		}
		if errors.Is(err, entity.ErrValidationFailed) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, entity.ErrRejectedByPlugin) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		s.logger.Error("failed to restore entity version", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to restore version")
		return
	}

	writeJSON(w, http.StatusOK, ent)
}

// handleValidateEntityAttributes validates attributes against a schema.
func (s *Server) handleValidateEntityAttributes(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SchemaID   uuid.UUID       `json:"schema_id"`
		Attributes json.RawMessage `json:"attributes"`
	}

	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if input.SchemaID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "schema_id is required")
		return
	}

	result := s.entityService.ValidateEntityAttributes(r.Context(), input.SchemaID, input.Attributes)
	writeJSON(w, http.StatusOK, result)
}

// writeEntityRefError answers the errors of an entity's slug, parent and
// author with a stable "code" next to the message, so clients and the admin
// can react to them: they are the caller's mistakes, never a 500.
func writeEntityRefError(w http.ResponseWriter, err error) bool {
	status, code := 0, ""
	switch {
	case errors.Is(err, entity.ErrSlugAlreadyExists):
		status, code = http.StatusConflict, "slug_exists"
	case errors.Is(err, entity.ErrHasChildren):
		status, code = http.StatusConflict, "has_children"
	case errors.Is(err, entity.ErrParentNotFound):
		status, code = http.StatusUnprocessableEntity, "parent_not_found"
	case errors.Is(err, entity.ErrParentCycle):
		status, code = http.StatusUnprocessableEntity, "parent_cycle"
	case errors.Is(err, entity.ErrParentTooDeep):
		status, code = http.StatusUnprocessableEntity, "parent_too_deep"
	case errors.Is(err, entity.ErrAuthorNotFound):
		status, code = http.StatusUnprocessableEntity, "author_not_found"
	case errors.Is(err, entity.ErrInvalidDoc):
		status, code = http.StatusUnprocessableEntity, "invalid_page_doc"
	case errors.Is(err, entity.ErrPageSectionsRemoved):
		status, code = http.StatusUnprocessableEntity, "page_sections_removed"
	default:
		return false
	}
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
	return true
}
