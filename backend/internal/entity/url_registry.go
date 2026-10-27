package entity

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/urlpattern"
)

// Built-in defaults applied to a schema whose settings carry no url_pattern.
// They reproduce the behaviour of a core install without any configuration:
// `page`-like schemas live at /{slug}, the post family at /blog/{slug}.
const (
	defaultURLPattern     = "/{slug}"
	defaultPostURLPattern = "/blog/{slug}"
	defaultPostArchive    = "/blog"
	defaultPostGroup      = "blog"
	miscSitemapGroup      = "misc"

	urlRegistryTTL = 60 * time.Second
)

// postFamilySlugs are the schema slugs that share the post defaults when they
// have no explicit settings. The first one present in the database owns the
// route; the others resolve to it as aliases.
var postFamilySlugs = []string{"post", "blog", "article", "blog_post"}

// SchemaLister is the slice of SchemaRepository the registry needs.
type SchemaLister interface {
	List(ctx context.Context) ([]*EntitySchema, error)
}

// SchemaURLConfig is the effective routing configuration of one schema
// (settings merged over the built-in defaults).
type SchemaURLConfig struct {
	SchemaSlug string
	Name       string
	// Aliases are extra schema slugs resolving to this schema.
	Aliases     []string
	Pattern     *urlpattern.Pattern
	ArchivePath string
	// SitemapGroup is the sitemap group the schema's entries belong to.
	SitemapGroup string
	Overrides    []SchemaURLOverride
	Sitemap      SchemaSitemapSettings
	SectionPages bool
	// ArchiveTitle and ArchiveDescription are the trimmed settings values;
	// empty when unset.
	ArchiveTitle       string
	ArchiveDescription string
	// ArchiveCrumbLabel is the trimmed archive_crumb_label setting.
	ArchiveCrumbLabel string
	// Explicit is true when the schema's settings supplied url_pattern.
	Explicit bool
}

// URLSnapshot is an immutable view of every schema's routing configuration.
//
// Locale: a snapshot resolves paths without a language. Entities carry a
// locale, but only those in the site language are public, and URLs never
// contain the locale. A second public locale would need the changes listed in
// the urlpattern package doc (a path token, Resolve returning the locale, a
// per-locale filter, hreflang, per-locale sitemaps and feeds, translation
// links, an admin switcher); none of it is built.
type URLSnapshot struct {
	router  *urlpattern.Router
	configs []*SchemaURLConfig
	byslug  map[string]*SchemaURLConfig
	// loc is the time zone {year}/{month}/{day} are read in: the site's
	// `timezone` setting, else UTC. Never nil.
	loc *time.Location
}

// Location is the time zone date URL tokens are rendered in.
func (s *URLSnapshot) Location() *time.Location {
	if s.loc == nil {
		return time.UTC
	}
	return s.loc
}

// Router returns the pattern router built from the schemas.
func (s *URLSnapshot) Router() *urlpattern.Router { return s.router }

// Configs returns every schema's configuration ordered by schema slug.
func (s *URLSnapshot) Configs() []*SchemaURLConfig { return s.configs }

// Config returns the configuration for a schema slug or alias.
func (s *URLSnapshot) Config(schemaSlug string) (*SchemaURLConfig, bool) {
	if c, ok := s.byslug[schemaSlug]; ok {
		return c, true
	}
	if own, ok := s.router.CanonicalSchema(schemaSlug); ok {
		c, ok := s.byslug[own]
		return c, ok
	}
	return nil, false
}

// URLRegistry loads schema routing settings, caches the resulting router and
// is invalidated whenever a schema is created, updated or deleted. A short TTL
// bounds staleness for changes made by other processes.
type URLRegistry struct {
	lister SchemaLister
	logger *slog.Logger
	now    func() time.Time

	// timezone returns the site time zone name ("" or an unknown name: UTC).
	timezone func(context.Context) string

	mu       sync.Mutex
	snap     *URLSnapshot
	loadedAt time.Time
}

// SetTimezone installs the source of the site time zone used for date URL
// tokens. It takes effect when the snapshot next reloads (at most
// urlRegistryTTL), or at once after Invalidate.
func (r *URLRegistry) SetTimezone(fn func(context.Context) string) {
	r.mu.Lock()
	r.timezone = fn
	r.snap = nil
	r.mu.Unlock()
}

// NewURLRegistry creates a registry over lister. logger may be nil.
func NewURLRegistry(lister SchemaLister, logger *slog.Logger) *URLRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	return &URLRegistry{lister: lister, logger: logger, now: time.Now}
}

// Invalidate drops the cached snapshot; the next call reloads.
func (r *URLRegistry) Invalidate() {
	r.mu.Lock()
	r.snap = nil
	r.mu.Unlock()
}

// Snapshot returns the cached snapshot, reloading when missing or expired.
func (r *URLRegistry) Snapshot(ctx context.Context) (*URLSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snap != nil && r.now().Sub(r.loadedAt) < urlRegistryTTL {
		return r.snap, nil
	}
	schemas, err := r.lister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("load schemas for url registry: %w", err)
	}
	snap, err := BuildURLSnapshot(schemas, r.logger)
	if err != nil {
		return nil, err
	}
	if r.timezone != nil {
		if name := strings.TrimSpace(r.timezone(ctx)); name != "" {
			if loc, err := time.LoadLocation(name); err == nil {
				snap.loc = loc
			} else {
				r.logger.Warn("unknown site timezone; date URLs use UTC", "timezone", name, "error", err)
			}
		}
	}
	r.snap, r.loadedAt = snap, r.now()
	return snap, nil
}

// URLFor renders the canonical path for an entity of schemaSlug (own slug or
// alias). The bool is false when no schema matches.
func (r *URLRegistry) URLFor(ctx context.Context, schemaSlug string, attrs map[string]string, v urlpattern.Vars) (string, bool, error) {
	snap, err := r.Snapshot(ctx)
	if err != nil {
		return "", false, err
	}
	return snap.router.URLFor(schemaSlug, attrs, v)
}

// BuildURLSnapshot merges schema settings over the defaults and builds the
// router. A schema whose stored settings are invalid is logged and routed with
// the defaults so one bad row cannot take the public site down.
func BuildURLSnapshot(schemas []*EntitySchema, logger *slog.Logger) (*URLSnapshot, error) {
	if logger == nil {
		logger = slog.Default()
	}
	sorted := append([]*EntitySchema(nil), schemas...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })

	type parsed struct {
		schema *EntitySchema
		st     SchemaURLSettings
	}
	var all []parsed
	for _, s := range sorted {
		st, err := s.URLSettings()
		if err == nil {
			err = ValidateSchemaSettings(s.Settings)
		}
		if err != nil {
			logger.Error("invalid schema settings; using defaults", "schema", s.Slug, "error", err)
			st = SchemaURLSettings{}
		}
		all = append(all, parsed{s, st})
	}

	// The first post-family schema without an explicit pattern owns the shared
	// post defaults; the remaining family members become its aliases. Family
	// slugs that an explicit schema claims (as its own slug or an alias) are
	// left to that schema: they neither own nor become aliases of the default.
	familyOwner := ""
	present := map[string]bool{}
	claimed := map[string]bool{}
	for _, p := range all {
		if p.st.URLPattern == "" {
			present[p.schema.Slug] = true
			continue
		}
		claimed[p.schema.Slug] = true
		for _, a := range p.st.Aliases {
			claimed[a] = true
		}
	}
	var ownedFamily []string
	for _, slug := range postFamilySlugs {
		if claimed[slug] {
			continue
		}
		ownedFamily = append(ownedFamily, slug)
		if familyOwner == "" && present[slug] {
			familyOwner = slug
		}
	}

	snap := &URLSnapshot{byslug: map[string]*SchemaURLConfig{}, loc: time.UTC}
	var routes []urlpattern.SchemaRoute
	for _, p := range all {
		slug := p.schema.Slug
		if p.st.URLPattern == "" && isPostFamily(slug) && slug != familyOwner {
			continue // served through the owner's aliases
		}
		cfg := &SchemaURLConfig{
			SchemaSlug:         slug,
			Name:               p.schema.Name,
			Aliases:            p.st.Aliases,
			ArchivePath:        p.st.ArchivePath,
			Overrides:          p.st.Overrides,
			Sitemap:            p.st.Sitemap,
			SectionPages:       p.st.SectionPages,
			ArchiveTitle:       strings.TrimSpace(p.st.ArchiveTitle),
			ArchiveDescription: strings.TrimSpace(p.st.ArchiveDescription),
			ArchiveCrumbLabel:  strings.TrimSpace(p.st.ArchiveCrumbLabel),
			Explicit:           p.st.URLPattern != "",
		}
		patternText := p.st.URLPattern
		group := p.st.SitemapGroup
		if patternText == "" {
			patternText = defaultURLPattern
			if slug == familyOwner {
				patternText = defaultPostURLPattern
				cfg.Aliases = appendMissing(cfg.Aliases, ownedFamily, slug)
				if cfg.ArchivePath == "" {
					cfg.ArchivePath = defaultPostArchive
				}
				if group == "" {
					group = defaultPostGroup
				}
			}
		}
		pat, err := urlpattern.Parse(patternText)
		if err != nil {
			return nil, fmt.Errorf("schema %q: %w", slug, err)
		}
		cfg.Pattern = pat
		cfg.SitemapGroup = effectiveGroup(pat, group)

		route := urlpattern.SchemaRoute{
			SchemaSlug:   slug,
			Aliases:      cfg.Aliases,
			Pattern:      patternText,
			SitemapGroup: cfg.SitemapGroup,
		}
		for i, o := range cfg.Overrides {
			op, err := urlpattern.Parse(o.URLPattern)
			if err != nil {
				return nil, fmt.Errorf("schema %q override %d: %w", slug, i, err)
			}
			cfg.Overrides[i].SitemapGroup = effectiveGroup(op, o.SitemapGroup)
			route.Overrides = append(route.Overrides, urlpattern.Override{
				Attribute:    o.Attribute,
				Equals:       o.Equals,
				Pattern:      o.URLPattern,
				SitemapGroup: cfg.Overrides[i].SitemapGroup,
			})
		}
		routes = append(routes, route)
		snap.configs = append(snap.configs, cfg)
		snap.byslug[slug] = cfg
	}

	router, err := urlpattern.NewRouter(routes)
	if err != nil {
		return nil, err
	}
	snap.router = router
	return snap, nil
}

// effectiveGroup resolves the sitemap group for a pattern: the configured
// group, else the first literal segment, else misc.
func effectiveGroup(p *urlpattern.Pattern, configured string) string {
	switch {
	case configured != "":
		return configured
	case p.FirstLiteral() != "":
		return p.FirstLiteral()
	}
	return miscSitemapGroup
}

func isPostFamily(slug string) bool {
	return slices.Contains(postFamilySlugs, slug)
}

// appendMissing appends each of extra (except self) not already in list.
func appendMissing(list, extra []string, self string) []string {
	have := map[string]bool{self: true}
	for _, s := range list {
		have[s] = true
	}
	out := append([]string(nil), list...)
	for _, s := range extra {
		if !have[s] {
			out = append(out, s)
			have[s] = true
		}
	}
	return out
}

// SectionPrefixes returns the first literal segment of every pattern (for example "post" for /post/{slug}). A page-like schema with
// SectionPages set serves a row whose slug equals such a prefix at /{slug}.
func (s *URLSnapshot) SectionPrefixes() map[string]bool {
	out := map[string]bool{}
	for _, c := range s.configs {
		if c.Pattern.FirstLiteral() != "" {
			out[c.Pattern.FirstLiteral()] = true
		}
	}
	return out
}

// SectionPageSchemas returns the schema slugs (own slug plus aliases) of every
// schema with SectionPages enabled.
func (s *URLSnapshot) SectionPageSchemas() []string {
	var out []string
	for _, c := range s.configs {
		if c.SectionPages {
			out = append(out, c.SchemaSlug)
			out = append(out, c.Aliases...)
		}
	}
	return out
}

// IsSectionPage reports whether an entity of schemaSlug with the given slug is
// a section landing page: its schema has SectionPages and the slug equals a
// section prefix.
func (s *URLSnapshot) IsSectionPage(schemaSlug, slug string) bool {
	c, ok := s.Config(schemaSlug)
	return ok && c.SectionPages && s.SectionPrefixes()[slug]
}

// OverrideAttributes returns the attribute names any schema override compares
// against (sorted, unique), i.e. the attributes a bulk listing must carry to
// pick each entity's pattern.
func (s *URLSnapshot) OverrideAttributes() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range s.configs {
		for _, o := range c.Overrides {
			if !seen[o.Attribute] {
				seen[o.Attribute] = true
				out = append(out, o.Attribute)
			}
		}
	}
	sort.Strings(out)
	return out
}

// postConfig is the config of the schema that owns the post family routes.
func (s *URLSnapshot) postConfig() *SchemaURLConfig {
	for _, c := range s.configs {
		if isPostFamily(c.SchemaSlug) {
			return c
		}
	}
	return nil
}

// PostArchivePath is the post archive path of the site ("/blog" by default).
func (s *URLSnapshot) PostArchivePath() string {
	if c := s.postConfig(); c != nil && c.ArchivePath != "" {
		return c.ArchivePath
	}
	return defaultPostArchive
}

// PostPath is the canonical path of the post with the given slug.
func (s *URLSnapshot) PostPath(slug string) string {
	if c := s.postConfig(); c != nil {
		if loc, found, err := s.Locate(c.SchemaSlug, nil, urlpattern.Vars{Slug: slug}); err == nil && found {
			return loc.Path
		}
	}
	return defaultPostArchive + "/" + slug
}
