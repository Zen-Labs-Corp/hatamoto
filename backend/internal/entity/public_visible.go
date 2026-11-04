package entity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// PublicIDs returns which of ids a visitor can see: published, not a variant,
// and passing the public visibility rule of every public read (hidden persons,
// hidden locales and built-in layout blocks never count). Locales follow
// VisibleLocales(ctx).
func (r *PostgresPublicRepository) PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Pool().Query(ctx, `
		SELECT e.id
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE e.id = ANY($1) AND e.status = 'published' AND e.variant_key IS NULL AND `+HiddenPersonFor("$2"), ids, VisibleLocales(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to check public entities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// PublicMediaStems returns which of the media file stems (the generated file
// name without extension, which also matches resized variants) a public page
// references: some publicly visible entity mentions it.
func (r *PostgresPublicRepository) PublicMediaStems(ctx context.Context, stems []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(stems) == 0 {
		return out, nil
	}
	rows, err := r.db.Pool().Query(ctx, `
		SELECT t.stem
		FROM unnest($1::text[]) AS t(stem)
		WHERE EXISTS (
			SELECT 1
			FROM hatamoto_entities e
			JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
			WHERE e.status = 'published' AND e.variant_key IS NULL AND `+HiddenPersonFor("$2")+`
			  AND e.attributes::text LIKE '%' || replace(replace(replace(t.stem, '\', '\\'), '%', '\%'), '_', '\_') || '%'
		)`, stems, VisibleLocales(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to check public media: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out[s] = true
	}
	return out, rows.Err()
}
