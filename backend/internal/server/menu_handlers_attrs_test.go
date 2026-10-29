package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/menu"
)

// Invalid item settings are a client error, not a server fault.
func TestCreateMenuItemInvalidAttrsIs400(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &Server{logger: logger, menuService: menu.NewService(nil, logger)}
	body := `{"label":"X","link_type":"custom","url":"/x","attrs":{"style":"Not Valid"}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.SetPathValue("id", "6f1b7a9e-0000-4000-8000-000000000001")
	rec := httptest.NewRecorder()
	srv.handleCreateMenuItem(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "style") {
		t.Fatalf("got %d %s, want 400 naming the style", rec.Code, rec.Body)
	}
}
