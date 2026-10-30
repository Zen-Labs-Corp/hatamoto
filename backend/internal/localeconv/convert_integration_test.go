//go:build integration

package localeconv

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const (
	keyAbout    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	keyServices = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	keySolo     = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

// regionFixture is a converted-site lookalike: three regions, each a home with
// children up to four levels deep; archived "home" pages under two homes; a
// translation key shared by three regions, one by two (a missing copy) and one
// by a single page; shared posts; a redirect under a segment.
func regionFixture() *Snapshot {
	fixtureSeq = 0
	home := func(seg, tag string) string {
		return fmt.Sprintf(`{"region":%q,"lang":%q,"region_root":true}`, seg, tag)
	}
	page := func(seg, key string) string {
		if key == "" {
			return fmt.Sprintf(`{"region":%q}`, seg)
		}
		return fmt.Sprintf(`{"region":%q,"translation_key":%q}`, seg, key)
	}
	return &Snapshot{
		Site: SiteState{Language: "en"},
		Schemas: []Schema{
			{ID: "s-page", Slug: "page", Name: "Page", Settings: []byte(pageSettings)},
			{ID: "s-post", Slug: "post", Name: "Post", Settings: []byte(`{}`)},
		},
		Entities: []Ent{
			ent("x0", "page", "xx", "", "published", home("xx", "en-XX")),
			ent("x1", "page", "about", "x0", "published", page("xx", keyAbout)),
			ent("x2", "page", "services", "x0", "published", page("xx", keyServices)),
			ent("x3", "page", "web", "x2", "published", page("xx", "")),
			ent("x4", "page", "pricing", "x3", "published", page("xx", "")),
			ent("x5", "page", "plans", "x4", "published", page("xx", "")),
			ent("x6", "page", "home", "x0", "archived", page("xx", "")),
			ent("x7", "page", "draft-page", "x0", "draft", page("xx", "")),
			ent("y0", "page", "yy", "", "published", home("yy", "en-XA")),
			ent("y1", "page", "about", "y0", "published", page("yy", keyAbout)),
			ent("y2", "page", "services", "y0", "published", page("yy", keyServices)),
			ent("y3", "page", "solo", "y0", "published", page("yy", keySolo)),
			ent("y4", "page", "home", "y0", "archived", page("yy", "")),
			ent("z0", "page", "zz", "", "published", home("zz", "en-ZZ")),
			ent("z1", "page", "about", "z0", "published", page("zz", keyAbout)),
			ent("p1", "post", "news-one", "", "published", `{}`),
			ent("p2", "post", "news-two", "", "published", `{}`),
		},
	}
}

func TestConvertRegionFixtureEndToEnd(t *testing.T) {
	ctx := context.Background()
	pool := scratchPool(t)
	seedFixture(t, pool, regionFixture())
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_redirects (source_path, target_url) VALUES ('/xx/home', '/xx')`); err != nil {
		t.Fatal(err)
	}
	svc := testService(pool)
	before := recordedDump(t, pool)
	fullBefore := dump(t, pool)
	urlsBefore := publicURLs(t, pool)

	// ISC-B1: the suggested plan validates; ISC-B3: its dry run changes no URL.
	plan, warnings, err := svc.Suggest(ctx)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("suggest = %v, %+v", err, warnings)
	}
	if issues := plan.Validate(); len(issues) != 0 {
		t.Fatalf("suggested plan: %+v", issues)
	}
	if plan.GroupFrom == nil || plan.GroupFrom.Attribute != "translation_key" || len(plan.StripAttributes) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	plan.StripAttributes = []string{"region_root"}
	rep, err := svc.DryRun(ctx, plan)
	if err != nil || !rep.OK || rep.PathChanges.Count != 0 {
		t.Fatalf("dry run: %+v, %v", rep, err)
	}
	if rep.Redirects.Count != 1 || rep.Redirects.ItemsSample[0] != "/xx/home" {
		t.Errorf("redirects = %+v", rep.Redirects)
	}
	if len(rep.RenamedCollisions) != 2 || rep.Groups.Formed != 2 || rep.Groups.Singletons != 1 {
		t.Errorf("renamed %+v groups %+v", rep.RenamedCollisions, rep.Groups)
	}
	if dump(t, pool) != fullBefore || recordedDump(t, pool) != before {
		t.Fatal("dry run wrote something")
	}

	mustApply(t, svc, plan)

	// ISC-B4 (DB level): every old public URL serves the same entity.
	urlsAfter := publicURLs(t, pool)
	for path, id := range urlsBefore {
		if urlsAfter[path] != id {
			t.Errorf("%s: %q after, want %q", path, urlsAfter[path], id)
		}
	}

	// Locale, slug, parent per row.
	rows := rowsByID(t, pool)
	want := map[string]struct{ locale, slug, parent string }{
		"x0": {"en-XX", "home", ""}, "x1": {"en-XX", "about", ""}, "x2": {"en-XX", "services", ""},
		"x3": {"en-XX", "web", "x2"}, "x4": {"en-XX", "pricing", "x3"}, "x5": {"en-XX", "plans", "x4"},
		"x6": {"en-XX", "home-old-1", ""}, "x7": {"en-XX", "draft-page", ""},
		"y0": {"en-XA", "home", ""}, "y1": {"en-XA", "about", ""}, "y2": {"en-XA", "services", ""},
		"y3": {"en-XA", "solo", ""}, "y4": {"en-XA", "home-old-1", ""},
		"z0": {"en-ZZ", "home", ""}, "z1": {"en-ZZ", "about", ""},
		"p1": {"und", "news-one", ""}, "p2": {"und", "news-two", ""},
	}
	for id, w := range want {
		got := rows[uid(id)]
		wp := ""
		if w.parent != "" {
			wp = uid(w.parent)
		}
		if got.Locale != w.locale || got.Slug != w.slug || got.Parent != wp {
			t.Errorf("%s = %+v, want %+v", id, got, w)
		}
	}

	// ISC-B5 (data level): a group's hreflang set is its copies in settings order,
	// x-default after them; a group with a missing copy lists only what exists.
	var siteOrder []string
	var xDefault string
	if err := pool.QueryRow(ctx, `SELECT array(SELECT l->>'tag' FROM jsonb_array_elements(locales) l), x_default_locale FROM hatamoto_site_config`).Scan(&siteOrder, &xDefault); err != nil {
		t.Fatal(err)
	}
	alternates := func(member string) []string {
		var tags []string
		if err := pool.QueryRow(ctx, `
			SELECT array_agg(e.locale) FROM hatamoto_entities e
			 WHERE e.translation_group = (SELECT translation_group FROM hatamoto_entities WHERE id = $1)`, uid(member)).Scan(&tags); err != nil {
			t.Fatal(err)
		}
		slices.SortFunc(tags, func(a, b string) int { return slices.Index(siteOrder, a) - slices.Index(siteOrder, b) })
		return append(tags, "x-default="+xDefault)
	}
	if got := alternates("y1"); !slices.Equal(got, []string{"en-XX", "en-XA", "en-ZZ", "x-default=en-XX"}) {
		t.Errorf("about alternates = %v", got)
	}
	if got := alternates("x2"); !slices.Equal(got, []string{"en-XX", "en-XA", "x-default=en-XX"}) {
		t.Errorf("services alternates (zz has no copy) = %v", got)
	}
	var solo *string
	if err := pool.QueryRow(ctx, `SELECT translation_group::text FROM hatamoto_entities WHERE id = $1`, uid("y3")).Scan(&solo); err != nil || solo != nil {
		t.Errorf("singleton group = %v (%v), want none", solo, err)
	}

	// ISC-B10: only sitemap_group goes; the override and the region attributes stay.
	var settings, regionRoot, region string
	if err := pool.QueryRow(ctx, `SELECT settings::text FROM hatamoto_entity_schemas WHERE slug = 'page'`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(settings, "sitemap_group") || strings.Count(settings, `"url_pattern": "/{path}"`) != 3 || strings.Count(settings, `"attribute": "region"`) != 3 {
		t.Errorf("page settings = %s", settings)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(attributes->>'region_root', ''), attributes->>'region' FROM hatamoto_entities WHERE id = $1`, uid("x0")).Scan(&regionRoot, &region); err != nil {
		t.Fatal(err)
	}
	if regionRoot != "" || region != "xx" {
		t.Errorf("x0 attributes: region_root %q (stripped), region %q (kept)", regionRoot, region)
	}
	var redirects int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_redirects`).Scan(&redirects); err != nil || redirects != 1 {
		t.Errorf("redirects = %d (%v): the converter creates none", redirects, err)
	}

	// ISC-B9: renamed rows are kept, not deleted.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities`).Scan(&n); err != nil || n != len(regionFixture().Entities) {
		t.Errorf("entities = %d (%v), want %d: nothing is deleted", n, err, len(regionFixture().Entities))
	}

	// ISC-B11: revert restores the recorded columns byte for byte, region_root included.
	var id int64
	if err := pool.QueryRow(ctx, `SELECT max(id) FROM hatamoto_locale_conversions`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Revert(ctx, id, false, "cli"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got := recordedDump(t, pool); got != before {
		t.Errorf("revert is not byte-identical:\nbefore %s\nafter  %s", before, got)
	}
}

// ISC-B8: three ways to abort, each leaving the database untouched.
func TestConvertRegionFixtureAborts(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		mutate func(*Snapshot)
		code   string
	}{
		"a published page with the home slug": {code: CodePublishedCollision, mutate: func(s *Snapshot) { s.Entities[6].Status = "published" }},
		"two group members in one locale": {code: CodeGroupTwoInLocale, mutate: func(s *Snapshot) {
			s.Entities = append(s.Entities, ent("x8", "page", "about-copy", "x0", "published", fmt.Sprintf(`{"region":"xx","translation_key":%q}`, keyAbout)))
		}},
		"a top-level page mapped to a region": {code: CodePathChanges, mutate: func(s *Snapshot) {
			s.Entities = append(s.Entities, ent("q1", "page", "stray", "", "published", `{"region":"yy"}`))
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			pool := scratchPool(t)
			snap := regionFixture()
			c.mutate(snap)
			seedFixture(t, pool, snap)
			svc := testService(pool)
			plan, _, err := svc.Suggest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			before := dump(t, pool)
			_, _, err = svc.Apply(ctx, plan, ApplyOpts{By: "cli"})
			if issues := refusal(t, err); !hasCode(issues, c.code) {
				t.Fatalf("issues = %+v, want %s", issues, c.code)
			}
			var records int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_locale_conversions`).Scan(&records); err != nil || records != 0 {
				t.Errorf("records = %d (%v)", records, err)
			}
			if dump(t, pool) != before {
				t.Error("an aborted apply changed the database")
			}
		})
	}
}
