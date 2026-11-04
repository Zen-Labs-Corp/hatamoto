package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
)

// takeOverRepo is the slice of the entity repository an update uses; the
// embedded nil interface panics if an update reaches for anything else.
type takeOverRepo struct {
	entity.EntityRepository
	took bool
}

func (r takeOverRepo) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	return &entity.Entity{ID: id, Name: "E", Slug: "e", Attributes: []byte(`{}`), Schema: &entity.EntitySchema{Slug: "page", Schema: []byte(`{}`)}}, nil
}

func (r takeOverRepo) Update(_ context.Context, id uuid.UUID, _ *uuid.UUID, _ *entity.UpdateEntityInput) (*entity.Entity, error) {
	return &entity.Entity{ID: id, Name: "E", Slug: "e", TookOwnership: r.took}, nil
}

// A REST update that takes a plugin's entity over is logged: this API has no
// audit table.
func TestUpdateEntityLogsTakeOver(t *testing.T) {
	for _, took := range []bool{true, false} {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		s := &Server{logger: logger, entityService: entity.NewService(nil, takeOverRepo{took: took}, logger)}
		id := uuid.New()
		req := httptest.NewRequest("PUT", "/api/v1/entities/"+id.String(), strings.NewReader(`{"slug":"moved"}`))
		req.SetPathValue("id", id.String())
		req = req.WithContext(middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: uuid.New()}))
		rec := httptest.NewRecorder()
		s.handleUpdateEntity(rec, req)
		if rec.Code != 200 {
			t.Fatalf("update: %d %s", rec.Code, rec.Body)
		}
		if got := strings.Contains(buf.String(), "entity taken over from its plugin"); got != took {
			t.Errorf("took = %v, log:\n%s", took, buf.String())
		}
	}
}
