package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
)

// Repository defines the interface for media data operations.
type Repository interface {
	Create(ctx context.Context, m *Media) error
	GetByID(ctx context.Context, id uuid.UUID) (*Media, error)
	List(ctx context.Context, filter *ListMediaFilter, limit, offset int) (*PagedResult, error)
	Update(ctx context.Context, m *Media) error
	Delete(ctx context.Context, id uuid.UUID) error
	// UpdateFile stores what a Replace changed: type, size, dimensions and
	// variants, and moves updated_at. Everything else is left alone.
	UpdateFile(ctx context.Context, m *Media) error
	// ListImagesAfter returns up to limit JPEG, PNG and WebP records whose id
	// is greater than after, ordered by id. The rebuild job walks the library
	// with it.
	ListImagesAfter(ctx context.Context, after uuid.UUID, limit int) ([]*Media, error)
	// CountImages is the number of records ListImagesAfter walks.
	CountImages(ctx context.Context) (int, error)
	// Folders lists each folder in use with its number of files, ordered by
	// name. Folders have no table: a folder exists while a file is in it.
	Folders(ctx context.Context) ([]FolderCount, error)
}

// PostgresRepository implements Repository using PostgreSQL.
type PostgresRepository struct {
	db *database.DB
}

// NewPostgresRepository creates a new PostgreSQL media repository.
func NewPostgresRepository(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create inserts a new media record.
func (r *PostgresRepository) Create(ctx context.Context, m *Media) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	variants, err := m.MarshalVariants()
	if err != nil {
		return fmt.Errorf("failed to marshal variants: %w", err)
	}

	query := `
		INSERT INTO hatamoto_media (
			id, filename, original_filename, mime_type, file_size,
			storage_provider, storage_path, public_url, width, height, variants,
			alt_text, caption, folder, uploaded_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id, created_at, updated_at
	`

	err = conn.QueryRow(ctx, query,
		m.ID, m.Filename, m.OriginalFilename, m.MimeType, m.FileSize,
		m.StorageProvider, m.StoragePath, m.PublicURL, m.Width, m.Height, variants,
		m.AltText, m.Caption, m.Folder, m.UploadedBy, m.CreatedAt, m.UpdatedAt,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create media: %w", err)
	}

	return nil
}

// insertMediaTx inserts m inside tx with the given origin and no run.
func insertMediaTx(ctx context.Context, tx pgx.Tx, m *Media, origin string) error {
	variants, err := m.MarshalVariants()
	if err != nil {
		return fmt.Errorf("failed to marshal variants: %w", err)
	}
	if origin == "" {
		origin = "owner"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO hatamoto_media (
			id, filename, original_filename, mime_type, file_size,
			storage_provider, storage_path, public_url, width, height, variants,
			alt_text, caption, credit, folder, uploaded_by, created_at, updated_at, origin
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $19, $14, $15, $16, $17, $18)`,
		m.ID, m.Filename, m.OriginalFilename, m.MimeType, m.FileSize,
		m.StorageProvider, m.StoragePath, m.PublicURL, m.Width, m.Height, variants,
		m.AltText, m.Caption, m.Folder, m.UploadedBy, m.CreatedAt, m.UpdatedAt, origin, m.Credit)
	if err != nil {
		return fmt.Errorf("failed to create media: %w", err)
	}
	return nil
}

// GetByID retrieves a media record by ID.
func (r *PostgresRepository) GetByID(ctx context.Context, id uuid.UUID) (*Media, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, filename, original_filename, mime_type, file_size,
		       storage_provider, storage_path, public_url, width, height, variants,
		       alt_text, caption, credit, folder, uploaded_by, created_at, updated_at
		FROM hatamoto_media
		WHERE id = $1
	`

	var m Media
	var variants []byte

	err = conn.QueryRow(ctx, query, id).Scan(
		&m.ID, &m.Filename, &m.OriginalFilename, &m.MimeType, &m.FileSize,
		&m.StorageProvider, &m.StoragePath, &m.PublicURL, &m.Width, &m.Height, &variants,
		&m.AltText, &m.Caption, &m.Credit, &m.Folder, &m.UploadedBy, &m.CreatedAt, &m.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMediaNotFound
		}
		return nil, fmt.Errorf("failed to get media: %w", err)
	}

	if err := m.UnmarshalVariants(variants); err != nil {
		return nil, fmt.Errorf("failed to unmarshal variants: %w", err)
	}

	return &m, nil
}

// rebuildMimes are the image types that get resized copies.
const rebuildMimes = `mime_type IN ('image/jpeg', 'image/png', 'image/webp') AND run_id IS NULL`

// ListImagesAfter implements Repository.
func (r *PostgresRepository) ListImagesAfter(ctx context.Context, after uuid.UUID, limit int) ([]*Media, error) {
	return r.listAfter(ctx, rebuildMimes, after, limit)
}

// ListAllAfter returns up to limit records of every type whose id is greater
// than after, ordered by id. Walking with it visits each record exactly once,
// whatever is inserted or deleted meanwhile (`media migrate` does).
func (r *PostgresRepository) ListAllAfter(ctx context.Context, after uuid.UUID, limit int) ([]*Media, error) {
	return r.listAfter(ctx, "run_id IS NULL", after, limit)
}

func (r *PostgresRepository) listAfter(ctx context.Context, where string, after uuid.UUID, limit int) ([]*Media, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `
		SELECT id, filename, original_filename, mime_type, file_size,
		       storage_provider, storage_path, public_url, width, height, variants,
		       alt_text, caption, folder, uploaded_by, created_at, updated_at
		FROM hatamoto_media
		WHERE `+where+` AND id > $1
		ORDER BY id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	defer rows.Close()

	var items []*Media
	for rows.Next() {
		var m Media
		var variants []byte
		if err := rows.Scan(
			&m.ID, &m.Filename, &m.OriginalFilename, &m.MimeType, &m.FileSize,
			&m.StorageProvider, &m.StoragePath, &m.PublicURL, &m.Width, &m.Height, &variants,
			&m.AltText, &m.Caption, &m.Folder, &m.UploadedBy, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan media: %w", err)
		}
		if err := m.UnmarshalVariants(variants); err != nil {
			return nil, fmt.Errorf("failed to unmarshal variants: %w", err)
		}
		items = append(items, &m)
	}
	return items, rows.Err()
}

// CountImages implements Repository.
func (r *PostgresRepository) CountImages(ctx context.Context) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var n int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_media WHERE `+rebuildMimes).Scan(&n); err != nil {
		return 0, fmt.Errorf("failed to count images: %w", err)
	}
	return n, nil
}

// List retrieves media records with filtering and pagination.
func (r *PostgresRepository) List(ctx context.Context, filter *ListMediaFilter, limit, offset int) (*PagedResult, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Build query with filters
	baseQuery := `
		SELECT id, filename, original_filename, mime_type, file_size,
		       storage_provider, storage_path, public_url, width, height, variants,
		       alt_text, caption, credit, folder, uploaded_by, created_at, updated_at
		FROM hatamoto_media
		WHERE run_id IS NULL
	`
	countQuery := `SELECT COUNT(*) FROM hatamoto_media WHERE run_id IS NULL`

	args := []any{}
	argIdx := 1

	if filter != nil {
		if filter.Folder != "" {
			filterClause := fmt.Sprintf(" AND lower(ltrim(folder, '/')) = lower(ltrim($%d, '/'))", argIdx)
			baseQuery += filterClause
			countQuery += filterClause
			args = append(args, filter.Folder)
			argIdx++
		}
		if filter.MimeType != "" {
			filterClause := fmt.Sprintf(" AND mime_type LIKE $%d", argIdx)
			baseQuery += filterClause
			countQuery += filterClause
			args = append(args, filter.MimeType+"%")
			argIdx++
		}
		if filter.Search != "" {
			filterClause := fmt.Sprintf(" AND (filename ILIKE $%d OR original_filename ILIKE $%d OR alt_text ILIKE $%d)", argIdx, argIdx, argIdx)
			baseQuery += filterClause
			countQuery += filterClause
			args = append(args, "%"+filter.Search+"%")
			argIdx++
		}
	}

	// Get total count
	var total int
	err = conn.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to count media: %w", err)
	}

	// Apply ordering and pagination
	baseQuery += " ORDER BY created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := conn.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list media: %w", err)
	}
	defer rows.Close()

	var items []*Media
	for rows.Next() {
		var m Media
		var variants []byte

		if err := rows.Scan(
			&m.ID, &m.Filename, &m.OriginalFilename, &m.MimeType, &m.FileSize,
			&m.StorageProvider, &m.StoragePath, &m.PublicURL, &m.Width, &m.Height, &variants,
			&m.AltText, &m.Caption, &m.Credit, &m.Folder, &m.UploadedBy, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan media: %w", err)
		}

		if err := m.UnmarshalVariants(variants); err != nil {
			return nil, fmt.Errorf("failed to unmarshal variants: %w", err)
		}

		items = append(items, &m)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &PagedResult{
		Items: items,
		Total: total,
	}, nil
}

// Update modifies a media record.
func (r *PostgresRepository) Update(ctx context.Context, m *Media) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	variants, err := m.MarshalVariants()
	if err != nil {
		return fmt.Errorf("failed to marshal variants: %w", err)
	}

	m.UpdatedAt = time.Now()

	query := `
		UPDATE hatamoto_media
		SET filename = $2,
		    public_url = $3,
		    width = $4,
		    height = $5,
		    variants = $6,
		    alt_text = $7,
		    caption = $8,
		    folder = $9,
		    updated_at = $10,
		    credit = $11
		WHERE id = $1
		RETURNING updated_at
	`

	result, err := conn.Exec(ctx, query,
		m.ID, m.Filename, m.PublicURL, m.Width, m.Height, variants,
		m.AltText, m.Caption, m.Folder, m.UpdatedAt, m.Credit,
	)

	if err != nil {
		return fmt.Errorf("failed to update media: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrMediaNotFound
	}

	return nil
}

// UpdateFile implements Repository.
func (r *PostgresRepository) UpdateFile(ctx context.Context, m *Media) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	variants, err := m.MarshalVariants()
	if err != nil {
		return fmt.Errorf("failed to marshal variants: %w", err)
	}
	m.UpdatedAt = time.Now()
	result, err := conn.Exec(ctx, `
		UPDATE hatamoto_media
		SET mime_type = $2, file_size = $3, width = $4, height = $5, variants = $6, updated_at = $7
		WHERE id = $1`,
		m.ID, m.MimeType, m.FileSize, m.Width, m.Height, variants, m.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update media file: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrMediaNotFound
	}
	return nil
}

// Delete removes a media record.
func (r *PostgresRepository) Delete(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	result, err := conn.Exec(ctx, `DELETE FROM hatamoto_media WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete media: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrMediaNotFound
	}

	return nil
}

// Folders implements Repository.
func (r *PostgresRepository) Folders(ctx context.Context) ([]FolderCount, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT folder, COUNT(*) FROM hatamoto_media WHERE run_id IS NULL GROUP BY folder ORDER BY folder`)
	if err != nil {
		return nil, fmt.Errorf("failed to list folders: %w", err)
	}
	defer rows.Close()
	var out []FolderCount
	for rows.Next() {
		var f FolderCount
		if err := rows.Scan(&f.Name, &f.Count); err != nil {
			return nil, fmt.Errorf("failed to scan folder: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
