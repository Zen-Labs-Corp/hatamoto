package admin

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// navCountTTL is how long a sidebar count is reused: NavItem.Badge runs on
// every render, and a count that is a few seconds old is fine.
const navCountTTL = 30 * time.Second

// navRefreshRun starts a background count refresh. Tests swap it for a
// synchronous runner so a first render already shows the count.
var navRefreshRun = func(f func()) { go f() }

// countBadge returns a NavItem.Badge showing count's value. A render never
// waits on count: it gets the cached value, possibly stale, and once the ttl
// has passed one background refresh (guarded against overlap) replaces it. The
// first render before any value exists shows no badge. Zero and errors give ""
// (no badge), and a failed count is not retried before the ttl passes, so a
// down database is not hit per render.
func countBadge(ttl time.Duration, now func() time.Time, run func(func()), count func() (int, error)) func() string {
	var (
		mu       sync.Mutex
		at       time.Time
		text     string
		inflight bool
	)
	refresh := func() {
		text2 := ""
		// The refresh runs in a bare goroutine, where a panic would end the process: log it and
		// treat it as a failed count (no badge, retried after the ttl).
		defer func() {
			if r := recover(); r != nil {
				slog.Error("admin: nav count panicked", "panic", r)
				mu.Lock()
				at, text, inflight = now(), "", false
				mu.Unlock()
			}
		}()
		if n, err := count(); err == nil && n > 0 {
			text2 = strconv.Itoa(n)
		}
		mu.Lock()
		at, text, inflight = now(), text2, false
		mu.Unlock()
	}
	return func() string {
		mu.Lock()
		start := (at.IsZero() || now().Sub(at) >= ttl) && !inflight
		if start {
			inflight = true
		}
		mu.Unlock()
		if start {
			run(refresh)
		}
		mu.Lock()
		defer mu.Unlock()
		return text
	}
}

// countLabel words a count: "1 draft to review", "3 drafts to review".
func countLabel(n, one, many string) string {
	if n == "1" {
		return "1 " + one
	}
	return n + " " + many
}

// wireNavCounts gives the sidebar's Entities and Plugins items their markers:
// the drafts waiting for review and the installed plugins. The Plugins count is the
// owner's: an editor admitted by a plugin UI does not see the installed total.
func (a *Admin) wireNavCounts() {
	entities := countBadge(navCountTTL, time.Now, navRefreshRun, a.entityDrafts)
	plugins := countBadge(navCountTTL, time.Now, navRefreshRun, a.pluginTotal)
	a.nav.update("entities", func(it *NavItem) {
		it.Badge = func(string) string { return entities() }
		it.BadgeLabel = func(_, b string) string { return countLabel(b, "draft to review", "drafts to review") }
	})
	a.nav.update(pluginsID, func(it *NavItem) {
		it.Badge = func(role string) string {
			if role != "owner" {
				return ""
			}
			return plugins()
		}
		it.BadgeLabel = func(_, b string) string { return countLabel(b, "plugin installed", "plugins installed") }
	})
}

// entityDrafts counts the drafts the Entities list shows (reuse schemas
// excluded), as the list's own Draft count does.
func (a *Admin) entityDrafts() (int, error) {
	c := a.deps.Content
	if c.Entities == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	schemas, err := c.Entities.ListSchemas(ctx)
	if err != nil {
		return 0, err
	}
	return a.entityCounts(ctx, schemas, nil).byStatus["draft"], nil
}

// pluginTotal counts the installed plugins.
func (a *Admin) pluginTotal() (int, error) {
	pa := a.deps.Growth.Plugins
	if pa == nil {
		return 0, nil
	}
	return len(pa.Plugins()), nil
}
