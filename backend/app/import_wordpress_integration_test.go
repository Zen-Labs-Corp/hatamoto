//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/server"
	"hatamoto.dev/core/internal/wpimport"
)

// The WordPress importer against a real, seeded WordPress (test/wpfixture):
//
//	test/wpfixture/run-local.sh up        # or docker compose up, see docs/IMPORT-WORDPRESS.md
//	set -a; . ~/.cache/hatamoto-wpfixture/site/wpfixture.env; set +a
//	DATABASE_URL=... go test -tags=integration -run WordPressFixture ./app
//
// Done when: every old URL answers 200 or exactly one 301; no wp-content URL
// remains in imported content; SEO titles match the old pages' own <title>
// 100%; a re-run creates nothing new.

// lazyProber builds the server only when the audit starts, after the import
// changed the schemas (the server caches URL patterns).
type lazyProber struct {
	build func() http.Handler
	h     http.Handler
}

func (l *lazyProber) Probe(ctx context.Context, path string) (int, string, error) {
	if l.h == nil {
		l.h = l.build()
	}
	return wpimport.HandlerProber{Handler: l.h, Host: "new-site.example.test"}.Probe(ctx, path)
}

func fixtureEnv(t *testing.T) (siteURL, user, pass string) {
	t.Helper()
	siteURL, user, pass = os.Getenv("WP_FIXTURE_URL"), os.Getenv("WP_FIXTURE_USER"), os.Getenv("WP_FIXTURE_APP_PASSWORD")
	if siteURL == "" {
		t.Skip("WP_FIXTURE_URL not set: start test/wpfixture and source its wpfixture.env")
	}
	return siteURL, user, pass
}

type counts struct{ Entities, Terms, Authors, Media, Menus, MenuItems, Redirects, Versions, MapRows int }

func countRows(t *testing.T, ctx context.Context, db *database.DB) counts {
	t.Helper()
	var c counts
	for q, dst := range map[string]*int{
		`SELECT count(*) FROM hatamoto_entities`: &c.Entities,
		`SELECT count(*) FROM hatamoto_terms`:    &c.Terms,
		`SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id=e.schema_id AND s.slug='person'`: &c.Authors,
		`SELECT count(*) FROM hatamoto_media`:           &c.Media,
		`SELECT count(*) FROM hatamoto_menus`:           &c.Menus,
		`SELECT count(*) FROM hatamoto_menu_items`:      &c.MenuItems,
		`SELECT count(*) FROM hatamoto_redirects`:       &c.Redirects,
		`SELECT count(*) FROM hatamoto_import_map`:      &c.MapRows,
		`SELECT count(*) FROM hatamoto_entity_versions`: &c.Versions,
	} {
		if err := db.Pool().QueryRow(ctx, q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func oldPageTitle(t *testing.T, link string) string {
	t.Helper()
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return wpimport.ParseHead(string(body), "html").Title
}

type wpRig struct {
	t       *testing.T
	ctx     context.Context
	db      *database.DB
	cfg     *config.Config
	deps    *wpimport.Deps
	log     bytes.Buffer
	siteURL string
	hostKey string
	// noPermalinks keeps the schemas' URL patterns, so old URLs must redirect.
	noPermalinks bool
	keepEdited   bool
}

// newWPRig migrates a scratch database, boots the first-run content and wires
// the importer services over it.
func newWPRig(t *testing.T, siteURL string) *wpRig {
	t.Helper()
	ctx := context.Background()
	dsn, pool := scratchDB(t)
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	if err := firstBoot(ctx, pool, "Fixture IT", quietLogger()); err != nil {
		t.Fatal(err)
	}
	mediaDir := t.TempDir()
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "wp-it")
	t.Setenv("HATAMOTO_SITE_NAME", "WP IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "WP IT")
	t.Setenv("PUBLIC_BASE_URL", "https://new-site.example.test")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("MEDIA_UPLOAD_DIR", mediaDir)
	t.Setenv("HATAMOTO_WORKER", "off")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	deps := wpimport.NewDeps(db, wpimport.DepsConfig{MediaDir: mediaDir, MediaBaseURL: "/media", MediaMaxMB: 10}, quietLogger())
	t.Cleanup(deps.Close)
	u, _ := url.Parse(siteURL)
	return &wpRig{t: t, ctx: ctx, db: db, cfg: cfg, deps: deps, siteURL: siteURL, hostKey: strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/")}
}

func (r *wpRig) run(dump *wpimport.Dump, audit bool) *wpimport.Result {
	r.t.Helper()
	st := wpimport.NewStore(r.db.Pool(), strings.TrimRight(dump.Site.URL, "/"))
	opts := wpimport.Options{AllowPrivate: true, NoPermalinks: r.noPermalinks, KeepEdited: r.keepEdited, Progress: func(phase, msg string) { r.log.WriteString("[" + phase + "] " + msg + "\n") }}
	if audit {
		opts.Audit = &lazyProber{build: func() http.Handler { return server.New(r.cfg, r.db, quietLogger()).Handler() }}
	}
	runner, err := wpimport.NewRunner(r.deps, st, dump, opts)
	if err != nil {
		r.t.Fatal(err)
	}
	res, err := runner.Run(r.ctx, uuid.Nil)
	if err != nil {
		r.t.Fatalf("run: %v\n%s", err, r.log.String())
	}
	return res
}

func (r *wpRig) str(q string, args ...any) string {
	r.t.Helper()
	var s string
	if err := r.db.Pool().QueryRow(r.ctx, q, args...).Scan(&s); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
	return s
}

func (r *wpRig) content(slug string) string {
	return r.str(`SELECT attributes->>'content' FROM hatamoto_entities WHERE slug=$1`, slug)
}

// assertDone checks the v1-22 done criteria after a run.
func (r *wpRig) assertDone(dump *wpimport.Dump, res *wpimport.Result, minURLs int) {
	t := r.t
	t.Helper()
	if res.Failed != 0 || len(res.Errors) != 0 {
		t.Fatalf("errors: %+v\n%s", res.Errors, r.log.String())
	}
	// Every old URL: 200 or exactly one 301.
	// Author archives of people not yet public stay 404 on purpose (Hidden).
	if a := res.Audit; a == nil || a.Checked < minURLs || len(a.Failures) != 0 {
		t.Errorf("audit: %+v", a)
	} else {
		t.Logf("audit: %d checked, %d 200, %d 301, %d hidden author pages", a.Checked, a.OK, a.Redirected, len(a.Hidden))
	}
	// No wp-content URL in any imported content.
	if len(res.WPContentLeft) != 0 {
		t.Errorf("wp-content left: %v", res.WPContentLeft)
	}
	if n := r.str(`SELECT count(*)::text FROM hatamoto_entities WHERE attributes::text LIKE '%wp-content%' OR attributes::text LIKE '%wp-includes%'`); n != "0" {
		t.Errorf("entities with wp-content: %s", n)
	}
	// SEO titles equal the old pages' own <title>, 100%.
	checked := 0
	for _, it := range dump.Items {
		if it.Status != "publish" || it.SEO.Title == "" || it.ID == fmt.Sprint(dump.Site.PostsPageID) {
			continue
		}
		want := oldPageTitle(t, it.Link)
		got := r.str(`SELECT COALESCE(attributes->>'meta_title','') FROM hatamoto_entities WHERE metadata->>'import_key' = $1`, "wp:"+r.hostKey+":"+it.ID)
		if html.UnescapeString(got) != want {
			t.Errorf("SEO title of %s: imported %q, old page %q", it.Link, got, want)
		}
		checked++
	}
	if checked < 3 {
		t.Errorf("only %d SEO titles compared", checked)
	}
	t.Logf("%d SEO titles match the old pages", checked)
}

// assertRerun runs the import again and checks nothing was added.
func (r *wpRig) assertRerun(dump *wpimport.Dump) {
	t := r.t
	t.Helper()
	before := countRows(t, r.ctx, r.db)
	res := r.run(dump, true)
	after := countRows(t, r.ctx, r.db)
	t.Logf("re-run: created=%d updated=%d failed=%d media=%d/%d", res.Created, res.Updated, res.Failed, res.MediaImported, res.MediaReused)
	if res.Created != 0 || res.Failed != 0 || res.MediaImported != 0 {
		t.Errorf("re-run created=%d failed=%d media imported=%d", res.Created, res.Failed, res.MediaImported)
	}
	before.Versions, after.Versions = 0, 0
	if before != after {
		t.Errorf("re-run changed row counts:\n before %+v\n after  %+v", before, after)
	}
	if a := res.Audit; a == nil || len(a.Failures) != 0 {
		t.Errorf("audit after re-run: %+v", a)
	}
}

func TestWordPressFixtureImport_Integration(t *testing.T) {
	siteURL, user, pass := fixtureEnv(t)
	r := newWPRig(t, siteURL)
	load := func() *wpimport.Dump {
		src, err := wpimport.NewRESTSource(wpimport.RESTOptions{
			ClientOptions: wpimport.ClientOptions{URL: siteURL, User: user, AppPassword: pass, AllowPrivate: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		d, err := src.Load(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	ctx, db := r.ctx, r.db

	// ---- scan report
	dump := load()
	rep := wpimport.Scan(dump, siteURL)
	t.Log("\n" + rep.Summary())
	if rep.SEOPlugin != "yoast" || rep.Permalink != "/%year%/%monthnum%/%postname%/" || rep.PermalinkPattern != "/{year}/{month}/{slug}" {
		t.Errorf("scan: seo=%q permalink=%q pattern=%q", rep.SEOPlugin, rep.Permalink, rep.PermalinkPattern)
	}
	if !contains(rep.Forms, "Contact Form 7") || !contains(rep.PageBuilders, "Elementor") || rep.Shortcodes["acme_pricing_table"] != 1 {
		t.Errorf("scan: forms=%v builders=%v shortcodes=%v", rep.Forms, rep.PageBuilders, rep.Shortcodes)
	}
	if rep.Private != 1 || rep.CustomTypes["portfolio"] != 2 || rep.Embeds < 2 {
		t.Errorf("scan: private=%d cpt=%v embeds=%d", rep.Private, rep.CustomTypes, rep.Embeds)
	}

	// ---- import
	res := r.run(dump, true)
	t.Logf("run 1: created=%d updated=%d failed=%d terms=%d authors=%d media=%d/%d redirects=%d links=%d",
		res.Created, res.Updated, res.Failed, res.Terms, res.Authors, res.MediaImported, res.MediaReused, res.Redirects, res.LinksRewritten)
	if res.Created == 0 || res.MediaReused != 1 { // hero-copy.jpg has the bytes of hero.jpg
		t.Errorf("created=%d media reused=%d (want 1: the duplicate upload)", res.Created, res.MediaReused)
	}
	r.assertDone(dump, res, 15)
	if h := res.Audit.Hidden; len(h) != 2 {
		t.Errorf("hidden author pages = %+v, want /author/admin/ and /author/jane/ (people are not public yet)", h)
	}

	// ---- mapping specifics
	str, content := r.str, r.content
	if s := str(`SELECT status FROM hatamoto_entities WHERE slug='members-only-roadmap'`); s != "draft" {
		t.Errorf("private post status = %s, want draft", s)
	}
	if s := str(`SELECT status FROM hatamoto_entities WHERE slug='upcoming-draft'`); s != "draft" {
		t.Errorf("draft post status = %s", s)
	}
	if s := str(`SELECT COALESCE(attributes->>'no_index','') FROM hatamoto_entities WHERE slug='hidden-notes'`); s != "true" {
		t.Errorf("noindex = %q", s)
	}
	if c := content("gallery-showcase"); strings.Count(c, "<img") != 3 || !strings.Contains(c, "/media/") {
		t.Errorf("gallery body: %s", c)
	}
	if c := content("video-roundup"); !strings.Contains(c, "youtube.com/embed/dQw4w9WgXcQ") || !strings.Contains(c, "player.vimeo.com/video/76979871") {
		t.Errorf("embeds lost: %s", c)
	}
	if c := content("contact"); strings.Contains(c, "contact-form-7") || strings.Contains(c, "wpcf7") {
		t.Errorf("form markup left: %s", c)
	}
	if c := content("hello-world"); strings.Contains(c, "<!--") {
		t.Errorf("block comments left: %s", c)
	}
	if c := content("about"); !strings.Contains(c, `href="/author/jane"`) && !strings.Contains(c, `href="/author/jane/"`) {
		t.Errorf("author link not relative: %s", c)
	}
	if c := content("local-seo-guide"); !strings.Contains(c, `data-entity-id=`) || strings.Contains(c, "127.0.0.1") {
		t.Errorf("internal links not rewritten: %s", c)
	}
	const janeQ = `SELECT e.attributes->>'%s' FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id=e.schema_id AND s.slug='person' WHERE e.slug='jane'`
	if s := str(fmt.Sprintf(janeQ, "bio")); !strings.Contains(s, "local search") {
		t.Errorf("person bio = %q", s)
	}
	if s := str(fmt.Sprintf(janeQ, "photo")); s == "" {
		t.Error("author photo not imported")
	}
	if s := str(`SELECT p.slug FROM hatamoto_terms t JOIN hatamoto_terms p ON p.id=t.parent_id WHERE t.slug='product-updates'`); s != "news" {
		t.Errorf("category parent = %q", s)
	}
	if s := str(`SELECT slug FROM hatamoto_entity_schemas WHERE slug='portfolio'`); s != "portfolio" {
		t.Errorf("cpt schema = %q", s)
	}
	if s := str(`SELECT count(*)::text FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id=e.schema_id WHERE s.slug='portfolio'`); s != "2" {
		t.Errorf("portfolio items = %s", s)
	}
	if s := str(`SELECT count(*)::text FROM hatamoto_menu_items`); s != "7" {
		t.Errorf("menu items = %s, want 7", s)
	}
	if s := str(`SELECT company_name FROM hatamoto_site_config`); s != "Acme Fixture" {
		t.Errorf("site name = %q", s)
	}
	if s := str(`SELECT COALESCE(e.slug,'') FROM hatamoto_site_config c JOIN hatamoto_entities e ON e.id=c.homepage_entity_id`); !strings.HasPrefix(s, "home") {
		t.Errorf("homepage = %q", s)
	}
	if s := str(`SELECT settings->>'url_pattern' FROM hatamoto_entity_schemas WHERE slug='post'`); s != "/{year}/{month}/{slug}" {
		t.Errorf("post pattern = %q", s)
	}

	r.assertRerun(dump)

	var runs int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_import_runs WHERE status='completed'`).Scan(&runs)
	if runs != 2 {
		t.Errorf("completed runs = %d", runs)
	}
}

// The same site through a WXR export: the file carries no sitemap, SEO titles
// only where they were customised, and fetches media from the old URLs.
func TestWordPressFixtureWXR_Integration(t *testing.T) {
	siteURL, _, _ := fixtureEnv(t)
	file := os.Getenv("WP_FIXTURE_WXR")
	if file == "" {
		t.Skip("WP_FIXTURE_WXR not set")
	}
	r := newWPRig(t, siteURL)
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dump, err := wpimport.ParseWXR(f, wpimport.WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep := wpimport.Scan(dump, "wxr")
	t.Log("\n" + rep.Summary())
	if rep.SEOPlugin != "yoast" || !contains(rep.Forms, "Contact Form 7") || !contains(rep.PageBuilders, "Elementor") || rep.Shortcodes["acme_pricing_table"] != 1 {
		t.Errorf("scan: seo=%q forms=%v builders=%v shortcodes=%v", rep.SEOPlugin, rep.Forms, rep.PageBuilders, rep.Shortcodes)
	}
	res := r.run(dump, true)
	r.assertDone(dump, res, 10)
	if c := r.content("gallery-showcase"); strings.Count(c, "<img") != 3 {
		t.Errorf("gallery body: %s", c)
	}
	if c := r.content("video-roundup"); !strings.Contains(c, "youtube.com/embed/dQw4w9WgXcQ") || !strings.Contains(c, "player.vimeo.com/video/76979871") {
		t.Errorf("embeds lost: %s", c)
	}
	if c := r.content("contact"); strings.Contains(c, "contact-form-7") {
		t.Errorf("shortcode left: %s", c)
	}
	if s := r.str(`SELECT status FROM hatamoto_entities WHERE slug='members-only-roadmap'`); s != "draft" {
		t.Errorf("private post status = %s", s)
	}
	r.assertRerun(dump)
}

// The CLI over REST: scan-only prints the report and writes no content.
func TestWordPressCLIScanOnly_Integration(t *testing.T) {
	siteURL, user, pass := fixtureEnv(t)
	r := newWPRig(t, siteURL)
	var out bytes.Buffer
	args := []string{"--url", siteURL, "--user", user, "--app-password", pass, "--allow-private-hosts", "--scan-only"}
	if err := runImportWordPress(r.ctx, r.db, r.cfg, args, quietLogger(), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"SEO plugin: yoast", "Page builders: Elementor", "Forms: Contact Form 7", "Expected fidelity"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("scan output lacks %q:\n%s", want, out.String())
		}
	}
	if n := r.str(`SELECT count(*)::text FROM hatamoto_entities WHERE metadata ? 'import_key'`); n != "0" {
		t.Errorf("scan-only imported %s entities", n)
	}
	if n := r.str(`SELECT count(*)::text FROM hatamoto_import_runs WHERE status='scanned'`); n != "1" {
		t.Errorf("scanned runs = %s", n)
	}
	// Without flags the command explains itself.
	if err := runImportWordPress(r.ctx, r.db, r.cfg, []string{}, quietLogger(), &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("no flags: %v", err)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// With the permalink structure left alone the posts move: every old post URL
// must answer exactly one 301 to the new address.
func TestWordPressFixtureRedirects_Integration(t *testing.T) {
	siteURL, user, pass := fixtureEnv(t)
	r := newWPRig(t, siteURL)
	r.noPermalinks = true
	src, err := wpimport.NewRESTSource(wpimport.RESTOptions{
		ClientOptions: wpimport.ClientOptions{URL: siteURL, User: user, AppPassword: pass, AllowPrivate: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	dump, err := src.Load(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	res := r.run(dump, true)
	r.assertDone(dump, res, 15)
	if res.Redirects < 5 || res.Audit.Redirected < 5 {
		t.Errorf("redirects=%d audited 301s=%d, want one per published post", res.Redirects, res.Audit.Redirected)
	}
	if got := r.str(`SELECT target_url FROM hatamoto_redirects WHERE source_path='/2024/05/hello-world'`); !strings.HasSuffix(got, "/hello-world") {
		t.Errorf("redirect target = %q", got)
	}
	r.assertRerun(dump)
}

// The command line and the wizard share one import lock: with it held, the
// command refuses at once, before it reads any source.
func TestWordPressCLIRefusesWhileAnotherImportRuns_Integration(t *testing.T) {
	r := newWPRig(t, "http://old.example.test")
	release, err := wpimport.AcquireImportLock(r.ctx, r.db.Pool())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = runImportWordPress(r.ctx, r.db, r.cfg, []string{"--url", "http://old.example.test", "--scan-only"}, quietLogger(), &out)
	if !errors.Is(err, wpimport.ErrImportRunning) || !strings.Contains(err.Error(), "another import is running") {
		t.Fatalf("with the lock held: %v", err)
	}
	if n := r.str(`SELECT count(*)::text FROM hatamoto_import_runs`); n != "0" {
		t.Errorf("a refused command recorded %s runs", n)
	}
	release()
	// Free again: the command gets past the lock (it then fails reaching the fake source).
	err = runImportWordPress(r.ctx, r.db, r.cfg, []string{"--url", "http://old.example.test", "--scan-only"}, quietLogger(), &out)
	if errors.Is(err, wpimport.ErrImportRunning) {
		t.Fatalf("the lock was not released: %v", err)
	}
}
