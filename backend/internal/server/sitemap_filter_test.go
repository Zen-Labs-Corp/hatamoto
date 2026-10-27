package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"

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
