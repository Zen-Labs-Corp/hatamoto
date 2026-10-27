package server

import (
	"context"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	linkingv2 "hatamoto.dev/core/internal/linking/v2"
)

// urlSnapshot returns the schema URL routing in effect. A registry failure
// is logged and answered with the built-in defaults so a public page never
// fails because of its URL shape.
func (s *Server) urlSnapshot(ctx context.Context) *entity.URLSnapshot {
	snap, err := s.entityService.URLRegistry().Snapshot(ctx)
	if err != nil {
		s.logger.Error("url registry unavailable; using default url patterns", "error", err)
		return entity.DefaultURLSnapshot()
	}
	return snap
}

// locateEntity resolves where ent lives; {parent} and {path} are filled from
// the entity hierarchy when the pattern uses them, the date tokens from its
// publication date. The result falls back to "/{slug}" when the schema is
// unknown.
func locateEntity(ctx context.Context, snap *entity.URLSnapshot, repo resolveRepo, ent *entity.Entity) entity.EntityLocation {
	fallback := entity.EntityLocation{Path: "/" + ent.Slug}
	if ent.Schema == nil {
		return fallback
	}
	attrs := entity.FlattenAttributes(ent.Attributes)
	loc, found, err := snap.Locate(ent.Schema.Slug, attrs, snap.VarsFor(ent, ancestorSlugs(ctx, snap, repo, ent, ent.Schema.Slug)))
	if err != nil || !found {
		return fallback
	}
	return loc
}

// locateWith is locateEntity with the ancestor slugs already loaded.
func locateWith(snap *entity.URLSnapshot, ent *entity.Entity, ancestors []string) entity.EntityLocation {
	fallback := entity.EntityLocation{Path: "/" + ent.Slug}
	if ent.Schema == nil {
		return fallback
	}
	loc, found, err := snap.Locate(ent.Schema.Slug, entity.FlattenAttributes(ent.Attributes), snap.VarsFor(ent, ancestors))
	if err != nil || !found {
		return fallback
	}
	return loc
}

// ancestorsOf loads, in one query for all of rows, the ancestor slugs of the
// entities whose URL pattern needs them. It queries nothing for sites without
// nested URLs. An error means the URLs of rows cannot be trusted.
func ancestorsOf(ctx context.Context, snap *entity.URLSnapshot, repo *entity.PostgresPublicRepository, rows []*entity.Entity) (map[uuid.UUID][]string, error) {
	var ids []uuid.UUID
	for _, e := range rows {
		if e.ParentID != nil && e.Schema != nil && snap.NeedsAncestors(e.Schema.Slug, entity.FlattenAttributes(e.Attributes)) {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return repo.GetAncestorSlugs(ctx, ids)
}

// publicPaths resolves the public paths of rows, all of schema sc, with one
// snapshot and one ancestor query for the lot. routable is false (and paths
// nil) when the URL registry has no routing for the schema: its entities have
// no public page, so "/slug" would be a dead link. An ancestor failure
// answers nothing rather than wrong paths.
func publicPaths(ctx context.Context, snap *entity.URLSnapshot, repo *entity.PostgresPublicRepository, sc *entity.EntitySchema, rows []*entity.Entity) ([]string, bool) {
	if _, ok := snap.Config(sc.Slug); !ok {
		return nil, false
	}
	if len(rows) == 0 {
		return nil, true
	}
	ancestors, err := ancestorsOf(ctx, snap, repo, rows)
	if err != nil {
		return nil, true
	}
	out := make([]string, len(rows))
	for i, e := range rows {
		out[i] = locateWith(snap, e, ancestors[e.ID]).Path
	}
	return out, true
}

// ancestorSlugs are the slugs of ent's parents, root first. They are only
// loaded when the pattern serving ent (as a schemaSlug row) uses {parent} or
// {path}, so sites without nested URLs pay nothing.
func ancestorSlugs(ctx context.Context, snap *entity.URLSnapshot, repo resolveRepo, ent *entity.Entity, schemaSlug string) []string {
	if ent.ParentID == nil || repo == nil || !snap.NeedsAncestors(schemaSlug, entity.FlattenAttributes(ent.Attributes)) {
		return nil
	}
	path, err := repo.GetEntityPath(ctx, ent.ID)
	if err != nil || len(path) < 2 {
		return nil
	}
	out := make([]string, 0, len(path)-1)
	for _, p := range path[:len(path)-1] {
		out = append(out, p.Slug)
	}
	return out
}

// linkURLs returns the URL builder the internal linker resolves through.
func (s *Server) linkURLs(ctx context.Context) linkingv2.URLBuilder { return s.urlSnapshot(ctx) }
