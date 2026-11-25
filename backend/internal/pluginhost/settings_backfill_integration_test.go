//go:build integration

package pluginhost

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSealExistingIdempotent(t *testing.T) {
	ctx, pool := settingsTestPool(t)
	const sealedName, plainName = "it-seal-paid-lookup", "it-seal-plain"
	seed(t, ctx, pool, sealedName, `{"greeting":"hey","api_key":"k-123"}`)
	seed(t, ctx, pool, plainName, `{"greeting":"hey","token":"t-1"}`)
	store := NewSettingsStore(pool, SettingsKey(strings.Repeat("k", 32)), nil)
	schemas := map[string][]byte{
		sealedName: []byte(testSchema),
		plainName:  []byte(`{"type":"object","properties":{"greeting":{"type":"string"},"token":{"type":"string"}}}`),
	}
	otherBefore := rawRow(t, ctx, pool, plainName)

	rep, err := store.SealExisting(ctx, schemas)
	if err != nil || rep.Sealed != 1 || rep.Plugins != 1 || rep.Unreadable != 0 {
		t.Fatalf("first run = %+v, %v; want 1 sealed in 1 plugin", rep, err)
	}
	if got := rawKey(t, ctx, pool, sealedName, "api_key"); !strings.HasPrefix(got, SealedPrefix) || strings.Contains(got, "k-123") {
		t.Fatal("api_key is not sealed after the first run")
	}
	if got := rawKey(t, ctx, pool, sealedName, "greeting"); got != "hey" {
		t.Fatalf("greeting = %q, want it untouched", got)
	}
	if got := rawRow(t, ctx, pool, plainName); got != otherBefore {
		t.Fatalf("plugin without writeOnly changed: %s", got)
	}
	after := rawRow(t, ctx, pool, sealedName)

	rep, err = store.SealExisting(ctx, schemas)
	if err != nil || rep != (SealReport{}) {
		t.Fatalf("second run = %+v, %v; want zero", rep, err)
	}
	if got := rawRow(t, ctx, pool, sealedName); got != after {
		t.Fatal("row changed on the second run")
	}
	v, err := store.Get(ctx, sealedName)
	if err != nil || !strings.Contains(string(v), `"api_key":"k-123"`) {
		t.Fatalf("sealed value does not open: %v", err)
	}
}

func TestSealExistingNoKey(t *testing.T) {
	ctx, pool := settingsTestPool(t)
	const name = "it-seal-nokey"
	seed(t, ctx, pool, name, `{"api_key":"k-123"}`)
	before := rawRow(t, ctx, pool, name)
	rep, err := NewSettingsStore(pool, nil, nil).SealExisting(ctx, map[string][]byte{name: []byte(testSchema)})
	if err == nil || rep != (SealReport{}) {
		t.Fatalf("no key = %+v, %v; want an error", rep, err)
	}
	if got := rawRow(t, ctx, pool, name); got != before {
		t.Fatal("row changed without a key")
	}
}

// settingsTestPool connects to DATABASE_URL and makes sure the table exists.
func settingsTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
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
	return ctx, pool
}

func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, settings string) {
	t.Helper()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM hatamoto_plugin_settings WHERE plugin=$1`, name) })
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_plugin_settings (plugin, settings) VALUES ($1, $2::jsonb)
		ON CONFLICT (plugin) DO UPDATE SET settings = EXCLUDED.settings`, name, settings); err != nil {
		t.Fatal(err)
	}
}

func rawRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(ctx, `SELECT settings::text FROM hatamoto_plugin_settings WHERE plugin=$1`, name).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func rawKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, key string) string {
	t.Helper()
	var s *string
	if err := pool.QueryRow(ctx, `SELECT settings->>$2 FROM hatamoto_plugin_settings WHERE plugin=$1`, name, key).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s == nil {
		return ""
	}
	return *s
}
