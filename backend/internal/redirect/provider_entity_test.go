package redirect

import (
	"context"
	"testing"
)

func blogLookup(known map[string]bool) BlogSlugLookup {
	return func(_ context.Context, slug string) string {
		if known[slug] {
			return "/blog/" + slug
		}
		return ""
	}
}

func TestProviders_BareSlugMatchesPublishedBlogSlug(t *testing.T) {
	p := NewBareSlugToBlogProvider(blogLookup(map[string]bool{"open-graph-tags": true}))
	m, variant := p.Resolve(context.Background(), "/open-graph-tags")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.Target != "/blog/open-graph-tags" || m.Status != 301 {
		t.Errorf("got %+v", m)
	}
	if variant != "exact" {
		t.Errorf("variant = %q", variant)
	}
}

func TestProviders_BareSlugDeclinesUnknownSlug(t *testing.T) {
	p := NewBareSlugToBlogProvider(blogLookup(map[string]bool{"known": true}))
	if m, _ := p.Resolve(context.Background(), "/not-a-post"); m != nil {
		t.Errorf("expected decline, got %+v", m)
	}
}

// A multi-segment path must not reach the lookup at all. The endpoint
// this ports is explicitly for bare single segments.
func TestProviders_BareSlugRejectsMultiSegment(t *testing.T) {
	called := false
	p := NewBareSlugToBlogProvider(func(_ context.Context, _ string) string {
		called = true
		return "/blog/x"
	})
	for _, path := range []string{"/blog/foo", "/old/seo", "/a/b/c"} {
		if m, _ := p.Resolve(context.Background(), path); m != nil {
			t.Errorf("%s: expected decline, got %+v", path, m)
		}
	}
	if called {
		t.Error("lookup must not be consulted for multi-segment paths")
	}
}

func TestProviders_BareSlugRejectsQueryAndFragment(t *testing.T) {
	p := NewBareSlugToBlogProvider(blogLookup(map[string]bool{"x": true}))
	for _, path := range []string{"/x?a=1", "/x#frag", "/"} {
		if m, _ := p.Resolve(context.Background(), path); m != nil {
			t.Errorf("%s: expected decline, got %+v", path, m)
		}
	}
}

func TestProviders_BareSlugNilLookupDeclines(t *testing.T) {
	if m, _ := NewBareSlugToBlogProvider(nil).Resolve(context.Background(), "/x"); m != nil {
		t.Error("nil lookup must decline, not panic")
	}
}

// BareSlugToBlog must run last, or a bare slug would win over a real rule.
func TestProviders_ChainOrderIsCorrect(t *testing.T) {
	chain := NewChain(
		NewBareSlugToBlogProvider(blogLookup(nil)),
		NewDBRulesProvider(nil),
		NewWPPatternsProvider(nil),
	)
	want := []string{"DBRules", "WPPatterns", "BareSlugToBlog"}
	got := []string{}
	for _, p := range chain.Providers() {
		got = append(got, p.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("chain has %d providers, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %s, want %s (full order %v)", i, got[i], want[i], got)
		}
	}
}

// A real rule in the DB must beat the bare-slug fallback. This is the
// case that matters: 14 rules already redirect bare slugs to /blog.
func TestProviders_DBRuleBeatsBareSlugFallback(t *testing.T) {
	db := NewDBRulesProvider(func(_ context.Context, path string) *Redirect {
		if path == "/open-graph-tags" {
			return &Redirect{SourcePath: path, TargetURL: "/blog/open-graph-tags-v2", StatusCode: 301}
		}
		return nil
	})
	bare := NewBareSlugToBlogProvider(blogLookup(map[string]bool{"open-graph-tags": true}))

	ans := NewChain(bare, db).Resolve(context.Background(), "/open-graph-tags")
	if ans.MatchedBy != "DBRules" {
		t.Fatalf("MatchedBy = %q, want DBRules", ans.MatchedBy)
	}
	if ans.Redirect.Target != "/blog/open-graph-tags-v2" {
		t.Errorf("target = %q, want the DB rule to win", ans.Redirect.Target)
	}
}
