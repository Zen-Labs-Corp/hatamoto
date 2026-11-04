package server

import (
	"context"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/plugrun"
)

// changesetSweepJob is the WORKER_DISABLE_JOBS name of the hourly changeset
// housekeeping (expiry, finished runs, orphan staged files).
const changesetSweepJob = "changeset_sweep"

// setupChangesets builds the changeset engine on the content services and
// puts the sweep on the worker scheduler. It runs after the taxonomy service
// exists, the last of the content services to be built.
func (s *Server) setupChangesets() {
	s.changesets = &changeset.Engine{
		DB:     s.db,
		Logger: s.logger,
		Blobs:  s.mediaService,
		Audit:  s.changesetAudit,
		Writers: map[changeset.Kind]changeset.KindWriter{
			changeset.KindEntity:   changeset.EntityWriter{Svc: s.entityService},
			changeset.KindTerm:     changeset.TermWriter{Svc: s.taxonomyService},
			changeset.KindMenu:     changeset.MenuWriter{Svc: s.menuService},
			changeset.KindRedirect: changeset.RedirectWriter{Svc: s.redirectService, SameHostOnly: s.demoOn(), SourceLive: s.pathIsLivePage},
			changeset.KindMedia:    changeset.MediaWriter{Svc: s.mediaService},
		},
	}
	s.scheduleChangesetSweep()
}

// changesetReviewer builds the admin's review service on the engine; nil until
// the engine, the run store's database and the site settings exist.
func (s *Server) changesetReviewer() *changesetReviewer {
	if s.changesets == nil || s.db == nil || s.siteConfig == nil {
		return nil
	}
	return &changesetReviewer{
		eng: s.changesets, runs: &plugrun.Store{DB: s.db}, settings: s.siteConfig,
		invalidate: s.invalidateSiteSettings, audit: s.changesetAudit,
	}
}

// scheduleChangesetSweep schedules the sweep unless there is no scheduler
// (HATAMOTO_WORKER=off) or WORKER_DISABLE_JOBS names it. Publish checks expiry
// itself, so a process without the sweep never publishes an expired changeset.
// It reports whether the job was scheduled.
func (s *Server) scheduleChangesetSweep() bool {
	if s.changesets == nil || s.scheduler == nil || !workerJobEnabled(changesetSweepJob) {
		return false
	}
	err := s.scheduler.ScheduleJitter(changesetSweepJob, time.Hour, 5*time.Minute, 3*time.Minute, s.changesets.Sweep)
	if err != nil {
		s.logger.Error("changesets: sweep job not scheduled", "error", err)
	}
	return err == nil
}

// changesetAudit writes a changeset action to the admin audit log. The actor
// is the user in ctx, or "system" for the sweeper.
func (s *Server) changesetAudit(ctx context.Context, action, target string, detail map[string]any) {
	e := admin.AuditEvent{Actor: "system", Action: action, Target: target, Detail: detail}
	if id, ok := database.GetUserID(ctx); ok {
		e.ActorID, e.Actor = id.String(), id.String()
	}
	s.adminAudit(ctx, e)
}

// pathIsLivePage reports whether the site serves a page at path: a published
// entity, a schema archive, or a home page (the site's or a locale's). The
// changeset redirect writer refuses a plugin redirect there: redirects are
// matched before pages, so it would shadow the owner's page. Like the public
// resolver it strips a locale prefix and scopes the lookup to that locale.
func (s *Server) pathIsLivePage(ctx context.Context, path string) (bool, error) {
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	snap := s.urlSnapshot(ctx)
	ctx, _, rest, _ := localeScope(ctx, snap, path)
	if rest == "/" {
		return true, nil
	}
	page, err := resolveEntity(ctx, entity.NewPostgresPublicRepository(s.db), snap, rest)
	return page != nil, err
}
