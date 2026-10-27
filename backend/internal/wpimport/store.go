package wpimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Kinds of the id map.
const (
	KindEntity   = "entity" // posts, pages and custom post types
	KindTerm     = "term"
	KindAuthor   = "author"
	KindMedia    = "media"
	KindMenu     = "menu"
	KindRedirect = "redirect"
)

// Run statuses.
const (
	RunRunning   = "running"
	RunCompleted = "completed"
	RunFailed    = "failed"
	RunScanned   = "scanned"
)

// Run is one row of hatamoto_import_runs.
type Run struct {
	ID        uuid.UUID
	Source    string
	SourceRef string
	Status    string
	Phase     string
	Options   json.RawMessage
	Report    json.RawMessage
	Error     string
}

// Store persists runs and the source-id to Hatamoto-id map.
type Store struct {
	pool *pgxpool.Pool
	// ref scopes map rows to one source site ("wordpress:https://example.com").
	ref string
}

// NewStore returns a store for one source site.
func NewStore(pool *pgxpool.Pool, sourceRef string) *Store {
	return &Store{pool: pool, ref: SourceName + ":" + strings.ToLower(strings.TrimRight(strings.TrimSpace(sourceRef), "/"))}
}

// Ref is the source reference runs and map rows of this store carry.
func (s *Store) Ref() string { return s.ref }

// StartRun records a new run.
func (s *Store) StartRun(ctx context.Context, status string, options any) (*Run, error) {
	opts, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	r := &Run{Source: SourceName, SourceRef: s.ref, Status: status, Options: opts}
	err = s.pool.QueryRow(ctx, `INSERT INTO hatamoto_import_runs (source, source_ref, status, options) VALUES ($1, $2, $3, $4) RETURNING id`,
		SourceName, s.ref, status, opts).Scan(&r.ID)
	return r, err
}

// GetRun loads a run; a run of another source site is an error.
func (s *Store) GetRun(ctx context.Context, id uuid.UUID) (*Run, error) {
	r := &Run{ID: id}
	err := s.pool.QueryRow(ctx, `SELECT source, source_ref, status, phase, options, report, error FROM hatamoto_import_runs WHERE id = $1`, id).
		Scan(&r.Source, &r.SourceRef, &r.Status, &r.Phase, &r.Options, &r.Report, &r.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("import run %s not found", id)
	}
	return r, err
}

// ResumeRun marks a run running again.
func (s *Store) ResumeRun(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE hatamoto_import_runs SET status = 'running', error = '', finished_at = NULL WHERE id = $1`, id)
	return err
}

// SetPhase records the phase a run reached.
func (s *Store) SetPhase(ctx context.Context, id uuid.UUID, phase string) error {
	_, err := s.pool.Exec(ctx, `UPDATE hatamoto_import_runs SET phase = $2 WHERE id = $1`, id, phase)
	return err
}

// FinishRun stores the outcome of a run.
func (s *Store) FinishRun(ctx context.Context, id uuid.UUID, status string, report any, runErr error) error {
	rep, err := json.Marshal(report)
	if err != nil {
		return err
	}
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
	}
	_, err = s.pool.Exec(ctx, `UPDATE hatamoto_import_runs SET status = $2, report = $3, error = $4, finished_at = now() WHERE id = $1`, id, status, rep, msg)
	return err
}

// Lookup returns the Hatamoto id a source id was imported as.
func (s *Store) Lookup(ctx context.Context, kind, sourceID string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT target_id FROM hatamoto_import_map WHERE source = $1 AND kind = $2 AND source_id = $3`, s.ref, kind, sourceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// LookupHash returns a target imported with the same content hash (media dedup).
func (s *Store) LookupHash(ctx context.Context, kind, hash string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT target_id FROM hatamoto_import_map WHERE source = $1 AND kind = $2 AND hash = $3 LIMIT 1`, s.ref, kind, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// Put records or updates a mapping.
func (s *Store) Put(ctx context.Context, run uuid.UUID, kind, sourceID string, target uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO hatamoto_import_map (source, kind, source_id, target_id, hash, run_id) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source, kind, source_id) DO UPDATE SET target_id = EXCLUDED.target_id, hash = EXCLUDED.hash, run_id = EXCLUDED.run_id, updated_at = now()`,
		s.ref, kind, sourceID, target, hash, run)
	return err
}

// All returns every mapping of a kind (source id to target id).
func (s *Store) All(ctx context.Context, kind string) (map[string]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT source_id, target_id FROM hatamoto_import_map WHERE source = $1 AND kind = $2`, s.ref, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var k string
		var id uuid.UUID
		if err := rows.Scan(&k, &id); err != nil {
			return nil, err
		}
		out[k] = id
	}
	return out, rows.Err()
}

// Delete forgets a mapping.
func (s *Store) Delete(ctx context.Context, kind, sourceID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM hatamoto_import_map WHERE source = $1 AND kind = $2 AND source_id = $3`, s.ref, kind, sourceID)
	return err
}

// EditedEntities returns the source ids of imported entities that were changed
// in Hatamoto after the importer last wrote them: the hash of an entity row is
// the updated_at the importer left behind.
func (s *Store) EditedEntities(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.source_id, e.updated_at, m.hash FROM hatamoto_import_map m
		JOIN hatamoto_entities e ON e.id = m.target_id
		WHERE m.source = $1 AND m.kind = $2 AND m.hash <> ''`, s.ref, KindEntity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id, hash string
		var updated time.Time
		if err := rows.Scan(&id, &updated, &hash); err != nil {
			return nil, err
		}
		if was, err := time.Parse(time.RFC3339Nano, hash); err == nil && !updated.Equal(was) {
			out[id] = true
		}
	}
	return out, rows.Err()
}

// stamp is the hash recorded for an entity row.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
