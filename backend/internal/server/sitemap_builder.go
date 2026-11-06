package server

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/taxonomy"
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
	// SEO holds the per-type noindex switches; nil keeps every type listed.
	SEO *seo.Settings
	// Terms are the term archives that may be listed; they are used only when
	// the taxonomy sitemaps are switched on in SEO > Sitemap.
	Terms []sitemapTerm
	// Redirected reports whether a redirect rule claims path; such rows are
	// dropped. Nil means no rules.
	Redirected func(path string) bool
	// Naming is the site's sitemap_naming setting: "" or "group" (one file per
	// locale and group) or "legacy" (one file per prefixed locale holding all
	// its groups). It only matters on a multi-locale site.
	Naming string
	// NamingUnread is set when the setting (or the term list) could not be
	// read and the plan is built from a default: the result is served but
	// must not be cached.
	NamingUnread bool
}

// sitemapTerm is a term archive: Path is the term's path inside its kind.
type sitemapTerm struct {
	Kind, Path string
	LastMod    string // YYYY-MM-DD, "" for today
	NoIndex    bool
}

// taxonomyGroupPrefix starts the group key of a taxonomy's sitemap file. Schema
// groups are named after a URL segment or a sitemap_group, so a collision is
// possible only by choice and is reported.
const taxonomyGroupPrefix = "taxonomy-"

// maxSitemapImages bounds the images listed for one URL.
const maxSitemapImages = 10

type groupRow struct {
	e          *entity.SitemapEntry
	path       string
	priority   float64
	changeFreq string
	images     []string
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
	// archiveSchema is the content type the archive lists, "" for a plugin's.
	archiveSchema string
	// archiveMod is the archive page's lastmod when it has group pages: the
	// newest update in any of them ("" = the plan's today).
	archiveMod string
	rows       []groupRow
	// folded is set on a legacy locale file: the per-group files it lists, in
	// order. They are not registered in the plan themselves.
	folded []*sitemapGroup
	// A group over the per-file URL limit is listed in several files: the
	// first keeps the group's key and chunk 0, the others are named
	// "<key>-2", "<key>-3", ... and point back at it through chunkOf. chunks
	// is the number of files, set on every one of them.
	chunkOf       *sitemapGroup
	chunk, chunks int
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
	// rendered keeps the URLs of each group split into files, so a chunk is
	// cut from them without rendering the group again.
	rendered map[*sitemapGroup][]URL
	// renders counts groupURLs calls, for tests.
	renders int
}

// maxLegacyFileURLs is the sitemap protocol limit; legacy files are not split.
// A lower limit saved in SEO > Sitemap replaces it.
const maxLegacyFileURLs = 50000

type localeCopy struct{ locale, path string }

func planSitemap(in sitemapInput) *sitemapPlan {
	p := &sitemapPlan{in: in, byKey: map[string]*sitemapGroup{}, junk: map[string]bool{}, maxDate: map[string]string{}, copies: map[uuid.UUID][]localeCopy{}, rendered: map[*sitemapGroup][]URL{}}
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
		// An excluded schema has no entries and no index page, except that
		// group pages keep the archive page and one row per group listed.
		groupsOnly := cfg.Sitemap.Exclude && hasArchiveGroups(cfg)
		if cfg.Sitemap.Exclude && !groupsOnly {
			continue
		}
		g := ensure(cfg.SitemapGroup)
		if g.archive == "" {
			g.archive, g.archiveSchema = cfg.ArchivePath, cfg.SchemaSlug
		}
		if groupsOnly {
			continue
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

	sm := p.sitemapSettings()
	var taxKeys []string
	groupBuckets := map[string]*archiveBucket{}
	for _, e := range in.Entries {
		if e.NoIndex || p.noIndexEntry(e) || e.Slug == "" {
			continue
		}
		// The hide flag only keeps the URL out of the sitemap; the page's
		// robots output is untouched.
		if e.Hidden || (sm != nil && sm.NoAuthor && e.SchemaSlug == entity.PersonSchemaSlug) {
			continue
		}
		cfg, known := in.Snap.Config(e.SchemaSlug)
		if known && hasArchiveGroups(cfg) {
			noteArchiveBucket(groupBuckets, cfg, e)
		}
		if known && cfg.Sitemap.Exclude {
			continue
		}
		priority, changeFreq := sitemapStats(e, cfg, known, sm)

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
				p.addGroupRow(ensure(pg.Key), groupRow{e: e, path: "/" + pg.PathPrefix + e.Slug, priority: priority, changeFreq: changeFreq, images: p.images(e)})
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
		p.addGroupRow(ensure(group), groupRow{e: e, path: path, priority: priority, changeFreq: changeFreq, images: p.images(e)})
	}

	p.addArchiveGroupRows(groupBuckets, ensure)

	if sm != nil && sm.Taxonomy {
		for _, t := range in.Terms {
			if t.NoIndex || in.SEO.NoIndexView(string(seo.KindTaxonomy), "", t.Kind) {
				continue
			}
			key := taxonomyGroupPrefix + t.Kind
			if _, taken := p.byKey[key]; taken && !slices.Contains(taxKeys, key) {
				p.collide(fmt.Sprintf("sitemap file %q is named by a content type group and a taxonomy; their rows are merged", key))
			}
			if !slices.Contains(taxKeys, key) {
				taxKeys = append(taxKeys, key)
			}
			// A synthetic shared entry: the archive has one URL for all
			// locales, listed in the default locale's file.
			te := &entity.SitemapEntry{Slug: t.Path, UpdatedAt: t.LastMod}
			p.addGroupRow(ensure(key), groupRow{e: te, path: taxonomy.ArchivePrefix(t.Kind) + t.Path, priority: sitemapIndexPriority, changeFreq: sitemapIndexChangeFreq})
		}
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
		p.splitFiles()
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
	p.splitFiles()
	return p
}

// splitFiles moves the URLs of a group over the per-file limit into further
// files. A legacy locale file folds several groups and is never split (see
// maxLegacyFileURLs). The sitemap.urls filter keeps seeing the bare group:
// children share its base.
func (p *sitemapPlan) splitFiles() {
	limit := p.sitemapSettings().EffectiveMaxURLs()
	var out []*sitemapGroup
	for _, g := range p.groups {
		out = append(out, g)
		if len(g.folded) > 0 {
			continue
		}
		if len(g.rows)+1+len(p.in.Plugins.Static) <= limit {
			continue // cannot exceed the limit: nothing to render here
		}
		urls := p.groupURLs(g)
		chunks := (len(urls) + limit - 1) / limit
		if chunks < 2 {
			continue
		}
		keys := make([]string, chunks)
		taken := false
		for i := 1; i < chunks; i++ {
			keys[i] = fmt.Sprintf("%s-%d", g.key, i+1)
			_, clash := p.byKey[keys[i]]
			taken = taken || clash
		}
		if taken {
			p.collide(fmt.Sprintf("sitemap file %q cannot be split: a file name it would use is taken", g.key))
			continue
		}
		g.chunks = chunks
		p.rendered[g] = urls
		for i := 1; i < chunks; i++ {
			c := &sitemapGroup{key: keys[i], base: g.base, locale: g.locale, chunkOf: g, chunk: i, chunks: chunks}
			for _, u := range urls[i*limit : min((i+1)*limit, len(urls))] {
				if u.LastMod > p.maxDate["group:"+c.key] {
					p.maxDate["group:"+c.key] = u.LastMod
				}
			}
			p.byKey[c.key] = c
			out = append(out, c)
		}
	}
	p.groups = out
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
		if limit := p.sitemapSettings().EffectiveMaxURLs(); n > limit {
			p.notes = append(p.notes, fmt.Sprintf("sitemap file %q lists more URLs than the %d limit of the sitemap protocol; legacy files are not split", g.key, limit))
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

// archiveBucket is one group page of an archive: the newest update among its
// entries.
type archiveBucket struct {
	cfg    *entity.SchemaURLConfig
	n      int64
	newest string
}

// hasArchiveGroups reports whether the schema's archive has group pages.
func hasArchiveGroups(cfg *entity.SchemaURLConfig) bool {
	return cfg.ArchivePath != "" && cfg.ArchiveGroups.Enabled()
}

// noteArchiveBucket counts e into its group page, if its attribute puts it in one.
func noteArchiveBucket(buckets map[string]*archiveBucket, cfg *entity.SchemaURLConfig, e *entity.SitemapEntry) {
	attrs := make(map[string]any, len(e.Attrs))
	for k, v := range e.Attrs {
		attrs[k] = v
	}
	n, ok := cfg.ArchiveGroups.Bucket(attrs)
	if !ok {
		return
	}
	key := fmt.Sprintf("%s/%d", cfg.SchemaSlug, n)
	b := buckets[key]
	if b == nil {
		b = &archiveBucket{cfg: cfg, n: n}
		buckets[key] = b
	}
	if e.UpdatedAt > b.newest {
		b.newest = e.UpdatedAt
	}
}

// addArchiveGroupRows lists each group page (newest group first, per schema in
// schema order) as a synthetic shared row of its schema's sitemap group, and
// dates the archive page by the newest of them.
func (p *sitemapPlan) addArchiveGroupRows(buckets map[string]*archiveBucket, ensure func(string) *sitemapGroup) {
	list := make([]*archiveBucket, 0, len(buckets))
	for _, b := range buckets {
		list = append(list, b)
	}
	slices.SortFunc(list, func(a, b *archiveBucket) int {
		if c := strings.Compare(a.cfg.SchemaSlug, b.cfg.SchemaSlug); c != 0 {
			return c
		}
		return cmp.Compare(b.n, a.n)
	})
	for _, b := range list {
		path := b.cfg.ArchiveGroups.URL(b.cfg.ArchivePath, b.n)
		g := ensure(b.cfg.SitemapGroup)
		te := &entity.SitemapEntry{Slug: strings.TrimPrefix(path, "/"), UpdatedAt: b.newest}
		p.addGroupRow(g, groupRow{e: te, path: path, priority: sitemapIndexPriority, changeFreq: sitemapIndexChangeFreq})
		if len(b.newest) < 10 {
			continue
		}
		// The bare group and its per-locale files (their archive path carries
		// the locale segment).
		for _, f := range p.byKey {
			if f.base == g.base && strings.HasSuffix(f.archive, b.cfg.ArchivePath) && f.archive != "" && b.newest[:10] > f.archiveMod {
				f.archiveMod = b.newest[:10]
			}
		}
	}
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
	if p.sitemapSettings().ExcludedPath(r.path) {
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
//
// The site-wide default sits between them: a saved value replaces the
// repository default of every type, a type's own setting still wins.
func sitemapStats(e *entity.SitemapEntry, cfg *entity.SchemaURLConfig, known bool, sm *seo.Sitemap) (float64, string) {
	priority, changeFreq := e.Priority, e.ChangeFreq
	if sm != nil {
		if sm.DefaultPriority != nil {
			priority = *sm.DefaultPriority
		}
		if sm.DefaultChangeFreq != "" {
			changeFreq = sm.DefaultChangeFreq
		}
	}
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
	if g.chunks < 2 {
		return p.groupURLs(g), true
	}
	src := g
	if g.chunkOf != nil {
		src = g.chunkOf
	}
	limit := p.sitemapSettings().EffectiveMaxURLs()
	all := p.rendered[src]
	return all[min(g.chunk*limit, len(all)):min((g.chunk+1)*limit, len(all))], true
}

// groupURLs lists every URL of g, whatever the file limit.
func (p *sitemapPlan) groupURLs(g *sitemapGroup) (urls []URL) {
	p.renders++
	key := g.key
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
		return urls
	}

	if g.base == miscSitemapGroupKey || key == miscSitemapGroupKey {
		if g.locale != "" && g.locale != p.locales().Default() {
			return p.rowURLs(g, urls)
		}
		for _, r := range p.in.Plugins.Static {
			loc := fmt.Sprintf("%s/%s", base, r.Path)
			if p.sitemapSettings().ExcludedPath("/" + r.Path) {
				continue
			}
			prio, cf := p.stats(r.Priority, r.ChangeFreq)
			urls = append(urls, URL{Loc: loc, LastMod: p.in.Today, ChangeFreq: cf, Priority: prio})
		}
	}

	return p.rowURLs(g, urls)
}

// noIndexEntry asks the head's predicate whether the settings keep e's page
// out of search: a person is served as an author page, the rest as entries.
func (p *sitemapPlan) noIndexEntry(e *entity.SitemapEntry) bool {
	kind := seo.KindSingle
	if e.SchemaSlug == entity.PersonSchemaSlug {
		kind = seo.KindAuthor
	}
	return p.in.SEO.NoIndexView(string(kind), e.SchemaSlug, "")
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
	if g.archive != "" && !sectionPaths[g.archive] && len(g.rows) > 0 &&
		!p.in.SEO.NoIndexView(string(seo.KindArchive), g.archiveSchema, "") &&
		!p.sitemapSettings().ExcludedPath(g.archive) {
		loc := base + g.archive
		prio, cf := p.stats(sitemapIndexPriority, sitemapIndexChangeFreq)
		lastMod := g.archiveMod
		if lastMod == "" {
			lastMod = p.in.Today
		}
		urls = append(urls, URL{Loc: loc, LastMod: lastMod, ChangeFreq: cf, Priority: prio})
	}
	for _, pass := range []bool{true, false} { // section pages, then the rest
		for _, r := range g.rows {
			if r.section != pass {
				continue
			}
			loc := base + r.path
			prio, cf := p.stats(r.priority, r.changeFreq)
			urls = append(urls, URL{Loc: loc, LastMod: p.entryDate(r.e), ChangeFreq: cf, Priority: prio, Links: p.alternates(r.e), Images: imageRefs(r.images)})
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
	var alts []render.Alternate
	for _, tag := range set.PublicTags() {
		for _, c := range cs {
			if c.locale != tag {
				continue
			}
			href := p.in.BaseURL + c.path
			out = append(out, HrefLang{Rel: "alternate", HrefLang: tag, Href: href})
			alts = append(alts, render.Alternate{Locale: tag, Href: href, Default: tag == set.Default()})
			break
		}
	}
	if len(out) < 2 {
		return nil
	}
	return append(out, HrefLang{Rel: "alternate", HrefLang: "x-default", Href: xDefaultHref(set, alts, func(render.Alternate) bool { return true })})
}

// sitemapSettings returns the site-wide sitemap settings; nil means none are
// saved, which every method of seo.Sitemap treats as the defaults.
func (p *sitemapPlan) sitemapSettings() *seo.Sitemap {
	if p.in.SEO == nil {
		return nil
	}
	return &p.in.SEO.Sitemap
}

// stats is the priority and changefreq a URL is written with: both left out
// when the site switched them off.
func (p *sitemapPlan) stats(priority float64, changeFreq string) (*float64, string) {
	if sm := p.sitemapSettings(); sm != nil && sm.NoStats {
		return nil, ""
	}
	return ptr(priority), changeFreq
}

// images lists the absolute image URLs of e when the image entries are on:
// og_image, featured_image and, for a person, photo, without duplicates, at
// most maxSitemapImages. A value that is neither absolute nor site-relative
// is skipped.
func (p *sitemapPlan) images(e *entity.SitemapEntry) []string {
	if sm := p.sitemapSettings(); sm == nil || !sm.Images {
		return nil
	}
	keys := []string{"og_image", "featured_image"}
	if e.SchemaSlug == entity.PersonSchemaSlug {
		keys = append(keys, "photo")
	}
	var out []string
	for _, k := range keys {
		v := strings.TrimSpace(e.Attrs[k])
		switch {
		case strings.HasPrefix(v, "http://"), strings.HasPrefix(v, "https://"):
		case strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//"):
			v = p.in.BaseURL + v
		default:
			continue
		}
		if !slices.Contains(out, v) && len(out) < maxSitemapImages {
			out = append(out, v)
		}
	}
	return out
}

func imageRefs(locs []string) []ImageRef {
	if len(locs) == 0 {
		return nil
	}
	refs := make([]ImageRef, len(locs))
	for i, l := range locs {
		refs[i] = ImageRef{Loc: l}
	}
	return refs
}
