package server

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// onEntitiesLive runs after entities were created, published or imported. A
// published entity answers at its path, and an automatic redirect (left by a
// rename, tag entity-move) at that same path would shadow it, because
// redirects are checked first. So it is retired: a live page always wins over
// an auto-generated redirect. Redirects an owner created are left alone.
func (s *Server) onEntitiesLive(ctx context.Context, ids []uuid.UUID) {
	if s.redirectService == nil {
		return
	}
	snap := s.urlSnapshot(ctx)
	repo := entity.NewPostgresPublicRepository(s.db)
	lang := s.siteLanguage(ctx)
	var paths []string
	for _, id := range ids {
		e, err := s.entityRepo.GetByID(ctx, id)
		if err != nil || e.Status != entity.StatusPublished || e.Schema == nil || e.Locale != lang {
			continue
		}
		if p := locateEntity(ctx, snap, repo, e).Path; p != "" && p != "/" {
			paths = append(paths, p)
		}
	}
	if err := s.redirectService.RetireEntityMoves(ctx, paths); err != nil {
		s.logger.Warn("retire entity-move redirects", slog.String("error", err.Error()))
	}
}

// scheduledPublishSink retires the redirects of an entity the publish worker
// promoted, drops the cached pages, then passes the notification on.
//
// The purge is done here, in the worker's call, and not left to the
// entity.published hook: plugin hooks run asynchronously, and with a long
// page cache TTL a page must go live (its own URL and every listing) the moment
// the worker's tick returns.
type scheduledPublishSink struct {
	s    *Server
	next interface {
		EntityPublished(ctx context.Context, id uuid.UUID, schemaSlug, slug, name string, version int)
	}
}

func (w scheduledPublishSink) EntityPublished(ctx context.Context, id uuid.UUID, schemaSlug, slug, name string, version int) {
	w.s.onEntitiesLive(ctx, []uuid.UUID{id})
	w.s.invalidateRendered(ctx)
	w.next.EntityPublished(ctx, id, schemaSlug, slug, name, version)
}
