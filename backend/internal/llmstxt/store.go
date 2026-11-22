package llmstxt

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/jsonx"
)

// maxChanges is how many recent changes the state row keeps.
const maxChanges = 20

// State is the stored state of both files.
type State struct {
	Snapshot      Snapshot
	Changes       []Change // newest first
	LLMsHash      string
	LLMsChangedAt *time.Time
	LLMsBytes     int
	FullHash      string
	FullChangedAt *time.Time
	FullBytes     int
}

// DB is the part of a pgx pool the Store needs.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store reads and writes the single hatamoto_ai_files_state row.
type Store struct{ db DB }

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db DB) *Store { return &Store{db: db} }

const selectState = `SELECT snapshot, changes, llms_hash, llms_changed_at, llms_bytes, full_hash, full_changed_at, full_bytes
FROM hatamoto_ai_files_state WHERE id`

// Get returns the stored state; a missing row is the zero State.
func (s *Store) Get(ctx context.Context) (State, error) {
	st, err := scanState(s.db.QueryRow(ctx, selectState))
	if errors.Is(err, pgx.ErrNoRows) {
		return State{}, nil
	}
	return st, err
}

func scanState(row pgx.Row) (State, error) {
	var st State
	var snap, changes []byte
	if err := row.Scan(&snap, &changes, &st.LLMsHash, &st.LLMsChangedAt, &st.LLMsBytes, &st.FullHash, &st.FullChangedAt, &st.FullBytes); err != nil {
		return State{}, err
	}
	if err := jsonx.Unmarshal(snap, &st.Snapshot); err != nil {
		return State{}, err
	}
	if err := jsonx.Unmarshal(changes, &st.Changes); err != nil {
		return State{}, err
	}
	return st, nil
}

// RecordIndex stores a built llms.txt: the diff against the previous snapshot
// joins the recent changes (the first build only records a baseline), and the
// change time moves only when the content hash does.
func (s *Store) RecordIndex(ctx context.Context, snap Snapshot, hash string, bytes int, excluded map[string]string, now time.Time) error {
	return s.update(ctx, func(st *State) error {
		var old Snapshot
		if st.LLMsHash != "" {
			old = st.Snapshot
		}
		st.Changes = Keep(st.Changes, Diff(old, snap, excluded, now), maxChanges)
		st.Snapshot = snap
		if st.LLMsHash != hash || st.LLMsChangedAt == nil {
			st.LLMsChangedAt = &now
		}
		st.LLMsHash, st.LLMsBytes = hash, bytes
		return nil
	})
}

// RecordFull stores a built llms-full.txt; the change time moves only when the
// content hash does.
func (s *Store) RecordFull(ctx context.Context, hash string, bytes int, now time.Time) error {
	return s.update(ctx, func(st *State) error {
		if st.FullHash != hash || st.FullChangedAt == nil {
			st.FullChangedAt = &now
		}
		st.FullHash, st.FullBytes = hash, bytes
		return nil
	})
}

// update runs fn on the locked row inside one transaction.
func (s *Store) update(ctx context.Context, fn func(*State) error) (err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_ai_files_state (id) VALUES (true) ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	st, err := scanState(tx.QueryRow(ctx, selectState+` FOR UPDATE`))
	if err != nil {
		return err
	}
	if err := fn(&st); err != nil {
		return err
	}
	if st.Snapshot == nil {
		st.Snapshot = Snapshot{}
	}
	if st.Changes == nil {
		st.Changes = []Change{}
	}
	snap, err := jsonx.Marshal(st.Snapshot)
	if err != nil {
		return err
	}
	changes, err := jsonx.Marshal(st.Changes)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_ai_files_state SET snapshot = $1, changes = $2,
		llms_hash = $3, llms_changed_at = $4, llms_bytes = $5, full_hash = $6, full_changed_at = $7, full_bytes = $8,
		updated_at = now() WHERE id`,
		snap, changes, st.LLMsHash, st.LLMsChangedAt, st.LLMsBytes, st.FullHash, st.FullChangedAt, st.FullBytes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
