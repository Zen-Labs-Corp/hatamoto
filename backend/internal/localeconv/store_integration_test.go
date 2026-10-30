//go:build integration

package localeconv

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/migrations"
)

// scratchPool migrates a new database next to DATABASE_URL and returns a pool
// on it, so a test may convert "the whole site" without touching other data.
func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_lc_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name

	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_ = db.Close()

	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testService(pool *pgxpool.Pool) Service {
	return NewService(pool, slog.New(slog.DiscardHandler))
}

// uid maps a fixture id to a stable uuid.
func uid(id string) string { return uuid.NewSHA1(uuid.NameSpaceOID, []byte(id)).String() }

// seedFixture writes fixtureSnap into the database: the page schema gets the
// region overrides, the rows keep their order (parents first).
func seedFixture(t *testing.T, pool *pgxpool.Pool, snap *Snapshot) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Example', 'en') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatalf("seed site config: %v", err)
	}
	for _, s := range snap.Schemas {
		if _, err := pool.Exec(ctx, `
			INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, settings)
			VALUES ($1, $2, $3, '{"type":"object"}', $4::jsonb)
			ON CONFLICT (slug) DO UPDATE SET settings = EXCLUDED.settings`,
			uid(s.ID), s.Name, s.Slug, string(s.Settings)); err != nil {
			t.Fatalf("seed schema %s: %v", s.Slug, err)
		}
	}
	for _, e := range snap.Entities {
		var parent *string
		if e.ParentID != "" {
			p := uid(e.ParentID)
			parent = &p
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes, parent_id, created_at, updated_at, published_at)
			VALUES ($1, (SELECT id FROM hatamoto_entity_schemas WHERE slug = $2), $3, $3, $4, $5::jsonb, $6, $7, $7, $8)`,
			uid(e.ID), e.Schema, e.Slug, e.Status, string(e.Attrs), parent, e.CreatedAt, e.PublishedAt); err != nil {
			t.Fatalf("seed entity %s: %v", e.ID, err)
		}
	}
}

// dump is every row the conversion may write, as JSON text, for byte-for-byte comparisons.
func dump(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var entities, schemas, site string
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(e) ORDER BY e.id), '[]')::text FROM hatamoto_entities e`).Scan(&entities); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.id), '[]')::text FROM hatamoto_entity_schemas s`).Scan(&schemas); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(c)::text FROM hatamoto_site_config c WHERE id = TRUE`).Scan(&site); err != nil {
		t.Fatal(err)
	}
	return entities + "\n" + schemas + "\n" + site
}

type rowState struct {
	Locale, Slug, Parent, Group string
	UpdatedAt                   time.Time
}

func rowsByID(t *testing.T, pool *pgxpool.Pool) map[string]rowState {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id::text, locale, slug, COALESCE(parent_id::text, ''), COALESCE(translation_group::text, ''), updated_at
		  FROM hatamoto_entities`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]rowState{}
	for rows.Next() {
		var id string
		var r rowState
		if err := rows.Scan(&id, &r.Locale, &r.Slug, &r.Parent, &r.Group, &r.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		out[id] = r
	}
	return out
}

// publicURLs maps every published entity's public path to its id, computed
// from the stored rows by the URL snapshot (the router's own path code).
// A region home is served at the bare locale path, as loadHomepage does.
func publicURLs(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	ctx := context.Background()
	snap, err := loadSnapshot(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	urls, err := entity.BuildURLSnapshot(entitySchemas(snap.Schemas), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	var entries []entity.LocaleEntry
	for _, l := range snap.Site.Locales {
		entries = append(entries, entity.LocaleEntry{Tag: l.Tag, Segment: l.Segment})
	}
	set, err := entity.NewLocaleSet(snap.Site.Language, entries, snap.Site.PrefixAll, snap.Site.XDefault)
	if err != nil {
		t.Fatal(err)
	}
	urls = urls.WithLocaleSet(set)

	byID := map[string]*Ent{}
	for i := range snap.Entities {
		byID[snap.Entities[i].ID] = &snap.Entities[i]
	}
	out := map[string]string{}
	for i := range snap.Entities {
		e := &snap.Entities[i]
		if !e.published() {
			continue
		}
		var chain []string
		for p := byID[e.ParentID]; p != nil; p = byID[p.ParentID] {
			chain = append([]string{p.Slug}, chain...)
		}
		path := pathOf(urls, e, e.Flat(), State{Locale: e.Locale, Slug: e.Slug}, chain)
		if e.Slug == "home" && e.ParentID == "" && e.Locale != entity.LocaleShared && set.Multi() {
			path = urls.WithLocale(e.Locale, "/")
		}
		out[path] = e.ID
	}
	return out
}

func mustApply(t *testing.T, svc Service, plan *Plan) (*Record, *Report) {
	t.Helper()
	rec, rep, err := svc.Apply(context.Background(), plan, ApplyOpts{By: "cli"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return rec, rep
}

// idsOf returns the uuids of fixture ids.
func idsOf(ids ...string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = uid(id)
	}
	return out
}

func TestApplyConvertsTheFixture(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	snap := fixtureSnap()
	seedFixture(t, pool, snap)
	svc := testService(pool)
	plan := fixturePlan(t)

	beforeURLs := publicURLs(t, pool)
	beforeRows := rowsByID(t, pool)

	rep, err := svc.DryRun(ctx, plan)
	if err != nil || !rep.OK || rep.PathChanges.Count != 0 {
		t.Fatalf("dry run = %+v, %v", rep, err)
	}
	rec, applied := mustApply(t, svc, plan)
	if rec.ID == 0 || rec.Status != "applied" || rec.AppliedBy != "cli" || applied.DataFingerprint != rep.DataFingerprint {
		t.Errorf("record = %+v", rec)
	}

	// Every previously public URL still resolves to the same entity.
	afterURLs := publicURLs(t, pool)
	for path, id := range beforeURLs {
		if afterURLs[path] != id {
			t.Errorf("%s served %q after the conversion, want %q", path, afterURLs[path], id)
		}
	}

	after := rowsByID(t, pool)
	want := map[string]struct{ locale, slug, parent string }{
		"x0": {"en-XX", "home", ""}, "x1": {"en-XX", "about", ""}, "x2": {"en-XX", "team", "x1"},
		"x3": {"en-XX", "bio", "x2"}, "x4": {"en-XX", "home-old-1", ""},
		"y0": {"en-XA", "home", ""}, "y1": {"en-XA", "about", ""}, "y2": {"en-XA", "solo", ""},
		"z0": {"en-ZZ", "home", ""}, "z1": {"en-ZZ", "about", ""}, "z2": {"en-ZZ", "team", ""},
		"p1": {"und", "news", ""},
	}
	for id, w := range want {
		got := after[uid(id)]
		wp := ""
		if w.parent != "" {
			wp = uid(w.parent)
		}
		if got.Locale != w.locale || got.Slug != w.slug || got.Parent != wp {
			t.Errorf("%s = %+v, want %+v", id, got, w)
		}
		if !got.UpdatedAt.Equal(beforeRows[uid(id)].UpdatedAt) {
			t.Errorf("%s updated_at changed: import mode must keep it", id)
		}
	}
	// Translation groups: one uuid per value, copies share it, singletons and shared rows none.
	g := func(id string) string { return after[uid(id)].Group }
	if g("x1") == "" || g("x1") != g("y1") || g("y1") != g("z1") || g("x2") == "" || g("x2") != g("z2") || g("x1") == g("x2") {
		t.Errorf("groups: about %q %q %q, team %q %q", g("x1"), g("y1"), g("z1"), g("x2"), g("z2"))
	}
	if g("y2") != "" || g("p1") != "" {
		t.Errorf("singleton %q and shared %q must have no group", g("y2"), g("p1"))
	}

	var language, locales, xdefault string
	var prefixAll bool
	if err := pool.QueryRow(ctx, `SELECT language, locales::text, prefix_all, x_default_locale FROM hatamoto_site_config`).
		Scan(&language, &locales, &prefixAll, &xdefault); err != nil {
		t.Fatal(err)
	}
	if language != "en-XX" || !prefixAll || xdefault != "en-XX" ||
		locales != `[{"tag": "en-XX", "segment": "xx"}, {"tag": "en-XA", "segment": "yy"}, {"tag": "en-ZZ", "segment": "zz"}]` {
		t.Errorf("settings = %q %s %v %q", language, locales, prefixAll, xdefault)
	}

	// Only sitemap_group goes from the overrides; the pattern and the match stay.
	var settings string
	if err := pool.QueryRow(ctx, `SELECT settings::text FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(settings, "sitemap_group") || strings.Count(settings, `"url_pattern": "/{path}"`) != 3 ||
		!strings.Contains(settings, `"attribute": "region"`) || !strings.Contains(settings, `"equals": "yy"`) {
		t.Errorf("page settings = %s", settings)
	}

	// The record holds exactly the rows that changed.
	var n, grouped int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(after_group) FROM hatamoto_locale_conversion_rows WHERE conversion_id = $1`, rec.ID).Scan(&n, &grouped); err != nil {
		t.Fatal(err)
	}
	if n != rec.Rows || n != len(snap.Entities) || grouped != 5 {
		t.Errorf("recorded %d rows (%d grouped), record says %d, fixture has %d", n, grouped, rec.Rows, len(snap.Entities))
	}
}

func TestApplyRollsBackAsAWhole(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	seedFixture(t, pool, fixtureSnap())
	svc := testService(pool)
	before := dump(t, pool)

	for _, step := range []string{stepLocale, stepRename, stepHomes, stepReparent, stepSchemas, stepSettings, stepRecord} {
		_, _, err := svc.Apply(ctx, fixturePlan(t), ApplyOpts{By: "cli", failAfter: step})
		if !errors.Is(err, errInjectedFailure) {
			t.Fatalf("after %s: err = %v, want the injected failure", step, err)
		}
		if got := dump(t, pool); got != before {
			t.Fatalf("after a failure following %s the database changed", step)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_locale_conversions`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("after %s: %d conversion records (%v)", step, n, err)
		}
	}
	// And the same plan still applies afterwards.
	mustApply(t, svc, fixturePlan(t))
}

func TestApplyBlockingErrorsChangeNothing(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		mutate func(*Snapshot)
		plan   func(*Plan)
		code   string
	}{
		"published collision": {mutate: func(s *Snapshot) { s.Entities[4].Status = "published" }, code: CodePublishedCollision},
		"two in one locale": {mutate: func(s *Snapshot) {
			s.Entities = append(s.Entities, ent("x6", "page", "about-2", "x0", "published", `{"region":"xx","key":"g-about"}`))
		}, code: CodeGroupTwoInLocale},
		"path change": {plan: func(p *Plan) { p.Locales[0].Segment = "ex" }, code: CodePathChanges},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			pool := scratchPool(t)
			snap := fixtureSnap()
			if c.mutate != nil {
				c.mutate(snap)
			}
			seedFixture(t, pool, snap)
			before := dump(t, pool)
			var mut []func(*Plan)
			if c.plan != nil {
				mut = append(mut, c.plan)
			}
			rec, rep, err := testService(pool).Apply(ctx, fixturePlan(t, mut...), ApplyOpts{By: "cli"})
			var be *BlockingError
			if !errors.As(err, &be) || rec != nil || rep == nil || !hasCode(be.Issues, c.code) {
				t.Fatalf("err = %v, want a BlockingError with %s", err, c.code)
			}
			if dump(t, pool) != before {
				t.Error("a refused apply changed the database")
			}
		})
	}
}

func TestApplyRefusesAMissingTriggerGuard(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	seedFixture(t, pool, fixtureSnap())
	// The body from before the multi-locale migration: no two-locale guard.
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION public.entity_locale_retag() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RETURN NULL; END $$`); err != nil {
		t.Fatal(err)
	}
	before := dump(t, pool)
	svc := testService(pool)

	rep, err := svc.DryRun(ctx, fixturePlan(t))
	if err != nil || rep.OK || !hasCode(rep.Errors, CodeTriggerGuardMissing) {
		t.Fatalf("dry run = %+v, %v", rep.Errors, err)
	}
	_, _, err = svc.Apply(ctx, fixturePlan(t), ApplyOpts{By: "cli"})
	var be *BlockingError
	if !errors.As(err, &be) || !hasCode(be.Issues, CodeTriggerGuardMissing) {
		t.Fatalf("apply err = %v", err)
	}
	if dump(t, pool) != before {
		t.Error("a refused apply changed the database")
	}
}

func TestDryRunWritesNothingAndStaleFingerprintRefuses(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	seedFixture(t, pool, fixtureSnap())
	svc := testService(pool)
	before := dump(t, pool)

	rep, err := svc.DryRun(ctx, fixturePlan(t))
	if err != nil || !rep.OK || rep.DataFingerprint == "" {
		t.Fatalf("dry run = %+v, %v", rep, err)
	}
	if dump(t, pool) != before {
		t.Fatal("a dry run changed the database")
	}

	// An editor saves a row the conversion touches after the dry run.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_entities SET updated_at = updated_at + interval '1 minute' WHERE id = $1`, uid("x1")); err != nil {
		t.Fatal(err)
	}
	before = dump(t, pool)
	_, _, err = svc.Apply(ctx, fixturePlan(t), ApplyOpts{By: "admin", Fingerprint: rep.DataFingerprint})
	var be *BlockingError
	if !errors.As(err, &be) || len(be.Issues) != 1 || be.Issues[0].Code != CodeStaleReport {
		t.Fatalf("err = %v, want stale_report", err)
	}
	if dump(t, pool) != before {
		t.Error("a stale apply changed the database")
	}
	// A fresh dry run's fingerprint is accepted.
	rep, _ = svc.DryRun(ctx, fixturePlan(t))
	if _, _, err := svc.Apply(ctx, fixturePlan(t), ApplyOpts{By: "admin", Fingerprint: rep.DataFingerprint}); err != nil {
		t.Fatalf("apply with the current fingerprint: %v", err)
	}
}

func TestApplyWaitsForEditorsAndGivesUp(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	seedFixture(t, pool, fixtureSnap())
	before := dump(t, pool)

	// An editor's transaction holds a row write for longer than lock_timeout.
	editor, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = editor.Rollback(ctx) }()
	if _, err := editor.Exec(ctx, `UPDATE hatamoto_entities SET name = 'edited' WHERE id = $1`, uid("p1")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, _, err = testService(pool).Apply(ctx, fixturePlan(t), ApplyOpts{By: "cli"})
	if err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("err = %v, want a lock timeout", err)
	}
	if d := time.Since(start); d < 4*time.Second || d > 15*time.Second {
		t.Errorf("gave up after %v, want about 5s", d)
	}
	_ = editor.Rollback(ctx)
	if dump(t, pool) != before {
		t.Error("a refused apply changed the database")
	}
}

func TestSuggestAndDryRunOnTheDatabase(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	seedFixture(t, pool, fixtureSnap())
	svc := testService(pool)
	before := dump(t, pool)

	plan, _, err := svc.Suggest(ctx)
	if err != nil || len(plan.Locales) != 3 || plan.LocaleFrom.Attribute != "region" {
		t.Fatalf("suggest = %+v, %v", plan, err)
	}
	if rep, err := svc.DryRun(ctx, plan); err != nil || !rep.OK {
		t.Errorf("the suggested plan does not dry-run cleanly: %+v %v", rep, err)
	}
	if dump(t, pool) != before {
		t.Error("suggest and dry run changed the database")
	}
}
