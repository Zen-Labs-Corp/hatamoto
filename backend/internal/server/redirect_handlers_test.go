package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/redirect"
)

// stubRedirectStore embeds the Store interface so only the methods under test
// need bodies; any other call panics on the nil interface.
type stubRedirectStore struct {
	redirect.Store
	rows []*redirect.Redirect
}

func (f *stubRedirectStore) GetActiveRedirects(context.Context) ([]*redirect.Redirect, error) {
	return f.rows, nil
}
func (f *stubRedirectStore) AddRedirectHits(context.Context, uuid.UUID, int) error { return nil }
func (f *stubRedirectStore) GetRedirectByID(_ context.Context, id uuid.UUID) (*redirect.Redirect, error) {
	for _, r := range f.rows {
		if r.ID == id {
			c := *r
			return &c, nil
		}
	}
	return nil, redirect.ErrRedirectNotFound
}

func newRedirectTestServer(t *testing.T, rows ...*redirect.Redirect) *Server {
	t.Helper()
	for _, r := range rows {
		r.ID, r.StatusCode, r.IsActive = uuid.New(), 301, true
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := redirect.NewServiceWithStore(&stubRedirectStore{rows: rows}, logger)
	t.Cleanup(svc.Close)
	return &Server{logger: logger, redirectService: svc}
}

func TestCreateAndUpdateRedirectRejectBadPatterns(t *testing.T) {
	existing := &redirect.Redirect{SourcePath: "/docs/**", TargetURL: "/elsewhere"}
	s := newRedirectTestServer(t, existing)
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	r.HandleFunc("POST /", s.handleCreateRedirect)
	r.HandleFunc("PUT /{id}", s.handleUpdateRedirect)

	do := func(method, url, body string) int {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, url, strings.NewReader(body)))
		return rec.Code
	}
	for name, body := range map[string]string{
		"mid-path **": `{"source_path":"/a/**/b","target_url":"/x"}`,
		"catch-all":   `{"source_path":"/**","target_url":"/maintenance"}`,
		"self-loop":   `{"source_path":"/docs/*","target_url":"/docs/$1"}`,
	} {
		if code := do(http.MethodPost, "/", body); code != http.StatusBadRequest {
			t.Errorf("create %s: status %d, want 400", name, code)
		}
	}
	for name, body := range map[string]string{
		"bad source":  `{"source_path":"/a/x*"}`,
		"loop target": `{"target_url":"/docs/archive"}`,
	} {
		if code := do(http.MethodPut, "/"+existing.ID.String(), body); code != http.StatusBadRequest {
			t.Errorf("update %s: status %d, want 400", name, code)
		}
	}
}
