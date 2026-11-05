package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/worker"
)

// offsiteJob is the scheduler job (and WORKER_DISABLE_JOBS name) that takes
// the scheduled off-site backup.
const offsiteJob = "backup_offsite"

// offsiteRetry is how long a due slot waits when another backup holds the lock.
const offsiteRetry = 10 * time.Minute

// offsiteStore is the part of offsite.Store the service uses.
type offsiteStore interface {
	Get(ctx context.Context) (offsite.Settings, bool, error)
	Claim(ctx context.Context, now, next time.Time) (bool, error)
	Defer(ctx context.Context, until time.Time) error
	SaveResult(ctx context.Context, r offsite.Result) error
}

// offsiteEngine is the part of backup.Engine the service uses.
type offsiteEngine interface {
	Begin(ctx context.Context, req backup.Request) (backup.Info, func(ctx context.Context) (backup.Info, error), error)
	Update(id string, f func(*backup.Info)) error
}

// offsiteService runs the scheduled and the "back up and upload now" runs:
// a local backup, then its upload from inside the backup lock (the engine's
// OnDone hook), so an upload never overlaps another backup.
type offsiteService struct {
	store  offsiteStore
	eng    offsiteEngine
	key    []byte // seals the secret key
	base   func() context.Context
	now    func() time.Time
	audit  func(ctx context.Context, e admin.AuditEvent)
	logger *slog.Logger
	wg     *sync.WaitGroup // background runs; Shutdown waits on it
}

func (o *offsiteService) clock() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

func (o *offsiteService) lifecycle() context.Context {
	if o.base != nil {
		if c := o.base(); c != nil {
			return c
		}
	}
	return context.Background()
}

// tick is the scheduler job. It claims a due slot (one UPDATE, so one replica
// wins), takes a scheduled backup and uploads it. It returns only errors that
// stop the job from working at all; a failed run is recorded as the result.
func (o *offsiteService) tick(ctx context.Context) error {
	set, found, err := o.store.Get(ctx)
	if err != nil {
		return err
	}
	if !found || !set.Enabled || set.Schedule.Kind == offsite.ScheduleOff {
		return nil
	}
	now := o.clock().UTC()
	next := set.Schedule.Next(now)
	if set.NextDueAt.IsZero() { // first tick after the schedule was saved
		return o.store.Defer(ctx, next)
	}
	if ok, err := o.store.Claim(ctx, now, next); err != nil || !ok {
		return err
	}
	// The backup and its upload outlive the scheduler's 10 minute job cap but
	// still stop on shutdown.
	o.run(worker.JobParent(ctx), set, backup.KindScheduled, "system", true)
	return nil
}

// RunNow starts a manual backup that is uploaded when it is done and returns
// its id at once. backup.ErrBusy means another backup holds the lock. The run
// belongs to the server's lifecycle, never to the request.
func (o *offsiteService) RunNow(ctx context.Context, actor string) (string, error) {
	set, found, err := o.store.Get(ctx)
	if err != nil {
		return "", err
	}
	if !found || !set.HasSecret() {
		return "", errors.New("off-site backup is not set up")
	}
	if !set.Enabled {
		return "", errors.New("off-site backup is switched off")
	}
	parent := o.lifecycle()
	info, work, err := o.begin(parent, set, backup.KindManual, actor)
	if err != nil {
		return "", err
	}
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.finish(parent, set, info, work, actor)
	}()
	return info.ID, nil
}

// run takes one backup and uploads it, on the caller's goroutine. A busy lock
// defers the slot when scheduled (retry) and is not a failure.
func (o *offsiteService) run(ctx context.Context, set offsite.Settings, kind backup.Kind, actor string, scheduled bool) {
	info, work, err := o.begin(ctx, set, kind, actor)
	switch {
	case errors.Is(err, backup.ErrBusy):
		if scheduled {
			if derr := o.store.Defer(ctx, o.clock().UTC().Add(offsiteRetry)); derr != nil {
				o.logger.Warn("off-site backup: cannot defer a busy slot", "error", derr)
			}
		}
		return
	case err != nil:
		o.record(ctx, "", actor, offsite.Result{At: o.clock().UTC(), Error: o.scrub(set, err)})
		return
	}
	o.finish(ctx, set, info, work, actor)
}

// begin opens the target and takes the backup lock. Its OnDone uploads.
func (o *offsiteService) begin(ctx context.Context, set offsite.Settings, kind backup.Kind, actor string) (backup.Info, func(context.Context) (backup.Info, error), error) {
	target, ok := set.Target(o.key)
	if !ok {
		return backup.Info{}, nil, errors.New("the saved secret key cannot be read; enter it again")
	}
	client, err := target.Client(nil)
	if err != nil {
		return backup.Info{}, nil, err
	}
	req := backup.Request{Kind: kind, OnDone: func(ctx context.Context, in backup.Info, archive string) {
		res, uerr := offsite.Upload(ctx, client, target.Prefix, in.ID, archive, set.Keep)
		if uerr != nil {
			res.Error = o.scrubText(target.SecretKey, uerr.Error())
		}
		state := "uploaded"
		if !res.OK {
			state = "failed"
		}
		if err := o.eng.Update(in.ID, func(i *backup.Info) {
			i.Offsite, i.OffsiteError = state, res.Error
			if res.OK {
				i.SHA256 = res.SHA256
			}
		}); err != nil {
			o.logger.Warn("off-site backup: cannot update the backup record", "id", in.ID, "error", err)
		}
		o.record(ctx, in.ID, actor, res)
	}}
	return o.eng.Begin(ctx, req)
}

// finish does the work of a begun backup; a failure before the upload (the
// local backup itself) is recorded here, an upload outcome by OnDone.
func (o *offsiteService) finish(ctx context.Context, set offsite.Settings, info backup.Info, work func(context.Context) (backup.Info, error), actor string) {
	if _, err := work(ctx); err != nil {
		o.record(ctx, info.ID, actor, offsite.Result{At: o.clock().UTC(), Error: o.scrub(set, err)})
	}
}

// record saves the result and audits it. The audit detail holds the id, the
// outcome, the size and the error text, which never contains the secret key.
func (o *offsiteService) record(ctx context.Context, id, actor string, res offsite.Result) {
	ctx = context.WithoutCancel(ctx)
	if err := o.store.SaveResult(ctx, res); err != nil {
		o.logger.Error("off-site backup: cannot save the result", "id", id, "error", err)
	}
	detail := map[string]any{"id": id, "ok": res.OK, "size": res.Size}
	if res.Error != "" {
		detail["error"] = res.Error
	}
	if o.audit != nil {
		o.audit(ctx, admin.AuditEvent{Actor: actor, Action: "backups.offsite.upload", Target: "backup", Detail: detail})
	}
	if !res.OK {
		o.logger.Warn("off-site backup failed", "id", id, "error", res.Error)
	}
}

func (o *offsiteService) scrub(set offsite.Settings, err error) string {
	secret, _ := set.Secret(o.key)
	return o.scrubText(secret, err.Error())
}

// scrubText removes the secret key from provider error text.
func (o *offsiteService) scrubText(secret, s string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// setupOffsite builds the service over the backup engine and schedules its
// job. It does nothing without a database or when the job is disabled.
func (s *Server) setupOffsite(eng *backup.Engine) {
	if s.db == nil || s.backups == nil {
		return
	}
	s.offsite = &offsiteService{
		store: offsite.NewStore(s.db.Pool()), eng: eng, key: offsite.SecretKey(s.cfg.Secret),
		base:   func() context.Context { return s.lifecycleCtx },
		audit:  s.adminAudit,
		logger: s.logger, wg: &s.backups.wg,
	}
	s.offsiteAdm = &offsiteAdmin{store: offsite.NewStore(s.db.Pool()), svc: s.offsite, key: s.offsite.key}
	if s.admin != nil {
		s.admin.SetOffsite(s.offsiteAdm)
	}
	if s.scheduler != nil && workerJobEnabled(offsiteJob) {
		if err := s.scheduler.Schedule(offsiteJob, time.Minute, s.offsite.tick); err != nil {
			s.logger.Error("off-site backup job not scheduled", "error", err)
		}
	}
}
