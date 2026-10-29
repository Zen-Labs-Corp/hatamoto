package entity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// localeUnd is the shared (language-neutral) locale; shared rows never join a
// translation group.
const localeUnd = "und"

// ErrInvalidGroup is returned by GroupStore.Link when the ids cannot form one
// translation group.
var ErrInvalidGroup = errors.New("invalid translation group")

// GroupStore reads and writes hatamoto_entities.translation_group, the link
// between the copies of one page in different locales. The column is kept off
// Entity on purpose; only this store touches it.
type GroupStore struct {
	db *pgxpool.Pool
}

// NewGroupStore returns a GroupStore on the given pool.
func NewGroupStore(db *pgxpool.Pool) *GroupStore { return &GroupStore{db: db} }

// Sibling is one published copy of a page in a translation group.
type Sibling struct {
	ID         uuid.UUID
	Locale     string
	Slug       string
	SchemaSlug string
	ParentID   *uuid.UUID
}

// GroupMember maps an entity to its group and locale (sitemap batches).
type GroupMember struct {
	ID     uuid.UUID
	Group  uuid.UUID
	Locale string
}

// Link puts ids in one translation group and returns its id. An existing
// group among the ids is reused (members of it count towards the rules); with
// none a new one is created. Rules: at least two ids, one row per locale,
// no shared (und) rows, one schema, at most one existing group.
func (s *GroupStore) Link(ctx context.Context, ids ...uuid.UUID) (uuid.UUID, error) {
	if len(ids) < 2 {
		return uuid.Nil, fmt.Errorf("%w: need at least two entities", ErrInvalidGroup)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin link: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, locale, schema_id, translation_group
		FROM hatamoto_entities WHERE id = ANY($1) FOR UPDATE`, ids)
	if err != nil {
		return uuid.Nil, fmt.Errorf("load entities: %w", err)
	}
	type row struct {
		locale string
		schema uuid.UUID
		group  *uuid.UUID
	}
	found := map[uuid.UUID]row{}
	for rows.Next() {
		var id uuid.UUID
		var r row
		if err := rows.Scan(&id, &r.locale, &r.schema, &r.group); err != nil {
			rows.Close()
			return uuid.Nil, fmt.Errorf("scan entity: %w", err)
		}
		found[id] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, fmt.Errorf("load entities: %w", err)
	}

	var group *uuid.UUID
	var schema uuid.UUID
	locales := map[string]bool{}
	for _, id := range ids {
		r, ok := found[id]
		if !ok {
			return uuid.Nil, fmt.Errorf("%w: entity %s not found", ErrInvalidGroup, id)
		}
		if r.locale == localeUnd {
			return uuid.Nil, fmt.Errorf("%w: entity %s is shared (%s)", ErrInvalidGroup, id, localeUnd)
		}
		if locales[r.locale] {
			return uuid.Nil, fmt.Errorf("%w: two entities in locale %s", ErrInvalidGroup, r.locale)
		}
		locales[r.locale] = true
		if schema != uuid.Nil && r.schema != schema {
			return uuid.Nil, fmt.Errorf("%w: entities use different schemas", ErrInvalidGroup)
		}
		schema = r.schema
		if r.group != nil {
			if group != nil && *group != *r.group {
				return uuid.Nil, fmt.Errorf("%w: entities belong to different groups", ErrInvalidGroup)
			}
			group = r.group
		}
	}

	if group == nil {
		g := uuid.New()
		group = &g
	} else {
		// Existing members outside ids must not clash with the new locales.
		var clash string
		err := tx.QueryRow(ctx, `
			SELECT locale FROM hatamoto_entities
			WHERE translation_group = $1 AND id <> ALL($2) AND locale = ANY($3) LIMIT 1`,
			*group, ids, localeKeys(locales)).Scan(&clash)
		if err == nil {
			return uuid.Nil, fmt.Errorf("%w: group already has locale %s", ErrInvalidGroup, clash)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("check group locales: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE hatamoto_entities SET translation_group = $1 WHERE id = ANY($2)`, *group, ids); err != nil {
		return uuid.Nil, fmt.Errorf("link entities: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit link: %w", err)
	}
	return *group, nil
}

func localeKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Unlink removes id from its translation group. A no-op when it has none.
func (s *GroupStore) Unlink(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `UPDATE hatamoto_entities SET translation_group = NULL WHERE id = $1`, id); err != nil {
		return fmt.Errorf("unlink entity: %w", err)
	}
	return nil
}

// GroupOf returns the group of id, or nil when it has none.
func (s *GroupStore) GroupOf(ctx context.Context, id uuid.UUID) (*uuid.UUID, error) {
	var g *uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT translation_group FROM hatamoto_entities WHERE id = $1`, id).Scan(&g)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("group of entity: %w", err)
	}
	return g, nil
}

// LiveSiblings returns the published copies in id's group (id itself
// included) whose locale is in tags, ordered by locale. The predicate matches
// the published reads in public_repository.go; locales are filtered by the
// explicit tag list, not by the request context.
func (s *GroupStore) LiveSiblings(ctx context.Context, id uuid.UUID, tags []string) ([]Sibling, error) {
	rows, err := s.db.Query(ctx, `
		SELECT e.id, e.locale, e.slug, s.slug, e.parent_id
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE e.translation_group = (SELECT translation_group FROM hatamoto_entities WHERE id = $1)
		  AND e.status = 'published' AND e.variant_key IS NULL
		  AND e.locale = ANY($2)
		ORDER BY e.locale`, id, tags)
	if err != nil {
		return nil, fmt.Errorf("list siblings: %w", err)
	}
	defer rows.Close()
	var out []Sibling
	for rows.Next() {
		var sb Sibling
		if err := rows.Scan(&sb.ID, &sb.Locale, &sb.Slug, &sb.SchemaSlug, &sb.ParentID); err != nil {
			return nil, fmt.Errorf("scan sibling: %w", err)
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

// GroupsFor returns group and locale for those of ids that are in a group,
// keyed by entity id.
func (s *GroupStore) GroupsFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]GroupMember, error) {
	out := map[uuid.UUID]GroupMember{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, translation_group, locale FROM hatamoto_entities
		WHERE id = ANY($1) AND translation_group IS NOT NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("groups for entities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.ID, &m.Group, &m.Locale); err != nil {
			return nil, fmt.Errorf("scan group member: %w", err)
		}
		out[m.ID] = m
	}
	return out, rows.Err()
}
