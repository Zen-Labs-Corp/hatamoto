package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"hatamoto.dev/core/internal/urlpattern"
)

// EntityLocation is where one entity lives on the public site.
type EntityLocation struct {
	// Path is the canonical URL path ("/blog/x", "/about").
	Path string
	// Section is true for a section landing page served at /{slug}.
	Section bool
	// ArchivePath and ArchiveName describe the section index crumb that
	// precedes the entity; empty when the entity has none (no archive_path,
	// a section page, or a per-entity override).
	ArchivePath string
	ArchiveName string
}

// FlattenAttributes returns the top-level scalar attributes as strings, the
// form pattern overrides compare against.
func FlattenAttributes(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case string:
			out[k] = x
		case bool, float64:
			out[k] = fmt.Sprint(x)
		}
	}
	return out
}

// NeedsAncestors reports whether the pattern serving an entity of schemaSlug
// with attrs uses {parent} or {path}, i.e. whether the caller should load the
// entity's ancestor slugs.
func (s *URLSnapshot) NeedsAncestors(schemaSlug string, attrs map[string]string) bool {
	t, ok := s.router.Lookup(schemaSlug, attrs)
	return ok && (t.Pattern.HasParent() || t.Pattern.HasPath())
}

// VarsFor builds the URL variables of ent. ancestors are the slugs of its
// parent chain, root first (nil for a root). The date tokens read
// published_at (created_at while it is unset, for drafts and previews) in the
// site time zone.
func (s *URLSnapshot) VarsFor(ent *Entity, ancestors []string) urlpattern.Vars {
	v := urlpattern.Vars{Slug: ent.Slug}
	if n := len(ancestors); n > 0 {
		v.Parent = ancestors[n-1]
	}
	v.Path = strings.Join(append(append([]string(nil), ancestors...), ent.Slug), "/")
	when := ent.CreatedAt
	if ent.PublishedAt != nil {
		when = *ent.PublishedAt
	}
	if !when.IsZero() {
		when = when.In(s.Location())
		v.Year, v.Month, v.Day = when.Format("2006"), when.Format("01"), when.Format("02")
	}
	return v
}

// NeedsEntityVars reports whether the pattern serving schemaSlug (base
// pattern or any override) uses {parent}, {path} or a date token, i.e. whether
// a URL can only be built from the loaded entity and not from its slug alone.
func (s *URLSnapshot) NeedsEntityVars(schemaSlug string) bool {
	c, ok := s.Config(schemaSlug)
	if !ok {
		return false
	}
	uses := func(p *urlpattern.Pattern) bool { return p.HasParent() || p.HasPath() || p.HasDate() }
	if uses(c.Pattern) {
		return true
	}
	for _, o := range c.Overrides {
		if p, err := urlpattern.Parse(o.URLPattern); err == nil && uses(p) {
			return true
		}
	}
	return false
}

// VarsForSitemap is VarsFor for a sitemap row.
func (s *URLSnapshot) VarsForSitemap(e *SitemapEntry) urlpattern.Vars {
	v := urlpattern.Vars{Slug: e.Slug, Parent: e.ParentSlug, Path: e.Slug}
	if e.AncestorPath != "" {
		v.Path = e.AncestorPath + "/" + e.Slug
	} else if e.ParentSlug != "" {
		v.Path = e.ParentSlug + "/" + e.Slug
	}
	if !e.PublishedAt.IsZero() {
		t := e.PublishedAt.In(s.Location())
		v.Year, v.Month, v.Day = t.Format("2006"), t.Format("01"), t.Format("02")
	}
	return v
}

// Locate resolves the public path of an entity of schemaSlug (own slug or
// alias) from its URL variables. Precedence: section pages, then explicit schema settings (including
// a matching override), then a plugin-registered EntityURLBuilder, then the
// built-in defaults. found is false when nothing knows the schema; err is a
// urlpattern error (for example ErrMissingVar for an empty slug).
func (s *URLSnapshot) Locate(schemaSlug string, attrs map[string]string, v urlpattern.Vars) (loc EntityLocation, found bool, err error) {
	slug := v.Slug
	cfg, known := s.Config(schemaSlug)
	if known && s.IsSectionPage(schemaSlug, slug) {
		return EntityLocation{Path: "/" + slug, Section: true}, true, nil
	}

	var target urlpattern.Target
	explicit := false
	if known {
		target, _ = s.router.Lookup(schemaSlug, attrs)
		explicit = cfg.Explicit || target.Override != nil
	}
	if !explicit {
		if fn, ok := LookupEntityURLBuilder(schemaSlug); ok {
			return EntityLocation{Path: fn("", slug)}, true, nil
		}
	}
	if !known {
		return EntityLocation{}, false, nil
	}

	path, err := urlpattern.Render(target.Pattern, v)
	if err != nil {
		return EntityLocation{}, true, err
	}
	loc = EntityLocation{Path: path}
	if target.Override == nil && cfg.ArchivePath != "" {
		loc.ArchivePath, loc.ArchiveName = cfg.ArchivePath, cfg.crumbLabel()
	}
	return loc, true, nil
}

// EntityPath is Locate for a loaded entity whose ancestor slugs (root first)
// are given. It falls back to "/{slug}" (the pre-pattern behaviour) when the
// schema is unknown or the slug is empty.
func (s *URLSnapshot) EntityPath(ent *Entity, ancestors []string) string {
	if ent.Schema == nil {
		return "/" + ent.Slug
	}
	loc, found, err := s.Locate(ent.Schema.Slug, FlattenAttributes(ent.Attributes), s.VarsFor(ent, ancestors))
	if err != nil || !found {
		return "/" + ent.Slug
	}
	return loc.Path
}

var (
	defaultSnapOnce sync.Once
	defaultSnap     *URLSnapshot
)

// DefaultURLSnapshot is the routing of an install without schema settings:
// page-like schemas at /{slug}, the post family at /blog/{slug}.
func DefaultURLSnapshot() *URLSnapshot {
	defaultSnapOnce.Do(func() {
		snap, err := BuildURLSnapshot([]*EntitySchema{{Slug: "page", Name: "Page"}, {Slug: "post", Name: "Post"}}, nil)
		if err != nil {
			panic("entity: default url snapshot: " + err.Error()) // static input; cannot fail
		}
		defaultSnap = snap
	})
	return defaultSnap
}

// globalURLs backs the pure JSON-LD builders, which have no request context.
var globalURLs atomic.Pointer[URLRegistry]

// SetURLRegistry installs the registry used by BuildEntityURLForJSONLD. The
// server calls it once at startup; without it the defaults apply.
func SetURLRegistry(r *URLRegistry) { globalURLs.Store(r) }

func currentURLSnapshot() *URLSnapshot {
	if r := globalURLs.Load(); r != nil {
		if snap, err := r.Snapshot(context.Background()); err == nil {
			return snap
		}
	}
	return DefaultURLSnapshot()
}

// BuildURL makes the snapshot usable as a linking URLBuilder: it renders the
// path for an entity of schemaSlug (own slug or alias). Unlike EntityPath it
// never falls back, so callers can tell an unknown schema from a real URL.
func (s *URLSnapshot) BuildURL(schemaSlug string, attrs map[string]string, v urlpattern.Vars) (string, bool, error) {
	loc, found, err := s.Locate(schemaSlug, attrs, v)
	return loc.Path, found, err
}

// crumbLabel is the section label of an entity's breadcrumb: the
// archive_crumb_label setting, else archive_title, else the schema name.
func (c *SchemaURLConfig) crumbLabel() string {
	switch {
	case c.ArchiveCrumbLabel != "":
		return c.ArchiveCrumbLabel
	case c.ArchiveTitle != "":
		return c.ArchiveTitle
	}
	return c.Name
}
