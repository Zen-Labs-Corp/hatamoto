package changeset

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ownership errors.
var (
	// ErrNotOwned: the target object belongs to another origin than the changeset.
	ErrNotOwned = errors.New("not_owned")
	// ErrSingleton: the kind is a site-wide singleton no changeset may write.
	ErrSingleton = errors.New("singleton kind")
)

// ReasonNotOwned is the Conflict reason of an item skipped at publish because
// its target changed hands after it was staged.
const ReasonNotOwned = "not_owned"

// singletonKinds are the site-wide settings a changeset never touches.
var singletonKinds = map[Kind]bool{
	"settings":      true,
	"url_patterns":  true,
	"home":          true,
	"menu_location": true,
	"404":           true,
	"robots":        true,
	"templates":     true,
}

// originTables maps a kind to the table that carries its origin column.
var originTables = map[Kind]string{
	KindEntity:   "hatamoto_entities",
	KindTerm:     "hatamoto_terms",
	KindMenu:     "hatamoto_menus",
	KindRedirect: "hatamoto_redirects",
	KindMedia:    "hatamoto_media",
}

// isAltChange reports whether it edits the alt text of an existing media item.
func isAltChange(it Item) bool {
	return it.Kind == KindMedia && it.Op == OpUpsert && it.TargetID != "" && len(it.BlobKeys) == 0
}

// ownerPlugin is the plugin whose origin an item's target must carry. A plugin
// changeset touches only what the plugin made, except an alt text change,
// which is made for media the owner uploaded.
func ownerPlugin(plugin string, it Item) string {
	if isAltChange(it) {
		return ""
	}
	return plugin
}

// checkOwnership refuses an update or delete of an object whose origin is not
// the changeset's: a plugin changeset only touches what that plugin created,
// and the owner's changeset only what the owner owns. A target that does not
// exist passes (the writer reports it as gone). Ownership lives in code only:
// there is no database constraint.
func (e *Engine) checkOwnership(ctx context.Context, tx pgx.Tx, plugin string, kind Kind, targetID string) error {
	if targetID == "" {
		return nil
	}
	table, ok := originTables[kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	id, err := uuid.Parse(targetID)
	if err != nil {
		return fmt.Errorf("%w: target %q is not an id", ErrInvalidItem, targetID)
	}
	var origin string
	err = tx.QueryRow(ctx, `SELECT origin FROM `+table+` WHERE id = $1`, id).Scan(&origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read origin: %w", err)
	}
	if origin != Origin(plugin) {
		return ErrNotOwned
	}
	return nil
}
