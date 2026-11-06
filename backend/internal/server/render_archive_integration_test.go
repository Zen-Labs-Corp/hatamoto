//go:build integration

package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// archiveITServer boots a server on DATABASE_URL with one schema (settings as
// given, %s = the archive path) and the given numeric "sort" values published
// as entities slugged e<sort>. It returns a GET helper and the archive path.
func archiveITServer(t *testing.T, settings string, sorts ...int) (get func(string) (int, string, http.Header), base string) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping archive integration test (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "archive-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Archive IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "Archive IT")
	t.Setenv("PUBLIC_BASE_URL", "https://archive-it.example.test")
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
	sfx := uuid.NewString()[:6]
	base = "/ait-" + sfx
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Archive Items',$2,'{"type":"object"}',$3)`,
		schemaID, "ait-"+sfx, fmt.Sprintf(settings, base, base))
	for _, n := range sorts {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			VALUES ($1,$2,$3,$4,'published',now(),$5::jsonb)`,
			uuid.New(), schemaID, fmt.Sprintf("Item %d", n), fmt.Sprintf("e%d", n), fmt.Sprintf(`{"sort":%d,"label":"L%d"}`, n, n))
	}
	var hadSite bool
	_ = db.Pool().QueryRow(ctx, `SELECT true FROM hatamoto_site_config`).Scan(&hadSite)
	if !hadSite {
		exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Archive IT')`)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
	})
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return func(path string) (int, string, http.Header) {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String(), rec.Header()
	}, base
}

const archiveITGroupSettings = `{"url_pattern":"%s/{slug}","archive_path":"%s","archive_title":"Items","archive_limit":2,` +
	`"archive_groups":{"attribute":"sort","divisor":1000,"path":"v{n}","label":"v{n}.x","title":"{archive_title} {label}","anchor_attribute":"label","redirect_entries":true}}`

// The archive page is capped and unpaginated; a group page lists its bucket;
// an empty bucket is a 404.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run ArchiveCapped
func TestArchiveCappedIntegration(t *testing.T) {
	get, base := archiveITServer(t, archiveITGroupSettings, 1001, 1002, 1003, 2001, 5)
	cards := func(body string) int { return strings.Count(body, `class="hm-card"`) }

	code, body, _ := get(base)
	if code != 200 || cards(body) != 2 {
		t.Fatalf("archive page = %d with %d cards, want 200 with 2\n%s", code, cards(body), body)
	}
	if !strings.Contains(body, "Item 2001") || !strings.Contains(body, "Item 1003") || strings.Contains(body, "Item 1002") {
		t.Errorf("archive page must list the 2 newest by the group attribute\n%s", body)
	}
	for _, bad := range []string{`rel="next"`, `rel="prev"`, `aria-current="page"`} {
		if strings.Contains(body, bad) {
			t.Errorf("archive page must not paginate: found %s", bad)
		}
	}
	if want := `<link rel="canonical" href="https://archive-it.example.test` + base + `">`; !strings.Contains(body, want) {
		t.Errorf("archive page: missing %s", want)
	}

	code, body, _ = get(base + "/v1")
	if code != 200 || cards(body) != 3 || strings.Contains(body, "Item 2001") || strings.Contains(body, "Item 5<") {
		t.Fatalf("group v1 = %d with %d cards, want 200 with 3\n%s", code, cards(body), body)
	}
	if want := `<link rel="canonical" href="https://archive-it.example.test` + base + `/v1">`; !strings.Contains(body, want) {
		t.Errorf("group page: missing %s", want)
	}
	if strings.Contains(body, `rel="next"`) {
		t.Error("group page must not paginate")
	}
	if code, _, _ = get(base + "/v0"); code != 200 {
		t.Errorf("group v0 (one entry) = %d, want 200", code)
	}
	if code, _, _ = get(base + "/v7"); code != 404 {
		t.Errorf("empty group v7 = %d, want 404", code)
	}
}
