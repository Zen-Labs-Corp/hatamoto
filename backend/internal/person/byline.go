package person

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/entity"
)

// maxNameLen is the longest person name (the entities.name column).
const maxNameLen = 255

// Byline resolves byline text to people and keeps the hatamoto_authors mirror
// of each person. It implements entity.BylineResolver.
//
// The mirror exists only for rollback: the previous binary renders bylines from
// author_id, so every person has an author row with the same slug and name (its
// id is kept in the person's metadata.legacy_author_id) and the plain-text
// attributes.author of their posts carries the display name.
type Byline struct{ db DB }

// NewByline returns a resolver over a pool or transaction.
func NewByline(db DB) *Byline { return &Byline{db: db} }

var _ entity.BylineResolver = (*Byline)(nil)

// companyName is the site organisation's name, "" when unset.
func (b *Byline) companyName(ctx context.Context) (string, error) {
	var name string
	err := b.db.QueryRow(ctx, `SELECT company_name FROM hatamoto_site_config LIMIT 1`).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(name), err
}

// Resolve implements entity.BylineResolver. Text equal (case-insensitive) to
// the company name credits the organisation and never makes a person.
func (b *Byline) Resolve(ctx context.Context, name string) (entity.Byline, error) {
	name = strings.TrimSpace(name)
	slug := author.Slugify(name)
	if slug == "" || utf8.RuneCountInString(name) > maxNameLen {
		return entity.Byline{}, fmt.Errorf("%w: byline must be 1 to %d characters", author.ErrInvalid, maxNameLen)
	}
	company, err := b.companyName(ctx)
	if err != nil {
		return entity.Byline{}, err
	}
	if company != "" && strings.EqualFold(company, name) {
		return entity.Byline{Org: true, Name: company}, nil
	}
	var id uuid.UUID
	var found string
	err = b.db.QueryRow(ctx, `
		SELECT e.id, e.name FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person'
		WHERE e.slug = $1 OR lower(btrim(e.name)) = lower($2)
		ORDER BY (e.slug = $1) DESC, e.created_at, e.id LIMIT 1`, slug, name).Scan(&id, &found)
	if err == nil {
		return entity.Byline{PersonID: &id, Name: found}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return entity.Byline{}, err
	}
	id, err = b.create(ctx, slug, name)
	if err != nil {
		return entity.Byline{}, err
	}
	return entity.Byline{PersonID: &id, Name: name}, nil
}

// create makes a non-public person without a login. A slug taken by another
// entity gets -2, -3, ... appended.
func (b *Byline) create(ctx context.Context, slug, name string) (uuid.UUID, error) {
	for n := 1; ; n++ {
		try := slug
		if n > 1 {
			try = fmt.Sprintf("%s-%d", slug, n)
		}
		var taken bool
		if err := b.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_entities WHERE slug = $1 AND parent_id IS NULL)`, try).Scan(&taken); err != nil {
			return uuid.Nil, err
		}
		if taken {
			continue
		}
		var id uuid.UUID
		err := b.db.QueryRow(ctx, `
			INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
			SELECT s.id, $2, $1, 'draft', '{"public": false}'::jsonb, NULL
			FROM hatamoto_entity_schemas s WHERE s.slug = 'person'
			RETURNING id`, try, name).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("the person content type is missing")
		}
		return id, err
	}
}

// Name implements entity.BylineResolver.
func (b *Byline) Name(ctx context.Context, personID *uuid.UUID) (string, error) {
	if personID == nil {
		return b.companyName(ctx)
	}
	var name string
	err := b.db.QueryRow(ctx, `SELECT name FROM hatamoto_entities WHERE id = $1`, *personID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entity.ErrAuthorNotFound
	}
	return name, err
}

// Check implements entity.BylineResolver.
func (b *Byline) Check(ctx context.Context, id uuid.UUID) error {
	var ok bool
	err := b.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person' WHERE e.id = $1)`, id).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return entity.ErrAuthorNotFound
	}
	return nil
}

// Mirror implements entity.BylineResolver: it makes the mirror row when the
// person has none and brings its name up to date.
func (b *Byline) Mirror(ctx context.Context, personID uuid.UUID) (*uuid.UUID, error) {
	var slug, name string
	var legacy *uuid.UUID
	err := b.db.QueryRow(ctx, `
		SELECT slug, name, NULLIF(metadata->>'legacy_author_id', '')::uuid
		FROM hatamoto_entities WHERE id = $1`, personID).Scan(&slug, &name, &legacy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, entity.ErrAuthorNotFound
	}
	if err != nil {
		return nil, err
	}
	if legacy != nil {
		tag, err := b.db.Exec(ctx, `UPDATE hatamoto_authors SET name = $2, updated_at = now() WHERE id = $1`, *legacy, name)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 1 {
			return legacy, nil
		}
	}
	// No (or a deleted) mirror: take the author with this slug, or make one.
	var id uuid.UUID
	err = b.db.QueryRow(ctx, `
		INSERT INTO hatamoto_authors (slug, name) VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, updated_at = now()
		RETURNING id`, slug, name).Scan(&id)
	if err != nil {
		return nil, err
	}
	_, err = b.db.Exec(ctx, `
		UPDATE hatamoto_entities SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{legacy_author_id}', to_jsonb($2::text))
		WHERE id = $1`, personID, id.String())
	return &id, err
}

// PersonChanged implements entity.BylineResolver: the mirror follows the
// person, and the plain-text byline of their posts follows the name.
func (b *Byline) PersonChanged(ctx context.Context, personID uuid.UUID) error {
	mirror, err := b.Mirror(ctx, personID)
	if err != nil {
		return err
	}
	for _, key := range []string{"author", "author_name"} {
		if _, err := b.db.Exec(ctx, `
			UPDATE hatamoto_entities x SET attributes = jsonb_set(x.attributes, ARRAY[$2::text], to_jsonb(p.name))
			FROM hatamoto_entities p
			WHERE p.id = $1 AND x.byline_person_id = $1
			  AND x.attributes ? $2::text
			  AND x.attributes->>$2::text IS DISTINCT FROM p.name`, personID, key); err != nil {
			return err
		}
	}
	_, err = b.db.Exec(ctx, `UPDATE hatamoto_entities SET author_id = $2 WHERE byline_person_id = $1 AND author_id IS DISTINCT FROM $2`, personID, mirror)
	return err
}

// PersonDeleting implements entity.BylineResolver. The mirror author row is
// kept (rollback); the posts keep their plain-text byline.
func (b *Byline) PersonDeleting(ctx context.Context, personID uuid.UUID) error {
	_, err := b.db.Exec(ctx, `UPDATE hatamoto_entities SET byline_person_id = NULL WHERE byline_person_id = $1`, personID)
	return err
}
