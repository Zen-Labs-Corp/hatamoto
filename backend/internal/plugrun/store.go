// Package plugrun stores the state of plugin runs: the run row (phase,
// cursor, confirmed hosts, upload, outcome), the per-run buffer, the
// proposals a run makes and the metadata of uploads handed to a run. It
// never touches changesets or their items; the changeset engine owns those.
package plugrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/database"
)

// Run states.
const (
	StateRunning  = "running"
	StateFinished = "finished"
	StateFailed   = "failed"
)

// Run phases. A run moves fetch -> transform and then finishes.
const (
	PhaseFetch     = "fetch"
	PhaseTransform = "transform"
)

// Limits.
const (
	MaxRunSteps    = 100000 // steps a driver may take before it fails the run
	MaxCursorLen   = 4096
	MaxReasonLen   = 2048
	MaxProposals   = 100 // proposals per run
	MaxProposalLen = 64 << 10
)

var (
	// ErrActiveRun means the plugin already has a running run.
	ErrActiveRun = errors.New("plugrun: plugin already has an active run")
	// ErrQuota means a per-run limit (buffer or proposals) would be exceeded.
	ErrQuota = errors.New("plugrun: quota exceeded")
	// ErrNotFound means the run, buffer key or upload does not exist.
	ErrNotFound = errors.New("plugrun: not found")
	// ErrNotRunning means the run is missing or no longer running.
	ErrNotRunning = errors.New("plugrun: run is not running")
	// ErrInvalid means an argument is malformed (empty or oversize key, bad phase).
	ErrInvalid = errors.New("plugrun: invalid argument")
)

// What started a run.
const (
	TriggerOwner = "owner"
	TriggerCron  = "cron"
)

// Run is one row of hatamoto_runs.
type Run struct {
	ID          uuid.UUID
	Plugin      string
	Name        string
	State       string
	Phase       string
	Cursor      string
	Reason      string
	Params      json.RawMessage
	Hosts       []string
	UploadID    *uuid.UUID
	Progress    json.RawMessage
	Summary     json.RawMessage // nil until finished
	ChangesetID *int64
	Steps       int
	// Trigger is what started the run: "owner" (the default) or "cron".
	Trigger   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store is the run store.
type Store struct {
	DB  *database.DB
	Now func() time.Time // clock seam; nil is time.Now
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

const runColumns = `id, plugin, name, state, phase, cursor, reason, params, hosts, upload_id, progress, summary, changeset_id, steps, created_at, updated_at, trigger`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	var params, progress, summary []byte
	err := row.Scan(&r.ID, &r.Plugin, &r.Name, &r.State, &r.Phase, &r.Cursor, &r.Reason, &params, &r.Hosts,
		&r.UploadID, &progress, &summary, &r.ChangesetID, &r.Steps, &r.CreatedAt, &r.UpdatedAt, &r.Trigger)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, fmt.Errorf("failed to read run: %w", err)
	}
	r.Params, r.Progress = params, progress
	if summary != nil {
		r.Summary = summary
	}
	return r, nil
}

// Create inserts r as a running run in the fetch phase. A zero ID gets a new
// one. It returns ErrActiveRun when the plugin already has a running run; the
// database enforces that, so it holds across replicas.
func (s *Store) Create(ctx context.Context, r Run) (Run, error) {
	if r.Plugin == "" {
		return r, fmt.Errorf("%w: plugin", ErrInvalid)
	}
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if len(r.Params) == 0 {
		r.Params = json.RawMessage(`{}`)
	}
	if r.Hosts == nil {
		r.Hosts = []string{}
	}
	if r.Trigger == "" {
		r.Trigger = TriggerOwner
	}
	now := s.now()
	_, err := s.DB.Pool().Exec(ctx, `
		INSERT INTO hatamoto_runs (id, plugin, name, state, phase, params, hosts, upload_id, trigger, created_at, updated_at)
		VALUES ($1, $2, $3, 'running', 'fetch', $4, $5, $6, $7, $8, $8)`,
		r.ID, r.Plugin, r.Name, []byte(r.Params), r.Hosts, r.UploadID, r.Trigger, now)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "hatamoto_runs_one_active" {
			return r, ErrActiveRun
		}
		return r, fmt.Errorf("failed to create run: %w", err)
	}
	return s.Get(ctx, r.ID)
}

// Get returns a run.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Run, error) {
	return scanRun(s.DB.Pool().QueryRow(ctx, `SELECT `+runColumns+` FROM hatamoto_runs WHERE id = $1`, id))
}

// Active returns the running run of plugin, or ErrNotFound.
func (s *Store) Active(ctx context.Context, plugin string) (Run, error) {
	return scanRun(s.DB.Pool().QueryRow(ctx,
		`SELECT `+runColumns+` FROM hatamoto_runs WHERE plugin = $1 AND state = 'running'`, plugin))
}

// StaleRunning lists running runs not touched for olderThan (left by a crash).
func (s *Store) StaleRunning(ctx context.Context, olderThan time.Duration) ([]Run, error) {
	rows, err := s.DB.Pool().Query(ctx,
		`SELECT `+runColumns+` FROM hatamoto_runs WHERE state = 'running' AND updated_at < $1 ORDER BY created_at`,
		s.now().Add(-olderThan))
	if err != nil {
		return nil, fmt.Errorf("failed to list stale runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// update runs one UPDATE on a running run; set is the SET clause (without the
// updated_at assignment) with $3.. for args. No row changed => ErrNotRunning.
func (s *Store) update(ctx context.Context, id uuid.UUID, set string, args ...any) error {
	all := append([]any{id, s.now()}, args...)
	tag, err := s.DB.Pool().Exec(ctx,
		`UPDATE hatamoto_runs SET updated_at = $2, `+set+` WHERE id = $1 AND state = 'running'`, all...)
	if err != nil {
		return fmt.Errorf("failed to update run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRunning
	}
	return nil
}

// SetChangeset records the run's changeset.
func (s *Store) SetChangeset(ctx context.Context, id uuid.UUID, changesetID int64) error {
	return s.update(ctx, id, `changeset_id = $3`, changesetID)
}

// Checkpoint saves the plugin's resume cursor.
func (s *Store) Checkpoint(ctx context.Context, id uuid.UUID, cursor string) error {
	if len(cursor) > MaxCursorLen {
		return fmt.Errorf("%w: cursor over %d bytes", ErrInvalid, MaxCursorLen)
	}
	return s.update(ctx, id, `cursor = $3`, cursor)
}

// AdvancePhase moves a run from fetch to transform and clears the cursor.
// Any other transition is ErrInvalid.
func (s *Store) AdvancePhase(ctx context.Context, id uuid.UUID, to string) error {
	if to != PhaseTransform {
		return fmt.Errorf("%w: phase %q", ErrInvalid, to)
	}
	tag, err := s.DB.Pool().Exec(ctx,
		`UPDATE hatamoto_runs SET updated_at = $2, phase = 'transform', cursor = ''
		 WHERE id = $1 AND state = 'running' AND phase = 'fetch'`, id, s.now())
	if err != nil {
		return fmt.Errorf("failed to advance run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRunning
	}
	return nil
}

// Progress replaces the progress document (any JSON value).
func (s *Store) Progress(ctx context.Context, id uuid.UUID, progress json.RawMessage) error {
	if !json.Valid(progress) {
		return fmt.Errorf("%w: progress is not JSON", ErrInvalid)
	}
	return s.update(ctx, id, `progress = $3`, []byte(progress))
}

// BumpSteps counts one driver step and returns the new total.
func (s *Store) BumpSteps(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := s.DB.Pool().QueryRow(ctx,
		`UPDATE hatamoto_runs SET updated_at = $2, steps = steps + 1 WHERE id = $1 AND state = 'running' RETURNING steps`,
		id, s.now()).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotRunning
	}
	if err != nil {
		return 0, fmt.Errorf("failed to count step: %w", err)
	}
	return n, nil
}

// Finish ends a running run successfully. summary may be nil.
func (s *Store) Finish(ctx context.Context, id uuid.UUID, summary json.RawMessage) error {
	var sum any
	if len(summary) > 0 {
		if !json.Valid(summary) {
			return fmt.Errorf("%w: summary is not JSON", ErrInvalid)
		}
		sum = []byte(summary)
	}
	return s.update(ctx, id, `state = 'finished', summary = $3`, sum)
}

// Fail ends a running run as failed with a reason (truncated to MaxReasonLen).
func (s *Store) Fail(ctx context.Context, id uuid.UUID, reason string) error {
	if len(reason) > MaxReasonLen {
		reason = reason[:MaxReasonLen]
	}
	return s.update(ctx, id, `state = 'failed', reason = $3`, reason)
}

// Proposal is a change a run asks the owner to sign off.
type Proposal struct {
	ID        int64
	Kind      string
	Payload   json.RawMessage
	CreatedAt time.Time
	// Decision is "" until the owner decides, then one of the Proposal*
	// constants. Before is the pre-image stored when a proposal was approved
	// (nil otherwise); DecidedAt is nil while undecided.
	Decision  string
	Before    json.RawMessage
	DecidedAt *time.Time
}

// Proposal decisions.
const (
	ProposalApproved = "approved"
	ProposalRejected = "rejected"
	ProposalReverted = "reverted"
)

// ErrProposalDecided means the proposal already carries a decision.
var ErrProposalDecided = errors.New("proposal already decided")

// AddProposal stores a proposal of a running run; ErrQuota past MaxProposals.
func (s *Store) AddProposal(ctx context.Context, id uuid.UUID, kind string, payload json.RawMessage) error {
	if kind == "" || len(payload) > MaxProposalLen || !json.Valid(payload) {
		return fmt.Errorf("%w: proposal", ErrInvalid)
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockRunning(ctx, tx, id); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_run_proposals WHERE run_id = $1`, id).Scan(&n); err != nil {
			return fmt.Errorf("failed to count proposals: %w", err)
		}
		if n >= MaxProposals {
			return fmt.Errorf("%w: more than %d proposals", ErrQuota, MaxProposals)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_run_proposals (run_id, kind, payload, created_at) VALUES ($1, $2, $3, $4)`,
			id, kind, []byte(payload), s.now()); err != nil {
			return fmt.Errorf("failed to store proposal: %w", err)
		}
		return nil
	})
}

// Proposals lists the proposals of a run in the order they were made.
func (s *Store) Proposals(ctx context.Context, id uuid.UUID) ([]Proposal, error) {
	rows, err := s.DB.Pool().Query(ctx,
		`SELECT id, kind, payload, created_at, COALESCE(decision, ''), before, decided_at
		 FROM hatamoto_run_proposals WHERE run_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list proposals: %w", err)
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		var p Proposal
		var payload, before []byte
		if err := rows.Scan(&p.ID, &p.Kind, &payload, &p.CreatedAt, &p.Decision, &before, &p.DecidedAt); err != nil {
			return nil, err
		}
		p.Payload = payload
		if before != nil {
			p.Before = before
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetProposalDecision records the owner's decision (ProposalApproved or
// ProposalRejected) on a proposal of run runID. before is the pre-image kept
// for Undo; it is stored for approvals only. A proposal that already has a
// decision yields ErrProposalDecided, a missing one ErrNotFound.
func (s *Store) SetProposalDecision(ctx context.Context, runID uuid.UUID, id int64, decision string, before json.RawMessage) error {
	if decision != ProposalApproved && decision != ProposalRejected {
		return fmt.Errorf("%w: decision", ErrInvalid)
	}
	var b []byte
	if decision == ProposalApproved && len(before) > 0 {
		if !json.Valid(before) {
			return fmt.Errorf("%w: before", ErrInvalid)
		}
		b = before
	}
	tag, err := s.DB.Pool().Exec(ctx, `
		UPDATE hatamoto_run_proposals SET decision = $3, before = $4, decided_at = $5
		WHERE run_id = $1 AND id = $2 AND decision IS NULL`, runID, id, decision, b, s.now())
	if err != nil {
		return fmt.Errorf("failed to decide proposal: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return s.proposalMissOrDecided(ctx, runID, id)
}

func (s *Store) proposalMissOrDecided(ctx context.Context, runID uuid.UUID, id int64) error {
	var ok bool
	if err := s.DB.Pool().QueryRow(ctx,
		`SELECT true FROM hatamoto_run_proposals WHERE run_id = $1 AND id = $2`, runID, id).Scan(&ok); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("failed to read proposal: %w", err)
	}
	return ErrProposalDecided
}

// SetProposalBefore stores the pre-image of an approved proposal at the moment
// its change is applied, for Undo. It yields ErrNotFound unless the proposal is
// approved and has no pre-image yet.
func (s *Store) SetProposalBefore(ctx context.Context, runID uuid.UUID, id int64, before json.RawMessage) error {
	if !json.Valid(before) {
		return fmt.Errorf("%w: before", ErrInvalid)
	}
	tag, err := s.DB.Pool().Exec(ctx, `
		UPDATE hatamoto_run_proposals SET before = $3
		WHERE run_id = $1 AND id = $2 AND decision = 'approved' AND before IS NULL`, runID, id, []byte(before))
	if err != nil {
		return fmt.Errorf("failed to store pre-image: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// UndecidedProposals counts the proposals of a run without a decision.
func (s *Store) UndecidedProposals(ctx context.Context, runID uuid.UUID) (int, error) {
	var n int
	if err := s.DB.Pool().QueryRow(ctx,
		`SELECT count(*) FROM hatamoto_run_proposals WHERE run_id = $1 AND decision IS NULL`, runID).Scan(&n); err != nil {
		return 0, fmt.Errorf("failed to count undecided proposals: %w", err)
	}
	return n, nil
}

// ClearProposalDecision marks an approved proposal as reverted after Undo
// restored its pre-image. The decision becomes ProposalReverted and Before is
// kept. Anything but an approved proposal yields ErrNotFound.
func (s *Store) ClearProposalDecision(ctx context.Context, runID uuid.UUID, id int64) error {
	tag, err := s.DB.Pool().Exec(ctx, `
		UPDATE hatamoto_run_proposals SET decision = 'reverted', decided_at = $3
		WHERE run_id = $1 AND id = $2 AND decision = 'approved'`, runID, id, s.now())
	if err != nil {
		return fmt.Errorf("failed to revert proposal: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// Recent lists runs newest first; plugin "" means all plugins. limit is
// clamped to 1..200.
func (s *Store) Recent(ctx context.Context, plugin string, limit int) ([]Run, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.DB.Pool().Query(ctx,
		`SELECT `+runColumns+` FROM hatamoto_runs WHERE $1 = '' OR plugin = $1 ORDER BY created_at DESC, id LIMIT $2`,
		plugin, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Upload is the metadata of a file handed to a run. BlobKey names the stored
// bytes; it is never a filesystem path.
type Upload struct {
	ID        uuid.UUID
	Plugin    string
	Filename  string
	Size      int64
	BlobKey   string
	CreatedAt time.Time
}

// AddUpload records an upload and returns its id.
func (s *Store) AddUpload(ctx context.Context, u Upload) (uuid.UUID, error) {
	if u.Plugin == "" || u.Filename == "" || u.BlobKey == "" || u.Size < 0 {
		return uuid.Nil, fmt.Errorf("%w: upload", ErrInvalid)
	}
	var id uuid.UUID
	err := s.DB.Pool().QueryRow(ctx, `
		INSERT INTO hatamoto_run_uploads (plugin, filename, size, blob_key, created_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		u.Plugin, u.Filename, u.Size, u.BlobKey, s.now()).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to store upload: %w", err)
	}
	return id, nil
}

// Upload returns the metadata of an upload, or ErrNotFound.
func (s *Store) Upload(ctx context.Context, id uuid.UUID) (Upload, error) {
	var u Upload
	err := s.DB.Pool().QueryRow(ctx,
		`SELECT id, plugin, filename, size, blob_key, created_at FROM hatamoto_run_uploads WHERE id = $1`, id).
		Scan(&u.ID, &u.Plugin, &u.Filename, &u.Size, &u.BlobKey, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	if err != nil {
		return u, fmt.Errorf("failed to read upload: %w", err)
	}
	return u, nil
}

// lockRunning row-locks a run and requires it to be running. Writers that
// check a quota take this lock so concurrent writers see each other.
func lockRunning(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var state string
	err := tx.QueryRow(ctx, `SELECT state FROM hatamoto_runs WHERE id = $1 FOR UPDATE`, id).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && state != StateRunning) {
		return ErrNotRunning
	}
	if err != nil {
		return fmt.Errorf("failed to lock run: %w", err)
	}
	return nil
}
