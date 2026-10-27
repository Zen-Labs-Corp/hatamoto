package sectiontemplate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
)

// Repository defines the interface for section template data operations.
type Repository interface {
	Create(ctx context.Context, userID *uuid.UUID, input *CreateInput) (*SectionTemplate, error)
	GetByID(ctx context.Context, id uuid.UUID) (*SectionTemplate, error)
	List(ctx context.Context, filter *ListFilter, limit, offset int) (*ListResult, error)
	Update(ctx context.Context, id uuid.UUID, input *UpdateInput) (*SectionTemplate, error)
	Delete(ctx context.Context, id uuid.UUID) error
	IncrementUsage(ctx context.Context, id uuid.UUID) error
}

// PostgresRepository implements Repository using PostgreSQL.
type PostgresRepository struct {
	db *database.DB
}

// NewPostgresRepository creates a new PostgreSQL section template repository.
func NewPostgresRepository(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create inserts a new section template.
func (r *PostgresRepository) Create(ctx context.Context, userID *uuid.UUID, input *CreateInput) (*SectionTemplate, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()
	now := time.Now()

	// Default empty tags to empty array
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}

	query := `
		INSERT INTO hatamoto_section_templates (
			id, name, description, section_type, content, tags, usage_count, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 0, $7, $8, $9)
		RETURNING id, name, description, section_type, content, tags, usage_count, created_by, created_at, updated_at
	`

	var t SectionTemplate
	err = conn.QueryRow(ctx, query,
		id, input.Name, input.Description, input.SectionType, input.Content, tags,
		userID, now, now,
	).Scan(
		&t.ID, &t.Name, &t.Description, &t.SectionType, &t.Content,
		&t.Tags, &t.UsageCount, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create section template: %w", err)
	}

	return &t, nil
}

// List retrieves section templates with optional filtering and offset pagination.
func (r *PostgresRepository) List(ctx context.Context, filter *ListFilter, limit, offset int) (*ListResult, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Build WHERE clause with filters
	whereClause := "WHERE 1=1"
	args := []any{}
	argIdx := 1

	if filter != nil {
		if filter.SectionType != "" {
			whereClause += fmt.Sprintf(" AND section_type = $%d", argIdx)
			args = append(args, filter.SectionType)
			argIdx++
		}
		if filter.Search != "" {
			whereClause += fmt.Sprintf(" AND (name ILIKE $%d OR description ILIKE $%d)", argIdx, argIdx)
			args = append(args, "%"+filter.Search+"%")
			argIdx++
		}
	}

	// Count total matching records
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM hatamoto_section_templates %s`, whereClause)
	var total int
	err = conn.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to count section templates: %w", err)
	}

	// Build main query with pagination
	dataQuery := fmt.Sprintf(`
		SELECT id, name, description, section_type, content, tags, usage_count, created_by, created_at, updated_at
		FROM hatamoto_section_templates
		%s
		ORDER BY usage_count DESC, created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := conn.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list section templates: %w", err)
	}
	defer rows.Close()

	var templates []SectionTemplate
	for rows.Next() {
		var t SectionTemplate
		if err := rows.Scan(
			&t.ID, &t.Name, &t.Description, &t.SectionType, &t.Content,
			&t.Tags, &t.UsageCount, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan section template: %w", err)
		}
		templates = append(templates, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Ensure Items is never nil (frontend expects [] not null)
	if templates == nil {
		templates = []SectionTemplate{}
	}

	return &ListResult{
		Items: templates,
		Total: total,
	}, nil
}

// Update changes a template's name, description and tags.
func (r *PostgresRepository) Update(ctx context.Context, id uuid.UUID, input *UpdateInput) (*SectionTemplate, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}
	var t SectionTemplate
	err = conn.QueryRow(ctx, `
		UPDATE hatamoto_section_templates SET name = $2, description = $3, tags = $4, updated_at = now()
		WHERE id = $1
		RETURNING id, name, description, section_type, content, tags, usage_count, created_by, created_at, updated_at`,
		id, input.Name, input.Description, tags,
	).Scan(&t.ID, &t.Name, &t.Description, &t.SectionType, &t.Content,
		&t.Tags, &t.UsageCount, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTemplateNotFound
		}
		return nil, fmt.Errorf("failed to update section template: %w", err)
	}
	return &t, nil
}

// Delete removes a section template by ID.
func (r *PostgresRepository) Delete(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	result, err := conn.Exec(ctx, `DELETE FROM hatamoto_section_templates WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete section template: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrTemplateNotFound
	}

	return nil
}

// IncrementUsage atomically increments the usage_count for a section template.
func (r *PostgresRepository) IncrementUsage(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	result, err := conn.Exec(ctx,
		`UPDATE hatamoto_section_templates SET usage_count = usage_count + 1 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to increment section template usage: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrTemplateNotFound
	}

	return nil
}

// GetByID retrieves a section template by ID. Used internally for existence checks.
func (r *PostgresRepository) GetByID(ctx context.Context, id uuid.UUID) (*SectionTemplate, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, description, section_type, content, tags, usage_count, created_by, created_at, updated_at
		FROM hatamoto_section_templates
		WHERE id = $1
	`

	var t SectionTemplate
	err = conn.QueryRow(ctx, query, id).Scan(
		&t.ID, &t.Name, &t.Description, &t.SectionType, &t.Content,
		&t.Tags, &t.UsageCount, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTemplateNotFound
		}
		return nil, fmt.Errorf("failed to get section template: %w", err)
	}

	return &t, nil
}
