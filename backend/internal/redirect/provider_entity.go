package redirect

import (
	"context"
	"strings"
)

// BlogSlugLookup reports the canonical path for a bare slug, or "".
//
// Mirrors handlePublicSlugResolve: a bare slug resolves to /blog/<slug>
// only when it names a published entity whose schema is blog flavoured.
// Everything else lives at /<slug> and needs no hint.
type BlogSlugLookup func(ctx context.Context, slug string) string

// BareSlugToBlogProvider resolves a bare legacy slug to its blog URL.
//
// Runs last in the chain. Legacy WordPress URLs and Google's index
// reference bare slugs like /the-ultimate-twitter-image-size-guide;
// without this they 404 when the content lives under /blog.
type BareSlugToBlogProvider struct {
	lookup BlogSlugLookup
}

func NewBareSlugToBlogProvider(l BlogSlugLookup) *BareSlugToBlogProvider {
	return &BareSlugToBlogProvider{lookup: l}
}

func (p *BareSlugToBlogProvider) Name() string  { return "BareSlugToBlog" }
func (p *BareSlugToBlogProvider) Priority() int { return PriorityBareSlugToBlog }

func (p *BareSlugToBlogProvider) Resolve(ctx context.Context, path string) (*Match, string) {
	if p.lookup == nil {
		return nil, ""
	}
	slug := strings.TrimPrefix(path, "/")
	// Bare, single segment only. A caller passing /blog/foo already has
	// the canonical URL.
	if slug == "" || strings.ContainsAny(slug, "/?#") {
		return nil, ""
	}
	canonical := p.lookup(ctx, slug)
	if canonical == "" {
		return nil, ""
	}
	return &Match{Target: canonical, Status: 301, Rule: "bare-slug-to-blog"}, "exact"
}
