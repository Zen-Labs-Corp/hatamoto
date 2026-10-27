package entity

// repository_published_at_test.go: UpdateStatus stamps published_at on the
// first publish only. Integration test against HATAMOTO_TEST_DB_URL (a
// migrated database); skipped when the env is absent.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func TestUpdateStatus_KeepsOriginalPublishedAt(t *testing.T) {
	url := os.Getenv("HATAMOTO_TEST_DB_URL")
	if url == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set; skipping published_at integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := database.New(ctx, config.DatabaseConfig{URL: url, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
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
	repo := NewPostgresEntityRepository(db)

	insert := func(t *testing.T, status string, publishedAt *time.Time) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := conn.Exec(adminCtx,
			`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes, metadata, version, published_at)
			 VALUES ($1, $2, 'published-at-probe', $3, $4, '{}'::jsonb, '{}'::jsonb, 1, $5)`,
			id, schemaID, fmt.Sprintf("published-at-probe-%s", id.String()[:8]), status, publishedAt); err != nil {
			t.Fatalf("insert probe: %v", err)
		}
		t.Cleanup(func() { _, _ = conn.Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE id = $1`, id) })
		return id
	}
	day := 24 * time.Hour

	t.Run("re-publish keeps the original date", func(t *testing.T) {
		orig := time.Date(2022, 9, 13, 21, 32, 24, 0, time.UTC)
		id := insert(t, "published", &orig)
		e, err := repo.UpdateStatus(adminCtx, id, nil, StatusPublished)
		if err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		if e.PublishedAt == nil || !e.PublishedAt.Equal(orig) {
			t.Fatalf("published_at = %v, want original %v", e.PublishedAt, orig)
		}
	})
	t.Run("unpublish then publish keeps the original date", func(t *testing.T) {
		orig := time.Date(2024, 6, 10, 10, 29, 12, 0, time.UTC)
		id := insert(t, "published", &orig)
		if _, err := repo.UpdateStatus(adminCtx, id, nil, StatusDraft); err != nil {
			t.Fatalf("to draft: %v", err)
		}
		e, err := repo.UpdateStatus(adminCtx, id, nil, StatusPublished)
		if err != nil {
			t.Fatalf("to published: %v", err)
		}
		if e.PublishedAt == nil || !e.PublishedAt.Equal(orig) {
			t.Fatalf("published_at = %v, want original %v", e.PublishedAt, orig)
		}
	})
	t.Run("first publish stamps now", func(t *testing.T) {
		id := insert(t, "draft", nil)
		before := time.Now().Add(-time.Minute)
		e, err := repo.UpdateStatus(adminCtx, id, nil, StatusPublished)
		if err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		if e.PublishedAt == nil || e.PublishedAt.Before(before) || e.PublishedAt.After(time.Now().Add(time.Minute)) {
			t.Fatalf("published_at = %v, want ~now", e.PublishedAt)
		}
	})
	t.Run("future (scheduled) date published early becomes now", func(t *testing.T) {
		future := time.Now().Add(10 * day).UTC().Truncate(time.Second)
		id := insert(t, "draft", &future)
		e, err := repo.UpdateStatus(adminCtx, id, nil, StatusPublished)
		if err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		if e.PublishedAt == nil || !e.PublishedAt.Before(future.Add(-day)) {
			t.Fatalf("published_at = %v, want ~now (not the future %v)", e.PublishedAt, future)
		}
	})
	t.Run("non-publish status leaves the date alone", func(t *testing.T) {
		orig := time.Date(2023, 1, 23, 1, 34, 2, 0, time.UTC)
		id := insert(t, "published", &orig)
		e, err := repo.UpdateStatus(adminCtx, id, nil, StatusArchived)
		if err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		if e.PublishedAt == nil || !e.PublishedAt.Equal(orig) {
			t.Fatalf("published_at = %v, want %v", e.PublishedAt, orig)
		}
	})
}
