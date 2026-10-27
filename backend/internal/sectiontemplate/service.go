package sectiontemplate

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxNameLength = 255
const maxContentSize = 512 * 1024 // 512 KB

// Service provides business logic for section template management.
type Service struct {
	repo   Repository
	logger *slog.Logger
}

// NewService creates a new section template service.
func NewService(repo Repository, logger *slog.Logger) *Service {
	return &Service{
		repo:   repo,
		logger: logger,
	}
}

// Create creates a new section template after validating required fields.
func (s *Service) Create(ctx context.Context, userID *uuid.UUID, input *CreateInput) (*SectionTemplate, error) {
	// Validate required fields
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}
	if utf8.RuneCountInString(input.Name) > maxNameLength {
		return nil, &ValidationError{Field: "name", Message: "name must be 255 characters or fewer"}
	}
	if input.SectionType == "" {
		return nil, &ValidationError{Field: "section_type", Message: "section_type is required"}
	}
	if len(input.Content) == 0 {
		input.Content = json.RawMessage(`{}`)
	}
	if !json.Valid(input.Content) {
		return nil, &ValidationError{Field: "content", Message: "content must be valid JSON"}
	}
	if len(input.Content) > maxContentSize {
		return nil, &ValidationError{Field: "content", Message: "content exceeds maximum size"}
	}

	tmpl, err := s.repo.Create(ctx, userID, input)
	if err != nil {
		s.logger.Error("failed to create section template",
			slog.String("name", input.Name),
			slog.String("section_type", input.SectionType),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("created section template",
		slog.String("template_id", tmpl.ID.String()),
		slog.String("name", tmpl.Name),
		slog.String("section_type", tmpl.SectionType),
	)

	return tmpl, nil
}

// List retrieves section templates with optional filtering and pagination.
func (s *Service) List(ctx context.Context, filter *ListFilter, limit, offset int) (*ListResult, error) {
	// Clamp pagination values
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	return s.repo.List(ctx, filter, limit, offset)
}

// Get returns one section template.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*SectionTemplate, error) {
	return s.repo.GetByID(ctx, id)
}

// Update renames a template and edits its description and tags.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input *UpdateInput) (*SectionTemplate, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}
	if utf8.RuneCountInString(input.Name) > maxNameLength {
		return nil, &ValidationError{Field: "name", Message: "name must be 255 characters or fewer"}
	}
	tmpl, err := s.repo.Update(ctx, id, input)
	if err != nil {
		if !errors.Is(err, ErrTemplateNotFound) {
			s.logger.Error("failed to update section template",
				slog.String("template_id", id.String()),
				slog.String("error", err.Error()),
			)
		}
		return nil, err
	}
	return tmpl, nil
}

// Delete removes a section template by ID.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		s.logger.Error("failed to delete section template",
			slog.String("template_id", id.String()),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.logger.Info("deleted section template",
		slog.String("template_id", id.String()),
	)

	return nil
}

// RecordUsage increments the usage count for a template.
func (s *Service) RecordUsage(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.IncrementUsage(ctx, id); err != nil {
		s.logger.Error("failed to record section template usage",
			slog.String("template_id", id.String()),
			slog.String("error", err.Error()),
		)
		return err
	}

	return nil
}

// ValidationError represents a field-level validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return e.Message
}
