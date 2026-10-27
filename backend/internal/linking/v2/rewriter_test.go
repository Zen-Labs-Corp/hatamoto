package v2

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// Integration-only because the rewriter depends on JSONB updates, the
// FOR UPDATE row lock, and the audit-log insert sequence — exercising those
// in mocks loses the value the test exists for.
//
// Setup contract:
//   1. HATAMOTO_TEST_DB_URL points at a Postgres database with migrations
//      086_linking_v2.sql applied (plus the prerequisite migrations).
//   2. The test creates throwaway entities and removes them on Cleanup.
//
// Without HATAMOTO_TEST_DB_URL the test is skipped — CI without a database
// (lint-only, no Docker) must stay green.

func openTestDB(t *testing.T) *database.DB {
	t.Helper()
	url := os.Getenv("HATAMOTO_TEST_DB_URL")
	if url == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set; skipping rewriter integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := database.New(ctx, config.DatabaseConfig{
		URL:             url,
		MaxConnections:  4,
		MinConnections:  1,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	})
	if err != nil {
		t.Skipf("connect to test db: %v", err)
	}
	return db
}

func requireSchema(t *testing.T, db *database.DB) {
	t.Helper()
	conn, err := db.GetConn(context.Background())
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	var ok bool
	err = conn.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_name = 'hatamoto_entity_link_index')`,
	).Scan(&ok)
	if err != nil || !ok {
		t.Skipf("hatamoto_entity_link_index not present (run migration 086); err=%v", err)
	}
}

func insertTestEntity(t *testing.T, db *database.DB, name, content string) uuid.UUID {
	t.Helper()
	ctx := database.WithSuperadmin(context.Background())
	conn, err := db.GetConn(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	var schemaID uuid.UUID
	if err := conn.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas LIMIT 1`).Scan(&schemaID); err != nil {
		t.Skipf("no entity schemas present in test db: %v", err)
	}

	id := uuid.New()
	attrs := fmt.Sprintf(`{"content": %q}`, content)
	_, err = conn.Exec(ctx,
		`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes, metadata, version)
		 VALUES ($1, $2, $3, $4, 'draft', $5::jsonb, '{}'::jsonb, 1)`,
		id, schemaID, name, "v2test-"+id.String()[:8], attrs,
	)
	if err != nil {
		t.Fatalf("insert entity %s: %v", name, err)
	}
	t.Cleanup(func() {
		cleanupCtx := database.WithSuperadmin(context.Background())
		conn2, err := db.GetConn(cleanupCtx)
		if err != nil {
			return
		}
		defer conn2.Release()
		_, _ = conn2.Exec(cleanupCtx, `DELETE FROM hatamoto_entities WHERE id = $1`, id)
	})
	return id
}

func TestRewriteForEntity_Integration(t *testing.T) {
	if os.Getenv("HATAMOTO_TEST_DB_URL") == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}

	db := openTestDB(t)
	defer db.Close()
	requireSchema(t, db)

	ctx := database.WithSuperadmin(context.Background())
	targetID := insertTestEntity(t, db, "rewrite-target", "")

	source1Content := `<p>Outbound to <a data-entity-id="` + targetID.String() + `" href="/blog/old-seo">SEO</a> service.</p>` +
		`<p>And another <a href="/blog/foo">plain link</a> that must survive.</p>`
	source2Content := `<p>Different source linking to <a data-entity-id="` + targetID.String() + `" href="/blog/old-seo">our SEO</a>.</p>`

	source1 := insertTestEntity(t, db, "rewrite-source-1", source1Content)
	source2 := insertTestEntity(t, db, "rewrite-source-2", source2Content)

	if err := Reindex(ctx, db, source1); err != nil {
		t.Fatalf("reindex source1: %v", err)
	}
	if err := Reindex(ctx, db, source2); err != nil {
		t.Fatalf("reindex source2: %v", err)
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	var indexedCount int
	_ = conn.QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_entity_link_index WHERE target_entity_id = $1`, targetID).Scan(&indexedCount)
	conn.Release()
	if indexedCount != 2 {
		t.Fatalf("expected 2 index rows, got %d", indexedCount)
	}

	if err := RewriteForEntity(ctx, db, targetID, "/blog/seo", ReasonSlugChange, nil); err != nil {
		t.Fatalf("RewriteForEntity: %v", err)
	}

	conn, err = db.GetConn(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	for _, sid := range []uuid.UUID{source1, source2} {
		var content string
		if err := conn.QueryRow(ctx, `SELECT attributes->>'content' FROM hatamoto_entities WHERE id = $1`, sid).Scan(&content); err != nil {
			t.Fatalf("load source %s: %v", sid, err)
		}
		if !strings.Contains(content, `href="/blog/seo"`) {
			t.Errorf("source %s missing new href; content=%q", sid, content)
		}
		if strings.Contains(content, `href="/blog/old-seo"`) {
			t.Errorf("source %s still has old href; content=%q", sid, content)
		}
	}

	var s1content string
	_ = conn.QueryRow(ctx, `SELECT attributes->>'content' FROM hatamoto_entities WHERE id = $1`, source1).Scan(&s1content)
	if !strings.Contains(s1content, `href="/blog/foo"`) {
		t.Errorf("plain href in source1 was rewritten or lost; content=%q", s1content)
	}
	if !strings.Contains(s1content, ">SEO<") {
		t.Errorf("source1 anchor text mutated; content=%q", s1content)
	}

	var auditCount int
	_ = conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM hatamoto_entity_link_audit WHERE target_entity_id = $1 AND reason = $2`,
		targetID, ReasonSlugChange,
	).Scan(&auditCount)
	if auditCount != 2 {
		t.Errorf("expected 2 audit rows, got %d", auditCount)
	}

	if err := RewriteForEntity(ctx, db, targetID, "/blog/seo", ReasonSlugChange, nil); err != nil {
		t.Fatalf("idempotent rewrite: %v", err)
	}
	_ = conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM hatamoto_entity_link_audit WHERE target_entity_id = $1`,
		targetID,
	).Scan(&auditCount)
	if auditCount != 2 {
		t.Errorf("re-running rewrite produced new audit rows; total=%d expected 2", auditCount)
	}

	_, _ = conn.Exec(context.Background(),
		`DELETE FROM hatamoto_entity_link_audit WHERE target_entity_id = $1`, targetID)
}
