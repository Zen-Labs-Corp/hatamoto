package server

import (
	"context"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

// buildRedirectChain wires every provider with its real lookup.
//
// Provider order is fixed by the priority constants, not by this call.
func (s *Server) buildRedirectChain() *redirect.Chain {
	return redirect.NewChain(
		redirect.NewDBRulesProvider(s.lookupDBRule).WithPatterns(s.lookupDBPattern),
		s.wpProvider(),
		redirect.NewBareSlugToBlogProvider(s.lookupBlogSlug),
	)
}

// wpProvider is the legacy WordPress redirect provider, targeting the site's
// own post archive and post URLs.
func (s *Server) wpProvider() *redirect.WPPatternsProvider {
	return redirect.NewWPPatternsProvider(s.legacyWPRedirects).WithTargets(func(ctx context.Context) redirect.PostTargets {
		snap := s.urlSnapshot(ctx)
		t := redirect.PostTargets{Archive: snap.PostArchivePath()}
		t.Post = func(slug string) string { return snap.PostPath(slug) }
		return t
	})
}

// lookupDBRule reads the admin-managed rules through the existing cache.
func (s *Server) lookupDBRule(ctx context.Context, path string) *redirect.Redirect {
	if s.redirectService == nil {
		return nil
	}
	return s.redirectService.CheckRedirect(ctx, path)
}

// lookupDBPattern matches the wildcard rules in hatamoto_redirects.
func (s *Server) lookupDBPattern(ctx context.Context, path string) *redirect.Redirect {
	if s.redirectService == nil {
		return nil
	}
	return s.redirectService.CheckRedirectPattern(ctx, path)
}

// lookupBlogSlug ports handlePublicSlugResolve's schema test.
func (s *Server) lookupBlogSlug(ctx context.Context, slug string) string {
	ent, err := entity.NewPostgresPublicRepository(s.db).GetPublishedBySlug(ctx, slug)
	if err != nil || ent == nil || ent.Schema == nil {
		return ""
	}
	path, _ := bareSlugCanonical(ent, s.urlSnapshot(ctx))
	return path
}

// bareSlugCanonical returns the canonical path of an entity reached by its
// bare slug. It is false when that path is the bare slug itself (pages and
// section landing pages already live there) or the schema is unknown.
func bareSlugCanonical(ent *entity.Entity, snap *entity.URLSnapshot) (string, bool) {
	loc, found, err := snap.Locate(ent.Schema.Slug, entity.FlattenAttributes(ent.Attributes), snap.VarsFor(ent, nil))
	if err != nil || !found || loc.Section || loc.Path == "/"+ent.Slug {
		return "", false
	}
	return loc.Path, true
}

// legacyWPRedirects reports the legacy_wp_redirects site setting.
func (s *Server) legacyWPRedirects(ctx context.Context) bool {
	if s.siteConfig == nil {
		return false
	}
	cfg, err := s.siteConfig.Get(ctx)
	return err == nil && cfg.LegacyWPRedirects
}

// matchRedirect is the redirect lookup of the public site: an exact rule, then
// the wildcard rules, then (when legacy_wp_redirects is on) the built-in
// WordPress URL families. The first hit wins.
func (s *Server) matchRedirect(ctx context.Context, path string) *redirect.Redirect {
	if s.redirectService == nil {
		return nil
	}
	// Whatever matches, the Location is checked once more here: an owner rule
	// may carry an absolute URL, nothing else may leave the site.
	if rd := s.redirectService.CheckRedirect(ctx, path); rd != nil {
		return safeRedirect(rd)
	}
	if rd := s.redirectService.CheckRedirectPattern(ctx, path); rd != nil {
		return safeRedirect(rd)
	}
	if m, _ := s.wpProvider().Resolve(ctx, path); m != nil {
		return safeRedirect(&redirect.Redirect{SourcePath: path, TargetURL: m.Target, StatusCode: m.Status})
	}
	return nil
}

// safeRedirect returns rd unless its target is not a safe Location, in which
// case the path is not redirected at all.
func safeRedirect(rd *redirect.Redirect) *redirect.Redirect {
	if !redirect.SafeLocation(rd.TargetURL, true) {
		return nil
	}
	return rd
}

// handleRedirectExplain answers "what happens to URL X, and why": every
// redirect provider in order, with the ones that declined recorded too.
//
// Query params:
//
//	path    required, the URL path to resolve
func (s *Server) handleRedirectExplain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	writeJSON(w, http.StatusOK, s.buildRedirectChain().Resolve(r.Context(), path))
}
