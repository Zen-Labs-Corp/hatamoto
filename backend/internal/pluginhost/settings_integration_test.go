//go:build integration

package pluginhost

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with:
//
//	DATABASE_URL=postgres://... go test -tags=integration ./internal/pluginhost/...
//
// Expects core migration 006 to be applied; the test creates it if absent.
func TestSettingsStoreRoundTrip(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS hatamoto_plugin_settings (
		plugin text PRIMARY KEY, settings jsonb NOT NULL DEFAULT '{}', updated_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}

	const name = "it-settings-plugin"
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM hatamoto_plugin_settings WHERE plugin=$1`, name) })
	store := NewSettingsStore(pool)

	got, err := store.Get(ctx, name)
	if err != nil || string(got) != `{}` {
		t.Fatalf("absent: %s, %v", got, err)
	}
	if err := store.Put(ctx, name, json.RawMessage(`{"retries":7}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, name, json.RawMessage(`{"retries":8}`)); err != nil { // upsert
		t.Fatal(err)
	}
	stored, err := store.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Greeting string `json:"greeting"`
		Retries  int    `json:"retries"`
	}
	if err := json.Unmarshal(MergeDefaults([]byte(testSchema), stored), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Retries != 8 || cfg.Greeting != "hi" {
		t.Fatalf("cfg = %+v", cfg)
	}
}
