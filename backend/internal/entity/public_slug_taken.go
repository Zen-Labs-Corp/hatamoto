package entity

import (
	"context"
	"fmt"
)

// SlugTaken reports whether a published entity of the schema holds slug in
// locale ("" is the site language). It sees what the public site sees: draft
// and scheduled entities, variants, hidden persons, hidden locales and
// built-in layout blocks never count, so the answer cannot leak unpublished
// content.
func (r *PostgresPublicRepository) SlugTaken(ctx context.Context, schemaSlug, slug, locale string) (bool, error) {
	var scope []string // nil: the site language
	if locale != "" {
		scope = append(scope, locale)
	}
	var taken bool
	err := r.db.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM hatamoto_entities e
			JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
			WHERE s.slug = $1 AND e.slug = $2
			  AND e.status = 'published' AND e.variant_key IS NULL AND `+HiddenPersonFor("$3")+`
		)`, schemaSlug, slug, scope).Scan(&taken)
	if err != nil {
		return false, fmt.Errorf("failed to check slug: %w", err)
	}
	return taken, nil
}
