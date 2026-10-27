//go:build integration

package server

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/migrations"
)

// A database seeded like hatamoto.dev before v1-09 (ten two-letter path
// prefixes, a page that lived under one of them, the manual cutover redirect)
// is migrated and served: every old /xx/... URL must 301 to its new path and
// the new paths must answer 200.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RetiredPrefix
func TestRetiredPrefixRedirects_ProdSnapshot_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	dbName := "hm_prefix_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)") })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + dbName
	scratchDSN := u.String()

	sqlDB, err := sql.Open("pgx", scratchDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.UpTo(ctx, 202610151229); err != nil {
		t.Fatalf("up to the previous migration: %v", err)
	}
	seed, err := os.ReadFile("../../migrations/testdata/prodlike_pre_v1_09.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, string(seed)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "prod-like")
	t.Setenv("HATAMOTO_SITE_NAME", "Prodlike")
	t.Setenv("PUBLIC_BASE_URL", "https://prodlike.example.test")
	t.Setenv("DATABASE_URL", scratchDSN)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	for from, to := range map[string]string{
		"/au/platform":   "/platform",
		"/au/platform/":  "/platform",
		"/au":            "/",
		"/us/post/hello": "/post/hello",
		"/de/platform":   "/platform",
		"/ca":            "/",
	} {
		if rec := get(from); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != to {
			t.Errorf("%s = %d -> %q, want 301 -> %s", from, rec.Code, rec.Header().Get("Location"), to)
		}
	}

	// The owner's /platform -> / redirect is kept: /au/platform takes two hops.
	if rec := get("/platform"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/" {
		t.Errorf("/platform = %d -> %q, want the owner redirect to /", rec.Code, rec.Header().Get("Location"))
	}

	page := get("/post/hello")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Hi") {
		t.Fatalf("/post/hello = %d", page.Code)
	}
	for _, want := range []string{
		`<html lang="en-AU"`, `"inLanguage":"en-AU"`, `"telephone":"+61 2 5550 0100"`, `"addressCountry":"AU"`,
		`rel="canonical" href="https://prodlike.example.test/post/hello"`,
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("/post/hello lacks %s", want)
		}
	}
	if strings.Contains(page.Body.String(), "hreflang") {
		t.Error("/post/hello still emits alternates")
	}
	for path, want := range map[string]int{
		"/post/hello":     http.StatusOK,
		"/":               http.StatusOK,
		"/xx/platform":    http.StatusNotFound, // not a retired prefix
		"/choose-region":  http.StatusNotFound,
		"/sitemap-au.xml": http.StatusNotFound,
	} {
		if rec := get(path); rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
	}

	// The built-in WordPress redirects only answer once the site opts in.
	if rec := get("/2020/05/old-post"); rec.Code != http.StatusNotFound {
		t.Errorf("legacy WordPress URL with the setting off = %d, want 404", rec.Code)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE hatamoto_site_config SET legacy_wp_redirects = true`); err != nil {
		t.Fatal(err)
	}
	srv.siteConfig.Invalidate()
	if rec := get("/2020/05/old-post"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/post/old-post" {
		t.Errorf("legacy WordPress URL with the setting on = %d -> %q", rec.Code, rec.Header().Get("Location"))
	}

	// Open-redirect payloads never produce an external Location.
	for _, p := range []string{"/au/%5Cevil.com", "/au/%09/evil.com", "/au/%2F%2Fevil.com", "/au//evil.com", "/us/%5C%5Cevil.com", "/2020/05/%5Cevil.com"} {
		rec := get(p)
		if loc := rec.Header().Get("Location"); loc != "" && (strings.HasPrefix(loc, "//") || strings.ContainsAny(loc, `\`+"\t\r\n")) {
			t.Errorf("%s -> %d Location %q", p, rec.Code, loc)
		}
	}

	index := get("/sitemap.xml").Body.String()
	for _, want := range []string{"/sitemap-post.xml", "/sitemap-misc.xml"} {
		if !strings.Contains(index, want) {
			t.Errorf("sitemap index lacks %s:\n%s", want, index)
		}
	}
	// /platform redirects to / (owner redirect, checked above), so the sitemap
	// must not list it: redirected paths are dropped (37311b73).
	if misc := get("/sitemap-misc.xml").Body.String(); strings.Contains(misc, "prodlike.example.test/platform<") {
		t.Errorf("misc sitemap lists the redirected /platform:\n%s", misc)
	}
}
