//go:build integration

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scratchPool creates an empty database next to DATABASE_URL's and returns a
// pool on it.
//
//	DATABASE_URL=... go test -tags=integration ./app -run Boot
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_boot_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	return pool
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// An empty database migrates fully, a second run is a no-op, and first boot
// seeds the site config and home page exactly once.
func TestBootEmptyDatabase_Integration(t *testing.T) {
	pool, ctx := scratchPool(t), context.Background()
	for range 2 {
		if err := migrateUp(ctx, pool, quietLogger()); err != nil {
			t.Fatal(err)
		}
		if err := firstBoot(ctx, pool, "Site", quietLogger()); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string) (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM hatamoto_site_config`); n != 1 {
		t.Errorf("site config rows = %d", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entities WHERE slug='home' AND status='published'`); n != 1 {
		t.Errorf("home pages = %d", n)
	}
	if n := count(`SELECT count(*) FROM hatamoto_entity_schemas WHERE slug IN ('page','post')`); n != 2 {
		t.Errorf("default schemas = %d", n)
	}
	if n := count(`SELECT count(*) FROM pg_extension WHERE extname='vector'`); n != 0 {
		t.Errorf("pgvector still installed")
	}
}

// Mixed numbering: a timestamp migration older than one already applied (two
// branches merged out of order) must still apply.
func TestMigrateOutOfOrder_Integration(t *testing.T) {
	pool, ctx := scratchPool(t), context.Background()
	mig := func(table string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(fmt.Sprintf("-- +goose Up\nCREATE TABLE %s (id int);\n-- +goose Down\nDROP TABLE %s;\n", table, table))}
	}
	orig := migrationsFS
	t.Cleanup(func() { migrationsFS = orig })

	migrationsFS = fs.FS(fstest.MapFS{"001_a.sql": mig("a"), "202610150200_c.sql": mig("c")})
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	migrationsFS = fs.FS(fstest.MapFS{"001_a.sql": mig("a"), "202610150100_b.sql": mig("b"), "202610150200_c.sql": mig("c")})
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatalf("older timestamp rejected: %v", err)
	}
	var ok bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('b') IS NOT NULL`).Scan(&ok); err != nil || !ok {
		t.Fatalf("migration b not applied: %v %v", ok, err)
	}
}

// The boot hint counts pages that have page_sections and no page_doc, read-only.
func TestLogUnconvertedPages_Integration(t *testing.T) {
	pool, ctx := scratchPool(t), context.Background()
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	var schemaID string
	if err := pool.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	for slug, attrs := range map[string]string{
		"legacy":    `{"page_sections":[]}`,
		"converted": `{"page_sections":[],"page_doc":{"v":2,"nodes":[]}}`,
		"doc-only":  `{"page_doc":{"v":2,"nodes":[]}}`,
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes) VALUES ($1, $2, $2, 'draft', $3::jsonb)`, schemaID, slug, attrs); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	logUnconvertedPages(ctx, pool, slog.New(slog.NewTextHandler(&buf, nil)))
	if got := buf.String(); !strings.Contains(got, "count=1") || !strings.Contains(got, "builder convert") {
		t.Errorf("log = %q", got)
	}
}
