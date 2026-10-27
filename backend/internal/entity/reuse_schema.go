package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The two built-in types of the builder's reuse feature. Both hold a page_doc
// and are never public: a block is embedded by reference (a ref node), a
// theme part fills a layout slot of an opted-in theme.
const (
	BlockSchemaSlug = "block"
	PartSchemaSlug  = "theme_part"
)

// IsReuseSlug reports whether slug is the slug of a block or a theme part.
func IsReuseSlug(slug string) bool {
	return slug == BlockSchemaSlug || slug == PartSchemaSlug
}

// IsReuseSchema reports whether sc is the built-in block or theme part type.
// A site type that took the slug is not one: it keeps its normal behaviour.
func IsReuseSchema(sc *EntitySchema) bool {
	return sc != nil && sc.IsBuiltin && IsReuseSlug(sc.Slug)
}

// IsBlockSchema reports whether sc is the built-in block type.
func IsBlockSchema(sc *EntitySchema) bool {
	return sc != nil && sc.IsBuiltin && sc.Slug == BlockSchemaSlug
}

// IsPartSchema reports whether sc is the built-in theme part type.
func IsPartSchema(sc *EntitySchema) bool {
	return sc != nil && sc.IsBuiltin && sc.Slug == PartSchemaSlug
}

// ReuseStatus says which reuse types are usable on this site. A type is
// unavailable when a site type of its own already owns the slug.
type ReuseStatus struct {
	Block, Part bool
}

// SQLDB is the slice of pgx EnsureReuseSchemas needs; *pgxpool.Pool and
// pgx.Tx satisfy it.
type SQLDB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const reuseSchemaSettings = `{"sitemap":{"exclude":true}}`

var reuseSchemas = []struct {
	slug, name, description, schema string
}{
	{BlockSchemaSlug, "Block", "Reusable blocks", `{
		"type": "object",
		"properties": {
			"description": {"type": "string", "title": "Description", "maxLength": 300}
		}
	}`},
	{PartSchemaSlug, "Theme part", "Header, footer and layout parts", `{
		"type": "object",
		"required": ["slot"],
		"properties": {
			"description": {"type": "string", "title": "Description", "maxLength": 300},
			"slot": {"type": "string", "title": "Slot", "enum": ["header", "footer", "single", "archive"]}
		}
	}`},
}

// EnsureReuseSchemas creates the block and theme_part types as built-in rows
// when they are missing. It is plain data (no migration), idempotent and safe
// with two processes starting at once. A slug already owned by a site's own
// (non-built-in) type is left alone, logged, and reported unavailable.
func EnsureReuseSchemas(ctx context.Context, db SQLDB, log *slog.Logger) (ReuseStatus, error) {
	var st ReuseStatus
	for _, rs := range reuseSchemas {
		if !json.Valid([]byte(rs.schema)) {
			return st, fmt.Errorf("reuse schema %s: invalid json", rs.slug)
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO hatamoto_entity_schemas (name, slug, description, schema, is_builtin, settings)
			VALUES ($1, $2, $3, $4::jsonb, true, $5::jsonb)
			ON CONFLICT (slug) DO NOTHING`,
			rs.name, rs.slug, rs.description, rs.schema, reuseSchemaSettings); err != nil {
			return st, fmt.Errorf("ensure %s schema: %w", rs.slug, err)
		}
		var builtin bool
		err := db.QueryRow(ctx, `SELECT is_builtin FROM hatamoto_entity_schemas WHERE slug = $1`, rs.slug).Scan(&builtin)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return st, fmt.Errorf("read %s schema: %w", rs.slug, err)
		}
		ok := err == nil && builtin
		if ok {
			// A block or part is never public. Rows that got published some other
			// way (an older release, a restore, raw SQL) go back to draft.
			if _, err := db.Exec(ctx, `
				UPDATE hatamoto_entities SET status = 'draft', published_at = NULL
				WHERE status <> 'draft' AND schema_id IN (SELECT id FROM hatamoto_entity_schemas WHERE slug = $1 AND is_builtin)`,
				rs.slug); err != nil {
				return st, fmt.Errorf("reset %s entities: %w", rs.slug, err)
			}
		}
		if !ok && log != nil {
			log.Warn("builder reuse unavailable for this type: a content type of this site already uses the slug; rename it to enable it",
				slog.String("slug", rs.slug))
		}
		if rs.slug == BlockSchemaSlug {
			st.Block = ok
		} else {
			st.Part = ok
		}
	}
	return st, nil
}

// SetReuseStatus records what EnsureReuseSchemas found. It is called once at
// boot, before requests are served.
func (s *Service) SetReuseStatus(st ReuseStatus) { s.reuse = st }

// ReuseStatus reports which reuse types are available.
func (s *Service) ReuseStatus() ReuseStatus { return s.reuse }
