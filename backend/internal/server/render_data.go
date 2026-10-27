package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/taxonomy"
)

// renderData feeds the recent, terms, menu and srcset template funcs from the
// database. Every method is fail-open: an error is logged and yields no data,
// so a slow or failing query costs a widget, never the page.
type renderData struct {
	s *Server

	// failures counts data lookups that failed (query error or timeout). A
	// caller that sees it move while rendering a page must not cache the page:
	// a transient failure would otherwise stick as an empty widget for the TTL.
	failures atomic.Int64

	mu       sync.Mutex
	variants map[string]cachedVariants
}

// cachedVariants is the cached media lookup of one URL: srcset candidates and
// the original's intrinsic size (0 when unknown; a file can have a size and no copies).
type cachedVariants struct {
	list    []render.ImageVariant
	w, h    int
	expires time.Time
}

const (
	variantsCacheTTL  = time.Minute
	variantsCacheSize = 2000
)

func newRenderData(s *Server) *renderData { return &renderData{s: s} }

func (d *renderData) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), dataFuncTimeout)
}

// Recent lists the newest published entities of a schema (and its aliases).
func (d *renderData) Recent(schema string, limit int) []render.RelatedItem {
	ctx, cancel := d.ctx()
	defer cancel()
	snap := d.s.urlSnapshot(ctx)
	repo := entity.NewPostgresPublicRepository(d.s.db)
	rows, err := repo.ListPublishedBySchemasNoCount(ctx, schemaSlugs(snap, schema), limit)
	if err != nil {
		d.fail()
		d.s.logger.Warn("render: recent", "error", err, "schema", schema)
		return nil
	}
	items, err := d.s.listingItems(ctx, repo, rows)
	if err != nil {
		d.fail()
		d.s.logger.Warn("render: recent urls", "error", err, "schema", schema)
		return nil
	}
	return items
}

// schemaSlugs is a schema slug plus its aliases (just the slug when unknown).
func schemaSlugs(snap *entity.URLSnapshot, schema string) []string {
	if sl := snap.Router().SchemaSlugs(schema); len(sl) > 0 {
		return sl
	}
	return []string{schema}
}

// Query lists one page of published entities of a schema (and its aliases):
// the count and the rows come from the same filter, the rows in one query and
// their URLs (ancestors) and bylines in batch (no query per item). Limit and page are
// clamped here as well as by the funcs, and Page is held within Pages. An
// unknown author or term, or a failed lookup, gives no items.
func (d *renderData) Query(q render.QuerySpec) render.QueryResult {
	limit := min(max(q.Limit, 1), render.MaxQueryLimit)
	page := min(max(q.Page, 1), render.MaxQueryPage)
	empty := render.QueryResult{Page: 1, Pages: 1, Limit: limit}
	ctx, cancel := d.ctx()
	defer cancel()
	f := entity.ListFilter{
		SchemaSlugs: schemaSlugs(d.s.urlSnapshot(ctx), q.Schema),
		Attr:        q.Attr, AttrEq: q.Eq, AttrEqSet: q.EqSet, Order: q.Order,
		IncludeScheduled: q.IncludeScheduled,
		Offset:           min(max(q.Offset, 0), entity.MaxListOffset),
	}
	if id, err := uuid.Parse(q.ExcludeID); err == nil {
		f.ExcludeID = id
	}
	if q.Author != "" {
		id, ok := d.authorID(ctx, q.Author)
		if !ok {
			return empty
		}
		f.PersonID = id
	}
	if q.Term != "" {
		id, ok := d.termID(ctx, q.Term)
		if !ok {
			return empty
		}
		f.TermID = id
	}
	repo := entity.NewPostgresPublicRepository(d.s.db)
	rows, total, err := repo.ListPublishedFiltered(ctx, f, page, limit)
	if err == nil {
		if pages := queryPages(total, f.Offset, limit); page > pages {
			page = pages
			if total > f.Offset { // past the end: show the last page, not nothing
				rows, total, err = repo.ListPublishedFiltered(ctx, f, page, limit)
			}
		}
	}
	if err != nil {
		d.fail()
		d.s.logger.Warn("render: query", "error", err, "schema", q.Schema)
		return empty
	}
	items, err := d.s.listingItems(ctx, repo, rows, queryAttrKeys(q)...)
	if err != nil {
		d.fail()
		d.s.logger.Warn("render: query urls", "error", err, "schema", q.Schema)
		return empty
	}
	return render.QueryResult{
		Items: items,
		Total: total, Page: page, Pages: queryPages(total, f.Offset, limit), Limit: limit,
	}
}

// queryPages is the number of pages of total matches after skipping offset
// (at least 1).
func queryPages(total, offset, limit int) int {
	return max((max(total-offset, 0)+limit-1)/limit, 1)
}

// queryAttrKeys are the attribute keys a query filters or orders by.
func queryAttrKeys(q render.QuerySpec) []string {
	var keys []string
	if q.Attr != "" {
		keys = append(keys, q.Attr)
	}
	if k, ok := strings.CutPrefix(strings.TrimPrefix(q.Order, "-"), "attr:"); ok && k != "" {
		keys = append(keys, k)
	}
	return keys
}

// authorID resolves the slug of a public person; ok is false when it names
// none.
func (d *renderData) authorID(ctx context.Context, slug string) (uuid.UUID, bool) {
	p, err := d.s.people().GetBySlug(ctx, slug)
	if err != nil {
		if !errors.Is(err, person.ErrNotFound) {
			d.fail()
			d.s.logger.Warn("render: query author", "error", err)
		}
		return uuid.Nil, false
	}
	return p.ID, p.Public
}

// termID resolves a "<kind>:<slug>" term; ok is false when it names no term.
func (d *renderData) termID(ctx context.Context, ref string) (uuid.UUID, bool) {
	kind, slug, found := strings.Cut(ref, ":")
	if !found || !taxonomy.ValidKind(kind) || !taxonomy.ValidSlug(slug) {
		return uuid.Nil, false
	}
	t, err := taxonomy.NewPostgresRepository(d.s.db).GetTermBySlug(ctx, kind, slug)
	if err != nil {
		if !errors.Is(err, taxonomy.ErrTermNotFound) {
			d.fail()
			d.s.logger.Warn("render: query term", "error", err, "kind", kind)
		}
		return uuid.Nil, false
	}
	return t.ID, true
}

// AttrValues lists the distinct string values of an attribute with counts.
func (d *renderData) AttrValues(schema, attr string, limit int) []render.AttrCount {
	ctx, cancel := d.ctx()
	defer cancel()
	rows, err := entity.NewPostgresPublicRepository(d.s.db).
		ListAttrValues(ctx, schemaSlugs(d.s.urlSnapshot(ctx), schema), attr, limit)
	if err != nil {
		d.fail()
		d.s.logger.Warn("render: attr values", "error", err, "schema", schema)
		return nil
	}
	out := make([]render.AttrCount, len(rows))
	for i, r := range rows {
		out[i] = render.AttrCount{Value: r.Value, Count: r.Count}
	}
	return out
}

// Terms lists the terms of a kind that have published entities.
func (d *renderData) Terms(kind string, limit int) []render.TermView {
	if !taxonomy.ValidKind(kind) {
		return nil
	}
	ctx, cancel := d.ctx()
	defer cancel()
	terms, err := taxonomy.NewPostgresRepository(d.s.db).ListPublicTerms(ctx, kind, limit)
	if err != nil {
		d.fail()
		d.s.logger.Warn("render: terms", "error", err, "kind", kind)
		return nil
	}
	out := make([]render.TermView, 0, len(terms))
	flat := make([]*taxonomy.Term, len(terms))
	for i, t := range terms {
		flat[i] = &t.Term
	}
	if err := taxonomy.FillPaths(ctx, taxonomy.NewPostgresRepository(d.s.db), flat...); err != nil {
		d.s.logger.Warn("render: term paths", "error", err, "kind", kind)
	}
	parents := termParents(ctx, d.s.db, flat)
	for _, t := range terms {
		tv := termView(&t.Term, parents)
		tv.Count = t.EntityCount
		out = append(out, tv)
	}
	return out
}

// Menu returns the menu at a location.
func (d *renderData) Menu(location string) []render.NavItem {
	ctx, cancel := d.ctx()
	defer cancel()
	items := d.s.menuNav(ctx, location, d.s.newNavLinks(ctx))
	if ctx.Err() != nil {
		d.fail()
	}
	return items
}

func (d *renderData) fail() { d.failures.Add(1) }

// Failures is the number of failed lookups so far (see failures).
func (d *renderData) Failures() int64 { return d.failures.Load() }

// ImageVariants returns the resized copies of a stored media file, found by
// its public URL (or the path part of it), smallest first. A file with no
// copies, and any URL that is not stored media, gives none.
func (d *renderData) ImageVariants(u string) []render.ImageVariant {
	c, _ := d.media(u)
	return c.list
}

// ImageSize returns the intrinsic size of a stored media file (see
// render.ImageSizer); ok is false for a URL that is not stored media or whose
// size is unknown.
func (d *renderData) ImageSize(u string) (w, h int, ok bool) {
	c, _ := d.media(u)
	return c.w, c.h, c.w > 0 && c.h > 0
}

// media returns the cached lookup of u, reading it when missing or expired.
// ok is false for a URL that cannot be media and when the lookup failed (then
// nothing is remembered).
func (d *renderData) media(u string) (c cachedVariants, ok bool) {
	if !strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "http") {
		return cachedVariants{}, false
	}
	d.mu.Lock()
	c, ok = d.variants[u]
	d.mu.Unlock()
	if ok && time.Now().Before(c.expires) {
		return c, true
	}
	c, ok = d.lookupVariants(u)
	if !ok { // lookup failed: serve without copies, but do not remember that
		return c, false
	}
	c.expires = time.Now().Add(variantsCacheTTL)
	d.mu.Lock()
	if d.variants == nil || len(d.variants) >= variantsCacheSize {
		d.variants = map[string]cachedVariants{}
	}
	d.variants[u] = c
	d.mu.Unlock()
	return c, true
}

// maxPrefetchVariants is the most URLs one prefetch statement looks up (the loop cap).
const maxPrefetchVariants = 48

// variantsQuery matches each reversed-URL pattern with its own OR'ed LIKE: the
// planner turns each into a range on idx_hatamoto_media_public_url_rev
// (BitmapOr), which LIKE ANY(array) cannot use.
func variantsQuery(patterns []string) (string, []any) {
	var b strings.Builder
	b.WriteString("SELECT public_url, variants, width, height FROM hatamoto_media WHERE ")
	args := make([]any, len(patterns))
	for i, p := range patterns {
		if i > 0 {
			b.WriteString(" OR ")
		}
		fmt.Fprintf(&b, "reverse(public_url) LIKE $%d", i+1)
		args[i] = p
	}
	b.WriteString(" ORDER BY created_at DESC")
	return b.String(), args
}

// PrefetchVariants fills the variants cache for urls with one query. It is
// fail-open and does not mark the render failed: a later single lookup still
// works (and reports its own failure).
func (d *renderData) PrefetchVariants(urls []string) {
	var want, patterns []string
	seen := map[string]bool{}
	d.mu.Lock()
	for _, u := range urls {
		if seen[u] || !(strings.HasPrefix(u, "/") || strings.HasPrefix(u, "http")) {
			continue
		}
		seen[u] = true
		if c, ok := d.variants[u]; ok && time.Now().Before(c.expires) {
			continue
		}
		if len(want) == maxPrefetchVariants { // the rest are looked up one by one
			break
		}
		want = append(want, u)
		patterns = append(patterns, likeEscaper.Replace(reverseString(u))+"%")
	}
	d.mu.Unlock()
	if len(want) == 0 {
		return
	}
	ctx, cancel := d.ctx()
	defer cancel()
	conn, err := d.s.db.GetConn(ctx)
	if err != nil {
		return
	}
	defer conn.Release()
	query, args := variantsQuery(patterns)
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	type mediaRow struct {
		url      string
		variants []byte
		width    *int
		height   *int
	}
	var found []mediaRow
	for rows.Next() {
		var m mediaRow
		if rows.Scan(&m.url, &m.variants, &m.width, &m.height) != nil {
			return
		}
		found = append(found, m)
	}
	if rows.Err() != nil {
		return
	}
	expires := time.Now().Add(variantsCacheTTL)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.variants == nil || len(d.variants)+len(want) > variantsCacheSize {
		d.variants = map[string]cachedVariants{}
	}
	for _, u := range want {
		// The exact URL wins, else the newest row whose URL ends with u.
		best := -1
		for i, m := range found {
			if m.url == u {
				best = i
				break
			}
			if best < 0 && strings.HasSuffix(m.url, u) {
				best = i
			}
		}
		c := cachedVariants{expires: expires}
		if best >= 0 {
			m := found[best]
			c.list = imageVariants(m.variants, u, m.width)
			c.w, c.h = dims(m.width, m.height)
		}
		d.variants[u] = c
	}
}

var likeEscaper = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")

func reverseString(s string) string {
	r := []rune(s)
	slices.Reverse(r)
	return string(r)
}

// lookupVariants finds the media row of u. ok is false when the lookup itself
// failed (as opposed to u simply not being stored media).
func (d *renderData) lookupVariants(u string) (c cachedVariants, ok bool) {
	ctx, cancel := d.ctx()
	defer cancel()
	conn, err := d.s.db.GetConn(ctx)
	if err != nil {
		d.fail()
		return cachedVariants{}, false
	}
	defer conn.Release()
	var variants []byte
	var width, height *int
	// The stored URL equals u or ends with it (a root-relative u is stored with
	// the CDN/base origin in front). The suffix test is a prefix range scan on
	// the reverse(public_url) index; the exact match wins, newest otherwise.
	err = conn.QueryRow(ctx, `
		SELECT variants, width, height FROM hatamoto_media
		WHERE reverse(public_url) LIKE $2
		ORDER BY (public_url = $1) DESC, created_at DESC
		LIMIT 1`, u, likeEscaper.Replace(reverseString(u))+"%").Scan(&variants, &width, &height)
	if errors.Is(err, pgx.ErrNoRows) {
		return cachedVariants{}, true
	}
	if err != nil {
		d.fail()
		return cachedVariants{}, false
	}
	c.list = imageVariants(variants, u, width)
	c.w, c.h = dims(width, height)
	return c, true
}

// dims is the stored size when both sides are known and positive, else 0, 0.
func dims(w, h *int) (int, int) {
	if w == nil || h == nil || *w <= 0 || *h <= 0 {
		return 0, 0
	}
	return *w, *h
}

// imageVariants turns stored variants (JSON) into srcset candidates: the
// resized copies except the square thumbnail, plus the original when its width
// is known, narrowest first.
func imageVariants(raw []byte, original string, width *int) []render.ImageVariant {
	var m media.Media
	if m.UnmarshalVariants(raw) != nil {
		return nil
	}
	var out []render.ImageVariant
	for _, v := range m.Variants {
		if v.Size != "thumbnail" && v.Width > 0 && v.URL != "" {
			out = append(out, render.ImageVariant{URL: v.URL, Width: v.Width})
		}
	}
	if len(out) == 0 {
		return nil
	}
	if width != nil && *width > 0 {
		out = append(out, render.ImageVariant{URL: original, Width: *width})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Width < out[j].Width })
	return out
}
