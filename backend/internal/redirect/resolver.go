package redirect

import (
	"context"
	"sort"
	"strings"
)

// Resolution priorities. Lower runs first.
//
// These are deliberately spaced so a provider can be inserted between two
// existing ones without renumbering. See plans/redirect-consolidation-plan.md.
const (
	PriorityDBRules        = 100
	PriorityWPPatterns     = 200
	PriorityBareSlugToBlog = 500
)

// Match is a provider's answer for one path.
type Match struct {
	// Target is where the request should go. Absolute path or full URL.
	Target string
	// Status is the HTTP status to send. 301, 302, 307 or 308.
	Status int
	// Rule names what matched, for explain output. Not user facing.
	Rule string
}

// Stage records one provider's turn, whether it matched or not.
//
// Every stage is recorded, including misses. That is the whole point:
// "what happens to URL X" has to be answerable with the providers that
// declined as well as the one that fired.
type Stage struct {
	Provider string `json:"provider"`
	Priority int    `json:"priority"`
	Matched  bool   `json:"matched"`
	Target   string `json:"target,omitempty"`
	Status   int    `json:"status,omitempty"`
	Rule     string `json:"rule,omitempty"`
	// Variant names which form of the path matched: "exact" or
	// "slash-toggled". Empty when the provider did not match.
	Variant string `json:"variant,omitempty"`
}

// Answer is the full resolution of one path.
type Answer struct {
	Path       string  `json:"path"`
	Normalised string  `json:"normalised"`
	Redirect   *Match  `json:"redirect,omitempty"`
	MatchedBy  string  `json:"matched_by,omitempty"`
	Stages     []Stage `json:"stages"`
}

// Found reports whether any provider matched.
func (a *Answer) Found() bool { return a != nil && a.Redirect != nil }

// Provider resolves one class of redirect.
//
// A provider returns nil to decline. It must not panic and must respect
// ctx cancellation; the chain runs on the request hot path.
type Provider interface {
	Name() string
	Priority() int
	Resolve(ctx context.Context, path string) (*Match, string)
}

// Chain walks providers in priority order and returns the first match.
type Chain struct {
	providers []Provider
}

// NewChain builds a chain, sorted by priority ascending.
func NewChain(providers ...Provider) *Chain {
	sorted := make([]Provider, len(providers))
	copy(sorted, providers)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority() < sorted[j].Priority()
	})
	return &Chain{providers: sorted}
}

// Providers returns the chain in execution order.
func (c *Chain) Providers() []Provider { return c.providers }

// Resolve walks the chain and returns the first match, recording every
// stage including the ones that declined.
func (c *Chain) Resolve(ctx context.Context, path string) *Answer {
	normalised := Normalise(path)
	ans := &Answer{
		Path:       path,
		Normalised: normalised,
		Stages:     make([]Stage, 0, len(c.providers)),
	}

	for _, p := range c.providers {
		if err := ctx.Err(); err != nil {
			return ans
		}
		match, variant := p.Resolve(ctx, normalised)
		st := Stage{Provider: p.Name(), Priority: p.Priority(), Matched: match != nil}
		if match != nil {
			st.Target, st.Status, st.Rule, st.Variant = match.Target, match.Status, match.Rule, variant
			ans.Stages = append(ans.Stages, st)
			ans.Redirect = match
			ans.MatchedBy = p.Name()
			return ans
		}
		ans.Stages = append(ans.Stages, st)
	}
	return ans
}

// Normalise canonicalises a path before matching.
//
// Collapse duplicate slashes, then strip one trailing slash, ahead of the
// lookup (the rule the retired Next.js middleware applied).
//
// Case is deliberately NOT folded. The existing lookup is
// case-sensitive, and folding here would make some paths that 404 today
// start redirecting, which is a behaviour change this stage does not
// make. Providers that want case folding ask for it explicitly.
func Normalise(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	if path == "" {
		return "/"
	}
	return path
}

// ToggleSlash returns the other trailing-slash form of a path.
//
// The existing lookup retries with the slash toggled, so the chain does
// too. See backend/internal/server/redirect_handlers.go.
func ToggleSlash(path string) string {
	if path == "/" {
		return "/"
	}
	if strings.HasSuffix(path, "/") {
		return strings.TrimRight(path, "/")
	}
	return path + "/"
}
