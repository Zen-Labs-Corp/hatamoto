package server

import (
	"context"
	"math"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/core/internal/worker"
)

// userAccessJob is the WORKER_DISABLE_JOBS name of the hourly job that revokes
// the sessions and keys of users whose access ended and reminds the owners.
const userAccessJob = "user_access"

// scheduleUserAccess schedules the job unless there is no scheduler, database,
// mail service or session store, or WORKER_DISABLE_JOBS names it.
func (s *Server) scheduleUserAccess() bool {
	if s.db == nil || s.scheduler == nil || s.emailService == nil || s.sessionStore == nil || s.apiKeyService == nil || !workerJobEnabled(userAccessJob) {
		return false
	}
	job := &worker.UserAccessJob{
		Store:    user.NewAccessStore(s.db),
		Sessions: s.sessionStore.RevokeAllForUser,
		Keys:     s.apiKeyService.RevokeAllForCreator,
		Audit: func(ctx context.Context, action, target string, detail map[string]any) {
			s.adminAudit(ctx, admin.AuditEvent{Actor: schedulerActor, Action: action, Target: target, Detail: detail})
		},
		Configured: s.emailService.IsConfigured,
		Notify:     s.notifyAccessEnding,
		Now:        time.Now,
		Log:        s.logger,
	}
	err := s.scheduler.ScheduleJitter(userAccessJob, time.Hour, 10*time.Minute, 2*time.Minute, job.Run)
	if err != nil {
		s.logger.Error("user access job not scheduled", "error", err)
	}
	return err == nil
}

// notifyAccessEnding mails one owner. The address comes from the configured
// site, never from a request, and the date is shown in the site time zone.
func (s *Server) notifyAccessEnding(ctx context.Context, to string, u user.AccessUser) error {
	name, base, adminPath := s.digestSite(ctx)
	loc := time.UTC
	if s.siteConfig != nil {
		if c, err := s.siteConfig.Get(ctx); err == nil && c.Timezone != "" {
			if l, err := time.LoadLocation(c.Timezone); err == nil {
				loc = l
			}
		}
	}
	return s.emailService.SendAccessEnding(ctx, to, email.AccessEndingData{
		SiteName: name,
		Name:     u.Name,
		Email:    u.Email,
		Role:     string(u.Role),
		EndsOn:   u.EndsAt.Add(-time.Second).In(loc).Format("2 Jan 2006"),
		DaysLeft: int(math.Ceil(time.Until(u.EndsAt).Hours() / 24)),
		UsersURL: strings.TrimRight(base, "/") + adminPath + "/settings/users",
	})
}
