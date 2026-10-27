package server

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
)

// handleListAPIKeys returns all API keys for the tenant.
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.apiKeyService.List(r.Context())
	if err != nil {
		s.logger.Error("failed to list API keys", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list API keys")
		return
	}

	// Ensure empty array instead of null
	if keys == nil {
		keys = []*auth.APIKey{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"api_keys": keys,
		"total":    len(keys),
	})
}

// handleCreateAPIKey creates a new API key.
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var input auth.CreateAPIKeyInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	key, err := s.apiKeyService.Create(r.Context(), &claims.UserID, &input)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidScope) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("failed to create API key", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create API key")
		return
	}

	// Return with the plain key (only shown once)
	writeJSON(w, http.StatusCreated, key)
}

// handleGetAPIKey retrieves an API key by ID.
func (s *Server) handleGetAPIKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid API key ID")
		return
	}

	key, err := s.apiKeyService.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			writeError(w, http.StatusNotFound, "API key not found")
			return
		}
		s.logger.Error("failed to get API key", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get API key")
		return
	}

	writeJSON(w, http.StatusOK, key)
}

// handleUpdateAPIKey modifies an API key.
func (s *Server) handleUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid API key ID")
		return
	}

	var input auth.UpdateAPIKeyInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	key, err := s.apiKeyService.Update(r.Context(), id, &input)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			writeError(w, http.StatusNotFound, "API key not found")
			return
		}
		if errors.Is(err, auth.ErrInvalidScope) || errors.Is(err, auth.ErrInvalidRateLimit) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("failed to update API key", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update API key")
		return
	}

	writeJSON(w, http.StatusOK, key)
}

// handleRevokeAPIKey revokes an API key.
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid API key ID")
		return
	}

	claims, ok := middleware.GetClaims(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	err = s.apiKeyService.Revoke(r.Context(), id, &claims.UserID)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			writeError(w, http.StatusNotFound, "API key not found")
			return
		}
		s.logger.Error("failed to revoke API key", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to revoke API key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "API key revoked"})
}

// handleDeleteAPIKey permanently removes an API key.
func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid API key ID")
		return
	}

	err = s.apiKeyService.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			writeError(w, http.StatusNotFound, "API key not found")
			return
		}
		s.logger.Error("failed to delete API key", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete API key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "API key deleted"})
}

// handleRegenerateAPIKey generates a new key value for an existing API key.
func (s *Server) handleRegenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid API key ID")
		return
	}

	plainKey, err := s.apiKeyService.Regenerate(r.Context(), id)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			writeError(w, http.StatusNotFound, "API key not found")
			return
		}
		s.logger.Error("failed to regenerate API key", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to regenerate API key")
		return
	}

	// Get the updated key details
	key, err := s.apiKeyService.GetByID(r.Context(), id)
	if err != nil {
		s.logger.Error("failed to get API key after regeneration", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retrieve API key")
		return
	}

	writeJSON(w, http.StatusOK, &auth.APIKeyWithSecret{
		APIKey:   *key,
		PlainKey: plainKey,
	})
}

// handleGetAPIKeyScopes returns available API key scopes.
func (s *Server) handleGetAPIKeyScopes(w http.ResponseWriter, r *http.Request) {
	scopes := auth.ValidScopes()
	scopeList := make([]map[string]interface{}, len(scopes))

	descriptions := map[auth.APIKeyScope]string{
		auth.ScopeReadEntities:     "Read access to entities",
		auth.ScopeWriteEntities:    "Write access to entities (includes read)",
		auth.ScopeReadCitations:    "Read access to citation events",
		auth.ScopeReadReports:      "Read access to reports",
		auth.ScopeWriteReports:     "Write access to reports (includes read)",
		auth.ScopeReadQuerySuites:  "Read access to query suites",
		auth.ScopeWriteQuerySuites: "Write access to query suites (includes read)",
		auth.ScopeWriteMedia:       "Upload media (MCP only; REST keys cannot reach media routes)",
		auth.ScopeWriteMenus:       "Edit menus (MCP only)",
		auth.ScopeWriteRedirects:   "Create and delete redirects (MCP only)",
		auth.ScopeWriteTerms:       "Create tags and categories (MCP only)",
		auth.ScopeAll:              "Full access to all resources",
	}

	for i, scope := range scopes {
		scopeList[i] = map[string]interface{}{
			"scope":       scope,
			"description": descriptions[scope],
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"scopes": scopeList,
	})
}
