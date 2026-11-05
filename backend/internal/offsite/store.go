package offsite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Store reads and writes the single hatamoto_offsite_settings row.
type Store struct{ db Querier }

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db} }

// Get returns the saved settings; found is false when no row exists.
func (s *Store) Get(ctx context.Context) (set Settings, found bool, err error) {
	var nextDue, lastAt *time.Time
	err = s.db.QueryRow(ctx, `
		SELECT enabled, service, endpoint, region, bucket, prefix, access_key, secret_sealed,
		       schedule, hour, weekday, keep, next_due_at, last_at, last_ok, last_size,
		       last_sha256, last_error, updated_at
		FROM hatamoto_offsite_settings WHERE id`).Scan(
		&set.Enabled, &set.Service, &set.Endpoint, &set.Region, &set.Bucket, &set.Prefix, &set.AccessKey,
		&set.SecretSealed, &set.Schedule.Kind, &set.Schedule.Hour, &set.Schedule.Weekday, &set.Keep,
		&nextDue, &lastAt, &set.Last.OK, &set.Last.Size, &set.Last.SHA256, &set.Last.Error, &set.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("load offsite settings: %w", err)
	}
	if nextDue != nil {
		set.NextDueAt = *nextDue
	}
	if lastAt != nil {
		set.Last.At = *lastAt
	}
	return set, true, nil
}

// Save validates set and stores the target, schedule and NextDueAt. It leaves
// the last result alone. The caller seals the secret key into SecretSealed.
func (s *Store) Save(ctx context.Context, set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	var due *time.Time
	if !set.NextDueAt.IsZero() {
		due = &set.NextDueAt
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_offsite_settings
			(id, enabled, service, endpoint, region, bucket, prefix, access_key, secret_sealed,
			 schedule, hour, weekday, keep, next_due_at, updated_at)
		VALUES (TRUE, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, service = EXCLUDED.service,
			endpoint = EXCLUDED.endpoint, region = EXCLUDED.region, bucket = EXCLUDED.bucket,
			prefix = EXCLUDED.prefix, access_key = EXCLUDED.access_key,
			secret_sealed = EXCLUDED.secret_sealed, schedule = EXCLUDED.schedule,
			hour = EXCLUDED.hour, weekday = EXCLUDED.weekday, keep = EXCLUDED.keep,
			next_due_at = EXCLUDED.next_due_at, updated_at = now()`,
		set.Enabled, set.Service, set.Endpoint, set.Region, set.Bucket, set.Prefix, set.AccessKey,
		set.SecretSealed, set.Schedule.Kind, set.Schedule.Hour, set.Schedule.Weekday, set.Keep, due)
	if err != nil {
		return fmt.Errorf("save offsite settings: %w", err)
	}
	return nil
}

// Clear deletes the saved row.
func (s *Store) Clear(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM hatamoto_offsite_settings WHERE id`); err != nil {
		return fmt.Errorf("clear offsite settings: %w", err)
	}
	return nil
}

// Claim takes the due slot: when the schedule is on and next_due_at is at or
// before now it moves next_due_at to next and reports true. One UPDATE decides,
// so of several replicas or ticks exactly one wins a slot.
func (s *Store) Claim(ctx context.Context, now, next time.Time) (bool, error) {
	var id bool
	err := s.db.QueryRow(ctx, `
		UPDATE hatamoto_offsite_settings SET next_due_at = $2
		WHERE id AND enabled AND schedule <> 'off' AND next_due_at IS NOT NULL AND next_due_at <= $1
		RETURNING id`, now, next).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim offsite slot: %w", err)
	}
	return true, nil
}

// Defer moves next_due_at to until, to retry a slot that could not run.
func (s *Store) Defer(ctx context.Context, until time.Time) error {
	if _, err := s.db.Exec(ctx, `UPDATE hatamoto_offsite_settings SET next_due_at = $1 WHERE id`, until); err != nil {
		return fmt.Errorf("defer offsite slot: %w", err)
	}
	return nil
}

// SaveResult records the outcome of the latest upload.
func (s *Store) SaveResult(ctx context.Context, r Result) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE hatamoto_offsite_settings
		SET last_at = $1, last_ok = $2, last_size = $3, last_sha256 = $4, last_error = $5
		WHERE id`, r.At, r.OK, r.Size, r.SHA256, r.Error); err != nil {
		return fmt.Errorf("save offsite result: %w", err)
	}
	return nil
}
