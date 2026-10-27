//go:build integration

package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/server"
	"hatamoto.dev/core/internal/wpimport"
)

// The two ways the importer treats its target (no WordPress needed):
//   - an empty site (first-boot or starter content only): the WordPress
//     permalinks, settings and home page are applied;
//   - a site with content: no URL pattern, setting or home page is changed,
//     the imported item gives way on a slug clash, and a live page always wins.

const modesSite = "https://old.example.test"

func modesDump() *wpimport.Dump {
	d0 := time.Date(2024, 5, 10, 9, 0, 0, 0, time.UTC)
	item := func(id, typ, slug, title, link string) wpimport.Item {
		return wpimport.Item{ID: id, Type: typ, Status: "publish", Slug: slug, Title: title, Content: "<p>" + title + " from WordPress</p>", Link: link, Date: d0, Modified: d0}
	}
	return &wpimport.Dump{
		Site: wpimport.Site{URL: modesSite, Title: "Old WP Site", Tagline: "WP tagline", Timezone: "Europe/Berlin", Language: "de-DE", Permalink: "/%year%/%monthnum%/%postname%/"},
		Items: []wpimport.Item{
			item("10", "post", "hello", "Hello", modesSite+"/2024/05/hello/"),
			item("11", "post", "other", "Other", modesSite+"/2024/05/other/"),
			item("20", "page", "about", "About WP", modesSite+"/about/"),
			item("21", "page", "team", "Team", modesSite+"/team/"),
		},
		OldURLs: []string{modesSite + "/2024/05/hello/", modesSite + "/about/", modesSite + "/team/"},
	}
}

func (r *wpRig) publish(schema, slug, name string, attrs map[string]any) {
	r.t.Helper()
	sc, err := r.deps.Entities.GetSchemaBySlug(r.ctx, schema)
	if err != nil {
		r.t.Fatal(err)
	}
	raw, _ := json.Marshal(attrs)
	e, err := r.deps.Entities.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: sc.ID, Name: name, Slug: slug, Attributes: raw})
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.deps.Entities.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
		r.t.Fatal(err)
	}
}

func (r *wpRig) status(path string) (int, string) {
	r.t.Helper()
	h := server.New(r.cfg, r.db, quietLogger()).Handler()
	code, loc, _ := wpimport.HandlerProber{Handler: h, Host: "new-site.example.test"}.Probe(r.ctx, path)
	return code, loc
}

func (r *wpRig) setting(q string) string { return r.str(q) }

func TestWordPressImportEmptySite_Integration(t *testing.T) {
	r := newWPRig(t, modesSite)
	// The setup wizard's starter content does not make the site "non-empty".
	r.publish("page", "about", "About", map[string]any{"starter": true, "content": "<p>Example About page</p>"})
	// Nor does a public person (the owner with an author page): people are not content.
	if sc, err := r.deps.Entities.GetSchemaBySlug(r.ctx, "person"); err != nil {
		t.Fatal(err)
	} else if e, err := r.deps.Entities.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: sc.ID, Name: "Site Owner", Slug: "site-owner",
		Attributes: json.RawMessage(`{"public": true, "photo": "/media/owner.jpg"}`)}); err != nil {
		t.Fatal(err)
	} else if e.Status != entity.StatusPublished {
		t.Fatalf("a public person is %s, want published", e.Status)
	}
	st, err := wpimport.InspectSite(r.ctx, r.db.Pool(), wpimport.KeyPrefix(modesSite))
	if err != nil || st.Mode != wpimport.ModeEmpty || st.Starter != 2 || st.Existing != 0 {
		t.Fatalf("state = %+v, %v (want empty: first-boot home + starter About)", st, err)
	}

	res := r.run(modesDump(), true)
	if res.SiteMode != wpimport.ModeEmpty || res.Report.SiteMode != wpimport.ModeEmpty {
		t.Fatalf("mode = %q / %q", res.SiteMode, res.Report.SiteMode)
	}
	if res.Failed != 0 || len(res.Errors) != 0 {
		t.Fatalf("errors: %+v", res.Errors)
	}
	if got := r.setting(`SELECT settings->>'url_pattern' FROM hatamoto_entity_schemas WHERE slug='post'`); got != "/{year}/{month}/{slug}" {
		t.Errorf("post pattern = %q, want the WordPress permalinks", got)
	}
	if got := r.setting(`SELECT settings->>'url_pattern' FROM hatamoto_entity_schemas WHERE slug='page'`); got != "/{path}" {
		t.Errorf("page pattern = %q", got)
	}
	if got := r.setting(`SELECT company_name FROM hatamoto_site_config`); got != "Old WP Site" {
		t.Errorf("site title = %q, want the WordPress one", got)
	}
	if code, loc := r.status("/2024/05/hello"); code != 200 {
		t.Errorf("/2024/05/hello = %d %s", code, loc)
	}
	// The imported About gives way to the starter About, which stays live.
	if got := r.setting(`SELECT slug FROM hatamoto_entities WHERE metadata->>'import_key' = 'wp:old.example.test:20'`); got != "about-2" {
		t.Errorf("imported about slug = %q, want about-2", got)
	}
	if code, loc := r.status("/about"); code != 200 {
		t.Errorf("/about = %d %s: the live page must stay", code, loc)
	}
	if len(res.URLConflicts) != 1 || res.URLConflicts[0].URL != "/about" || res.URLConflicts[0].Target != "/about-2" {
		t.Errorf("conflicts = %+v", res.URLConflicts)
	}
	if res.Audit == nil || len(res.Audit.Failures) != 0 || len(res.Audit.Unresolved) != 1 {
		t.Errorf("audit = %+v: want the clash listed as unresolved, nothing failed", res.Audit)
	}
}

func TestWordPressImportSiteWithContent_Integration(t *testing.T) {
	r := newWPRig(t, modesSite)
	r.publish("page", "about", "About us", map[string]any{"content": "<p>Our own About page</p>"})
	r.publish("post", "my-post", "My post", map[string]any{"content": "<p>Written here</p>"})
	if _, err := r.db.Pool().Exec(r.ctx, `UPDATE hatamoto_site_config SET company_name='Mine', tagline='Our tagline', timezone='UTC'`); err != nil {
		t.Fatal(err)
	}
	// A redirect the owner made for one of the old URLs.
	if _, err := r.deps.Redirects.CreateRedirect(r.ctx, &redirect.CreateRedirectInput{SourcePath: "/2024/05/other", TargetURL: "/somewhere-else", StatusCode: 301}); err != nil {
		t.Fatal(err)
	}
	patternsBefore := r.str(`SELECT string_agg(slug || '=' || settings::text, ';' ORDER BY slug) FROM hatamoto_entity_schemas WHERE NOT (is_builtin AND slug IN ('block','theme_part'))`)
	homeBefore := r.str(`SELECT COALESCE(homepage_entity_id::text,'') FROM hatamoto_site_config`)

	st, err := wpimport.InspectSite(r.ctx, r.db.Pool(), wpimport.KeyPrefix(modesSite))
	if err != nil || st.Mode != wpimport.ModeExisting || st.Existing != 2 {
		t.Fatalf("state = %+v, %v", st, err)
	}
	rep := wpimport.Scan(modesDump(), modesSite)
	rep.ApplySiteState(st)
	if !strings.Contains(rep.Summary(), "already has 2 published items") {
		t.Errorf("scan summary does not show the mode:\n%s", rep.Summary())
	}

	dump := modesDump()
	res := r.run(dump, true)
	if res.SiteMode != wpimport.ModeExisting || res.Failed != 0 || len(res.Errors) != 0 {
		t.Fatalf("mode=%q failed=%d errors=%+v", res.SiteMode, res.Failed, res.Errors)
	}
	// Nothing site-wide changed. The built-in reuse schemas (block, theme_part) may be
	// ensured at boot by the import run and are not site settings.
	if got := r.str(`SELECT string_agg(slug || '=' || settings::text, ';' ORDER BY slug) FROM hatamoto_entity_schemas WHERE NOT (is_builtin AND slug IN ('block','theme_part'))`); got != patternsBefore {
		t.Errorf("schema settings changed:\n before %s\n after  %s", patternsBefore, got)
	}
	if got := r.str(`SELECT company_name || '|' || tagline || '|' || timezone FROM hatamoto_site_config`); got != "Mine|Our tagline|UTC" {
		t.Errorf("site settings = %q", got)
	}
	if got := r.str(`SELECT COALESCE(homepage_entity_id::text,'') FROM hatamoto_site_config`); got != homeBefore {
		t.Errorf("home page changed")
	}
	warnings := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"site settings kept: site title is \"Mine\" here, \"Old WP Site\" in WordPress", "URL patterns were not changed"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q:\n%s", want, warnings)
		}
	}
	// Imported posts take the existing pattern; old URLs redirect there.
	if code, _ := r.status("/blog/hello"); code != 200 {
		t.Errorf("/blog/hello = %d", code)
	}
	if code, loc := r.status("/2024/05/hello"); code != 301 || !strings.HasSuffix(loc, "/blog/hello") {
		t.Errorf("/2024/05/hello = %d %s", code, loc)
	}
	if code, _ := r.status("/team"); code != 200 {
		t.Errorf("/team = %d", code)
	}
	// about/about: the imported page is renamed, the live page keeps /about, nothing redirects it.
	if got := r.str(`SELECT slug FROM hatamoto_entities WHERE metadata->>'import_key' = 'wp:old.example.test:20'`); got != "about-2" {
		t.Errorf("imported about slug = %q", got)
	}
	if code, loc := r.status("/about"); code != 200 {
		t.Errorf("/about = %d %s", code, loc)
	}
	if got := r.str(`SELECT name FROM hatamoto_entities WHERE slug='about'`); got != "About us" {
		t.Errorf("the live about page was changed: %q", got)
	}
	if n := r.str(`SELECT count(*)::text FROM hatamoto_redirects WHERE source_path='/about'`); n != "0" {
		t.Errorf("a redirect shadows the live /about page")
	}
	// The owner's own redirect was not overwritten.
	if got := r.str(`SELECT target_url FROM hatamoto_redirects WHERE source_path='/2024/05/other'`); got != "/somewhere-else" {
		t.Errorf("owner redirect target = %q", got)
	}
	urls := map[string]bool{}
	for _, c := range res.URLConflicts {
		urls[c.URL] = true
	}
	if len(res.URLConflicts) != 2 || !urls["/about"] || !urls["/2024/05/other"] {
		t.Errorf("conflicts = %+v, want /about and /2024/05/other", res.URLConflicts)
	}
	if res.Audit == nil || len(res.Audit.Unresolved) != 2 {
		t.Errorf("audit = %+v", res.Audit)
	}

	// Re-run: nothing new, nothing reported as an owner edit.
	before := countRows(t, r.ctx, r.db)
	again := r.run(dump, false)
	after := countRows(t, r.ctx, r.db)
	before.Versions, after.Versions = 0, 0
	if again.Created != 0 || before != after || len(again.OverwrittenEdits) != 0 {
		t.Errorf("re-run created=%d edits=%v\n before %+v\n after  %+v", again.Created, again.OverwrittenEdits, before, after)
	}

	// An edit made in Hatamoto is listed when a re-run overwrites it, and kept with KeepEdited.
	edit := func() {
		if _, err := r.db.Pool().Exec(r.ctx, `UPDATE hatamoto_entities SET name='Hello (edited here)', updated_at = now() WHERE metadata->>'import_key' = 'wp:old.example.test:10'`); err != nil {
			t.Fatal(err)
		}
	}
	edit()
	r.keepEdited = true
	kept := r.run(dump, false)
	if len(kept.KeptEdits) != 1 || len(kept.OverwrittenEdits) != 0 {
		t.Fatalf("kept=%v overwritten=%v", kept.KeptEdits, kept.OverwrittenEdits)
	}
	if got := r.str(`SELECT name FROM hatamoto_entities WHERE metadata->>'import_key' = 'wp:old.example.test:10'`); got != "Hello (edited here)" {
		t.Errorf("KeepEdited overwrote the edit: %q", got)
	}
	r.keepEdited = false
	over := r.run(dump, false)
	if len(over.OverwrittenEdits) != 1 || !strings.Contains(over.OverwrittenEdits[0], "Hello") {
		t.Fatalf("overwritten = %v", over.OverwrittenEdits)
	}
	if got := r.str(`SELECT name FROM hatamoto_entities WHERE metadata->>'import_key' = 'wp:old.example.test:10'`); got != "Hello" {
		t.Errorf("edit not overwritten: %q", got)
	}
	if more := r.run(dump, false); len(more.OverwrittenEdits) != 0 {
		t.Errorf("a clean re-run reports edits: %v", more.OverwrittenEdits)
	}

	// Stale redirect: once the old URL is the live one (the site now serves it),
	// the redirect of the earlier run must go.
	if n := r.str(`SELECT count(*)::text FROM hatamoto_redirects WHERE source_path='/2024/05/hello'`); n != "1" {
		t.Fatalf("redirect missing before the re-sync: %s", n)
	}
	dump.Items[0].Link = modesSite + "/blog/hello/"
	final := r.run(dump, false)
	if final.RedirectsRemoved != 1 {
		t.Errorf("redirects removed = %d", final.RedirectsRemoved)
	}
	if n := r.str(`SELECT count(*)::text FROM hatamoto_redirects WHERE source_path='/2024/05/hello'`); n != "0" {
		t.Errorf("stale redirect left")
	}
	if code, _ := r.status("/blog/hello"); code != http.StatusOK {
		t.Errorf("/blog/hello = %d", code)
	}
}

func TestWordPressResumeOfAnotherSourceFails_Integration(t *testing.T) {
	r := newWPRig(t, modesSite)
	first := r.run(modesDump(), false)
	other := modesDump()
	other.Site.URL = "https://other.example.test"
	st := wpimport.NewStore(r.db.Pool(), other.Site.URL)
	runner, err := wpimport.NewRunner(r.deps, st, other, wpimport.Options{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := runner.Run(r.ctx, first.RunID); err == nil || res != nil {
		t.Fatalf("resume across sources: res=%v err=%v", res, err)
	}
}
