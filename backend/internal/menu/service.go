package menu

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// Service provides business logic for menu management.
type Service struct {
	repo   Repository
	logger *slog.Logger
	// changed runs after any successful write: menus are part of every
	// rendered page, whoever wrote them (handlers, admin, MCP, plugins).
	changed func(ctx context.Context)
}

// SetChangeObserver installs the callback run after a menu or menu item was
// written; nil disables it.
func (s *Service) SetChangeObserver(f func(ctx context.Context)) { s.changed = f }

func (s *Service) notifyChanged(ctx context.Context) {
	if s.changed != nil {
		s.changed(ctx)
	}
}

// NewService creates a new menu service.
func NewService(repo Repository, logger *slog.Logger) *Service {
	return &Service{
		repo:   repo,
		logger: logger,
	}
}

// --- Menu Operations ---

// CreateMenu creates a new menu.
func (s *Service) CreateMenu(ctx context.Context, input *CreateMenuInput) (*Menu, error) {
	// Validate location if provided
	if input.Location != nil && !ValidLocation(*input.Location) {
		return nil, ErrInvalidLocation
	}

	// Auto-generate slug if not provided
	if input.Slug == "" {
		input.Slug = generateSlug(input.Name)
	} else {
		input.Slug = normalizeSlug(input.Slug)
	}

	menu, err := s.repo.CreateMenu(ctx, input)
	if err != nil {
		s.logger.Error("failed to create menu",
			slog.String("name", input.Name),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("created menu",
		slog.String("menu_id", menu.ID.String()),
		slog.String("name", menu.Name),
		slog.String("slug", menu.Slug),
	)

	s.notifyChanged(ctx)
	return menu, nil
}

// GetMenu retrieves a menu by ID.
func (s *Service) GetMenu(ctx context.Context, id uuid.UUID) (*Menu, error) {
	return s.repo.GetMenuByID(ctx, id)
}

// GetMenuBySlug retrieves a menu by slug.
func (s *Service) GetMenuBySlug(ctx context.Context, slug string) (*Menu, error) {
	return s.repo.GetMenuBySlug(ctx, slug)
}

// GetMenuByLocation retrieves a menu by location.
func (s *Service) GetMenuByLocation(ctx context.Context, location string) (*Menu, error) {
	if !ValidLocation(location) {
		return nil, ErrInvalidLocation
	}
	return s.repo.GetMenuByLocation(ctx, location)
}

// ListMenus retrieves all menus for the tenant.
func (s *Service) ListMenus(ctx context.Context) ([]*Menu, error) {
	return s.repo.ListMenus(ctx)
}

// UpdateMenu modifies a menu.
func (s *Service) UpdateMenu(ctx context.Context, id uuid.UUID, input *UpdateMenuInput) (*Menu, error) {
	// Validate location if provided
	if input.Location != nil && !ValidLocation(*input.Location) {
		return nil, ErrInvalidLocation
	}

	menu, err := s.repo.UpdateMenu(ctx, id, input)
	if err != nil {
		s.logger.Error("failed to update menu",
			slog.String("menu_id", id.String()),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("updated menu",
		slog.String("menu_id", menu.ID.String()),
		slog.String("name", menu.Name),
	)

	s.notifyChanged(ctx)
	return menu, nil
}

// ReplaceMenu updates a menu's name/location and replaces its items in one
// transaction. A nil items list leaves the items alone.
func (s *Service) ReplaceMenu(ctx context.Context, id uuid.UUID, fields *UpdateMenuInput, items []ReplaceItem) (*Menu, error) {
	if fields != nil && fields.Location != nil && !ValidLocation(*fields.Location) {
		return nil, ErrInvalidLocation
	}
	for _, it := range items {
		if !ValidLinkType(it.LinkType) {
			return nil, ErrInvalidLinkType
		}
	}
	if err := s.repo.ReplaceMenu(ctx, id, fields, items); err != nil {
		return nil, err
	}
	s.notifyChanged(ctx)
	return s.repo.GetMenuWithItems(ctx, id)
}

// DeleteMenu removes a menu.
func (s *Service) DeleteMenu(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteMenu(ctx, id); err != nil {
		s.logger.Error("failed to delete menu",
			slog.String("menu_id", id.String()),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.logger.Info("deleted menu",
		slog.String("menu_id", id.String()),
	)

	s.notifyChanged(ctx)
	return nil
}

// GetMenuWithItems retrieves a menu with all nested items.
func (s *Service) GetMenuWithItems(ctx context.Context, menuID uuid.UUID) (*Menu, error) {
	return s.repo.GetMenuWithItems(ctx, menuID)
}

// --- Menu Item Operations ---

// CreateMenuItem adds an item to a menu.
func (s *Service) CreateMenuItem(ctx context.Context, menuID uuid.UUID, input *CreateMenuItemInput) (*MenuItem, error) {
	// Validate link type
	if !ValidLinkType(input.LinkType) {
		return nil, ErrInvalidLinkType
	}

	// Validate menu exists
	if _, err := s.repo.GetMenuByID(ctx, menuID); err != nil {
		return nil, err
	}

	// Validate parent exists if provided
	if input.ParentID != nil {
		parent, err := s.repo.GetMenuItemByID(ctx, *input.ParentID)
		if err != nil {
			return nil, ErrMenuItemNotFound
		}
		// Ensure parent belongs to the same menu
		if parent.MenuID != menuID {
			return nil, ErrMenuItemNotFound
		}
	}

	item, err := s.repo.CreateMenuItem(ctx, menuID, input)
	if err != nil {
		s.logger.Error("failed to create menu item",
			slog.String("menu_id", menuID.String()),
			slog.String("label", input.Label),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("created menu item",
		slog.String("item_id", item.ID.String()),
		slog.String("menu_id", menuID.String()),
		slog.String("label", item.Label),
	)

	s.notifyChanged(ctx)
	return item, nil
}

// GetMenuItem retrieves a menu item by ID.
func (s *Service) GetMenuItem(ctx context.Context, id uuid.UUID) (*MenuItem, error) {
	return s.repo.GetMenuItemByID(ctx, id)
}

// UpdateMenuItem modifies a menu item.
func (s *Service) UpdateMenuItem(ctx context.Context, id uuid.UUID, input *UpdateMenuItemInput) (*MenuItem, error) {
	// Validate link type if provided
	if input.LinkType != nil && !ValidLinkType(*input.LinkType) {
		return nil, ErrInvalidLinkType
	}

	// Validate parent doesn't create circular reference
	if input.ParentID != nil {
		current, err := s.repo.GetMenuItemByID(ctx, id)
		if err != nil {
			return nil, err
		}

		// Cannot be own parent
		if *input.ParentID == id {
			return nil, ErrCircularParent
		}

		// Check if new parent is a descendant (would create cycle)
		if err := s.checkCircularReference(ctx, id, *input.ParentID); err != nil {
			return nil, err
		}

		// Ensure parent belongs to same menu
		parent, err := s.repo.GetMenuItemByID(ctx, *input.ParentID)
		if err != nil {
			return nil, ErrMenuItemNotFound
		}
		if parent.MenuID != current.MenuID {
			return nil, ErrMenuItemNotFound
		}
	}

	item, err := s.repo.UpdateMenuItem(ctx, id, input)
	if err != nil {
		s.logger.Error("failed to update menu item",
			slog.String("item_id", id.String()),
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	s.logger.Info("updated menu item",
		slog.String("item_id", item.ID.String()),
		slog.String("label", item.Label),
	)

	s.notifyChanged(ctx)
	return item, nil
}

// DeleteMenuItem removes a menu item.
func (s *Service) DeleteMenuItem(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteMenuItem(ctx, id); err != nil {
		s.logger.Error("failed to delete menu item",
			slog.String("item_id", id.String()),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.logger.Info("deleted menu item",
		slog.String("item_id", id.String()),
	)

	s.notifyChanged(ctx)
	return nil
}

// ReorderItems updates positions and parent relationships for multiple items.
func (s *Service) ReorderItems(ctx context.Context, menuID uuid.UUID, items []ReorderInput) error {
	// Validate menu exists
	if _, err := s.repo.GetMenuByID(ctx, menuID); err != nil {
		return err
	}

	if err := s.repo.ReorderItems(ctx, menuID, items); err != nil {
		s.logger.Error("failed to reorder menu items",
			slog.String("menu_id", menuID.String()),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.logger.Info("reordered menu items",
		slog.String("menu_id", menuID.String()),
		slog.Int("count", len(items)),
	)

	s.notifyChanged(ctx)
	return nil
}

// checkCircularReference checks if setting parentID as parent would create a cycle.
func (s *Service) checkCircularReference(ctx context.Context, itemID, parentID uuid.UUID) error {
	visited := make(map[uuid.UUID]bool)
	current := parentID

	for {
		if visited[current] {
			return ErrCircularParent
		}
		if current == itemID {
			return ErrCircularParent
		}
		visited[current] = true

		item, err := s.repo.GetMenuItemByID(ctx, current)
		if err != nil {
			return nil // Item not found, no cycle possible
		}

		if item.ParentID == nil {
			return nil // Reached root, no cycle
		}
		current = *item.ParentID
	}
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
