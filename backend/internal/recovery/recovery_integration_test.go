//go:build integration

package recovery

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// DATABASE_URL=... go test -tags=integration ./internal/recovery
func TestStore_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	pool := db.Pool()
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(where string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_recovery_copies WHERE `+where, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	suffix := uuid.NewString()[:8]
	var schemaID, entityID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'person'`).Scan(&schemaID); err != nil {
		t.Fatalf("person schema (is the database migrated?): %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes)
		VALUES ($1, 'Rec', $2, 'published', '{}'::jsonb) RETURNING id`, schemaID, "rec-"+suffix).Scan(&entityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, entityID) })
	ua, ub := uuid.New(), uuid.New()
	for i, u := range []uuid.UUID{ua, ub} {
		must(`INSERT INTO hatamoto_users (id, email, name) VALUES ($1, $2, 'u')`, u, []string{"a-", "b-"}[i]+suffix+"@example.com")
		id := u
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM hatamoto_users WHERE id = $1`, id) })
	}

	s := New(db)

	if c, err := s.Get(ctx, ua, entityID); err != nil || c != nil {
		t.Fatalf("Get before Put = %v, %v; want nil, nil", c, err)
	}
	first := Copy{Content: "<p>one</p>", Name: "N", Excerpt: "E", FeaturedImage: "/m/a.jpg", BaseVersion: 3}
	if err := s.Put(ctx, ua, entityID, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, ua, entityID)
	if err != nil || got == nil {
		t.Fatalf("Get = %v, %v", got, err)
	}
	if got.Content != first.Content || got.Name != "N" || got.Excerpt != "E" || got.FeaturedImage != "/m/a.jpg" || got.BaseVersion != 3 {
		t.Fatalf("round trip = %+v", got)
	}
	if time.Since(got.SavedAt) > time.Minute {
		t.Fatalf("SavedAt = %v, want now", got.SavedAt)
	}

	// A second Put replaces the copy; another user's copy is separate.
	if err := s.Put(ctx, ua, entityID, Copy{Content: "<p>two</p>", BaseVersion: 4}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, ub, entityID, Copy{Content: "<p>other</p>", BaseVersion: 4}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, ua, entityID); got == nil || got.Content != "<p>two</p>" || got.Name != "" || got.BaseVersion != 4 {
		t.Fatalf("after replace = %+v", got)
	}
	if n := count(`entity_id = $1`, entityID); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}

	// Delete removes only the caller's row and tolerates a missing one.
	if err := s.Delete(ctx, ua, entityID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, ua, entityID); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if got, _ = s.Get(ctx, ua, entityID); got != nil {
		t.Fatalf("copy survived Delete: %+v", got)
	}
	if got, _ = s.Get(ctx, ub, entityID); got == nil {
		t.Fatal("Delete removed another user's copy")
	}

	// Deleting the user cascades.
	must(`DELETE FROM hatamoto_users WHERE id = $1`, ub)
	if n := count(`user_id = $1`, ub); n != 0 {
		t.Fatalf("rows after user delete = %d", n)
	}

	// Deleting the entity cascades.
	if err := s.Put(ctx, ua, entityID, Copy{Content: "x"}); err != nil {
		t.Fatal(err)
	}
	must(`DELETE FROM hatamoto_entities WHERE id = $1`, entityID)
	if n := count(`entity_id = $1`, entityID); n != 0 {
		t.Fatalf("rows after entity delete = %d", n)
	}
}
