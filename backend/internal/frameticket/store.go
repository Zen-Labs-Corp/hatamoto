package frameticket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// Store keeps tickets in hatamoto_frame_tickets, shared by every server.
type Store struct{ db *database.DB }

// NewStore returns a Store on db.
func NewStore(db *database.DB) *Store { return &Store{db: db} }

// Put sweeps expired rows, inserts t, then drops whatever exceeds the count
// or byte bound, oldest first. Expiry is judged against the caller's now.
func (s *Store) Put(ctx context.Context, t Ticket, now time.Time) error {
	err := pgx.BeginFunc(ctx, s.db.Pool(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_frame_tickets WHERE expires_at < $1`, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO hatamoto_frame_tickets (nonce, purpose, session_id, target, expires_at, payload)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			t.Nonce, t.Purpose, t.Session, t.Target, t.Expiry, t.Payload); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM hatamoto_frame_tickets WHERE nonce IN (
			   SELECT nonce FROM (
			     SELECT nonce,
			            row_number() OVER w AS n,
			            sum(coalesce(octet_length(payload), 0)) OVER w AS b
			     FROM hatamoto_frame_tickets
			     WINDOW w AS (ORDER BY expires_at DESC, created_at DESC ROWS UNBOUNDED PRECEDING)) x
			   WHERE x.n > $1 OR x.b > $2)`, MaxTickets, MaxBytes)
		return err
	})
	if err != nil {
		return fmt.Errorf("frameticket: put: %w", err)
	}
	return nil
}

// Take spends the ticket under nonce in one DELETE ... RETURNING, so exactly
// one caller on any server gets it. An expired ticket is spent and reported
// as a miss.
func (s *Store) Take(ctx context.Context, nonce string, now time.Time) (Ticket, bool, error) {
	t := Ticket{Nonce: nonce}
	var sess uuid.UUID
	err := s.db.Pool().QueryRow(ctx,
		`DELETE FROM hatamoto_frame_tickets WHERE nonce = $1
		 RETURNING purpose, session_id, target, expires_at, payload`, nonce).
		Scan(&t.Purpose, &sess, &t.Target, &t.Expiry, &t.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ticket{}, false, nil
	}
	if err != nil {
		return Ticket{}, false, fmt.Errorf("frameticket: take: %w", err)
	}
	if now.After(t.Expiry) {
		return Ticket{}, false, nil
	}
	t.Session = sess
	t.Expiry = t.Expiry.UTC()
	return t, true, nil
}

// PurgeExpired deletes rows past expiry and returns how many went.
func (s *Store) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.db.Pool().Exec(ctx, `DELETE FROM hatamoto_frame_tickets WHERE expires_at < $1`, now)
	if err != nil {
		return 0, fmt.Errorf("frameticket: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}
