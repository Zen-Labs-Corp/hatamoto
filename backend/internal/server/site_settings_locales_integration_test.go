//go:build integration

package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/siteconfig"
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
	for _, src := range []string{"/fr/old", "/qq/**", "/rr", "/zz/old"} {
		exec(`INSERT INTO hatamoto_redirects (source_path, target_url) VALUES ($1, '/elsewhere')`, src)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_redirects WHERE source_path IN ('/fr/old','/qq/**','/rr','/zz/old')`)
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
	if err := srv.checkLocaleSegments(ctx, []string{"fr", "rr"}); err == nil || !strings.Contains(err.Error(), "redirect rule /rr") {
		t.Errorf("new segment claimed by a redirect: err = %v", err)
	}
	if err := srv.checkLocaleSegments(ctx, []string{"fr", "qq"}); err == nil || !strings.Contains(err.Error(), "redirect rule /qq/**") {
		t.Errorf("new segment claimed by a wildcard redirect: err = %v", err)
	}
}

// localeCheckServer is a server on the test database for the segment checks.
func localeCheckServer(t *testing.T) (*Server, context.Context) {
	t.Helper()
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
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.siteConfig.Invalidate()
	return srv, ctx
}

// A database failure during the check is a localeCheckInfraError, which the
// validator turns into a message that does not echo the driver text.
func TestLocaleSegmentChecks_InfraError_Integration(t *testing.T) {
	srv, ctx := localeCheckServer(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	err := srv.checkLocaleSegments(cancelled, []string{"yy"})
	var infra *localeCheckInfraError
	if !errors.As(err, &infra) {
		t.Fatalf("cancelled check: err = %v, want *localeCheckInfraError", err)
	}
	_, _, err = validateLocaleSettings(cancelled, localeSettingsRequest{
		Default: "en", Entries: []siteconfig.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "yy"}},
	}, srv.checkLocaleSegments)
	var le *localeSettingsError
	if !errors.As(err, &le) || le.Field != "locales" {
		t.Fatalf("validate: err = %v, want a locales field error", err)
	}
	if strings.Contains(le.Message, "context canceled") || !strings.Contains(le.Message, "try again") {
		t.Errorf("message = %q", le.Message)
	}
}

// A segment taken by a published top-level page is refused with the page's
// name, slug and id and a pointer to the conversion; a draft page, a child
// page and a term kind are told apart.
func TestLocaleSegmentChecks_NamesPage_Integration(t *testing.T) {
	srv, ctx := localeCheckServer(t)
	pool := srv.db.Pool()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var siteRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_site_config`).Scan(&siteRows); err != nil {
		t.Fatal(err)
	}
	if siteRows == 0 {
		exec(`INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Segment IT', 'en')`)
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM hatamoto_site_config`) })
	}
	srv.siteConfig.Invalidate()
	var schemaID, pubID, parentID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	insert := func(name, slug, status string, parent any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id,name,slug,status,parent_id,attributes)
			VALUES ($1,$2,$3,$4,$5,'{}'::jsonb) RETURNING id::text`, schemaID, name, slug, status, parent).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pubID = insert("Yy Page", "yy", "published", nil)
	insert("Draft Page", "yd", "draft", nil)
	parentID = insert("Parent", "yp", "published", nil)
	insert("Child", "yc", "published", parentID)
	exec(`INSERT INTO hatamoto_terms (kind,slug,name) VALUES ('yk','a','A')`)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM hatamoto_entities WHERE slug IN ('yy','yd','yc') OR id = $1::uuid`, parentID)
		_, _ = pool.Exec(c, `DELETE FROM hatamoto_terms WHERE kind = 'yk'`)
	})

	tests := []struct {
		seg  string
		want []string // substrings of the error; nil = accepted
	}{
		{"yy", []string{`segment "yy"`, `published page "Yy Page"`, "(/yy, id " + pubID + ")", "Pick another segment, or move or unpublish that page first, or convert region pages to locales in Settings > General > Convert existing region pages"}},
		{"yk", []string{`segment "yk" is already used as a term kind`}},
		{"yd", nil},
		{"yc", nil},
	}
	for _, tt := range tests {
		err := srv.checkLocaleSegments(ctx, []string{tt.seg})
		if tt.want == nil {
			if err != nil {
				t.Errorf("%s: err = %v, want accepted", tt.seg, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted", tt.seg)
			continue
		}
		for _, w := range tt.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q lacks %q", tt.seg, err, w)
			}
		}
		if strings.ContainsAny(err.Error(), "—–") {
			t.Errorf("%s: message has a dash: %q", tt.seg, err)
		}
	}
}

// A stored index or misc segment is live in group naming and must not block
// saves; a new one is refused.
func TestLocaleSegmentChecks_StoredFileNameSegment_Integration(t *testing.T) {
	srv, ctx := localeCheckServer(t)
	var prev []byte
	hadSite := srv.db.Pool().QueryRow(ctx, `SELECT locales FROM hatamoto_site_config`).Scan(&prev) == nil
	if _, err := srv.db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, language, locales) VALUES ('Segment IT', 'en', $1::jsonb)
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales`, `[{"tag":"en","segment":""},{"tag":"fr","segment":"misc"}]`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadSite {
			_, _ = srv.db.Pool().Exec(context.Background(), `UPDATE hatamoto_site_config SET locales=$1::jsonb`, string(prev))
		} else {
			_, _ = srv.db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_site_config`)
		}
	})
	srv.siteConfig.Invalidate()
	if err := srv.checkLocaleSegments(ctx, []string{"misc"}); err != nil {
		t.Errorf("stored misc segment rejected: %v", err)
	}
	if err := srv.checkLocaleSegments(ctx, []string{"misc", "index"}); err == nil {
		t.Error("new index segment accepted")
	}
}
