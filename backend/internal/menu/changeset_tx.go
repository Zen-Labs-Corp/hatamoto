package menu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Limits of the menu and item columns.
const (
	maxMenuSlugLen = 100
	maxMenuNameLen = 100
	maxLabelLen    = 100
	maxItemURLLen  = 500
)

// Changeset write errors.
var (
	// ErrLocationNotAllowed is returned for an op that sets a location: menu
	// locations are singletons and a changeset never assigns them.
	ErrLocationNotAllowed = errors.New("menu changesets cannot set a location")
	// ErrInvalidMenu is returned (wrapped, with the reason) for a menu or item
	// a normal save would refuse as well.
	ErrInvalidMenu = errors.New("invalid menu")
)

// MenuOp is one menu write of a changeset. A nil ID creates a menu; a set ID
// updates it (name and items, wholesale) or, with Delete, removes it. An
// update keeps the slug. Location must stay empty.
type MenuOp struct {
	ID            *uuid.UUID
	Slug          string
	Name          string
	Location      string
	Items         []ReplaceItem
	BaseUpdatedAt *time.Time
	Origin        string
	Delete        bool
}

// MenuTxResult reports what ApplyChangesetTx did. Before is the pre-image
// (menu plus items) of an updated or deleted menu, for RestoreChangesetTx.
// Conflict is set, and nothing written, when the menu moved since BaseUpdatedAt.
type MenuTxResult struct {
	ID       uuid.UUID
	Slug     string
	Before   json.RawMessage
	Created  bool
	Conflict bool
}

type menuImage struct {
	ID        uuid.UUID   `json:"id"`
	Name      string      `json:"name"`
	Slug      string      `json:"slug"`
	Location  *string     `json:"location,omitempty"`
	Origin    string      `json:"origin"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Items     []itemImage `json:"items"`
}

type itemImage struct {
	ID           uuid.UUID     `json:"id"`
	ParentID     *uuid.UUID    `json:"parent_id,omitempty"`
	Label        string        `json:"label"`
	LinkType     string        `json:"link_type"`
	EntityID     *uuid.UUID    `json:"entity_id,omitempty"`
	URL          *string       `json:"url,omitempty"`
	SchemaSlug   *string       `json:"schema_slug,omitempty"`
	Position     int           `json:"position"`
	OpenInNewTab bool          `json:"open_in_new_tab"`
	CSSClasses   *string       `json:"css_classes,omitempty"`
	Attrs        MenuItemAttrs `json:"attrs"`
}

// ApplyChangesetTx writes one menu op inside the caller's transaction. It uses
// tx only: no other connection, no observer call (the engine calls
// NotifyChanged after commit). It does not assign locations and does not check
// ownership.
func (s *Service) ApplyChangesetTx(ctx context.Context, tx pgx.Tx, op MenuOp) (MenuTxResult, error) {
	if op.Location != "" {
		return MenuTxResult{}, ErrLocationNotAllowed
	}
	if op.ID != nil {
		return applyExistingTx(ctx, tx, op)
	}
	if op.Delete {
		return MenuTxResult{}, ErrMenuNotFound
	}
	return createTx(ctx, tx, op)
}

// NotifyChanged runs the menu change observer; call it once after the
// changeset's transaction committed.
func (s *Service) NotifyChanged(ctx context.Context) { s.notifyChanged(ctx) }

// cleanOp validates and normalises the name and items like Service.ReplaceMenu.
func cleanOp(op MenuOp) (string, []ReplaceItem, error) {
	name := strings.TrimSpace(op.Name)
	if name == "" || utf8.RuneCountInString(name) > maxMenuNameLen {
		return "", nil, fmt.Errorf("%w: name must be 1-%d characters", ErrInvalidMenu, maxMenuNameLen)
	}
	clean := make([]ReplaceItem, len(op.Items))
	for i, it := range op.Items {
		if !ValidLinkType(it.LinkType) {
			return "", nil, ErrInvalidLinkType
		}
		if it.Parent < -1 || it.Parent >= i {
			return "", nil, fmt.Errorf("%w: item %d has an invalid parent", ErrInvalidMenu, i)
		}
		if l := utf8.RuneCountInString(it.Label); l == 0 || l > maxLabelLen {
			return "", nil, fmt.Errorf("%w: item %d label must be 1-%d characters", ErrInvalidMenu, i, maxLabelLen)
		}
		attrs, classes, err := cleanItemSettings(it.Attrs, it.CSSClasses)
		if err != nil {
			return "", nil, err
		}
		it.Attrs, it.CSSClasses = attrs, classes
		if it.LinkType == LinkTypeNone {
			it.EntityID, it.URL, it.SchemaSlug = nil, nil, nil
		}
		if it.URL != nil && utf8.RuneCountInString(*it.URL) > maxItemURLLen {
			return "", nil, fmt.Errorf("%w: item %d url is too long", ErrInvalidMenu, i)
		}
		clean[i] = it
	}
	return name, clean, nil
}

func createTx(ctx context.Context, tx pgx.Tx, op MenuOp) (MenuTxResult, error) {
	name, items, err := cleanOp(op)
	if err != nil {
		return MenuTxResult{}, err
	}
	base := generateSlug(name)
	if op.Slug != "" {
		base = normalizeSlug(op.Slug)
	}
	if base == "" {
		return MenuTxResult{}, fmt.Errorf("%w: empty slug", ErrInvalidMenu)
	}
	if len(base) > maxMenuSlugLen {
		base = strings.TrimRight(base[:maxMenuSlugLen], "-")
	}
	origin := op.Origin
	if origin == "" {
		origin = "owner"
	}

	id := uuid.New()
	for n := 1; ; n++ {
		slug := suffixed(base, n)
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_menus WHERE slug = $1)`, slug).Scan(&taken); err != nil {
			return MenuTxResult{}, fmt.Errorf("failed to check menu slug: %w", err)
		}
		if taken {
			continue
		}
		// The insert runs in a savepoint: losing a race on the slug must not
		// poison the publish transaction.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return MenuTxResult{}, fmt.Errorf("failed to open savepoint: %w", err)
		}
		_, err = sp.Exec(ctx, `INSERT INTO hatamoto_menus (id, name, slug, origin, created_at, updated_at) VALUES ($1, $2, $3, $4, NOW(), NOW())`,
			id, name, slug, origin)
		if err == nil {
			err = insertItems(ctx, sp, id, items)
		}
		if err != nil {
			_ = sp.Rollback(ctx)
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				continue
			}
			return MenuTxResult{}, err
		}
		if err := sp.Commit(ctx); err != nil {
			return MenuTxResult{}, fmt.Errorf("failed to release savepoint: %w", err)
		}
		return MenuTxResult{ID: id, Slug: slug, Created: true}, nil
	}
}

// suffixed is base for n=1, else base-n cut so the result fits the column.
func suffixed(base string, n int) string {
	if n <= 1 {
		return base
	}
	suffix := "-" + strconv.Itoa(n)
	if len(base)+len(suffix) > maxMenuSlugLen {
		base = strings.TrimRight(base[:maxMenuSlugLen-len(suffix)], "-")
	}
	return base + suffix
}

func applyExistingTx(ctx context.Context, tx pgx.Tx, op MenuOp) (MenuTxResult, error) {
	var name string
	var items []ReplaceItem
	if !op.Delete {
		var err error
		if name, items, err = cleanOp(op); err != nil {
			return MenuTxResult{}, err
		}
	}
	id := *op.ID
	var updatedAt time.Time
	var slug string
	var location *string
	if err := tx.QueryRow(ctx, `SELECT slug, updated_at, location FROM hatamoto_menus WHERE id = $1 FOR UPDATE`, id).Scan(&slug, &updatedAt, &location); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MenuTxResult{}, ErrMenuNotFound
		}
		return MenuTxResult{}, fmt.Errorf("failed to lock menu: %w", err)
	}
	res := MenuTxResult{ID: id, Slug: slug}
	if op.BaseUpdatedAt != nil && !op.BaseUpdatedAt.Truncate(time.Microsecond).Equal(updatedAt.Truncate(time.Microsecond)) {
		res.Conflict = true
		return res, nil
	}
	before, err := loadImage(ctx, tx, id)
	if err != nil {
		return MenuTxResult{}, err
	}
	if res.Before, err = json.Marshal(before); err != nil {
		return MenuTxResult{}, fmt.Errorf("encode menu pre-image: %w", err)
	}
	if op.Delete {
		if location != nil { // deleting it would empty a location of the site
			return MenuTxResult{}, ErrMenuHasLocation
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_menus WHERE id = $1`, id); err != nil {
			return MenuTxResult{}, fmt.Errorf("failed to delete menu: %w", err)
		}
		return res, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_menus SET name = $2, updated_at = NOW() WHERE id = $1`, id, name); err != nil {
		return MenuTxResult{}, fmt.Errorf("failed to update menu: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_menu_items WHERE menu_id = $1`, id); err != nil {
		return MenuTxResult{}, fmt.Errorf("failed to clear menu items: %w", err)
	}
	if err := insertItems(ctx, tx, id, items); err != nil {
		return MenuTxResult{}, err
	}
	return res, nil
}

func loadImage(ctx context.Context, tx pgx.Tx, id uuid.UUID) (menuImage, error) {
	m := menuImage{Items: []itemImage{}}
	if err := tx.QueryRow(ctx, `SELECT id, name, slug, location, origin, created_at, updated_at FROM hatamoto_menus WHERE id = $1`, id).
		Scan(&m.ID, &m.Name, &m.Slug, &m.Location, &m.Origin, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return m, fmt.Errorf("failed to read menu: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT id, parent_id, label, link_type, entity_id, url, schema_slug, COALESCE(position, 0),
		       COALESCE(open_in_new_tab, false), css_classes, attrs
		FROM hatamoto_menu_items WHERE menu_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return m, fmt.Errorf("failed to read menu items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it itemImage
		var raw []byte
		if err := rows.Scan(&it.ID, &it.ParentID, &it.Label, &it.LinkType, &it.EntityID, &it.URL, &it.SchemaSlug,
			&it.Position, &it.OpenInNewTab, &it.CSSClasses, &raw); err != nil {
			return m, fmt.Errorf("failed to read menu item: %w", err)
		}
		it.Attrs = decodeAttrs(raw)
		m.Items = append(m.Items, it)
	}
	return m, rows.Err()
}

// RestoreChangesetTx puts a menu back to the pre-image an earlier
// ApplyChangesetTx returned (revert of an update or delete). A menu created by
// a changeset is reverted with a Delete op instead.
func (s *Service) RestoreChangesetTx(ctx context.Context, tx pgx.Tx, before json.RawMessage) error {
	var m menuImage
	if err := json.Unmarshal(before, &m); err != nil {
		return fmt.Errorf("decode menu pre-image: %w", err)
	}
	// An update trigger stamps updated_at, so the restored row is "now"; created_at is kept.
	// A location is never assigned here: the owner decides where a menu shows,
	// so a live menu keeps its location and a re-created one has none.
	if _, err := tx.Exec(ctx, `
		INSERT INTO hatamoto_menus (id, name, slug, origin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, slug = EXCLUDED.slug,
			origin = EXCLUDED.origin, updated_at = NOW()`,
		m.ID, m.Name, m.Slug, m.Origin, m.CreatedAt); err != nil {
		return fmt.Errorf("failed to restore menu: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_menu_items WHERE menu_id = $1`, m.ID); err != nil {
		return fmt.Errorf("failed to clear menu items: %w", err)
	}
	// Parents come before children: a parent's row is inserted first.
	for _, it := range orderParentsFirst(m.Items) {
		attrs, err := encodeAttrs(it.Attrs)
		if err != nil {
			return err
		}
		// A linked entity deleted since is dropped (the FK would be SET NULL anyway).
		if _, err := tx.Exec(ctx, `
			INSERT INTO hatamoto_menu_items (id, menu_id, parent_id, label, link_type, entity_id, url, schema_slug, position, open_in_new_tab, css_classes, attrs, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, (SELECT id FROM hatamoto_entities WHERE id = $6), $7, $8, $9, $10, $11, $12::jsonb, NOW(), NOW())`,
			it.ID, m.ID, it.ParentID, it.Label, it.LinkType, it.EntityID, it.URL, it.SchemaSlug, it.Position, it.OpenInNewTab, it.CSSClasses, attrs); err != nil {
			return fmt.Errorf("failed to restore menu item: %w", err)
		}
	}
	return nil
}

func orderParentsFirst(items []itemImage) []itemImage {
	out := make([]itemImage, 0, len(items))
	placed := map[uuid.UUID]bool{}
	for len(out) < len(items) {
		progress := false
		for _, it := range items {
			if placed[it.ID] || (it.ParentID != nil && !placed[*it.ParentID]) {
				continue
			}
			placed[it.ID] = true
			out = append(out, it)
			progress = true
		}
		if !progress { // dangling parent: keep the rest as they are
			for _, it := range items {
				if !placed[it.ID] {
					out = append(out, it)
				}
			}
			break
		}
	}
	return out
}
