//go:build integration

package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/render"
)

// A loop's attribute filters, resolved against "today" in the site zone, and
// the expiry they report (needs a migrated Postgres):
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run LoopAttr
func TestRenderLoopAttrIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping loop attribute integration test (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "la-it")
	t.Setenv("HATAMOTO_SITE_NAME", "LA IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "LA IT")
	t.Setenv("PUBLIC_BASE_URL", "https://la-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
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

	sfx := uuid.NewString()[:6]
	schema := "event-" + sfx
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Events',$2,'{"type":"object"}','{}')`, schemaID, schema)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})

	// The site is in Auckland (UTC+12 in June); at this instant its date is a day ahead of UTC.
	nowUTC := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	const local = "2026-06-11"
	day := func(plus int) string {
		d, _ := time.Parse("2006-01-02", local)
		return d.AddDate(0, 0, plus).Format("2006-01-02")
	}
	for _, e := range []struct{ slug, date, status string }{
		{"yesterday", day(-1), "scheduled"},
		{"today", day(0), "scheduled"},
		{"tomorrow", day(1), "scheduled"},
		{"later", day(10), "cancelled"},
	} {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,'published',now(),$4::jsonb)`, uuid.New(), schemaID, e.slug,
			fmt.Sprintf(`{"start_date":%q,"status":%q}`, e.date, e.status))
	}

	// The site zone is set before the server's first siteconfig read (it is cached).
	var hadSite bool
	var prevTZ *string
	if err := db.Pool().QueryRow(ctx, `SELECT true, timezone FROM hatamoto_site_config LIMIT 1`).Scan(&hadSite, &prevTZ); err != nil {
		hadSite = false
	}
	if hadSite {
		exec(`UPDATE hatamoto_site_config SET timezone = 'Pacific/Auckland'`)
	} else {
		exec(`INSERT INTO hatamoto_site_config (company_name, timezone) VALUES ('Loop IT', 'Pacific/Auckland')`)
	}
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET timezone = $1`, prevTZ)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
	})
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d := newRenderData(srv)
	d.now = func() time.Time { return nowUTC }

	names := func(r render.QueryResult) string {
		var out []string
		for _, it := range r.Items {
			out = append(out, it.Name)
		}
		return strings.Join(out, ",")
	}
	q := func(fs ...render.AttrFilter) render.QueryResult {
		return d.Query(render.QuerySpec{Schema: schema, Order: "attr:start_date", Limit: 10, Page: 1, Filters: fs})
	}
	fromToday := render.AttrFilter{Attr: "start_date", Op: "from_today"}
	wantExpiry := 23 * time.Hour // 01:00 on June 11 in Auckland to midnight

	r := q(fromToday)
	if names(r) != "today,tomorrow,later" || r.Total != 3 {
		t.Errorf("from_today = %s total %d, want today,tomorrow,later", names(r), r.Total)
	}
	if r.ExpiresIn != wantExpiry {
		t.Errorf("ExpiresIn = %v, want %v", r.ExpiresIn, wantExpiry)
	}
	if r := q(fromToday, render.AttrFilter{Attr: "start_date", Op: "next_days", Days: 1}); names(r) != "today,tomorrow" {
		t.Errorf("next_days 1 = %s", names(r))
	}
	if r := q(fromToday, render.AttrFilter{Attr: "status", Op: "ne", Text: "cancelled"}); names(r) != "today,tomorrow" {
		t.Errorf("ne cancelled = %s", names(r))
	}
	if r := q(render.AttrFilter{Attr: "start_date", Op: "before_today"}); names(r) != "yesterday" {
		t.Errorf("before_today = %s", names(r))
	}
	empty := q(render.AttrFilter{Attr: "start_date", Op: "next_days", Days: 0}, render.AttrFilter{Attr: "status", Op: "eq", Text: "nope"})
	if len(empty.Items) != 0 || empty.ExpiresIn != wantExpiry {
		t.Errorf("empty result = %s expires %v, want none and %v", names(empty), empty.ExpiresIn, wantExpiry)
	}
	fixed := q(render.AttrFilter{Attr: "status", Op: "eq", Text: "scheduled"})
	if names(fixed) != "yesterday,today,tomorrow" || fixed.ExpiresIn != 0 {
		t.Errorf("fixed filter = %s expires %v, want no expiry", names(fixed), fixed.ExpiresIn)
	}
}
