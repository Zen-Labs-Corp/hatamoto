package server

import (
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

// paramRepo records the pagination a list request forwards; the embedded nil
// interface panics on any call this slice does not fake.
type paramRepo struct {
	entity.EntityRepository
	keyset *entity.KeysetPage
	offset *entity.OffsetPage
}

func (r *paramRepo) List(_ context.Context, _ *entity.ListEntitiesFilter, page *entity.KeysetPage) (*entity.PagedResult[*entity.Entity], error) {
	r.keyset = page
	return &entity.PagedResult[*entity.Entity]{}, nil
}

func (r *paramRepo) ListWithOffset(_ context.Context, _ *entity.ListEntitiesFilter, page *entity.OffsetPage) (*entity.OffsetPagedResult[*entity.Entity], error) {
	r.offset = page
	return &entity.OffsetPagedResult[*entity.Entity]{}, nil
}

func newParamServer(repo entity.EntityRepository) *Server {
	logger := slog.Default()
	return &Server{logger: logger, entityService: entity.NewService(nil, repo, logger)}
}

// GET /entities refuses a limit it cannot honor; the previous behaviour was to
// fall back to the default of 20 with no signal to the caller.
func TestListEntitiesRejectsBadLimit(t *testing.T) {
	for _, q := range []string{"limit=abc", "limit=0", "limit=-3"} {
		s := newParamServer(&paramRepo{})
		req := httptest.NewRequest("GET", "/api/v1/entities?"+q, nil)
		rec := httptest.NewRecorder()
		s.handleListEntities(rec, req)
		if rec.Code != 400 {
			t.Errorf("GET ?%s: %d %s, want 400", q, rec.Code, rec.Body)
		}
	}
}

// The same rejection applies on the offset-paginated path.
func TestListEntitiesRejectsBadLimitWithPage(t *testing.T) {
	s := newParamServer(&paramRepo{})
	req := httptest.NewRequest("GET", "/api/v1/entities?page=1&limit=abc", nil)
	rec := httptest.NewRecorder()
	s.handleListEntities(rec, req)
	if rec.Code != 400 {
		t.Fatalf("GET ?page=1&limit=abc: %d %s, want 400", rec.Code, rec.Body)
	}
}

// per_page is answered with a 400 naming limit instead of being ignored.
func TestListEntitiesRejectsPerPage(t *testing.T) {
	s := newParamServer(&paramRepo{})
	req := httptest.NewRequest("GET", "/api/v1/entities?per_page=10", nil)
	rec := httptest.NewRecorder()
	s.handleListEntities(rec, req)
	if rec.Code != 400 {
		t.Fatalf("GET ?per_page=10: %d %s, want 400", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "limit") {
		t.Errorf("answer does not name limit: %s", rec.Body)
	}
}

// A limit over the 100 cap clamps to it instead of resetting to the default.
func TestListEntitiesClampsBigLimit(t *testing.T) {
	repo := &paramRepo{}
	s := newParamServer(repo)

	req := httptest.NewRequest("GET", "/api/v1/entities?limit=500", nil)
	rec := httptest.NewRecorder()
	s.handleListEntities(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET ?limit=500: %d %s", rec.Code, rec.Body)
	}
	if repo.keyset == nil || repo.keyset.Limit != 100 {
		t.Errorf("keyset limit = %+v, want 100", repo.keyset)
	}

	req = httptest.NewRequest("GET", "/api/v1/entities?page=1&limit=500", nil)
	rec = httptest.NewRecorder()
	s.handleListEntities(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET ?page=1&limit=500: %d %s", rec.Code, rec.Body)
	}
	if repo.offset == nil || repo.offset.Limit != 100 {
		t.Errorf("offset limit = %+v, want 100", repo.offset)
	}
}

// An in-range limit still applies on both pagination paths.
func TestListEntitiesKeepsGoodLimit(t *testing.T) {
	repo := &paramRepo{}
	s := newParamServer(repo)

	req := httptest.NewRequest("GET", "/api/v1/entities?limit=5", nil)
	rec := httptest.NewRecorder()
	s.handleListEntities(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET ?limit=5: %d %s", rec.Code, rec.Body)
	}
	if repo.keyset == nil || repo.keyset.Limit != 5 {
		t.Errorf("keyset limit = %+v, want 5", repo.keyset)
	}
}

// A status member on create is refused: status only moves through
// PATCH /entities/{id}/status.
func TestCreateEntityRejectsStatusField(t *testing.T) {
	s := newParamServer(&paramRepo{})
	body := `{"schema_id":"` + uuid.NewString() + `","name":"x","attributes":{},"status":"published"}`
	req := httptest.NewRequest("POST", "/api/v1/entities", strings.NewReader(body))
	req = req.WithContext(middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: uuid.New()}))
	rec := httptest.NewRecorder()
	s.handleCreateEntity(rec, req)
	if rec.Code != 400 {
		t.Fatalf("create with status: %d %s, want 400", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "/status") {
		t.Errorf("answer does not name the status endpoint: %s", rec.Body)
	}
}

func TestUpdateEntityRejectsStatusField(t *testing.T) {
	s := newParamServer(&paramRepo{})
	id := uuid.New()
	req := httptest.NewRequest("PATCH", "/api/v1/entities/"+id.String(), strings.NewReader(`{"name":"x","status":"published"}`))
	req.SetPathValue("id", id.String())
	req = req.WithContext(middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: uuid.New()}))
	rec := httptest.NewRecorder()
	s.handleUpdateEntity(rec, req)
	if rec.Code != 400 {
		t.Fatalf("update with status: %d %s, want 400", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "/status") {
		t.Errorf("answer does not name the status endpoint: %s", rec.Body)
	}
}
