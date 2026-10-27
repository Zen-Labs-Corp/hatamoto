// Package sectiontemplate provides domain models and data operations for section templates.
// Section templates are user-saved section configurations that can be reused across pages.
package sectiontemplate

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Common errors.
var (
	ErrTemplateNotFound = errors.New("section template not found")
)

// SectionTemplate represents a saved section configuration.
type SectionTemplate struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	SectionType string          `json:"section_type"`
	Content     json.RawMessage `json:"content"`
	Tags        []string        `json:"tags"`
	UsageCount  int             `json:"usage_count"`
	CreatedBy   *uuid.UUID      `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CreateInput represents input for creating a new section template.
type CreateInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	SectionType string          `json:"section_type"`
	Content     json.RawMessage `json:"content"`
	Tags        []string        `json:"tags"`
}

// UpdateInput represents the editable parts of a section template: its
// labels. The saved section content is changed by saving the section again.
type UpdateInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// ListFilter represents filter options for listing section templates.
type ListFilter struct {
	SectionType string `json:"section_type,omitempty"`
	Search      string `json:"search,omitempty"`
}

// ListResult represents a paginated list of section templates.
type ListResult struct {
	Items []SectionTemplate `json:"items"`
	Total int               `json:"total"`
}
