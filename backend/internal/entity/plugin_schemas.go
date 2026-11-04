package entity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PluginSchemaUse is one entity type a plugin installed and how many entries
// of it exist.
type PluginSchemaUse struct {
	Slug, Name string
	Entries    int
}

// PluginSchemaUses lists the entity types a plugin installed (origin_plugin)
// with their entry counts, sorted by slug.
func (s *Service) PluginSchemaUses(ctx context.Context, plugin string) ([]PluginSchemaUse, error) {
	var out []PluginSchemaUse
	err := s.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT s.slug, s.name, (SELECT count(*) FROM hatamoto_entities e WHERE e.schema_id = s.id)
			FROM hatamoto_entity_schemas s WHERE s.origin_plugin = $1 AND NOT s.is_builtin ORDER BY s.slug`, plugin)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (u PluginSchemaUse, err error) {
			return u, row.Scan(&u.Slug, &u.Name, &u.Entries)
		})
		return err
	})
	return out, err
}

// RemovePluginSchemas deletes the entity types a plugin installed (origin_plugin)
// and every entity of those types. Each entity goes through DeleteEntity, so
// subscribers (webhooks, IndexNow, plugin hooks) hear about the deletions. It
// is not one transaction: a failure leaves some entries deleted and the types
// in place, and the caller retries. Owner-created and built-in types are never
// touched. It returns the removed type slugs.
func (s *Service) RemovePluginSchemas(ctx context.Context, plugin string) ([]string, error) {
	var slugs []string
	var ids []uuid.UUID
	err := s.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT slug FROM hatamoto_entity_schemas WHERE origin_plugin = $1 AND NOT is_builtin ORDER BY slug`, plugin)
		if err != nil {
			return err
		}
		if slugs, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil || len(slugs) == 0 {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT e.id FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id WHERE s.origin_plugin = $1 AND NOT s.is_builtin`, plugin)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	if err != nil || len(slugs) == 0 {
		return nil, err
	}
	// A parent cannot go while it has children: delete in passes until the
	// remainder cannot shrink.
	for len(ids) > 0 {
		var blocked []uuid.UUID
		var last error
		for _, id := range ids {
			if err := s.DeleteEntity(ctx, id); err != nil {
				blocked, last = append(blocked, id), err
			}
		}
		if len(blocked) == len(ids) {
			return nil, fmt.Errorf("deleting %d entries: %w", len(blocked), last)
		}
		ids = blocked
	}
	err = s.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE origin_plugin = $1 AND NOT is_builtin`, plugin)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.schemaChanged()
	return slugs, nil
}
