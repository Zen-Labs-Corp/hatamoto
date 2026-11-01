package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

func sitemapTestServer() *Server {
	return &Server{hooks: hooks.New(slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

func sampleURLs() []URL {
	return []URL{{
		Loc: "https://x.test/a", LastMod: "2026-01-01", ChangeFreq: "daily", Priority: 0.8,
		Links: []HrefLang{{Rel: "alternate", HrefLang: "en", Href: "https://x.test/a"}},
	}}
}

// ISC-16: with no filters (or no engine) the URLs round-trip unchanged.
func TestSitemapFilterNoopRoundTrip(t *testing.T) {
	in := sampleURLs()
	if got := (&Server{}).applySitemapFilter(context.Background(), "g", in); !reflect.DeepEqual(got, in) {
		t.Fatalf("nil engine changed urls: %+v", got)
	}
	if got := sitemapTestServer().applySitemapFilter(context.Background(), "g", in); !reflect.DeepEqual(got, in) {
		t.Fatalf("no filters changed urls: %+v", got)
	}
}

func TestSitemapFilterAddsEntriesOnly(t *testing.T) {
	s := sitemapTestServer()
	var seen sdk.SitemapURLs
	hooks.OnFilter(s.hooks.For("p"), sdk.SitemapURLsF, 10, func(_ context.Context, v sdk.SitemapURLs) (sdk.SitemapURLs, error) {
		seen = v
		v.Add = append(v.Add,
			sdk.SitemapURL{Loc: "https://x.test/extra", Alternates: []sdk.Alternate{{Rel: "alternate", HrefLang: "fr", Href: "https://x.test/fr"}}},
			sdk.SitemapURL{Loc: "https://x.test/a", Priority: 0.1}, // already in the file: dropped, not overwritten
			sdk.SitemapURL{Loc: ""},                                // no loc: dropped
		)
		return v, nil
	})
	// A second plugin sees the first one's additions and appends its own.
	hooks.OnFilter(s.hooks.For("q"), sdk.SitemapURLsF, 20, func(_ context.Context, v sdk.SitemapURLs) (sdk.SitemapURLs, error) {
		v.Add = append(v.Add, sdk.SitemapURL{Loc: "https://x.test/more"}, sdk.SitemapURL{Loc: "https://x.test/extra"})
		return v, nil
	})

	in := sampleURLs()
	got := s.applySitemapFilter(context.Background(), "g", in)
	if seen.Group != "g" || len(seen.Add) != 0 {
		t.Fatalf("filter saw %+v; want the group and no URLs", seen)
	}
	if len(got) != 3 || got[0].Priority != 0.8 || got[1].Loc != "https://x.test/extra" || got[1].Links[0].HrefLang != "fr" || got[2].Loc != "https://x.test/more" {
		t.Fatalf("got %+v", got)
	}
	if len(in) != 1 {
		t.Fatal("the caller's slice was modified")
	}
	if !sitemapHasLinks(got) || sitemapHasLinks([]URL{{Loc: "x"}}) {
		t.Fatal("sitemapHasLinks wrong")
	}
}

func TestSitemapFilterErrorKeepsURLs(t *testing.T) {
	s := sitemapTestServer()
	hooks.OnFilter(s.hooks.For("bad"), sdk.SitemapURLsF, 10, func(_ context.Context, v sdk.SitemapURLs) (sdk.SitemapURLs, error) {
		v.Add = []sdk.SitemapURL{{Loc: "https://x.test/never"}}
		return v, errors.New("boom")
	})
	in := sampleURLs()
	if got := s.applySitemapFilter(context.Background(), "misc", in); !reflect.DeepEqual(got, in) {
		t.Fatalf("failed filter altered urls: %+v", got)
	}
}

// The sitemap.urls filter sees the bare group name (and the file's locale on a
// multi-locale site) for every planned file, not an empty group.
func TestApplySitemapFilterFor_GroupName(t *testing.T) {
	run := func(t *testing.T, snap *entity.URLSnapshot, entries []*entity.SitemapEntry, key string) sdk.SitemapURLs {
		t.Helper()
		s := sitemapTestServer()
		s.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		s.entityService = entity.NewService(&countingSchemaLister{}, nil, s.logger)
		s.entityService.URLRegistry().SetLocales(func(context.Context) (*entity.LocaleSet, error) { return snap.Locales(), nil })
		var seen sdk.SitemapURLs
		hooks.OnFilter(s.hooks.For("p"), sdk.SitemapURLsF, 10, func(_ context.Context, v sdk.SitemapURLs) (sdk.SitemapURLs, error) {
			seen = v
			return v, nil
		})
		plan := planSitemap(sitemapInput{Snap: snap, BaseURL: "https://x.io", Today: testToday, Entries: entries})
		urls, ok := plan.GroupURLs(key)
		if !ok {
			t.Fatalf("no group %q", key)
		}
		s.applySitemapFilterFor(context.Background(), plan.byKey[key], urls)
		return seen
	}

	t.Run("one locale", func(t *testing.T) {
		reg := entity.NewURLRegistry(pageSchemaLister{}, nil)
		snap, err := reg.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		got := run(t, snap, []*entity.SitemapEntry{localeEntry("about", "en", nil)}, "misc")
		if got.Group != "misc" || got.Language != "" || got.Segment != "" || got.File != "" {
			t.Fatalf("filter saw %+v; want group misc, no language, segment or file", got)
		}
	})
	t.Run("multi locale", func(t *testing.T) {
		got := run(t, multiSitemapSnap(t, false), []*entity.SitemapEntry{localeEntry("a-propos", "fr", nil)}, "fr-misc")
		if got.Group != "misc" || got.Language != "fr" || got.Segment != "fr" || got.File != "fr-misc" {
			t.Fatalf("filter saw %+v; want group misc, language fr, segment fr, file fr-misc", got)
		}
	})
	t.Run("unprefixed default", func(t *testing.T) {
		got := run(t, multiSitemapSnap(t, false), []*entity.SitemapEntry{localeEntry("about", "en", nil)}, "misc")
		if got.Group != "misc" || got.Language != "en" || got.Segment != "" || got.File != "misc" {
			t.Fatalf("filter saw %+v; want group misc, language en, no segment, file misc", got)
		}
	})
}
