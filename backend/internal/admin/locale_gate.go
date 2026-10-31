package admin

import (
	"context"

	"hatamoto.dev/core/internal/entity"
)

// multiLocale is the one gate for every locale surface that only a
// multi-locale site shows (Translations tab and drawer, bulk copies, term
// locales, Start with): it returns the locale set when the site has two or
// more locales and the group store is wired. Every failure answers false, so a
// single-locale site renders exactly as before.
func (a *Admin) multiLocale(ctx context.Context) (*entity.LocaleSet, bool) {
	if a.deps.Site == nil || a.deps.Content.Groups == nil {
		return nil, false
	}
	cfg, err := a.loadSite(ctx)
	if err != nil {
		return nil, false
	}
	set := localeSetOf(cfg)
	if !set.Multi() {
		return nil, false
	}
	return set, true
}
