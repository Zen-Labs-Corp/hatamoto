package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/standing"
	"hatamoto.dev/core/internal/user"
)

// standingDigestJob is the WORKER_DISABLE_JOBS name of the daily mail that
// lists the runs which published themselves.
const standingDigestJob = "standing_digest"

// digestJob sends the daily summary of automatic runs to the owners.
type digestJob struct {
	configured func() bool
	rows       func(ctx context.Context, since time.Time) ([]standing.DigestRow, error)
	claim      func(ctx context.Context, through time.Time) (bool, error)
	owners     func(ctx context.Context) ([]string, error)
	site       func(ctx context.Context) (name, base, adminPath string)
	send       func(ctx context.Context, to string, data email.RunDigestData) error
	now        func() time.Time
	log        *slog.Logger
}

// run sends at most one summary per UTC day across replicas and restarts: it
// claims the day just before sending, and only when mail is set up and
// something published automatically in the last 24 hours.
func (j *digestJob) run(ctx context.Context) error {
	if !j.configured() {
		return nil
	}
	now := j.now().UTC()
	rows, err := j.rows(ctx, now.Add(-24*time.Hour))
	if err != nil || len(rows) == 0 {
		return err
	}
	won, err := j.claim(ctx, now.Truncate(24*time.Hour))
	if err != nil || !won {
		return err
	}
	owners, err := j.owners(ctx)
	if err != nil {
		return fmt.Errorf("failed to list owners: %w", err)
	}
	name, base, adminPath := j.site(ctx)
	root := strings.TrimRight(base, "/") + adminPath
	data := email.RunDigestData{SiteName: name, HistoryURL: root + "/settings/changes"}
	for _, r := range rows {
		data.Runs = append(data.Runs, email.RunDigestRow{
			Plugin: r.Plugin, Run: r.Run, Changes: r.Changes, Created: r.Created,
			URL: root + "/plugins/" + url.PathEscape(r.Plugin) + "/changesets/" + strconv.FormatInt(r.ChangesetID, 10),
		})
	}
	for _, to := range owners {
		if err := j.send(ctx, to, data); err != nil {
			j.log.Warn("automatic updates summary not sent", slog.Any("error", err))
		}
	}
	return nil
}

// scheduleStandingDigest schedules the daily summary unless there is no
// scheduler, no mail service or WORKER_DISABLE_JOBS names it.
func (s *Server) scheduleStandingDigest() bool {
	if s.db == nil || s.scheduler == nil || s.emailService == nil || s.userRepo == nil || !workerJobEnabled(standingDigestJob) {
		return false
	}
	store := &standing.Store{DB: s.db}
	job := &digestJob{
		configured: s.emailService.IsConfigured,
		rows:       store.Digest,
		claim:      store.ClaimDigest,
		owners:     s.ownerEmails,
		site:       s.digestSite,
		send:       s.emailService.SendRunDigest,
		now:        time.Now,
		log:        s.logger,
	}
	err := s.scheduler.ScheduleJitter(standingDigestJob, 24*time.Hour, 30*time.Minute, 10*time.Minute, job.run)
	if err != nil {
		s.logger.Error("standing: summary job not scheduled", "error", err)
	}
	return err == nil
}

// digestSite reads the site name, the configured site address (never one
// derived from a request) and the admin address.
func (s *Server) digestSite(ctx context.Context) (name, base, adminPath string) {
	if s.cfg != nil {
		base = strings.TrimSpace(s.cfg.PublicSite.BaseURL)
	}
	if s.siteConfig != nil {
		if c, err := s.siteConfig.Get(ctx); err == nil {
			name = c.CompanyName
			if base == "" {
				base = strings.TrimSpace(c.PublicBaseURL)
			}
		}
	}
	if name == "" {
		name = "Hatamoto"
	}
	return name, base, s.adminPathValue()
}

// ownerEmails lists every owner's address, paging through the user list.
func (s *Server) ownerEmails(ctx context.Context) ([]string, error) {
	const page = 200
	var out []string
	for offset := 0; ; offset += page {
		users, err := s.userRepo.List(ctx, page, offset)
		if err != nil {
			return out, err
		}
		for _, u := range users {
			if u.Role == user.RoleOwner {
				out = append(out, u.Email)
			}
		}
		if len(users) < page {
			return out, nil
		}
	}
}
