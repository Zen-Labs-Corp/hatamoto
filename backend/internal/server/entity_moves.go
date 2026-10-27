package server

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	linkingv2 "hatamoto.dev/core/internal/linking/v2"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
)

// onEntityMoved runs after an update changed an entity's slug or parent. For
// the entity (when it is published) and every published row below it whose URL
// changed, it makes the old path answer 301 to the new one and rewrites the
// stored internal links (<a data-entity-id>) to the new URL. A draft parent
// gives its children their URL (a draft region page), so its children are
// walked whatever its own status. Failures are logged: the save itself already
// succeeded.
func (s *Server) onEntityMoved(ctx context.Context, before, after *entity.Entity, actor *uuid.UUID) {
	if after.Schema == nil {
		return
	}
	lang := s.siteLanguage(ctx)
	snap := s.urlSnapshot(ctx)
	oldAnc, newAnc, err := s.ancestorChains(ctx, before, after)
	if err != nil {
		s.logger.Warn("entity move: ancestors", slog.String("entity_id", after.ID.String()), slog.String("error", err.Error()))
		return
	}

	type moved struct {
		id               uuid.UUID
		oldPath, newPath string
	}
	var all []moved
	pub := entity.NewPostgresPublicRepository(s.db)
	add := func(ent *entity.Entity, schema string, oldChain, newChain []string, oldSlug string) {
		// Only the site language is public, and its paths are the same as its
		// twins' paths: another locale must not redirect, or repoint, a live URL.
		if ent.Locale != lang {
			return
		}
		attrs := entity.FlattenAttributes(ent.Attributes)
		was := *ent
		was.Slug = oldSlug
		o, ok1, e1 := snap.Locate(schema, attrs, snap.VarsFor(&was, oldChain))
		n, ok2, e2 := snap.Locate(schema, attrs, snap.VarsFor(ent, newChain))
		if e1 != nil || e2 != nil || !ok1 || !ok2 || o.Path == n.Path {
			return
		}
		// The old path may now belong to another live entity (the site-language
		// twin's page or child): redirecting it would shadow that page.
		if cur, err := resolveEntity(ctx, pub, snap, o.Path); err != nil {
			s.logger.Warn("entity move: resolve old path", slog.String("path", o.Path), slog.String("error", err.Error()))
			return
		} else if cur != nil && cur.Entity != nil && cur.Entity.ID != ent.ID {
			return
		}
		all = append(all, moved{ent.ID, o.Path, n.Path})
	}
	// Only patterns that use the slug or the chain move with it; a page whose
	// pattern ignores both keeps its path and produces no move.
	// A path that was never public has nothing to redirect from.
	// The old path was this entity's public URL only if it already had the site
	// language before the update (a locale change into it must not redirect the
	// live twin's path).
	// The root keeps G1 (site language before and after). Descendants are
	// decided one by one in add: they are public through any parent chain, so
	// each moves when it is in the site language and its old path is not owned
	// by another live entity.
	inSiteLang := before.Locale == lang && after.Locale == lang
	if inSiteLang && before.Status == entity.StatusPublished && after.Status == entity.StatusPublished {
		add(after, after.Schema.Slug, oldAnc, newAnc, before.Slug)
	}

	if hs, ok := s.entityRepo.(entity.HierarchyStore); ok {
		desc, err := hs.Descendants(ctx, after.ID)
		if err != nil {
			s.logger.Warn("entity move: descendants", slog.String("entity_id", after.ID.String()), slog.String("error", err.Error()))
		}
		for _, d := range desc {
			oc := append(append(append([]string(nil), oldAnc...), before.Slug), d.Between...)
			nc := append(append(append([]string(nil), newAnc...), after.Slug), d.Between...)
			add(d.Entity, d.Entity.Schema.Slug, oc, nc, d.Entity.Slug)
		}
	}
	if len(all) == 0 {
		return
	}

	moves := make([]redirect.PathMove, 0, len(all))
	for _, m := range all {
		moves = append(moves, redirect.PathMove{From: m.oldPath, To: m.newPath})
	}
	if s.redirectService != nil {
		if err := s.redirectService.MoveEntityPaths(ctx, moves); err != nil {
			s.logger.Warn("entity move: redirects", slog.String("entity_id", after.ID.String()), slog.String("error", err.Error()))
		}
	}
	for _, m := range all {
		if err := linkingv2.RewriteForEntity(database.WithSuperadmin(ctx), s.db, m.id, m.newPath, "path_change", actor); err != nil {
			s.logger.Warn("entity move: link rewrite", slog.String("entity_id", m.id.String()), slog.String("error", err.Error()))
		}
	}
	// The rewrites change other entities' content after the entity.updated
	// purge may already have run: purge again so linking pages show the new URL.
	s.invalidateRendered(ctx)
}

// siteLanguage reads the language the public site serves, from the same
// function the public filter uses. On error it returns "" so nothing counts as
// public (no redirect is written).
func (s *Server) siteLanguage(ctx context.Context) string {
	var lang string
	if err := s.db.Pool().QueryRow(ctx, `SELECT public.hatamoto_site_language()`).Scan(&lang); err != nil {
		s.logger.Warn("site language", slog.String("error", err.Error()))
		return ""
	}
	return lang
}

// ancestorChains returns the parent chains (slugs, root first) the entity had
// before an update and has after it.
func (s *Server) ancestorChains(ctx context.Context, before, after *entity.Entity) (oldChain, newChain []string, err error) {
	hs, ok := s.entityRepo.(entity.HierarchyStore)
	if !ok {
		return nil, nil, nil
	}
	chain := func(id *uuid.UUID) ([]string, error) {
		if id == nil {
			return nil, nil
		}
		anc, err := hs.Ancestors(ctx, *id)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(anc)+1)
		for _, a := range anc {
			out = append(out, a.Slug)
		}
		// The parent itself is not part of its own ancestors.
		p, err := s.entityRepo.GetByID(ctx, *id)
		if err != nil {
			return nil, err
		}
		return append(out, p.Slug), nil
	}
	if oldChain, err = chain(before.ParentID); err != nil {
		return nil, nil, err
	}
	if newChain, err = chain(after.ParentID); err != nil {
		return nil, nil, err
	}
	return oldChain, newChain, nil
}

// onTermsMoved makes the old archive paths of renamed or re-parented terms
// answer 301 to the new ones, like entity moves do.
func (s *Server) onTermsMoved(ctx context.Context, moves []taxonomy.PathMove) {
	if s.redirectService == nil {
		return
	}
	pm := make([]redirect.PathMove, 0, len(moves))
	for _, m := range moves {
		pm = append(pm, redirect.PathMove{From: m.From, To: m.To})
	}
	if err := s.redirectService.MoveEntityPaths(ctx, pm); err != nil {
		s.logger.Warn("term move: redirects", slog.String("error", err.Error()))
	}
}
