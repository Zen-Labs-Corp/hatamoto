//go:build integration

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/localeconv"
)

const (
	convKeyAbout    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	convKeyServices = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	convKeySolo     = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

	convPageSettings = `{"url_pattern":"/{slug}","overrides":[
 {"attribute":"region","equals":"xx","url_pattern":"/{path}","sitemap_group":"xx"},
 {"attribute":"region","equals":"yy","url_pattern":"/{path}","sitemap_group":"yy"},
 {"attribute":"region","equals":"zz","url_pattern":"/{path}","sitemap_group":"zz"}]}`
)

// convEnt is one fixture row; the id doubles as the marker the page renders.
type convEnt struct {
	id, schema, slug, parent, status, attrs string
}

// convFixture is a converted-site lookalike (copied from the localeconv
// integration test): three regions, each a home with children up to four
// levels deep, an archived and a draft page, a translation key shared by three
// regions, one by two (a missing copy), one by a single page, shared posts.
func convFixture() []convEnt {
	home := func(seg, tag string) string {
		return fmt.Sprintf(`{"region":%q,"lang":%q,"region_root":true}`, seg, tag)
	}
	page := func(seg, key string) string {
		if key == "" {
			return fmt.Sprintf(`{"region":%q}`, seg)
		}
		return fmt.Sprintf(`{"region":%q,"translation_key":%q}`, seg, key)
	}
	return []convEnt{
		{"x0", "page", "xx", "", "published", home("xx", "en-XX")},
		{"x1", "page", "about", "x0", "published", page("xx", convKeyAbout)},
		{"x2", "page", "services", "x0", "published", page("xx", convKeyServices)},
		{"x3", "page", "web", "x2", "published", page("xx", "")},
		{"x4", "page", "pricing", "x3", "published", page("xx", "")},
		{"x5", "page", "plans", "x4", "published", page("xx", "")},
		{"x6", "page", "home", "x0", "archived", page("xx", "")},
		{"x7", "page", "draft-page", "x0", "draft", page("xx", "")},
		{"y0", "page", "yy", "", "published", home("yy", "en-XA")},
		{"y1", "page", "about", "y0", "published", page("yy", convKeyAbout)},
		{"y2", "page", "services", "y0", "published", page("yy", convKeyServices)},
		{"y3", "page", "solo", "y0", "published", page("yy", convKeySolo)},
		{"y4", "page", "home", "y0", "archived", page("yy", "")},
		{"z0", "page", "zz", "", "published", home("zz", "en-ZZ")},
		{"z1", "page", "about", "z0", "published", page("zz", convKeyAbout)},
		{"p1", "post", "news-one", "", "published", `{}`},
		{"p2", "post", "news-two", "", "published", `{}`},
	}
}

// convID maps a fixture id to a stable uuid.
func convID(id string) string { return uuid.NewSHA1(uuid.NameSpaceOID, []byte(id)).String() }

// convMarker is rendered by every fixture page, so a response names its entity.
func convMarker(id string) string { return "MARK-" + id + "-END" }

// convSeed writes the fixture (parents first) into the database.
func convSeed(t *testing.T, exec func(q string, args ...any), rows []convEnt) {
	t.Helper()
	for slug, settings := range map[string]string{"page": convPageSettings, "post": `{"url_pattern":"/{slug}"}`} {
		exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, settings)
			VALUES ($1, $2, $2, '{"type":"object"}', $3::jsonb)
			ON CONFLICT (slug) DO UPDATE SET settings = EXCLUDED.settings`, uuid.New(), slug, settings)
	}
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, e := range rows {
		attrs := map[string]any{}
		if err := json.Unmarshal([]byte(e.attrs), &attrs); err != nil {
			t.Fatal(err)
		}
		attrs["page_sections"] = []map[string]any{{
			"id": "s1", "type": "hero", "sort_order": 1, "content": map[string]any{"headline": convMarker(e.id)},
		}}
		raw, _ := json.Marshal(attrs)
		var parent *string
		if e.parent != "" {
			p := convID(e.parent)
			parent = &p
		}
		exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes, parent_id, created_at, updated_at, published_at)
			VALUES ($1, (SELECT id FROM hatamoto_entity_schemas WHERE slug = $2), $3, $3, $4, $5::jsonb, $6, $7, $7, $8)`,
			convID(e.id), e.schema, e.slug, e.status, string(raw), parent, epoch.Add(time.Duration(i)*time.Minute), epoch)
	}
}

// convPublicPaths is the public path of every published fixture row before
// the conversion: region rows live under their region page (/{path}), posts
// at their slug.
func convPublicPaths(rows []convEnt) map[string]string {
	byID := map[string]convEnt{}
	for _, e := range rows {
		byID[e.id] = e
	}
	out := map[string]string{}
	for _, e := range rows {
		if e.status != "published" {
			continue
		}
		path := e.slug
		for p, ok := byID[e.parent]; ok; p, ok = byID[p.parent] {
			path = p.slug + "/" + path
		}
		out["/"+path] = e.id
	}
	return out
}

var (
	convMarkerRe   = regexp.MustCompile(`MARK-([a-z0-9]+)-END`)
	convHreflangRe = regexp.MustCompile(`<link[^>]*rel="alternate"[^>]*>`)
	convAttrRe     = regexp.MustCompile(`(hreflang|href)="([^"]*)"`)
)

// TestLocaleConvertFixtureHTTP_Integration drives the full server over the
// converted fixture: every public URL of the region site answers the same
// entity before and after the conversion and again after the revert, the
// hreflang set of a grouped page lists its copies in settings order plus
// x-default, and with prefix-all the bare root redirects to the default home.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run LocaleConvertFixtureHTTP
func TestLocaleConvertFixtureHTTP_Integration(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set - skipping locale convert integration test")
	}
	// A scratch database: the conversion rewrites the whole site, so the shared
	// one (and its site config) stays as it was.
	dsn := scratchMigratedDB(t, base)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "locale-conv")
	t.Setenv("HATAMOTO_SITE_NAME", "Locale Conv")
	t.Setenv("PUBLIC_BASE_URL", "https://locale-conv.example.test")
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

	rows := convFixture()
	exec(`INSERT INTO hatamoto_site_config (company_name, language) VALUES ('Locale Conv', 'en')
		ON CONFLICT (id) DO UPDATE SET language = 'en', locales = '[]'::jsonb`)
	convSeed(t, exec, rows)
	// An explicit homepage on a shared page: the conversion must clear it, or
	// it would be served in place of every region home.
	exec(`UPDATE hatamoto_site_config SET homepage_entity_id = $1`, convID("p1"))
	recordSite := func() string {
		var s string
		if err := db.Pool().QueryRow(ctx, `SELECT jsonb_build_object('language', language, 'locales', locales, 'prefix_all', prefix_all, 'x_default_locale', x_default_locale, 'homepage_entity_id', homepage_entity_id)::text
			FROM hatamoto_site_config WHERE id = TRUE`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	recorded := recordSite()

	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	served := func(path string) (int, string) {
		rec := do(path)
		if m := convMarkerRe.FindStringSubmatch(rec.Body.String()); m != nil {
			return rec.Code, m[1]
		}
		return rec.Code, ""
	}

	// Before: status and served entity of every public URL.
	urls := convPublicPaths(rows)
	type answer struct {
		code int
		id   string
	}
	before := map[string]answer{}
	for path, id := range urls {
		code, got := served(path)
		if code != http.StatusOK || got != id {
			t.Fatalf("before: GET %s = %d serving %q, want 200 serving %q", path, code, got, id)
		}
		before[path] = answer{code, got}
	}
	checkAll := func(stage string) {
		t.Helper()
		for path, want := range before {
			if code, got := served(path); code != want.code || got != want.id {
				t.Errorf("%s: GET %s = %d serving %q, want %d serving %q", stage, path, code, got, want.code, want.id)
			}
		}
	}

	// ISC-B4: apply through the real service, drop the caches, every URL answers as before.
	svc := localeconv.NewService(db.Pool(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	plan, warnings, err := svc.Suggest(ctx)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("suggest = %v, %+v", err, warnings)
	}
	plan.StripAttributes = []string{"region_root"}
	rec, _, err := svc.Apply(ctx, plan, localeconv.ApplyOpts{By: "cli"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	invalidate := func() {
		srv.siteConfig.Invalidate()
		srv.entityService.URLRegistry().Invalidate()
		srv.invalidateSiteSettings(ctx)
	}
	invalidate()
	checkAll("after apply")

	// ISC-B5: the hreflang set of a grouped page is its copies in settings order, then x-default.
	var tags []string
	var xDefault string
	if err := db.Pool().QueryRow(ctx, `SELECT array(SELECT l->>'tag' FROM jsonb_array_elements(locales) l), x_default_locale FROM hatamoto_site_config`).Scan(&tags, &xDefault); err != nil {
		t.Fatal(err)
	}
	if len(tags) != 3 || xDefault == "" {
		t.Fatalf("settings = %v, x-default %q", tags, xDefault)
	}
	var segments []string
	if err := db.Pool().QueryRow(ctx, `SELECT array(SELECT l->>'segment' FROM jsonb_array_elements(locales) l) FROM hatamoto_site_config`).Scan(&segments); err != nil {
		t.Fatal(err)
	}
	// hreflang returns the head's alternates as "<hreflang>=<path>" in document order.
	hreflang := func(path string) []string {
		t.Helper()
		rec := do(path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		var out []string
		for _, link := range convHreflangRe.FindAllString(rec.Body.String(), -1) {
			var lang, href string
			for _, m := range convAttrRe.FindAllStringSubmatch(link, -1) {
				if m[1] == "hreflang" {
					lang = m[2]
				} else {
					href = strings.TrimPrefix(m[2], "https://locale-conv.example.test")
				}
			}
			if lang != "" {
				out = append(out, lang+"="+href)
			}
		}
		return out
	}
	// Copies of a group: locale tag -> public path, in settings order.
	pathOf := func(seg, rest string) string { return strings.TrimSuffix("/"+seg+"/"+rest, "/") }
	want := func(copies map[string]string) []string {
		var out []string
		for i, tag := range tags {
			if rest, ok := copies[tag]; ok {
				out = append(out, tag+"="+pathOf(segments[i], rest))
			}
		}
		// x-default points at the default locale's copy.
		for i, tag := range tags {
			if tag == xDefault {
				out = append(out, "x-default="+pathOf(segments[i], copies[tag]))
			}
		}
		return out
	}
	all := map[string]string{}
	for _, tag := range tags {
		all[tag] = "about"
	}
	about := hreflang(pathOf(segments[slices.Index(tags, "en-XA")], "about"))
	if exp := want(all); !slices.Equal(about, exp) {
		t.Errorf("about hreflang = %v, want %v", about, exp)
	}
	if len(about) != len(tags)+1 {
		t.Errorf("about hreflang = %v, want %d copies + x-default", about, len(tags))
	}
	// services has no zz copy: only the existing copies and x-default are listed.
	services := hreflang(pathOf(segments[slices.Index(tags, "en-XX")], "services"))
	exp := want(map[string]string{"en-XX": "services", "en-XA": "services"})
	if !slices.Equal(services, exp) {
		t.Errorf("services hreflang (zz has no copy) = %v, want %v", services, exp)
	}

	// ISC-B6: with prefix-all the bare root is a 302 to the default home.
	var defSeg string
	if err := db.Pool().QueryRow(ctx, `SELECT l->>'segment' FROM hatamoto_site_config, jsonb_array_elements(locales) l WHERE l->>'tag' = language`).Scan(&defSeg); err != nil {
		t.Fatal(err)
	}
	root := do("/")
	if root.Code != http.StatusFound || root.Header().Get("Location") != "/"+defSeg {
		t.Errorf("GET / = %d -> %q, want 302 -> /%s", root.Code, root.Header().Get("Location"), defSeg)
	}
	if code, id := served("/" + defSeg); code != http.StatusOK || id == "" {
		t.Errorf("default home /%s = %d serving %q", defSeg, code, id)
	}

	// Revert: the original responses and the recorded settings are back.
	if _, err := svc.Revert(ctx, rec.ID, false, "cli"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	invalidate()
	checkAll("after revert")
	if got := recordSite(); got != recorded {
		t.Errorf("site settings after revert = %s, want %s", got, recorded)
	}
	if code, _ := served("/"); code == http.StatusFound {
		t.Errorf("GET / still redirects after the revert")
	}
}
