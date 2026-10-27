package server

import (
	"context"
	"strings"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

const (
	maxRelated = 12
	// relatedExcerptRunes bounds a related item's excerpt.
	relatedExcerptRunes = 300
)

// noRelated is PageView.Related for views with no public entity behind them
// (attributes-only preview), so `call .Related` is always safe in a layout.
func noRelated(int) []render.RelatedItem { return nil }

// relatedFunc returns the lazy PageView.Related closure. It runs the same
// published-only, base-rows-only, tenant-scoped listing as the public archive
// (newest first, no COUNT query) and only when a layout calls it, once per
// render however often or with whatever limit it is called. A failed query
// sets *uncacheable so the page is served but not cached. A page with no
// entity or schema gets the no-op.
func (s *Server) relatedFunc(ctx context.Context, selfID, schema string, uncacheable *bool) func(int) []render.RelatedItem {
	if selfID == "" || schema == "" {
		return noRelated
	}
	snap := s.urlSnapshot(ctx)
	fetch := func() ([]*entity.Entity, error) {
		slugs := []string{schema}
		if sl := snap.Router().SchemaSlugs(schema); len(sl) > 0 {
			slugs = sl
		}
		// One spare row so excluding the page itself still fills maxRelated.
		return entity.NewPostgresPublicRepository(s.db).ListPublishedBySchemasNoCount(ctx, slugs, maxRelated+1)
	}
	return memoRelated(selfID, fetch,
		relatedLocator(ctx, snap, entity.NewPostgresPublicRepository(s.db), schema),
		func(err error) {
			s.logger.Error("related entities query failed", "error", err, "schema", schema)
			if uncacheable != nil {
				*uncacheable = true
			}
		})
}

// memoRelated fetches at most once (on the first call, for the largest
// possible limit) and serves every call from that result. A fetch error is
// reported once to onErr and yields no items for the rest of the render.
func memoRelated(selfID string, fetch func() ([]*entity.Entity, error), locate func(*entity.Entity) string, onErr func(error)) func(int) []render.RelatedItem {
	var (
		fetched bool
		rows    []*entity.Entity
	)
	return func(limit int) []render.RelatedItem {
		if !fetched {
			fetched = true
			var err error
			if rows, err = fetch(); err != nil {
				onErr(err)
				rows = nil
			}
		}
		return relatedItems(rows, selfID, clampRelated(limit), locate)
	}
}

// relatedLocator builds each item's URL through the URL registry.
func relatedLocator(ctx context.Context, snap *entity.URLSnapshot, repo resolveRepo, schema string) func(*entity.Entity) string {
	return func(e *entity.Entity) string {
		loc, found, err := snap.Locate(schema, entity.FlattenAttributes(e.Attributes), snap.VarsFor(e, ancestorSlugs(ctx, snap, repo, e, schema)))
		if err != nil || !found {
			return ""
		}
		return loc.Path
	}
}

func clampRelated(n int) int {
	return min(max(n, 1), maxRelated)
}

// relatedItems maps listed entities to the whitelisted view: self excluded,
// non-published skipped (defence in depth), items with no URL dropped, at most limit returned.
func relatedItems(items []*entity.Entity, selfID string, limit int, locate func(*entity.Entity) string) []render.RelatedItem {
	var out []render.RelatedItem
	for _, e := range items {
		if len(out) >= limit {
			break
		}
		if e == nil || e.ID.String() == selfID || e.Status != entity.StatusPublished {
			continue
		}
		url := locate(e)
		if url == "" {
			continue
		}
		attrs := attrsForView(e.Attributes)
		it := render.RelatedItem{
			Title:    firstNonEmpty(strAttr(attrs, "title"), e.Name),
			URL:      url,
			Excerpt:  truncateRunes(strAttr(attrs, "excerpt"), relatedExcerptRunes),
			Image:    strAttr(attrs, "featured_image"),
			Category: strAttr(attrs, "category"),
		}
		if e.PublishedAt != nil {
			it.PublishedAt = *e.PublishedAt
		}
		fillItemExtras(&it, e, attrs)
		out = append(out, it)
	}
	return out
}

// fillItemExtras sets the entity name, word count, reading time and the
// attributes copy of a card. attrs is the item's own decoded copy (see
// attrsForView); the content body is counted, then dropped from it.
func fillItemExtras(it *render.RelatedItem, e *entity.Entity, attrs map[string]any) {
	it.Name = e.Name
	if content, ok := attrs["content"].(string); ok {
		it.WordCount = render.WordCount(content)
		it.ReadingTime = render.ReadingMinutes(it.WordCount)
	}
	delete(attrs, "content")
	it.Attrs = attrs
}

func strAttr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
