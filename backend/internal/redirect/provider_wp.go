package redirect

import (
	"context"
	"regexp"
	"strings"
)

var (
	reNumeric   = regexp.MustCompile(`^\d+$`)
	reYear      = regexp.MustCompile(`^\d{4}$`)
	reMonthDay  = regexp.MustCompile(`^\d{1,2}$`)
	wpStrayDirs = map[string]bool{
		"wp-json": true, "wp-admin": true, "wp-content": true, "wp-login.php": true,
	}
)

func segments(path string) []string {
	out := []string{}
	for s := range strings.SplitSeq(strings.TrimRight(path, "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// WPPatternsProvider serves the generic WordPress URL families that are
// shapes rather than literal paths: dated permalinks, taxonomy archives,
// feeds and stray wp-* directories.
//
// The provider only answers while the site setting legacy_wp_redirects is on
// (see docs/REDIRECTS.md). Site-specific renames and retired sections are DB
// redirect rules, not code.
//
// Taxonomy archives (/author, /category, /tag) and /blog/page/N are native
// routes, so they are not handled here. Targets point at the site's post
// archive and post URL (see WithTargets); without it they are /blog and
// /blog/{slug}.
type WPPatternsProvider struct {
	enabled func(ctx context.Context) bool
	targets func(ctx context.Context) PostTargets
}

// PostTargets are where legacy WordPress URLs land: the post archive path and
// a function giving a post's canonical path.
type PostTargets struct {
	Archive string
	Post    func(slug string) string
}

// WithTargets sets the lookup of the site's post archive and post URLs.
func (p *WPPatternsProvider) WithTargets(t func(ctx context.Context) PostTargets) *WPPatternsProvider {
	p.targets = t
	return p
}

// NewWPPatternsProvider builds the provider. enabled reports the
// legacy_wp_redirects setting; nil means always on.
func NewWPPatternsProvider(enabled func(ctx context.Context) bool) *WPPatternsProvider {
	return &WPPatternsProvider{enabled: enabled}
}

func (p *WPPatternsProvider) Name() string  { return "WPPatterns" }
func (p *WPPatternsProvider) Priority() int { return PriorityWPPatterns }

func (p *WPPatternsProvider) Resolve(ctx context.Context, path string) (*Match, string) {
	if p.enabled != nil && !p.enabled(ctx) {
		return nil, ""
	}
	parts := segments(path)
	if len(parts) == 0 {
		return nil, ""
	}
	t := PostTargets{Archive: "/blog", Post: func(slug string) string { return "/blog/" + slug }}
	if p.targets != nil {
		if got := p.targets(ctx); got.Archive != "" && got.Post != nil {
			t = got
		}
	}
	blog := func(rule string) (*Match, string) {
		return &Match{Target: t.Archive, Status: 301, Rule: rule}, "exact"
	}

	// 1. WordPress root pagination /page/N
	if parts[0] == "page" && len(parts) > 1 && reNumeric.MatchString(parts[1]) {
		return blog("wp-root-pagination")
	}
	// 2. dated permalinks /YYYY/MM/slug and /YYYY/MM/DD/slug
	if len(parts) >= 3 && reYear.MatchString(parts[0]) && reMonthDay.MatchString(parts[1]) {
		var slug string
		if len(parts) == 3 {
			slug = parts[2]
		} else if len(parts) >= 4 && reMonthDay.MatchString(parts[2]) {
			slug = parts[3]
		}
		if slug != "" {
			return &Match{Target: t.Post(slug), Status: 301, Rule: "wp-dated-permalink"}, "exact"
		}
	}
	// 3. /feed and /comments/feed
	if parts[0] == "feed" || (parts[0] == "comments" && len(parts) > 1 && parts[1] == "feed") {
		return blog("wp-feed")
	}
	// 4. stray wp-* directories
	if wpStrayDirs[parts[0]] {
		return blog("wp-stray-dir")
	}
	return nil, ""
}
