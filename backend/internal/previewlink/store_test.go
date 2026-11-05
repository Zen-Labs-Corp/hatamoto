//go:build integration

package previewlink

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func fixture(t *testing.T) (*Store, *database.DB, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	schemaID, entID := uuid.New(), uuid.New()
	slug := "pl-" + schemaID.String()[:8]
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema) VALUES ($1,$2,$2,'{"type":"object"}')`, schemaID, slug); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,attributes) VALUES ($1,$2,'x','x','en','draft','{}')`, entID, schemaID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, entID)
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	return New(db), db, entID
}

func TestCreateBounds(t *testing.T) {
	s, _, id := fixture(t)
	ctx := context.Background()
	for _, ttl := range []time.Duration{59 * time.Minute, 31 * 24 * time.Hour, 0, -time.Hour} {
		if _, _, err := s.Create(ctx, id, nil, "A", ttl); !errors.Is(err, ErrTTL) {
			t.Errorf("ttl %v: err = %v, want ErrTTL", ttl, err)
		}
	}
	for _, ttl := range []time.Duration{Min, Max} {
		if _, _, err := s.Create(ctx, id, nil, "A", ttl); err != nil {
			t.Errorf("ttl %v: %v", ttl, err)
		}
	}
}

func TestTokenStoredAsHashOnly(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	tok, l, err := s.Create(ctx, id, nil, "Ann", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 {
		t.Errorf("token length %d, want 43 (32 bytes, base64url)", len(tok))
	}
	var h []byte
	if err := db.Pool().QueryRow(ctx, `SELECT token_hash FROM hatamoto_preview_links WHERE id = $1`, l.ID).Scan(&h); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(tok))
	if string(h) != string(want[:]) {
		t.Error("stored hash is not SHA-256 of the token")
	}
	var leaked bool
	if err := db.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM hatamoto_preview_links WHERE token_tail = $1 AND length(token_tail) > 4)`, tok).Scan(&leaked); err != nil || leaked {
		t.Errorf("full token stored: %v %v", leaked, err)
	}
	if got, ok := s.Resolve(ctx, tok); !ok || got != id {
		t.Errorf("Resolve = %v %v", got, ok)
	}
	if _, ok := s.Resolve(ctx, tok+"x"); ok {
		t.Error("tampered token resolved")
	}
}

func TestExpiryRevokeAndCap(t *testing.T) {
	s, _, id := fixture(t)
	ctx := context.Background()
	tok, l, err := s.Create(ctx, id, nil, "Ann", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Revoke(ctx, id, l.ID); err != nil || !ok {
		t.Fatalf("Revoke = %v %v", ok, err)
	}
	if _, ok := s.Resolve(ctx, tok); ok {
		t.Error("revoked token resolved")
	}

	tok2, _, _ := s.Create(ctx, id, nil, "Ann", time.Hour)
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, ok := s.Resolve(ctx, tok2); ok {
		t.Error("expired token resolved")
	}
	s.now = time.Now

	for n := 1; n < MaxActive; n++ { // tok2 is the first live link
		if _, _, err := s.Create(ctx, id, nil, "Ann", time.Hour); err != nil {
			t.Fatalf("link %d: %v", n+1, err)
		}
	}
	if _, _, err := s.Create(ctx, id, nil, "Ann", time.Hour); !errors.Is(err, ErrTooMany) {
		t.Errorf("11th link: err = %v, want ErrTooMany", err)
	}
	if n, _ := s.CountActive(ctx, id); n != MaxActive {
		t.Errorf("CountActive = %d", n)
	}
	if ls, _ := s.List(ctx, id); len(ls) != MaxActive {
		t.Errorf("List = %d", len(ls))
	}
}
