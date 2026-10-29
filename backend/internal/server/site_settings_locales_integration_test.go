//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// A redirect added under a stored locale segment does not block later saves,
// while a new segment the site's redirects claim is still rejected.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run LocaleSegmentChecks
func TestLocaleSegmentChecks_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set - skipping locale segment integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "segment-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Segment IT")
	t.Setenv("PUBLIC_BASE_URL", "https://segment-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	var prev struct {
		locales   []byte
		prefixAll bool
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT locales, prefix_all FROM hatamoto_site_config`).Scan(&prev.locales, &prev.prefixAll) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, language, locales) VALUES ('Segment IT', 'en', $1::jsonb)
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales`, `[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"}]`)
	for _, src := range []string{"/fr/old", "/qq", "/zz/old"} {
		exec(`INSERT INTO hatamoto_redirects (source_path, target_url) VALUES ($1, '/elsewhere')`, src)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_redirects WHERE source_path IN ('/fr/old','/qq','/zz/old')`)
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, prefix_all=$2`, string(prev.locales), prev.prefixAll)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
	})

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.siteConfig.Invalidate()

	if err := srv.checkLocaleSegments(ctx, []string{"fr"}); err != nil {
		t.Errorf("stored segment with a redirect under it rejected: %v", err)
	}
	if err := srv.checkLocaleSegments(ctx, []string{"fr", "zz"}); err != nil {
		t.Errorf("new segment with only a deeper redirect rejected: %v", err)
	}
	if err := srv.checkLocaleSegments(ctx, []string{"fr", "qq"}); err == nil || !strings.Contains(err.Error(), "redirect rule /qq") {
		t.Errorf("new segment claimed by a redirect: err = %v", err)
	}
}
