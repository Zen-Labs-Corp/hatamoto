package person

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
)

// SQLDB is the slice of database/sql Backfill needs; *sql.Tx and *sql.DB
// satisfy it, so it runs inside a goose migration and after an import.
type SQLDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Backfill makes every author a person and points the author's entities at
// it. It is idempotent: a second run finds everything done and changes
// nothing, so it also serves after importing an archive that predates people.
//
//   - Each author whose name is not the company name gets one person entity
//     (slug = author slug so /author/{slug} keeps working, bio, photo from the
//     avatar, public when the author has published entities). The person
//     carries metadata.import_key "author:<author id>" (the idempotency key)
//     and metadata.legacy_author_id (the mirror row for rollback).
//   - An author named like the company (exact, case-insensitive, trimmed) gets
//     no person: its entities become byline_org instead. Nothing else is read
//     to decide that.
//   - Entities that had author_id and no byline yet get byline_person_id (or
//     byline_org). author_id is left untouched.
//   - Users are linked to a person by equal name; failing that, a site with
//     exactly one user and one person-author links the two. Everyone else
//     stays unlinked and creates their profile at sign-in.
//   - People never carry page_sections: the key is stripped from every person
//     entity, so the Page Builder is never the editor of a person.
//
// It fails when a non-builtin schema already uses the slug `person`.
func Backfill(ctx context.Context, db SQLDB) error {
	var schemaID string
	var builtin bool
	err := db.QueryRowContext(ctx, `SELECT id::text, is_builtin FROM hatamoto_entity_schemas WHERE slug = $1`, SchemaSlug).Scan(&schemaID, &builtin)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("person backfill: the person content type is missing")
	}
	if err != nil {
		return err
	}
	if !builtin {
		return errors.New("person backfill: this site has its own content type with the slug 'person'; rename that content type (Settings, Content types) and run the upgrade again")
	}

	// Keep updated_at: this is a data move, not an edit.
	if _, err := db.ExecContext(ctx, `SELECT set_config('hatamoto.import', 'on', true)`); err != nil {
		return err
	}
	defer func() { _, _ = db.ExecContext(ctx, `SELECT set_config('hatamoto.import', '', true)`) }()

	var company string
	err = db.QueryRowContext(ctx, `SELECT btrim(company_name) FROM hatamoto_site_config LIMIT 1`).Scan(&company)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if err := createPeople(ctx, db, schemaID, company); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE hatamoto_entities e SET byline_person_id = p.id
		FROM hatamoto_entities p
		WHERE p.metadata->>'import_key' = 'author:' || e.author_id::text
		  AND p.schema_id = $1::uuid
		  AND e.byline_person_id IS NULL AND NOT e.byline_org`, schemaID); err != nil {
		return err
	}
	if company != "" {
		if _, err := db.ExecContext(ctx, `
			UPDATE hatamoto_entities e SET byline_org = true
			FROM hatamoto_authors a
			WHERE a.id = e.author_id AND lower(btrim(a.name)) = lower($1::text)
			  AND e.byline_person_id IS NULL AND NOT e.byline_org`, company); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE hatamoto_entities SET attributes = attributes - 'page_sections'
		WHERE schema_id = $1::uuid AND attributes ? 'page_sections'`, schemaID); err != nil {
		return err
	}
	return linkUsers(ctx, db, schemaID, company)
}

// createPeople inserts a person for every non-organisation author that has
// none yet.
func createPeople(ctx context.Context, db SQLDB, schemaID, company string) error {
	type row struct {
		id, slug, name, bio, photo string
		published                  bool
	}
	rows, err := db.QueryContext(ctx, `
		SELECT a.id::text, a.slug, a.name, a.bio, COALESCE(m.public_url, ''),
		       EXISTS (SELECT 1 FROM hatamoto_entities e WHERE e.author_id = a.id AND e.status = 'published')
		FROM hatamoto_authors a
		LEFT JOIN hatamoto_media m ON m.id = a.avatar_media_id
		WHERE ($1::text = '' OR lower(btrim(a.name)) <> lower($1::text))
		  AND NOT EXISTS (SELECT 1 FROM hatamoto_entities p
		                  WHERE p.metadata->>'import_key' = 'author:' || a.id::text)
		ORDER BY a.created_at, a.id`, company)
	if err != nil {
		return err
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.name, &r.bio, &r.photo, &r.published); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, r := range todo {
		// Status follows public: the previous release shows every published entity.
		status := "draft"
		if r.published {
			status = "published"
		}
		attrs := map[string]any{"public": r.published}
		if bio := strings.TrimSpace(r.bio); bio != "" {
			attrs["bio"] = "<p>" + html.EscapeString(bio) + "</p>"
		}
		if r.photo != "" {
			attrs["photo"] = r.photo
		}
		attrJSON, _ := json.Marshal(attrs)
		meta, _ := json.Marshal(map[string]string{"import_key": "author:" + r.id, "legacy_author_id": r.id})

		slug := r.slug
		for n := 2; ; n++ {
			var taken bool
			if err := db.QueryRowContext(ctx, `
				SELECT EXISTS (SELECT 1 FROM hatamoto_entities WHERE schema_id = $1::uuid AND slug = $2 AND variant_key IS NULL)`,
				schemaID, slug).Scan(&taken); err != nil {
				return err
			}
			if !taken {
				break
			}
			slug = fmt.Sprintf("%s-%d", r.slug, n)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, metadata, published_at)
			VALUES ($1::uuid, $2, $3, $6::text, $4::jsonb, $5::jsonb, CASE WHEN $6::text = 'published' THEN now() END)
			ON CONFLICT ((metadata->>'import_key')) WHERE metadata ? 'import_key' DO NOTHING`,
			schemaID, r.name, slug, string(attrJSON), string(meta), status); err != nil {
			return err
		}
	}
	return nil
}

// linkUsers links unlinked users to the people this backfill made.
func linkUsers(ctx context.Context, db SQLDB, schemaID, company string) error {
	// Equal names, one-to-one only: an ambiguous name links nobody.
	if _, err := db.ExecContext(ctx, `
		WITH pairs AS (
			SELECT u.id AS user_id, p.id AS entity_id,
			       count(*) OVER (PARTITION BY u.id) AS per_user,
			       count(*) OVER (PARTITION BY p.id) AS per_person
			FROM hatamoto_users u
			JOIN hatamoto_entities p ON p.schema_id = $1::uuid AND p.metadata->>'import_key' LIKE 'author:%'
			                        AND lower(btrim(p.name)) = lower(btrim(u.name))
			WHERE NOT EXISTS (SELECT 1 FROM hatamoto_user_people l WHERE l.user_id = u.id OR l.entity_id = p.id)
		)
		INSERT INTO hatamoto_user_people (user_id, entity_id)
		SELECT user_id, entity_id FROM pairs WHERE per_user = 1 AND per_person = 1
		ON CONFLICT DO NOTHING`, schemaID); err != nil {
		return err
	}
	// A site with one user and one person: they are the same human.
	_, err := db.ExecContext(ctx, `
		INSERT INTO hatamoto_user_people (user_id, entity_id)
		SELECT u.id, p.id
		FROM (SELECT id FROM hatamoto_users) u,
		     (SELECT id FROM hatamoto_entities WHERE schema_id = $1::uuid AND metadata->>'import_key' LIKE 'author:%') p
		WHERE (SELECT count(*) FROM hatamoto_users) = 1
		  AND (SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1::uuid AND metadata->>'import_key' LIKE 'author:%') = 1
		  AND NOT EXISTS (SELECT 1 FROM hatamoto_user_people)
		ON CONFLICT DO NOTHING`, schemaID)
	return err
}
