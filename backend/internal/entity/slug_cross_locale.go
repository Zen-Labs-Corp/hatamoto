package entity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// crossLocaleSlug describes a row about to be written, for the check that
// refuses a slug already used on the other side of the und / language-specific
// split. The unique indexes are keyed on locale, so they cannot see that pair.
type crossLocaleSlug struct {
	SchemaID uuid.UUID
	Slug     string
	// Locale is the locale the row will have; "" means the site language,
	// resolved in SQL the way the INSERT resolves it.
	Locale string
	Parent *uuid.UUID
	// Self is the row being updated, nil on create.
	Self *uuid.UUID
}

// checkCrossLocaleSlug returns ErrSlugAlreadyExists when a same-type row (same
// parent scope for nested types) holds the slug and exactly one of the two rows
// is language-neutral (und). Two language-specific rows may share a slug. It
// takes a per-schema-and-slug advisory lock first, so two saves racing on
// opposite sides of the split are serialised and the second one sees the first.
func checkCrossLocaleSlug(ctx context.Context, tx pgx.Tx, in crossLocaleSlug) error {
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('hatamoto_entity_slug:' || $1::text || ':' || $2))`,
		in.SchemaID, in.Slug); err != nil {
		return fmt.Errorf("failed to lock the slug: %w", err)
	}
	var taken bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM hatamoto_entities
			WHERE schema_id = $1 AND slug = $2 AND variant_key IS NULL AND ($4::uuid IS NULL OR id <> $4)
			  AND (NOT slug_nested OR COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE($3::uuid, '00000000-0000-0000-0000-000000000000'::uuid))
			  AND (locale = 'und') <> (COALESCE(NULLIF($5::text, ''), public.hatamoto_site_language()) = 'und'))`,
		in.SchemaID, in.Slug, in.Parent, in.Self, in.Locale).Scan(&taken); err != nil {
		return fmt.Errorf("failed to probe slug: %w", err)
	}
	if taken {
		return fmt.Errorf("%w: another entry of this type already uses this slug, in all languages or in a specific language", ErrSlugAlreadyExists)
	}
	return nil
}
