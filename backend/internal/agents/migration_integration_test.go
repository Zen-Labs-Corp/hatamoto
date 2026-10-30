//go:build integration

package agents

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// Exercises migrations 009/010 against a migrated database:
// DATABASE_URL=... go test -tags=integration ./internal/agents -run Migration
func TestAgentMigrations_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	marker := "it-" + uuid.NewString()[:6]

	// Migration 014 drops hatamoto_bot_visits, but 010 (the backfill under test)
	// still reads it. Recreate the legacy shape for the backfill subtests and
	// remove it again if this test was the one that created it.
	ensureLegacyBotVisits := func() {
		t.Helper()
		var exists bool
		if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('public.hatamoto_bot_visits') IS NOT NULL`).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			return
		}
		exec(`CREATE TABLE public.hatamoto_bot_visits (
		    id uuid DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
		    bot_name text NOT NULL, bot_category text NOT NULL, path text NOT NULL,
		    hit_count integer DEFAULT 1 NOT NULL, visit_date date DEFAULT CURRENT_DATE NOT NULL,
		    last_user_agent text, last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
		    created_at timestamp with time zone DEFAULT now() NOT NULL)`)
		t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DROP TABLE IF EXISTS public.hatamoto_bot_visits`) })
	}

	t.Run("partition routing", func(t *testing.T) {
		inPartition := uuid.New()
		inDefault := uuid.New()
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE agent_id = $1`, marker)
		})
		exec(`INSERT INTO hatamoto_agent_hits (id, occurred_at, agent_id, source) VALUES ($1, '2026-10-15 12:00:00+00', $2, 'backend')`, inPartition, marker)
		exec(`INSERT INTO hatamoto_agent_hits (id, occurred_at, agent_id, source) VALUES ($1, '2020-01-15 12:00:00+00', $2, 'backend')`, inDefault, marker)

		var rel string
		if err := db.Pool().QueryRow(ctx, `SELECT tableoid::regclass::text FROM hatamoto_agent_hits WHERE id = $1`, inPartition).Scan(&rel); err != nil {
			t.Fatal(err)
		}
		if rel != "hatamoto_agent_hits_2026_10" {
			t.Errorf("partition = %s, want hatamoto_agent_hits_2026_10", rel)
		}
		if err := db.Pool().QueryRow(ctx, `SELECT tableoid::regclass::text FROM hatamoto_agent_hits WHERE id = $1`, inDefault).Scan(&rel); err != nil {
			t.Fatal(err)
		}
		if rel != "hatamoto_agent_hits_default" {
			t.Errorf("partition = %s, want hatamoto_agent_hits_default", rel)
		}
	})

	t.Run("backfill normalises paths", func(t *testing.T) {
		ensureLegacyBotVisits()
		botName := "ItBot-" + marker
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_bot_visits WHERE bot_name = $1`, botName)
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE operator = $1`, botName)
		})
		exec(`INSERT INTO hatamoto_bot_visits (bot_name, bot_category, path, hit_count, visit_date, last_user_agent, last_seen_at)
		      VALUES ($1, 'seo', '/api/v1/public/sitemap-au.xml', 3, '2026-10-05', 'ua', now())`, botName)
		// Run the real 010 Up section (idempotent), then check the fixture row.
		raw, err := os.ReadFile("../../migrations/010_agent_daily_backfill.sql")
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		exec(strings.Replace(up, "-- +goose Up", "", 1))
		var path string
		if err := db.Pool().QueryRow(ctx, `SELECT path FROM hatamoto_agent_daily WHERE operator = $1`, botName).Scan(&path); err != nil {
			t.Fatal(err)
		}
		if path != "/sitemap-au.xml" {
			t.Errorf("path = %q, want /sitemap-au.xml", path)
		}
	})

	t.Run("backfill maps legacy names to registry agents", func(t *testing.T) {
		ensureLegacyBotVisits()
		day := "2026-10-06"
		names := []string{"MajesticBot", "GPTBot", "Unknown-" + marker}
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_bot_visits WHERE path = '/api/v1/public/' || $1`, marker)
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE path = '/' || $1`, marker)
		})
		for _, n := range names {
			cat := "seo"
			if n == "GPTBot" {
				cat = "ai"
			}
			exec(`INSERT INTO hatamoto_bot_visits (bot_name, bot_category, path, hit_count, visit_date, last_user_agent, last_seen_at)
			      VALUES ($1, $2, '/api/v1/public/' || $3::text, 2, $4::date, 'ua', now())`, n, cat, marker, day)
		}
		raw, err := os.ReadFile("../../migrations/010_agent_daily_backfill.sql")
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		exec(strings.Replace(up, "-- +goose Up", "", 1))
		for _, c := range []struct{ id, operator, purpose string }{
			{"mj12bot", "Majestic", "seo"},
			{"gptbot", "OpenAI", "training"},
			{"unknown-" + marker, "Unknown-" + marker, "seo"},
		} {
			var operator, purpose string
			if err := db.Pool().QueryRow(ctx, `SELECT operator, purpose FROM hatamoto_agent_daily WHERE agent_id = $1 AND path = '/' || $2::text`, c.id, marker).Scan(&operator, &purpose); err != nil {
				t.Fatalf("%s: %v", c.id, err)
			}
			if operator != c.operator || purpose != c.purpose {
				t.Errorf("%s = %s/%s, want %s/%s", c.id, operator, purpose, c.operator, c.purpose)
			}
		}
	})
	t.Run("facebook split merges facebookbot rows into facebookexternalhit", func(t *testing.T) {
		path := "/" + marker
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE path = $1`, path)
		})
		insP := func(agent, purpose, day string, status, hits, verified, spoofed, unverifiable, bytes int) {
			exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, hits, verified_hits, spoofed_hits, unverifiable_hits, bytes_total)
			      VALUES ($1::date, $2, $3, $4, 'Meta', $10, $5, $6, $7, $8, $9)`, day, agent, path, status, hits, verified, spoofed, unverifiable, bytes, purpose)
		}
		ins := func(agent, day string, status, hits, verified, spoofed, unverifiable, bytes int) {
			insP(agent, "social", day, status, hits, verified, spoofed, unverifiable, bytes)
		}
		ins("facebookbot", "2026-10-06", 200, 5, 3, 1, 1, 500)         // conflicts with the row below
		ins("facebookexternalhit", "2026-10-06", 200, 2, 1, 0, 1, 200) // existing target row
		ins("facebookbot", "2026-10-07", 200, 4, 4, 0, 0, 400)         // no conflict
		// Genuine FacebookBot written by the new binary (purpose training) must stay.
		insP("facebookbot", "training", "2026-10-08", 200, 3, 0, 0, 3, 300)
		raw, err := os.ReadFile("../../migrations/015_agent_facebook_split.sql")
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		up = strings.NewReplacer("-- +goose Up", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(up)
		exec(up)
		exec(up) // idempotent

		var left int
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_agent_daily WHERE agent_id = 'facebookbot' AND purpose = 'social' AND path = $1`, path).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Errorf("%d social facebookbot rows left", left)
		}
		var kept int64
		if err := db.Pool().QueryRow(ctx, `SELECT hits FROM hatamoto_agent_daily WHERE agent_id = 'facebookbot' AND purpose = 'training' AND path = $1`, path).Scan(&kept); err != nil || kept != 3 {
			t.Errorf("training facebookbot row: hits=%d err=%v, want 3", kept, err)
		}
		for _, c := range []struct {
			day                                     string
			hits, verified, spoofed, unverif, bytes int64
		}{
			{"2026-10-06", 7, 4, 1, 2, 700},
			{"2026-10-07", 4, 4, 0, 0, 400},
		} {
			var hits, verified, spoofed, unverif, bytes int64
			var operator, purpose string
			if err := db.Pool().QueryRow(ctx, `SELECT hits, verified_hits, spoofed_hits, unverifiable_hits, bytes_total, operator, purpose
			    FROM hatamoto_agent_daily WHERE agent_id = 'facebookexternalhit' AND path = $1 AND day = $2::date AND status = 200`, path, c.day).
				Scan(&hits, &verified, &spoofed, &unverif, &bytes, &operator, &purpose); err != nil {
				t.Fatalf("%s: %v", c.day, err)
			}
			if hits != c.hits || verified != c.verified || spoofed != c.spoofed || unverif != c.unverif || bytes != c.bytes || operator != "Meta" || purpose != "social" {
				t.Errorf("%s = %d/%d/%d/%d/%d %s/%s, want %+v", c.day, hits, verified, spoofed, unverif, bytes, operator, purpose, c)
			}
		}
	})
	t.Run("verification reset sends re-checkable spoofed hits back to pending", func(t *testing.T) {
		path := "/reset-" + marker
		agent := "reset-" + marker
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE agent_id = $1`, agent)
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE agent_id = $1`, agent)
		})
		// Yesterday (UTC) is inside the window and keeps every hit on one daily
		// key whatever the time of day the test runs; the daily row adds up to them.
		yesterday := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
		exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, hits, verified_hits, spoofed_hits, unverifiable_hits)
		      VALUES ($1::date, $2, $3, 200, 9, 1, 5, 1)`, yesterday, agent, path)
		ins := func(at time.Time, verification string, truncated bool) uuid.UUID {
			id := uuid.New()
			exec(`INSERT INTO hatamoto_agent_hits (id, occurred_at, agent_id, path, status, verification, ip, ip_truncated, source)
			      VALUES ($1, $2, $3, $4, 200, $5, '203.0.113.7', $6, 'backend')`, id, at, agent, path, verification, truncated)
			return id
		}
		recent := ins(yesterday.Add(1*time.Hour), "spoofed", false)
		recent2 := ins(yesterday.Add(2*time.Hour), "spoofed", false)
		trunc := ins(yesterday.Add(3*time.Hour), "spoofed", true)
		verified := ins(yesterday.Add(4*time.Hour), "verified", false)
		pending := ins(yesterday.Add(5*time.Hour), "pending", false)
		// Older than the window and untruncated: keeps its verdict (no daily row needed).
		old := ins(time.Now().Add(-10*24*time.Hour), "spoofed", false)

		raw, err := os.ReadFile("../../migrations/202610301700_agent_verification_reset.sql")
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		up = strings.NewReplacer("-- +goose Up", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(up)
		state := func(id uuid.UUID) string {
			var v string
			if err := db.Pool().QueryRow(ctx, `SELECT verification FROM hatamoto_agent_hits WHERE id = $1`, id).Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		counters := func() (hits, verifiedN, spoofed, unverif int64) {
			if err := db.Pool().QueryRow(ctx, `SELECT hits, verified_hits, spoofed_hits, unverifiable_hits FROM hatamoto_agent_daily
			    WHERE agent_id = $1 AND path = $2 AND status = 200`, agent, path).Scan(&hits, &verifiedN, &spoofed, &unverif); err != nil {
				t.Fatal(err)
			}
			return
		}
		check := func(when string) {
			t.Helper()
			for _, c := range []struct {
				name string
				id   uuid.UUID
				want string
			}{
				{"recent", recent, "pending"}, {"recent2", recent2, "pending"}, {"truncated", trunc, "unverifiable"},
				{"older than the window", old, "spoofed"}, {"verified", verified, "verified"}, {"pending", pending, "pending"},
			} {
				if got := state(c.id); got != c.want {
					t.Errorf("%s: %s = %s, want %s", when, c.name, got, c.want)
				}
			}
			// 2 reset to pending + 1 truncated: spoofed 5-3, unverifiable 1+1.
			hits, v, s, u := counters()
			if hits != 9 || v != 1 || s != 2 || u != 2 || hits < v+s+u {
				t.Errorf("%s: counters hits=%d verified=%d spoofed=%d unverifiable=%d, want 9/1/2/2", when, hits, v, s, u)
			}
		}
		exec(up)
		check("first run")
		exec(up)
		check("second run")
	})
}
