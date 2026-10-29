package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/taxonomy"
)

// Built-in listing routes. They are fallbacks, not routes: they are tried only
// when the normal resolver (redirects, schema URL patterns, schema archives)
// found nothing for the path, so an entity or archive that lives at
// /category/x, /tag/x or /search always wins. See docs/THEMES.md.
const (
	searchPath = "/search"

	// listingPerPage is the page size of every listing.
	listingPerPage = 12
	// maxListingPage bounds ?page= so a crawler cannot ask for a huge OFFSET.
	maxListingPage = 10000
	// maxSearchQuery bounds the length of ?q= in runes.
	maxSearchQuery = 200
	// listingExcerptRunes bounds a card excerpt derived from content.
	listingExcerptRunes = 200
	// listingBioRunes bounds the bio on a person page.
	listingBioRunes = 2000
	// dataFuncTimeout bounds one DataSource call (recent, terms, menu, srcset).
	dataFuncTimeout = 3 * time.Second
)

// listingRoute is a built-in listing a path resolved to.
type listingRoute struct {
	Kind  string // render.KindTaxonomy or render.KindSearch
	Path  string
	Term  *taxonomy.Term
	Query string
	// Type limits a search to one schema (and its aliases); NoType marks a ?type= that is not a schema slug (it matches nothing).
	Type   string
	NoType bool
}

// resolveForRender is resolveRouteForRender plus the built-in listings and the
// registry detail pages: they only answer paths the resolver reported as not
// found, in that order (a CMS page wins, then a listing, then a registry page).
func (s *Server) resolveForRender(ctx context.Context, path string, r *http.Request) (*routeResult, pageOutcome, error) {
	rr, outcome, err := s.resolveRouteForRender(ctx, path, r)
	if err != nil || rr == nil || !rr.NotFound {
		return rr, outcome, err
	}
	// Listings and registry pages know the path without its locale segment;
	// the listing keeps the full path for its canonical and paging links.
	bare := path
	if rr.Rest != "" {
		bare = rr.Rest
	}
	// Under prefix-all an unprefixed path resolved to the shared scope, but the
	// listings the theme links to unprefixed (/search, term archives) belong to
	// the default locale: they list its rows and the shared ones.
	locale := rr.Locale
	if locale == entity.LocaleShared {
		if def := s.urlSnapshot(ctx).Locales().Default(); def != "" {
			locale = def
		}
	}
	lr, err := s.resolveListing(ctx, bare, r)
	if lr != nil {
		lr.Path = path
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "render: resolve listing", "error", err, "path", path)
		return nil, pageOutcome{Status: http.StatusInternalServerError},
			&pageError{http.StatusInternalServerError, "failed to resolve path"}
	}
	if lr != nil {
		return &routeResult{Listing: lr, Locale: locale, Rest: rr.Rest}, pageOutcome{Status: http.StatusOK}, nil
	}
	reg, err := s.resolveRegistry(ctx, bare, r)
	if err != nil {
		s.logger.ErrorContext(ctx, "render: resolve registry page", "error", err, "path", path)
		return nil, pageOutcome{Status: http.StatusInternalServerError},
			&pageError{http.StatusInternalServerError, "failed to resolve path"}
	}
	if reg != nil {
		return &routeResult{Registry: reg, Locale: locale, Rest: rr.Rest}, pageOutcome{Status: http.StatusOK}, nil
	}
	return rr, outcome, nil
}

// resolveListing maps /category/{slug}, /tag/{slug} and /search
// to a listing; nil when the path is none of them or names nothing that exists.
func (s *Server) resolveListing(ctx context.Context, path string, r *http.Request) (*listingRoute, error) {
	switch {
	case path == searchPath:
		typ, ok := cleanSearchType(r.URL.Query().Get("type"))
		return &listingRoute{Kind: render.KindSearch, Path: path, Query: cleanSearchQuery(r.URL.Query().Get("q")), Type: typ, NoType: !ok}, nil
	}
	// /category/..., /tag/... and /{kind}/... for a custom term kind.
	kind, rest, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !ok || rest == "" || !taxonomy.ValidKind(kind) {
		return nil, nil
	}
	return s.resolveTerm(ctx, path, kind, rest)
}

// resolveTerm serves a term archive: rest is the term's path, the slugs of its
// ancestors and its own joined by '/'. Only the canonical path resolves, so a
// nested term has one URL.
func (s *Server) resolveTerm(ctx context.Context, path, kind, rest string) (*listingRoute, error) {
	segs := strings.Split(rest, "/")
	if len(segs) > taxonomy.MaxDepth {
		return nil, nil
	}
	for _, seg := range segs {
		if !taxonomy.ValidSlug(seg) {
			return nil, nil
		}
	}
	repo := taxonomy.NewPostgresRepository(s.db)
	t, err := repo.GetTermBySlug(ctx, kind, segs[len(segs)-1])
	if errors.Is(err, taxonomy.ErrTermNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := taxonomy.FillPaths(ctx, repo, t); err != nil {
		return nil, err
	}
	if t.Path != rest {
		return nil, nil
	}
	return &listingRoute{Kind: render.KindTaxonomy, Path: path, Term: t}, nil
}

// authorSchemas are the schema slugs (own slug plus aliases) whose entities
// carry a public byline and are listed on author pages: the schemas with an
// archive (posts and the like), which excludes pages.
func (s *Server) authorSchemas(ctx context.Context) []string {
	var out []string
	for _, c := range s.urlSnapshot(ctx).Configs() {
		if c.ArchivePath != "" {
			out = append(out, c.SchemaSlug)
			out = append(out, c.Aliases...)
		}
	}
	return out
}

// cleanSearchQuery makes ?q= safe for Postgres: invalid UTF-8 is dropped,
// control characters (NUL included) become spaces, whitespace is collapsed
// and the result is cut to maxSearchQuery runes.
func cleanSearchQuery(q string) string {
	q = strings.ToValidUTF8(q, "")
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, q)
	q = strings.Join(strings.Fields(q), " ")
	if rs := []rune(q); len(rs) > maxSearchQuery {
		q = strings.TrimSpace(string(rs[:maxSearchQuery]))
	}
	return q
}

// searchTypeRe is a schema slug: what ?type= may name.
var searchTypeRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// cleanSearchType trims and lower-cases ?type=. Empty means no filter (ok);
// anything that is not a schema slug is not ok.
func cleanSearchType(t string) (slug string, ok bool) {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return "", true
	}
	if !searchTypeRe.MatchString(t) {
		return "", false
	}
	return t, true
}

// hasPositiveTerm reports whether a websearch query has a term that is not a
// negation; a query of only "-word" terms would match every row.
func hasPositiveTerm(q string) bool {
	for _, f := range strings.Fields(q) {
		if !strings.HasPrefix(f, "-") && !strings.EqualFold(f, "or") && strings.Trim(f, `"`) != "" {
			return true
		}
	}
	return false
}

// listingPage reads ?page= (1..maxListingPage).
func listingPage(r *http.Request) int {
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		return min(p, maxListingPage)
	}
	return 1
}

// fillView adds the database-backed view data to a view pageViewFromRoute
// built: the entity (terms, author) of an entity page, or the items and
// pagination of an archive, taxonomy, author or search listing.
func (s *Server) fillView(ctx context.Context, r *http.Request, rr *routeResult, v *render.PageView) error {
	if rr == nil {
		return nil
	}
	switch {
	case rr.Page != nil && rr.Page.Entity != nil:
		s.fillEntity(ctx, v, rr.Page.Entity, rr.Kind)
		if v.SchemaSlug == person.SchemaSlug {
			return s.fillPersonPage(ctx, r, rr.Page.Entity, v)
		}
	case rr.Archive != nil:
		return s.fillArchive(ctx, r, rr, v)
	case rr.Listing != nil:
		return s.fillListing(ctx, r, rr.Listing, v)
	case rr.Registry != nil:
		fillRegistry(v, rr.Registry)
	}
	return nil
}

// fillEntity sets v.Entity and the template kind of an entity page.
func (s *Server) fillEntity(ctx context.Context, v *render.PageView, e *entity.Entity, kind string) {
	v.Kind = render.KindSingle
	if kind == resolveKindHome {
		v.Kind = render.KindHome
	}
	links := s.newNavLinks(ctx)
	repo := entity.NewPostgresPublicRepository(s.db)
	ev := &render.EntityView{
		ID: e.ID.String(), Title: firstNonEmpty(strAttr(v.Attrs, "title"), e.Name), Slug: e.Slug,
		Schema: v.SchemaSlug, Attributes: v.Attrs, PublishedAt: v.PublishedAt, UpdatedAt: v.UpdatedAt,
		Excerpt: firstAttr(v.Attrs, excerptKeys...), Image: firstAttr(v.Attrs, imageKeys...),
		URL: locateEntity(ctx, links.snap, repo, e).Path,
	}
	ev.Terms = s.entityTermViews(ctx, e.ID)
	ev.Author = s.bylinesOf(ctx, e.ID).view(ctx, e.ID, v.Attrs, v.SchemaSlug, s.authorSchemas(ctx))
	v.Entity = ev
}

// fillPersonPage turns the entity page of a person into the author page: the
// profile and, below it, the person's published posts (none is fine).
func (s *Server) fillPersonPage(ctx context.Context, r *http.Request, e *entity.Entity, v *render.PageView) error {
	p, err := s.people().Get(ctx, e.ID)
	if err != nil {
		return fmt.Errorf("person %s: %w", e.ID, err)
	}
	path := s.personPath(ctx, p.Slug)
	v.Kind, v.Dynamic = render.KindAuthor, true
	v.Sections, v.Doc = nil, nil // a person has no page builder content, whatever is stored
	v.Author = &render.AuthorView{
		Name: p.Name, Slug: p.Slug, URL: path, Avatar: p.Photo, JobTitle: p.JobTitle,
		Bio:    firstNonEmpty(p.ShortBio, plainText(p.Bio, listingBioRunes)),
		SameAs: p.SameAs(),
	}
	for _, l := range p.LabelledLinks() {
		v.Author.Links = append(v.Author.Links, render.AuthorLink{Label: l.Label, URL: l.URL})
	}
	schemas := s.authorSchemas(ctx)
	if len(schemas) == 0 { // an empty filter would list every schema
		return nil
	}
	return s.fillItems(ctx, r, v, entity.ListFilter{PersonID: p.ID, SchemaSlugs: schemas}, path)
}

// entityTermViews are the terms attached to an entity, with their archive URLs.
func (s *Server) entityTermViews(ctx context.Context, id uuid.UUID) []render.TermView {
	repo := taxonomy.NewPostgresRepository(s.db)
	terms, err := repo.ListTermsForEntity(ctx, id)
	if err != nil {
		s.logger.Warn("render: entity terms", "error", err, "entity", id)
	}
	if err := taxonomy.FillPaths(ctx, repo, terms...); err != nil {
		s.logger.Warn("render: term paths", "error", err, "entity", id)
	}
	parents := termParents(ctx, s.db, terms)
	var out []render.TermView
	for _, t := range terms {
		out = append(out, termView(t, parents))
	}
	return out
}

// termView is the theme view of a term. t.Path (taxonomy.FillPaths) makes the
// URL; parents maps a term id to its parent's slug.
func termView(t *taxonomy.Term, parents map[uuid.UUID]string) render.TermView {
	path := firstNonEmpty(t.Path, t.Slug)
	v := render.TermView{Kind: t.Kind, Slug: t.Slug, Name: t.Name, URL: taxonomy.ArchivePrefix(t.Kind) + path, Description: t.Description}
	if t.ParentID != nil {
		v.Parent = parents[*t.ParentID]
	}
	return v
}

// termParents maps the parent ids of terms to their slugs (one query).
func termParents(ctx context.Context, db *database.DB, terms []*taxonomy.Term) map[uuid.UUID]string {
	var ids []uuid.UUID
	for _, t := range terms {
		if t.ParentID != nil {
			ids = append(ids, *t.ParentID)
		}
	}
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out
	}
	parents, err := taxonomy.NewPostgresRepository(db).ListTermsByIDs(ctx, ids)
	if err != nil {
		return out
	}
	for _, p := range parents {
		out[p.ID] = p.Slug
	}
	return out
}

// fillArchive lists a schema archive page by page.
func (s *Server) fillArchive(ctx context.Context, r *http.Request, rr *routeResult, v *render.PageView) error {
	v.Kind = render.KindArchive
	if cfg, ok := s.urlSnapshot(ctx).Config(rr.Archive.SchemaSlug); ok && cfg.ArchiveTitle == "" {
		// No explicit archive_title: the schema's singular name ("Post") is a
		// poor heading. Prefer what the site calls the section in its menu,
		// else the plural.
		title := firstNonEmpty(navLabelFor(v.Site.Nav, rr.Archive.Path), pluralLabel(cfg.Name))
		if title != "" {
			v.Page.Name, v.Page.Title = title, title
		}
	}
	slugs := append([]string{rr.Archive.SchemaSlug}, rr.Archive.Aliases...)
	return s.fillItems(ctx, r, v, entity.ListFilter{SchemaSlugs: slugs}, rr.Archive.Path)
}

func (s *Server) fillListing(ctx context.Context, r *http.Request, lr *listingRoute, v *render.PageView) error {
	v.Kind, v.Dynamic = lr.Kind, true
	f := entity.ListFilter{}
	query := url.Values{}
	switch lr.Kind {
	case render.KindTaxonomy:
		t := termView(lr.Term, termParents(ctx, s.db, []*taxonomy.Term{lr.Term}))
		v.Term = &t
		v.Page = render.PageMeta{Name: lr.Term.Name, Title: lr.Term.Name}
		f.TermID = lr.Term.ID
	case render.KindSearch:
		v.Query, v.SearchType = lr.Query, lr.Type
		v.Page = render.PageMeta{Name: "Search", Title: "Search", NoIndex: true}
		if lr.Query != "" && hasPositiveTerm(lr.Query) {
			f.Search = lr.Query
			query.Set("q", lr.Query)
			switch {
			case lr.NoType: // not a slug: nothing can match, never silently site-wide
				f.SchemaSlugs = []string{"-"}
			case lr.Type != "":
				f.SchemaSlugs = schemaSlugs(s.urlSnapshot(ctx), lr.Type)
				query.Set("type", lr.Type)
			}
			v.Page.Title = "Search: " + lr.Query
		}
	}
	if lr.Kind == render.KindSearch && f.Search == "" {
		return nil
	}
	return s.fillItemsQ(ctx, r, v, f, lr.Path, query)
}

func (s *Server) fillItems(ctx context.Context, r *http.Request, v *render.PageView, f entity.ListFilter, path string) error {
	return s.fillItemsQ(ctx, r, v, f, path, url.Values{})
}

// fillItemsQ loads one page of a listing into v.Items and v.Pagination, and
// sets the canonical URL (with ?page=N from page 2) the head links point at.
func (s *Server) fillItemsQ(ctx context.Context, r *http.Request, v *render.PageView, f entity.ListFilter, path string, query url.Values) error {
	page := listingPage(r)
	repo := entity.NewPostgresPublicRepository(s.db)
	rows, total, err := repo.ListPublishedFiltered(ctx, f, page, listingPerPage)
	if err != nil {
		return fmt.Errorf("list %s: %w", path, err)
	}
	pg := render.NewPagination(path, query, page, listingPerPage, total)
	if pg.Page != page { // past the last page: there is nothing to show (404), not a duplicate of the last page
		return &pageError{http.StatusNotFound, "page out of range"}
	}
	v.Pagination = pg
	v.Page.CanonicalURL = v.BaseURL + pg.URL(pg.Page)
	if v.Page.Name == "" {
		v.Page.Name = v.Page.Title
	}

	if v.Items, err = s.listingItems(ctx, repo, rows); err != nil {
		return fmt.Errorf("list %s: %w", path, err)
	}
	return nil
}

// listingItems maps listed entities to cards: their URLs through the URL
// registry (ancestors and bylines each in one query for the whole page). An
// error means the ancestors could not be loaded, so the URLs would be wrong.
// Scheduled rows (only listed on request) are cut down to scheduledItem;
// keepAttrs are the attribute keys the listing itself filters or orders by.
func (s *Server) listingItems(ctx context.Context, repo *entity.PostgresPublicRepository, rows []*entity.Entity, keepAttrs ...string) ([]render.RelatedItem, error) {
	links := s.newNavLinks(ctx)
	ancestors, err := ancestorsOf(ctx, links.snap, repo, rows)
	if err != nil {
		return nil, err
	}
	schemas := s.authorSchemas(ctx)
	ids := make([]uuid.UUID, len(rows))
	for i, e := range rows {
		ids[i] = e.ID
	}
	authors := s.bylinesOf(ctx, ids...)
	out := make([]render.RelatedItem, 0, len(rows))
	for _, e := range rows {
		if e.Status == entity.StatusScheduled {
			out = append(out, scheduledItem(e, keepAttrs))
			continue
		}
		it := listItem(e, locateWith(links.snap, e, ancestors[e.ID]).Path)
		if a := authors.view(ctx, e.ID, attrsForView(e.Attributes), e.Schema.Slug, schemas); a != nil {
			it.Author, it.AuthorURL = a.Name, a.URL
		}
		out = append(out, it)
	}
	return out, nil
}

// scheduledItem is the card of an unpublished (scheduled) entity: only what a
// "Publishing <date>" row needs. No URL, excerpt, image, byline, counts or
// category, and of the attributes only keepAttrs (the keys the query filters
// or orders by, e.g. a series part number), so embargoed content cannot leak.
func scheduledItem(e *entity.Entity, keepAttrs []string) render.RelatedItem {
	attrs := attrsForView(e.Attributes)
	it := render.RelatedItem{
		Title:  firstNonEmpty(strAttr(attrs, "title"), e.Name),
		Name:   e.Name,
		Schema: schemaSlug(e),
		Attrs:  map[string]any{},
	}
	if e.PublishedAt != nil {
		it.PublishedAt = *e.PublishedAt
	}
	for _, k := range keepAttrs {
		if v, ok := attrs[k]; ok && k != "content" && k != "page_sections" && k != "page_doc" {
			it.Attrs[k] = v
		}
	}
	return it
}

var (
	excerptKeys = []string{"excerpt", "summary", "description"}
	imageKeys   = []string{"featured_image", "hero_image", "cover", "image", "thumbnail"}
)

func firstAttr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := strAttr(m, k); s != "" {
			return s
		}
	}
	return ""
}

// listItem is the card view of a listed entity. The excerpt falls back to the
// start of attributes.content (tags stripped), so imported posts still get one.
func listItem(e *entity.Entity, path string) render.RelatedItem {
	attrs := attrsForView(e.Attributes)
	excerpt := firstAttr(attrs, excerptKeys...)
	if excerpt == "" {
		excerpt = plainText(strAttr(attrs, "content"), listingExcerptRunes)
	}
	it := render.RelatedItem{
		Title:    firstNonEmpty(strAttr(attrs, "title"), e.Name),
		URL:      path,
		Excerpt:  truncateRunes(excerpt, listingExcerptRunes),
		Image:    firstAttr(attrs, imageKeys...),
		Category: strAttr(attrs, "category"),
		Schema:   schemaSlug(e),
	}
	if e.PublishedAt != nil {
		it.PublishedAt = *e.PublishedAt
	}
	it.UpdatedAt = e.UpdatedAt
	fillItemExtras(&it, e, attrs)
	return it
}

// plainText turns stored HTML into plain text of at most n runes (see
// entity.PlainText); the result is only ever rendered escaped.
func plainText(markup string, n int) string { return entity.PlainText(markup, n) }

// navLabelFor is the label of the (first) menu item linking to path.
func navLabelFor(items []render.NavItem, path string) string {
	for _, it := range items {
		if strings.TrimRight(it.Href, "/") == strings.TrimRight(path, "/") && strings.TrimSpace(it.Label) != "" {
			return strings.TrimSpace(it.Label)
		}
		if l := navLabelFor(it.Children, path); l != "" {
			return l
		}
	}
	return ""
}

// pluralLabel pluralises a schema display name for an archive heading.
func pluralLabel(name string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return ""
	case strings.HasSuffix(name, "s"):
		return name
	case strings.HasSuffix(name, "y") && len(name) > 1 && !strings.ContainsRune("aeiouAEIOU", rune(name[len(name)-2])):
		return name[:len(name)-1] + "ies"
	}
	return name + "s"
}
