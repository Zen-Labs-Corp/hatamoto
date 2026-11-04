package entity

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// RemovePluginSchemas deletes the entity types a plugin installed (origin_plugin)
// and every entity of those types, in one transaction. Owner-created and
// built-in types are never touched. It returns the removed type slugs.
func (s *Service) RemovePluginSchemas(ctx context.Context, plugin string) ([]string, error) {
	var slugs []string
	err := s.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT slug FROM hatamoto_entity_schemas WHERE origin_plugin = $1 AND NOT is_builtin ORDER BY slug`, plugin)
		if err != nil {
			return err
		}
		slugs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil || len(slugs) == 0 {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id IN (SELECT id FROM hatamoto_entity_schemas WHERE origin_plugin = $1 AND NOT is_builtin)`, plugin); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE origin_plugin = $1 AND NOT is_builtin`, plugin)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(slugs) > 0 {
		s.schemaChanged()
	}
	return slugs, nil
}
