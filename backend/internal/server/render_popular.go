package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/entity"
)

const (
	// popularKeys is how many landing paths one ranking reads from the daily
	// rollup (the TopLanding cap).
	popularKeys = 500
	// popularResolves is how many landing paths of the schema one ranking
	// resolves to an entity; it bounds the point lookups per refresh.
	popularResolves = 100
	// popularMemoSize bounds the memoised rankings (one per days and schema).
	popularMemoSize = 64
	// popularServed is how many rows of a popular listing are served (and
	// counted by its pager): the ranked head and the newest fill read 100 each.
	popularServed = 100
)

// popularDays is the window in days of a "popular_7|30|90" order.
func popularDays(order string) (int, bool) {
	switch order {
	case "popular_7":
		return 7, true
	case "popular_30":
		return 30, true
	case "popular_90":
		return 90, true
	}
	return 0, false
}

// popularRank is the memoised ranking of one schema over one window: entity
// ids, most pageviews first (ties newest first).
type popularRank struct {
	ids     []uuid.UUID
	expires time.Time
}

// topLanding reads the busiest landing paths of from..to (the rollup).
func (d *renderData) topLanding(ctx context.Context, from, to time.Time, limit int) ([]analytics.TrafficTop, error) {
	if d.landingSource != nil {
		return d.landingSource(ctx, from, to, limit)
	}
	return analytics.NewReader(d.s.db).TopLanding(ctx, from, to, limit)
}

// rankedPopular returns the ids of the published entities of schema (and its
// aliases) with the most pageviews over the last days UTC days, today
// included. Landing paths are mapped to entities with the public resolver, so
// a trailing slash, a locale prefix or a casing difference land on the same
// entity and their views add up. The result is memoised for a minute per
// (days, schema); errors are not, and neither is a ranking that skipped a path
// or ran out of time (it is served, but marks the page uncacheable).
func (d *renderData) rankedPopular(ctx context.Context, schema string, days int) ([]uuid.UUID, error) {
	key := fmt.Sprintf("%d|%s", days, schema)
	d.mu.Lock()
	if m, ok := d.popular[key]; ok && time.Now().Before(m.expires) {
		d.mu.Unlock()
		return m.ids, nil
	}
	d.mu.Unlock()

	clock := time.Now
	if d.now != nil {
		clock = d.now
	}
	today := clock().UTC().Truncate(24 * time.Hour)
	top, err := d.topLanding(ctx, today.AddDate(0, 0, -(days-1)), today, popularKeys)
	if err != nil {
		return nil, err
	}
	snap := d.s.urlSnapshot(ctx)
	slugs := schemaSlugs(snap, schema)
	repo := entity.NewPostgresPublicRepository(d.s.db)
	type scored struct {
		id    uuid.UUID
		views int64
		at    time.Time
	}
	var (
		list     []scored
		at       = map[uuid.UUID]int{}
		resolved int
		failed   bool
	)
	for _, t := range top {
		if resolved >= popularResolves {
			break
		}
		if ctx.Err() != nil { // out of time: serve what is ranked, but not as healthy
			failed = true
			break
		}
		path := strings.ToLower(t.K1)
		if len(path) > 1 {
			path = strings.TrimRight(path, "/")
		}
		if path == "" || path == "/" {
			continue
		}
		sctx, locale, rest, prefixed := localeScope(ctx, snap, path)
		if prefixed && (snap.Locales().IsHidden(locale) || rest == "/") {
			continue
		}
		if !servesSchema(snap, rest, slugs) {
			continue
		}
		resolved++
		res, err := resolveEntity(sctx, repo, snap, rest)
		if err != nil { // one bad path loses its views, not the whole ranking
			failed = true
			d.s.logger.Warn("render: popular path", "error", err, "path", rest)
			continue
		}
		if res == nil || res.Entity == nil || res.Entity.Schema == nil || !slices.Contains(slugs, res.Entity.Schema.Slug) {
			continue
		}
		e := res.Entity
		if i, ok := at[e.ID]; ok {
			list[i].views += t.Pageviews
			continue
		}
		at[e.ID] = len(list)
		s := scored{id: e.ID, views: t.Pageviews}
		if e.PublishedAt != nil {
			s.at = *e.PublishedAt
		}
		list = append(list, s)
	}
	slices.SortStableFunc(list, func(a, b scored) int {
		if a.views != b.views {
			return int(b.views - a.views)
		}
		return b.at.Compare(a.at)
	})
	ids := make([]uuid.UUID, len(list))
	for i, s := range list {
		ids[i] = s.id
	}
	if failed { // a partial ranking is served once: not memoised, page not cached
		d.fail()
		return ids, nil
	}
	d.mu.Lock()
	if d.popular == nil || len(d.popular) >= popularMemoSize {
		d.popular = map[string]popularRank{}
	}
	d.popular[key] = popularRank{ids: ids, expires: time.Now().Add(variantsCacheTTL)}
	d.mu.Unlock()
	return ids, nil
}

// servesSchema reports whether a URL pattern of one of slugs matches path, a
// cheap in-memory test that spares the lookups of paths that cannot be entities
// of the schema.
func servesSchema(snap *entity.URLSnapshot, path string, slugs []string) bool {
	for _, c := range snap.Router().Resolve(path) {
		if slices.ContainsFunc(c.SchemaSlugs, func(s string) bool { return slices.Contains(slugs, s) }) {
			return true
		}
	}
	return false
}

// popularRows is one page of a popular listing: the entities of f (the filter
// without IDs) that have views, most viewed first, then the newest ones to fill
// the page. The total is the match count of f capped at popularServed, so a pager
// never offers pages past the rows served (offset included).
func (d *renderData) popularRows(ctx context.Context, repo *entity.PostgresPublicRepository, f entity.ListFilter, schema string, days, page, limit int) ([]*entity.Entity, int, error) {
	ranked, err := d.rankedPopular(ctx, schema, days)
	if err != nil {
		return nil, 0, err
	}
	skip := (page-1)*limit + f.Offset
	want := skip + limit

	f.Offset, f.Order, f.IncludeScheduled = 0, "", false
	var head []*entity.Entity
	if len(ranked) > 0 {
		hf := f
		hf.IDs = ranked
		rows, _, err := repo.ListPublishedFiltered(ctx, hf, 1, 100)
		if err != nil {
			return nil, 0, err
		}
		rank := make(map[uuid.UUID]int, len(ranked))
		for i, id := range ranked {
			rank[id] = i
		}
		slices.SortFunc(rows, func(a, b *entity.Entity) int { return rank[a.ID] - rank[b.ID] })
		head = rows
	}

	perPage := 1 // only the count is needed when the ranked rows fill the page
	if len(head) < want {
		perPage = 100
	}
	fill, total, err := repo.ListPublishedFiltered(ctx, f, 1, perPage)
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[uuid.UUID]bool, len(head))
	for _, e := range head {
		seen[e.ID] = true
	}
	all := head
	if len(head) < want {
		for _, e := range fill {
			if !seen[e.ID] {
				all = append(all, e)
			}
		}
	}
	total = min(total, popularServed) // paging never advertises rows past the cap
	if skip >= len(all) {
		return nil, total, nil
	}
	return all[skip:min(want, len(all))], total, nil
}
