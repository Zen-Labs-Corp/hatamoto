package server

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/llmstxt"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/seo"
)

const (
	// llmsBatch is how many entities one body load reads.
	llmsBatch = 200
	// llmsDescRunes caps one item description.
	llmsDescRunes = 200
	// llmsFallbackRunes bounds the plain text a fallback description is cut from.
	llmsFallbackRunes = 400
)

// llmsRef is one listed entity: its sitemap entry and the public path the
// sitemap plan gave it.
type llmsRef struct {
	entry  *entity.SitemapEntry
	path   string
	locale string // resolved tag; "" on a single-locale site
}

func (r llmsRef) key() string { return r.locale + "|" + r.entry.ID.String() }

// llmsEnt is what the files need of a loaded entity.
type llmsEnt struct {
	name, schema, desc, job, updated string
}

// llmsModel is everything both files are built from. The sections hold the
// listed items; for the full file the same items, with every post, are
// loaded again for their bodies.
type llmsModel struct {
	site     llmstxt.Site
	sections []llmstxt.Section
	// excluded maps a snapshot key to why a still-published entity is not listed.
	excluded map[string]string
	locales  *entity.LocaleSet
	// degraded is set when an input fell back to a default (URL registry or
	// site settings unreadable): the file is served but never cached or recorded.
	degraded bool
}

// loadLLMs gathers the model. The URLs and the exclusions come from the
// sitemap plan (registry only, locale prefixes, homepage "/"), narrowed by the
// llms filter. allPosts lists every post, not only the newest set.Posts.
func (s *Server) loadLLMs(ctx context.Context, base string, set seo.AIFiles, allPosts bool) (*llmsModel, error) {
	in, err := s.loadSitemapInputFor(ctx, base)
	if err != nil {
		return nil, err
	}
	plan := planSitemap(in)
	locales := in.Snap.Locales()
	def := locales.Default()
	resolve := llmsLocaleResolver(locales)

	paths := llmsPaths(plan)
	kept := map[*entity.SitemapEntry]bool{}
	for _, e := range llmsEntries(in.Entries, in.SEO) {
		kept[e] = true
	}
	m := &llmsModel{excluded: map[string]string{}, locales: locales, degraded: in.NamingUnread}
	var refs []llmsRef
	for _, e := range in.Entries {
		if e.ID == uuid.Nil {
			continue
		}
		if p, ok := paths[e]; ok && kept[e] {
			refs = append(refs, llmsRef{entry: e, path: p, locale: resolve(e)})
			continue
		}
		switch {
		case !e.IncludeInLLMs:
			m.excluded[resolve(e)+"|"+e.ID.String()] = "opted out"
		case e.NoIndex || in.SEO.NoIndexType(e.SchemaSlug):
			m.excluded[resolve(e)+"|"+e.ID.String()] = "noindex"
		}
	}

	postSlugs := []string{feedPostSchema}
	if sl := in.Snap.Router().SchemaSlugs(feedPostSchema); len(sl) > 0 {
		postSlugs = sl
	}
	isPost := func(r llmsRef) bool { return slices.Contains(postSlugs, r.entry.SchemaSlug) }

	// Newest posts per locale; the rest stay out of llms.txt.
	if !allPosts {
		refs = limitPosts(refs, isPost, set.EffectivePosts())
	}

	// The full file reads each body again later, under its cap, and never prints
	// a description: its first pass only needs what ordering and titles use.
	loadEnts := s.loadLLMsEnts
	if allPosts {
		loadEnts = s.loadLLMsNames
	}
	ents, err := loadEnts(ctx, locales, refs)
	if err != nil {
		return nil, err
	}

	var siteOK bool
	m.site, siteOK = s.llmsSite(ctx, base, set)
	m.degraded = m.degraded || !siteOK
	m.site.FullOn = !set.FullOff
	_, m.site.HasFeed = in.Snap.Config(feedPostSchema)

	rank := s.headerMenuRank(ctx)
	tags := []string{def}
	if locales.Multi() {
		for _, t := range locales.PublicTags() {
			if t != def {
				tags = append(tags, t)
			}
		}
	}
	for _, tag := range tags {
		suffix, loc := "", ""
		if tag != def {
			suffix, loc = " ("+locales.DisplayName(tag)+")", tag
		}
		var own []llmsRef
		for _, r := range refs {
			if r.locale == tag {
				own = append(own, r)
			}
		}
		m.sections = append(m.sections, llmsSections(plan, in, own, ents, rank, isPost, suffix, loc)...)
	}
	return m, nil
}

// llmsLocaleResolver returns the bucket tag of an entry. A site with no locales
// configured has an empty default tag while its entries carry the site language
// (the column default), so every entry shares the one default bucket there.
func llmsLocaleResolver(locales *entity.LocaleSet) func(*entity.SitemapEntry) string {
	def := locales.Default()
	multi := locales.Multi()
	return func(e *entity.SitemapEntry) string {
		if !multi || e.Locale == "" || e.Locale == entity.LocaleShared {
			return def
		}
		return e.Locale
	}
}

// llmsPaths maps every listed entry to its public path, over every group the
// plan holds (localized files and the folded legacy ones included).
func llmsPaths(plan *sitemapPlan) map[*entity.SitemapEntry]string {
	out := map[*entity.SitemapEntry]string{}
	var walk func(g *sitemapGroup)
	walk = func(g *sitemapGroup) {
		for _, row := range g.rows {
			if row.e == nil || row.e.ID == uuid.Nil {
				continue
			}
			if _, ok := out[row.e]; !ok {
				out[row.e] = row.path
			}
		}
		for _, f := range g.folded {
			walk(f)
		}
	}
	for _, g := range plan.groups {
		walk(g)
	}
	return out
}

// limitPosts keeps the newest n posts of each locale and every other ref.
func limitPosts(refs []llmsRef, isPost func(llmsRef) bool, n int) []llmsRef {
	byLocale := map[string][]llmsRef{}
	for _, r := range refs {
		if isPost(r) {
			byLocale[r.locale] = append(byLocale[r.locale], r)
		}
	}
	keep := map[*entity.SitemapEntry]bool{}
	for _, posts := range byLocale {
		sortPosts(posts)
		for i := 0; i < len(posts) && i < n; i++ {
			keep[posts[i].entry] = true
		}
	}
	out := refs[:0:0]
	for _, r := range refs {
		if !isPost(r) || keep[r.entry] {
			out = append(out, r)
		}
	}
	return out
}

// sortPosts orders newest first by publication date, then by path for a
// stable result.
func sortPosts(posts []llmsRef) {
	sort.SliceStable(posts, func(i, j int) bool {
		a, b := posts[i].entry.PublishedAt, posts[j].entry.PublishedAt
		if !a.Equal(b) {
			return a.After(b)
		}
		return posts[i].path < posts[j].path
	})
}

// llmsSections builds one locale's sections: key pages, other pages, one
// section per other type, posts, people.
func llmsSections(plan *sitemapPlan, in sitemapInput, refs []llmsRef, ents map[uuid.UUID]llmsEnt, rank map[uuid.UUID]int, isPost func(llmsRef) bool, suffix, locale string) []llmstxt.Section {
	item := func(r llmsRef) llmstxt.Item {
		e := ents[r.entry.ID]
		name := e.name
		if name == "" {
			name = r.entry.Slug
		}
		return llmstxt.Item{Key: r.key(), EntityID: r.entry.ID.String(), Name: name, Path: r.path, Desc: e.desc, Job: e.job}
	}
	byName := func(rs []llmsRef) {
		sort.SliceStable(rs, func(i, j int) bool {
			a, b := strings.ToLower(ents[rs[i].entry.ID].name), strings.ToLower(ents[rs[j].entry.ID].name)
			if a != b {
				return a < b
			}
			return rs[i].path < rs[j].path
		})
	}
	var home *llmsRef
	var top, pages, posts, people []llmsRef
	types := map[string][]llmsRef{}
	for i, r := range refs {
		switch {
		case plan.isHomepage(r.entry):
			if home == nil {
				home = &refs[i]
			}
		case r.entry.SchemaSlug == "page" && r.entry.ParentSlug == "":
			top = append(top, r)
		case r.entry.SchemaSlug == "page":
			pages = append(pages, r)
		case isPost(r):
			posts = append(posts, r)
		case r.entry.SchemaSlug == entity.PersonSchemaSlug:
			people = append(people, r)
		default:
			types[r.entry.SchemaSlug] = append(types[r.entry.SchemaSlug], r)
		}
	}
	// Header menu order (default locale only: menus are not per-locale), then title.
	byName(top)
	if suffix == "" {
		sort.SliceStable(top, func(i, j int) bool {
			a, aok := rank[top[i].entry.ID]
			b, bok := rank[top[j].entry.ID]
			switch {
			case aok && bok:
				return a < b
			default:
				return aok && !bok
			}
		})
	}
	byName(pages)
	sortPosts(posts)
	byName(people)

	mk := func(title, kind string, rs []llmsRef) llmstxt.Section {
		sec := llmstxt.Section{Title: title + suffix, Locale: locale, Kind: kind}
		for _, r := range rs {
			sec.Items = append(sec.Items, item(r))
		}
		return sec
	}
	var out []llmstxt.Section
	keyRefs := top
	if home != nil {
		keyRefs = append([]llmsRef{*home}, top...)
	}
	out = append(out, mk("Key pages", llmstxt.KindKey, keyRefs), mk("Pages", llmstxt.KindPages, pages))

	type typed struct {
		title string
		refs  []llmsRef
	}
	var ts []typed
	for slug, rs := range types {
		title := slug
		if cfg, ok := in.Snap.Config(slug); ok && cfg.ArchiveTitle != "" {
			title = cfg.ArchiveTitle
		} else if n := ents[rs[0].entry.ID].schema; n != "" {
			title = n
		}
		byName(rs)
		ts = append(ts, typed{title, rs})
	}
	sort.Slice(ts, func(i, j int) bool { return strings.ToLower(ts[i].title) < strings.ToLower(ts[j].title) })
	for _, t := range ts {
		out = append(out, mk(t.title, llmstxt.KindType, t.refs))
	}
	out = append(out, mk("Posts", llmstxt.KindPosts, posts), mk("People", llmstxt.KindPeople, people))
	return out
}

// headerMenuRank is the reading position of each entity the header menu links
// to, in menu order (top level first, then each item's children).
func (s *Server) headerMenuRank(ctx context.Context) map[uuid.UUID]int {
	rank := map[uuid.UUID]int{}
	m := s.menusAt(ctx, "header")["header"]
	if m == nil {
		return rank
	}
	var walk func(items []*menu.MenuItem)
	walk = func(items []*menu.MenuItem) {
		items = slices.Clone(items)
		sort.SliceStable(items, func(i, j int) bool { return items[i].Position < items[j].Position })
		for _, it := range items {
			if it.LinkType == "entity" && it.EntityID != nil {
				if _, ok := rank[*it.EntityID]; !ok {
					rank[*it.EntityID] = len(rank)
				}
			}
		}
		for _, it := range items {
			walk(it.Children)
		}
	}
	walk(m.Items)
	return rank
}

// llmsSite reads the file-level text: name, summary and the details paragraph.
// ok is false when the site settings could not be read.
func (s *Server) llmsSite(ctx context.Context, base string, set seo.AIFiles) (site llmstxt.Site, ok bool) {
	site = llmstxt.Site{BaseURL: base}
	ok = true
	var prof bizprofile.Profile
	tagline, company := "", ""
	if s.siteConfig != nil {
		if cfg, err := s.siteConfig.Get(ctx); err == nil {
			company, tagline = strings.TrimSpace(cfg.CompanyName), strings.TrimSpace(cfg.Tagline)
			if p, perr := bizprofile.Parse(cfg.BusinessProfile); perr == nil {
				prof = bizprofile.FromLegacy(p, cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
			}
		} else {
			ok = false
			if s.logger != nil {
				s.logger.Warn("llms: site config unreadable", "error", err)
			}
		}
	}
	site.Name = firstText(company, strings.TrimSpace(prof.Name), hostOf(base))
	site.Summary = firstText(strings.TrimSpace(set.Summary), tagline, strings.TrimSpace(prof.Description))
	site.Details = llmsDetails(prof)
	return site, ok
}

func firstText(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func hostOf(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
}

// llmsDetails is the paragraph under the summary: location, contact, hours.
func llmsDetails(p bizprofile.Profile) string {
	var addr, contact, hours string
	if parts := nonEmpty(p.Address.City, firstText(p.Address.Country, p.CountryName)); len(parts) > 0 {
		addr = "Based in " + strings.Join(parts, ", ") + "."
	}
	if parts := nonEmpty(p.Email, p.Phone); len(parts) > 0 {
		contact = strings.Join(parts, ", ") + "."
	}
	if h := firstText(strings.TrimSpace(p.HoursNote), openingHours(p.OpeningHours)); h != "" {
		hours = "Open " + strings.TrimRight(h, ".") + "."
	}
	return llmstxt.Details(addr, contact, hours)
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// openingHours writes the structured hours as "Mon-Fri 09:00-18:00, Sat 10:00-14:00".
func openingHours(hs []bizprofile.Hours) string {
	var out []string
	for _, h := range hs {
		if len(h.Days) == 0 || h.Opens == "" || h.Closes == "" {
			continue
		}
		out = append(out, dayRange(h.Days)+" "+h.Opens+"-"+h.Closes)
	}
	return strings.Join(out, ", ")
}

// dayRange joins weekdays: a consecutive run becomes "Mon-Fri".
func dayRange(days []string) string {
	idx := make([]int, 0, len(days))
	for _, d := range days {
		if i := slices.Index(bizprofile.Days, d); i >= 0 {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)
	short := func(i int) string { return bizprofile.Days[i][:3] }
	switch {
	case len(idx) == 0:
		return ""
	case len(idx) == 1:
		return short(idx[0])
	case idx[len(idx)-1]-idx[0] == len(idx)-1 && len(idx) > 2:
		return short(idx[0]) + "-" + short(idx[len(idx)-1])
	}
	names := make([]string, len(idx))
	for i, d := range idx {
		names[i] = short(d)
	}
	return strings.Join(names, ", ")
}

// llmsCtx makes the entity reads of a build see the same locales the sitemap does.
func llmsCtx(ctx context.Context, locales *entity.LocaleSet) context.Context {
	if locales.Multi() {
		return entity.WithVisibleLocaleSet(ctx, locales.PublicTags()...)
	}
	return ctx
}

// loadLLMsEnts reads title, description and job title of every ref, in
// batches. Bodies are only read when a description has to be cut from one.
func (s *Server) loadLLMsEnts(ctx context.Context, locales *entity.LocaleSet, refs []llmsRef) (map[uuid.UUID]llmsEnt, error) {
	ctx = llmsCtx(ctx, locales)
	repo := entity.NewPostgresPublicRepository(s.db)
	out := make(map[uuid.UUID]llmsEnt, len(refs))
	ids := make([]uuid.UUID, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.entry.ID)
	}
	resolver := s.llmsResolver(ctx)
	for len(ids) > 0 {
		n := min(llmsBatch, len(ids))
		got, err := repo.GetPublishedByIDs(ctx, ids[:n])
		if err != nil {
			return nil, err
		}
		for _, e := range got {
			out[e.ID] = describeEntity(e, resolver)
		}
		ids = ids[n:]
	}
	return out, nil
}

// loadLLMsNames is loadLLMsEnts with names only (title and schema name): no
// attributes are read and no description is cut.
func (s *Server) loadLLMsNames(ctx context.Context, locales *entity.LocaleSet, refs []llmsRef) (map[uuid.UUID]llmsEnt, error) {
	ctx = llmsCtx(ctx, locales)
	repo := entity.NewPostgresPublicRepository(s.db)
	out := make(map[uuid.UUID]llmsEnt, len(refs))
	ids := make([]uuid.UUID, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.entry.ID)
	}
	for len(ids) > 0 {
		n := min(llmsBatch, len(ids))
		got, err := repo.GetPublishedNames(ctx, ids[:n])
		if err != nil {
			return nil, err
		}
		for id, nm := range got {
			out[id] = llmsEnt{name: strings.TrimSpace(nm.Name), schema: nm.SchemaName}
		}
		ids = ids[n:]
	}
	return out, nil
}

// describeEntity picks the list-line facts of one entity.
func describeEntity(e *entity.Entity, resolve llmstxt.RefResolver) llmsEnt {
	attrs := entityAttrs(e)
	desc, _ := metaDescriptionAndImage(attrs, e.Schema)
	if strings.TrimSpace(desc) == "" {
		desc = fallbackDescription(attrs, resolve)
	}
	var schema string
	if e.Schema != nil {
		schema = e.Schema.Name
	}
	return llmsEnt{
		name:    strings.TrimSpace(e.Name),
		schema:  schema,
		desc:    llmstxt.OneLine(desc, llmsDescRunes),
		job:     llmstxt.OneLine(strAttr(attrs, "job_title"), llmsDescRunes),
		updated: e.UpdatedAt.UTC().Format("2006-01-02"),
	}
}

func entityAttrs(e *entity.Entity) map[string]any {
	var attrs map[string]any
	if len(e.Attributes) > 0 {
		_ = jsonx.Unmarshal(e.Attributes, &attrs, jsonx.StoredLoose)
	}
	return attrs
}

// markdownInline matches the inline Markdown a builder doc paragraph can carry
// (link text kept, emphasis and code marks dropped).
var markdownInline = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)|\*+|__|` + "`" + `+`)

// fallbackDescription is the first sentence of the body as plain text.
func fallbackDescription(attrs map[string]any, resolve llmstxt.RefResolver) string {
	if c := strAttr(attrs, "content"); c != "" {
		return llmstxt.FirstSentence(plainText(c, llmsFallbackRunes))
	}
	if doc := pageDoc(attrs); doc != nil {
		for _, block := range strings.Split(llmstxt.DocToMarkdown(doc, resolve), "\n\n") {
			if b := strings.TrimSpace(block); b != "" && !strings.ContainsAny(b[:1], "#!-") {
				return llmstxt.FirstSentence(markdownInline.ReplaceAllString(b, "$1"))
			}
		}
	}
	return ""
}

// pageDoc parses the stored builder doc of an entity, nil when it has none.
func pageDoc(attrs map[string]any) *builder.Doc {
	raw, ok := attrs["page_doc"]
	if !ok || raw == nil {
		return nil
	}
	b, err := jsonx.Marshal(raw)
	if err != nil {
		return nil
	}
	doc, err := builder.ParseExact(b)
	if err != nil {
		return nil
	}
	return doc
}

// llmsResolver expands a block reference through the same lookup the renderer
// uses; nil result = unknown block.
func (s *Server) llmsResolver(ctx context.Context) llmstxt.RefResolver {
	if s.renderData == nil {
		return nil
	}
	return func(id string) *builder.Doc {
		if d, ok := s.renderData.Block(ctx, id); ok {
			return d
		}
		return nil
	}
}

// llmsBody is the Markdown body of one entity: content, else the builder doc,
// else a person's bio, else the description. Never themed HTML.
func llmsBody(e *entity.Entity, base string, resolve llmstxt.RefResolver) string {
	attrs := entityAttrs(e)
	if c := strAttr(attrs, "content"); c != "" {
		return llmstxt.HTMLToMarkdown(absolutizeHTML(c, base))
	}
	if doc := pageDoc(attrs); doc != nil {
		if md := llmstxt.DocToMarkdown(doc, resolve); strings.TrimSpace(md) != "" {
			return md
		}
	}
	if bio := strAttr(attrs, "bio"); bio != "" {
		return llmstxt.HTMLToMarkdown(absolutizeHTML(bio, base))
	}
	desc, _ := metaDescriptionAndImage(attrs, e.Schema)
	return strings.TrimSpace(desc)
}

// buildLLMsIndex renders /llms.txt; the model is returned for the snapshot.
func (s *Server) buildLLMsIndex(ctx context.Context, base string, set seo.AIFiles) ([]byte, *llmsModel, error) {
	m, err := s.loadLLMs(ctx, base, set, false)
	if err != nil {
		return nil, nil, err
	}
	return llmstxt.BuildIndex(m.site, m.sections), m, nil
}

// buildLLMsFull renders /llms-full.txt: every listed page's body in section
// order, loaded in batches until the cap is hit; the rest is only counted.
func (s *Server) buildLLMsFull(ctx context.Context, base string, set seo.AIFiles) (body []byte, stats llmstxt.FullStats, degraded bool, err error) {
	m, err := s.loadLLMs(ctx, base, set, true)
	if err != nil {
		return nil, llmstxt.FullStats{}, false, err
	}
	type listed struct {
		item   llmstxt.Item
		locale string
	}
	var flat []listed
	seen := map[string]bool{}
	for _, sec := range m.sections {
		for _, it := range sec.Items {
			if !seen[it.Key] {
				seen[it.Key] = true
				flat = append(flat, listed{it, sec.Locale})
			}
		}
	}
	ctx = llmsCtx(ctx, m.locales)
	repo := entity.NewPostgresPublicRepository(s.db)
	resolver := s.llmsResolver(ctx)
	w := llmstxt.NewFullWriter(m.site, set.EffectiveFullCapBytes())
	offered := 0
	for start := 0; start < len(flat); start += llmsBatch {
		end := min(start+llmsBatch, len(flat))
		ids := make([]uuid.UUID, 0, end-start)
		for _, l := range flat[start:end] {
			if id, err := uuid.Parse(l.item.EntityID); err == nil {
				ids = append(ids, id)
			}
		}
		got, err := repo.GetPublishedByIDs(ctx, ids)
		if err != nil {
			return nil, llmstxt.FullStats{}, false, err
		}
		byID := make(map[string]*entity.Entity, len(got))
		for _, e := range got {
			byID[e.ID.String()] = e
		}
		full := false
		for _, l := range flat[start:end] {
			offered++
			e := byID[l.item.EntityID]
			if e == nil {
				continue // unpublished between the two reads
			}
			page := llmstxt.Page{
				Name: l.item.Name, Path: l.item.Path, Locale: l.locale,
				Updated: e.UpdatedAt.UTC().Format(time.DateOnly), Body: llmsBody(e, base, resolver),
			}
			if !w.Add(page) {
				full = true
				break
			}
		}
		if full {
			break
		}
	}
	out, stats := w.Finish(len(flat) - offered)
	return out, stats, m.degraded, nil
}
