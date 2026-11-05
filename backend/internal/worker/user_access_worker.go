package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/user"
)

// UserAccessLead is how long before an access end the owners are reminded.
const UserAccessLead = 72 * time.Hour

// UserAccessStore is the claim side of the job (user.AccessStore).
type UserAccessStore interface {
	ClaimEnded(ctx context.Context, now time.Time) ([]user.AccessUser, error)
	ReleaseEnded(ctx context.Context, id uuid.UUID, end time.Time) error
	ClaimReminders(ctx context.Context, now time.Time, lead time.Duration) ([]user.AccessUser, error)
	ReleaseReminder(ctx context.Context, id uuid.UUID, end time.Time) error
	ActiveOwnerEmails(ctx context.Context, now time.Time, excluding uuid.UUID) ([]string, error)
}

// UserAccessJob does the bookkeeping for expiring user access: it signs out
// and revokes the keys of users whose end has passed, writes the audit entry,
// and reminds the owners shortly before an end. The request-time checks are
// the security boundary; this job only cleans up and informs.
type UserAccessJob struct {
	Store UserAccessStore
	// Sessions deletes every session of a user.
	Sessions func(ctx context.Context, id uuid.UUID) error
	// Keys revokes the API keys the user created and returns how many.
	Keys func(ctx context.Context, id uuid.UUID) (int64, error)
	// Audit records one audit entry as the scheduler.
	Audit func(ctx context.Context, action, target string, detail map[string]any)
	// Configured reports whether mail can be sent.
	Configured func() bool
	// Notify mails one owner that u's access is ending.
	Notify func(ctx context.Context, to string, u user.AccessUser) error
	Now    func() time.Time
	Log    *slog.Logger
}

// Run handles ended users first, then reminders. A user that fails is logged
// and left for the next run; the first error is returned.
func (j *UserAccessJob) Run(ctx context.Context) error {
	now := j.Now()
	first := j.revoke(ctx, now)
	if err := j.remind(ctx, now); first == nil {
		first = err
	}
	return first
}

func (j *UserAccessJob) revoke(ctx context.Context, now time.Time) error {
	ended, err := j.Store.ClaimEnded(ctx, now)
	if err != nil {
		return fmt.Errorf("claim ended users: %w", err)
	}
	var first error
	for _, u := range ended {
		if err := j.revokeOne(ctx, u); err != nil {
			j.Log.Warn("user access: revoke failed", slog.String("user_id", u.ID.String()), slog.Any("error", err))
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// revokeOne revokes sessions, then keys, then audits. When revoking fails the
// claim is released (on a context that survives a cancelled tick) so the next
// run retries.
func (j *UserAccessJob) revokeOne(ctx context.Context, u user.AccessUser) error {
	keys, err := j.revokeAccess(ctx, u.ID)
	if err != nil {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if rerr := j.Store.ReleaseEnded(rctx, u.ID, u.EndsAt); rerr != nil {
			j.Log.Warn("user access: release failed", slog.String("user_id", u.ID.String()), slog.Any("error", rerr))
		}
		return err
	}
	j.Audit(ctx, "user.access_ended", u.Email, map[string]any{
		"email":        u.Email,
		"ended_at":     u.EndsAt.UTC().Format(time.RFC3339),
		"keys_revoked": keys,
	})
	return nil
}

func (j *UserAccessJob) revokeAccess(ctx context.Context, id uuid.UUID) (int64, error) {
	if err := j.Sessions(ctx, id); err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	keys, err := j.Keys(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("revoke keys: %w", err)
	}
	return keys, nil
}

// remind sends one reminder per user and end date. It claims nothing when mail
// is not configured, so the reminder is still due once mail works.
func (j *UserAccessJob) remind(ctx context.Context, now time.Time) error {
	if !j.Configured() {
		return nil
	}
	due, err := j.Store.ClaimReminders(ctx, now, UserAccessLead)
	if err != nil {
		return fmt.Errorf("claim reminders: %w", err)
	}
	for _, u := range due {
		j.remindOne(ctx, now, u)
	}
	return nil
}

func (j *UserAccessJob) remindOne(ctx context.Context, now time.Time, u user.AccessUser) {
	owners, err := j.Store.ActiveOwnerEmails(ctx, now, u.ID)
	sent, failed := 0, 0
	if err != nil {
		j.Log.Warn("user access: list owners failed", slog.Any("error", err))
		failed++
	}
	for _, to := range owners {
		if err := j.Notify(ctx, to, u); err != nil {
			j.Log.Warn("user access: reminder not sent", slog.Any("error", err))
			failed++
			continue
		}
		sent++
	}
	// Nobody got it and something went wrong: give the claim back so the next
	// run tries again. A partial failure is not retried.
	if sent == 0 && failed > 0 {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := j.Store.ReleaseReminder(rctx, u.ID, u.EndsAt); err != nil {
			j.Log.Warn("user access: release reminder failed", slog.Any("error", err))
		}
	}
}
