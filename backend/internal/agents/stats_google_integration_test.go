//go:build integration

package agents

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// The Google view over a seeded rollup. Data sits on days in 2020 so the
// shared database's other rows stay out of the range.
func TestGoogleStats_Integration(t *testing.T) {
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

	marker := "it-google-" + uuid.NewString()[:8]
	schemaID := uuid.New()
	crawled, stale, never, spoofOnly, errPage, flip := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	day1, day2 := time.Date(2020, 3, 10, 0, 0, 0, 0, time.UTC), time.Date(2020, 3, 11, 0, 0, 0, 0, time.UTC)
	rg := Range{From: day1, To: day2}
	pathOf := func(s string) string { return "/" + marker + "-" + s }
	var trackingStart time.Time
	if err := db.Pool().QueryRow(ctx, `SELECT started_at FROM hatamoto_agent_search_tracking`).Scan(&trackingStart); err != nil {
		t.Fatal(err)
	}
	var aiTrackingStart time.Time
	if err := db.Pool().QueryRow(ctx, `SELECT started_at FROM hatamoto_agent_tracking`).Scan(&aiTrackingStart); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p := db.Pool()
		_, _ = p.Exec(ctx, `UPDATE hatamoto_agent_search_tracking SET started_at = $1`, trackingStart)
		_, _ = p.Exec(ctx, `UPDATE hatamoto_agent_tracking SET started_at = $1`, aiTrackingStart)
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_agent_daily WHERE path LIKE $1 OR day IN ($2, $3)`, "/"+marker+"%", day1, day2)
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID) // cascades first crawl rows
		_, _ = p.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, $2, $2, '{}')`, schemaID, marker)
	// stale was edited on 2020-03-12, after its last crawl on 03-11; crawled on 03-11 stays older.
	exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, published_at, updated_at) VALUES
		($1,$6,'Crawled',$7,'published','2020-03-01','2020-03-09'),
		($2,$6,'Stale',$8,'published','2020-03-01','2020-03-12 08:00:00+00'),
		($3,$6,'Never',$9,'published','2020-03-01','2020-03-01'),
		($4,$6,'SpoofOnly',$10,'published','2020-03-01','2020-03-01'),
		($5,$6,'ErrPage',$11,'published','2020-03-01','2020-03-01'),
		($12,$6,'Flip',$13,'published','2020-03-01','2020-03-01')`,
		crawled, stale, never, spoofOnly, errPage, schemaID,
		marker+"-1", marker+"-2", marker+"-3", marker+"-4", marker+"-5", flip, marker+"-6")

	type row struct {
		day               time.Time
		agent, path       string
		status            int
		entity            any
		hits, ver, spoofd int64
	}
	var seed []row
	add := func(r row) { seed = append(seed, r) }
	add(row{day1, "googlebot", pathOf("crawled"), 200, crawled, 5, 5, 0})
	add(row{day2, "googlebot", pathOf("crawled"), 200, crawled, 3, 2, 1}) // last day: 200 (+404 below)
	add(row{day2, "googlebot", pathOf("crawled"), 404, crawled, 1, 1, 0}) // highest status of the last day
	add(row{day2, "googlebot", pathOf("stale"), 200, stale, 4, 4, 0})
	add(row{day1, "googlebot", pathOf("spoof"), 200, spoofOnly, 6, 0, 6}) // forged: not crawled
	add(row{day2, "googlebot", pathOf("gone"), 404, nil, 7, 7, 0})        // not an entity: Errors
	add(row{day1, "googlebot", pathOf("home-ish"), 200, nil, 2, 2, 0})
	add(row{day1, "googlebot", "/robots.txt", 200, nil, 9, 9, 0}) // a file: not a page
	add(row{day1, "googlebot", pathOf("err"), 500, errPage, 2, 2, 0})
	// A served page that starts failing: the error row has no entity (only served
	// hits are attributed), the path is the same.
	add(row{day1, "googlebot", pathOf("flip"), 200, flip, 2, 2, 0})
	add(row{day2, "googlebot", pathOf("flip"), 500, nil, 1, 1, 0})
	add(row{day2, "bingbot", pathOf("crawled"), 200, crawled, 50, 50, 0}) // other engine
	for _, r := range seed {
		purpose, op := "search", "Google"
		if r.agent == "bingbot" {
			op = "Microsoft"
		}
		exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, entity_id, hits, verified_hits, spoofed_hits)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, r.day, r.agent, r.path, r.status, op, purpose, r.entity, r.hits, r.ver, r.spoofd)
	}
	// Today's AI row must stay out of the Google queries and join SeriesWithSearch.
	exec(`INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, hits) VALUES ($1,'gptbot',$2,200,'OpenAI','training',11)`, day1, pathOf("ai"))

	repo := NewStatsRepository(db)

	t.Run("SeriesWithSearch", func(t *testing.T) {
		pts, err := repo.SeriesWithSearch(ctx, Range{From: day1, To: day1}, "purpose")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, p := range pts {
			got[p.Key] += p.Hits
		}
		if got["search"] != 5+6+2+9+2+2 || got["training"] != 11 {
			t.Errorf("series = %v, want search %d training 11", got, 5+6+2+9+2+2)
		}
		ai, err := repo.Series(ctx, Range{From: day1, To: day1}, "purpose")
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ai {
			if p.Key == "search" {
				t.Errorf("Series (AI only) returned search: %+v", p)
			}
		}
	})

	t.Run("status series counts every hit", func(t *testing.T) {
		pts, err := repo.GooglebotStatusSeries(ctx, rg)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, p := range pts {
			got[p.Day+" "+p.Key] = p.Hits
		}
		want := map[string]int64{
			"2020-03-10 2xx": 5 + 6 + 2 + 9 + 2, "2020-03-10 5xx": 2,
			"2020-03-11 2xx": 3 + 4, "2020-03-11 4xx": 1 + 7, "2020-03-11 5xx": 1,
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
			}
		}
		if len(got) != len(want) {
			t.Errorf("series = %v", got)
		}
	})

	t.Run("KPIs", func(t *testing.T) {
		exec(`INSERT INTO hatamoto_agent_search_first_crawl (entity_id, agent_id, first_at, published_at) VALUES
			($1,'googlebot','2020-03-10 10:00:00+00','2020-03-10 04:00:00+00'),
			($2,'googlebot','2020-03-10 20:00:00+00','2020-03-10 04:00:00+00'),
			($3,'googlebot','2020-03-11 10:00:00+00','2020-03-10 04:00:00+00'),
			($4,'bingbot','2020-03-10 05:00:00+00','2020-03-10 04:00:00+00')`, crawled, stale, errPage, never)
		exec(`UPDATE hatamoto_agent_search_tracking SET started_at = '2020-01-01 00:00:00+00'`)
		k, err := repo.GoogleKPIs(ctx, rg)
		if err != nil {
			t.Fatal(err)
		}
		wantHits := int64(5 + 3 + 1 + 4 + 6 + 7 + 2 + 9 + 2 + 2 + 1)
		if k.Hits != wantHits || k.Verified != wantHits-1-6 {
			t.Errorf("hits/verified = %d/%d, want %d/%d", k.Hits, k.Verified, wantHits, wantHits-7)
		}
		var published int
		if err := db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_entities e WHERE `+publishedFilter).Scan(&published); err != nil {
			t.Fatal(err)
		}
		if k.Published != published {
			t.Errorf("published = %d, want %d", k.Published, published)
		}
		// crawled, stale, flip: served and genuine. spoofOnly: forged. errPage: only a 500. never: none.
		if k.PagesCrawled != 3 {
			t.Errorf("pages crawled = %d, want 3", k.PagesCrawled)
		}
		// Googlebot deltas 6 h, 16 h, 30 h (the Bing row is another agent): median 16 h.
		if k.MedianFirstCrawl == nil || *k.MedianFirstCrawl != 16*time.Hour {
			t.Errorf("median = %v, want 16h", k.MedianFirstCrawl)
		}
		// The AI first-crawl tracking date plays no part: only the search one does.
		exec(`UPDATE hatamoto_agent_tracking SET started_at = '2030-01-01'`)
		if k, err = repo.GoogleKPIs(ctx, rg); err != nil || k.MedianFirstCrawl == nil || *k.MedianFirstCrawl != 16*time.Hour {
			t.Errorf("median with a late AI tracking start = %v (%v), want 16h", k.MedianFirstCrawl, err)
		}
		if at, err := repo.SearchTrackingStart(ctx); err != nil || at == nil || !at.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("search tracking start = %v (%v)", at, err)
		}
		// A page published before tracking began has no latency.
		exec(`UPDATE hatamoto_agent_search_tracking SET started_at = '2020-03-10 12:00:00+00'`)
		if k, err = repo.GoogleKPIs(ctx, rg); err != nil || k.MedianFirstCrawl != nil {
			t.Errorf("median before tracking = %v (%v), want nil", k.MedianFirstCrawl, err)
		}
	})

	byKey := func(rows []GooglePageRow) map[string]GooglePageRow {
		m := map[string]GooglePageRow{}
		for _, r := range rows {
			if r.EntityID != nil {
				m[r.Name] = r
			} else {
				m[r.Path] = r
			}
		}
		return m
	}
	list := func(filter string) map[string]GooglePageRow {
		t.Helper()
		rows, total, err := repo.GooglePages(ctx, rg, filter, 1000, 0)
		if err != nil {
			t.Fatal(err)
		}
		if total != len(rows) {
			t.Errorf("%s: total %d, rows %d", filter, total, len(rows))
		}
		return byKey(rows)
	}

	t.Run("all", func(t *testing.T) {
		m := list(GoogleFilterAll)
		c := m["Crawled"]
		if c.Crawls != 5+2+1 || c.LastDay == nil || !c.LastDay.Equal(day2) || c.LastStatus != 404 || c.Stale || c.Path != pathOf("crawled") {
			t.Errorf("crawled = %+v", c)
		}
		if s := m["Stale"]; s.Crawls != 4 || !s.Stale || s.LastStatus != 200 {
			t.Errorf("stale = %+v", s)
		}
		if n := m["Never"]; n.Crawls != 0 || n.LastDay != nil || n.Path != "" || n.Stale || n.LastStatus != 0 {
			t.Errorf("never = %+v", n)
		}
		if s := m["SpoofOnly"]; s.Crawls != 0 || s.LastDay != nil {
			t.Errorf("a forged-only page must look uncrawled: %+v", s)
		}
		if g := m[pathOf("gone")]; g.EntityID != nil || g.Crawls != 7 || g.LastStatus != 404 {
			t.Errorf("gone path = %+v", g)
		}
		if _, ok := m["/robots.txt"]; ok {
			t.Error("robots.txt listed as a page")
		}
		if _, ok := m[pathOf("spoof")]; ok {
			t.Error("a spoof-only fetch listed as a bare path")
		}
	})
	t.Run("not-crawled", func(t *testing.T) {
		m := list(GoogleFilterNotCrawled)
		for _, name := range []string{"Never", "SpoofOnly"} {
			if _, ok := m[name]; !ok {
				t.Errorf("%s missing from not-crawled", name)
			}
		}
		for _, name := range []string{"Crawled", "Stale", "ErrPage", pathOf("gone")} {
			if _, ok := m[name]; ok {
				t.Errorf("%s listed as not crawled", name)
			}
		}
	})
	t.Run("errors", func(t *testing.T) {
		m := list(GoogleFilterErrors)
		for _, name := range []string{"Crawled", "ErrPage", "Flip", pathOf("gone")} {
			if _, ok := m[name]; !ok {
				t.Errorf("%s missing from errors", name)
			}
		}
		// Flip: a 200 day then a 500 day of an entity-less row on the same path is
		// one row, with the 500, never a second bare-path row.
		if f := m["Flip"]; f.Crawls != 3 || f.LastStatus != 500 || f.LastDay == nil || !f.LastDay.Equal(day2) {
			t.Errorf("flip = %+v, want 3 crawls, last status 500 on day 2", f)
		}
		if _, ok := m[pathOf("flip")]; ok {
			t.Error("flip's failing path listed again as a bare path")
		}
		if len(m) != 4 {
			t.Errorf("errors = %v, want crawled, errpage, flip, gone", m)
		}
	})
	t.Run("stale", func(t *testing.T) {
		m := list(GoogleFilterStale)
		if _, ok := m["Stale"]; !ok || len(m) != 1 {
			t.Errorf("stale = %v, want only Stale", m)
		}
	})
	t.Run("paging", func(t *testing.T) {
		first, total, err := repo.GooglePages(ctx, rg, GoogleFilterErrors, 2, 0)
		if err != nil || total != 4 || len(first) != 2 {
			t.Fatalf("page 1: %d rows, total %d (%v)", len(first), total, err)
		}
		rest, _, err := repo.GooglePages(ctx, rg, GoogleFilterErrors, 2, 2)
		if err != nil || len(rest) != 2 {
			t.Fatalf("page 2: %d rows (%v)", len(rest), err)
		}
		if _, _, err := repo.GooglePages(ctx, rg, "bogus", 10, 0); err == nil {
			t.Error("an unknown filter was accepted")
		}
	})
	t.Run("search first crawl", func(t *testing.T) {
		at, err := repo.SearchFirstCrawl(ctx, crawled)
		if err != nil || at == nil || !at.Equal(time.Date(2020, 3, 10, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("first crawl = %v (%v)", at, err)
		}
		if at, err := repo.SearchFirstCrawl(ctx, spoofOnly); err != nil || at != nil {
			t.Errorf("no row: %v (%v)", at, err)
		}
	})
}
