package staticexport

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/jsonx"
)

// Querier is the part of a pgx pool the stores need.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// SettingsStore reads and writes the single hatamoto_static_hosting_settings row.
type SettingsStore struct{ db Querier }

// NewSettingsStore returns a store over db (a *pgxpool.Pool fits).
func NewSettingsStore(db Querier) *SettingsStore { return &SettingsStore{db: db} }

// Get returns the saved settings; found is false when no row exists (the
// site is served).
func (s *SettingsStore) Get(ctx context.Context) (set Settings, found bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT mode, target, dir_path, service, endpoint, region, bucket, prefix, access_key,
		       secret_sealed, live_origin, updated_at,
		       sftp_host, sftp_port, sftp_user, sftp_auth, sftp_secret_sealed, sftp_dir, sftp_host_key, sftp_htaccess
		FROM hatamoto_static_hosting_settings WHERE id`).Scan(
		&set.Mode, &set.Target, &set.DirPath, &set.Service, &set.Endpoint, &set.Region, &set.Bucket,
		&set.Prefix, &set.AccessKey, &set.SecretSealed, &set.LiveOrigin, &set.UpdatedAt,
		&set.SFTPHost, &set.SFTPPort, &set.SFTPUser, &set.SFTPAuth, &set.SFTPSecretSealed, &set.SFTPDir,
		&set.SFTPHostKey, &set.SFTPHTAccess)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("load static hosting settings: %w", err)
	}
	return set, true, nil
}

// Save validates set and stores it. The caller seals the secret key into
// SecretSealed.
func (s *SettingsStore) Save(ctx context.Context, set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_static_hosting_settings
			(id, mode, target, dir_path, service, endpoint, region, bucket, prefix, access_key,
			 secret_sealed, live_origin, updated_at,
			 sftp_host, sftp_port, sftp_user, sftp_auth, sftp_secret_sealed, sftp_dir, sftp_host_key, sftp_htaccess)
		VALUES (TRUE, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now(),
			$12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (id) DO UPDATE SET mode = EXCLUDED.mode, target = EXCLUDED.target,
			dir_path = EXCLUDED.dir_path, service = EXCLUDED.service, endpoint = EXCLUDED.endpoint,
			region = EXCLUDED.region, bucket = EXCLUDED.bucket, prefix = EXCLUDED.prefix,
			access_key = EXCLUDED.access_key, secret_sealed = EXCLUDED.secret_sealed,
			live_origin = EXCLUDED.live_origin, updated_at = now(),
			sftp_host = EXCLUDED.sftp_host, sftp_port = EXCLUDED.sftp_port, sftp_user = EXCLUDED.sftp_user,
			sftp_auth = EXCLUDED.sftp_auth, sftp_secret_sealed = EXCLUDED.sftp_secret_sealed,
			sftp_dir = EXCLUDED.sftp_dir, sftp_host_key = EXCLUDED.sftp_host_key,
			sftp_htaccess = EXCLUDED.sftp_htaccess`,
		set.Mode, set.Target, set.DirPath, set.Service, set.Endpoint, set.Region, set.Bucket,
		set.Prefix, set.AccessKey, set.SecretSealed, set.LiveOrigin,
		set.SFTPHost, set.SFTPPort, set.SFTPUser, set.SFTPAuth, set.SFTPSecretSealed, set.SFTPDir,
		set.SFTPHostKey, set.SFTPHTAccess)
	if err != nil {
		return fmt.Errorf("save static hosting settings: %w", err)
	}
	return nil
}

// ErrBusy is returned by RunStore.Start while another export is running.
var ErrBusy = errors.New("another static export is running")

const (
	staleAfter     = "5 minutes"
	maxStoredError = 50
	maxErrorLen    = 500
)

// RunResult is what Finish records.
type RunResult struct {
	OK                                                                     bool
	Pages, Assets, Media, Redirects, Changed, Unchanged, Removed, Warnings int
	Bytes                                                                  int64
	Duration                                                               time.Duration
	Errors                                                                 []string
}

// Run is one recorded export.
type Run struct {
	ID         uuid.UUID
	Trigger    string
	Target     string
	Status     string
	Pages      int
	Assets     int
	Media      int
	Redirects  int
	Bytes      int64
	Changed    int
	Unchanged  int
	Removed    int
	Warnings   int
	Duration   time.Duration
	Errors     []string
	StartedAt  time.Time
	FinishedAt *time.Time
}

// RunStore records export runs. Only one run can be 'running'.
type RunStore struct{ db Querier }

// NewRunStore returns a store over db (a *pgxpool.Pool fits).
func NewRunStore(db Querier) *RunStore { return &RunStore{db: db} }

// Start opens a run. A run whose heartbeat is older than five minutes is
// taken as crashed and marked failed first. ErrBusy means another run is
// active; no row is written then.
func (s *RunStore) Start(ctx context.Context, trigger, target string) (uuid.UUID, error) {
	if err := s.ClearAbandoned(ctx); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `
		INSERT INTO hatamoto_static_export_runs (trigger, target) VALUES ($1, $2) RETURNING id`,
		trigger, target).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return uuid.Nil, ErrBusy
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("start static export run: %w", err)
	}
	return id, nil
}

// ClearAbandoned marks a running row whose heartbeat is older than five
// minutes as failed, so a crashed run stops looking live. Start does it too.
func (s *RunStore) ClearAbandoned(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE hatamoto_static_export_runs
		SET status = 'failed', errors = '["abandoned"]'::jsonb, finished_at = now()
		WHERE status = 'running' AND heartbeat_at < now() - interval '`+staleAfter+`'`); err != nil {
		return fmt.Errorf("clear abandoned static export runs: %w", err)
	}
	return nil
}

// Heartbeat tells other callers the run is alive.
func (s *RunStore) Heartbeat(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE hatamoto_static_export_runs SET heartbeat_at = now() WHERE id = $1`, id)
	return err
}

// truncateError caps e at maxErrorLen bytes without splitting a UTF-8 rune,
// which the JSON encoder would reject.
func truncateError(e string) string {
	if len(e) <= maxErrorLen {
		return e
	}
	n := maxErrorLen
	for n > 0 && !utf8.RuneStart(e[n]) {
		n--
	}
	return e[:n]
}

// Finish closes the run. Errors are capped at 50 entries of 500 characters.
func (s *RunStore) Finish(ctx context.Context, id uuid.UUID, r RunResult) error {
	errs := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		if len(errs) == maxStoredError {
			break
		}
		errs = append(errs, truncateError(e))
	}
	raw, err := jsonx.Marshal(errs)
	if err != nil {
		return err
	}
	status := "failed"
	if r.OK {
		status = "ok"
	}
	_, err = s.db.Exec(ctx, `
		UPDATE hatamoto_static_export_runs
		SET status = $2, pages = $3, assets = $4, media = $5, redirects = $6, bytes = $7,
		    changed = $8, unchanged = $9, removed = $10, warnings = $11, duration_ms = $12,
		    errors = $13::jsonb, finished_at = now(), heartbeat_at = now()
		WHERE id = $1`,
		id, status, r.Pages, r.Assets, r.Media, r.Redirects, r.Bytes, r.Changed, r.Unchanged,
		r.Removed, r.Warnings, r.Duration.Milliseconds(), string(raw))
	if err != nil {
		return fmt.Errorf("finish static export run: %w", err)
	}
	return nil
}

// PruneRuns deletes finished runs started before the given time. A running
// row is never deleted. It returns the number of rows removed.
func (s *RunStore) PruneRuns(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM hatamoto_static_export_runs WHERE status <> 'running' AND started_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune static export runs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// List returns the latest runs, newest first.
func (s *RunStore) List(ctx context.Context, limit int) ([]Run, error) {
	return s.list(ctx, "", limit)
}

// ListLive returns the latest runs that are not Test export runs, newest
// first, however many test runs are newer.
func (s *RunStore) ListLive(ctx context.Context, limit int) ([]Run, error) {
	return s.list(ctx, "WHERE trigger <> 'test'", limit)
}

func (s *RunStore) list(ctx context.Context, where string, limit int) ([]Run, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, trigger, target, status, pages, assets, media, redirects, bytes, changed,
		       unchanged, removed, warnings, duration_ms, errors, started_at, finished_at
		FROM hatamoto_static_export_runs `+where+` ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list static export runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var ms int64
		var raw []byte
		if err := rows.Scan(&r.ID, &r.Trigger, &r.Target, &r.Status, &r.Pages, &r.Assets, &r.Media,
			&r.Redirects, &r.Bytes, &r.Changed, &r.Unchanged, &r.Removed, &r.Warnings, &ms, &raw,
			&r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		r.Duration = time.Duration(ms) * time.Millisecond
		if err := jsonx.Unmarshal(raw, &r.Errors); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
