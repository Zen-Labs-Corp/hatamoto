package server

import (
	"fmt"
	"slices"
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
	// Naming is the site's sitemap_naming setting: "" or "group" (one file per
	// locale and group) or "legacy" (one file per prefixed locale holding all
	// its groups). It only matters on a multi-locale site.
	Naming string
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
	// base is the bare group name (the sitemap.urls filter's group); locale is
	// the locale tag of the file, set only on a multi-locale site, where key
	// is "<segment>-<base>".
	base, locale string
	// archive is the group's index path ("/post") from archive_path or a plugin.
	archive string
	rows    []groupRow
	// folded is set on a legacy locale file: the per-group files it lists, in
	// order. They are not registered in the plan themselves.
	folded []*sitemapGroup
}

type sitemapPlan struct {
	in      sitemapInput
	groups  []*sitemapGroup // misc is always last
	byKey   map[string]*sitemapGroup
	junk    map[string]bool
	defPat  *urlpattern.Pattern
	maxDate map[string]string // per group key
	// copies lists, per translation group, the locale and path of every listed
	// copy; GroupURLs turns them into alternates.
	copies map[uuid.UUID][]localeCopy
	// bare lists every bare group key of the site (also when the group has no
	// rows), so a request for a historic file name can be recognised.
	bare []string
	// collisions and notes are reported to the caller, which logs them once.
	collisions, notes []string
}

// maxLegacyFileURLs is the sitemap protocol limit; legacy files are not split.
const maxLegacyFileURLs = 50000

type localeCopy struct{ locale, path string }

func planSitemap(in sitemapInput) *sitemapPlan {
	p := &sitemapPlan{in: in, byKey: map[string]*sitemapGroup{}, junk: map[string]bool{}, maxDate: map[string]string{}, copies: map[uuid.UUID][]localeCopy{}}
	p.defPat, _ = urlpattern.Parse(defaultPattern)

	ensure := func(key string) *sitemapGroup {
		if g, ok := p.byKey[key]; ok {
			return g
		}
		g := &sitemapGroup{key: key, base: key}
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

	// Drop groups that ended up with nothing to list; misc is always kept,
	// last. A multi-locale plan prunes while splitting: the rows of a bare group
	// may all sit in its per-locale files (prefix-all, a fr-only schema).
	if p.multi() {
		for _, g := range p.groups {
			p.bare = append(p.bare, g.key)
		}
		if p.legacy() {
			p.splitLegacy(misc)
		} else {
			p.splitByLocale(misc)
		}
		return p
	}
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

func (p *sitemapPlan) locales() *entity.LocaleSet { return p.in.Snap.Locales() }

func (p *sitemapPlan) multi() bool { return p.locales().Multi() }

// legacy reports whether the plan uses the pre-locale file names.
// A locale segment that is also a legacy file name (index, misc) keeps group
// naming, so the index never lists one name twice.
func (p *sitemapPlan) legacy() bool {
	if !p.multi() || p.in.Naming != "legacy" {
		return false
	}
	for _, e := range p.locales().Entries() {
		if e.Segment == "index" || e.Segment == "misc" {
			return false
		}
	}
	return true
}

func (p *sitemapPlan) collide(msg string) {
	if !slices.Contains(p.collisions, msg) {
		p.collisions = append(p.collisions, msg)
	}
}

// fileLocale is the locale whose sitemap files list e: its own, shared rows go
// with the default locale.
func (p *sitemapPlan) fileLocale(e *entity.SitemapEntry) string {
	if e.Locale == "" || e.Locale == entity.LocaleShared {
		return p.locales().Default()
	}
	return e.Locale
}

// localized returns the per-locale file group of base, creating it. Its key is
// the bare group for an unprefixed default locale, else "<segment>-<group>".
func (p *sitemapPlan) localized(base *sitemapGroup, tag string) *sitemapGroup {
	key := base.key
	if seg := strings.TrimPrefix(p.locales().Prefix(tag), "/"); seg != "" {
		key = seg + "-" + base.key
	}
	if g, ok := p.byKey[key]; ok {
		if (g.locale != "" && (g.locale != tag || g.base != base.key)) || (g.locale == "" && key != base.key) {
			p.collide(fmt.Sprintf("sitemap file %q is shared by several locale and group pairs; their rows are merged", key))
		}
		if g.locale == "" {
			g.base, g.locale = base.key, tag // the unprefixed default file keeps the bare name
		}
		return g
	}
	g := &sitemapGroup{key: key, base: base.key, locale: tag}
	if base.archive != "" {
		g.archive = p.in.Snap.WithLocale(tag, base.archive)
	}
	p.byKey[key] = g
	return g
}

// splitByLocale replaces the bare groups by their per-locale files, locales in
// settings order and groups in plan order inside each, misc last.
func (p *sitemapPlan) splitByLocale(misc *sitemapGroup) {
	bare := p.groups
	p.groups = nil
	for _, tag := range p.locales().PublicTags() {
		for _, b := range bare {
			if b == misc {
				continue
			}
			if g := p.localized(b, tag); len(g.rows) > 0 {
				p.groups = append(p.groups, g)
			} else {
				delete(p.byKey, g.key)
			}
		}
		p.groups = append(p.groups, p.localized(misc, tag))
	}
	for _, b := range bare {
		if p.byKey[b.key] == b && !slices.Contains(p.groups, b) {
			delete(p.byKey, b.key)
		}
	}
}

// splitLegacy lays the files out as a site had them before locales: each
// prefixed locale gets one file "<segment>" holding all its groups, while the
// shared rows (and the default locale when it has no prefix) keep the bare
// group files. Locale files come first, then the bare groups, misc last.
func (p *sitemapPlan) splitLegacy(misc *sitemapGroup) {
	set := p.locales()
	bare := p.groups
	p.groups = nil

	segFile := map[string]*sitemapGroup{}
	for _, tag := range set.PublicTags() {
		seg := strings.TrimPrefix(set.Prefix(tag), "/")
		if seg == "" {
			continue
		}
		c := &sitemapGroup{key: seg, locale: tag}
		for _, b := range bare {
			if b != misc {
				if g := p.localized(b, tag); len(g.rows) > 0 {
					c.folded = append(c.folded, g)
				}
			}
		}
		if g := p.localized(misc, tag); len(g.rows) > 0 {
			c.folded = append(c.folded, g)
		}
		segFile[seg] = c
		if len(c.folded) > 0 {
			p.groups = append(p.groups, c)
		}
	}

	unprefixedDefault := set.Prefix(set.Default()) == ""
	var files []*sitemapGroup
	for _, b := range bare {
		g := b
		if unprefixedDefault {
			g = p.localized(b, set.Default()) // sets the file's locale; same group
		}
		if g == misc || len(g.rows) == 0 {
			continue
		}
		if c := segFile[g.key]; c != nil && len(c.folded) > 0 {
			c.folded = append(c.folded, g)
			p.collide(fmt.Sprintf("segment %q and group %q name the same sitemap file; the group is merged into the locale file", g.key, g.key))
			continue
		}
		files = append(files, g)
	}
	if unprefixedDefault {
		p.localized(misc, set.Default())
	}
	p.groups = append(p.groups, files...)
	p.groups = append(p.groups, misc)

	p.byKey = map[string]*sitemapGroup{}
	for _, g := range p.groups {
		p.byKey[g.key] = g
		if len(g.folded) == 0 {
			continue
		}
		n := 0
		for _, part := range g.folded {
			if d := p.maxDate["group:"+part.key]; d > p.maxDate["group:"+g.key] {
				p.maxDate["group:"+g.key] = d
			}
			n += len(part.rows)
			if part.archive != "" {
				n++
			}
		}
		if n > maxLegacyFileURLs {
			p.notes = append(p.notes, fmt.Sprintf("sitemap file %q lists %d URLs, over the %d limit of the sitemap protocol; legacy files are not split", g.key, n, maxLegacyFileURLs))
		}
	}
}

// FileParts returns the per-group files a child sitemap lists: the folded
// parts of a legacy locale file, else the file itself.
func (p *sitemapPlan) FileParts(key string) ([]*sitemapGroup, bool) {
	g, ok := p.byKey[key]
	if !ok {
		return nil, false
	}
	if len(g.folded) > 0 {
		return g.folded, true
	}
	return []*sitemapGroup{g}, true
}

const miscSitemapGroupKey = "misc"

func (p *sitemapPlan) entryDate(e *entity.SitemapEntry) string {
	if len(e.UpdatedAt) >= 10 {
		return e.UpdatedAt[:10]
	}
	return p.in.Today
}

func (p *sitemapPlan) addGroupRow(g *sitemapGroup, r groupRow) {
	if p.multi() {
		if p.locales().IsHidden(r.e.Locale) {
			return // not public: the loader already skips these, the plan never lists them either
		}
		r.path = p.in.Snap.WithLocale(r.e.Locale, r.path)
		shared := r.e.Locale == "" || r.e.Locale == entity.LocaleShared
		// Legacy names: shared rows stay in the bare group files when the
		// default locale has a prefix of its own.
		if !(p.legacy() && shared && p.locales().Prefix(p.locales().Default()) != "") {
			g = p.localized(g, p.fileLocale(r.e))
		}
	}
	if p.in.Redirected != nil && p.in.Redirected(r.path) {
		return
	}
	if p.multi() && r.e.Group != nil && r.e.Locale != entity.LocaleShared && !r.section {
		p.copies[*r.e.Group] = append(p.copies[*r.e.Group], localeCopy{r.e.Locale, r.path})
	}
	g.rows = append(g.rows, r)
	if d := p.entryDate(r.e); d > p.maxDate["group:"+g.key] {
		p.maxDate["group:"+g.key] = d
	}
}

// isHomepage reports whether e is the entity served at "/": the configured
// homepage, else the entity with the conventional slug "home".
func (p *sitemapPlan) isHomepage(e *entity.SitemapEntry) bool {
	if p.multi() && e.Slug == "home" && p.fileLocale(e) != p.locales().Default() {
		return true // each other locale has its own home
	}
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

	if len(g.folded) > 0 {
		seen := map[string]bool{}
		for _, part := range g.folded {
			for _, u := range p.rowURLs(part, nil) {
				if !seen[u.Loc] {
					seen[u.Loc] = true
					urls = append(urls, u)
				}
			}
		}
		return urls, true
	}

	if g.base == miscSitemapGroupKey || key == miscSitemapGroupKey {
		if g.locale != "" && g.locale != p.locales().Default() {
			return p.rowURLs(g, urls), true
		}
		for _, r := range p.in.Plugins.Static {
			loc := fmt.Sprintf("%s/%s", base, r.Path)
			urls = append(urls, URL{Loc: loc, LastMod: p.in.Today, ChangeFreq: r.ChangeFreq, Priority: r.Priority})
		}
	}

	return p.rowURLs(g, urls), true
}

// rowURLs appends the archive page and the rows of g to urls.
func (p *sitemapPlan) rowURLs(g *sitemapGroup, urls []URL) []URL {
	base := p.in.BaseURL
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
			urls = append(urls, URL{Loc: loc, LastMod: p.entryDate(r.e), ChangeFreq: r.changeFreq, Priority: r.priority, Links: p.alternates(r.e)})
		}
	}
	return urls
}

// alternates lists every listed copy of e's translation group, in settings
// order, plus x-default; nil unless two or more copies are listed.
func (p *sitemapPlan) alternates(e *entity.SitemapEntry) []HrefLang {
	if e.Group == nil {
		return nil
	}
	cs := p.copies[*e.Group]
	if len(cs) < 2 {
		return nil
	}
	set := p.locales()
	var out []HrefLang
	xdef := ""
	for _, tag := range set.PublicTags() {
		for _, c := range cs {
			if c.locale != tag {
				continue
			}
			href := p.in.BaseURL + c.path
			out = append(out, HrefLang{Rel: "alternate", HrefLang: tag, Href: href})
			if xdef == "" || tag == set.XDefault() {
				xdef = href
			}
			break
		}
	}
	if len(out) < 2 {
		return nil
	}
	return append(out, HrefLang{Rel: "alternate", HrefLang: "x-default", Href: xdef})
}
