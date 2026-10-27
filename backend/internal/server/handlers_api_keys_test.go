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

func TestHandleUpdateAPIKeyRejectsZeroRateLimit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{logger: logger, apiKeyService: auth.NewAPIKeyService(nil, logger)}

	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	r.HandleFunc("PATCH /api-keys/{id}", s.handleUpdateAPIKey)

	for _, body := range []string{`{"rate_limit":0}`, `{"rate_limit":-5}`} {
		req := httptest.NewRequest(http.MethodPatch, "/api-keys/6f1b1d52-8b3e-4a52-9c2f-0a1b2c3d4e5f", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400 (%s)", body, rec.Code, rec.Body.String())
		}
	}
}
