package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auditctx"
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
		DB:      s.db,
		Logger:  s.logger,
		Blobs:   s.mediaService,
		Audit:   s.changesetAudit,
		AuditTx: s.changesetAuditTx,
		Writers: map[changeset.Kind]changeset.KindWriter{
			changeset.KindEntity:   changeset.EntityWriter{Svc: s.entityService},
			changeset.KindTerm:     changeset.TermWriter{Svc: s.taxonomyService},
			changeset.KindMenu:     changeset.MenuWriter{Svc: s.menuService},
			changeset.KindRedirect: changeset.RedirectWriter{Svc: s.redirectService, SameHostOnly: s.demoOn(), SourceLive: s.pathIsLivePage},
			changeset.KindMedia:    changeset.MediaWriter{Svc: s.mediaService},
		},
	}
	s.scheduleChangesetSweep()
	s.setupReview()
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

// changesetAuditTx writes a publish in the business transaction. Only a
// registered sink's error fails it; a failed default row is logged and the
// publish goes on. The user's email is read through tx, under a savepoint so a
// failed read cannot abort the publish transaction, and without a second pool
// connection while tx holds one.
func (s *Server) changesetAuditTx(ctx context.Context, tx pgx.Tx, action, target string, detail map[string]any) error {
	ev := audit.Event{Actor: "system", ActorKind: audit.KindSystem, Action: action, Target: target, Detail: detail}
	if id, ok := database.GetUserID(ctx); ok {
		ev.ActorID, ev.Actor, ev.ActorKind = id.String(), id.String(), audit.KindUser
		if email := userEmailInTx(ctx, tx, id); email != "" {
			ev.Actor = email
		}
	} else if a, ok := auditctx.From(ctx); ok {
		// MCP and CLI calls carry their principal here, not as a signed-in user.
		ev.ActorID, ev.Actor, ev.ActorKind, ev.KeyID = a.ID, a.Name, a.Kind, a.KeyID
	}
	err := audit.Record(ctx, tx, ev)
	if errors.Is(err, audit.ErrSink) {
		return err
	}
	if err != nil {
		s.logger.Error("admin audit write failed", "action", action, "error", err)
	}
	s.logger.Info("admin audit", "action", action, "actor", ev.Actor, "target", target)
	return nil
}

// userEmailInTx returns the user's email, or "" when it cannot be read. The
// lookup runs in a savepoint that is always rolled back.
func userEmailInTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) string {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return ""
	}
	defer func() { _ = sp.Rollback(ctx) }()
	var email string
	if err := sp.QueryRow(ctx, `SELECT email FROM hatamoto_users WHERE id = $1`, id).Scan(&email); err != nil {
		return ""
	}
	return email
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
