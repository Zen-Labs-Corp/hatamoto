package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authzctx"
)

func subjectSeenBy(ctx context.Context) (authz.Subject, bool) {
	var s authz.Subject
	var ok bool
	h := AttachSubject(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		s, ok = authzctx.From(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	return s, ok
}

func TestAttachSubjectSession(t *testing.T) {
	id := uuid.New()
	ctx := ContextWithClaims(context.Background(), &auth.Claims{UserID: id, Role: "author"})
	s, ok := subjectSeenBy(ctx)
	if !ok || s.UserID != id.String() || s.Role != "author" || s.KeyID != "" {
		t.Fatalf("subject = %+v ok=%v, want the author session", s, ok)
	}
}

func TestAttachSubjectKey(t *testing.T) {
	creator := uuid.New()
	key := &auth.APIKey{ID: uuid.New(), CreatedBy: &creator, CreatorRole: "author", Scopes: []auth.APIKeyScope{auth.ScopeAll}}
	ctx := context.WithValue(claimsCtx("editor"), apiKeyContextKey, key)
	ctx = context.WithValue(ctx, authTypeContextKey, AuthTypeAPIKey)
	s, ok := subjectSeenBy(ctx)
	if !ok || s.KeyID == "" {
		t.Fatalf("subject = %+v ok=%v, want a key subject", s, ok)
	}
}

func TestAttachSubjectNoClaims(t *testing.T) {
	if s, ok := subjectSeenBy(context.Background()); ok {
		t.Fatalf("subject = %+v, want none", s)
	}
}
