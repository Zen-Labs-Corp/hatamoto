package server

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/urlpattern"
)

// The sitemap is built in two pure steps so it can be unit tested without a
// database: planSitemap classifies every published entity into the child
// sitemap of its group (from the schema URL registry merged with
// plugin-registered groups), and the plan's methods render the index and the
// child URL sets.
//
//   - An entity goes to the child named by its schema's sitemap_group
//     (default: the pattern's first literal segment, else misc). The group's
//     index page (archive_path and/or a section page) comes first.
//   - Overrides route single entities to another pattern/group (for example
//     into misc); static plugin routes are appended to misc.
//   - The homepage entity is listed as "/" in misc.
//   - A path an active redirect rule claims is never listed: it answers 3xx,
//     not the entity.

const (
	sitemapIndexPriority   = 0.9
	sitemapIndexChangeFreq = "daily"
	// defaultPattern is the pattern of a schema the registry does not know.
	defaultPattern = "/{slug}"
)

// builtinJunkSlugs are entity slugs whose /{slug} URL is dead and never
// advertised: "home" is reserved (served at the root). Section prefixes and
// the tenant's canonical slug are added at plan time.
var builtinJunkSlugs = []string{"home"}

type sitemapInput struct {
	Snap       *entity.URLSnapshot
	Entries    []*entity.SitemapEntry
	BaseURL    string
	Today      string
	TenantSlug string
	// HomepageID is the configured homepage entity; it is advertised as the
	// root URL, so its own pattern URL is skipped.
	HomepageID uuid.UUID
	Plugins    sitemapPluginHooks
	// Redirected reports whether a redirect rule claims path; such rows are
	// dropped. Nil means no rules.
	Redirected func(path string) bool
}

type groupRow struct {
	e          *entity.SitemapEntry
	path       string
	priority   float64
	changeFreq string
	// section marks a section landing page (the group's index).
	section bool
}

type sitemapGroup struct {
	key string
	// archive is the group's index path ("/post") from archive_path or a plugin.
	archive string
	rows    []groupRow
}

type sitemapPlan struct {
	in      sitemapInput
	groups  []*sitemapGroup // misc is always last
	byKey   map[string]*sitemapGroup
	junk    map[string]bool
	defPat  *urlpattern.Pattern
	maxDate map[string]string // per group key
}

func planSitemap(in sitemapInput) *sitemapPlan {
	p := &sitemapPlan{in: in, byKey: map[string]*sitemapGroup{}, junk: map[string]bool{}, maxDate: map[string]string{}}
	p.defPat, _ = urlpattern.Parse(defaultPattern)

	ensure := func(key string) *sitemapGroup {
		if g, ok := p.byKey[key]; ok {
			return g
		}
		g := &sitemapGroup{key: key}
		p.byKey[key] = g
		p.groups = append(p.groups, g)
		return g
	}

	// Candidate groups from the schema settings, in schema order; empty ones
	// are pruned below. prefixGroup maps a section prefix to its group.
	prefixGroup := map[string]string{}
	for _, cfg := range in.Snap.Configs() {
		if cfg.Sitemap.Exclude {
			continue // no entries and no index page
		}
		g := ensure(cfg.SitemapGroup)
		if g.archive == "" {
			g.archive = cfg.ArchivePath
		}
		if first := cfg.Pattern.FirstLiteral(); first != "" && prefixGroup[first] == "" {
			prefixGroup[first] = cfg.SitemapGroup
		}
		for _, o := range cfg.Overrides {
			ensure(o.SitemapGroup)
		}
	}
	for _, pg := range in.Plugins.Groups {
		g := ensure(pg.Key)
		if g.archive == "" && pg.ArchiveIndexPath != "" {
			g.archive = "/" + strings.TrimPrefix(pg.ArchiveIndexPath, "/")
		}
	}
	misc := ensure(miscSitemapGroupKey)

	for _, s := range builtinJunkSlugs {
		p.junk[s] = true
	}
	for prefix := range in.Snap.SectionPrefixes() {
		p.junk[prefix] = true
	}
	if in.TenantSlug != "" {
		p.junk[in.TenantSlug] = true
	}

	for _, e := range in.Entries {
		if e.NoIndex || e.Slug == "" {
			continue
		}
		cfg, known := in.Snap.Config(e.SchemaSlug)
		if known && cfg.Sitemap.Exclude {
			continue
		}
		priority, changeFreq := sitemapStats(e, cfg, known)

		if p.isHomepage(e) {
			p.addGroupRow(misc, groupRow{e: e, path: "/", priority: 1.0, changeFreq: "daily"})
			continue
		}
		if known && in.Snap.IsSectionPage(e.SchemaSlug, e.Slug) {
			key := prefixGroup[e.Slug]
			if key == "" {
				continue
			}
			p.addGroupRow(ensure(key), groupRow{e: e, path: "/" + e.Slug, priority: sitemapIndexPriority, changeFreq: sitemapIndexChangeFreq, section: true})
			continue
		}

		pattern, group := p.defPat, ""
		explicit := false
		if known {
			t, _ := in.Snap.Router().Lookup(e.SchemaSlug, e.Attrs)
			pattern, group = t.Pattern, t.SitemapGroup
			explicit = cfg.Explicit || t.Override != nil
		}

		// Plugin groups claim schemas that have no explicit settings.
		if !explicit {
			if pg := pluginGroupFor(in.Plugins.Groups, e.SchemaSlug); pg != nil {
				p.addGroupRow(ensure(pg.Key), groupRow{e: e, path: "/" + pg.PathPrefix + e.Slug, priority: priority, changeFreq: changeFreq})
				continue
			}
		}

		path, err := urlpattern.Render(pattern, in.Snap.VarsForSitemap(e))
		if err != nil {
			continue
		}
		// Dead or duplicate root URLs: reserved slugs and section prefixes.
		if path == "/"+e.Slug && p.junk[e.Slug] {
			continue
		}
		if group == "" {
			group = miscSitemapGroupKey
		}
		p.addGroupRow(ensure(group), groupRow{e: e, path: path, priority: priority, changeFreq: changeFreq})
	}

	// Drop groups that ended up with nothing to list; misc is always kept, last.
	var kept []*sitemapGroup
	for _, g := range p.groups {
		if g == misc {
			continue
		}
		if len(g.rows) > 0 {
			kept = append(kept, g)
		} else {
			delete(p.byKey, g.key)
		}
	}
	p.groups = append(kept, misc)
	return p
}

const miscSitemapGroupKey = "misc"

func (p *sitemapPlan) entryDate(e *entity.SitemapEntry) string {
	if len(e.UpdatedAt) >= 10 {
		return e.UpdatedAt[:10]
	}
	return p.in.Today
}

func (p *sitemapPlan) addGroupRow(g *sitemapGroup, r groupRow) {
	if p.in.Redirected != nil && p.in.Redirected(r.path) {
		return
	}
	g.rows = append(g.rows, r)
	if d := p.entryDate(r.e); d > p.maxDate["group:"+g.key] {
		p.maxDate["group:"+g.key] = d
	}
}

// isHomepage reports whether e is the entity served at "/": the configured
// homepage, else the entity with the conventional slug "home".
func (p *sitemapPlan) isHomepage(e *entity.SitemapEntry) bool {
	if p.in.HomepageID != uuid.Nil {
		return e.ID == p.in.HomepageID
	}
	return e.Slug == "home"
}

func pluginGroupFor(groups []namedArchiveGroup, schemaSlug string) *namedArchiveGroup {
	for i := range groups {
		for _, s := range groups[i].SchemaSlugs {
			if s == schemaSlug {
				return &groups[i]
			}
		}
	}
	return nil
}

// sitemapStats returns the entry's priority and changefreq: the schema's
// sitemap settings when set, else the repository defaults for its schema.
func sitemapStats(e *entity.SitemapEntry, cfg *entity.SchemaURLConfig, known bool) (float64, string) {
	priority, changeFreq := e.Priority, e.ChangeFreq
	if known {
		if cfg.Sitemap.Priority != nil {
			priority = *cfg.Sitemap.Priority
		}
		if cfg.Sitemap.ChangeFreq != "" {
			changeFreq = cfg.Sitemap.ChangeFreq
		}
	}
	return priority, changeFreq
}

// HasGroup reports whether key names a child sitemap.
func (p *sitemapPlan) HasGroup(key string) bool {
	_, ok := p.byKey[key]
	return ok
}

// Index renders the sitemap index: one child per non-empty group (misc always).
func (p *sitemapPlan) Index() SitemapIndex {
	idx := SitemapIndex{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, g := range p.groups {
		lastMod := p.maxDate["group:"+g.key]
		if lastMod == "" {
			lastMod = p.in.Today
		}
		idx.Sitemaps = append(idx.Sitemaps, SitemapIndexEntry{Loc: fmt.Sprintf("%s/sitemap-%s.xml", p.in.BaseURL, g.key), LastMod: lastMod})
	}
	return idx
}

// GroupURLs renders a group's child: its index page(s) first, then the rows
// (misc starts with the plugin static routes). ok is false for unknown keys.
func (p *sitemapPlan) GroupURLs(key string) (urls []URL, ok bool) {
	g, ok := p.byKey[key]
	if !ok {
		return nil, false
	}
	base := p.in.BaseURL

	if key == miscSitemapGroupKey {
		for _, r := range p.in.Plugins.Static {
			loc := fmt.Sprintf("%s/%s", base, r.Path)
			urls = append(urls, URL{Loc: loc, LastMod: p.in.Today, ChangeFreq: r.ChangeFreq, Priority: r.Priority})
		}
	}

	sectionPaths := map[string]bool{}
	for _, r := range g.rows {
		if r.section {
			sectionPaths[r.path] = true
		}
	}
	// An archive with nothing published in it is an empty page: not listed.
	if g.archive != "" && !sectionPaths[g.archive] && len(g.rows) > 0 {
		loc := base + g.archive
		urls = append(urls, URL{Loc: loc, LastMod: p.in.Today, ChangeFreq: sitemapIndexChangeFreq, Priority: sitemapIndexPriority})
	}
	for _, pass := range []bool{true, false} { // section pages, then the rest
		for _, r := range g.rows {
			if r.section != pass {
				continue
			}
			loc := base + r.path
			urls = append(urls, URL{Loc: loc, LastMod: p.entryDate(r.e), ChangeFreq: r.changeFreq, Priority: r.priority})
		}
	}
	return urls, true
}
