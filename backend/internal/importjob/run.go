package importjob

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/wpimport"
)

// record is a row of hatamoto_import_runs.
type record struct {
	id        uuid.UUID
	sourceRef string
	status    string
	phase     string
	st        stored
	report    json.RawMessage
	errText   string
	started   time.Time
	finished  *time.Time
}

func (m *Manager) record(ctx context.Context, id uuid.UUID) (*record, error) {
	r := &record{id: id}
	var opts json.RawMessage
	err := m.cfg.Pool.QueryRow(ctx, `SELECT source_ref, status, phase, options, report, error, started_at, finished_at FROM hatamoto_import_runs WHERE id = $1 AND source = 'wordpress'`, id).
		Scan(&r.sourceRef, &r.status, &r.phase, &opts, &r.report, &r.errText, &r.started, &r.finished)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(opts, &r.st)
	if r.st.Kind == "" { // a run of the CLI
		r.st.Kind = KindREST
		if r.st.URL == "" {
			r.st.Kind = KindWXR
		}
	}
	return r, nil
}

func (r *record) hasReport() bool { return len(r.report) > 2 }

// effective is the status the wizard shows: a run that says it is running but
// has no job in this process was interrupted by a restart.
func (r *record) effective(active bool) string {
	if (r.status == StatusRunning || r.status == StatusScanning) && !active {
		return StatusInterrupted
	}
	return r.status
}

// Run is a run as the wizard shows it.
type Run struct {
	ID       uuid.UUID
	Status   string
	Phase    string
	Kind     string
	URL      string
	User     string
	File     string
	Options  Options
	Report   *wpimport.Report // the scan: set once the source was read
	Result   *wpimport.Result // set once an import ran (also a failed one)
	Error    string
	Started  time.Time
	Finished *time.Time
	// NeedsCredentials: resuming asks for the password or the file again.
	NeedsCredentials bool
	Progress         []string
	// PhaseIndex is how far the run is in Phases (1-based; 0 before the first).
	PhaseIndex int
	// EntityIDs maps a WordPress item id to the Hatamoto entity it became.
	EntityIDs map[string]uuid.UUID
	// Redirects are the redirects this run wrote or confirmed.
	Redirects []RedirectRow
	// StartedBy is the address of the person who started it.
	StartedBy string
}

// RedirectRow is one redirect of a finished run.
type RedirectRow struct {
	From, To string
	Code     int
}

// Get loads a run for display; the report, the id map and the redirects are
// included.
func (m *Manager) Get(ctx context.Context, id uuid.UUID) (*Run, error) {
	rec, err := m.record(ctx, id)
	if err != nil {
		return nil, err
	}
	j := m.job(id)
	run := m.view(rec, j)
	if rec.hasReport() {
		var res wpimport.Result
		if json.Unmarshal(rec.report, &res) == nil && res.RunID != uuid.Nil {
			run.Result, run.Report = &res, res.Report
		} else {
			var rep wpimport.Report
			if json.Unmarshal(rec.report, &rep) == nil && rep.Source != "" {
				run.Report = &rep
			}
		}
	}
	// The id map and the redirects are for the report page: a running or
	// scanning run (polled every few seconds) does not need them.
	if run.Result != nil && run.Status != StatusRunning && run.Status != StatusScanning {
		if run.EntityIDs, err = m.entityIDs(ctx, rec.sourceRef); err != nil {
			return nil, err
		}
		if run.Redirects, err = m.redirects(ctx, rec); err != nil {
			return nil, err
		}
	}
	switch run.Kind {
	case KindREST:
		_, has, _ := m.getSecret(ctx, id)
		run.NeedsCredentials = rec.st.User != "" && !has
	default:
		_, statErr := os.Stat(m.spoolPath(id))
		run.NeedsCredentials = statErr != nil
	}
	return run, nil
}

func (m *Manager) view(rec *record, j *job) *Run {
	run := &Run{
		ID: rec.id, Status: rec.effective(j != nil), Phase: rec.phase, Kind: rec.st.Kind, URL: rec.st.URL, User: rec.st.User,
		File: rec.st.File, Error: rec.errText, Started: rec.started, Finished: rec.finished, StartedBy: rec.st.StartedBy,
		Options: DefaultOptions(),
	}
	if rec.st.Options != nil {
		run.Options = *rec.st.Options
	}
	if j != nil {
		run.Progress = j.tail()
	}
	for i, p := range Phases {
		if p == rec.phase {
			run.PhaseIndex = i + 1
		}
	}
	return run
}

// Recent lists the latest runs (without their reports).
func (m *Manager) Recent(ctx context.Context, limit int) ([]Run, error) {
	rows, err := m.cfg.Pool.Query(ctx, `SELECT id FROM hatamoto_import_runs WHERE source = 'wordpress' ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []Run
	for _, id := range ids {
		rec, err := m.record(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *m.view(rec, m.job(id)))
	}
	return out, rows.Err()
}

func (m *Manager) entityIDs(ctx context.Context, ref string) (map[string]uuid.UUID, error) {
	rows, err := m.cfg.Pool.Query(ctx, `SELECT source_id, target_id FROM hatamoto_import_map WHERE source = $1 AND kind = $2`, ref, wpimport.KindEntity)
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

// maxRedirectRows bounds the list shown on the report page.
const maxRedirectRows = 1000

func (m *Manager) redirects(ctx context.Context, rec *record) ([]RedirectRow, error) {
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT r.source_path, r.target_url, r.status_code FROM hatamoto_import_map m
		JOIN hatamoto_redirects r ON r.id = m.target_id
		WHERE m.source = $1 AND m.kind = $2 AND m.run_id = $3 ORDER BY r.source_path LIMIT $4`,
		rec.sourceRef, wpimport.KindRedirect, rec.id, maxRedirectRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RedirectRow
	for rows.Next() {
		var r RedirectRow
		if err := rows.Scan(&r.From, &r.To, &r.Code); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
