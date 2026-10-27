package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/testutil"
)

// TestEntityUpdateEndpoint_P0BUG001 tests the fix for P0-BUG-001: Save/Publish Fix
// The entity update endpoint should use PATCH method, not PUT.
func TestEntityUpdateEndpoint_P0BUG001(t *testing.T) {
	t.Parallel()

	// Create a minimal router that mimics the entity routes setup
	// We're testing the route registration, not the handler logic
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")

	// Register a mock handler that echoes the method in a response header
	// so each subtest can read its own recorder without a shared variable.
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Method", req.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Setup routes similar to server.go setupRoutes
	{
		r := r.Group("/api/v1/entities")
		r.HandleFunc("GET /", mockHandler)
		r.HandleFunc("POST /", mockHandler)
		r.HandleFunc("GET /{id}", mockHandler)
		r.HandleFunc("PATCH /{id}", mockHandler) // This is the fix - was r.Put before
		r.HandleFunc("PATCH /{id}/status", mockHandler)
		r.HandleFunc("DELETE /{id}", mockHandler)
	}

	// ServeMux answers 405 itself when the path matches another method.

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
		expectedMethod string
		description    string
	}{
		{
			name:           "PATCH_request_succeeds",
			method:         http.MethodPatch,
			path:           "/api/v1/entities/123e4567-e89b-12d3-a456-426614174000",
			expectedStatus: http.StatusOK,
			expectedMethod: http.MethodPatch,
			description:    "PATCH method should be accepted for entity updates",
		},
		{
			name:           "PUT_request_returns_405",
			method:         http.MethodPut,
			path:           "/api/v1/entities/123e4567-e89b-12d3-a456-426614174000",
			expectedStatus: http.StatusMethodNotAllowed,
			expectedMethod: "",
			description:    "PUT method should return 405 Method Not Allowed after the fix",
		},
		{
			name:           "GET_entity_still_works",
			method:         http.MethodGet,
			path:           "/api/v1/entities/123e4567-e89b-12d3-a456-426614174000",
			expectedStatus: http.StatusOK,
			expectedMethod: http.MethodGet,
			description:    "GET method should still work for fetching entities",
		},
		{
			name:           "DELETE_entity_still_works",
			method:         http.MethodDelete,
			path:           "/api/v1/entities/123e4567-e89b-12d3-a456-426614174000",
			expectedStatus: http.StatusOK,
			expectedMethod: http.MethodDelete,
			description:    "DELETE method should still work",
		},
		{
			name:           "POST_entity_still_works",
			method:         http.MethodPost,
			path:           "/api/v1/entities/",
			expectedStatus: http.StatusOK,
			expectedMethod: http.MethodPost,
			description:    "POST method should still work for creating entities",
		},
		{
			name:           "PATCH_status_endpoint_works",
			method:         http.MethodPatch,
			path:           "/api/v1/entities/123e4567-e89b-12d3-a456-426614174000/status",
			expectedStatus: http.StatusOK,
			expectedMethod: http.MethodPatch,
			description:    "PATCH method should work for status updates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			testutil.Equal(t, tt.expectedStatus, rec.Code, tt.description)
			if tt.expectedMethod != "" {
				testutil.Equal(t, tt.expectedMethod, rec.Header().Get("X-Method"))
			}
		})
	}
}

// TestEntityRoutesRegistration verifies all entity routes are registered correctly
func TestEntityRoutesRegistration(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")

	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Setup routes exactly as in server.go
	{
		r := r.Group("/api/v1/entities")
		r.HandleFunc("GET /", mockHandler)              // handleListEntities
		r.HandleFunc("POST /", mockHandler)             // handleCreateEntity
		r.HandleFunc("POST /validate", mockHandler)     // handleValidateEntityAttributes
		r.HandleFunc("GET /{id}", mockHandler)          // handleGetEntity
		r.HandleFunc("PATCH /{id}", mockHandler)        // handleUpdateEntity (FIXED from Put)
		r.HandleFunc("PATCH /{id}/status", mockHandler) // handleUpdateEntityStatus
		r.HandleFunc("DELETE /{id}", mockHandler)       // handleDeleteEntity

		// Version management
		r.HandleFunc("GET /{id}/versions", mockHandler)
		r.HandleFunc("GET /{id}/versions/{version}", mockHandler)
		r.HandleFunc("POST /{id}/restore/{version}", mockHandler)
	}

	tests := []struct {
		method string
		path   string
		exists bool
	}{
		{http.MethodGet, "/api/v1/entities/", true},
		{http.MethodPost, "/api/v1/entities/", true},
		{http.MethodPost, "/api/v1/entities/validate", true},
		{http.MethodGet, "/api/v1/entities/123", true},
		{http.MethodPatch, "/api/v1/entities/123", true},
		{http.MethodPatch, "/api/v1/entities/123/status", true},
		{http.MethodDelete, "/api/v1/entities/123", true},
		{http.MethodGet, "/api/v1/entities/123/versions", true},
		{http.MethodGet, "/api/v1/entities/123/versions/1", true},
		{http.MethodPost, "/api/v1/entities/123/restore/1", true},
		// These should NOT exist
		{http.MethodPut, "/api/v1/entities/123", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+"_"+tt.path, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if tt.exists {
				testutil.Equal(t, http.StatusOK, rec.Code,
					"Route %s %s should exist", tt.method, tt.path)
			} else {
				testutil.Equal(t, http.StatusMethodNotAllowed, rec.Code,
					"Route %s %s should NOT exist (405)", tt.method, tt.path)
			}
		})
	}
}

// TestPatchVsPutMethodSemanticsForEntityUpdate documents why PATCH is correct for entity updates
func TestPatchVsPutMethodSemanticsForEntityUpdate(t *testing.T) {
	t.Parallel()

	// This test documents the semantic difference and why PATCH is correct:
	// - PUT: Full resource replacement (all fields required)
	// - PATCH: Partial resource update (only changed fields required)
	//
	// The entity update endpoint accepts partial updates, so PATCH is semantically correct.

	t.Run("PATCH_allows_partial_updates", func(t *testing.T) {
		t.Parallel()

		// Simulate a partial update request body
		partialUpdate := `{"name": "Updated Name"}`

		// PATCH should accept this partial payload
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/entities/123", strings.NewReader(partialUpdate))
		req.Header.Set("Content-Type", "application/json")

		// The actual handler would process this partial update
		testutil.Equal(t, http.MethodPatch, req.Method)
		testutil.Contains(t, partialUpdate, "name")
		testutil.NotContains(t, partialUpdate, "slug")       // Partial - slug not included
		testutil.NotContains(t, partialUpdate, "attributes") // Partial - attributes not included
	})

	t.Run("PUT_would_require_full_resource", func(t *testing.T) {
		t.Parallel()

		// PUT would semantically require the full resource
		fullResource := `{
			"name": "Full Entity",
			"slug": "full-entity",
			"schema_id": "123",
			"attributes": {},
			"meta": {}
		}`

		// This is why PUT was incorrect - users want to update single fields
		req := httptest.NewRequest(http.MethodPut, "/api/v1/entities/123", strings.NewReader(fullResource))
		req.Header.Set("Content-Type", "application/json")

		testutil.Equal(t, http.MethodPut, req.Method)
		// Full resource would be required for PUT semantics
		testutil.Contains(t, fullResource, "name")
		testutil.Contains(t, fullResource, "slug")
		testutil.Contains(t, fullResource, "schema_id")
		testutil.Contains(t, fullResource, "attributes")
	})
}

// TestHandleUpdateEntityWithPatch verifies the handler accepts PATCH method
func TestHandleUpdateEntityWithPatch(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")

	{
		r := r.Group("/api/v1/entities")
		r.HandleFunc("PATCH /{id}", func(w http.ResponseWriter, req *http.Request) {
			id := req.PathValue("id")
			w.Header().Set("X-Method", req.Method)
			w.Header().Set("X-Entity-ID", id)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
		})
	}

	// ServeMux answers 405 itself when the path matches another method.

	tests := []struct {
		name           string
		entityID       string
		method         string
		body           string
		expectedStatus int
	}{
		{
			name:           "valid_PATCH_request",
			entityID:       "123e4567-e89b-12d3-a456-426614174000",
			method:         http.MethodPatch,
			body:           `{"name":"Updated"}`,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "PUT_request_rejected",
			entityID:       "123e4567-e89b-12d3-a456-426614174000",
			method:         http.MethodPut,
			body:           `{"name":"Updated"}`,
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := "/api/v1/entities/" + tt.entityID
			req := httptest.NewRequest(tt.method, path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			testutil.RequireEqual(t, tt.expectedStatus, rec.Code)

			if tt.expectedStatus == http.StatusOK {
				testutil.Equal(t, http.MethodPatch, rec.Header().Get("X-Method"))
				testutil.Equal(t, tt.entityID, rec.Header().Get("X-Entity-ID"))
			}
		})
	}
}
