//go:build integration

package agents

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// Funnel + pages + spoofed SQL against a database migrated through 013:
// DATABASE_URL=... go test -tags=integration ./internal/agents -run StatsRepository
func TestStatsRepository_Integration(t *testing.T) {
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

	marker := "it-" + uuid.NewString()[:8]
	aiSource := marker // unique rollup key so other rows in the DB do not skew counts
	schemaID, entityID, neverID := uuid.New(), uuid.New(), uuid.New()
	path := "/" + marker
	today := time.Now().UTC().Truncate(24 * time.Hour)
	t.Cleanup(func() {
		p := db.Pool()
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE path = $1`, path)
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE agent_id = $1`, marker)
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_analytics_daily WHERE key = $1`, aiSource)
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = ANY($1)`, []uuid.UUID{entityID, neverID})
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})

	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, $2, $2, '{}')`, schemaID, marker)
	exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, published_at) VALUES ($1,$2,'Fetched',$3,'published',now()), ($4,$2,'Never',$5,'published',now())`,
		entityID, schemaID, marker+"-a", neverID, marker+"-b")

	// Rollup: 10 AI hits (6 verified, 1 spoofed, 1 unverifiable, 2 pending) on the entity,
	// 3 hits on a 404, 4 from a non-AI purpose (must not count as AI).
	day := today.AddDate(0, 0, -2)
	exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, entity_id, hits, verified_hits, spoofed_hits, unverifiable_hits)
		VALUES ($1,$2,$3,200,'OpenAI','training',$4,10,6,1,1),
		       ($1,$2,$5,404,'OpenAI','training',NULL,3,0,0,3),
		       ($1,$2,'/seo','200','Ahrefs','seo',NULL,4,0,0,4)`, day, marker, path, entityID, path+"-gone")
	// Old rollup day outside the range must not leak in.
	exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, hits) VALUES ($1,$2,$3,200,'OpenAI','training',99)`,
		today.AddDate(0, 0, -200), marker, path)

	// Analytics rollup: 3 AI-referred visits today.
	exec(`INSERT INTO hatamoto_analytics_daily (day, kind, key, count) VALUES ($1,'ai_referral',$2,3)`, today, aiSource)

	// Spoofed hit with a full address: the API must only ever see the /24.
	ip := netip.MustParseAddr("203.0.113.77")
	if err := NewRepository(db).InsertHits(ctx, []Hit{{
		At: time.Now().UTC(), AgentID: marker, Operator: "OpenAI", Purpose: PurposeTraining,
		Verification: VerificationSpoofed, IP: &ip, Method: "GET", Path: path, Status: 200, Source: SourceBackend,
	}}); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE hatamoto_agent_hits SET verification = 'spoofed' WHERE path = $1`, path)

	repo := NewStatsRepository(db)
	rg := Range{From: today.AddDate(0, 0, -29), To: today}

	t.Run("overview", func(t *testing.T) {
		tot, err := repo.Overview(ctx, rg)
		if err != nil {
			t.Fatal(err)
		}
		// Other agents' rows may exist in a shared DB: assert on deltas via the agent view instead.
		if tot.AIVisits < 3 {
			t.Errorf("ai referrals=%d, want >=3", tot.AIVisits)
		}
		agent, err := repo.AgentDetail(ctx, rg, marker)
		if err != nil || !agent.Found {
			t.Fatalf("detail: %v found=%v", err, agent.Found)
		}
		// 10 + 3 training + 4 seo + 1 hit written via InsertHits today = 18 in range; 99 out of range.
		if agent.Totals.Hits != 18 {
			t.Errorf("agent hits in range = %d, want 18", agent.Totals.Hits)
		}
		if agent.Totals.Verified != 6 || agent.Totals.Spoofed != 1 || agent.Totals.Unverifiable != 8 {
			t.Errorf("verification split = %d/%d/%d", agent.Totals.Verified, agent.Totals.Spoofed, agent.Totals.Unverifiable)
		}
		// Served paths in range: path (200, plus the InsertHits row on the same path); the 404 and the
		// out-of-range day do not count; /seo is served (200) and counts.
		if agent.PagesRead != 2 {
			t.Errorf("pages read = %d, want 2", agent.PagesRead)
		}
		if agent.LastSeenAt == nil {
			t.Error("last seen nil")
		}
		series, err := repo.Series(ctx, rg, "operator")
		if err != nil || len(series) == 0 {
			t.Fatalf("series: %v len=%d", err, len(series))
		}
		for _, s := range series {
			if s.Key == "" {
				t.Errorf("empty series key: %+v", s)
			}
		}
	})

	t.Run("pages", func(t *testing.T) {
		top, total, err := repo.TopPages(ctx, rg, Page{Limit: 500})
		if err != nil || total == 0 {
			t.Fatalf("top pages: %v total=%d", err, total)
		}
		var found bool
		rowsForPath := 0
		for _, p := range top {
			if p.Path == path {
				rowsForPath++
			}
			if p.Path == path && p.EntityID != nil && *p.EntityID == entityID {
				found = true
				// 10 rollup hits with the entity + 1 InsertHits row without: one path, one row.
				if p.Hits != 11 || p.EntityName == nil || *p.EntityName != "Fetched" {
					t.Errorf("page row = %+v", p)
				}
			}
			if p.Path == path && p.EntityID != nil && (p.SchemaSlug == nil || *p.SchemaSlug != marker) {
				t.Errorf("schema slug = %v, want %s", p.SchemaSlug, marker)
			}
			if p.Path == path+"-gone" {
				t.Error("404 path must not be in top fetched")
			}
		}
		if rowsForPath != 1 {
			t.Errorf("path appears %d times in top pages, want 1", rowsForPath)
		}
		if !found {
			t.Error("fetched entity page missing")
		}
		never, _, err := repo.NeverFetched(ctx, Page{Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		var sawNever, sawFetched bool
		for _, n := range never {
			sawNever = sawNever || n.EntityID == neverID
			sawFetched = sawFetched || n.EntityID == entityID
		}
		if !sawNever || sawFetched {
			t.Errorf("never-fetched: never=%v fetched=%v", sawNever, sawFetched)
		}
	})

	t.Run("published pages ignore drafts and variants", func(t *testing.T) {
		before, err := repo.PublishedPages(ctx)
		if err != nil || before < 2 {
			t.Fatalf("published pages = %d, err %v, want >= 2 (the seeded entities)", before, err)
		}
		draftID, variantID := uuid.New(), uuid.New()
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = ANY($1)`, []uuid.UUID{draftID, variantID})
		})
		exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1,$2,'Draft',$3,'draft')`, draftID, schemaID, marker+"-d")
		exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, variant_key, published_at) VALUES ($1,$2,'Variant',$3,'published','v',now())`, variantID, schemaID, marker+"-v")
		after, err := repo.PublishedPages(ctx)
		if err != nil || after != before {
			t.Errorf("published pages after draft+variant = %d (err %v), want %d", after, err, before)
		}
	})

	t.Run("by page type", func(t *testing.T) {
		pts, err := repo.FetchesBySchema(ctx, rg)
		if err != nil {
			t.Fatal(err)
		}
		var typed, other int64
		for _, p := range pts {
			switch p.Key {
			case marker:
				typed += p.Hits
			case otherPagesSlug:
				other += p.Hits
			}
		}
		// 10 served rollup hits on the entity; the 404 and the seo row do not count.
		if typed != 10 || other < 1 {
			t.Errorf("typed=%d other=%d, want 10 and >=1 (the entity-less InsertHits row)", typed, other)
		}
		never, err := repo.NeverFetchedBySchema(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		for _, c := range never {
			if c.SchemaSlug != nil && *c.SchemaSlug == marker {
				n = c.Count
			}
		}
		if n != 1 {
			t.Errorf("never fetched of seeded type = %d, want 1", n)
		}
	})

	t.Run("health series", func(t *testing.T) {
		before, err := repo.HealthSeries(ctx, rg)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, hits) VALUES
			($1,$2,'/h200',200,'OpenAI','training',5), ($1,$2,'/h301',301,'OpenAI','training',4),
			($1,$2,'/h404',404,'OpenAI','training',3), ($1,$2,'/h500',500,'OpenAI','training',2),
			($1,$2,'/h0',0,'OpenAI','training',1), ($1,$2,'/hs404',404,'Google','search',7)`, today.AddDate(0, 0, -3), marker)
		after, err := repo.HealthSeries(ctx, rg)
		if err != nil {
			t.Fatal(err)
		}
		at := func(rows []HealthDay) (h HealthDay) {
			for _, r := range rows {
				if r.Day == today.AddDate(0, 0, -3).Format(dayFmt) {
					h = r
				}
			}
			return h
		}
		b, a := at(before), at(after)
		// Status 0 is in Hits but in no class; the AI counts leave out the search crawler's 404.
		if a.Hits-b.Hits != 22 || a.S2xx-b.S2xx != 5 || a.S3xx-b.S3xx != 4 || a.S4xx-b.S4xx != 10 || a.S5xx-b.S5xx != 2 ||
			a.AI4xx-b.AI4xx != 3 || a.AI5xx-b.AI5xx != 2 {
			t.Errorf("health delta = %+v from %+v", a, b)
		}
	})

	t.Run("errors", func(t *testing.T) {
		rows, _, err := repo.Errors(ctx, rg, Page{Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		var ok bool
		for _, e := range rows {
			if e.Path == path+"-gone" && e.Status == 404 && e.Hits == 3 && len(e.AgentIDs) == 1 {
				ok = true
			}
		}
		if !ok {
			t.Errorf("404 row missing: %+v", rows)
		}
	})

	t.Run("spoofed returns the network, not the address", func(t *testing.T) {
		rows, total, err := repo.Spoofed(ctx, rg, Page{Limit: 500})
		if err != nil || total == 0 {
			t.Fatalf("spoofed: %v total=%d", err, total)
		}
		var ok bool
		for _, s := range rows {
			if s.Path == path {
				ok = s.IPMasked != nil && *s.IPMasked == "203.0.113.0/24"
				if !ok {
					t.Errorf("ip_masked = %v, want 203.0.113.0/24", s.IPMasked)
				}
			}
		}
		if !ok {
			t.Error("spoofed hit missing")
		}
	})
}

// First-crawl store + entity activity SQL: DATABASE_URL=... go test -tags=integration ./internal/agents -run EntityActivity
func TestEntityActivity_Integration(t *testing.T) {
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
	marker := "it-" + uuid.NewString()[:8]
	schemaID, entityID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	published := now.Add(-5 * time.Hour)
	t.Cleanup(func() {
		p := db.Pool()
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_agent_hits WHERE entity_id = $1`, entityID)
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE entity_id = $1`, entityID)
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, entityID) // cascades first_crawl
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, $2, $2, '{}')`, schemaID, marker)
	exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, published_at) VALUES ($1,$2,'E',$3,'published',$4)`,
		entityID, schemaID, marker, published)

	fc := NewFirstCrawlRepository(db)
	pub, inserted, err := fc.RecordFirstCrawl(ctx, entityID, "gptbot", now)
	if err != nil || !inserted || pub == nil || pub.Sub(published).Abs() > time.Second {
		t.Fatalf("first insert: pub=%v inserted=%v err=%v", pub, inserted, err)
	}
	if _, inserted, err := fc.RecordFirstCrawl(ctx, entityID, "claudebot", now.Add(time.Hour)); err != nil || inserted {
		t.Fatalf("second insert: inserted=%v err=%v", inserted, err)
	}
	if _, inserted, err := fc.RecordFirstCrawl(ctx, uuid.New(), "gptbot", now); err != nil || inserted {
		t.Fatalf("unknown entity must be a no-op: inserted=%v err=%v", inserted, err)
	}

	// user_fetch: 3 today (7d), 5 twenty days ago (30d only), 7 fifty days ago (neither); 404s never count.
	exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, entity_id, hits) VALUES
		($1,'chatgpt-user','/p',200,'OpenAI','user_fetch',$2,3),
		($3,'chatgpt-user','/p',200,'OpenAI','user_fetch',$2,5),
		($4,'chatgpt-user','/p',200,'OpenAI','user_fetch',$2,7),
		($1,'chatgpt-user','/p',404,'OpenAI','user_fetch',$2,9)`,
		today, entityID, today.AddDate(0, 0, -20), today.AddDate(0, 0, -50))
	// gptbot: only in the rollup (hits aged out); claudebot: a raw hit newer than its rollup day.
	exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, entity_id, hits) VALUES
		($1,'gptbot','/p',200,'OpenAI','training',$2,1), ($1,'claudebot','/p',200,'Anthropic','training',$2,1)`,
		today.AddDate(0, 0, -100), entityID)
	if err := NewRepository(db).InsertHits(ctx, []Hit{{At: now, AgentID: "claudebot", Operator: "Anthropic", Purpose: PurposeTraining,
		Verification: VerificationPending, Method: "GET", Path: "/p", EntityID: &entityID, Status: 200, Source: SourceMiddleware}}); err != nil {
		t.Fatal(err)
	}

	d, err := NewStatsRepository(db).EntityActivity(ctx, entityID, []string{"gptbot", "claudebot", "perplexitybot"}, today)
	if err != nil || !d.Exists {
		t.Fatalf("activity: %v exists=%v", err, d.Exists)
	}
	if d.UserFetch7d != 3 || d.UserFetch30d != 8 {
		t.Errorf("user fetches 7d=%d 30d=%d, want 3 and 8", d.UserFetch7d, d.UserFetch30d)
	}
	if d.FirstAt == nil || d.PublishedAt == nil {
		t.Error("first crawl not loaded")
	}
	if d.TrackingFrom == nil {
		t.Error("tracking start (migration 013 row) not loaded")
	}
	if lf := d.Last["gptbot"]; lf.At.Before(today.AddDate(0, 0, -101)) || lf.At.After(today.AddDate(0, 0, -99)) {
		t.Errorf("gptbot should fall back to the rollup day: %v", lf.At)
	}
	if lf := d.Last["claudebot"]; lf.At.Sub(now).Abs() > time.Second {
		t.Errorf("claudebot should use the raw hit: %v", lf.At)
	}
	if _, ok := d.Last["perplexitybot"]; ok {
		t.Error("never-fetched agent must be absent")
	}
	if d2, err := NewStatsRepository(db).EntityActivity(ctx, uuid.New(), nil, today); err != nil || d2.Exists {
		t.Errorf("unknown entity: exists=%v err=%v", d2.Exists, err)
	}
}

// Robots policy store: replace semantics, and the table starts empty
// (STOP-ROBOTS: never seeded). DATABASE_URL=... go test -tags=integration ./internal/agents -run RobotsPolicy
func TestRobotsPolicy_Integration(t *testing.T) {
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

	repo := NewRobotsRepository(db)
	// Preserve whatever policy the target DB had and restore it afterwards.
	orig, err := repo.DeniedPurposes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.SetDenied(ctx, orig) })

	if err := repo.SetDenied(ctx, []Purpose{PurposeTraining, PurposeSEO}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.DeniedPurposes(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("after save: %v %v", got, err)
	}
	if err := repo.SetDenied(ctx, []Purpose{PurposeSEO}); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.DeniedPurposes(ctx); len(got) != 1 || got[0] != PurposeSEO {
		t.Fatalf("replace: %v", got)
	}
	if err := repo.SetDenied(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.DeniedPurposes(ctx); len(got) != 0 {
		t.Fatalf("clear: %v", got)
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_agent_robots_policy`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after clear = %d (%v)", n, err)
	}
}

// First-crawl buckets and per-agent counts cover the whole range, not a page.
// A far-away range keeps other rows in a shared database out of the counts.
func TestFirstCrawlAggregates(t *testing.T) {
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
	marker := "it-" + uuid.NewString()[:8]
	schemaID := uuid.New()
	ids := make([]uuid.UUID, 6)
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = ANY($1)`, ids) // cascades first_crawl
		_, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, $2, $2, '{}')`, schemaID, marker)

	first := time.Date(2001, 2, 3, 12, 0, 0, 0, time.UTC)
	// published this long before the first crawl; nil = no publish time; -1h = published after the crawl.
	lags := []*time.Duration{dur(time.Hour), dur(48 * time.Hour), dur(5 * 24 * time.Hour), dur(9 * 24 * time.Hour), nil, dur(-time.Hour)}
	agentOf := []string{"a", "a", "a", "b", "b", "b"}
	for i := range ids {
		ids[i] = uuid.New()
		exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, published_at) VALUES ($1,$2,'E',$3,'published',now())`,
			ids[i], schemaID, marker+"-"+string(rune('a'+i)))
		var pub *time.Time
		if lags[i] != nil {
			p := first.Add(-*lags[i])
			pub = &p
		}
		exec(`INSERT INTO hatamoto_agent_first_crawl (entity_id, agent_id, first_at, published_at) VALUES ($1,$2,$3,$4)`,
			ids[i], marker+"-"+agentOf[i], first, pub)
	}

	rg := Range{From: time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC), To: time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)}
	got, err := NewStatsRepository(db).FirstCrawlAggregates(ctx, rg)
	if err != nil {
		t.Fatal(err)
	}
	if got.SameDay != 1 || got.D1to3 != 1 || got.D4to7 != 1 || got.D8Plus != 1 || got.NotRecorded != 2 {
		t.Errorf("buckets = %+v, want 1/1/1/1 and 2 not recorded", got)
	}
	if len(got.ByAgent) != 2 || got.ByAgent[0].Count != 3 || got.ByAgent[1].Count != 3 || got.ByAgent[0].AgentID != marker+"-a" {
		t.Errorf("by agent = %+v", got.ByAgent)
	}
}

func dur(d time.Duration) *time.Duration { return &d }
