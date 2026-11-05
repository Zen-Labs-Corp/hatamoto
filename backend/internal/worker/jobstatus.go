package worker

import (
	"context"
	"strings"
	"time"
	"unicode"

	"hatamoto.dev/core/internal/database"
)

// maxErrorRunes is the longest error text stored for a job run.
const maxErrorRunes = 500

// Run is the outcome of one job run.
type Run struct {
	ID       string
	Started  time.Time
	Finished time.Time
	OK       bool
	Err      string
}

// Duration is the wall time of the run.
func (r Run) Duration() time.Duration { return r.Finished.Sub(r.Started) }

// Recorder stores the last run of a job.
type Recorder interface {
	Record(ctx context.Context, r Run) error
}

// StatusStore keeps the last run of each job in hatamoto_job_status.
type StatusStore struct{ db *database.DB }

// NewStatusStore returns a store on db.
func NewStatusStore(db *database.DB) *StatusStore { return &StatusStore{db: db} }

// Record upserts the last run of r.ID.
func (s *StatusStore) Record(ctx context.Context, r Run) error {
	_, err := s.db.Pool().Exec(ctx, `
		INSERT INTO hatamoto_job_status (job_id, last_started_at, last_finished_at, duration_ms, ok, error, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (job_id) DO UPDATE SET
			last_started_at = EXCLUDED.last_started_at,
			last_finished_at = EXCLUDED.last_finished_at,
			duration_ms = EXCLUDED.duration_ms,
			ok = EXCLUDED.ok,
			error = EXCLUDED.error,
			updated_at = now()`,
		r.ID, r.Started, r.Finished, r.Duration().Milliseconds(), r.OK, truncateRunes(r.Err, maxErrorRunes))
	return err
}

// List returns the last run of every job that has one, by job id.
func (s *StatusStore) List(ctx context.Context) (map[string]Run, error) {
	rows, err := s.db.Pool().Query(ctx, `SELECT job_id, last_started_at, last_finished_at, ok, error FROM hatamoto_job_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Started, &r.Finished, &r.OK, &r.Err); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

// cleanError turns err into one line of at most 500 runes: control characters
// and line breaks become spaces.
func cleanError(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, err.Error())
	return truncateRunes(strings.TrimSpace(s), maxErrorRunes)
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
