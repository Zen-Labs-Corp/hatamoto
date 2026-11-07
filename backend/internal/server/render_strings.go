package server

import (
	"bytes"
	"context"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/themestrings"
)

// applyStrings fills v.StringOverrides: the owner's theme string overrides for
// the active theme, chained locale -> default locale (the renderer puts the
// theme's own defaults underneath). Call it after applyLocale, which sets
// v.Locale. It only writes the view it is given.
func (s *Server) applyStrings(ctx context.Context, v *render.PageView) {
	if s.renderEngine == nil || s.siteConfig == nil {
		return
	}
	cfg, err := s.siteConfig.Get(ctx)
	if err != nil || !bytes.Contains(cfg.ThemeExtra, []byte(themestrings.ExtraKey)) {
		return
	}
	v.StringOverrides = themeStringOverrides(cfg.ThemeExtra, s.renderEngine.ThemeName(),
		v.Locale, defaultStringsTag(s.urlSnapshot(ctx).Locales(), siteLanguage(cfg)))
}

// defaultStringsTag is the default locale tag: the locale set's default on a
// multi-locale site, else the normalised site language.
func defaultStringsTag(set *entity.LocaleSet, language string) string {
	if set.Multi() {
		return set.Default()
	}
	if tag, err := entity.NormalizeLocale(language); err == nil {
		return tag
	}
	return language
}

// themeStringOverrides chains the stored overrides of theme for a page of
// locale ("" on a one-locale site: the default tag).
func themeStringOverrides(themeExtra []byte, theme, locale, defaultTag string) map[string]string {
	return themestrings.Chain(themestrings.ParseOverrides(themeExtra), theme, locale, defaultTag, nil)
}
