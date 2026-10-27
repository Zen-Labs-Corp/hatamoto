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
)

// The real handler chain retires a legacy session: if expireLegacyCookies is
// dropped from buildHandler, this fails.
func TestLegacyCookieExpiryInRealChain(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("l", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "legacy-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Legacy IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Legacy IT")
	t.Setenv("PUBLIC_BASE_URL", "https://legacy-it.example.test")
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	userID := uuid.New()
	hash, err := auth.HashPassword("correct horse battery staple 9")
	if err != nil {
		t.Fatal(err)
	}
	email := "legacy-" + uuid.NewString()[:6] + "@example.test"
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,$3,'owner')`, userID, email, hash); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_sessions WHERE user_id=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	useNeutralNames(t)
	token, sess, err := srv.sessionStore.IssueAdmin(ctx, userID, time.Hour, "", "")
	if err != nil {
		t.Fatal(err)
	}
	do := func(cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/health", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	sessionRows := func() int {
		var n int
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_sessions WHERE id=$1`, sess.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if rec := do(&http.Cookie{Name: "hatamoto_session", Value: "bogus"}); len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("bogus legacy value got Set-Cookie %q", rec.Header().Values("Set-Cookie"))
	}
	if sessionRows() != 1 {
		t.Fatal("bogus legacy value changed the session table")
	}
	rec := do(&http.Cookie{Name: "hatamoto_session", Value: token})
	var expired bool
	for _, c := range rec.Result().Cookies() {
		expired = expired || (c.Name == "hatamoto_session" && c.MaxAge == -1)
	}
	if !expired {
		t.Fatalf("live legacy session not expired through the real chain: %q", rec.Header().Values("Set-Cookie"))
	}
	if sessionRows() != 0 {
		t.Fatal("live legacy session was not revoked")
	}
}
