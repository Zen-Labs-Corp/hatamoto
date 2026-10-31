package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

func resolvedID(res *resolvedPage) uuid.UUID {
	if res.Entity == nil {
		return uuid.Nil
	}
	return res.Entity.ID
}

// redirectHit is a matched redirect rule.
type redirectHit struct {
	Target string
	Status int
	// KeepQuery appends the request's query string to Target (wildcard rules).
	KeepQuery bool
}

// routeResult is the typed outcome of resolving a public URL path. Exactly
// one of Redirect, NotFound, Page, Archive, Listing or Registry describes the answer.
type routeResult struct {
	Redirect *redirectHit
	NotFound bool
	Page     *PublicPageResponse
	Archive  *resolvedArchive
	// Listing is a built-in taxonomy, author or search listing (render only).
	Listing *listingRoute
	// Registry is a plugin or theme detail page of the verified registry
	// (render only).
	Registry *registryRoute
	// Kind is resolveKindEntity, resolveKindHome or resolveKindArchive.
	Kind         string
	CanonicalURL string
	// Locale is the locale scope the path resolved under: a configured tag,
	// the default locale for an unprefixed path, LocaleShared for an unprefixed
	// path under prefix-all, "" on a single-locale install. The renderer scopes
	// the page's data reads to it.
	Locale string
	// Rest is the path without its locale segment ("" on a single-locale
	// install, where it is the request path).
	Rest string
}

// routeDeps are the collaborators resolveRouteWith needs, as funcs so the flow is
// testable without a database.
type routeDeps struct {
	checkRedirect func(ctx context.Context, path string) *redirect.Redirect
	snapshot      func(ctx context.Context) (*entity.URLSnapshot, error)
	repo          resolveRepo
	log404        func(path, referer, userAgent string)
	loadPage      func(r *http.Request, slug string) (*PublicPageResponse, pageOutcome, error)
	// loadHome serves a locale's home ("/fr"); nil answers not found.
	loadHome func(r *http.Request) (*PublicPageResponse, pageOutcome, error)
}

// localeScope strips the locale segment from a public path and scopes ctx's
// reads to that locale. An unprefixed path is the default locale, or only
// shared content under prefix-all. prefixed reports an explicit locale segment.
// A single-locale site returns ctx, path unchanged and an empty locale.
func localeScope(ctx context.Context, snap *entity.URLSnapshot, path string) (scoped context.Context, locale, rest string, prefixed bool) {
	set := snap.Locales()
	if !set.Multi() {
		return ctx, "", path, false
	}
	tag, rest, prefixed := set.Resolve(path)
	switch {
	case prefixed:
		locale, path = tag, rest
	case set.Prefix(set.Default()) != "":
		locale = entity.LocaleShared
	default:
		locale = set.Default()
	}
	return entity.WithVisibleLocales(ctx, locale), locale, path, prefixed
}

// resolveRouteWith resolves a public URL path (not "/", which callers serve via
// loadHomepage) to its page without going through HTTP handlers: redirects
// first, then the URL registry, then the matching page loader scoped to the
// matched schemas. The outcome carries the cache posture of the loaded page.
func (s *Server) resolveRouteWith(ctx context.Context, deps routeDeps, path string, r *http.Request) (*routeResult, pageOutcome, error) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return nil, pageOutcome{Status: http.StatusBadRequest},
			&pageError{http.StatusBadRequest, "path must be an absolute url path"}
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	fail := func() (*routeResult, pageOutcome, error) {
		return nil, pageOutcome{Status: http.StatusInternalServerError},
			&pageError{http.StatusInternalServerError, "failed to resolve path"}
	}

	if deps.checkRedirect != nil {
		if rd := deps.checkRedirect(ctx, path); rd != nil {
			return &routeResult{Redirect: &redirectHit{Target: rd.TargetURL, Status: rd.StatusCode, KeepQuery: rd.Expanded}},
				pageOutcome{Status: http.StatusOK}, nil
		}
	}

	snap, err := deps.snapshot(ctx)
	if err != nil {
		s.logger.Error("resolve: url registry", "error", err)
		return fail()
	}
	// After the redirect check (rules may name a prefixed path): strip the
	// locale segment and scope every read to that locale. An unprefixed path is
	// the default locale, or only shared content under prefix-all.
	fullPath := path
	ctx, locale, path, prefixed := localeScope(ctx, snap, path)
	if prefixed && path == "/" {
		if deps.loadHome == nil {
			return &routeResult{NotFound: true, Locale: locale, Rest: path}, pageOutcome{Status: http.StatusNotFound, NotFound: true}, nil
		}
		rr, outcome, err := homeRoute(deps.loadHome(r.Clone(ctx)))
		if rr != nil {
			rr.Locale, rr.Rest = locale, path
		}
		return rr, outcome, err
	}
	notFound := &routeResult{NotFound: true, Locale: locale, Rest: path}
	res, err := resolveEntity(ctx, deps.repo, snap, path)
	if err != nil {
		s.logger.Error("resolve: lookup", "error", err, "path", path)
		return fail()
	}
	if res == nil {
		if deps.log404 != nil && r != nil {
			deps.log404(fullPath, r.Referer(), r.UserAgent())
		}
		return notFound, pageOutcome{Status: http.StatusNotFound, NotFound: true}, nil
	}

	canonLocale := locale
	if res.Entity != nil {
		canonLocale = res.Entity.Locale
	}
	out := &routeResult{Kind: res.Kind, CanonicalURL: snap.WithLocale(canonLocale, res.URL), Locale: locale, Rest: path}
	if res.Kind == resolveKindArchive {
		out.Archive = res.Archive
		return out, pageOutcome{Status: http.StatusOK}, nil
	}

	// Scoped delegation: the payload (JSON-LD, breadcrumbs)
	// is the page loader's.
	sub := r.Clone(withPublicScope(ctx, publicScope{SchemaSlugs: res.SchemaSlugs, SkipRedirects: true, EntityID: resolvedID(res)}))
	u := *sub.URL
	u.RawQuery = ""
	sub.URL = &u
	var outcome pageOutcome
	out.Page, outcome, err = deps.loadPage(sub, res.Slug)
	if err != nil {
		return nil, outcome, err
	}
	if outcome.NotFound {
		return notFound, outcome, nil
	}
	return out, outcome, nil
}
