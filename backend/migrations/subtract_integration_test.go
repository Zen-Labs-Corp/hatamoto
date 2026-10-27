//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/migrations"
)

// Migrates a scratch database to 015, seeds rows in the old shape, applies
// 016/017 and checks that data the new schema still cares about was carried
// over (or made safe) instead of silently lost:
// DATABASE_URL=... go test -tags=integration ./migrations -run SubtractMigrations
func TestSubtractMigrations_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	dbName := "hm_subtract_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)") })

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + dbName
	sqlDB, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.UpTo(ctx, 15); err != nil {
		t.Fatalf("up to 15: %v", err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := sqlDB.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := sqlDB.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}

	// --- seed (old schema) ---
	for _, ev := range []struct {
		typ, src, ts string
		n            int
	}{
		{"ai_referred_visit", "chatgpt", "2026-10-05 10:00:00+00", 3},
		{"ai_referred_visit", "claude", "2026-10-05 11:00:00+00", 1},
		{"page_view", "", "2026-10-05 12:00:00+00", 4},
		{"other", "", "2026-10-05 12:00:00+00", 9},
	} {
		for i := 0; i < ev.n; i++ {
			var src any
			if ev.src != "" {
				src = ev.src
			}
			exec(`INSERT INTO hatamoto_events (event_type, ai_source, occurred_at) VALUES ($1, $2, $3)`, ev.typ, src, ev.ts)
		}
	}

	owner, viewer := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_users (id, email, role) VALUES ($1, 'o@x.test', 'owner'), ($2, 'v@x.test', 'viewer')`, owner, viewer)
	for i, creator := range []uuid.UUID{owner, viewer} {
		exec(`INSERT INTO hatamoto_api_keys (name, key_prefix, key_hash, created_by) VALUES ($1, $2, $3, $4)`,
			fmt.Sprintf("k%d", i), fmt.Sprintf("p%d", i), fmt.Sprintf("h%d", i), creator)
		exec(`INSERT INTO hatamoto_sessions (user_id, expires_at) VALUES ($1, now() + interval '1 day')`, creator)
	}

	schema := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, 'S', $2, '{}')`, schema, "s-"+schema.String())
	exec(`INSERT INTO hatamoto_locales (code, country_code, language_code, name) VALUES ('en','US','en','English') ON CONFLICT DO NOTHING`)
	ent := func(slug, status string, gated bool, successor any) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, gated, locale_code, successor_entity_id, published_at)
		      VALUES ($1, $2, $3, $3, $4, $5, 'en', $6, now())`, id, schema, slug, status, gated, successor)
		return id
	}
	succ := ent("new-page", "published", false, nil)
	ent("old-page", "redirected", false, succ)
	ent("old-conflict", "redirected", false, succ)
	exec(`INSERT INTO hatamoto_redirects (source_path, target_url) VALUES ('/en/old-conflict', '/elsewhere')`)
	ent("secret", "published", true, nil)
	ent("secret-draft", "draft", true, nil)

	exec(`INSERT INTO hatamoto_webhooks (name, url, events) VALUES
	      ('mixed', 'https://x.test/a', '["lead.captured","entity.created"]'),
	      ('leadonly', 'https://x.test/b', '["lead.verified"]'),
	      ('clean', 'https://x.test/c', '["entity.created"]')`)

	if _, err := prov.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	// --- 016: rollup backfill ---
	for _, c := range []struct {
		kind, key string
		want      int
	}{{"ai_referral", "chatgpt", 3}, {"ai_referral", "claude", 1}, {"pageview", "", 4}} {
		if got := count(`SELECT count FROM hatamoto_analytics_daily WHERE day = '2026-10-05' AND kind = $1 AND key = $2`, c.kind, c.key); got != c.want {
			t.Errorf("rollup %s/%s = %d, want %d", c.kind, c.key, got, c.want)
		}
	}
	if got := count(`SELECT count(*) FROM hatamoto_analytics_daily`); got != 3 {
		t.Errorf("rollup rows = %d, want 3 (non-pageview events must not be counted)", got)
	}

	// --- 017: demoted users ---
	if got := count(`SELECT count(*) FROM hatamoto_api_keys WHERE created_by = $1 AND is_active AND revoked_at IS NULL`, viewer); got != 0 {
		t.Errorf("viewer's API keys still live: %d", got)
	}
	if got := count(`SELECT count(*) FROM hatamoto_api_keys WHERE created_by = $1 AND is_active AND revoked_at IS NULL`, owner); got != 1 {
		t.Errorf("owner's API key was touched: live = %d", got)
	}
	if got := count(`SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1`, viewer); got != 0 {
		t.Errorf("viewer's sessions survived: %d", got)
	}
	if got := count(`SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1`, owner); got != 1 {
		t.Errorf("owner's session was deleted: %d", got)
	}
	if got := count(`SELECT count(*) FROM hatamoto_users WHERE id = $1 AND role = 'editor'`, viewer); got != 1 {
		t.Errorf("viewer was not turned into an editor")
	}

	// --- 017: gated → draft ---
	if got := count(`SELECT count(*) FROM hatamoto_entities WHERE slug IN ('secret','secret-draft') AND status = 'draft'`); got != 2 {
		t.Errorf("gated entities left public: %d of 2 are draft", got)
	}
	if got := count(`SELECT count(*) FROM hatamoto_entities WHERE slug = 'new-page' AND status = 'published'`); got != 1 {
		t.Errorf("ungated entity was unpublished")
	}

	// --- 017: successors are dropped, not turned into redirects ---
	if got := count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/en/old-page'`); got != 0 {
		t.Errorf("successor created a redirect row (%d); successors were never served on main", got)
	}
	var target string
	if err := sqlDB.QueryRowContext(ctx, `SELECT target_url FROM hatamoto_redirects WHERE source_path = '/en/old-conflict'`).Scan(&target); err != nil || target != "/elsewhere" {
		t.Errorf("existing redirect was altered: %q (err %v)", target, err)
	}
	// (The locale-removal migration, which Up also applied, adds its own tagged rows.)
	if got := count(`SELECT count(*) FROM hatamoto_redirects WHERE source_tag IS DISTINCT FROM 'v1-09-kill-locales'`); got != 1 {
		t.Errorf("redirect rows = %d, want only the pre-existing one", got)
	}

	// --- 017: webhooks (v1-19 handed them to the plugin as hand-over rows; the core webhooks migration copied them back) ---
	// One Up runs 202610201100 and 202610251000 together (a direct jump from the pre-plugin
	// core), so the hand-over rows are fresh and keep their active flag.
	legacy := func(name string) (events string, active bool) {
		t.Helper()
		if err := sqlDB.QueryRowContext(ctx, `SELECT events::text, active FROM hatamoto_webhooks WHERE name = $1`, name).Scan(&events, &active); err != nil {
			t.Fatalf("webhook %q: %v", name, err)
		}
		return events, active
	}
	if events, active := legacy("mixed"); events != `["entity.created"]` || !active {
		t.Errorf("mixed webhook not trimmed to its remaining event: %s active=%v", events, active)
	}
	if _, active := legacy("leadonly"); active {
		t.Errorf("lead-only webhook should be deactivated")
	}
	if _, active := legacy("clean"); !active {
		t.Errorf("clean webhook was touched")
	}
}

// 016 must not fail when the events table is already gone (or never existed).
func TestSubtractMigration016_NoEventsTable_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	dbName := "hm_subtract_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)") })
	u, _ := url.Parse(dsn)
	u.Path = "/" + dbName
	sqlDB, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	prov, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.UpTo(ctx, 15); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `DROP TABLE hatamoto_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Up(ctx); err != nil {
		t.Fatalf("up without events table: %v", err)
	}
}
