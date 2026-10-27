package importjob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Retention is how long the sealed password and the spooled export of a run
// that is not running are kept after the run last changed: long enough to
// come back to a scan or to resume a failed import, not forever.
const Retention = 7 * 24 * time.Hour

const sweepEvery = time.Hour

// activeIDs are the runs with a job in this process.
func (m *Manager) activeIDs() []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]uuid.UUID, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	return ids
}

// Sweep deletes the sealed passwords and the spooled export files of runs that
// were left for longer than Retention, and spool files that belong to no run.
// A run with a job is never touched. It reports how many credentials and files
// it removed.
func (m *Manager) Sweep(ctx context.Context) (secrets, files int, err error) {
	active := m.activeIDs()
	cutoff := time.Now().Add(-Retention)
	tag, err := m.cfg.Pool.Exec(ctx, `DELETE FROM hatamoto_import_secrets s USING hatamoto_import_runs r
		WHERE r.id = s.run_id AND r.id <> ALL($1) AND COALESCE(r.finished_at, r.started_at) < $2`, active, cutoff)
	if err != nil {
		return 0, 0, err
	}
	secrets = int(tag.RowsAffected())

	entries, err := os.ReadDir(m.cfg.SpoolDir)
	if err != nil {
		if os.IsNotExist(err) {
			return secrets, 0, nil
		}
		return secrets, 0, err
	}
	skip := map[uuid.UUID]bool{}
	for _, id := range active {
		skip[id] = true
	}
	for _, e := range entries {
		id, perr := uuid.Parse(strings.TrimSuffix(e.Name(), ".wxr"))
		if perr != nil || !strings.HasSuffix(e.Name(), ".wxr") || skip[id] {
			continue
		}
		var last *time.Time
		qerr := m.cfg.Pool.QueryRow(ctx, `SELECT COALESCE(finished_at, started_at) FROM hatamoto_import_runs WHERE id = $1`, id).Scan(&last)
		switch {
		case errors.Is(qerr, pgx.ErrNoRows): // no run: nothing will ever use it
		case qerr != nil:
			return secrets, files, qerr
		case last != nil && last.After(cutoff):
			continue
		}
		if os.Remove(filepath.Join(m.cfg.SpoolDir, e.Name())) == nil {
			files++
		}
	}
	return secrets, files, nil
}

// sweepLoop sweeps at start and then hourly until the manager closes.
func (m *Manager) sweepLoop() {
	defer m.wg.Done()
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		if s, f, err := m.Sweep(m.ctx); err != nil {
			if m.ctx.Err() == nil {
				m.cfg.Logger.Error("import retention sweep failed", "err", err)
			}
		} else if s+f > 0 {
			m.cfg.Logger.Info("import retention sweep", "credentials", s, "files", f)
		}
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}
	}
}
