// Package menu provides domain models and repository interfaces for menu management.
package menu

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Link types for menu items.
const (
	LinkTypeEntity   = "entity"
	LinkTypeExternal = "external"
	LinkTypeArchive  = "archive"
	LinkTypeCustom   = "custom"
)

// Menu locations.
const (
	LocationHeader  = "header"
	LocationFooter  = "footer"
	LocationSidebar = "sidebar"
)

// Common errors.
var (
	ErrMenuNotFound      = errors.New("menu not found")
	ErrMenuItemNotFound  = errors.New("menu item not found")
	ErrSlugAlreadyExists = errors.New("menu slug already exists")
	ErrInvalidLinkType   = errors.New("invalid link type")
	ErrInvalidLocation   = errors.New("invalid menu location")
	ErrEntityMissing     = errors.New("menu item links to an entity that does not exist")
	ErrCircularParent    = errors.New("circular parent reference")
)

// Menu represents a navigation menu.
type Menu struct {
	ID        uuid.UUID   `json:"id"`
	Name      string      `json:"name"`
	Slug      string      `json:"slug"`
	Location  *string     `json:"location,omitempty"`
	Items     []*MenuItem `json:"items,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// MenuItem represents a single item in a menu with optional nesting.
type MenuItem struct {
	ID           uuid.UUID   `json:"id"`
	MenuID       uuid.UUID   `json:"menu_id"`
	ParentID     *uuid.UUID  `json:"parent_id,omitempty"`
	Label        string      `json:"label"`
	LinkType     string      `json:"link_type"` // entity, external, archive, custom
	EntityID     *uuid.UUID  `json:"entity_id,omitempty"`
	URL          *string     `json:"url,omitempty"`
	SchemaSlug   *string     `json:"schema_slug,omitempty"`
	Position     int         `json:"position"`
	OpenInNewTab bool        `json:"open_in_new_tab"`
	CSSClasses   *string     `json:"css_classes,omitempty"`
	Children     []*MenuItem `json:"children,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`

	// Computed from entity (not stored in DB)
	EntitySlug *string `json:"entity_slug,omitempty"`
	EntityName *string `json:"entity_name,omitempty"`
	// EntitySchemaSlug is the linked entity's schema (render-side URL
	// building); hidden from JSON so the API shape is unchanged.
	EntitySchemaSlug *string `json:"-"`
}

// CreateMenuInput represents input for creating a new menu.
type CreateMenuInput struct {
	Name     string  `json:"name"`
	Slug     string  `json:"slug,omitempty"`
	Location *string `json:"location,omitempty"`
}

// UpdateMenuInput represents input for updating a menu.
type UpdateMenuInput struct {
	Name     *string `json:"name,omitempty"`
	Location *string `json:"location,omitempty"`
}

// CreateMenuItemInput represents input for creating a menu item.
type CreateMenuItemInput struct {
	ParentID     *uuid.UUID `json:"parent_id,omitempty"`
	Label        string     `json:"label"`
	LinkType     string     `json:"link_type"`
	EntityID     *uuid.UUID `json:"entity_id,omitempty"`
	URL          *string    `json:"url,omitempty"`
	SchemaSlug   *string    `json:"schema_slug,omitempty"`
	Position     *int       `json:"position,omitempty"`
	OpenInNewTab bool       `json:"open_in_new_tab"`
	CSSClasses   *string    `json:"css_classes,omitempty"`
}

// UpdateMenuItemInput represents input for updating a menu item.
type UpdateMenuItemInput struct {
	ParentID     *uuid.UUID `json:"parent_id,omitempty"`
	Label        *string    `json:"label,omitempty"`
	LinkType     *string    `json:"link_type,omitempty"`
	EntityID     *uuid.UUID `json:"entity_id,omitempty"`
	URL          *string    `json:"url,omitempty"`
	SchemaSlug   *string    `json:"schema_slug,omitempty"`
	Position     *int       `json:"position,omitempty"`
	OpenInNewTab *bool      `json:"open_in_new_tab,omitempty"`
	CSSClasses   *string    `json:"css_classes,omitempty"`
}

// ReorderInput represents a single item in a reorder request.
type ReorderInput struct {
	ID       uuid.UUID  `json:"id"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
	Position int        `json:"position"`
}

// ValidLinkType checks if a link type is valid.
func ValidLinkType(lt string) bool {
	switch lt {
	case LinkTypeEntity, LinkTypeExternal, LinkTypeArchive, LinkTypeCustom:
		return true
	default:
		return false
	}
}

// ValidLocation checks if a menu location is valid.
func ValidLocation(loc string) bool {
	if loc == "" {
		return true // Location is optional
	}
	switch loc {
	case LocationHeader, LocationFooter, LocationSidebar:
		return true
	default:
		return false
	}
}

// ReplaceItem is one item of a ReplaceMenu call. Parent is the index of an
// earlier item in the same list, or -1 for the top level.
type ReplaceItem struct {
	Label        string
	LinkType     string
	EntityID     *uuid.UUID
	URL          *string
	SchemaSlug   *string
	OpenInNewTab bool
	Parent       int
}
