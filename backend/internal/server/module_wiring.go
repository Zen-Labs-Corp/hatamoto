package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/modhost"
	"hatamoto.dev/core/internal/modmail"
	"hatamoto.dev/core/internal/user"
	"hatamoto.dev/core/internal/worker"
)

// wireModuleJobs schedules the jobs compiled-in modules registered and returns
// how many it scheduled. Nothing is created when no module registered a job.
func (s *Server) wireModuleJobs() int {
	registered := modhost.Jobs()
	if len(registered) == 0 {
		return 0
	}
	if s.scheduler == nil || s.db == nil {
		s.logger.Info("module jobs not scheduled: the worker is off here", "count", len(registered))
		return 0
	}
	var jobs []worker.ModuleJob
	for _, j := range registered {
		if err := modhost.MigrationErr(j.Module); err != nil {
			s.logger.Warn("module job not scheduled: its migration failed", "job", j.ID(), "error", err)
			continue
		}
		if !workerJobEnabled(j.ID()) {
			continue
		}
		jobs = append(jobs, worker.ModuleJob{ID: j.ID(), Every: j.Every, Run: j.Run})
	}
	return worker.ScheduleModuleJobs(s.scheduler, worker.NewClaimer(s.db).Claim, jobs, s.logger)
}

// wireModuleMail gives modules the mail funnel. It only stores a pointer, so it
// is installed whenever there is a database; a missing mail service surfaces
// as modhost.ErrNoMailer at send time.
func (s *Server) wireModuleMail() {
	if s.db == nil || s.userRepo == nil {
		return
	}
	modhost.SetMailer(&modmail.Adapter{
		Sender: s.moduleMailSender,
		Owners: func(ctx context.Context) ([]string, error) {
			return user.NewAccessStore(s.db).ActiveOwnerEmails(ctx, time.Now().UTC(), uuid.Nil)
		},
		UserEmail: func(ctx context.Context, id uuid.UUID) (string, error) {
			u, err := s.userRepo.GetByID(ctx, id)
			if errors.Is(err, user.ErrUserNotFound) {
				return "", modhost.ErrUnknownUser
			}
			if err != nil {
				return "", err
			}
			return u.Email, nil
		},
	})
}

// moduleMailSender returns the mail service as an interface value, or an
// untyped nil when there is none (a nil *email.Service in an interface would
// not compare equal to nil).
func (s *Server) moduleMailSender() modmail.Sender {
	if s.emailService == nil {
		return nil
	}
	return s.emailService
}

// moduleJobName turns module:<module>:<job> into "Module: <module> > <job>".
func moduleJobName(id string) string {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) < 3 {
		return id
	}
	return "Module: " + parts[1] + " > " + parts[2]
}

// moduleJobEvery returns the interval a module job was registered with.
func moduleJobEvery(id string) time.Duration {
	for _, j := range modhost.Jobs() {
		if j.ID() == id {
			return j.Every
		}
	}
	return 0
}
