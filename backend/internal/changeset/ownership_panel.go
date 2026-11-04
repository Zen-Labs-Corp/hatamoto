package changeset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// OwnershipState is where an entity stands relative to the plugin that
// imported it.
type OwnershipState string

const (
	// OwnershipNone: no plugin ever imported the entity; nothing to show.
	OwnershipNone OwnershipState = ""
	// OwnershipTracked: a plugin owns the entity and keeps it up to date.
	OwnershipTracked OwnershipState = "tracked"
	// OwnershipYours: a plugin imported the entity and a person took it over.
	OwnershipYours OwnershipState = "yours"
)

// Ownership errors of the hand-back.
var (
	// ErrNotHandedOver: the entity is not the site owner's, so there is
	// nothing to hand back.
	ErrNotHandedOver = errors.New("the entity is already tracked by a plugin")
	// ErrNoMapping: no plugin ever imported the entity, so no plugin can take
	// it back.
	ErrNoMapping = errors.New("no plugin imported this entity")
)

// Ownership describes who owns one entity. Plugin is the machine name of the
// importing plugin and SourceKey the key it uses for the entity. The last
// changeset that applied to the entity is ChangesetID (zero when none).
type Ownership struct {
	State       OwnershipState
	Plugin      string
	SourceKey   string
	ChangesetID int64
	PublishedAt *time.Time
}

// OwnershipService reads the ownership of entities and hands them back to the
// plugin that imported them.
type OwnershipService struct {
	DB *database.DB
}

// pluginOf returns the plugin name of an origin ("plugin:demo" is "demo").
func pluginOf(origin string) string { return strings.TrimPrefix(origin, "plugin:") }

// Get reports the ownership of an entity. An origin of the form plugin:X is
// tracked; the owner's origin with an import mapping is yours (the mapping
// survives a hand-over, which is what a hand-back relies on); without a
// mapping nothing is reported. An unknown entity reports none.
func (s *OwnershipService) Get(ctx context.Context, id uuid.UUID) (*Ownership, error) {
	var origin string
	err := s.DB.Pool().QueryRow(ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, id).Scan(&origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return &Ownership{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the origin: %w", err)
	}

	// The mapping table has no index on target_id; it stays small (one row per
	// imported object).
	var mapOrigin, sourceKey string
	err = s.DB.Pool().QueryRow(ctx, `
		SELECT origin, source_key FROM hatamoto_changeset_source_keys
		WHERE kind = $1 AND target_id = $2::text
		ORDER BY (origin = $3) DESC, origin, source_key LIMIT 1`,
		string(KindEntity), id.String(), origin).Scan(&mapOrigin, &sourceKey)
	hasMap := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to read the import mapping: %w", err)
	}

	out := &Ownership{}
	switch {
	case origin != "owner":
		out.State, out.Plugin, out.SourceKey = OwnershipTracked, pluginOf(origin), sourceKey
	case hasMap:
		out.State, out.Plugin, out.SourceKey = OwnershipYours, pluginOf(mapOrigin), sourceKey
	default:
		return out, nil
	}

	var csID int64
	var at *time.Time
	err = s.DB.Pool().QueryRow(ctx, `
		SELECT c.id, c.published_at
		FROM hatamoto_changeset_items i JOIN hatamoto_changesets c ON c.id = i.changeset_id
		WHERE i.kind = $1 AND i.target_id = $2 AND i.result = $3
		ORDER BY c.id DESC LIMIT 1`,
		string(KindEntity), id.String(), ResultApplied).Scan(&csID, &at)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to read the last changeset: %w", err)
	}
	if err == nil {
		out.ChangesetID, out.PublishedAt = csID, at
	}
	return out, nil
}

// HandBack gives an entity a person took over back to the plugin that
// imported it, and returns that plugin's name. Version and updated_at stay as
// they are: nothing about the content changed.
func (s *OwnershipService) HandBack(ctx context.Context, id uuid.UUID) (string, error) {
	var plugin string
	err := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		var origin string
		err := tx.QueryRow(ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1 FOR UPDATE`, id).Scan(&origin)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoMapping
		}
		if err != nil {
			return fmt.Errorf("failed to read the origin: %w", err)
		}
		if origin != "owner" {
			return ErrNotHandedOver
		}
		var mapOrigin string
		err = tx.QueryRow(ctx, `
			SELECT origin FROM hatamoto_changeset_source_keys
			WHERE kind = $1 AND target_id = $2::text
			ORDER BY origin, source_key LIMIT 1`,
			string(KindEntity), id.String()).Scan(&mapOrigin)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoMapping
		}
		if err != nil {
			return fmt.Errorf("failed to read the import mapping: %w", err)
		}
		// The updated_at trigger stamps NOW() unless hatamoto.import is on
		// (transaction-local).
		if _, err := tx.Exec(ctx, `SELECT set_config('hatamoto.import', 'on', true)`); err != nil {
			return fmt.Errorf("failed to keep updated_at: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_entities SET origin = $2 WHERE id = $1 AND origin = 'owner'`, id, mapOrigin); err != nil {
			return fmt.Errorf("failed to hand the entity back: %w", err)
		}
		plugin = pluginOf(mapOrigin)
		return nil
	})
	if err != nil {
		return "", err
	}
	return plugin, nil
}
