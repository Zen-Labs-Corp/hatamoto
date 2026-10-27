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

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/hooks"
)

type stubFormRepo struct{ form.Repository }

func (stubFormRepo) CreateForm(_ context.Context, in *form.CreateFormInput) (*form.Form, error) {
	return &form.Form{ID: uuid.New(), Name: in.Name, Slug: in.Slug}, nil
}
func (stubFormRepo) UpdateForm(_ context.Context, id uuid.UUID, _ *form.UpdateFormInput) (*form.Form, error) {
	return &form.Form{ID: id, Name: "f"}, nil
}
func (stubFormRepo) DeleteForm(context.Context, uuid.UUID) error { return nil }

// Contact-form fields are rendered into pages, so form edits must clear the
// rendered-page cache. These drive the real handlers.
func TestFormEditsClearRenderedPages(t *testing.T) {
	id := uuid.New().String()
	for name, tc := range map[string]struct {
		call func(s *Server, w http.ResponseWriter, r *http.Request)
		body string
	}{
		"create": {func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleCreateForm(w, r) }, `{"name":"Contact"}`},
		"update": {func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleUpdateForm(w, r) }, `{"name":"Contact"}`},
		"delete": {func(s *Server, w http.ResponseWriter, r *http.Request) { s.handleDeleteForm(w, r) }, ``},
	} {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		probe := newRenderProbe(log)
		c := probe.c
		s := &Server{
			logger: log, hooks: hooks.New(log),
			infra:       &Infrastructure{Cache: c, CacheInvalidator: cache.NewCacheInvalidator(c, log)},
			formService: form.NewService(stubFormRepo{}, log),
		}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		tc.call(s, w, req)
		if w.Code >= 300 {
			t.Fatalf("%s: status %d %s", name, w.Code, w.Body)
		}
		if !probe.cleared() {
			t.Errorf("form %s did not clear render:*", name)
		}
	}
}
