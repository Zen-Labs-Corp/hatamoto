package form

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
)

// Repository defines the interface for form data operations.
type Repository interface {
	// Form operations
	CreateForm(ctx context.Context, input *CreateFormInput) (*Form, error)
	GetFormByID(ctx context.Context, id uuid.UUID) (*Form, error)
	GetFormBySlug(ctx context.Context, slug string) (*Form, error)
	ListForms(ctx context.Context, status string) ([]*Form, error)
	UpdateForm(ctx context.Context, id uuid.UUID, input *UpdateFormInput) (*Form, error)
	DeleteForm(ctx context.Context, id uuid.UUID) error

	// Submission operations
	CreateSubmission(ctx context.Context, formID uuid.UUID, input *CreateSubmissionInput) (*FormSubmission, error)
	GetSubmissionByID(ctx context.Context, id uuid.UUID) (*FormSubmission, error)
	ListSubmissions(ctx context.Context, formID uuid.UUID, params *ListSubmissionsParams) ([]*FormSubmission, int, error)
	UpdateSubmission(ctx context.Context, id uuid.UUID, input *UpdateSubmissionInput) (*FormSubmission, error)
	DeleteSubmission(ctx context.Context, id uuid.UUID) error
	GetSubmissionCount(ctx context.Context, formID uuid.UUID) (int, error)
}

// PostgresRepository implements Repository using PostgreSQL.
type PostgresRepository struct {
	db *database.DB
}

// NewPostgresRepository creates a new PostgreSQL form repository.
func NewPostgresRepository(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// CreateForm inserts a new form.
func (r *PostgresRepository) CreateForm(ctx context.Context, input *CreateFormInput) (*Form, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()
	now := time.Now()

	fieldsJSON, err := json.Marshal(input.Fields)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal fields: %w", err)
	}

	settingsJSON, err := json.Marshal(input.Settings)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal settings: %w", err)
	}

	query := `
		INSERT INTO hatamoto_forms (id, name, slug, description, fields, settings, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, name, slug, description, fields, settings, status, created_at, updated_at
	`

	var f Form
	var fieldsData, settingsData []byte

	err = conn.QueryRow(ctx, query,
		id, input.Name, input.Slug, input.Description, fieldsJSON, settingsJSON, StatusActive, now, now,
	).Scan(&f.ID, &f.Name, &f.Slug, &f.Description, &fieldsData, &settingsData, &f.Status, &f.CreatedAt, &f.UpdatedAt)

	if err != nil {
		if isPgUniqueViolation(err) {
			return nil, ErrSlugAlreadyExists
		}
		return nil, fmt.Errorf("failed to create form: %w", err)
	}

	if err := json.Unmarshal(fieldsData, &f.Fields); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}

	if err := json.Unmarshal(settingsData, &f.Settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	return &f, nil
}

// GetFormByID retrieves a form by ID.
func (r *PostgresRepository) GetFormByID(ctx context.Context, id uuid.UUID) (*Form, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, description, fields, settings, status, created_at, updated_at
		FROM hatamoto_forms
		WHERE id = $1
	`

	var f Form
	var fieldsData, settingsData []byte

	err = conn.QueryRow(ctx, query, id).Scan(
		&f.ID, &f.Name, &f.Slug, &f.Description, &fieldsData, &settingsData, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFormNotFound
		}
		return nil, fmt.Errorf("failed to get form: %w", err)
	}

	if err := json.Unmarshal(fieldsData, &f.Fields); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}

	if err := json.Unmarshal(settingsData, &f.Settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	return &f, nil
}

// GetFormBySlug retrieves a form by slug.
func (r *PostgresRepository) GetFormBySlug(ctx context.Context, slug string) (*Form, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, description, fields, settings, status, created_at, updated_at
		FROM hatamoto_forms
		WHERE slug = $1
	`

	var f Form
	var fieldsData, settingsData []byte

	err = conn.QueryRow(ctx, query, slug).Scan(
		&f.ID, &f.Name, &f.Slug, &f.Description, &fieldsData, &settingsData, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFormNotFound
		}
		return nil, fmt.Errorf("failed to get form by slug: %w", err)
	}

	if err := json.Unmarshal(fieldsData, &f.Fields); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}

	if err := json.Unmarshal(settingsData, &f.Settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	return &f, nil
}

// ListForms retrieves all forms for the tenant.
func (r *PostgresRepository) ListForms(ctx context.Context, status string) ([]*Form, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, description, fields, settings, status, created_at, updated_at
		FROM hatamoto_forms
	`
	args := []any{}

	if status != "" {
		query += ` WHERE status = $1`
		args = append(args, status)
	}

	query += ` ORDER BY name ASC`

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list forms: %w", err)
	}
	defer rows.Close()

	var forms []*Form
	for rows.Next() {
		var f Form
		var fieldsData, settingsData []byte

		if err := rows.Scan(
			&f.ID, &f.Name, &f.Slug, &f.Description, &fieldsData, &settingsData, &f.Status, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan form: %w", err)
		}

		if err := json.Unmarshal(fieldsData, &f.Fields); err != nil {
			return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
		}

		if err := json.Unmarshal(settingsData, &f.Settings); err != nil {
			return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
		}

		forms = append(forms, &f)
	}

	return forms, rows.Err()
}

// UpdateForm modifies a form.
func (r *PostgresRepository) UpdateForm(ctx context.Context, id uuid.UUID, input *UpdateFormInput) (*Form, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Get current form to merge values
	current, err := r.GetFormByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Apply updates
	name := current.Name
	if input.Name != nil {
		name = *input.Name
	}

	description := current.Description
	if input.Description != nil {
		description = input.Description
	}

	fields := current.Fields
	if input.Fields != nil {
		fields = input.Fields
	}

	settings := current.Settings
	if input.Settings != nil {
		settings = *input.Settings
	}

	status := current.Status
	if input.Status != nil {
		status = *input.Status
	}

	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal fields: %w", err)
	}

	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal settings: %w", err)
	}

	query := `
		UPDATE hatamoto_forms
		SET name = $2, description = $3, fields = $4, settings = $5, status = $6, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, slug, description, fields, settings, status, created_at, updated_at
	`

	var f Form
	var fieldsData, settingsData []byte

	err = conn.QueryRow(ctx, query, id, name, description, fieldsJSON, settingsJSON, status).Scan(
		&f.ID, &f.Name, &f.Slug, &f.Description, &fieldsData, &settingsData, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFormNotFound
		}
		return nil, fmt.Errorf("failed to update form: %w", err)
	}

	if err := json.Unmarshal(fieldsData, &f.Fields); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}

	if err := json.Unmarshal(settingsData, &f.Settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	return &f, nil
}

// DeleteForm removes a form and all its submissions.
func (r *PostgresRepository) DeleteForm(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	result, err := conn.Exec(ctx, `DELETE FROM hatamoto_forms WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete form: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrFormNotFound
	}

	return nil
}

// CreateSubmission inserts a new form submission.
func (r *PostgresRepository) CreateSubmission(ctx context.Context, formID uuid.UUID, input *CreateSubmissionInput) (*FormSubmission, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()
	now := time.Now()

	dataJSON, err := json.Marshal(input.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	metadataJSON, err := json.Marshal(input.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO hatamoto_form_submissions (id, form_id, data, metadata, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, form_id, data, metadata, status, created_at
	`

	var s FormSubmission
	var dataData, metadataData []byte

	err = conn.QueryRow(ctx, query,
		id, formID, dataJSON, metadataJSON, initialStatus(input), now,
	).Scan(&s.ID, &s.FormID, &dataData, &metadataData, &s.Status, &s.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create submission: %w", err)
	}

	if err := json.Unmarshal(dataData, &s.Data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	if err := json.Unmarshal(metadataData, &s.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &s, nil
}

// initialStatus is the status a new submission is stored with.
func initialStatus(input *CreateSubmissionInput) string {
	if input.Status == "" {
		return SubmissionStatusNew
	}
	return input.Status
}

// GetSubmissionByID retrieves a submission by ID.
func (r *PostgresRepository) GetSubmissionByID(ctx context.Context, id uuid.UUID) (*FormSubmission, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT s.id, s.form_id, s.data, s.metadata, s.status, s.created_at, f.name
		FROM hatamoto_form_submissions s
		JOIN hatamoto_forms f ON s.form_id = f.id
		WHERE s.id = $1
	`

	var s FormSubmission
	var dataData, metadataData []byte

	err = conn.QueryRow(ctx, query, id).Scan(
		&s.ID, &s.FormID, &dataData, &metadataData, &s.Status, &s.CreatedAt, &s.FormName,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubmissionNotFound
		}
		return nil, fmt.Errorf("failed to get submission: %w", err)
	}

	if err := json.Unmarshal(dataData, &s.Data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	if err := json.Unmarshal(metadataData, &s.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &s, nil
}

// ListSubmissions retrieves submissions for a form with pagination.
func (r *PostgresRepository) ListSubmissions(ctx context.Context, formID uuid.UUID, params *ListSubmissionsParams) ([]*FormSubmission, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Release()

	// Build count query
	countQuery := `SELECT COUNT(*) FROM hatamoto_form_submissions WHERE form_id = $1`
	countArgs := []any{formID}

	if params != nil && params.Status != "" {
		countQuery += ` AND status = $2`
		countArgs = append(countArgs, params.Status)
	}

	var total int
	if params == nil || params.After == nil {
		if err := conn.QueryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("failed to count submissions: %w", err)
		}
	}

	// Build list query
	query := `
		SELECT s.id, s.form_id, s.data, s.metadata, s.status, s.created_at, f.name
		FROM hatamoto_form_submissions s
		JOIN hatamoto_forms f ON s.form_id = f.id
		WHERE s.form_id = $1
	`
	args := []any{formID}
	argIndex := 2

	if params != nil && params.Status != "" {
		query += fmt.Sprintf(` AND s.status = $%d`, argIndex)
		args = append(args, params.Status)
		argIndex++
	}

	if params != nil && params.After != nil {
		query += fmt.Sprintf(` AND (s.created_at, s.id) < ($%d, $%d)`, argIndex, argIndex+1)
		args = append(args, params.After.CreatedAt, params.After.ID)
		argIndex += 2
	}

	query += ` ORDER BY s.created_at DESC, s.id DESC`

	limit := 50
	offset := 0
	if params != nil {
		if params.Limit > 0 {
			limit = params.Limit
		}
		offset = params.Offset
		if params.After != nil {
			offset = 0
		}
	}

	query += fmt.Sprintf(` LIMIT $%d OFFSET $%d`, argIndex, argIndex+1)
	args = append(args, limit, offset)

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list submissions: %w", err)
	}
	defer rows.Close()

	var submissions []*FormSubmission
	for rows.Next() {
		var s FormSubmission
		var dataData, metadataData []byte

		if err := rows.Scan(
			&s.ID, &s.FormID, &dataData, &metadataData, &s.Status, &s.CreatedAt, &s.FormName,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan submission: %w", err)
		}

		if err := json.Unmarshal(dataData, &s.Data); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal data: %w", err)
		}

		if err := json.Unmarshal(metadataData, &s.Metadata); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}

		submissions = append(submissions, &s)
	}

	return submissions, total, rows.Err()
}

// UpdateSubmission modifies a submission.
func (r *PostgresRepository) UpdateSubmission(ctx context.Context, id uuid.UUID, input *UpdateSubmissionInput) (*FormSubmission, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if input.Status == nil {
		return r.GetSubmissionByID(ctx, id)
	}

	query := `
		UPDATE hatamoto_form_submissions
		SET status = $2
		WHERE id = $1
		RETURNING id, form_id, data, metadata, status, created_at
	`

	var s FormSubmission
	var dataData, metadataData []byte

	err = conn.QueryRow(ctx, query, id, *input.Status).Scan(
		&s.ID, &s.FormID, &dataData, &metadataData, &s.Status, &s.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubmissionNotFound
		}
		return nil, fmt.Errorf("failed to update submission: %w", err)
	}

	if err := json.Unmarshal(dataData, &s.Data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	if err := json.Unmarshal(metadataData, &s.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &s, nil
}

// DeleteSubmission removes a submission.
func (r *PostgresRepository) DeleteSubmission(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	result, err := conn.Exec(ctx, `DELETE FROM hatamoto_form_submissions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete submission: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrSubmissionNotFound
	}

	return nil
}

// GetSubmissionCount returns the count of submissions for a form.
func (r *PostgresRepository) GetSubmissionCount(ctx context.Context, formID uuid.UUID) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	var count int
	err = conn.QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_form_submissions WHERE form_id = $1`, formID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count submissions: %w", err)
	}

	return count, nil
}

// isPgUniqueViolation checks if an error is a PostgreSQL unique constraint violation.
func isPgUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return containsString(errStr, "duplicate key") || containsString(errStr, "23505")
}

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
