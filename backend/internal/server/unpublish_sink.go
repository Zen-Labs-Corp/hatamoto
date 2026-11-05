package server

import (
	"context"
	"errors"
	"log/slog"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"

	"github.com/google/uuid"
)

// auditEntityUnpublishScheduled is the audit action of a scheduled unpublish
// that the worker carried out.
const auditEntityUnpublishScheduled = "entity.unpublish_scheduled"

// schedulerActor names the worker in the audit log: a scheduled change has no
// signed-in user.
const schedulerActor = "system (scheduler)"

// unpublishSink carries out a due scheduled unpublish for the worker.
type unpublishSink struct{ s *Server }

// Unpublish moves the entity to draft through the entity service (so events,
// link sync and caches behave as for an owner's Unpublish), then leaves a 302
// from its old public path to the owner's chosen target. It skips an entity
// that is no longer published, which makes a repeated tick harmless.
func (k unpublishSink) Unpublish(ctx context.Context, id uuid.UUID) error {
	s := k.s
	e, err := s.entityRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, entity.ErrEntityNotFound) {
			return nil
		}
		return err
	}
	if e.Status != entity.StatusPublished {
		return nil
	}
	sched, err := s.entityService.UnpublishFor(ctx, id)
	if err != nil {
		return err
	}
	if sched == nil {
		return nil
	}

	// The path is read before the change: it is where visitors and search
	// engines know the page.
	var path string
	if e.Schema != nil && e.Locale == s.siteLanguage(ctx) {
		path = locateEntity(ctx, s.urlSnapshot(ctx), entity.NewPostgresPublicRepository(s.db), e).Path
	}

	if _, err := s.entityService.UpdateEntityStatus(ctx, id, nil, entity.StatusDraft); err != nil {
		return err
	}
	s.invalidateRendered(ctx)

	detail := map[string]any{"name": e.Name, "slug": e.Slug, "scheduled_for": sched.At.UTC().Format("2006-01-02T15:04Z")}
	if e.Schema != nil {
		detail["schema"] = e.Schema.Slug
	}
	if sched.RedirectTarget != "" {
		detail["redirect"] = sched.RedirectTarget
		switch {
		case s.redirectService == nil || path == "" || path == "/":
			detail["redirect_skipped"] = "no public path"
		default:
			rd, err := s.redirectService.CreateRedirect(ctx, &redirect.CreateRedirectInput{
				SourcePath: path, TargetURL: sched.RedirectTarget, StatusCode: 302,
			})
			switch {
			case err == nil:
				if err := s.entityService.UnpublishStore().SetRedirect(ctx, id, rd.ID); err != nil {
					s.logger.Warn("record unpublish redirect", slog.String("entity_id", id.String()), slog.String("error", err.Error()))
				}
			case errors.Is(err, redirect.ErrDuplicateSourcePath):
				// An owner's rule already owns this path: leave it alone.
				detail["redirect_skipped"] = "a redirect already exists for this path"
			default:
				detail["redirect_skipped"] = err.Error()
				s.logger.Warn("unpublish redirect", slog.String("entity_id", id.String()), slog.String("error", err.Error()))
			}
		}
	}
	s.adminAudit(ctx, admin.AuditEvent{Actor: schedulerActor, Action: auditEntityUnpublishScheduled, Target: id.String(), Detail: detail})
	return nil
}

// retireUnpublishRedirect switches off the redirect a scheduled unpublish left
// behind for id, now that the entity is published again. The redirect stays
// as a record (inactive), so it is neither lost nor served.
func (s *Server) retireUnpublishRedirect(ctx context.Context, id uuid.UUID) {
	st := s.entityService.UnpublishStore()
	if st == nil || s.redirectService == nil {
		return
	}
	rid, err := st.TakeRedirect(ctx, id)
	if err != nil {
		s.logger.Warn("take unpublish redirect", slog.String("entity_id", id.String()), slog.String("error", err.Error()))
		return
	}
	if rid == nil {
		return
	}
	off := false
	if _, err := s.redirectService.UpdateRedirect(ctx, *rid, &redirect.UpdateRedirectInput{IsActive: &off}); err != nil {
		s.logger.Warn("deactivate unpublish redirect", slog.String("entity_id", id.String()), slog.String("error", err.Error()))
	}
}
