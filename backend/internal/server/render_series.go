package server

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// authorPostsSchema is the archive a team member's posts are listed from.
const authorPostsSchema = "blog"

// attrInt reads a whole-number attribute stored as a JSON number or a numeric string.
func attrInt(attrs map[string]any, key string) int {
	switch v := attrs[key].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

// schemaFamily is the schema slug plus its aliases, as the URL registry knows them.
func schemaFamily(snap *entity.URLSnapshot, schema string) []string {
	if sl := snap.Router().SchemaSlugs(schema); len(sl) > 0 {
		return sl
	}
	return []string{schema}
}

// seriesFunc returns the lazy PageView.Series closure: the series named by the
// entity's attributes.series_slug, fetched once per render, only when a layout
// calls it: published and scheduled parts ordered by part number. A failed query sets *uncacheable.
func (s *Server) seriesFunc(ctx context.Context, schema string, attrs map[string]any, uncacheable *bool) func() *render.SeriesView {
	slug := strAttr(attrs, "series_slug")
	if slug == "" || schema == "" {
		return func() *render.SeriesView { return nil }
	}
	snap := s.urlSnapshot(ctx)
	locate := relatedLocator(ctx, snap, nil, schema)
	return memoSeries(slug,
		func() ([]entity.SeriesRow, error) {
			return entity.NewPostgresPublicRepository(s.db).ListSeriesRows(ctx, schemaFamily(snap, schema), slug)
		},
		func(r entity.SeriesRow) string {
			return locate(&entity.Entity{Name: r.Name, Slug: r.Slug, Attributes: r.Attributes, PublishedAt: r.PublishedAt})
		},
		func(err error) {
			s.logger.Error("series query failed", "error", err, "series_slug", slug)
			if uncacheable != nil {
				*uncacheable = true
			}
		})
}

// memoSeries fetches at most once; an error is reported once and yields nil.
func memoSeries(slug string, fetch func() ([]entity.SeriesRow, error), locate func(entity.SeriesRow) string, onErr func(error)) func() *render.SeriesView {
	var (
		done bool
		view *render.SeriesView
	)
	return func() *render.SeriesView {
		if !done {
			done = true
			rows, err := fetch()
			if err != nil {
				onErr(err)
				return nil
			}
			view = seriesView(slug, rows, locate)
		}
		return view
	}
}

// seriesView builds the view from the series rows. Only published parts get a
// URL; the index (part 0) is listed but not counted by Total, which is the
// series_total attribute. nil when there are no rows.
func seriesView(slug string, rows []entity.SeriesRow, locate func(entity.SeriesRow) string) *render.SeriesView {
	if len(rows) == 0 {
		return nil
	}
	sv := &render.SeriesView{Slug: slug, Parts: make([]render.SeriesPartView, 0, len(rows))}
	for _, r := range rows {
		var attrs map[string]any
		if json.Unmarshal(r.Attributes, &attrs) != nil {
			continue
		}
		part := attrInt(attrs, "series_part")
		if sv.Name == "" {
			sv.Name = strAttr(attrs, "series")
		}
		if sv.Total == 0 {
			sv.Total = attrInt(attrs, "series_total")
		}
		p := render.SeriesPartView{
			Title:     firstNonEmpty(strAttr(attrs, "title"), r.Name),
			Part:      part,
			Index:     part == 0,
			Published: r.Status == entity.StatusPublished,
		}
		if r.PublishedAt != nil {
			p.PublishDate = *r.PublishedAt
		}
		if p.Published {
			p.URL = locate(r)
			p.Published = p.URL != ""
			if p.Index {
				sv.IndexURL = p.URL
			}
		}
		sv.Parts = append(sv.Parts, p)
	}
	if len(sv.Parts) == 0 {
		return nil
	}
	sort.SliceStable(sv.Parts, func(i, j int) bool { return sv.Parts[i].Part < sv.Parts[j].Part })
	return sv
}

// postsByAuthorFunc returns the lazy PageView.PostsByAuthor closure for a team
// member entity (slug and name): the published posts credited to that member
// in the blog archive, once per (limit, page) per render. pageNum is the
// request's page: a call for exactly that page (>= 2) marks the render as paged
// (PagedLoop) and flags LoopOutOfRange when it is past the last page. A failed
// query sets *uncacheable. No slug gets a zero page.
func (s *Server) postsByAuthorFunc(ctx context.Context, slug, name string, pageNum int, uncacheable, pagedLoop, outOfRange *bool) func(limit, page int) render.PostsPage {
	if slug == "" {
		return func(_, page int) render.PostsPage { return emptyPostsPage(page) }
	}
	snap := s.urlSnapshot(ctx)
	repo := entity.NewPostgresPublicRepository(s.db)
	schemas := schemaFamily(snap, authorPostsSchema)
	return memoPostsByAuthor(pageNum,
		func(limit, page int) (*entity.PublicPagedResult, error) {
			return repo.ListPublishedByAuthor(ctx, schemas, slug, name, page, limit)
		},
		relatedLocator(ctx, snap, repo, authorPostsSchema),
		func(out bool) {
			if pagedLoop != nil {
				*pagedLoop = true // ?page=N changes the output: the server may cache it
			}
			if outOfRange != nil {
				*outOfRange = out
			}
		},
		func(err error) {
			s.logger.Error("posts by author query failed", "error", err, "author", slug)
			if uncacheable != nil {
				*uncacheable = true
			}
		})
}

func emptyPostsPage(page int) render.PostsPage {
	return render.PostsPage{Page: max(page, 1), Pages: 1}
}

// memoPostsByAuthor memoises per clamped (limit, page); a fetch error is
// reported once per key and yields an empty page. paged is called after a
// successful fetch of the request's own page (page == pageNum >= 2) with
// whether that page is past the last one.
func memoPostsByAuthor(pageNum int, fetch func(limit, page int) (*entity.PublicPagedResult, error), locate func(*entity.Entity) string, paged func(outOfRange bool), onErr func(error)) func(limit, page int) render.PostsPage {
	type key struct{ limit, page int }
	memo := map[key]render.PostsPage{}
	return func(limit, page int) render.PostsPage {
		k := key{min(max(limit, 1), render.MaxQueryLimit), min(max(page, 1), render.MaxQueryPage)}
		if p, ok := memo[k]; ok {
			return p
		}
		out := emptyPostsPage(k.page)
		res, err := fetch(k.limit, k.page)
		if err != nil {
			onErr(err)
		} else if res != nil {
			out.Items = relatedItems(res.Items, "", k.limit, locate)
			out.Total = res.Total
			out.Pages = max(1, int(math.Ceil(float64(res.Total)/float64(k.limit))))
			if k.page == pageNum && pageNum >= 2 {
				paged(pageNum > out.Pages)
			}
		}
		memo[k] = out
		return out
	}
}
