//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

// An ended user's admin cookie no longer counts as signed in for the site
// access gate or the admin address intercept, on the very next request and
// without the worker having run.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run EndedAdminCookie
func TestEndedAdminCookieIsNotSignedIn(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	users := user.NewPostgresRepository(db)
	sessions := session.NewStore(db)
	sctx := database.WithSuperadmin(ctx)
	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	u, err := users.Create(sctx, &user.CreateUserInput{Email: "ended-" + uuid.NewString()[:8] + "@example.test", Role: user.RoleEditor, Name: "E"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = users.Delete(sctx, u.ID) })
	token, _, err := sessions.IssueAdmin(ctx, u.ID, time.Hour, "ua", "")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		db: db, userRepo: users, sessionStore: sessions,
		authService: auth.NewService(users, nil, sessions),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	deps := s.newInterceptDeps()
	req := httptest.NewRequest(http.MethodGet, "/about", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})

	for name, signedIn := range map[string]func(*http.Request) bool{"site gate": s.siteAccessSignedIn, "admin address": deps.signedIn} {
		if !signedIn(req) {
			t.Fatalf("%s: a live cookie is not signed in", name)
		}
	}
	past := time.Now().Add(-time.Minute)
	if _, err := users.Update(sctx, u.ID, &user.UpdateUserInput{AccessEndsAt: user.AccessEnd{Set: true, At: &past}}); err != nil {
		t.Fatal(err)
	}
	for name, signedIn := range map[string]func(*http.Request) bool{"site gate": s.siteAccessSignedIn, "admin address": deps.signedIn} {
		if signedIn(req) {
			t.Errorf("%s: an ended user's cookie is still signed in", name)
		}
	}
}
