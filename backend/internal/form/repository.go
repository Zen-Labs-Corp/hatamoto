package form

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
)

// Repository defines the interface for form data operations.
type Repository interface {
	// Form operations
	CreateForm(ctx context.Context, input *CreateFormInput) (*Form, error)
	GetFormByID(ctx context.Context, id uuid.UUID) (*Form, error)
	GetFormBySlug(ctx context.Context, slug string) (*Form, error)
	ListForms(ctx context.Context, status string) ([]*Form, error)
	UpdateForm(ctx context.Context, id uuid.UUID, input *UpdateFormInput) (*Form, error)
	// SaveDefinition replaces the definition when the form's updated_at still
	// equals base; otherwise it returns ErrVersionConflict.
	SaveDefinition(ctx context.Context, id uuid.UUID, base time.Time, input *SaveDefinitionInput) (*Form, error)
	DeleteForm(ctx context.Context, id uuid.UUID) error

	// Submission operations
	CreateSubmission(ctx context.Context, formID uuid.UUID, input *CreateSubmissionInput) (*FormSubmission, error)
	GetSubmissionByID(ctx context.Context, id uuid.UUID) (*FormSubmission, error)
	ListSubmissions(ctx context.Context, formID uuid.UUID, params *ListSubmissionsParams) ([]*FormSubmission, int, error)
	UpdateSubmission(ctx context.Context, id uuid.UUID, input *UpdateSubmissionInput) (*FormSubmission, error)
	DeleteSubmission(ctx context.Context, id uuid.UUID) error
	GetSubmissionCount(ctx context.Context, formID uuid.UUID) (int, error)
	CountRecentSubmissionsByIP(ctx context.Context, formID uuid.UUID, ip string, since time.Time) (int, error)
	PurgeExpiredSubmissions(ctx context.Context) (int64, error)
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

	fieldsJSON, err := jsonx.Marshal(input.Fields)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal fields: %w", err)
	}

	settingsJSON, err := jsonx.Marshal(input.Settings)
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

	if err := jsonx.Unmarshal(fieldsData, &f.Fields, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}

	if err := jsonx.Unmarshal(settingsData, &f.Settings, jsonx.StoredLoose); err != nil {
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

	if err := jsonx.Unmarshal(fieldsData, &f.Fields, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}

	if err := jsonx.Unmarshal(settingsData, &f.Settings, jsonx.StoredLoose); err != nil {
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

	if err := jsonx.Unmarshal(fieldsData, &f.Fields, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}

	if err := jsonx.Unmarshal(settingsData, &f.Settings, jsonx.StoredLoose); err != nil {
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

		if err := jsonx.Unmarshal(fieldsData, &f.Fields, jsonx.StoredLoose); err != nil {
			return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
		}

		if err := jsonx.Unmarshal(settingsData, &f.Settings, jsonx.StoredLoose); err != nil {
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

	fieldsJSON, err := jsonx.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal fields: %w", err)
	}

	settingsJSON, err := jsonx.Marshal(settings)
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

	if err := jsonx.Unmarshal(fieldsData, &f.Fields, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}

	if err := jsonx.Unmarshal(settingsData, &f.Settings, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	return &f, nil
}

// SaveDefinition replaces name, description, status, fields and settings in
// one statement guarded by the base version (updated_at).
func (r *PostgresRepository) SaveDefinition(ctx context.Context, id uuid.UUID, base time.Time, input *SaveDefinitionInput) (*Form, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	fields := input.Fields
	if fields == nil {
		fields = []FormField{}
	}
	fieldsJSON, err := jsonx.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal fields: %w", err)
	}
	settingsJSON, err := jsonx.Marshal(input.Settings)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal settings: %w", err)
	}

	var f Form
	var fieldsData, settingsData []byte
	err = conn.QueryRow(ctx, `
		UPDATE hatamoto_forms
		SET name = $3, description = $4, fields = $5, settings = $6, status = $7, updated_at = NOW()
		WHERE id = $1 AND updated_at = $2
		RETURNING id, name, slug, description, fields, settings, status, created_at, updated_at
	`, id, base, input.Name, input.Description, fieldsJSON, settingsJSON, input.Status).Scan(
		&f.ID, &f.Name, &f.Slug, &f.Description, &fieldsData, &settingsData, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if qerr := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_forms WHERE id = $1)`, id).Scan(&exists); qerr != nil {
			return nil, fmt.Errorf("failed to check form: %w", qerr)
		}
		if !exists {
			return nil, ErrFormNotFound
		}
		return nil, ErrVersionConflict
	}
	if err != nil {
		return nil, fmt.Errorf("failed to save form definition: %w", err)
	}
	if err := jsonx.Unmarshal(fieldsData, &f.Fields, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}
	if err := jsonx.Unmarshal(settingsData, &f.Settings, jsonx.StoredLoose); err != nil {
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

	dataJSON, err := jsonx.Marshal(input.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	metadataJSON, err := jsonx.Marshal(input.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO hatamoto_form_submissions (id, form_id, data, metadata, status, created_at,
			first_channel, first_source, first_medium, first_campaign, first_landing,
			last_channel, last_source, last_medium, last_campaign, last_landing)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, form_id, data, metadata, status, created_at, ` + attributionColumns + `
	`

	var s FormSubmission
	var dataData, metadataData []byte
	var attr attributionScan

	args := append([]any{id, formID, dataJSON, metadataJSON, initialStatus(input), now}, attributionArgs(input.Attribution)...)
	err = conn.QueryRow(ctx, query, args...).Scan(
		append([]any{&s.ID, &s.FormID, &dataData, &metadataData, &s.Status, &s.CreatedAt}, attr.dest()...)...)

	if err != nil {
		return nil, fmt.Errorf("failed to create submission: %w", err)
	}

	if err := jsonx.Unmarshal(dataData, &s.Data, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	if err := jsonx.Unmarshal(metadataData, &s.Metadata, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}
	s.Attribution = attr.attribution()

	return &s, nil
}

// attributionColumns lists the ten touch columns in attributionScan order.
const attributionColumns = `first_channel, first_source, first_medium, first_campaign, first_landing,
		last_channel, last_source, last_medium, last_campaign, last_landing`

// attributionArgs flattens a into the ten column values; empty values are NULL.
func attributionArgs(a *Attribution) []any {
	var first, last Touch
	if a != nil {
		if a.First != nil {
			first = *a.First
		}
		if a.Last != nil {
			last = *a.Last
		}
	}
	out := make([]any, 0, 10)
	for _, t := range []Touch{first, last} {
		for _, v := range []string{t.Channel, t.Source, t.Medium, t.Campaign, t.Landing} {
			if v == "" {
				out = append(out, nil)
			} else {
				out = append(out, v)
			}
		}
	}
	return out
}

// attributionScan receives the ten nullable touch columns of one row.
type attributionScan [10]*string

func (a *attributionScan) dest() []any {
	d := make([]any, len(a))
	for i := range a {
		d[i] = &a[i]
	}
	return d
}

// attribution returns nil when every column is NULL or empty.
func (a *attributionScan) attribution() *Attribution {
	touch := func(v []*string) *Touch {
		var t Touch
		for i, p := range []*string{&t.Channel, &t.Source, &t.Medium, &t.Campaign, &t.Landing} {
			if v[i] != nil {
				*p = *v[i]
			}
		}
		if t == (Touch{}) {
			return nil
		}
		return &t
	}
	first, last := touch(a[:5]), touch(a[5:])
	if first == nil && last == nil {
		return nil
	}
	return &Attribution{First: first, Last: last}
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
		SELECT s.id, s.form_id, s.data, s.metadata, s.status, s.created_at, f.name,
			s.first_channel, s.first_source, s.first_medium, s.first_campaign, s.first_landing,
			s.last_channel, s.last_source, s.last_medium, s.last_campaign, s.last_landing
		FROM hatamoto_form_submissions s
		JOIN hatamoto_forms f ON s.form_id = f.id
		WHERE s.id = $1
	`

	var s FormSubmission
	var dataData, metadataData []byte
	var attr attributionScan

	err = conn.QueryRow(ctx, query, id).Scan(
		append([]any{&s.ID, &s.FormID, &dataData, &metadataData, &s.Status, &s.CreatedAt, &s.FormName}, attr.dest()...)...,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubmissionNotFound
		}
		return nil, fmt.Errorf("failed to get submission: %w", err)
	}

	if err := jsonx.Unmarshal(dataData, &s.Data, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	if err := jsonx.Unmarshal(metadataData, &s.Metadata, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}
	s.Attribution = attr.attribution()

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
		SELECT s.id, s.form_id, s.data, s.metadata, s.status, s.created_at, f.name,
			s.first_channel, s.first_source, s.first_medium, s.first_campaign, s.first_landing,
			s.last_channel, s.last_source, s.last_medium, s.last_campaign, s.last_landing
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
		var attr attributionScan

		if err := rows.Scan(
			append([]any{&s.ID, &s.FormID, &dataData, &metadataData, &s.Status, &s.CreatedAt, &s.FormName}, attr.dest()...)...,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan submission: %w", err)
		}

		if err := jsonx.Unmarshal(dataData, &s.Data, jsonx.StoredLoose); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal data: %w", err)
		}

		if err := jsonx.Unmarshal(metadataData, &s.Metadata, jsonx.StoredLoose); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
		s.Attribution = attr.attribution()

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
		RETURNING id, form_id, data, metadata, status, created_at, ` + attributionColumns + `
	`

	var s FormSubmission
	var dataData, metadataData []byte
	var attr attributionScan

	err = conn.QueryRow(ctx, query, id, *input.Status).Scan(
		append([]any{&s.ID, &s.FormID, &dataData, &metadataData, &s.Status, &s.CreatedAt}, attr.dest()...)...,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubmissionNotFound
		}
		return nil, fmt.Errorf("failed to update submission: %w", err)
	}

	if err := jsonx.Unmarshal(dataData, &s.Data, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	if err := jsonx.Unmarshal(metadataData, &s.Metadata, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}
	s.Attribution = attr.attribution()

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

// CountRecentSubmissionsByIP counts a form's submissions from one address
// since the given time. An empty address counts nothing.
func (r *PostgresRepository) CountRecentSubmissionsByIP(ctx context.Context, formID uuid.UUID, ip string, since time.Time) (int, error) {
	if ip == "" {
		return 0, nil
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	var count int
	err = conn.QueryRow(ctx, `SELECT COUNT(*) FROM hatamoto_form_submissions
		WHERE form_id = $1 AND created_at >= $2 AND metadata->>'ip_address' = $3`, formID, since, ip).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count recent submissions: %w", err)
	}
	return count, nil
}

const purgeBatchSize = 1000

// PurgeExpiredSubmissions deletes, for every form with a retention of 30, 90 or
// 365 days, the submissions (spam included) older than that. Forms without a
// retention keep everything. Run it with a superadmin context.
func (r *PostgresRepository) PurgeExpiredSubmissions(ctx context.Context) (int64, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	// The regexp guards the cast: only the three allowed values are ever read as int.
	rows, err := conn.Query(ctx, `SELECT id, (settings->>'retention_days')::int FROM hatamoto_forms
		WHERE settings->>'retention_days' ~ '^(30|90|365)$'`)
	if err != nil {
		return 0, fmt.Errorf("failed to list forms with retention: %w", err)
	}
	type retained struct {
		id   uuid.UUID
		days int
	}
	var forms []retained
	for rows.Next() {
		var f retained
		if err := rows.Scan(&f.id, &f.days); err != nil {
			rows.Close()
			return 0, fmt.Errorf("failed to scan form retention: %w", err)
		}
		forms = append(forms, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("failed to list forms with retention: %w", err)
	}

	var total int64
	for _, f := range forms {
		for {
			res, err := conn.Exec(ctx, `DELETE FROM hatamoto_form_submissions WHERE ctid IN (
				SELECT ctid FROM hatamoto_form_submissions
				WHERE form_id = $1 AND created_at < now() - make_interval(days => $2)
				LIMIT $3)`, f.id, f.days, purgeBatchSize)
			if err != nil {
				return total, fmt.Errorf("failed to purge submissions: %w", err)
			}
			total += res.RowsAffected()
			if res.RowsAffected() < purgeBatchSize {
				break
			}
		}
	}
	return total, nil
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
