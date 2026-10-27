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
// one of Redirect, NotFound, Page, Archive or Listing describes the answer.
type routeResult struct {
	Redirect *redirectHit
	NotFound bool
	Page     *PublicPageResponse
	Archive  *resolvedArchive
	// Listing is a built-in taxonomy, author or search listing (render only).
	Listing *listingRoute
	// Kind is resolveKindEntity, resolveKindHome or resolveKindArchive.
	Kind         string
	CanonicalURL string
}

// routeDeps are the collaborators resolveRouteWith needs, as funcs so the flow is
// testable without a database.
type routeDeps struct {
	checkRedirect func(ctx context.Context, path string) *redirect.Redirect
	snapshot      func(ctx context.Context) (*entity.URLSnapshot, error)
	repo          resolveRepo
	log404        func(path, referer, userAgent string)
	loadPage      func(r *http.Request, slug string) (*PublicPageResponse, pageOutcome, error)
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
	res, err := resolveEntity(ctx, deps.repo, snap, path)
	if err != nil {
		s.logger.Error("resolve: lookup", "error", err, "path", path)
		return fail()
	}
	if res == nil {
		if deps.log404 != nil && r != nil {
			deps.log404(path, r.Referer(), r.UserAgent())
		}
		return &routeResult{NotFound: true}, pageOutcome{Status: http.StatusNotFound, NotFound: true}, nil
	}

	out := &routeResult{Kind: res.Kind, CanonicalURL: res.URL}
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
		return &routeResult{NotFound: true}, outcome, nil
	}
	return out, outcome, nil
}
