package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

func multiSitemapSnap(t *testing.T, prefixAll bool) *entity.URLSnapshot {
	t.Helper()
	enSeg := ""
	if prefixAll {
		enSeg = "en"
	}
	set, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en", Segment: enSeg}, {Tag: "fr", Segment: "fr"}, {Tag: "nl", Segment: "nl"}}, prefixAll, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := entity.NewURLRegistry(pageSchemaLister{}, nil)
	reg.SetLocales(func(context.Context) *entity.LocaleSet { return set })
	snap, err := reg.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func localeEntry(slug, locale string, group *uuid.UUID) *entity.SitemapEntry {
	return &entity.SitemapEntry{ID: uuid.New(), Slug: slug, SchemaSlug: "page", Locale: locale, Group: group, UpdatedAt: "2029-12-01", Priority: 0.8, ChangeFreq: "monthly"}
}

func TestPlanSitemap_PerLocaleFiles(t *testing.T) {
	g := uuid.New()
	in := sitemapInput{
		Snap: multiSitemapSnap(t, false), BaseURL: "https://x.io", Today: testToday,
		Entries: []*entity.SitemapEntry{
			localeEntry("about", "en", &g), localeEntry("a-propos", "fr", &g), localeEntry("solo", "nl", nil),
			localeEntry("shared", entity.LocaleShared, nil), localeEntry("home", "fr", nil),
		},
	}
	plan := planSitemap(in)
	var locs []string
	for _, e := range plan.Index().Sitemaps {
		locs = append(locs, strings.TrimPrefix(e.Loc, "https://x.io/"))
	}
	want := []string{"sitemap-misc.xml", "sitemap-fr-misc.xml", "sitemap-nl-misc.xml"}
	if strings.Join(locs, ",") != strings.Join(want, ",") {
		t.Fatalf("index = %v, want %v", locs, want)
	}
	en, _ := plan.GroupURLs("misc")
	if len(en) != 2 || en[0].Loc != "https://x.io/about" || en[1].Loc != "https://x.io/shared" {
		t.Fatalf("en file = %+v", en)
	}
	var hl []string
	for _, l := range en[0].Links {
		hl = append(hl, l.HrefLang+"="+l.Href)
	}
	if strings.Join(hl, " ") != "en=https://x.io/about fr=https://x.io/fr/a-propos x-default=https://x.io/about" {
		t.Errorf("alternates = %v", hl)
	}
	if len(en[1].Links) != 0 {
		t.Error("shared row has alternates")
	}
	fr, ok := plan.GroupURLs("fr-misc")
	if !ok || len(fr) != 2 || fr[0].Loc != "https://x.io/fr/a-propos" || fr[1].Loc != "https://x.io/fr" || len(fr[0].Links) != 3 {
		t.Fatalf("fr file = %+v", fr)
	}
	nl, _ := plan.GroupURLs("nl-misc")
	if len(nl) != 1 || len(nl[0].Links) != 0 {
		t.Fatalf("nl file = %+v", nl)
	}
	if plan.HasGroup("fr") || plan.HasGroup("de-misc") {
		t.Error("unknown group accepted")
	}
}

func TestPlanSitemap_PrefixAllNamesDefaultFile(t *testing.T) {
	in := sitemapInput{Snap: multiSitemapSnap(t, true), BaseURL: "https://x.io", Today: testToday,
		Entries: []*entity.SitemapEntry{localeEntry("about", "en", nil)}}
	plan := planSitemap(in)
	if !plan.HasGroup("en-misc") || plan.HasGroup("misc") {
		t.Fatalf("groups: %v", plan.Index())
	}
	urls, _ := plan.GroupURLs("en-misc")
	if len(urls) != 1 || urls[0].Loc != "https://x.io/en/about" {
		t.Fatalf("urls = %+v", urls)
	}
}

// The multi-locale index is pinned byte for byte.
func TestPlanSitemap_IndexMultiGolden(t *testing.T) {
	g := uuid.New()
	in := sitemapInput{
		Snap: multiSitemapSnap(t, false), BaseURL: "http://example.test", Today: testToday,
		Entries: []*entity.SitemapEntry{localeEntry("about", "en", &g), localeEntry("a-propos", "fr", &g), localeEntry("solo", "nl", nil)},
	}
	got := renderXML(t, planSitemap(in).Index())
	path := filepath.Join("testdata", "sitemap", "index_multi.xml")
	if os.Getenv("SITEMAP_GOLDEN_UPDATE") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("index_multi differs:\n%s", got)
	}
}
