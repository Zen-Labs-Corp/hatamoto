//go:build integration

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/render"
)

// The popular_7|30|90 orders rank published entities by landing pageviews of
// the daily rollup, fill with the newest and fail open.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run Popular
func TestRenderDataPopular_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set - skipping popular integration test")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "pop-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Popular IT")
	t.Setenv("PUBLIC_BASE_URL", "https://pop-it.example.test")
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

	// A multi-locale site: entities are shared (und), so "/pp/x" and "/fr/pp/x" are one entity.
	var prev struct {
		name, lang, xdef string
		locales          []byte
		prefixAll        bool
	}
	hadSite := db.Pool().QueryRow(ctx, `SELECT company_name, language, locales, prefix_all, x_default_locale FROM hatamoto_site_config`).
		Scan(&prev.name, &prev.lang, &prev.locales, &prev.prefixAll, &prev.xdef) == nil
	exec(`INSERT INTO hatamoto_site_config (company_name, language, locales, prefix_all) VALUES ('Popular IT', 'en', $1::jsonb, false)
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = EXCLUDED.locales, prefix_all = false, homepage_entity_id = NULL`,
		`[{"tag":"en","segment":""},{"tag":"fr","segment":"fr"}]`)

	sfx := "x" + uuid.NewString()[:5]
	post, other, empty, bound, bulk := "pp-"+sfx, "oo-"+sfx, "ee-"+sfx, "bb-"+sfx, "kk-"+sfx
	var schemaIDs []uuid.UUID
	for _, s := range []string{post, other, empty, bound, bulk} {
		id := uuid.New()
		schemaIDs = append(schemaIDs, id)
		exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
			id, s, `{"url_pattern":"/`+s+`/{slug}"}`)
	}
	t.Cleanup(func() {
		c := context.Background()
		if hadSite {
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_site_config SET locales=$1::jsonb, prefix_all=$2, x_default_locale=$3`, string(prev.locales), prev.prefixAll, prev.xdef)
		} else {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		for _, id := range schemaIDs {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, id)
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, id)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_traffic_daily WHERE dim='landing' AND k1 LIKE '%-`+sfx+`/%'`)
	})
	ids := map[string]uuid.UUID{}
	seed := func(schemaID uuid.UUID, slug, status string, ageDays int) {
		id := uuid.New()
		ids[slug] = id
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at)
			VALUES ($1,$2,$3,$3,'und',$4, now() - make_interval(days => $5))`, id, schemaID, slug, status, ageDays)
	}
	// p1 is the newest, p5 the oldest.
	seed(schemaIDs[0], "p1", "published", 1)
	seed(schemaIDs[0], "p2", "published", 2)
	seed(schemaIDs[0], "p3", "published", 3)
	seed(schemaIDs[0], "p4", "published", 4)
	seed(schemaIDs[0], "p5", "published", 5)
	seed(schemaIDs[0], "draft", "draft", 0)
	seed(schemaIDs[1], "o1", "published", 0)
	seed(schemaIDs[2], "e1", "published", 1)
	seed(schemaIDs[2], "e2", "published", 2)

	// One clock for the seeded rows and the code under test (newData injects it),
	// so a UTC midnight in between cannot move the window.
	clock := time.Now().UTC()
	today := clock.Truncate(24 * time.Hour)
	day := func(ago int) time.Time { return today.AddDate(0, 0, -ago) }
	row := func(ago int, k string, pv int64) analytics.TrafficRow {
		return analytics.TrafficRow{Day: day(ago), Dim: analytics.DimLanding, K1: k, Counts: analytics.Counts{Visits: 1, Pageviews: pv}}
	}
	// Window edges: a row `days` ago is out, `days-1` ago is in.
	var edge []analytics.TrafficRow
	for _, age := range []int{6, 7, 29, 30, 89, 90} {
		slug := fmt.Sprintf("b%d", age)
		seed(schemaIDs[3], slug, "published", 1)
		edge = append(edge, row(age, "/"+bound+"/"+slug, 4))
	}
	// 120 entities: more than the 100 rows a popular listing serves.
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at)
		SELECT gen_random_uuid(), $1, 'k'||g, 'k'||g, 'und', 'published', now() - make_interval(days => g) FROM generate_series(1,120) g`, schemaIDs[4])
	p := "/" + post + "/"
	if err := analytics.NewPostgresTrafficRepository(db).AddTraffic(ctx, append(edge, []analytics.TrafficRow{
		row(0, p+"p1", 5), row(1, p+"p1/", 3), row(1, "/fr"+p+"p1", 2), // slash and locale variants of one entity: 10
		row(6, p+"p2", 6), row(6, p+"p5", 6), // tie: p2 is newer
		row(20, p+"p3", 7), row(60, p+"p4", 9), // only in the 30 and 90 day windows
		row(0, p+"draft", 100), row(0, "/"+other+"/o1", 100), row(0, analytics.KeyOther, 100), row(0, "/", 100),
	}...)); err != nil {
		t.Fatal(err)
	}

	newData := func() *renderData {
		// A fresh server per case: the URL registry caches the locale set.
		d := newRenderData(New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil))))
		d.now = func() time.Time { return clock }
		return d
	}
	names := func(res render.QueryResult) []string {
		var out []string
		for _, it := range res.Items {
			out = append(out, it.Name)
		}
		return out
	}
	d := newData()
	var calls atomic.Int32
	d.landingSource = func(ctx context.Context, from, to time.Time, limit int) ([]analytics.TrafficTop, error) {
		calls.Add(1)
		return analytics.NewReader(db).TopLanding(ctx, from, to, limit)
	}
	query := func(schema, order string, limit, page int) render.QueryResult {
		return d.Query(render.QuerySpec{Schema: schema, Order: order, Limit: limit, Page: page})
	}

	for _, tt := range []struct {
		order string
		want  []string
	}{
		{"popular_7", []string{"p1", "p2", "p5", "p3", "p4"}},  // ranked, then the newest fill
		{"popular_30", []string{"p1", "p3", "p2", "p5", "p4"}}, // p4 only fills
		{"popular_90", []string{"p1", "p4", "p3", "p2", "p5"}},
	} {
		got := query(post, tt.order, 10, 1)
		if !slices.Equal(names(got), tt.want) || got.Total != 5 {
			t.Errorf("%s = %v (total %d), want %v: drafts, other schemas, (other) and / never rank", tt.order, names(got), got.Total, tt.want)
		}
	}
	if got := names(query(post, "popular_7", 3, 1)); !slices.Equal(got, []string{"p1", "p2", "p5"}) {
		t.Errorf("limit 3 = %v", got)
	}
	if got := query(post, "popular_30", 2, 2); !slices.Equal(names(got), []string{"p2", "p5"}) || got.Pages != 3 {
		t.Errorf("page 2 = %v (pages %d)", names(got), got.Pages)
	}
	if got := names(query(post, "popular_30", 2, 3)); !slices.Equal(got, []string{"p4"}) {
		t.Errorf("page 3 = %v", got)
	}
	if got := names(query(empty, "popular_30", 5, 1)); !slices.Equal(got, []string{"e1", "e2"}) {
		t.Errorf("schema without views = %v, want the newest", got)
	}
	// Three schemas, two windows' worth of memoised rankings: 3 (days) x 1 + 1 (30) for empty = 4 reads.
	if n := calls.Load(); n != 4 {
		t.Errorf("rollup reads = %d, want 4 (one per days+schema per minute)", n)
	}
	if d.Failures() != 0 {
		t.Errorf("failures = %d", d.Failures())
	}

	for _, tt := range []struct {
		days int
		want []string
	}{
		{7, []string{"b6"}},
		{30, []string{"b29", "b6", "b7"}},
		{90, []string{"b29", "b30", "b6", "b7", "b89"}},
	} {
		bd := newData()
		got, err := bd.rankedPopular(ctx, bound, tt.days)
		if err != nil {
			t.Fatal(err)
		}
		var have []string
		for _, id := range got {
			for n, v := range ids {
				if v == id {
					have = append(have, n)
				}
			}
		}
		slices.Sort(have)
		if !slices.Equal(have, tt.want) {
			t.Errorf("window %d = %v, want %v (days ago is out, days-1 is in)", tt.days, have, tt.want)
		}
	}

	// Paging stops at the 100 rows served: no pages past them, whatever the offset.
	for _, tt := range []struct {
		limit, page, offset int
		total, pages, page2 int
		items               int
	}{
		{12, 1, 0, 100, 9, 1, 12},
		{12, 25, 0, 100, 9, 9, 4}, // past the end shows the last page
		{12, 1, 50, 100, 5, 1, 12},
		{12, 1, 100, 100, 1, 1, 0}, // offset at the cap: nothing, one page
	} {
		got := newData().Query(render.QuerySpec{Schema: bulk, Order: "popular_30", Limit: tt.limit, Page: tt.page, Offset: tt.offset})
		if got.Total != tt.total || got.Pages != tt.pages || got.Page != tt.page2 || len(got.Items) != tt.items {
			t.Errorf("bulk %+v = total %d pages %d page %d items %d", tt, got.Total, got.Pages, got.Page, len(got.Items))
		}
	}

	// One failing path lookup loses that path only, but the page is marked
	// uncacheable and the partial ranking is not memoised.
	one := newData()
	reads := 0
	one.landingSource = func(context.Context, time.Time, time.Time, int) ([]analytics.TrafficTop, error) {
		reads++
		return []analytics.TrafficTop{
			{K1: "/" + post + "/bad\x00", Pageviews: 50}, // NUL: the lookup errors
			{K1: "/" + post + "/p5", Pageviews: 5},
		}, nil
	}
	spec := render.QuerySpec{Schema: post, Order: "popular_30", Limit: 2, Page: 1}
	if got := names(one.Query(spec)); !slices.Equal(got, []string{"p5", "p1"}) || one.Failures() == 0 {
		t.Errorf("bad path = %v, failures %d, want p5 first and failures > 0", got, one.Failures())
	}
	one.Query(spec)
	if reads != 2 {
		t.Errorf("rollup read %d times, want 2 (partial ranking not memoised)", calls)
	}

	// An expired context stops the resolving and is a failure too.
	exp := newData()
	exp.landingSource = one.landingSource
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ids, err := exp.rankedPopular(ctx, post, 30); err != nil || len(ids) != 0 || exp.Failures() == 0 {
		t.Errorf("cancelled = %v, %v, failures %d, want none, nil, > 0", ids, err, exp.Failures())
	}

	// A failing rollup falls back to the newest and marks the page uncacheable.
	bad := newData()
	bad.landingSource = func(context.Context, time.Time, time.Time, int) ([]analytics.TrafficTop, error) {
		return nil, errors.New("rollup down")
	}
	got := bad.Query(render.QuerySpec{Schema: post, Order: "popular_30", Limit: 3, Page: 1})
	if !slices.Equal(names(got), []string{"p1", "p2", "p3"}) || bad.Failures() != 1 {
		t.Errorf("failure = %v, failures %d, want newest and 1", names(got), bad.Failures())
	}
}
