package server

import (
	"context"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

// maxEntityLinks bounds how many distinct internal link targets one page
// resolves.
const maxEntityLinks = 200

// linkIDs collects the distinct entity ids of the <a data-entity-id> links in
// every string found inside values (strings, maps and slices, nested).
func linkIDs(values ...any) []uuid.UUID {
	var ids []uuid.UUID
	seen := map[string]bool{}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			for _, id := range render.EntityLinkIDs(t) {
				if u, err := uuid.Parse(id); err == nil && !seen[id] && len(ids) < maxEntityLinks {
					seen[id] = true
					ids = append(ids, u)
				}
			}
		case map[string]any:
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	for _, v := range values {
		walk(v)
	}
	return ids
}

// entityLinkTargets resolves the links in a view's section content and body.
func (s *Server) entityLinkTargets(ctx context.Context, v *render.PageView) map[string]string {
	var contents []any
	for _, sec := range viewSections(v.Sections, v.Doc) {
		contents = append(contents, sec.Content)
	}
	contents = append(contents, v.Attrs["content"])
	return s.resolveEntityLinks(ctx, linkIDs(contents...))
}

// resolveEntityLinks maps entity ids to their current public paths in one
// query. Only published targets with a public URL are returned; the renderer
// leaves other links alone. Errors degrade to "no rewriting": the stored hrefs
// are still valid links.
func (s *Server) resolveEntityLinks(ctx context.Context, ids []uuid.UUID) map[string]string {
	if len(ids) == 0 {
		return nil
	}
	targets, err := entity.NewPostgresPublicRepository(s.db).GetPublishedByIDs(ctx, ids)
	if err != nil {
		s.logger.Warn("internal link targets query failed", "error", err)
		return nil
	}
	snap := s.urlSnapshot(ctx)
	repo := entity.NewPostgresPublicRepository(s.db)
	out := make(map[string]string, len(targets))
	for _, e := range targets {
		if e == nil || e.Schema == nil {
			continue
		}
		if path := relatedLocator(ctx, snap, repo, e.Schema.Slug)(e); path != "" {
			out[e.ID.String()] = path
		}
	}
	return out
}
