package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
)

// ErrAccessEndPast is returned when a new access end is not in the future.
var ErrAccessEndPast = errors.New("access end must be in the future")

const dateLayout = "2006-01-02"

// AccessEndFromDate turns the day an owner picked (YYYY-MM-DD, in loc) into the
// stored instant: the start of the next day in loc, in UTC. Access lasts
// through the whole chosen day.
func AccessEndFromDate(date string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	d, err := time.ParseInLocation(dateLayout, date, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: %w", date, err)
	}
	// time.Date normalises d+1 across month ends and picks the right offset on
	// a day with a clock change.
	return time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc).UTC(), nil
}

// AccessEndDay renders a stored end back as the day the owner chose.
func AccessEndDay(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.Add(-time.Second).In(loc).Format(dateLayout)
}

// ValidateAccessEnd refuses an end that is not after now.
func ValidateAccessEnd(t, now time.Time) error {
	if !t.After(now) {
		return ErrAccessEndPast
	}
	return nil
}

// EnsureOpenOwner takes the owner lock and returns ErrLastOwner unless an
// owner other than excluding has no end date. Call it inside the transaction
// that gives an owner an end date.
func EnsureOpenOwner(ctx context.Context, q Queryer, excluding uuid.UUID) error {
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerGuardKey); err != nil {
		return err
	}
	var others int
	err := q.QueryRow(ctx, `SELECT count(*) FROM hatamoto_users
		WHERE role = 'owner' AND id <> $1 AND access_ends_at IS NULL`, excluding).Scan(&others)
	if err != nil {
		return err
	}
	if others == 0 {
		return ErrLastOwner
	}
	return nil
}

// AccessUser is a user the access worker acts on.
type AccessUser struct {
	ID     uuid.UUID
	Email  string
	Name   string
	Role   Role
	EndsAt time.Time
}

// AccessStore holds the worker's claim queries. Each claim is one atomic
// UPDATE, so replicas and reruns do each piece of work once.
type AccessStore struct {
	db *database.DB
}

// NewAccessStore creates the store.
func NewAccessStore(db *database.DB) *AccessStore { return &AccessStore{db: db} }

const accessUserCols = `id, email, COALESCE(name, ''), role, access_ends_at`

func (s *AccessStore) claim(ctx context.Context, query string, args ...any) ([]AccessUser, error) {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccessUser
	for rows.Next() {
		var u AccessUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.EndsAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ClaimEnded marks every user whose end has passed as handled for that end and
// returns them. A user whose date changed is claimed again.
func (s *AccessStore) ClaimEnded(ctx context.Context, now time.Time) ([]AccessUser, error) {
	return s.claim(ctx, `UPDATE hatamoto_users SET access_ended_for = access_ends_at
		WHERE access_ends_at <= $1 AND access_ended_for IS DISTINCT FROM access_ends_at
		RETURNING `+accessUserCols, now)
}

// ReleaseEnded undoes a ClaimEnded so the next run retries.
func (s *AccessStore) ReleaseEnded(ctx context.Context, id uuid.UUID, end time.Time) error {
	return s.exec(ctx, `UPDATE hatamoto_users SET access_ended_for = NULL
		WHERE id = $1 AND access_ended_for = $2`, id, end)
}

// ClaimReminders marks every user whose end is still ahead but within lead as
// reminded for that end and returns them.
func (s *AccessStore) ClaimReminders(ctx context.Context, now time.Time, lead time.Duration) ([]AccessUser, error) {
	return s.claim(ctx, `UPDATE hatamoto_users SET access_reminder_for = access_ends_at
		WHERE access_ends_at > $1 AND access_ends_at <= $2
		  AND access_reminder_for IS DISTINCT FROM access_ends_at
		RETURNING `+accessUserCols, now, now.Add(lead))
}

// ReleaseReminder undoes a ClaimReminders so the next run retries.
func (s *AccessStore) ReleaseReminder(ctx context.Context, id uuid.UUID, end time.Time) error {
	return s.exec(ctx, `UPDATE hatamoto_users SET access_reminder_for = NULL
		WHERE id = $1 AND access_reminder_for = $2`, id, end)
}

func (s *AccessStore) exec(ctx context.Context, query string, args ...any) error {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, query, args...)
	return err
}

// ActiveOwnerEmails lists the owners whose own access has not ended, except one.
func (s *AccessStore) ActiveOwnerEmails(ctx context.Context, now time.Time, excluding uuid.UUID) ([]string, error) {
	conn, err := s.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT email FROM hatamoto_users
		WHERE role = 'owner' AND id <> $1 AND (access_ends_at IS NULL OR access_ends_at > $2)
		ORDER BY email`, excluding, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
