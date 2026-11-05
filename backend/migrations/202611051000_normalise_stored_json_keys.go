// hatamoto:backfill idempotent

package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/jsonx/keynorm"
)

func init() {
	goose.AddMigrationContext(normaliseStoredJSONKeys, noopDown)
}

// storedKeyColumn is one typed JSON column whose object keys are rewritten to
// the spelling the decoders read.
type storedKeyColumn struct {
	table, column string
	shape         *keynorm.Shape
}

// storedKeyColumns lists the only columns this migration touches. The shapes
// are frozen copies of the key layout at the time of writing, not the live
// types, so a later change of a type cannot change what this migration does.
// A test pins them to a literal snapshot of the key names.
func storedKeyColumns() []storedKeyColumn {
	return []storedKeyColumn{
		{"hatamoto_site_config", "theme_extra", nskThemeExtra()},
		{"hatamoto_entity_schemas", "settings", nskSchemaSettings()},
		{"hatamoto_forms", "fields", &keynorm.Shape{Elem: nskFormField()}},
		{"hatamoto_forms", "settings", nskFormSettings()},
	}
}

// nskObj is an object shape with the given canonical keys and opaque values.
func nskObj(keys ...string) *keynorm.Shape {
	s := &keynorm.Shape{Fields: map[string]*keynorm.Shape{}}
	for _, k := range keys {
		s.Fields[k] = nil
	}
	return s
}

// nskWith sets the shape of one key of s and returns s.
func nskWith(s *keynorm.Shape, key string, child *keynorm.Shape) *keynorm.Shape {
	s.Fields[key] = child
	return s
}

func nskThemeExtra() *keynorm.Shape {
	s := nskObj("builder", "media_rebuild")
	nskWith(s, "colors", nskObj("primary", "secondary", "accent", "background", "text"))
	nskWith(s, "typography", nskObj("fontFamily", "headingFamily"))
	nskWith(s, "borderRadius", nskObj("sm", "md", "lg"))
	nskWith(s, "headerCta", nskObj("text", "url"))
	nskWith(s, "socialLinks", &keynorm.Shape{Open: true})
	nskWith(s, "media_formats", nskObj("webp", "avif"))
	return s
}

func nskSchemaSettings() *keynorm.Shape {
	s := nskObj("url_pattern", "archive_path", "sitemap_group", "aliases", "section_pages",
		"archive_title", "archive_description", "archive_crumb_label",
		"schema_org_type", "article_type", "faq_from_content", "editor", "locale_neutral")
	nskWith(s, "overrides", &keynorm.Shape{Elem: nskObj("attribute", "equals", "url_pattern", "sitemap_group")})
	nskWith(s, "sitemap", nskObj("priority", "changefreq", "exclude"))
	return s
}

func nskFormField() *keynorm.Shape {
	s := nskObj("id", "type", "label", "name", "placeholder", "required", "default_value", "help_text", "width", "position")
	nskWith(s, "options", &keynorm.Shape{Elem: nskObj("label", "value")})
	nskWith(s, "validation", nskObj("min_length", "max_length", "min", "max", "pattern", "message"))
	nskWith(s, "attributes", &keynorm.Shape{Open: true})
	return s
}

func nskFormSettings() *keynorm.Shape {
	return nskObj("submit_button_text", "success_message", "success_redirect_url", "enable_captcha",
		"notify_email", "confirmation_email", "confirmation_message", "store_submissions")
}

// normaliseStoredJSONKeys rewrites case-variant object keys of the typed JSON
// columns to the canonical tag names. Version 1 of encoding/json matched keys
// case-insensitively and version 2 does not, so a key stored as "headercta"
// would stop being read once a decoder moves over. The canonical spelling is
// what the current decoders and the previous release already read (and the
// previous release matches either spelling), so the rewrite is safe to roll
// back over. When two keys of one object fold to the same name the last
// one in jsonb text order wins, which is the value v1 read; hatamoto doctor
// json shows what would change beforehand. A row that is already canonical is
// not written, so a second run changes nothing. Other columns, including the
// ones the passkey library owns, are never touched.
func normaliseStoredJSONKeys(ctx context.Context, tx *sql.Tx) error {
	for _, c := range storedKeyColumns() {
		if _, err := normaliseColumnKeys(ctx, tx, c); err != nil {
			return fmt.Errorf("%s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// normaliseColumnKeys normalises one column row by row and returns how many
// rows it rewrote. A missing table is skipped.
func normaliseColumnKeys(ctx context.Context, tx *sql.Tx, c storedKeyColumn) (int, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, c.table).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	type row struct{ id, raw string }
	var todo []row
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(
		`SELECT id::text, %[2]s::text FROM %[1]s WHERE %[2]s IS NOT NULL ORDER BY id::text FOR UPDATE`, c.table, c.column))
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.raw); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()

	changed := 0
	for _, r := range todo {
		out, changes, err := keynorm.Normalise([]byte(r.raw), c.shape)
		if err != nil {
			return changed, fmt.Errorf("row %s: %w", r.id, err)
		}
		if len(changes) == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`UPDATE %[1]s SET %[2]s = $1::jsonb WHERE id::text = $2`, c.table, c.column), string(out), r.id); err != nil {
			return changed, fmt.Errorf("row %s: %w", r.id, err)
		}
		changed++
	}
	return changed, nil
}
