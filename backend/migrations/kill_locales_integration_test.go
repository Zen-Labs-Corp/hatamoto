//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/migrations"
)

const killLocalesVersion = 202610151230

// scratchDB creates an empty database next to DATABASE_URL and returns its
// connection and DSN.
func scratchDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	return scratchDBWith(t, "")
}

// scratchDBUTF8 is a scratch database that is UTF8 whatever the cluster's
// template1 encoding is (a dev cluster may be SQL_ASCII, where left() counts bytes).
func scratchDBUTF8(t *testing.T) (*sql.DB, string) {
	t.Helper()
	return scratchDBWith(t, " ENCODING 'UTF8' TEMPLATE template0")
}

func scratchDBWith(t *testing.T, createOpts string) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "hm_kl_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+createOpts); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, u.String()
}

func provider(t *testing.T, db *sql.DB) *goose.Provider {
	t.Helper()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A fresh install keeps the new defaults: nothing is pinned, the tables are gone.
func TestKillLocales_FreshInstall(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var settings sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT settings::text FROM hatamoto_entity_schemas WHERE slug = 'post'`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(settings.String, "url_pattern") {
		t.Errorf("a fresh install must use the built-in post pattern, got settings %s", settings.String)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_redirects`).Scan(&n); err != nil || n != 0 {
		t.Errorf("fresh install redirects = %d (%v), want 0", n, err)
	}
	// Down then Up again must work (structure only).
	p := provider(t, db)
	if _, err := p.DownTo(ctx, killLocalesVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// The prod snapshot (testdata) migrates to one site, one language.
func TestKillLocales_ProdSnapshot(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, killLocalesVersion-1); err != nil {
		t.Fatalf("up to the previous migration: %v", err)
	}
	seed, err := os.ReadFile("testdata/prodlike_pre_v1_09.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(seed)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	scalar := func(q string, args ...any) string {
		t.Helper()
		var s sql.NullString
		if err := db.QueryRowContext(ctx, q, args...).Scan(&s); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return s.String
	}

	for _, tbl := range []string{"hatamoto_locales", "hatamoto_entity_locale_overrides"} {
		if got := scalar(`SELECT to_regclass('public.' || $1)::text`, tbl); got != "" {
			t.Errorf("table %s still exists", tbl)
		}
	}
	if got := scalar(`SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'hatamoto_entities' AND column_name IN ('locale_code', 'locale_scope')`); got != "0" {
		t.Errorf("entity locale columns left: %s", got)
	}
	if got := scalar(`SELECT count(*) FROM pg_indexes WHERE indexdef ~* 'locale_(code|scope)|hatamoto_locales|entity_locale_overrides'`); got != "0" {
		t.Errorf("indexes still mention locales: %s", got)
	}

	// The default locale's details moved into the site config, once.
	for q, want := range map[string]string{
		`SELECT language FROM hatamoto_site_config`:                   "en-AU",
		`SELECT phone FROM hatamoto_site_config`:                      "+61 2 5550 0100",
		`SELECT business_hours FROM hatamoto_site_config`:             "Mon-Fri 9:00-17:30 AEST",
		`SELECT postal_address->>'country' FROM hatamoto_site_config`: "AU",
		`SELECT timezone FROM hatamoto_site_config`:                   "Australia/Sydney",
		`SELECT legacy_wp_redirects::text FROM hatamoto_site_config`:  "false",
	} {
		if got := scalar(q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}

	// One redirect pair per previously active locale; owner redirects stay as they are.
	if got := scalar(`SELECT count(*) FROM hatamoto_redirects WHERE source_tag = 'v1-09-kill-locales'`); got != "20" {
		t.Errorf("redirects added = %s, want 20", got)
	}
	if got := scalar(`SELECT target_url FROM hatamoto_redirects WHERE source_path = '/au/**'`); got != "/$1" {
		t.Errorf("/au/** -> %q", got)
	}
	if got := scalar(`SELECT target_url FROM hatamoto_redirects WHERE source_path = '/au'`); got != "/" {
		t.Errorf("/au -> %q", got)
	}
	// Owner redirects are never switched off: /platform stays a redirect to /,
	// and /au/platform reaches it in two hops.
	if got := scalar(`SELECT is_active::text || ' ' || target_url FROM hatamoto_redirects WHERE source_path = '/platform'`); got != "true /" {
		t.Errorf("cutover redirect /platform = %q, want it active and unchanged", got)
	}
	if got := scalar(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/au/**' AND is_active`); got != "1" {
		t.Errorf("/au/** inactive or missing")
	}

	// Existing post URLs keep their pattern; other settings survive.
	if got := scalar(`SELECT settings->>'url_pattern' || ' ' || (settings->>'archive_path') || ' ' || (settings->>'sitemap_group') || ' ' || (settings->>'archive_title')
		FROM hatamoto_entity_schemas WHERE slug = 'post'`); got != "/post/{slug} /post post Insights" {
		t.Errorf("post settings = %q", got)
	}
	if got := scalar(`SELECT settings->>'url_pattern' FROM hatamoto_entity_schemas WHERE slug = 'page'`); got != "" {
		t.Errorf("page keeps the built-in pattern, got %q", got)
	}
}

// Rows that would collide once the locale is gone are archived, not lost; an
// existing redirect on a locale path wins over the generated one.
func TestKillLocales_ConflictsAreResolvedWithoutLoss(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, killLocalesVersion-1); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO hatamoto_locales (code, country_code, language_code, name, is_active, is_default)
		VALUES ('au','AU','en-AU','Australia',true,true), ('nz','NZ','en-NZ','New Zealand',true,false)`)
	exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Conflicts')`)
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at, locale_scope, locale_code)
		SELECT id, 'About', 'about', 'published', now(), 'locale_specific', NULL FROM hatamoto_entity_schemas WHERE slug = 'page'`)
	exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at, locale_scope, locale_code)
		SELECT id, 'About NZ', 'about', 'published', now(), 'locale_specific', 'nz' FROM hatamoto_entity_schemas WHERE slug = 'page'`)
	exec(`INSERT INTO hatamoto_redirects (source_path, target_url) VALUES ('/nz', '/somewhere')`)

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var slug, status string
	if err := db.QueryRowContext(ctx, `SELECT slug, status FROM hatamoto_entities WHERE name = 'About NZ'`).Scan(&slug, &status); err != nil {
		t.Fatal(err)
	}
	if slug != "about-nz" || status != "archived" {
		t.Errorf("colliding locale row = %s/%s, want about-nz/archived", slug, status)
	}
	var target string
	if err := db.QueryRowContext(ctx, `SELECT target_url FROM hatamoto_redirects WHERE source_path = '/nz'`).Scan(&target); err != nil || target != "/somewhere" {
		t.Errorf("existing /nz redirect changed: %q (%v)", target, err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/nz/**'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("/nz/** must still be added next to an existing /nz redirect: %d", n)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_redirects WHERE source_path IN ('/au', '/au/**')`).Scan(&n); err != nil || n != 2 {
		t.Errorf("/au redirects = %d, want 2", n)
	}
}

// scenario runs the migration over a hand-built pre-v1-09 database.
type scenario struct {
	t   *testing.T
	db  *sql.DB
	ctx context.Context
}

func newScenario(t *testing.T) *scenario {
	t.Helper()
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, killLocalesVersion-1); err != nil {
		t.Fatal(err)
	}
	sc := &scenario{t: t, db: db, ctx: ctx}
	sc.exec(`INSERT INTO hatamoto_locales (code, country_code, language_code, name, is_active, is_default)
		VALUES ('au','AU','en-AU','Australia',true,true), ('nz','NZ','en-NZ','New Zealand',true,false), ('de','DE','de','Germany',false,false)`)
	sc.exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Scenario')`)
	return sc
}

func (sc *scenario) exec(q string, args ...any) {
	sc.t.Helper()
	if _, err := sc.db.ExecContext(sc.ctx, q, args...); err != nil {
		sc.t.Fatalf("%s: %v", q, err)
	}
}

func (sc *scenario) up() {
	sc.t.Helper()
	if _, err := provider(sc.t, sc.db).Up(sc.ctx); err != nil {
		sc.t.Fatalf("up: %v", err)
	}
}

func (sc *scenario) scalar(q string, args ...any) string {
	sc.t.Helper()
	var s sql.NullString
	if err := sc.db.QueryRowContext(sc.ctx, q, args...).Scan(&s); err != nil {
		sc.t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

// page inserts a page entity; locale "" is the locale-neutral row.
func (sc *scenario) page(name, slug, status, locale string) {
	sc.t.Helper()
	sc.exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at, locale_scope, locale_code)
		SELECT id, $1, $2, $3, now(), 'locale_specific', NULLIF($4, '') FROM hatamoto_entity_schemas WHERE slug = 'page'`,
		name, slug, status, locale)
}

func (sc *scenario) entity(name string) string {
	sc.t.Helper()
	return sc.scalar(`SELECT slug || '/' || status FROM hatamoto_entities WHERE name = $1`, name)
}

// A published row wins over a draft whatever its locale; between equals the
// default locale's row (what /<default>/x served) wins over the neutral row.
func TestKillLocales_CollisionWinner(t *testing.T) {
	sc := newScenario(t)
	// Draft neutral + published default-locale row: the live page must stay.
	sc.page("A neutral draft", "about", "draft", "")
	sc.page("A au live", "about", "published", "au")
	// Published neutral + published default-locale row: the default locale wins.
	sc.page("B neutral", "team", "published", "")
	sc.page("B au", "team", "published", "au")
	sc.page("B nz", "team", "published", "nz")
	// Published neutral beats a scheduled default-locale row.
	sc.page("C neutral", "jobs", "published", "")
	sc.page("C au scheduled", "jobs", "scheduled", "au")
	// The target slug of a rename is taken already: pick a free suffix.
	sc.page("D taken", "faq-nz", "published", "")
	sc.page("D neutral", "faq", "published", "")
	sc.page("D nz", "faq", "published", "nz")
	// A retired locale's live row is archived even when it does not collide.
	sc.page("E de", "impressum", "published", "de")
	sc.up()

	for name, want := range map[string]string{
		"A au live": "about/published", "A neutral draft": "about-neutral/draft",
		"B au": "team/published", "B neutral": "team-neutral/archived", "B nz": "team-nz/archived",
		"C neutral": "jobs/published", "C au scheduled": "jobs-au/archived",
		"D neutral": "faq/published", "D nz": "faq-nz-2/archived", "D taken": "faq-nz/published",
		"E de": "impressum/archived",
	} {
		if got := sc.entity(name); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	// Every move is logged.
	if got := sc.scalar(`SELECT count(*) FROM hatamoto_migration_notes WHERE kind = 'entity_slug_collision'`); got != "5" {
		t.Errorf("collision notes = %s, want 5", got)
	}
	if got := sc.scalar(`SELECT count(*) FROM hatamoto_migration_notes WHERE kind = 'entity_archived_inactive_locale'`); got != "1" {
		t.Errorf("inactive-locale notes = %s, want 1", got)
	}
}

// Redirects set up by the owner are never switched off, and their {locale} and
// /<code> targets lose the locale segment; a rule that would loop is switched
// off, an inserted one first.
func TestKillLocales_RedirectsKeepOwnerRules(t *testing.T) {
	sc := newScenario(t)
	sc.page("Platform", "platform", "published", "au")
	sc.exec(`INSERT INTO hatamoto_redirects (source_path, target_url, status_code) VALUES
		('/platform', '/', 301),
		('/old/**', '/{locale}/new/$1', 301),
		('/seo', '/au/seo', 301),
		('/a', '/au/b', 301), ('/b', '/a', 301),
		('/campaign', '/au', 302),
		('/nz', '/somewhere', 301)`)
	sc.up()

	for src, want := range map[string]string{
		"/platform": "true /", "/old/**": "true /new/$1", "/campaign": "true /",
		"/nz": "true /somewhere",
	} {
		if got := sc.scalar(`SELECT is_active::text || ' ' || target_url FROM hatamoto_redirects WHERE source_path = $1`, src); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
	// /seo -> /au/seo became /seo -> /seo: a self-loop, switched off.
	if got := sc.scalar(`SELECT is_active::text || ' ' || target_url FROM hatamoto_redirects WHERE source_path = '/seo'`); got != "false /seo" {
		t.Errorf("/seo = %q", got)
	}
	// /a -> /au/b became /a -> /b, and /b -> /a closes the loop: the rewritten rule goes.
	if got := sc.scalar(`SELECT is_active::text FROM hatamoto_redirects WHERE source_path = '/a'`); got != "false" {
		t.Errorf("/a still active: %s", got)
	}
	if got := sc.scalar(`SELECT is_active::text FROM hatamoto_redirects WHERE source_path = '/b'`); got != "true" {
		t.Errorf("/b (an owner rule) was touched: %s", got)
	}
	// The prefix rules stay, and /nz/** is still added next to the owner's /nz.
	if got := sc.scalar(`SELECT count(*) FROM hatamoto_redirects WHERE source_tag = 'v1-09-kill-locales' AND is_active`); got != "3" {
		t.Errorf("active inserted rules = %s, want 3 (/au, /au/**, /nz/**; de is inactive)", got)
	}
	if got := sc.scalar(`SELECT count(*) FROM hatamoto_migration_notes WHERE kind = 'redirect_loop_deactivated'`); got != "2" {
		t.Errorf("loop notes = %s, want 2", got)
	}
	if got := sc.scalar(`SELECT count(*) FROM hatamoto_migration_notes WHERE kind = 'redirect_target_rewritten'`); got != "4" {
		t.Errorf("rewrite notes = %s, want 4", got)
	}
}

// A draft post or a page in another schema called like a locale does not stop
// the redirects, and a published page at /<code> keeps only its own source free.
func TestKillLocales_PageAtLocalePath(t *testing.T) {
	sc := newScenario(t)
	sc.page("NZ page", "nz", "published", "")
	sc.up()
	if got := sc.scalar(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/nz'`); got != "0" {
		t.Errorf("/nz redirect added over a page: %s", got)
	}
	if got := sc.scalar(`SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/nz/**'`); got != "1" {
		t.Errorf("/nz/** missing: %s", got)
	}
}

// Default-locale overrides are merged into the entity; the rest is kept in the notes.
func TestKillLocales_OverridesAreMergedAndKept(t *testing.T) {
	sc := newScenario(t)
	sc.page("Home", "home", "published", "")
	sc.exec(`INSERT INTO hatamoto_entity_locale_overrides (entity_id, locale_code, attribute_overrides, seo_overrides, slug_override, status)
		SELECT id, 'au', '{"headline":"G''day"}', '{"meta_title":"AU title","no_index":true}', 'start', 'published' FROM hatamoto_entities WHERE name = 'Home'`)
	sc.exec(`INSERT INTO hatamoto_entity_locale_overrides (entity_id, locale_code, attribute_overrides, status)
		SELECT id, 'nz', '{"headline":"Kia ora"}', 'published' FROM hatamoto_entities WHERE name = 'Home'`)
	sc.up()
	if got := sc.scalar(`SELECT attributes->>'headline' || '|' || (attributes->>'meta_title') || '|' || (attributes->>'no_index') FROM hatamoto_entities WHERE name = 'Home'`); got != "G'day|AU title|true" {
		t.Errorf("merged attributes = %q", got)
	}
	if got := sc.scalar(`SELECT details->>'slug_override' FROM hatamoto_migration_notes WHERE kind = 'override_merged_default_locale'`); got != "start" {
		t.Errorf("slug_override not kept in the notes: %q", got)
	}
	if got := sc.scalar(`SELECT details->'attribute_overrides'->>'headline' FROM hatamoto_migration_notes WHERE kind = 'override_dropped'`); got != "Kia ora" {
		t.Errorf("dropped nz override not kept: %q", got)
	}
}

// With several post schemas the owner of /post/{slug} is the first of the
// runtime's family order (post, blog, article, blog_post), not the first
// alphabetically.
func TestKillLocales_PostFamilyOwnerOrder(t *testing.T) {
	sc := newScenario(t)
	sc.exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, settings)
		VALUES (gen_random_uuid(), 'Article', 'article', '{"type":"object"}', '{"archive_title":"Articles"}')`)
	sc.exec(`INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at, locale_scope)
		SELECT id, 'A post', 'a-post', 'published', now(), 'locale_neutral' FROM hatamoto_entity_schemas WHERE slug = 'article'`)
	sc.up()
	if got := sc.scalar(`SELECT settings->>'url_pattern' FROM hatamoto_entity_schemas WHERE slug = 'post'`); got != "/post/{slug}" {
		t.Errorf("post (first in the family order) does not own the pattern: %q", got)
	}
	if got := sc.scalar(`SELECT COALESCE(settings->>'url_pattern', '') FROM hatamoto_entity_schemas WHERE slug = 'article'`); got != "" {
		t.Errorf("article was chosen as the owner: %q", got)
	}
}
