// Package menu provides domain models and repository interfaces for menu management.
package menu

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Link types for menu items.
const (
	LinkTypeEntity   = "entity"
	LinkTypeExternal = "external"
	LinkTypeArchive  = "archive"
	LinkTypeCustom   = "custom"
	// LinkTypeNone is plain text: the item has no target and renders without a link.
	LinkTypeNone = "none"
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
	ErrMenuHasLocation   = errors.New("menu is assigned to a location: unassign it first")
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
	ID           uuid.UUID     `json:"id"`
	MenuID       uuid.UUID     `json:"menu_id"`
	ParentID     *uuid.UUID    `json:"parent_id,omitempty"`
	Label        string        `json:"label"`
	LinkType     string        `json:"link_type"` // entity, external, archive, custom, none
	EntityID     *uuid.UUID    `json:"entity_id,omitempty"`
	URL          *string       `json:"url,omitempty"`
	SchemaSlug   *string       `json:"schema_slug,omitempty"`
	Position     int           `json:"position"`
	OpenInNewTab bool          `json:"open_in_new_tab"`
	CSSClasses   *string       `json:"css_classes,omitempty"`
	Attrs        MenuItemAttrs `json:"attrs"`
	Children     []*MenuItem   `json:"children,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`

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
	// TakeLocation unassigns the menu that held Location. Only the admin and the
	// WordPress import set it; the plugin API and REST decode into this struct,
	// and `json:"-"` keeps them from asking for it, so there duplicates coexist.
	TakeLocation bool `json:"-"`
}

// UpdateMenuInput represents input for updating a menu.
type UpdateMenuInput struct {
	Name     *string `json:"name,omitempty"`
	Location *string `json:"location,omitempty"`
	// TakeLocation: see CreateMenuInput. It applies only when the location
	// changes; saving the current holder never unassigns another menu.
	TakeLocation bool `json:"-"`
}

// CreateMenuItemInput represents input for creating a menu item.
type CreateMenuItemInput struct {
	ParentID     *uuid.UUID     `json:"parent_id,omitempty"`
	Label        string         `json:"label"`
	LinkType     string         `json:"link_type"`
	EntityID     *uuid.UUID     `json:"entity_id,omitempty"`
	URL          *string        `json:"url,omitempty"`
	SchemaSlug   *string        `json:"schema_slug,omitempty"`
	Position     *int           `json:"position,omitempty"`
	OpenInNewTab bool           `json:"open_in_new_tab"`
	CSSClasses   *string        `json:"css_classes,omitempty"`
	Attrs        *MenuItemAttrs `json:"attrs,omitempty"`
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
	// Attrs replaces all of the item's settings when set.
	Attrs *MenuItemAttrs `json:"attrs,omitempty"`
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
	case LinkTypeEntity, LinkTypeExternal, LinkTypeArchive, LinkTypeCustom, LinkTypeNone:
		return true
	default:
		return false
	}
}

var locationRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidLocation checks the format of a menu location: empty (none) or a
// lowercase key. Which locations a site offers is decided by the caller
// (core locations plus the ones the active theme declares).
func ValidLocation(loc string) bool {
	return loc == "" || locationRe.MatchString(loc)
}

// IsCoreLocation reports whether loc is one of the locations every theme has.
func IsCoreLocation(loc string) bool {
	return loc == LocationHeader || loc == LocationFooter || loc == LocationSidebar
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
	CSSClasses   *string
	Attrs        MenuItemAttrs
	Parent       int
}
