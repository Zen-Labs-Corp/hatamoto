package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/firstboot"
	"hatamoto.dev/core/internal/jsonx"
)

// firstBoot makes a brand-new database render a site. Every step is idempotent
// and only fills what is missing; the built-in page/post content types come
// from migration 002.
//
//   - the singleton site config row (the renderer refuses to build a page without it)
//   - a published home page (a welcome hero, getting-started steps and an import band) so "/" renders instead of 404, only when there is no
//     entity at all, so a page an operator deleted is never resurrected; ON CONFLICT DO NOTHING keeps
//     two replicas booting together from racing on the slug
func firstBoot(ctx context.Context, pool *pgxpool.Pool, siteName string, logger *slog.Logger) error {
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ($1) ON CONFLICT (id) DO NOTHING`, siteName); err != nil {
		return fmt.Errorf("seed site config: %w", err)
	}
	doc, err := jsonx.Marshal(firstboot.HomeDoc(siteName))
	if err != nil {
		return fmt.Errorf("starter home page doc: %w", err)
	}
	tag, err := pool.Exec(ctx, `
		INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		SELECT s.id, $1::text, 'home', 'published',
		       jsonb_build_object('page_doc', $2::jsonb,
		           'content', '<p>This site is up and running. Sign in to replace this page with your own words.</p>'),
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
