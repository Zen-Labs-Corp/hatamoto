package media

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Usage is one place a media file is referenced from: an entity (its
// attributes mention the file's URL, which covers a person's photo), the site
// settings (logo, theme), a menu or a plugin's settings. The legacy authors
// table is a rollback mirror of the people and is not a usage of its own.
type Usage struct {
	Kind       string // entity | site | menu | plugin
	ID         uuid.UUID
	Name       string
	SchemaSlug string // entities only
}

// usageStore is implemented by repositories that can answer Usage.
type usageStore interface {
	Usage(ctx context.Context, stem string) ([]Usage, error)
}

// Usage lists what references m. Entities hold media as URL strings inside
// their JSON attributes, so a reference is the generated file name (its stem
// also matches the resized variants).
func (s *Service) Usage(ctx context.Context, m *Media) ([]Usage, error) {
	u, ok := s.repo.(usageStore)
	if !ok {
		return nil, nil
	}
	stem := strings.TrimSuffix(m.Filename, filepath.Ext(m.Filename))
	if stem == "" {
		return nil, nil
	}
	return u.Usage(ctx, stem)
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Usage is the Postgres implementation behind Service.Usage.
func (r *PostgresRepository) Usage(ctx context.Context, stem string) ([]Usage, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `
		SELECT 'entity', e.id, e.name, s.slug
		FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE e.status <> 'redirected' AND e.attributes::text LIKE $1
		UNION ALL
		SELECT 'site', '00000000-0000-0000-0000-000000000000'::uuid, 'Site logo', ''
		FROM hatamoto_site_config WHERE logo_url LIKE $1
		UNION ALL
		SELECT 'site', '00000000-0000-0000-0000-000000000000'::uuid, 'Theme settings', ''
		FROM hatamoto_site_config WHERE theme_extra::text LIKE $1
		UNION ALL
		SELECT 'menu', m.id, m.name, ''
		FROM hatamoto_menus m
		WHERE EXISTS (SELECT 1 FROM hatamoto_menu_items i WHERE i.menu_id = m.id AND i.url LIKE $1)
		UNION ALL
		SELECT 'plugin', '00000000-0000-0000-0000-000000000000'::uuid, p.plugin, ''
		FROM hatamoto_plugin_settings p WHERE p.settings::text LIKE $1
		ORDER BY 1, 3
		LIMIT 200`, "%"+likeEscaper.Replace(stem)+"%")
	if err != nil {
		return nil, fmt.Errorf("failed to list media usage: %w", err)
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		var u Usage
		if err := rows.Scan(&u.Kind, &u.ID, &u.Name, &u.SchemaSlug); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
