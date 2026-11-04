// Package emaillog stores the email log: one row per mail the server handed
// to SMTP or dropped, with the retention that bounds how long it is kept.
package emaillog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/email"
)

// Retention bounds in days.
const (
	MinDays = 1
	MaxDays = 365
)

// ErrRetention reports a retention value outside 1..365 or a body period
// longer than the entry period.
var ErrRetention = errors.New("emaillog: invalid retention")

// ErrNotFound reports an unknown log id.
var ErrNotFound = errors.New("emaillog: entry not found")

// Row is one stored entry. List leaves the bodies empty.
type Row struct {
	ID         int64
	CreatedAt  time.Time
	Recipients []string
	Subject    string
	Kind       string
	Plugin     string
	Status     string
	Error      string
	MessageID  string
	// HasBody is true while a body is still stored.
	HasBody    bool
	BodyText   string
	BodyHTML   string
	ResentFrom int64 // 0 when the mail is not a resend
}

// Store reads and writes the log.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewStore returns a store on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, now: time.Now} }

// Record inserts one entry and returns its id. An empty body is stored as NULL.
func (s *Store) Record(ctx context.Context, e email.LogEntry) (int64, error) {
	rcpt := e.Recipients
	if rcpt == nil {
		rcpt = []string{}
	}
	var resent *int64
	if e.ResentFrom > 0 {
		resent = &e.ResentFrom
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO hatamoto_email_log
  (recipients, subject, kind, plugin, status, error, message_id, body_text, body_html, resent_from)
VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10)
RETURNING id`,
		rcpt, e.Subject, string(e.Kind), e.Plugin, e.Status, e.Error, e.MessageID, e.BodyText, e.BodyHTML, resent,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("emaillog: record: %w", err)
	}
	return id, nil
}

const rowColumns = `id, created_at, recipients, subject, kind, plugin, status, error, message_id,
  (body_text IS NOT NULL OR body_html IS NOT NULL), COALESCE(body_text, ''), COALESCE(body_html, ''), COALESCE(resent_from, 0)`

func scanRow(r pgx.Row) (Row, error) {
	var x Row
	err := r.Scan(&x.ID, &x.CreatedAt, &x.Recipients, &x.Subject, &x.Kind, &x.Plugin, &x.Status, &x.Error,
		&x.MessageID, &x.HasBody, &x.BodyText, &x.BodyHTML, &x.ResentFrom)
	return x, err
}

// List returns one page of entries, newest first, and the total that match.
// status "" matches every status. Bodies are never returned.
func (s *Store) List(ctx context.Context, status string, limit, offset int) ([]Row, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM hatamoto_email_log WHERE $1 = '' OR status = $1`, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("emaillog: count: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, created_at, recipients, subject, kind, plugin, status, error, message_id,
  (body_text IS NOT NULL OR body_html IS NOT NULL), '', '', COALESCE(resent_from, 0)
FROM hatamoto_email_log
WHERE $1 = '' OR status = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("emaillog: list: %w", err)
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		x, err := scanRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("emaillog: list: %w", err)
		}
		out = append(out, x)
	}
	return out, total, rows.Err()
}

// Get returns one entry with its bodies, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (Row, error) {
	x, err := scanRow(s.pool.QueryRow(ctx, `SELECT `+rowColumns+` FROM hatamoto_email_log WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	if err != nil {
		return Row{}, fmt.Errorf("emaillog: get: %w", err)
	}
	return x, nil
}

// Retention returns the days bodies and entries are kept.
func (s *Store) Retention(ctx context.Context) (bodyDays, entryDays int, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT email_log_body_days, email_log_days FROM hatamoto_site_config WHERE id`).Scan(&bodyDays, &entryDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultBodyDays, DefaultEntryDays, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("emaillog: retention: %w", err)
	}
	return bodyDays, entryDays, nil
}

// Retention defaults, used until an owner saves other values.
const (
	DefaultBodyDays  = 30
	DefaultEntryDays = 90
)

// SetRetention saves the retention days. Both must be 1..365 and body <= entry.
func (s *Store) SetRetention(ctx context.Context, body, entry int) error {
	if err := ValidateRetention(body, entry); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE hatamoto_site_config SET email_log_body_days = $1, email_log_days = $2 WHERE id`, body, entry)
	if err != nil {
		return fmt.Errorf("emaillog: set retention: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("emaillog: set retention: no site configuration row")
	}
	return nil
}

// ValidateRetention checks the retention rule without touching the database.
func ValidateRetention(body, entry int) error {
	switch {
	case body < MinDays || body > MaxDays, entry < MinDays || entry > MaxDays:
		return fmt.Errorf("%w: days must be %d to %d", ErrRetention, MinDays, MaxDays)
	case body > entry:
		return fmt.Errorf("%w: bodies cannot be kept longer than entries", ErrRetention)
	}
	return nil
}

// Purge drops bodies older than the body period and deletes entries older
// than the entry period. It returns the number of rows changed.
func (s *Store) Purge(ctx context.Context) (int64, error) {
	body, entry, err := s.Retention(ctx)
	if err != nil {
		return 0, err
	}
	now := s.now()
	bodyCut := now.AddDate(0, 0, -body)
	entryCut := now.AddDate(0, 0, -entry)
	del, err := s.pool.Exec(ctx, `DELETE FROM hatamoto_email_log WHERE created_at < $1`, entryCut)
	if err != nil {
		return 0, fmt.Errorf("emaillog: purge entries: %w", err)
	}
	upd, err := s.pool.Exec(ctx, `
UPDATE hatamoto_email_log SET body_text = NULL, body_html = NULL
WHERE created_at < $1 AND (body_text IS NOT NULL OR body_html IS NOT NULL)`, bodyCut)
	if err != nil {
		return del.RowsAffected(), fmt.Errorf("emaillog: purge bodies: %w", err)
	}
	return del.RowsAffected() + upd.RowsAffected(), nil
}
