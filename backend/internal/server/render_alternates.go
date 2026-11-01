package server

import (
	"context"
	"strings"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// applyLocale fills the locale part of a page view on a multi-locale site:
// PageView.Locale, the per-request site language, and the alternates with the
// x-default URL. It does nothing on a one-locale site, and never changes the
// shared SiteView (v.Site is a per-request copy).
func (s *Server) applyLocale(ctx context.Context, rr *routeResult, v *render.PageView, baseURL string) {
	if rr == nil {
		return
	}
	snap := s.urlSnapshot(ctx)
	s.applyLocaleSnap(ctx, snap, rr, v, baseURL, func(set *entity.LocaleSet, loc string) (*render.BusinessView, string) {
		return s.localeBusinessView(ctx, set, loc)
	})
}

// applyLocaleSnap is applyLocale on a given URL snapshot, with the local
// business lookup injected.
func (s *Server) applyLocaleSnap(ctx context.Context, snap *entity.URLSnapshot, rr *routeResult, v *render.PageView, baseURL string,
	business func(set *entity.LocaleSet, loc string) (*render.BusinessView, string)) {
	set := snap.Locales()
	if !set.Multi() {
		return
	}
	var ent *entity.Entity
	if rr.Page != nil {
		ent = rr.Page.Entity
	}
	loc := rr.Locale
	if ent != nil && ent.Locale != entity.LocaleShared {
		loc = ent.Locale
	}
	shared := isSharedPage(rr, ent)
	if loc == entity.LocaleShared || loc == "" {
		loc = set.Default()
	}
	v.Locale = loc
	// Empty for a locale served without a prefix, even when a segment is stored.
	v.Segment = strings.TrimPrefix(set.Prefix(loc), "/")
	v.Site.Language = loc
	v.Shared = shared
	v.Locales = s.localesView(ctx, snap, set, baseURL)
	if !shared {
		v.Business, v.GeoRegion = business(set, loc)
	}

	switch {
	case ent == nil:
	case rr.Kind == resolveKindHome:
		v.Alternates, v.XDefaultHref = homeAlternates(snap, set, baseURL, loc)
	case ent.Locale != entity.LocaleShared && s.db != nil:
		v.Alternates, v.XDefaultHref = s.alternatesFor(ctx, snap, baseURL, ent)
	}
}

// isSharedPage reports whether the page belongs to no locale: an entity stored
// as shared, or a page resolved without a locale scope (an unprefixed path under
// prefix-all). Shared pages carry no local business. It reads the route and the
// entity, never the fallback locale applyLocale uses for the site language.
func isSharedPage(rr *routeResult, ent *entity.Entity) bool {
	if ent != nil {
		return ent.Locale == entity.LocaleShared
	}
	return rr.Locale == entity.LocaleShared || rr.Locale == ""
}

// homeAlternates lists every locale home in settings order.
func homeAlternates(snap *entity.URLSnapshot, set *entity.LocaleSet, baseURL, current string) ([]render.Alternate, string) {
	var alts []render.Alternate
	for _, tag := range set.PublicTags() {
		alts = append(alts, render.Alternate{
			Locale: tag, Lang: tag, Href: baseURL + snap.WithLocale(tag, "/"),
			Current: tag == current, Default: tag == set.Default(),
		})
	}
	return alts, xDefaultHref(set, alts, func(a render.Alternate) bool { return true })
}

// alternatesFor lists the copies of ent in every locale, in settings order. A
// locale without a live copy points at its home and is marked Missing. The
// x-default is the configured locale when it has a copy, else the first copy.
func (s *Server) alternatesFor(ctx context.Context, snap *entity.URLSnapshot, baseURL string, ent *entity.Entity) ([]render.Alternate, string) {
	set := snap.Locales()
	sibs, err := entity.NewGroupStore(s.db.Pool()).LiveSiblings(ctx, ent.ID, set.PublicTags())
	if err != nil {
		s.logger.Warn("render: translation siblings unavailable, no alternates", "error", err)
		return nil, ""
	}
	repo := entity.NewPostgresPublicRepository(s.db)
	hrefs := map[string]string{ent.Locale: baseURL + locateEntity(ctx, snap, repo, ent).Path}
	for _, sb := range sibs {
		if sb.ID == ent.ID || hrefs[sb.Locale] != "" {
			continue
		}
		other, err := repo.GetPublishedByID(entity.WithVisibleLocales(ctx, sb.Locale), sb.ID)
		if err != nil {
			continue
		}
		hrefs[sb.Locale] = baseURL + locateEntity(entity.WithVisibleLocales(ctx, sb.Locale), snap, repo, other).Path
	}
	var alts []render.Alternate
	for _, tag := range set.PublicTags() {
		a := render.Alternate{Locale: tag, Lang: tag, Current: tag == ent.Locale, Default: tag == set.Default()}
		if href, ok := hrefs[tag]; ok {
			a.Href = href
		} else {
			a.Href, a.Missing = baseURL+snap.WithLocale(tag, "/"), true
		}
		alts = append(alts, a)
	}
	return alts, xDefaultHref(set, alts, func(a render.Alternate) bool { return !a.Missing })
}

// xDefaultHref picks the x-default URL among the alternates that pass live.
func xDefaultHref(set *entity.LocaleSet, alts []render.Alternate, live func(render.Alternate) bool) string {
	first := ""
	for _, a := range alts {
		if !live(a) {
			continue
		}
		if a.Locale == set.XDefault() {
			return a.Href
		}
		if first == "" {
			first = a.Href
		}
	}
	if set.XDefault() == "" {
		for _, a := range alts {
			if a.Default && live(a) {
				return a.Href
			}
		}
	}
	return first
}

// localesView lists the public locales for the Organization node, each with its
// absolute home URL. Nil without site settings or with an unreadable profile
// (logged), which leaves the Organization as a one-locale site would print it.
func (s *Server) localesView(ctx context.Context, snap *entity.URLSnapshot, set *entity.LocaleSet, baseURL string) []render.LocaleSummary {
	if s.siteConfig == nil {
		return nil
	}
	cfg, err := s.siteConfig.Get(ctx)
	if err != nil {
		return nil
	}
	out, err := localeSummaries(cfg, set)
	if err != nil {
		s.logger.Warn("render: invalid business profile, no locale summary printed", "error", err)
		return nil
	}
	for i := range out {
		out[i].Home = baseURL + snap.WithLocale(out[i].Tag, "/")
	}
	return out
}
