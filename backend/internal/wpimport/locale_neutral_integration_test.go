//go:build integration

package wpimport

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

// NewDeps wires the site locales into the entity service, so an item created
// without a language in a "shared across languages" type gets und on a
// multi-language site, as it does through the server.
func TestNewDeps_LocaleNeutralDefaultOnMultiLanguageSite(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	pool := db.Pool()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var prev []byte
	hadSite := pool.QueryRow(ctx, `SELECT locales FROM hatamoto_site_config`).Scan(&prev) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, language, locales) VALUES ('wpn test', 'en', '[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"}]'::jsonb)
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales, x_default_locale = ''`)
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = pool.Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb`, string(prev))
		} else {
			_, _ = pool.Exec(c, `DELETE FROM hatamoto_site_config`)
		}
	})
	schemaID := uuid.New()
	slug := "wpn-" + schemaID.String()[:6]
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{"locale_neutral":true}')`, schemaID, slug)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = pool.Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})

	d := NewDeps(db, DepsConfig{MediaDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(d.Close)
	e, err := d.Entities.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: schemaID, Name: "FAQ", Attributes: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if e.Locale != "und" {
		t.Errorf("locale = %q, want und", e.Locale)
	}
}
