package server

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

func TestSitemapHistoricName(t *testing.T) {
	legacy := namingPlan(t, namingSnap(t, true), "legacy", namingEntries()...)
	group := namingPlan(t, namingSnap(t, true), "group", namingEntries()...)
	single := planSitemap(sitemapInput{Snap: sitemapSnap(t, sitemapSchema("post", "")), BaseURL: "https://x.io", Today: testToday})

	for _, name := range []string{"fr-blog", "fr-misc", "blog", "misc", "fr", "nl-anything"} {
		if legacy.HasGroup(name) {
			continue
		}
		if !legacy.historicName(name) {
			t.Errorf("legacy: %s should redirect", name)
		}
	}
	for _, name := range []string{"fr", "en", "blog", "misc", "xx-nope"} {
		if group.HasGroup(name) {
			continue
		}
		want := name != "xx-nope"
		if got := group.historicName(name); got != want {
			t.Errorf("group: historicName(%s) = %v, want %v", name, got, want)
		}
	}
	for _, name := range []string{"zzz", "frx", "typo-blog"} {
		if legacy.historicName(name) || group.historicName(name) {
			t.Errorf("%s should stay a 404", name)
		}
	}
	if single.historicName("blog") || single.historicName("misc") {
		t.Error("single-locale site redirects unknown names")
	}
}

func TestSitemapFileURLs_FilterPerPart(t *testing.T) {
	run := func(t *testing.T, naming, key string) []sdk.SitemapURLs {
		t.Helper()
		s := sitemapTestServer()
		s.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		snap := namingSnap(t, true)
		s.entityService = entity.NewService(&countingSchemaLister{}, nil, s.logger)
		s.entityService.URLRegistry().SetLocales(func(context.Context) (*entity.LocaleSet, error) { return snap.Locales(), nil })
		var seen []sdk.SitemapURLs
		hooks.OnFilter(s.hooks.For("p"), sdk.SitemapURLsF, 10, func(_ context.Context, v sdk.SitemapURLs) (sdk.SitemapURLs, error) {
			seen = append(seen, v)
			return v, nil
		})
		plan := namingPlan(t, snap, naming, namingEntries()...)
		if urls := s.sitemapFileURLs(context.Background(), plan, key); len(urls) == 0 {
			t.Fatalf("no urls for %s", key)
		}
		return seen
	}

	t.Run("legacy runs once per group with the file key", func(t *testing.T) {
		var got []string
		for _, v := range run(t, "legacy", "fr") {
			got = append(got, v.Group+"/"+v.Language+"/"+v.Segment+"/"+v.File)
		}
		if want := "blog/fr/fr/fr,misc/fr/fr/fr"; strings.Join(got, ",") != want {
			t.Fatalf("filter calls = %v, want %s", got, want)
		}
	})
	t.Run("group mode sends one unchanged payload", func(t *testing.T) {
		seen := run(t, "group", "fr-blog")
		if len(seen) != 1 || seen[0].Group != "blog" || seen[0].File != "fr-blog" || seen[0].Segment != "fr" {
			t.Fatalf("filter calls = %+v", seen)
		}
	})
}

// The shared bare files of legacy naming send their file key, as the plugin
// docs say, with no language or segment; a one-locale site still sends none.
func TestSitemapFileURLs_SharedBareFileKey(t *testing.T) {
	s := sitemapTestServer()
	s.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	snap := namingSnap(t, true)
	s.entityService = entity.NewService(&countingSchemaLister{}, nil, s.logger)
	s.entityService.URLRegistry().SetLocales(func(context.Context) (*entity.LocaleSet, error) { return snap.Locales(), nil })
	var seen []sdk.SitemapURLs
	hooks.OnFilter(s.hooks.For("p"), sdk.SitemapURLsF, 10, func(_ context.Context, v sdk.SitemapURLs) (sdk.SitemapURLs, error) {
		seen = append(seen, v)
		return v, nil
	})
	plan := namingPlan(t, snap, "legacy", namingEntries()...)
	for _, key := range []string{"blog", "misc"} {
		seen = nil
		s.sitemapFileURLs(context.Background(), plan, key)
		if len(seen) != 1 || seen[0].Group != key || seen[0].File != key || seen[0].Language != "" || seen[0].Segment != "" {
			t.Errorf("%s: filter calls = %+v", key, seen)
		}
	}
}
