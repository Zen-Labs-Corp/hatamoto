// Package media provides media file management functionality.
package media

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Media represents a media file stored in the system.
type Media struct {
	ID               uuid.UUID      `json:"id"`
	Filename         string         `json:"filename"`
	OriginalFilename string         `json:"original_filename"`
	MimeType         string         `json:"mime_type"`
	FileSize         int64          `json:"file_size"`
	StorageProvider  string         `json:"storage_provider"`
	StoragePath      string         `json:"storage_path"`
	PublicURL        *string        `json:"public_url,omitempty"`
	Width            *int           `json:"width,omitempty"`
	Height           *int           `json:"height,omitempty"`
	Variants         []ImageVariant `json:"variants,omitempty"`
	AltText          *string        `json:"alt_text,omitempty"`
	Caption          *string        `json:"caption,omitempty"`
	Folder           string         `json:"folder"`
	UploadedBy       *uuid.UUID     `json:"uploaded_by,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// ImageVariant represents a resized version of an image.
type ImageVariant struct {
	Size   string `json:"size"`
	Path   string `json:"path"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// VariantSizes defines the standard image variant sizes.
var VariantSizes = map[string]struct {
	MaxWidth  int
	MaxHeight int
	Crop      bool
}{
	"thumbnail": {MaxWidth: 150, MaxHeight: 150, Crop: true},
	"medium":    {MaxWidth: 300, MaxHeight: 300, Crop: false},
	"large":     {MaxWidth: 1024, MaxHeight: 1024, Crop: false},
}

// ListMediaFilter contains filter options for listing media.
type ListMediaFilter struct {
	Folder   string
	MimeType string
	Search   string
}

// CreateMediaInput contains the input for creating media.
type CreateMediaInput struct {
	Filename         string
	OriginalFilename string
	MimeType         string
	FileSize         int64
	StoragePath      string
	Width            *int
	Height           *int
	Folder           string
}

// UpdateMediaInput contains the input for updating media metadata.
type UpdateMediaInput struct {
	AltText *string `json:"alt_text,omitempty"`
	Caption *string `json:"caption,omitempty"`
	Folder  *string `json:"folder,omitempty"`
}

// PagedResult represents a paginated result set.
type PagedResult struct {
	Items []*Media `json:"items"`
	Total int      `json:"total"`
}

// MarshalVariants converts variants slice to JSON for database storage.
func (m *Media) MarshalVariants() ([]byte, error) {
	if m.Variants == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(m.Variants)
}

// UnmarshalVariants converts JSON from database to variants slice.
func (m *Media) UnmarshalVariants(data []byte) error {
	if len(data) == 0 {
		m.Variants = []ImageVariant{}
		return nil
	}
	return json.Unmarshal(data, &m.Variants)
}

// IsImage returns true if the media is an image based on mime type.
func (m *Media) IsImage() bool {
	switch m.MimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/svg+xml":
		return true
	default:
		return false
	}
}

// SupportsVariants returns true if the image format supports creating variants.
func (m *Media) SupportsVariants() bool {
	switch m.MimeType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}
