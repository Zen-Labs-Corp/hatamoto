package form

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// Service provides business logic for form management.
type Service struct {
	repo   Repository
	logger *slog.Logger
}

// NewService creates a new form service.
func NewService(repo Repository, logger *slog.Logger) *Service {
	return &Service{
		repo:   repo,
		logger: logger,
	}
}

// --- Form Operations ---

// CreateForm creates a new form.
func (s *Service) CreateForm(ctx context.Context, input *CreateFormInput) (*Form, error) {
	// Auto-generate slug if not provided
	if input.Slug == "" {
		input.Slug = generateSlug(input.Name)
	} else {
		input.Slug = normalizeSlug(input.Slug)
	}

	// Validate fields
	for i, field := range input.Fields {
		if !ValidFieldType(field.Type) {
			s.logger.Warn("invalid field type",
				slog.String("field_type", field.Type),
				slog.String("field_name", field.Name),
			)
			return nil, ErrInvalidFieldType
		}

		// Ensure field has an ID
		if field.ID == "" {
			input.Fields[i].ID = uuid.New().String()
		}

		// Ensure field has a name
		if field.Name == "" {
			input.Fields[i].Name = generateFieldName(field.Label)
		}

		// Set position if not set
		if field.Position == 0 {
			input.Fields[i].Position = i
		}
	}

	// Set default settings if not provided
	if input.Settings.SubmitButtonText == "" {
		input.Settings.SubmitButtonText = "Submit"
	}
	if input.Settings.SuccessMessage == "" {
		input.Settings.SuccessMessage = "Thank you for your submission!"
	}

	form, err := s.repo.CreateForm(ctx, input)
	if err != nil {
		s.logger.Error("failed to create form",
			slog.String("name", input.Name),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("created form",
		slog.String("form_id", form.ID.String()),
		slog.String("name", form.Name),
		slog.String("slug", form.Slug),
	)

	return form, nil
}

// GetForm retrieves a form by ID.
func (s *Service) GetForm(ctx context.Context, id uuid.UUID) (*Form, error) {
	return s.repo.GetFormByID(ctx, id)
}

// GetFormBySlug retrieves a form by slug.
func (s *Service) GetFormBySlug(ctx context.Context, slug string) (*Form, error) {
	return s.repo.GetFormBySlug(ctx, slug)
}

// ListForms retrieves all forms for the tenant.
func (s *Service) ListForms(ctx context.Context, status string) ([]*Form, error) {
	if status != "" && !ValidStatus(status) {
		return nil, ErrInvalidStatus
	}
	return s.repo.ListForms(ctx, status)
}

// UpdateForm modifies a form.
func (s *Service) UpdateForm(ctx context.Context, id uuid.UUID, input *UpdateFormInput) (*Form, error) {
	// Validate status if provided
	if input.Status != nil && !ValidStatus(*input.Status) {
		return nil, ErrInvalidStatus
	}

	// Validate fields if provided
	if input.Fields != nil {
		for i, field := range input.Fields {
			if !ValidFieldType(field.Type) {
				s.logger.Warn("invalid field type",
					slog.String("field_type", field.Type),
					slog.String("field_name", field.Name),
				)
				return nil, ErrInvalidFieldType
			}

			// Ensure field has an ID
			if field.ID == "" {
				input.Fields[i].ID = uuid.New().String()
			}

			// Ensure field has a name
			if field.Name == "" {
				input.Fields[i].Name = generateFieldName(field.Label)
			}
		}
	}

	form, err := s.repo.UpdateForm(ctx, id, input)
	if err != nil {
		s.logger.Error("failed to update form",
			slog.String("form_id", id.String()),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("updated form",
		slog.String("form_id", form.ID.String()),
		slog.String("name", form.Name),
	)

	return form, nil
}

// DeleteForm removes a form.
func (s *Service) DeleteForm(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteForm(ctx, id); err != nil {
		s.logger.Error("failed to delete form",
			slog.String("form_id", id.String()),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.logger.Info("deleted form",
		slog.String("form_id", id.String()),
	)

	return nil
}

// --- Submission Operations ---

// CreateSubmission creates a new form submission.
func (s *Service) CreateSubmission(ctx context.Context, formID uuid.UUID, input *CreateSubmissionInput) (*FormSubmission, error) {
	if input.Status != "" && !ValidSubmissionStatus(input.Status) {
		return nil, ErrInvalidStatus
	}

	// Verify form exists and is active
	form, err := s.repo.GetFormByID(ctx, formID)
	if err != nil {
		return nil, err
	}

	if form.Status != StatusActive {
		return nil, ErrFormNotFound // Treat inactive forms as not found for public
	}

	submission, err := s.repo.CreateSubmission(ctx, formID, input)
	if err != nil {
		s.logger.Error("failed to create submission",
			slog.String("form_id", formID.String()),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("created submission",
		slog.String("submission_id", submission.ID.String()),
		slog.String("form_id", formID.String()),
	)

	return submission, nil
}

// GetSubmission retrieves a submission by ID.
func (s *Service) GetSubmission(ctx context.Context, id uuid.UUID) (*FormSubmission, error) {
	return s.repo.GetSubmissionByID(ctx, id)
}

// ListSubmissions retrieves submissions for a form.
func (s *Service) ListSubmissions(ctx context.Context, formID uuid.UUID, params *ListSubmissionsParams) ([]*FormSubmission, int, error) {
	// Verify form exists
	if _, err := s.repo.GetFormByID(ctx, formID); err != nil {
		return nil, 0, err
	}

	// Validate status if provided
	if params != nil && params.Status != "" && !ValidSubmissionStatus(params.Status) {
		return nil, 0, ErrInvalidStatus
	}

	return s.repo.ListSubmissions(ctx, formID, params)
}

// UpdateSubmission modifies a submission.
func (s *Service) UpdateSubmission(ctx context.Context, id uuid.UUID, input *UpdateSubmissionInput) (*FormSubmission, error) {
	// Validate status if provided
	if input.Status != nil && !ValidSubmissionStatus(*input.Status) {
		return nil, ErrInvalidStatus
	}

	submission, err := s.repo.UpdateSubmission(ctx, id, input)
	if err != nil {
		s.logger.Error("failed to update submission",
			slog.String("submission_id", id.String()),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("updated submission",
		slog.String("submission_id", submission.ID.String()),
	)

	return submission, nil
}

// DeleteSubmission removes a submission.
func (s *Service) DeleteSubmission(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteSubmission(ctx, id); err != nil {
		s.logger.Error("failed to delete submission",
			slog.String("submission_id", id.String()),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.logger.Info("deleted submission",
		slog.String("submission_id", id.String()),
	)

	return nil
}

// GetSubmissionCount returns the count of submissions for a form.
func (s *Service) GetSubmissionCount(ctx context.Context, formID uuid.UUID) (int, error) {
	return s.repo.GetSubmissionCount(ctx, formID)
}

// --- Slug Generation ---

var (
	slugRegex    = regexp.MustCompile(`[^a-z0-9-]+`)
	multiDash    = regexp.MustCompile(`-+`)
	slugTrimDash = regexp.MustCompile(`^-+|-+$`)
)

// generateSlug creates a URL-friendly slug from a name.
func generateSlug(name string) string {
	// Normalize unicode characters
	slug := norm.NFKD.String(name)

	// Convert to lowercase
	slug = strings.ToLower(slug)

	// Remove accents/diacritics
	slug = removeAccents(slug)

	// Replace non-alphanumeric characters with dashes
	slug = slugRegex.ReplaceAllString(slug, "-")

	// Collapse multiple dashes
	slug = multiDash.ReplaceAllString(slug, "-")

	// Trim leading/trailing dashes
	slug = slugTrimDash.ReplaceAllString(slug, "")

	// Truncate to reasonable length
	if len(slug) > 100 {
		slug = slug[:100]
		if lastDash := strings.LastIndex(slug, "-"); lastDash > 50 {
			slug = slug[:lastDash]
		}
	}

	return slug
}

// normalizeSlug cleans up a user-provided slug.
func normalizeSlug(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	slug = slugRegex.ReplaceAllString(slug, "-")
	slug = multiDash.ReplaceAllString(slug, "-")
	slug = slugTrimDash.ReplaceAllString(slug, "")
	return slug
}

// removeAccents removes diacritics from a string.
func removeAccents(s string) string {
	var result strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue // Skip combining marks
		}
		result.WriteRune(r)
	}
	return result.String()
}

// generateFieldName creates a valid field name from a label.
func generateFieldName(label string) string {
	// Convert to snake_case
	name := strings.ToLower(strings.TrimSpace(label))
	name = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(name, "_")
	name = regexp.MustCompile(`_+`).ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")

	if name == "" {
		name = "field_" + uuid.New().String()[:8]
	}

	return name
}
