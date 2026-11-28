package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/taxonomy"
)

// publicContentAPIEnabled reads the owner's switch (Settings > Integrations >
// Public content API). Any error reads as off.
func (s *Server) publicContentAPIEnabled(ctx context.Context) bool {
	if s.publicContentAPIOn != nil {
		return s.publicContentAPIOn(ctx)
	}
	if s.siteConfig == nil {
		return false
	}
	c, err := s.siteConfig.Get(ctx)
	return err == nil && c.PublicContentAPI
}

// publicContentGate answers exactly like an unknown route while the switch is
// off, so an install that never turned the API on looks like it has none.
func (s *Server) publicContentGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.publicContentAPIEnabled(r.Context()) {
			notFoundHandler(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mountPublicContentAPI mounts the read-only content API on r (one public
// mount; each mount gets its own limiter). Only GET routes: no CORS, no keys,
// no writes. Order: limiter, then the switch, then the handler.
func (s *Server) mountPublicContentAPI(r *httpx.Group) {
	lim := middleware.NewRateLimiter(10, 30).ByIPv6Prefix()
	g := r.Group("", lim.Middleware, s.publicContentGate)
	g.HandleFunc("GET /content/{schema}", s.handlePublicContentList)
	g.HandleFunc("GET /content/{schema}/{slug}", s.handlePublicContentItem)
	g.HandleFunc("GET /terms/{kind}", s.handlePublicTermsList)
}

// maxPublicPage keeps (page-1)*perPage far from overflowing the SQL offset; a
// deeper page is a 400 instead of a database error.
const maxPublicPage = 100000

// publicPaging reads page and per_page (default 20, at most 100). It answers
// 400 and returns false on a bad value.
func publicPaging(w http.ResponseWriter, r *http.Request) (page, perPage int, ok bool) {
	pp := httpx.NewPaging(r.URL.Query(), 100)
	page = pp.Page("page", maxPublicPage)
	perPage = pp.Size("per_page", "limit", 20)
	if err := pp.Err(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return 0, 0, false
	}
	return page, perPage, true
}

// publicContentSchema resolves the {schema} path value to its canonical slug
// and the slugs to list (canonical plus aliases), leaving out noindex types.
// It answers 404 and returns false for an unknown schema, a schema without
// public routing or a noindex type.
func (s *Server) publicContentSchema(w http.ResponseWriter, r *http.Request) (canonical string, slugs []string, ok bool) {
	ctx := r.Context()
	cfg, found := s.urlSnapshot(ctx).Config(r.PathValue("schema"))
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return "", nil, false
	}
	idx := s.seoSettings(ctx)
	for _, slug := range append([]string{cfg.SchemaSlug}, cfg.Aliases...) {
		if !idx.NoIndexType(slug) {
			slugs = append(slugs, slug)
		}
	}
	if len(slugs) == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return "", nil, false
	}
	return cfg.SchemaSlug, slugs, true
}

// publicContentItems builds the public view of rows: absolute URLs, bylines
// and terms, each loaded with one query for the lot.
func (s *Server) publicContentItems(r *http.Request, schema string, rows []*entity.Entity) []contentItem {
	ctx := r.Context()
	snap := s.urlSnapshot(ctx)
	repo := entity.NewPostgresPublicRepository(s.db)

	bySchema := map[string][]*entity.Entity{}
	for _, e := range rows {
		bySchema[e.Schema.Slug] = append(bySchema[e.Schema.Slug], e)
	}
	base := s.publicBaseURL(r)
	urls := make(map[uuid.UUID]string, len(rows))
	for _, group := range bySchema {
		paths, routable := publicPaths(ctx, snap, repo, group[0].Schema, group)
		if !routable {
			continue
		}
		for i, e := range group {
			if i < len(paths) {
				urls[e.ID] = base + paths[i]
			}
		}
	}

	ids := make([]uuid.UUID, len(rows))
	for i, e := range rows {
		ids[i] = e.ID
	}
	authors := s.bylinesOf(ctx, ids...)
	linkSchemas := s.authorSchemas(ctx)
	terms, err := taxonomy.NewPostgresRepository(s.db).TermsForEntities(ctx, ids)
	if err != nil {
		s.logger.Warn("public content api: terms", "error", err)
	}

	out := make([]contentItem, 0, len(rows))
	for _, e := range rows {
		a := authors.view(ctx, e.ID, attrsForView(e.Attributes), e.Schema.Slug, linkSchemas)
		out = append(out, newContentItem(e, schema, urls[e.ID], a, terms[e.ID]))
	}
	return out
}

// GET /content/{schema}: one page of the published entries of a content type.
func (s *Server) handlePublicContentList(w http.ResponseWriter, r *http.Request) {
	page, perPage, ok := publicPaging(w, r)
	if !ok {
		return
	}
	schema, slugs, ok := s.publicContentSchema(w, r)
	if !ok {
		return
	}
	rows, total, err := entity.NewPostgresPublicRepository(s.db).ListPublishedFiltered(r.Context(),
		entity.ListFilter{SchemaSlugs: slugs, ExcludeOptedOut: true}, page, perPage)
	if err != nil {
		s.logger.Error("public content api: list", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writePublicJSON(w, r, publicList[contentItem]{
		Data: s.publicContentItems(r, schema, rows),
		Meta: newPublicMeta(total, page, perPage),
	})
}

// GET /content/{schema}/{slug}: one published entry.
func (s *Server) handlePublicContentItem(w http.ResponseWriter, r *http.Request) {
	schema, slugs, ok := s.publicContentSchema(w, r)
	if !ok {
		return
	}
	ent, err := entity.NewPostgresPublicRepository(s.db).GetPublishedBySlugAndSchemas(r.Context(), r.PathValue("slug"), slugs)
	switch {
	case errors.Is(err, entity.ErrEntityNotFound):
		writeError(w, http.StatusNotFound, "not found")
		return
	case err != nil:
		s.logger.Error("public content api: item", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !entityIncludesInLLMsTxt(ent) || entityNoIndex(ent, s.seoSettings(r.Context())) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writePublicJSON(w, r, publicOne[contentItem]{Data: s.publicContentItems(r, schema, []*entity.Entity{ent})[0]})
}

// GET /terms/{kind}: one page of the terms of a kind that have published entries.
func (s *Server) handlePublicTermsList(w http.ResponseWriter, r *http.Request) {
	page, perPage, ok := publicPaging(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	kind := strings.ToLower(r.PathValue("kind"))
	repo := taxonomy.NewPostgresRepository(s.db)
	if !taxonomy.ValidKind(kind) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	exists, err := repo.KindExists(ctx, kind)
	if err != nil {
		s.logger.Error("public content api: terms kind", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	terms, total, err := repo.ListPublicTermsPage(ctx, kind, page, perPage)
	if err != nil {
		s.logger.Error("public content api: terms", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	items := make([]publicTermItem, 0, len(terms))
	for _, t := range terms {
		items = append(items, publicTermItem{Kind: t.Kind, Slug: t.Slug, Name: t.Name, Description: t.Description, Count: t.EntityCount})
	}
	writePublicJSON(w, r, publicList[publicTermItem]{Data: items, Meta: newPublicMeta(total, page, perPage)})
}
