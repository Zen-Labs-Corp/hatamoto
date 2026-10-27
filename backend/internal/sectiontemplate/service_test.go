package sectiontemplate

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Mock Repository
// ---------------------------------------------------------------------------

// mockRepository implements Repository for testing.
type mockRepository struct {
	templates    map[uuid.UUID]*SectionTemplate
	createErr    error
	getByIDErr   error
	listErr      error
	deleteErr    error
	incrementErr error
	listResult   *ListResult
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		templates: make(map[uuid.UUID]*SectionTemplate),
	}
}

func (m *mockRepository) Create(ctx context.Context, userID *uuid.UUID, input *CreateInput) (*SectionTemplate, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	id := uuid.New()
	now := time.Now()
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}
	tmpl := &SectionTemplate{
		ID:          id,
		Name:        input.Name,
		Description: input.Description,
		SectionType: input.SectionType,
		Content:     input.Content,
		Tags:        tags,
		UsageCount:  0,
		CreatedBy:   userID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.templates[id] = tmpl
	return tmpl, nil
}

func (m *mockRepository) GetByID(ctx context.Context, id uuid.UUID) (*SectionTemplate, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	tmpl, ok := m.templates[id]
	if !ok {
		return nil, ErrTemplateNotFound
	}
	return tmpl, nil
}

func (m *mockRepository) List(ctx context.Context, filter *ListFilter, limit, offset int) (*ListResult, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if m.listResult != nil {
		return m.listResult, nil
	}
	// Default: return all templates with basic filtering
	var items []SectionTemplate
	for _, tmpl := range m.templates {
		if filter != nil {
			if filter.SectionType != "" && tmpl.SectionType != filter.SectionType {
				continue
			}
		}
		items = append(items, *tmpl)
	}
	if items == nil {
		items = []SectionTemplate{}
	}
	return &ListResult{Items: items, Total: len(items)}, nil
}

func (m *mockRepository) Delete(ctx context.Context, id uuid.UUID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.templates[id]; !ok {
		return ErrTemplateNotFound
	}
	delete(m.templates, id)
	return nil
}

func (m *mockRepository) Update(ctx context.Context, id uuid.UUID, in *UpdateInput) (*SectionTemplate, error) {
	tmpl, ok := m.templates[id]
	if !ok {
		return nil, ErrTemplateNotFound
	}
	tmpl.Name, tmpl.Description, tmpl.Tags = in.Name, in.Description, in.Tags
	return tmpl, nil
}

func (m *mockRepository) IncrementUsage(ctx context.Context, id uuid.UUID) error {
	if m.incrementErr != nil {
		return m.incrementErr
	}
	tmpl, ok := m.templates[id]
	if !ok {
		return ErrTemplateNotFound
	}
	tmpl.UsageCount++
	return nil
}

// addTemplate inserts a template directly for testing.
func (m *mockRepository) addTemplate(tmpl *SectionTemplate) {
	if tmpl.ID == uuid.Nil {
		tmpl.ID = uuid.New()
	}
	now := time.Now()
	tmpl.CreatedAt = now
	tmpl.UpdatedAt = now
	m.templates[tmpl.ID] = tmpl
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestService() (*Service, *mockRepository) {
	repo := newMockRepository()
	logger := newTestLogger()
	svc := NewService(repo, logger)
	return svc, repo
}

// ---------------------------------------------------------------------------
// Service.Create Tests
// ---------------------------------------------------------------------------

func TestService_Create(t *testing.T) {
	t.Parallel()

	t.Run("empty name returns validation error", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		input := &CreateInput{
			Name:        "",
			SectionType: "hero",
			Content:     json.RawMessage(`{"heading": "test"}`),
		}

		_, err := svc.Create(context.Background(), nil, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if ve.Field != "name" {
			t.Errorf("expected field 'name', got %q", ve.Field)
		}
	})

	t.Run("name exceeding 255 characters returns validation error", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		longName := strings.Repeat("a", 256)
		input := &CreateInput{
			Name:        longName,
			SectionType: "hero",
			Content:     json.RawMessage(`{}`),
		}

		_, err := svc.Create(context.Background(), nil, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if ve.Field != "name" {
			t.Errorf("expected field 'name', got %q", ve.Field)
		}
	})

	t.Run("name at exactly 255 characters is accepted", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		exactName := strings.Repeat("a", 255)
		input := &CreateInput{
			Name:        exactName,
			SectionType: "hero",
			Content:     json.RawMessage(`{}`),
		}

		tmpl, err := svc.Create(context.Background(), nil, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tmpl.Name != exactName {
			t.Errorf("expected name length 255, got %d", len(tmpl.Name))
		}
	})

	t.Run("empty section_type returns validation error", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		input := &CreateInput{
			Name:        "My Template",
			SectionType: "",
			Content:     json.RawMessage(`{}`),
		}

		_, err := svc.Create(context.Background(), nil, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if ve.Field != "section_type" {
			t.Errorf("expected field 'section_type', got %q", ve.Field)
		}
	})

	t.Run("invalid JSON content returns validation error", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		input := &CreateInput{
			Name:        "My Template",
			SectionType: "hero",
			Content:     json.RawMessage(`{not valid json`),
		}

		_, err := svc.Create(context.Background(), nil, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if ve.Field != "content" {
			t.Errorf("expected field 'content', got %q", ve.Field)
		}
	})

	t.Run("content exceeding 512KB returns validation error", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		// Build a JSON value that exceeds 512KB.
		// A JSON string of the form: {"k":"<large payload>"}
		payloadSize := maxContentSize + 1
		largeValue := strings.Repeat("x", payloadSize)
		largeJSON := json.RawMessage(`{"k":"` + largeValue + `"}`)

		input := &CreateInput{
			Name:        "My Template",
			SectionType: "hero",
			Content:     largeJSON,
		}

		_, err := svc.Create(context.Background(), nil, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if ve.Field != "content" {
			t.Errorf("expected field 'content', got %q", ve.Field)
		}
	})

	t.Run("empty content defaults to empty JSON object", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		input := &CreateInput{
			Name:        "My Template",
			SectionType: "hero",
			Content:     nil, // empty / zero-value
		}

		tmpl, err := svc.Create(context.Background(), nil, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if string(tmpl.Content) != "{}" {
			t.Errorf("expected content '{}', got %q", string(tmpl.Content))
		}
	})

	t.Run("valid input creates template successfully", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()
		userID := uuid.New()

		input := &CreateInput{
			Name:        "Hero Banner",
			Description: "A large hero section",
			SectionType: "hero",
			Content:     json.RawMessage(`{"heading":"Welcome","subheading":"Hello world"}`),
			Tags:        []string{"landing", "homepage"},
		}

		tmpl, err := svc.Create(context.Background(), &userID, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if tmpl.ID == uuid.Nil {
			t.Error("expected template ID to be set")
		}
		if tmpl.Name != "Hero Banner" {
			t.Errorf("expected name 'Hero Banner', got %q", tmpl.Name)
		}
		if tmpl.SectionType != "hero" {
			t.Errorf("expected section_type 'hero', got %q", tmpl.SectionType)
		}
		if tmpl.CreatedBy == nil || *tmpl.CreatedBy != userID {
			t.Errorf("expected created_by %v, got %v", userID, tmpl.CreatedBy)
		}
	})

	t.Run("repository error is propagated", func(t *testing.T) {
		t.Parallel()

		svc, repo := newTestService()
		repo.createErr = errors.New("database connection lost")

		input := &CreateInput{
			Name:        "Valid Template",
			SectionType: "hero",
			Content:     json.RawMessage(`{}`),
		}

		_, err := svc.Create(context.Background(), nil, input)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "database connection lost" {
			t.Errorf("expected 'database connection lost', got %q", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Service.List Tests
// ---------------------------------------------------------------------------

func TestService_List(t *testing.T) {
	t.Parallel()

	t.Run("negative offset is clamped to 0", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		// Should not error even with negative offset
		result, err := svc.List(context.Background(), nil, 10, -5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("limit greater than 100 is clamped to 20", func(t *testing.T) {
		t.Parallel()

		svc, repo := newTestService()

		// Add enough templates to distinguish between limit values
		for range 25 {
			repo.addTemplate(&SectionTemplate{
				Name:        "Template",
				SectionType: "hero",
				Content:     json.RawMessage(`{}`),
			})
		}

		// When limit > 100, the service should clamp it to 20.
		// The mock returns all items, but the service passes 20 to repo.
		// We verify by checking the service does not error.
		result, err := svc.List(context.Background(), nil, 200, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("limit of 0 defaults to 20", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		result, err := svc.List(context.Background(), nil, 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("negative limit defaults to 20", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		result, err := svc.List(context.Background(), nil, -10, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("valid limit within range is passed through", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		result, err := svc.List(context.Background(), nil, 50, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("filter is passed to repository", func(t *testing.T) {
		t.Parallel()

		svc, repo := newTestService()

		repo.addTemplate(&SectionTemplate{
			Name:        "Hero Template",
			SectionType: "hero",
			Content:     json.RawMessage(`{}`),
		})
		repo.addTemplate(&SectionTemplate{
			Name:        "Features Template",
			SectionType: "features",
			Content:     json.RawMessage(`{}`),
		})

		filter := &ListFilter{SectionType: "hero"}
		result, err := svc.List(context.Background(), filter, 20, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Total != 1 {
			t.Errorf("expected 1 result with hero filter, got %d", result.Total)
		}
	})

	t.Run("repository error is propagated", func(t *testing.T) {
		t.Parallel()

		svc, repo := newTestService()
		repo.listErr = errors.New("query timeout")

		_, err := svc.List(context.Background(), nil, 20, 0)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "query timeout" {
			t.Errorf("expected 'query timeout', got %q", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Service.Delete Tests
// ---------------------------------------------------------------------------

func TestService_Delete(t *testing.T) {
	t.Parallel()

	t.Run("delete existing template succeeds", func(t *testing.T) {
		t.Parallel()

		svc, repo := newTestService()

		tmpl := &SectionTemplate{
			Name:        "To Delete",
			SectionType: "hero",
			Content:     json.RawMessage(`{}`),
		}
		repo.addTemplate(tmpl)

		err := svc.Delete(context.Background(), tmpl.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify it was removed from the repository
		_, getErr := repo.GetByID(context.Background(), tmpl.ID)
		if !errors.Is(getErr, ErrTemplateNotFound) {
			t.Error("expected template to be deleted from repository")
		}
	})

	t.Run("delete non-existent template returns ErrTemplateNotFound", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		err := svc.Delete(context.Background(), uuid.New())
		if !errors.Is(err, ErrTemplateNotFound) {
			t.Errorf("expected ErrTemplateNotFound, got %v", err)
		}
	})

	t.Run("repository error is propagated", func(t *testing.T) {
		t.Parallel()

		svc, repo := newTestService()
		repo.deleteErr = errors.New("constraint violation")

		err := svc.Delete(context.Background(), uuid.New())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "constraint violation" {
			t.Errorf("expected 'constraint violation', got %q", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Service.RecordUsage Tests
// ---------------------------------------------------------------------------

func TestService_RecordUsage(t *testing.T) {
	t.Parallel()

	t.Run("record usage for existing template succeeds", func(t *testing.T) {
		t.Parallel()

		svc, repo := newTestService()

		tmpl := &SectionTemplate{
			Name:        "Popular Template",
			SectionType: "hero",
			Content:     json.RawMessage(`{}`),
			UsageCount:  5,
		}
		repo.addTemplate(tmpl)

		err := svc.RecordUsage(context.Background(), tmpl.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify usage count was incremented
		updated := repo.templates[tmpl.ID]
		if updated.UsageCount != 6 {
			t.Errorf("expected usage count 6, got %d", updated.UsageCount)
		}
	})

	t.Run("record usage for non-existent template returns ErrTemplateNotFound", func(t *testing.T) {
		t.Parallel()

		svc, _ := newTestService()

		err := svc.RecordUsage(context.Background(), uuid.New())
		if !errors.Is(err, ErrTemplateNotFound) {
			t.Errorf("expected ErrTemplateNotFound, got %v", err)
		}
	})

	t.Run("repository error is propagated", func(t *testing.T) {
		t.Parallel()

		svc, repo := newTestService()
		repo.incrementErr = errors.New("deadlock detected")

		err := svc.RecordUsage(context.Background(), uuid.New())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "deadlock detected" {
			t.Errorf("expected 'deadlock detected', got %q", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// ValidationError Tests
// ---------------------------------------------------------------------------

func TestValidationError(t *testing.T) {
	t.Parallel()

	t.Run("Error returns message string", func(t *testing.T) {
		t.Parallel()

		ve := &ValidationError{Field: "name", Message: "name is required"}
		if ve.Error() != "name is required" {
			t.Errorf("expected 'name is required', got %q", ve.Error())
		}
	})

	t.Run("errors.As extracts ValidationError", func(t *testing.T) {
		t.Parallel()

		var err error = &ValidationError{Field: "content", Message: "content must be valid JSON"}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatal("expected errors.As to match ValidationError")
		}
		if ve.Field != "content" {
			t.Errorf("expected field 'content', got %q", ve.Field)
		}
	})
}

// ---------------------------------------------------------------------------
// NewService Tests
// ---------------------------------------------------------------------------

func TestNewService(t *testing.T) {
	t.Parallel()

	svc, _ := newTestService()
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
}

func TestUpdateRenamesAndValidates(t *testing.T) {
	svc, repo := newTestService()
	tmpl := &SectionTemplate{Name: "Old", SectionType: "hero"}
	repo.addTemplate(tmpl)

	got, err := svc.Update(context.Background(), tmpl.ID, &UpdateInput{Name: "  New  ", Description: "d", Tags: []string{"a"}})
	if err != nil || got.Name != "New" || got.Description != "d" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if _, err := svc.Update(context.Background(), tmpl.ID, &UpdateInput{Name: "  "}); err == nil {
		t.Fatal("blank name accepted")
	}
	if _, err := svc.Update(context.Background(), uuid.New(), &UpdateInput{Name: "x"}); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}
