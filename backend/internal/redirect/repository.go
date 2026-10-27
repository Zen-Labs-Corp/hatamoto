package redirect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/database"
)

// Repository handles database operations for redirects and 404 log.
//
// Path B PR 2b/7: tenant scoping has been dropped from every query. The
// hatamoto_redirects and hatamoto_404_log tables retain their nullable tenant_id
// column for PR 3 to drop; this code simply stops writing or filtering on
// it. Methods that previously took a tenantID now ignore it for API
// compatibility — call sites will be cleaned up wholesale in PR 3.
type Repository struct {
	db *database.DB
}

// NewRepository creates a new redirect repository.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

// ── 404 Log ──────────────────────────────────────────────────────────────────

// Upsert404Entry inserts or increments a 404 entry. The tenantID parameter
// is accepted for compatibility but no longer scopes the upsert — the
// (path) column carries a unique index post-Migration A.
func (r *Repository) Upsert404Entry(ctx context.Context, path, referrer, userAgent string) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `
		INSERT INTO hatamoto_404_log (path, last_referrer, last_user_agent)
		VALUES ($1, $2, $3)
		ON CONFLICT (path)
		DO UPDATE SET
			hit_count = hatamoto_404_log.hit_count + 1,
			last_referrer = EXCLUDED.last_referrer,
			last_user_agent = EXCLUDED.last_user_agent,
			last_seen_at = NOW()
	`

	var refPtr, uaPtr *string
	if referrer != "" {
		refPtr = &referrer
	}
	if userAgent != "" {
		uaPtr = &userAgent
	}

	_, err = conn.Exec(ctx, query, path, refPtr, uaPtr)
	if err != nil {
		return fmt.Errorf("failed to upsert 404 entry: %w", err)
	}
	return nil
}

// List404Entries returns paginated 404 entries sorted by hit_count DESC.
func (r *Repository) List404Entries(ctx context.Context, params NotFoundListParams) ([]*NotFoundEntry, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	// Build WHERE clause
	where := []string{}
	args := []any{}
	argIdx := 1

	if params.Search != "" {
		where = append(where, fmt.Sprintf("path ILIKE $%d", argIdx))
		args = append(args, "%"+params.Search+"%")
		argIdx++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Count
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM hatamoto_404_log %s", whereClause)
	var total int
	if err := conn.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count 404 entries: %w", err)
	}

	// Page defaults
	if params.PerPage <= 0 {
		params.PerPage = 50
	}
	if params.Page <= 0 {
		params.Page = 1
	}
	offset := (params.Page - 1) * params.PerPage

	query := fmt.Sprintf(`
		SELECT id, path, hit_count, last_referrer, last_user_agent,
		       first_seen_at, last_seen_at, ignored, redirect_id
		FROM hatamoto_404_log
		%s
		ORDER BY hit_count DESC, last_seen_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)
	args = append(args, params.PerPage, offset)

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query 404 entries: %w", err)
	}
	defer rows.Close()

	var entries []*NotFoundEntry
	for rows.Next() {
		var e NotFoundEntry
		if err := rows.Scan(
			&e.ID, &e.Path, &e.HitCount,
			&e.LastReferrer, &e.LastUserAgent,
			&e.FirstSeenAt, &e.LastSeenAt, &e.Ignored, &e.RedirectID,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan 404 entry: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, total, rows.Err()
}

// ListRecent404Entries returns up to limit unresolved entries (not ignored, not
// redirected) last seen at or after since, most hits first (lifetime), then most recently
// seen. It is read-only and bounded, for summaries that must not miss new 404s
// behind old high-hit rows.
func (r *Repository) ListRecent404Entries(ctx context.Context, since time.Time, limit int) ([]*NotFoundEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `
		SELECT id, path, hit_count, last_referrer, last_user_agent,
		       first_seen_at, last_seen_at, ignored, redirect_id
		FROM hatamoto_404_log
		WHERE NOT ignored AND redirect_id IS NULL AND last_seen_at >= $1
		ORDER BY hit_count DESC, last_seen_at DESC
		LIMIT $2
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent 404 entries: %w", err)
	}
	defer rows.Close()

	var entries []*NotFoundEntry
	for rows.Next() {
		var e NotFoundEntry
		if err := rows.Scan(
			&e.ID, &e.Path, &e.HitCount,
			&e.LastReferrer, &e.LastUserAgent,
			&e.FirstSeenAt, &e.LastSeenAt, &e.Ignored, &e.RedirectID,
		); err != nil {
			return nil, fmt.Errorf("failed to scan 404 entry: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// Ignore404Entry marks a 404 entry as ignored.
func (r *Repository) Ignore404Entry(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx, "UPDATE hatamoto_404_log SET ignored = TRUE WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to ignore 404 entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFoundEntryNotFound
	}
	return nil
}

// Delete404Entry deletes a 404 entry.
func (r *Repository) Delete404Entry(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx, "DELETE FROM hatamoto_404_log WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete 404 entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFoundEntryNotFound
	}
	return nil
}

// Link404ToRedirect sets the redirect_id on a 404 entry.
func (r *Repository) Link404ToRedirect(ctx context.Context, entryID, redirectID uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx,
		"UPDATE hatamoto_404_log SET redirect_id = $1 WHERE id = $2",
		redirectID, entryID,
	)
	return err
}

// ── Redirects ────────────────────────────────────────────────────────────────

// CreateRedirect inserts a new redirect rule. tenantID is accepted for API
// compatibility but no longer persisted — the column is nullable post-A.
func (r *Repository) CreateRedirect(ctx context.Context, input *CreateRedirectInput) (*Redirect, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `
		INSERT INTO hatamoto_redirects (source_path, target_url, status_code)
		VALUES ($1, $2, $3)
		RETURNING id, source_path, target_url, status_code, hit_count, is_active, created_at, updated_at
	`

	var rd Redirect
	err = conn.QueryRow(ctx, query, input.SourcePath, input.TargetURL, input.StatusCode).Scan(
		&rd.ID, &rd.SourcePath, &rd.TargetURL,
		&rd.StatusCode, &rd.HitCount, &rd.IsActive, &rd.CreatedAt, &rd.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateSourcePath
		}
		return nil, fmt.Errorf("failed to create redirect: %w", err)
	}
	return &rd, nil
}

// GetRedirectByID returns a redirect by ID.
func (r *Repository) GetRedirectByID(ctx context.Context, id uuid.UUID) (*Redirect, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `
		SELECT id, source_path, target_url, status_code, hit_count, is_active, created_at, updated_at
		FROM hatamoto_redirects WHERE id = $1
	`

	var rd Redirect
	err = conn.QueryRow(ctx, query, id).Scan(
		&rd.ID, &rd.SourcePath, &rd.TargetURL,
		&rd.StatusCode, &rd.HitCount, &rd.IsActive, &rd.CreatedAt, &rd.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRedirectNotFound
		}
		return nil, fmt.Errorf("failed to get redirect: %w", err)
	}
	return &rd, nil
}

// UpdateRedirect updates a redirect rule.
func (r *Repository) UpdateRedirect(ctx context.Context, id uuid.UUID, input *UpdateRedirectInput) (*Redirect, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	sets := []string{}
	args := []any{}
	argIdx := 1

	if input.SourcePath != nil {
		sets = append(sets, fmt.Sprintf("source_path = $%d", argIdx))
		args = append(args, *input.SourcePath)
		argIdx++
	}
	if input.TargetURL != nil {
		sets = append(sets, fmt.Sprintf("target_url = $%d", argIdx))
		args = append(args, *input.TargetURL)
		argIdx++
	}
	if input.StatusCode != nil {
		sets = append(sets, fmt.Sprintf("status_code = $%d", argIdx))
		args = append(args, *input.StatusCode)
		argIdx++
	}
	if input.IsActive != nil {
		sets = append(sets, fmt.Sprintf("is_active = $%d", argIdx))
		args = append(args, *input.IsActive)
		argIdx++
	}

	if len(sets) == 0 {
		return r.GetRedirectByID(ctx, id)
	}

	query := fmt.Sprintf(`
		UPDATE hatamoto_redirects SET %s
		WHERE id = $%d
		RETURNING id, source_path, target_url, status_code, hit_count, is_active, created_at, updated_at
	`, strings.Join(sets, ", "), argIdx)
	args = append(args, id)

	var rd Redirect
	err = conn.QueryRow(ctx, query, args...).Scan(
		&rd.ID, &rd.SourcePath, &rd.TargetURL,
		&rd.StatusCode, &rd.HitCount, &rd.IsActive, &rd.CreatedAt, &rd.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRedirectNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateSourcePath
		}
		return nil, fmt.Errorf("failed to update redirect: %w", err)
	}
	return &rd, nil
}

// DeleteRedirect deletes a redirect.
func (r *Repository) DeleteRedirect(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Exec(ctx, "DELETE FROM hatamoto_redirects WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete redirect: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRedirectNotFound
	}
	return nil
}

// ListRedirects returns paginated redirect rules.
func (r *Repository) ListRedirects(ctx context.Context, params RedirectListParams) ([]*Redirect, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	where := []string{}
	args := []any{}
	argIdx := 1

	if params.Search != "" {
		where = append(where, fmt.Sprintf("(source_path ILIKE $%d OR target_url ILIKE $%d)", argIdx, argIdx))
		args = append(args, "%"+params.Search+"%")
		argIdx++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM hatamoto_redirects %s", whereClause)
	var total int
	if err := conn.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count redirects: %w", err)
	}

	if params.PerPage <= 0 {
		params.PerPage = 50
	}
	if params.Page <= 0 {
		params.Page = 1
	}
	offset := (params.Page - 1) * params.PerPage

	query := fmt.Sprintf(`
		SELECT id, source_path, target_url, status_code, hit_count, is_active, created_at, updated_at
		FROM hatamoto_redirects
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)
	args = append(args, params.PerPage, offset)

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query redirects: %w", err)
	}
	defer rows.Close()

	var redirects []*Redirect
	for rows.Next() {
		var rd Redirect
		if err := rows.Scan(
			&rd.ID, &rd.SourcePath, &rd.TargetURL,
			&rd.StatusCode, &rd.HitCount, &rd.IsActive, &rd.CreatedAt, &rd.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan redirect: %w", err)
		}
		redirects = append(redirects, &rd)
	}
	return redirects, total, rows.Err()
}

// GetActiveRedirects returns all active redirects (used to populate the
// in-memory cache). The tenantID parameter is accepted for compatibility
// but ignored — there's a single tenant now and the cache spans all rows.
func (r *Repository) GetActiveRedirects(ctx context.Context) ([]*Redirect, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `
		SELECT id, source_path, target_url, status_code, hit_count, is_active, created_at, updated_at
		FROM hatamoto_redirects
		WHERE is_active = TRUE
		ORDER BY source_path
	`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query active redirects: %w", err)
	}
	defer rows.Close()

	var redirects []*Redirect
	for rows.Next() {
		var rd Redirect
		if err := rows.Scan(
			&rd.ID, &rd.SourcePath, &rd.TargetURL,
			&rd.StatusCode, &rd.HitCount, &rd.IsActive, &rd.CreatedAt, &rd.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan redirect: %w", err)
		}
		redirects = append(redirects, &rd)
	}
	return redirects, rows.Err()
}

// AddRedirectHits adds n to the hit count of a redirect.
func (r *Repository) AddRedirectHits(ctx context.Context, id uuid.UUID, n int) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "UPDATE hatamoto_redirects SET hit_count = hit_count + $2 WHERE id = $1", id, n)
	return err
}
