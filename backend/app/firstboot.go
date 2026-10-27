package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/builder"
)

// firstBoot makes a brand-new database render a site. Every step is idempotent
// and only fills what is missing; the built-in page/post content types come
// from migration 002.
//
//   - the singleton site config row (the renderer refuses to build a page without it)
//   - a published home page so "/" renders instead of 404, only when there is no
//     entity at all, so a page an operator deleted is never resurrected; ON CONFLICT DO NOTHING keeps
//     two replicas booting together from racing on the slug
func firstBoot(ctx context.Context, pool *pgxpool.Pool, siteName string, logger *slog.Logger) error {
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ($1) ON CONFLICT (id) DO NOTHING`, siteName); err != nil {
		return fmt.Errorf("seed site config: %w", err)
	}
	doc, err := json.Marshal(builder.FromSections([]builder.Section{{
		ID: "welcome", Type: "hero", SortOrder: 1,
		Content: map[string]any{"headline": siteName, "subheadline": "Your site is running. Sign in to the admin to edit this page."},
	}}))
	if err != nil {
		return fmt.Errorf("starter home page doc: %w", err)
	}
	tag, err := pool.Exec(ctx, `
		INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		SELECT s.id, $1::text, 'home', 'published',
		       jsonb_build_object('page_doc', $2::jsonb,
		           'content', '<p>Your site is running. Sign in to the admin to edit this page.</p>'),
		       now()
		FROM hatamoto_entity_schemas s
		WHERE s.slug = 'page' AND NOT EXISTS (SELECT 1 FROM hatamoto_entities)
		ON CONFLICT DO NOTHING`, siteName, doc)
	if err != nil {
		return fmt.Errorf("seed starter home page: %w", err)
	}
	if tag.RowsAffected() > 0 {
		logger.Info("created starter home page")
	}
	return nil
}
