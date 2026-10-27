package menu

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/database"
)

// Repository defines the interface for menu data operations.
type Repository interface {
	// Menu operations
	CreateMenu(ctx context.Context, input *CreateMenuInput) (*Menu, error)
	GetMenuByID(ctx context.Context, id uuid.UUID) (*Menu, error)
	GetMenuBySlug(ctx context.Context, slug string) (*Menu, error)
	GetMenuByLocation(ctx context.Context, location string) (*Menu, error)
	ListMenus(ctx context.Context) ([]*Menu, error)
	UpdateMenu(ctx context.Context, id uuid.UUID, input *UpdateMenuInput) (*Menu, error)
	DeleteMenu(ctx context.Context, id uuid.UUID) error

	// Menu item operations
	CreateMenuItem(ctx context.Context, menuID uuid.UUID, input *CreateMenuItemInput) (*MenuItem, error)
	GetMenuItemByID(ctx context.Context, id uuid.UUID) (*MenuItem, error)
	UpdateMenuItem(ctx context.Context, id uuid.UUID, input *UpdateMenuItemInput) (*MenuItem, error)
	DeleteMenuItem(ctx context.Context, id uuid.UUID) error

	// Nested operations
	GetMenuWithItems(ctx context.Context, menuID uuid.UUID) (*Menu, error)
	ReorderItems(ctx context.Context, menuID uuid.UUID, items []ReorderInput) error

	// ReplaceMenu applies the menu fields (when set) and replaces every item
	// in one transaction: either all of it happens or none of it.
	ReplaceMenu(ctx context.Context, id uuid.UUID, fields *UpdateMenuInput, items []ReplaceItem) error
}

// PostgresRepository implements Repository using PostgreSQL.
type PostgresRepository struct {
	db *database.DB
}

// NewPostgresRepository creates a new PostgreSQL menu repository.
func NewPostgresRepository(db *database.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// CreateMenu inserts a new menu.
func (r *PostgresRepository) CreateMenu(ctx context.Context, input *CreateMenuInput) (*Menu, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO hatamoto_menus (id, name, slug, location, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, slug, location, created_at, updated_at
	`

	var m Menu
	err = conn.QueryRow(ctx, query,
		id, input.Name, input.Slug, input.Location, now, now,
	).Scan(&m.ID, &m.Name, &m.Slug, &m.Location, &m.CreatedAt, &m.UpdatedAt)

	if err != nil {
		if isPgUniqueViolation(err) {
			return nil, ErrSlugAlreadyExists
		}
		return nil, fmt.Errorf("failed to create menu: %w", err)
	}

	return &m, nil
}

// GetMenuByID retrieves a menu by ID.
func (r *PostgresRepository) GetMenuByID(ctx context.Context, id uuid.UUID) (*Menu, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, location, created_at, updated_at
		FROM hatamoto_menus
		WHERE id = $1
	`

	var m Menu
	err = conn.QueryRow(ctx, query, id).Scan(
		&m.ID, &m.Name, &m.Slug, &m.Location, &m.CreatedAt, &m.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMenuNotFound
		}
		return nil, fmt.Errorf("failed to get menu: %w", err)
	}

	return &m, nil
}

// GetMenuBySlug retrieves a menu by slug.
func (r *PostgresRepository) GetMenuBySlug(ctx context.Context, slug string) (*Menu, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, location, created_at, updated_at
		FROM hatamoto_menus
		WHERE slug = $1
	`

	var m Menu
	err = conn.QueryRow(ctx, query, slug).Scan(
		&m.ID, &m.Name, &m.Slug, &m.Location, &m.CreatedAt, &m.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMenuNotFound
		}
		return nil, fmt.Errorf("failed to get menu by slug: %w", err)
	}

	return &m, nil
}

// GetMenuByLocation retrieves a menu by location.
func (r *PostgresRepository) GetMenuByLocation(ctx context.Context, location string) (*Menu, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, location, created_at, updated_at
		FROM hatamoto_menus
		WHERE location = $1
		LIMIT 1
	`

	var m Menu
	err = conn.QueryRow(ctx, query, location).Scan(
		&m.ID, &m.Name, &m.Slug, &m.Location, &m.CreatedAt, &m.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMenuNotFound
		}
		return nil, fmt.Errorf("failed to get menu by location: %w", err)
	}

	return &m, nil
}

// ListMenus retrieves all menus for the tenant.
func (r *PostgresRepository) ListMenus(ctx context.Context) ([]*Menu, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, name, slug, location, created_at, updated_at
		FROM hatamoto_menus
		ORDER BY name ASC
	`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list menus: %w", err)
	}
	defer rows.Close()

	var menus []*Menu
	for rows.Next() {
		var m Menu
		if err := rows.Scan(
			&m.ID, &m.Name, &m.Slug, &m.Location, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan menu: %w", err)
		}
		menus = append(menus, &m)
	}

	return menus, rows.Err()
}

// UpdateMenu modifies a menu.
func (r *PostgresRepository) UpdateMenu(ctx context.Context, id uuid.UUID, input *UpdateMenuInput) (*Menu, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		UPDATE hatamoto_menus
		SET name = COALESCE($2, name),
		    location = COALESCE($3, location),
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, slug, location, created_at, updated_at
	`

	var m Menu
	err = conn.QueryRow(ctx, query, id, input.Name, input.Location).Scan(
		&m.ID, &m.Name, &m.Slug, &m.Location, &m.CreatedAt, &m.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMenuNotFound
		}
		return nil, fmt.Errorf("failed to update menu: %w", err)
	}

	return &m, nil
}

// DeleteMenu removes a menu and all its items.
func (r *PostgresRepository) DeleteMenu(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	result, err := conn.Exec(ctx, `DELETE FROM hatamoto_menus WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete menu: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrMenuNotFound
	}

	return nil
}

// CreateMenuItem inserts a new menu item.
func (r *PostgresRepository) CreateMenuItem(ctx context.Context, menuID uuid.UUID, input *CreateMenuItemInput) (*MenuItem, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	id := uuid.New()
	now := time.Now()

	// Calculate position if not provided
	position := 0
	if input.Position != nil {
		position = *input.Position
	} else {
		// Get max position for the parent
		var maxPos *int
		posQuery := `SELECT MAX(position) FROM hatamoto_menu_items WHERE menu_id = $1 AND parent_id IS NOT DISTINCT FROM $2`
		if err := conn.QueryRow(ctx, posQuery, menuID, input.ParentID).Scan(&maxPos); err != nil {
			return nil, fmt.Errorf("read the last menu position: %w", err)
		}
		if maxPos != nil {
			position = *maxPos + 1
		}
	}

	query := `
		INSERT INTO hatamoto_menu_items (
			id, menu_id, parent_id, label, link_type, entity_id, url,
			schema_slug, position, open_in_new_tab, css_classes, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, menu_id, parent_id, label, link_type, entity_id, url,
		          schema_slug, position, open_in_new_tab, css_classes, created_at, updated_at
	`

	var item MenuItem
	err = conn.QueryRow(ctx, query,
		id, menuID, input.ParentID, input.Label, input.LinkType,
		input.EntityID, input.URL, input.SchemaSlug, position, input.OpenInNewTab,
		input.CSSClasses, now, now,
	).Scan(
		&item.ID, &item.MenuID, &item.ParentID, &item.Label, &item.LinkType,
		&item.EntityID, &item.URL, &item.SchemaSlug, &item.Position, &item.OpenInNewTab,
		&item.CSSClasses, &item.CreatedAt, &item.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create menu item: %w", err)
	}

	// Fetch entity info if linked
	if item.EntityID != nil {
		r.populateEntityInfo(ctx, conn, &item)
	}

	return &item, nil
}

// GetMenuItemByID retrieves a menu item by ID.
func (r *PostgresRepository) GetMenuItemByID(ctx context.Context, id uuid.UUID) (*MenuItem, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		SELECT id, menu_id, parent_id, label, link_type, entity_id, url,
		       schema_slug, position, open_in_new_tab, css_classes, created_at, updated_at
		FROM hatamoto_menu_items
		WHERE id = $1
	`

	var item MenuItem
	err = conn.QueryRow(ctx, query, id).Scan(
		&item.ID, &item.MenuID, &item.ParentID, &item.Label, &item.LinkType,
		&item.EntityID, &item.URL, &item.SchemaSlug, &item.Position, &item.OpenInNewTab,
		&item.CSSClasses, &item.CreatedAt, &item.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMenuItemNotFound
		}
		return nil, fmt.Errorf("failed to get menu item: %w", err)
	}

	if item.EntityID != nil {
		r.populateEntityInfo(ctx, conn, &item)
	}

	return &item, nil
}

// UpdateMenuItem modifies a menu item.
func (r *PostgresRepository) UpdateMenuItem(ctx context.Context, id uuid.UUID, input *UpdateMenuItemInput) (*MenuItem, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	query := `
		UPDATE hatamoto_menu_items
		SET parent_id = COALESCE($2, parent_id),
		    label = COALESCE($3, label),
		    link_type = COALESCE($4, link_type),
		    entity_id = COALESCE($5, entity_id),
		    url = COALESCE($6, url),
		    schema_slug = COALESCE($7, schema_slug),
		    position = COALESCE($8, position),
		    open_in_new_tab = COALESCE($9, open_in_new_tab),
		    css_classes = COALESCE($10, css_classes),
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, menu_id, parent_id, label, link_type, entity_id, url,
		          schema_slug, position, open_in_new_tab, css_classes, created_at, updated_at
	`

	var item MenuItem
	err = conn.QueryRow(ctx, query,
		id, input.ParentID, input.Label, input.LinkType, input.EntityID,
		input.URL, input.SchemaSlug, input.Position, input.OpenInNewTab, input.CSSClasses,
	).Scan(
		&item.ID, &item.MenuID, &item.ParentID, &item.Label, &item.LinkType,
		&item.EntityID, &item.URL, &item.SchemaSlug, &item.Position, &item.OpenInNewTab,
		&item.CSSClasses, &item.CreatedAt, &item.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMenuItemNotFound
		}
		return nil, fmt.Errorf("failed to update menu item: %w", err)
	}

	if item.EntityID != nil {
		r.populateEntityInfo(ctx, conn, &item)
	}

	return &item, nil
}

// DeleteMenuItem removes a menu item and reassigns children to parent.
func (r *PostgresRepository) DeleteMenuItem(ctx context.Context, id uuid.UUID) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get the item's parent_id
	var parentID *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT parent_id FROM hatamoto_menu_items WHERE id = $1`, id).Scan(&parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMenuItemNotFound
		}
		return fmt.Errorf("failed to get menu item: %w", err)
	}

	// Move children to the deleted item's parent
	_, err = tx.Exec(ctx, `UPDATE hatamoto_menu_items SET parent_id = $1 WHERE parent_id = $2`, parentID, id)
	if err != nil {
		return fmt.Errorf("failed to reassign children: %w", err)
	}

	// Delete the item
	result, err := tx.Exec(ctx, `DELETE FROM hatamoto_menu_items WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete menu item: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrMenuItemNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetMenuWithItems retrieves a menu with all items in nested structure.
func (r *PostgresRepository) GetMenuWithItems(ctx context.Context, menuID uuid.UUID) (*Menu, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	// Get menu
	menu, err := r.GetMenuByID(ctx, menuID)
	if err != nil {
		return nil, err
	}

	// Get all items for the menu with entity info
	query := `
		SELECT
			mi.id, mi.menu_id, mi.parent_id, mi.label, mi.link_type,
			mi.entity_id, mi.url, mi.schema_slug, mi.position, mi.open_in_new_tab,
			mi.css_classes, mi.created_at, mi.updated_at,
			e.slug AS entity_slug, e.name AS entity_name, es.slug AS entity_schema_slug
		FROM hatamoto_menu_items mi
		LEFT JOIN hatamoto_entities e ON mi.entity_id = e.id
		LEFT JOIN hatamoto_entity_schemas es ON e.schema_id = es.id
		WHERE mi.menu_id = $1
		ORDER BY mi.position ASC
	`

	rows, err := conn.Query(ctx, query, menuID)
	if err != nil {
		return nil, fmt.Errorf("failed to get menu items: %w", err)
	}
	defer rows.Close()

	// Build flat list first
	itemMap := make(map[uuid.UUID]*MenuItem)
	var items []*MenuItem

	for rows.Next() {
		item := &MenuItem{}
		if err := rows.Scan(
			&item.ID, &item.MenuID, &item.ParentID, &item.Label, &item.LinkType,
			&item.EntityID, &item.URL, &item.SchemaSlug, &item.Position, &item.OpenInNewTab,
			&item.CSSClasses, &item.CreatedAt, &item.UpdatedAt,
			&item.EntitySlug, &item.EntityName, &item.EntitySchemaSlug,
		); err != nil {
			return nil, fmt.Errorf("failed to scan menu item: %w", err)
		}
		item.Children = []*MenuItem{}
		itemMap[item.ID] = item
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Build tree structure
	var rootItems []*MenuItem
	for _, item := range items {
		if item.ParentID == nil {
			rootItems = append(rootItems, item)
		} else if parent, ok := itemMap[*item.ParentID]; ok {
			parent.Children = append(parent.Children, item)
		}
	}

	menu.Items = rootItems
	return menu, nil
}

// ReorderItems updates positions and parent relationships for multiple items.
func (r *PostgresRepository) ReorderItems(ctx context.Context, menuID uuid.UUID, items []ReorderInput) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, item := range items {
		_, err := tx.Exec(ctx,
			`UPDATE hatamoto_menu_items SET parent_id = $1, position = $2, updated_at = NOW() WHERE id = $3 AND menu_id = $4`,
			item.ParentID, item.Position, item.ID, menuID,
		)
		if err != nil {
			return fmt.Errorf("failed to update item position: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// populateEntityInfo fetches entity slug and name for a menu item.
func (r *PostgresRepository) populateEntityInfo(ctx context.Context, conn *pgxpool.Conn, item *MenuItem) {
	if item.EntityID == nil {
		return
	}

	var slug, name string
	err := conn.QueryRow(ctx,
		`SELECT slug, name FROM hatamoto_entities WHERE id = $1`,
		*item.EntityID,
	).Scan(&slug, &name)

	if err == nil {
		item.EntitySlug = &slug
		item.EntityName = &name
	}
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

// ReplaceMenu implements Repository. The menu row is locked for the whole
// call, so concurrent replacements run one after the other.
func (r *PostgresRepository) ReplaceMenu(ctx context.Context, id uuid.UUID, fields *UpdateMenuInput, items []ReplaceItem) error {
	return r.db.WithTx(ctx, func(tx pgx.Tx) error {
		var locked uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM hatamoto_menus WHERE id = $1 FOR UPDATE`, id).Scan(&locked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrMenuNotFound
			}
			return fmt.Errorf("failed to lock menu: %w", err)
		}
		if fields != nil && (fields.Name != nil || fields.Location != nil) {
			if _, err := tx.Exec(ctx, `UPDATE hatamoto_menus SET name = COALESCE($2, name), location = COALESCE($3, location), updated_at = NOW() WHERE id = $1`,
				id, fields.Name, fields.Location); err != nil {
				return fmt.Errorf("failed to update menu: %w", err)
			}
		}
		if items == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_menu_items WHERE menu_id = $1`, id); err != nil {
			return fmt.Errorf("failed to clear menu items: %w", err)
		}
		ids := make([]uuid.UUID, len(items))
		next := map[int]int{} // position per parent index
		for i, it := range items {
			ids[i] = uuid.New()
			var parent *uuid.UUID
			if it.Parent >= 0 {
				parent = &ids[it.Parent]
			}
			pos := next[it.Parent]
			next[it.Parent]++
			_, err := tx.Exec(ctx, `
				INSERT INTO hatamoto_menu_items (id, menu_id, parent_id, label, link_type, entity_id, url, schema_slug, position, open_in_new_tab, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())`,
				ids[i], id, parent, it.Label, it.LinkType, it.EntityID, it.URL, it.SchemaSlug, pos, it.OpenInNewTab)
			if err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == "23503" {
					return ErrEntityMissing
				}
				return fmt.Errorf("failed to create menu item: %w", err)
			}
		}
		return nil
	})
}
