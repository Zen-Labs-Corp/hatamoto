package entity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Member is one copy in a translation group, in any status.
type Member struct {
	ID       uuid.UUID
	Group    uuid.UUID
	SchemaID uuid.UUID
	Locale   string
	Slug     string
	Name     string
	Status   string
	// Original is the stored flag. Use OriginalOf to find the effective
	// original: a group may have no flagged member.
	Original  bool
	CreatedAt time.Time
	UpdatedAt time.Time
	// ContentChangedAt is the last time the copy's attributes changed: the
	// newest entity version row, else its creation. Version rows exist only
	// for attribute changes, so rename-only edits do not move it, and neither
	// does a publish or a link.
	ContentChangedAt time.Time
}

// Candidate is an entity that can be linked into a group.
type Candidate struct {
	ID     uuid.UUID
	Locale string
	Slug   string
	Name   string
}

const memberSelect = `
	SELECT e.id, e.translation_group, e.schema_id, e.locale, e.slug, e.name, e.status,
	       e.translation_original, e.created_at, e.updated_at,
	       GREATEST(e.created_at, COALESCE(v.last_edit, e.created_at))
	FROM hatamoto_entities e
	LEFT JOIN LATERAL (
		SELECT max(created_at) AS last_edit FROM hatamoto_entity_versions WHERE entity_id = e.id
	) v ON true`

// Members returns every member of id's group, id included, in any status,
// ordered by locale. It returns nil when id has no group.
func (s *GroupStore) Members(ctx context.Context, id uuid.UUID) ([]Member, error) {
	rows, err := s.db.Query(ctx, memberSelect+`
		WHERE e.translation_group = (SELECT translation_group FROM hatamoto_entities WHERE id = $1)
		  AND e.variant_key IS NULL
		ORDER BY e.locale, e.id`, id)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		m, err := scanMember(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MembersFor returns the members of each of the given groups, keyed by group
// and ordered by locale.
func (s *GroupStore) MembersFor(ctx context.Context, groups []uuid.UUID) (map[uuid.UUID][]Member, error) {
	out := map[uuid.UUID][]Member{}
	if len(groups) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, memberSelect+`
		WHERE e.translation_group = ANY($1) AND e.variant_key IS NULL
		ORDER BY e.locale, e.id`, groups)
	if err != nil {
		return nil, fmt.Errorf("list members of groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMember(rows.Scan)
		if err != nil {
			return nil, err
		}
		out[m.Group] = append(out[m.Group], m)
	}
	return out, rows.Err()
}

func scanMember(scan func(...any) error) (Member, error) {
	var m Member
	if err := scan(&m.ID, &m.Group, &m.SchemaID, &m.Locale, &m.Slug, &m.Name, &m.Status,
		&m.Original, &m.CreatedAt, &m.UpdatedAt, &m.ContentChangedAt); err != nil {
		return Member{}, fmt.Errorf("scan group member: %w", err)
	}
	return m, nil
}

// LinkCandidates lists entities that Link would accept together with id: same
// schema, no group yet, not shared, and a locale that id's group (or id
// itself, when it has no group) does not use. q matches name or slug.
func (s *GroupStore) LinkCandidates(ctx context.Context, id uuid.UUID, q string, limit int) ([]Candidate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.locale, c.slug, c.name
		FROM hatamoto_entities c, hatamoto_entities me
		WHERE me.id = $1
		  AND c.schema_id = me.schema_id AND c.id <> me.id
		  AND c.translation_group IS NULL AND c.variant_key IS NULL
		  AND c.locale <> $2
		  AND c.locale NOT IN (
		      SELECT g.locale FROM hatamoto_entities g
		      WHERE g.id = me.id
		         OR (me.translation_group IS NOT NULL AND g.translation_group = me.translation_group))
		  AND ($3 = '' OR c.name ILIKE '%' || $3 || '%' OR c.slug ILIKE '%' || $3 || '%')
		ORDER BY c.name, c.locale
		LIMIT $4`, id, LocaleShared, escapeLike(q), limit)
	if err != nil {
		return nil, fmt.Errorf("list link candidates: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.ID, &c.Locale, &c.Slug, &c.Name); err != nil {
			return nil, fmt.Errorf("scan link candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// escapeLike makes q literal inside a LIKE pattern.
func escapeLike(q string) string {
	out := make([]rune, 0, len(q))
	for _, r := range q {
		if r == '\\' || r == '%' || r == '_' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

// OriginalOf picks the original of a group's members: the flagged one, else
// the default-locale one, else the oldest. The flag counts only for members
// that are in a group (Group set). It returns nil for no members.
func OriginalOf(ms []Member, defaultLocale string) *Member {
	if len(ms) == 0 {
		return nil
	}
	for i := range ms {
		if ms[i].Original && ms[i].Group != uuid.Nil {
			return &ms[i]
		}
	}
	for i := range ms {
		if ms[i].Locale == defaultLocale {
			return &ms[i]
		}
	}
	oldest := ms[0]
	idx := 0
	for i, m := range ms {
		if m.CreatedAt.Before(oldest.CreatedAt) {
			oldest, idx = m, i
		}
	}
	return &ms[idx]
}

// OutOfDate reports whether copy m was last edited strictly before the
// original. The original itself is never out of date.
func OutOfDate(m, orig Member) bool {
	return m.ID != orig.ID && m.ContentChangedAt.Before(orig.ContentChangedAt)
}
