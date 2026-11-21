package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
)

func TestEntAuditActorAPIKey(t *testing.T) {
	t.Parallel()
	creator, keyID := uuid.New(), uuid.New()
	a, ok := fillRestActor(auditctx.Actor{Via: "api"}, &auth.APIKey{ID: keyID, Name: "ci", CreatedBy: &creator}, nil)
	if !ok || a.Kind != "api_key" || a.KeyID != keyID.String() || a.Name != "api-key:ci" || a.ID != creator.String() || a.Via != "api" {
		t.Fatalf("actor = %+v, %v", a, ok)
	}
	if a, _ := fillRestActor(auditctx.Actor{}, &auth.APIKey{ID: keyID, Name: "ci"}, nil); a.ID != "" {
		t.Fatalf("creator-less key got actor id %q", a.ID)
	}
}

func TestEntAuditActorSessionAndNone(t *testing.T) {
	t.Parallel()
	uid := uuid.New()
	r := httptest.NewRequest("POST", "/api/v1/entities", nil)
	r.Header.Set("X-Request-ID", "req-1")
	if _, ok := restAuditActor(r); ok {
		t.Fatal("anonymous request has an actor")
	}
	r = r.WithContext(middleware.ContextWithClaims(r.Context(), &auth.Claims{UserID: uid, Email: "a@example.com"}))
	a, ok := restAuditActor(r)
	if !ok || a.Kind != "user" || a.ID != uid.String() || a.Name != "a@example.com" || a.Via != "api" || a.RequestID != "req-1" {
		t.Fatalf("actor = %+v, %v", a, ok)
	}
}

func TestEntAuditActorMiddleware(t *testing.T) {
	t.Parallel()
	s := &Server{logger: quietLogger()}
	var got auditctx.Actor
	var found bool
	h := s.auditActorMiddleware(entAuditActorHandler(func(ctx context.Context) { got, found = auditctx.From(ctx) }))
	r := httptest.NewRequest("POST", "/x", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if found {
		t.Fatal("anonymous request got an actor")
	}
	r = r.WithContext(middleware.ContextWithClaims(r.Context(), &auth.Claims{UserID: uuid.New(), Email: "a@example.com"}))
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !found || got.Kind != "user" {
		t.Fatalf("actor = %+v, %v", got, found)
	}
}

func TestEntAuditActorFromCtxWithoutActorWritesNothing(t *testing.T) {
	t.Parallel()
	s := &Server{logger: quietLogger()} // db nil: a write would panic
	s.auditFromCtx(context.Background(), "term.create", "t", map[string]any{"name": "n"})
	s.auditFromCtx(auditctx.With(context.Background(), auditctx.Actor{Kind: "user"}), "term.create", "t", nil)
}

type entAuditActorHandler func(ctx context.Context)

func (f entAuditActorHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) { f(r.Context()) }
