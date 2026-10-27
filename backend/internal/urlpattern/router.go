package urlpattern

import (
	"fmt"
	"sort"
)

// Override swaps the pattern for entities whose attribute Attribute equals
// Equals. SitemapGroup optionally names a different sitemap child.
type Override struct {
	Attribute    string
	Equals       string
	Pattern      string
	SitemapGroup string
}

// SchemaRoute describes how one schema is addressed. Pattern is raw pattern
// text; Aliases are extra schema slugs resolving to this schema.
type SchemaRoute struct {
	SchemaSlug   string
	Aliases      []string
	Pattern      string
	SitemapGroup string
	Overrides    []Override
}

// Target is the pattern chosen for one entity.
type Target struct {
	Pattern      *Pattern
	SitemapGroup string
	// Override is non-nil when an override (not the schema's base pattern) applies.
	Override *Override
}

// Candidate is one way a request path can be served.
type Candidate struct {
	// SchemaSlug is the schema's own slug; SchemaSlugs is the slug plus its
	// aliases, i.e. every schema slug whose rows may be served here.
	SchemaSlug  string
	SchemaSlugs []string
	Pattern     *Pattern
	Vars        Vars
	// Override is non-nil when the match came from an override pattern: the
	// caller must verify the entity's attribute equals Override.Equals.
	Override *Override
}

type route struct {
	slug      string
	slugs     []string
	base      *Pattern
	group     string
	overrides []parsedOverride
}

type parsedOverride struct {
	Override
	pattern *Pattern
}

// Router resolves paths to candidate schemas and entities to URLs.
// It is immutable after NewRouter and safe for concurrent use.
type Router struct {
	routes  []*route
	bySlug  map[string]*route // own slugs first, then aliases (own slugs win)
	entries []entry           // all patterns, in resolution order
}

type entry struct {
	route    *route
	pattern  *Pattern
	override *parsedOverride
}

// NewRouter parses every route. It fails on the first invalid pattern.
func NewRouter(routes []SchemaRoute) (*Router, error) {
	sorted := append([]SchemaRoute(nil), routes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].SchemaSlug < sorted[j].SchemaSlug })

	r := &Router{bySlug: map[string]*route{}}
	for _, sr := range sorted {
		base, err := Parse(sr.Pattern)
		if err != nil {
			return nil, fmt.Errorf("schema %q: %w", sr.SchemaSlug, err)
		}
		rt := &route{slug: sr.SchemaSlug, base: base, group: sr.SitemapGroup}
		rt.slugs = append([]string{sr.SchemaSlug}, sr.Aliases...)
		for _, o := range sr.Overrides {
			op, err := Parse(o.Pattern)
			if err != nil {
				return nil, fmt.Errorf("schema %q override %s=%s: %w", sr.SchemaSlug, o.Attribute, o.Equals, err)
			}
			rt.overrides = append(rt.overrides, parsedOverride{Override: o, pattern: op})
		}
		r.routes = append(r.routes, rt)
		r.entries = append(r.entries, entry{route: rt, pattern: base})
		for i := range rt.overrides {
			r.entries = append(r.entries, entry{route: rt, pattern: rt.overrides[i].pattern, override: &rt.overrides[i]})
		}
	}
	for _, rt := range r.routes {
		r.bySlug[rt.slug] = rt
	}
	for _, rt := range r.routes {
		for _, a := range rt.slugs[1:] {
			if _, taken := r.bySlug[a]; !taken {
				r.bySlug[a] = rt
			}
		}
	}
	sort.SliceStable(r.entries, func(i, j int) bool {
		a, b := r.entries[i].pattern, r.entries[j].pattern
		if a.LiteralCount() != b.LiteralCount() {
			return a.LiteralCount() > b.LiteralCount()
		}
		return a.TokenCount() < b.TokenCount()
	})
	return r, nil
}

// Resolve returns every candidate whose pattern matches path, most specific
// first (more literal segments, then fewer tokens, then schema slug order).
func (r *Router) Resolve(path string) []Candidate {
	var out []Candidate
	for _, e := range r.entries {
		v, ok := Match(e.pattern, path)
		if !ok {
			continue
		}
		c := Candidate{
			SchemaSlug:  e.route.slug,
			SchemaSlugs: append([]string(nil), e.route.slugs...),
			Pattern:     e.pattern,
			Vars:        v,
		}
		if e.override != nil {
			o := e.override.Override
			c.Override = &o
		}
		out = append(out, c)
	}
	return out
}

// Lookup returns the pattern for an entity of schemaSlug (own slug or alias).
// attrs are the entity's attributes flattened to strings; the first matching
// override wins.
func (r *Router) Lookup(schemaSlug string, attrs map[string]string) (Target, bool) {
	rt, ok := r.bySlug[schemaSlug]
	if !ok {
		return Target{}, false
	}
	for i := range rt.overrides {
		o := &rt.overrides[i]
		if v, has := attrs[o.Attribute]; has && v == o.Equals {
			ov := o.Override
			return Target{Pattern: o.pattern, SitemapGroup: o.SitemapGroup, Override: &ov}, true
		}
	}
	return Target{Pattern: rt.base, SitemapGroup: rt.group}, true
}

// URLFor renders the canonical path for an entity. It returns false when the
// schema is unknown to the router; the error reports missing variables.
func (r *Router) URLFor(schemaSlug string, attrs map[string]string, v Vars) (string, bool, error) {
	t, ok := r.Lookup(schemaSlug, attrs)
	if !ok {
		return "", false, nil
	}
	u, err := Render(t.Pattern, v)
	return u, true, err
}

// CanonicalSchema maps a schema slug or alias to the owning schema's own slug.
func (r *Router) CanonicalSchema(schemaSlug string) (string, bool) {
	rt, ok := r.bySlug[schemaSlug]
	if !ok {
		return "", false
	}
	return rt.slug, true
}

// SchemaSlugs returns the schema's own slug plus aliases for any known slug.
func (r *Router) SchemaSlugs(schemaSlug string) []string {
	rt, ok := r.bySlug[schemaSlug]
	if !ok {
		return nil
	}
	return append([]string(nil), rt.slugs...)
}
