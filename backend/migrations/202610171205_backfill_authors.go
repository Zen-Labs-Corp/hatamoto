package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/author"
)

func init() {
	goose.AddMigrationContext(backfillAuthors, func(context.Context, *sql.Tx) error { return nil })
}

// backfillAuthors turns the plain-text bylines of existing entities
// (attributes.author, else attributes.author_name) into author rows and links
// the entities to them (v1-21-import-foundations, part 1b).
//
// It is a Go migration so the slug is computed by author.Slugify, the very
// function that finds or creates authors on every save: the slug rule cannot
// drift between the migration and the code, and a later save of any of these
// entities finds the author this created instead of making a second one.
// Names that differ only in case or punctuation share a slug and so one author
// (the byline page listed them together already); the name kept is the first
// spelling in byte order, so every database picks the same one. Bylines longer
// than an author name (255 characters) get no row and stay plain text.
// attributes are never written, so JSON-LD and the rendered byline are
// identical before and after; updated_at is left alone too.
//
// The Down step does nothing: dropping the table is the previous migration's
// Down.
func backfillAuthors(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id::text, attributes::text FROM hatamoto_entities WHERE author_id IS NULL AND (attributes ? 'author' OR attributes ? 'author_name')`)
	if err != nil {
		return err
	}
	type link struct{ entity, slug string }
	var links []link
	firstName := map[string]string{} // slug -> name, smallest in byte order
	for rows.Next() {
		var id, attrs string
		if err := rows.Scan(&id, &attrs); err != nil {
			rows.Close()
			return err
		}
		name := bylineOf(attrs)
		if name == "" || utf8.RuneCountInString(name) > author.MaxNameLen {
			continue
		}
		slug := author.Slugify(name)
		links = append(links, link{id, slug})
		if cur, ok := firstName[slug]; !ok || name < cur {
			firstName[slug] = name
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(links) == 0 {
		return nil
	}

	slugs, names := make([]string, 0, len(firstName)), make([]string, 0, len(firstName))
	for s, n := range firstName {
		slugs, names = append(slugs, s), append(names, n)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO hatamoto_authors (slug, name) SELECT * FROM unnest($1::text[], $2::text[])
		ON CONFLICT (slug) DO NOTHING`, slugs, names); err != nil {
		return err
	}

	ids, ls := make([]string, len(links)), make([]string, len(links))
	for i, l := range links {
		ids[i], ls[i] = l.entity, l.slug
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('hatamoto.import', 'on', true)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE hatamoto_entities e SET author_id = a.id
		FROM unnest($1::uuid[], $2::text[]) AS t(entity_id, slug)
		JOIN hatamoto_authors a ON a.slug = t.slug
		WHERE e.id = t.entity_id`, ids, ls); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `SELECT set_config('hatamoto.import', '', true)`)
	return err
}

// bylineOf is the byline of an attributes document: author, else author_name
// (the rule of the entity service).
func bylineOf(attrs string) string {
	var m map[string]any
	if json.Unmarshal([]byte(attrs), &m) != nil {
		return ""
	}
	for _, k := range []string{"author", "author_name"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
