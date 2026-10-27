package entity

// repository_concurrency_test.go locks in the current behaviour of
// PostgresEntityRepository.Update with respect to hatamoto_entities.version.
//
// As of 2026-05-30, repository.go:451 auto-increments version on every
// content update but does NOT include a `WHERE version = $X` clause. This
// means a concurrent Update silently overwrites an in-flight rewrite —
// see ADR plans/ADR-internal-linking-v2.md §8 row "(New) Race: editor save
// vs slug-rewriter clobbers rewrite". Internal Linking v2 mitigates this
// via SELECT ... FOR UPDATE inside the rewriter transaction, NOT by
// retrofitting optimistic concurrency onto Update.
//
// The integration test below exercises a real Postgres database via
// HATAMOTO_TEST_DB_URL (same gate as the v2 rewriter test). When the DB env
// is absent the test is skipped — CI without infra stays green.
//
// PURPOSE: if anyone ever adds a `WHERE version = $X` clause to
// repository.go:451 — accidentally or deliberately — this test will fail.
// That failure is intentional: such a change has ripple effects across
// every writer in the codebase and must be a conscious decision, with the
// rewriter's row-lock strategy revisited at the same time.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func TestUpdate_NoOptimisticConcurrencyGate(t *testing.T) {
	url := os.Getenv("HATAMOTO_TEST_DB_URL")
	if url == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set; skipping concurrency invariant test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	defer db.Close()

	adminCtx := database.WithSuperadmin(ctx)
	conn, err := db.GetConn(adminCtx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()

	var schemaID uuid.UUID
	if err := conn.QueryRow(adminCtx, `SELECT id FROM hatamoto_entity_schemas LIMIT 1`).Scan(&schemaID); err != nil {
		t.Skipf("no entity schemas present: %v", err)
	}

	id := uuid.New()
	_, err = conn.Exec(adminCtx,
		`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes, metadata, version)
		 VALUES ($1, $2, 'concurrency-probe', $3, 'draft', '{"content":"v0"}'::jsonb, '{}'::jsonb, 1)`,
		id, schemaID, fmt.Sprintf("concurrency-probe-%s", id.String()[:8]),
	)
	if err != nil {
		t.Fatalf("insert probe entity: %v", err)
	}
	t.Cleanup(func() {
		cleanupConn, err := db.GetConn(database.WithSuperadmin(context.Background()))
		if err != nil {
			return
		}
		defer cleanupConn.Release()
		_, _ = cleanupConn.Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE id = $1`, id)
	})

	repo := NewPostgresEntityRepository(db)

	// Both writers see version=1 at fetch time. Writer A commits "vA",
	// writer B commits "vB" with no version gate. The currently-correct
	// behaviour is that the second write wins silently and version
	// increments to 3. If a `WHERE version = $X` clause is introduced,
	// writer B should fail or no-op, which would break this assertion —
	// at which point the rewriter strategy must also be reconsidered.
	attrsA := json.RawMessage(`{"content":"vA"}`)
	attrsB := json.RawMessage(`{"content":"vB"}`)

	if _, err := repo.Update(adminCtx, id, nil, &UpdateEntityInput{Attributes: &attrsA}); err != nil {
		t.Fatalf("writer A update: %v", err)
	}
	if _, err := repo.Update(adminCtx, id, nil, &UpdateEntityInput{Attributes: &attrsB}); err != nil {
		t.Fatalf("writer B update: %v", err)
	}

	var finalContent string
	var finalVersion int
	if err := conn.QueryRow(adminCtx,
		`SELECT attributes->>'content', version FROM hatamoto_entities WHERE id = $1`,
		id,
	).Scan(&finalContent, &finalVersion); err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if finalContent != "vB" {
		t.Errorf("expected writer B to win silently (content=vB), got %q — "+
			"someone may have added optimistic concurrency to repository.go:451; "+
			"if so, revisit linking/v2/rewriter.go's SELECT ... FOR UPDATE strategy", finalContent)
	}
	if finalVersion != 3 {
		t.Errorf("expected version=3 after two sequential updates, got %d — "+
			"version auto-increment behaviour may have changed", finalVersion)
	}
}
