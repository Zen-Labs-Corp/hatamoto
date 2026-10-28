// Package installstats is the opt-in, anonymous install stats sender. Nothing
// is sent until the owner chooses Yes; see Service.Run for the rules.
package installstats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Consent is the owner's stored answer.
type Consent string

const (
	ConsentUnset Consent = "unset" // never asked, or not answered yet
	ConsentYes   Consent = "yes"
	ConsentNo    Consent = "no"
)

// sendInterval is the least time between two sends. A little under a day so a
// daily job with jitter is never skipped.
const sendInterval = "20 hours"

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// State is the stored row. InstallID is empty unless Consent is yes.
type State struct {
	Consent    Consent
	InstallID  string
	LastSentAt *time.Time
	LastStatus string
}

// Store reads and writes the single hatamoto_install_stats row. It does no
// caching: every call goes to the database, so a choice takes effect at once
// on every replica.
type Store struct{ db Querier }

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db} }

// Load returns the stored state; a missing row is consent unset.
func (s *Store) Load(ctx context.Context) (State, error) {
	var (
		st State
		id *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT consent, install_id, last_sent_at, last_status
		FROM hatamoto_install_stats WHERE id`).Scan(&st.Consent, &id, &st.LastSentAt, &st.LastStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return State{Consent: ConsentUnset}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("load install stats: %w", err)
	}
	if id != nil {
		st.InstallID = *id
	}
	return st, nil
}

// Enable records consent yes. An existing ID is kept; otherwise a new random
// one is created. This is the only place an ID comes into being besides Reset.
func (s *Store) Enable(ctx context.Context) (State, error) {
	id, err := newID()
	if err != nil {
		return State{}, err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO hatamoto_install_stats (id, consent, install_id) VALUES (TRUE, 'yes', $1)
		ON CONFLICT (id) DO UPDATE SET consent = 'yes',
			install_id = COALESCE(hatamoto_install_stats.install_id, EXCLUDED.install_id)`, id)
	if err != nil {
		return State{}, fmt.Errorf("enable install stats: %w", err)
	}
	return s.Load(ctx)
}

// Disable records consent no and deletes the ID, in one statement.
func (s *Store) Disable(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_install_stats (id, consent, install_id) VALUES (TRUE, 'no', NULL)
		ON CONFLICT (id) DO UPDATE SET consent = 'no', install_id = NULL`)
	if err != nil {
		return fmt.Errorf("disable install stats: %w", err)
	}
	return nil
}

// Reset replaces the ID with a new random one. It does nothing unless consent
// is yes: an ID never exists without it.
func (s *Store) Reset(ctx context.Context) (State, error) {
	id, err := newID()
	if err != nil {
		return State{}, err
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE hatamoto_install_stats SET install_id = $1 WHERE id AND consent = 'yes'`, id); err != nil {
		return State{}, fmt.Errorf("reset install id: %w", err)
	}
	return s.Load(ctx)
}

// Claim takes the right to send now: it stamps last_sent_at and returns the ID,
// but only when consent is yes and the last claim is older than the interval.
// The check and the stamp are one statement, so replicas and restarts cannot
// both send. ok is false when nothing may be sent.
func (s *Store) Claim(ctx context.Context) (id string, ok bool, err error) {
	err = s.db.QueryRow(ctx, `
		UPDATE hatamoto_install_stats SET last_sent_at = now()
		WHERE id AND consent = 'yes' AND install_id IS NOT NULL
		  AND (last_sent_at IS NULL OR last_sent_at < now() - interval '`+sendInterval+`')
		RETURNING install_id`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claim install stats send: %w", err)
	}
	return id, true, nil
}

// RecordResult stores the outcome of the last send. The text must not contain
// the install ID.
func (s *Store) RecordResult(ctx context.Context, status string) error {
	if _, err := s.db.Exec(ctx, `UPDATE hatamoto_install_stats SET last_status = $1 WHERE id`, status); err != nil {
		return fmt.Errorf("record install stats result: %w", err)
	}
	return nil
}

// newID returns 16 random bytes as hex.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("install id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
