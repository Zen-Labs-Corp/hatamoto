package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/httpx"
)

// Request bodies are decoded strictly: a repeated member or data after the
// value is a 400, never a silently merged or truncated request.
func TestParseJSONStrictRejectsDuplicatesAndTrailingData(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{logger: logger, apiKeyService: auth.NewAPIKeyService(nil, logger)}

	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	r.HandleFunc("PATCH /api-keys/{id}", s.handleUpdateAPIKey)

	for name, body := range map[string]string{
		"duplicate key":   `{"name":"a","name":"b"}`,
		"trailing junk":   `{"name":"a"} junk`,
		"trailing object": `{"name":"a"}{"name":"b"}`,
	} {
		req := httptest.NewRequest(http.MethodPatch, "/api-keys/6f1b1d52-8b3e-4a52-9c2f-0a1b2c3d4e5f", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A nil slice in a response is an empty array, not null.
func TestWriteJSONNilSliceIsEmptyArray(t *testing.T) {
	var keys []*auth.APIKey
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"api_keys": keys, "total": len(keys)})
	if got, want := strings.TrimSpace(rec.Body.String()), `{"api_keys":[],"total":0}`; got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}
