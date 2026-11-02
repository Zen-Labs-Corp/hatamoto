package admin

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// navCountTTL is how long a sidebar count is reused: NavItem.Badge runs on
// every render, and a count that is a few seconds old is fine.
const navCountTTL = 30 * time.Second

// countBadge returns a NavItem.Badge showing count's value, computed at most
// once per ttl. Zero and errors give "" (no badge), and a failed count is
// not retried before the ttl passes, so a down database is not hit per render.
func countBadge(ttl time.Duration, now func() time.Time, count func() (int, error)) func() string {
	var (
		mu   sync.Mutex
		at   time.Time
		text string
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && now().Sub(at) < ttl {
			return text
		}
		at, text = now(), ""
		if n, err := count(); err == nil && n > 0 {
			text = strconv.Itoa(n)
		}
		return text
	}
}

// wireNavCounts gives the sidebar's Entities and Plugins items their counts
// (the preview's "Entities 158", "Plugins 3").
func (a *Admin) wireNavCounts() {
	a.nav.update("entities", func(it *NavItem) { it.Badge = countBadge(navCountTTL, time.Now, a.entityTotal) })
	a.nav.update(pluginsID, func(it *NavItem) { it.Badge = countBadge(navCountTTL, time.Now, a.pluginTotal) })
}

// entityTotal counts the entities the Entities list shows (reuse schemas
// excluded), as the list's own counts do.
func (a *Admin) entityTotal() (int, error) {
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
	return a.entityCounts(ctx, schemas, nil).all, nil
}

// pluginTotal counts the installed plugins.
func (a *Admin) pluginTotal() (int, error) {
	pa := a.deps.Growth.Plugins
	if pa == nil {
		return 0, nil
	}
	return len(pa.Plugins()), nil
}
