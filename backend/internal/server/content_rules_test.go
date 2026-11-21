package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/middleware"
)

// rulesEntityRepo serves the slice of the entity repository the content
// writes use, over a fixed set of entities.
type rulesEntityRepo struct {
	entity.EntityRepository
	mu   sync.Mutex
	ents map[uuid.UUID]*entity.Entity
}

func (r *rulesEntityRepo) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.ents[id]
	if !ok {
		return nil, entity.ErrEntityNotFound
	}
	cp := *e
	return &cp, nil
}

func (r *rulesEntityRepo) Update(_ context.Context, id uuid.UUID, _ *uuid.UUID, _ *entity.UpdateEntityInput) (*entity.Entity, error) {
	return &entity.Entity{ID: id, Name: "E", Slug: "e"}, nil
}

func (r *rulesEntityRepo) UpdateStatus(_ context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	return &entity.Entity{ID: id, Name: "E", Slug: "e", Status: st}, nil
}

func (r *rulesEntityRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }

// rulesMediaRepo serves the slice of the media repository update and delete use.
type rulesMediaRepo struct {
	media.Repository
	items map[uuid.UUID]*media.Media
}

func (r rulesMediaRepo) GetByID(_ context.Context, id uuid.UUID) (*media.Media, error) {
	m, ok := r.items[id]
	if !ok {
		return nil, media.ErrMediaNotFound
	}
	cp := *m
	return &cp, nil
}
func (r rulesMediaRepo) Update(context.Context, *media.Media) error { return nil }
func (r rulesMediaRepo) Delete(context.Context, uuid.UUID) error    { return nil }

// rulesSchemaRepo serves one schema by id.
type rulesSchemaRepo struct {
	entity.SchemaRepository
	schema *entity.EntitySchema
}

func (r rulesSchemaRepo) GetByID(context.Context, uuid.UUID) (*entity.EntitySchema, error) {
	return r.schema, nil
}

type rulesWorld struct {
	s                      *Server
	mine, theirs, orphan   uuid.UUID
	me                     uuid.UUID
	mediaMine, mediaTheirs uuid.UUID
}

func newRulesWorld(t *testing.T) *rulesWorld {
	t.Helper()
	w := &rulesWorld{me: uuid.New(), mine: uuid.New(), theirs: uuid.New(), orphan: uuid.New(), mediaMine: uuid.New(), mediaTheirs: uuid.New()}
	other := uuid.New()
	schema := &entity.EntitySchema{Slug: "page", Schema: []byte(`{}`)}
	mk := func(id uuid.UUID, by *uuid.UUID, st entity.EntityStatus) *entity.Entity {
		return &entity.Entity{ID: id, Name: "E", Slug: "e", Status: st, CreatedBy: by, Attributes: []byte(`{}`), Schema: schema}
	}
	repo := &rulesEntityRepo{ents: map[uuid.UUID]*entity.Entity{
		w.mine:   mk(w.mine, &w.me, entity.StatusDraft),
		w.theirs: mk(w.theirs, &other, entity.StatusDraft),
		w.orphan: mk(w.orphan, nil, entity.StatusDraft),
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mrepo := rulesMediaRepo{items: map[uuid.UUID]*media.Media{
		w.mediaMine:   {ID: w.mediaMine, UploadedBy: &w.me},
		w.mediaTheirs: {ID: w.mediaTheirs, UploadedBy: &other},
	}}
	w.s = &Server{
		logger:        logger,
		entityService: entity.NewService(rulesSchemaRepo{schema: schema}, repo, logger),
		mediaService:  media.NewService(mrepo, media.Config{UploadDir: t.TempDir()}, logger),
	}
	return w
}

func (w *rulesWorld) call(h http.HandlerFunc, role, method, body string, id uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	req.SetPathValue("id", id.String())
	req = req.WithContext(middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: w.me, Role: role}))
	rec := httptest.NewRecorder()
	middleware.AttachSubject(h).ServeHTTP(rec, req)
	return rec
}

func wantCode(t *testing.T, what string, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("%s = %d %s, want %d", what, rec.Code, strings.TrimSpace(rec.Body.String()), want)
	}
}

func TestRESTEntityObjectRules(t *testing.T) {
	w := newRulesWorld(t)
	upd, status, del := w.s.handleUpdateEntity, w.s.handleUpdateEntityStatus, w.s.handleDeleteEntity

	wantCode(t, "author PATCH other's", w.call(upd, "author", "PATCH", `{"name":"N"}`, w.theirs), 403)
	wantCode(t, "author PATCH own", w.call(upd, "author", "PATCH", `{"name":"N"}`, w.mine), 200)
	wantCode(t, "author DELETE other's", w.call(del, "author", "DELETE", ``, w.theirs), 403)
	wantCode(t, "author DELETE own", w.call(del, "author", "DELETE", ``, w.mine), 200)

	for _, st := range []string{"published", "scheduled"} {
		rec := w.call(status, "contributor", "PATCH", `{"status":"`+st+`"}`, w.mine)
		wantCode(t, "contributor publish own ("+st+")", rec, 403)
		if !strings.Contains(rec.Body.String(), "you cannot publish this entry") {
			t.Errorf("refusal body = %s, want the publish sentence", rec.Body)
		}
	}
	wantCode(t, "reviewer publish other's", w.call(status, "reviewer", "PATCH", `{"status":"published"}`, w.theirs), 200)
	wantCode(t, "reviewer publish unowned", w.call(status, "reviewer", "PATCH", `{"status":"published"}`, w.orphan), 200)
	wantCode(t, "reviewer create", w.call(w.s.handleCreateEntity, "reviewer", "POST", `{"schema_id":"`+uuid.NewString()+`","name":"N"}`, w.mine), 403)
}

func TestRESTEntityReorderNeedsTheReorderAction(t *testing.T) {
	w := newRulesWorld(t)
	rec := w.call(w.s.handleUpdateEntity, "author", "PATCH", `{"position":1}`, w.mine)
	wantCode(t, "author reorder own", rec, 403)
}

func TestRESTEntityKeyObjectRules(t *testing.T) {
	w := newRulesWorld(t)
	key := &auth.APIKey{ID: uuid.New(), CreatedBy: &w.me, CreatorRole: "author", Scopes: []auth.APIKeyScope{auth.ScopeAll}}
	call := func(id uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/x", strings.NewReader(`{"name":"N"}`))
		req.SetPathValue("id", id.String())
		ctx := middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: w.me, Role: "editor"})
		ctx = authzctx.With(ctx, authzsubject.ForKey(key))
		rec := httptest.NewRecorder()
		w.s.handleUpdateEntity(rec, req.WithContext(ctx))
		return rec
	}
	wantCode(t, "author key PATCH other's", call(w.theirs), 403)
	wantCode(t, "author key PATCH own", call(w.mine), 200)
}

func TestRESTMediaObjectRules(t *testing.T) {
	w := newRulesWorld(t)
	upd, del := w.s.handleMediaUpdate, w.s.handleMediaDelete

	wantCode(t, "author PUT other's media", w.call(upd, "author", "PUT", `{}`, w.mediaTheirs), 403)
	wantCode(t, "author PUT own media", w.call(upd, "author", "PUT", `{}`, w.mediaMine), 200)
	wantCode(t, "author DELETE other's media", w.call(del, "author", "DELETE", ``, w.mediaTheirs), 403)
	wantCode(t, "author DELETE own media", w.call(del, "author", "DELETE", ``, w.mediaMine), 200)
	wantCode(t, "author PUT missing media", w.call(upd, "author", "PUT", `{}`, uuid.New()), 404)
	wantCode(t, "editor DELETE other's media", w.call(del, "editor", "DELETE", ``, w.mediaTheirs), 200)
}
