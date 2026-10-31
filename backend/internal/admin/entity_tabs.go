package admin

import (
	"context"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
)

// entityTabsFor is entityTabs plus the Translations tab, between Details and
// Versions. The tab shows only on a multi-locale site with the group store
// wired; a single-locale site keeps the two-tab row. The label carries how
// many of the configured locales have a copy ("Translations 2/3").
func (a *Admin) entityTabsFor(ctx context.Context, e *entity.Entity, active string) []ui.Tab {
	tabs := a.entityTabs(e.ID.String(), active)
	g := a.deps.Content.Groups
	if g == nil || a.deps.Site == nil {
		return tabs
	}
	cfg, err := a.loadSite(ctx)
	if err != nil {
		return tabs
	}
	set := localeSetOf(cfg)
	if !set.Multi() {
		return tabs
	}
	label := "Translations"
	if e.Locale != entity.LocaleShared {
		// A failed lookup never fails the page: the tab just loses its count.
		if ms, err := g.Members(ctx, e.ID); err == nil {
			have := max(len(ms), 1) // an ungrouped entry is its own copy
			label += " " + strconv.Itoa(have) + "/" + strconv.Itoa(len(set.Tags()))
		}
	}
	tab := ui.Tab{Label: label, Href: a.adminPath + "/entities/" + e.ID.String() + "/translations", Active: active == "translations"}
	return []ui.Tab{tabs[0], tab, tabs[1]}
}
