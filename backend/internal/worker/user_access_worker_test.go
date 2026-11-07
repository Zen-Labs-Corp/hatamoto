package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/user"
)

// fakeAccessStore claims like the SQL: a user is returned once per end value.
type fakeAccessStore struct {
	users    []*fakeAccessUser
	owners   map[uuid.UUID][]string // emails to return per affected user
	releases int
}

type fakeAccessUser struct {
	user.AccessUser
	endedFor, remindedFor time.Time
}

func (f *fakeAccessStore) ClaimEnded(_ context.Context, now time.Time) ([]user.AccessUser, error) {
	var out []user.AccessUser
	for _, u := range f.users {
		if !u.EndsAt.After(now) && !u.endedFor.Equal(u.EndsAt) {
			u.endedFor = u.EndsAt
			out = append(out, u.AccessUser)
		}
	}
	return out, nil
}

func (f *fakeAccessStore) ReleaseEnded(_ context.Context, id uuid.UUID, _ time.Time) error {
	for _, u := range f.users {
		if u.ID == id {
			u.endedFor = time.Time{}
		}
	}
	return nil
}

func (f *fakeAccessStore) ClaimReminders(_ context.Context, now time.Time, lead time.Duration) ([]user.AccessUser, error) {
	var out []user.AccessUser
	for _, u := range f.users {
		if u.EndsAt.After(now) && !u.EndsAt.After(now.Add(lead)) && !u.remindedFor.Equal(u.EndsAt) {
			u.remindedFor = u.EndsAt
			out = append(out, u.AccessUser)
		}
	}
	return out, nil
}

func (f *fakeAccessStore) ReleaseReminder(_ context.Context, id uuid.UUID, _ time.Time) error {
	f.releases++
	for _, u := range f.users {
		if u.ID == id {
			u.remindedFor = time.Time{}
		}
	}
	return nil
}

func (f *fakeAccessStore) ActiveOwnerEmails(_ context.Context, _ time.Time, id uuid.UUID) ([]string, error) {
	return f.owners[id], nil
}

type accessRig struct {
	store      *fakeAccessStore
	job        *UserAccessJob
	now        time.Time
	configured bool
	sessions   []uuid.UUID
	keys       []uuid.UUID
	audits     []string
	auditExtra []map[string]any
	mails      []string // "to>user email"
	sendErr    error
	revokeErr  error
}

func newAccessRig() *accessRig {
	r := &accessRig{store: &fakeAccessStore{owners: map[uuid.UUID][]string{}}, now: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC), configured: true}
	r.job = &UserAccessJob{
		Store: r.store,
		Sessions: func(_ context.Context, id uuid.UUID) error {
			if r.revokeErr != nil {
				return r.revokeErr
			}
			r.sessions = append(r.sessions, id)
			return nil
		},
		Keys: func(_ context.Context, id uuid.UUID) (int64, error) {
			r.keys = append(r.keys, id)
			return 2, nil
		},
		Audit: func(_ context.Context, action, target string, detail map[string]any) {
			r.audits = append(r.audits, action+" "+target)
			r.auditExtra = append(r.auditExtra, detail)
		},
		Configured: func() bool { return r.configured },
		Notify: func(_ context.Context, to string, u user.AccessUser) error {
			if r.sendErr != nil {
				return r.sendErr
			}
			r.mails = append(r.mails, to+">"+u.Email)
			return nil
		},
		Now: func() time.Time { return r.now },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return r
}

func (r *accessRig) add(email string, end time.Time, owners ...string) *fakeAccessUser {
	u := &fakeAccessUser{AccessUser: user.AccessUser{ID: uuid.New(), Email: email, Role: user.RoleEditor, EndsAt: end}}
	r.store.users = append(r.store.users, u)
	r.store.owners[u.ID] = owners
	return u
}

func TestUserAccessRevokesOnce(t *testing.T) {
	r := newAccessRig()
	ended := r.add("ended@example.test", r.now.Add(-time.Hour))
	r.add("open@example.test", r.now.Add(24*time.Hour*30))

	if err := r.job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.sessions, []uuid.UUID{ended.ID}) || !slices.Equal(r.keys, []uuid.UUID{ended.ID}) {
		t.Fatalf("revoked sessions %v keys %v", r.sessions, r.keys)
	}
	if !slices.Equal(r.audits, []string{"user.access_ended ended@example.test"}) || r.auditExtra[0]["keys_revoked"] != int64(2) {
		t.Fatalf("audits %v %v", r.audits, r.auditExtra)
	}
	if err := r.job.Run(context.Background()); err != nil || len(r.audits) != 1 || len(r.sessions) != 1 {
		t.Fatalf("second run did work: %v %v %v", err, r.audits, r.sessions)
	}
	// A later extension that passes again is claimed again (a new end value).
	ended.EndsAt = r.now.Add(-time.Minute)
	if err := r.job.Run(context.Background()); err != nil || len(r.audits) != 2 {
		t.Fatalf("changed end not handled: %v %v", err, r.audits)
	}
}

func TestUserAccessFailedRevokeIsRetried(t *testing.T) {
	r := newAccessRig()
	r.add("ended@example.test", r.now.Add(-time.Hour))
	r.revokeErr = errors.New("db down")
	if err := r.job.Run(context.Background()); err == nil {
		t.Fatal("error swallowed")
	}
	if len(r.audits) != 0 {
		t.Fatal("audited a failed revoke")
	}
	r.revokeErr = nil
	if err := r.job.Run(context.Background()); err != nil || len(r.audits) != 1 {
		t.Fatalf("retry: %v %v", err, r.audits)
	}
}

func TestUserAccessReminders(t *testing.T) {
	r := newAccessRig()
	u := r.add("soon@example.test", r.now.Add(48*time.Hour), "a@example.test", "b@example.test")
	r.add("far@example.test", r.now.Add(10*24*time.Hour), "a@example.test")

	if err := r.job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.mails, []string{"a@example.test>soon@example.test", "b@example.test>soon@example.test"}) {
		t.Fatalf("mails %v", r.mails)
	}
	_ = r.job.Run(context.Background())
	if len(r.mails) != 2 {
		t.Fatalf("second run sent again: %v", r.mails)
	}
	// A changed date re-notifies.
	u.EndsAt = r.now.Add(60 * time.Hour)
	_ = r.job.Run(context.Background())
	if len(r.mails) != 4 {
		t.Fatalf("changed date: %v", r.mails)
	}
}

func TestUserAccessReminderForDateLessThanThreeDaysAhead(t *testing.T) {
	r := newAccessRig()
	r.add("tomorrow@example.test", r.now.Add(24*time.Hour), "a@example.test")
	_ = r.job.Run(context.Background())
	if len(r.mails) != 1 {
		t.Fatalf("mails %v", r.mails)
	}
}

func TestUserAccessReminderNeedsMail(t *testing.T) {
	r := newAccessRig()
	u := r.add("soon@example.test", r.now.Add(48*time.Hour), "a@example.test")
	r.configured = false
	_ = r.job.Run(context.Background())
	if len(r.mails) != 0 || !u.remindedFor.IsZero() {
		t.Fatalf("claimed without mail: %v %v", r.mails, u.remindedFor)
	}
	r.configured = true
	_ = r.job.Run(context.Background())
	if len(r.mails) != 1 {
		t.Fatalf("not sent once mail works: %v", r.mails)
	}
}

func TestUserAccessAllSendsFailedReleasesTheClaim(t *testing.T) {
	r := newAccessRig()
	r.add("soon@example.test", r.now.Add(48*time.Hour), "a@example.test")
	r.sendErr = errors.New("smtp down")
	_ = r.job.Run(context.Background())
	if r.store.releases != 1 {
		t.Fatalf("releases = %d", r.store.releases)
	}
	r.sendErr = nil
	_ = r.job.Run(context.Background())
	if len(r.mails) != 1 {
		t.Fatalf("not retried: %v", r.mails)
	}
}
