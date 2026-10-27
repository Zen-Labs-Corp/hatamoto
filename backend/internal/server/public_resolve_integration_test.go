//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// Exercises resolveRouteForRender (the renderer's path resolution) end to end against a migrated database with
// neutral fixtures: DATABASE_URL=... go test -tags=integration ./internal/server -run Resolve
func TestResolveRouteForRender_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
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

	suffix := uuid.NewString()[:6]
	pageSlug, docSlug := "it-page-"+suffix, "it-doc-"+suffix
	pageID, docID := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{}')`, pageID, pageSlug)
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		docID, docSlug, `{"url_pattern":"/guides/{slug}","archive_path":"/guides","aliases":["it-legacy-`+suffix+`"],"overrides":[{"attribute":"format","equals":"report","url_pattern":"/reports/{slug}"}]}`)
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = ANY($1)`, []uuid.UUID{pageID, docID})
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = ANY($1)`, []uuid.UUID{pageID, docID})
	})
	ins := func(schema uuid.UUID, slug, variant, attrs string) {
		var vk any
		if variant != "" {
			vk = variant
		}
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,variant_key,attributes)
			VALUES ($1,$2,$3,$3,'published',now(),$4,$5::jsonb)`, uuid.New(), schema, slug, vk, attrs)
	}
	ins(pageID, "hello", "", `{}`)
	ins(pageID, "hello", "variant-1", `{}`) // plugin variant sharing the slug
	ins(docID, "manual", "", `{}`)
	ins(docID, "annual", "", `{"format":"report"}`)

	entitySvc := entity.NewService(entity.NewPostgresSchemaRepository(db), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := &Server{db: db, entityService: entitySvc, siteConfig: siteconfig.NewStore(db, slog.Default()), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// get resolves a path like the renderer does. status is 200 for a page or
	// archive, the loader's status for a miss or error.
	get := func(path string) (int, *routeResult) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		res, outcome, err := srv.resolveRouteForRender(ctx, path, req)
		switch {
		case err != nil:
			return outcome.Status, nil
		case res.NotFound:
			return http.StatusNotFound, res
		}
		return http.StatusOK, res
	}

	code, res := get("/hello")
	if code != 200 || res.CanonicalURL != "/hello" {
		t.Fatalf("/hello = %d %+v", code, res)
	}
	if code, _ := get("/xx/hello"); code != 404 {
		t.Fatalf("an unknown prefix is not routed: got %d", code)
	}

	code, res = get("/guides/manual")
	if code != 200 || res.CanonicalURL != "/guides/manual" {
		t.Fatalf("/guides/manual = %d %+v", code, res)
	}
	if code, res = get("/reports/annual"); code != 200 || res.CanonicalURL != "/reports/annual" {
		t.Fatalf("/reports/annual = %d %+v", code, res)
	}
	if code, _ := get("/guides/annual"); code != 404 {
		t.Fatalf("override entity at base url: got %d", code)
	}
	if code, res := get("/guides"); code != 200 || res.Kind != resolveKindArchive || res.Archive.SchemaSlug != docSlug {
		t.Fatalf("archive /guides = %d %+v", code, res)
	}
	if code, _ := get("relative"); code != 400 {
		t.Fatalf("bad path: got %d", code)
	}
}
