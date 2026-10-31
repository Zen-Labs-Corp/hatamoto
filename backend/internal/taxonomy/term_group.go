package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// Term locales and translation groups are an admin-only concern: the locale
// and group live in hatamoto_terms.locale and .translation_group, and only
// TermGroupStore reads or writes them. Term, termCols and every public query
// stay as they were, so public JSON and routes do not change.

var (
	// ErrTermGroupInvalid is returned when the terms cannot form or keep one
	// translation group.
	ErrTermGroupInvalid = errors.New("taxonomy: invalid term translation group")
	// ErrTermLocaleTaken is returned when a group already has a term in the
	// locale the caller wants to add. It matches ErrTermGroupInvalid too.
	ErrTermLocaleTaken = fmt.Errorf("%w: locale already used in this group", ErrTermGroupInvalid)
	// ErrTermLocaleUnset is returned when a term without a locale is copied.
	ErrTermLocaleUnset = errors.New("taxonomy: set the term's locale before copying it")
)

// termLocaleShared is the "undetermined" tag, never written as a term locale.
const termLocaleShared = "und"

// TermMember is one term of a translation group, or a link candidate.
type TermMember struct {
	ID     uuid.UUID
	Kind   string
	Locale string
	Name   string
	Slug   string
}

// TermGroupStore reads and writes the locale and translation group of terms.
type TermGroupStore struct {
	db *database.DB
}

// NewTermGroupStore returns a TermGroupStore on the given database.
func NewTermGroupStore(db *database.DB) *TermGroupStore { return &TermGroupStore{db: db} }

// withGroupTx runs fn in a transaction holding the term group lock. Concurrent
// group writes serialize on it, since row locks alone cannot stop two callers
// adding the same locale to one group through different rows.
func (s *TermGroupStore) withGroupTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return s.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('hm_term_translation_group'))`); err != nil {
			return fmt.Errorf("lock term groups: %w", err)
		}
		return fn(tx)
	})
}

func cleanLocale(locale string) (string, error) {
	locale = strings.TrimSpace(locale)
	if strings.EqualFold(locale, termLocaleShared) {
		return "", fmt.Errorf("%w: und is not a term locale", ErrTermGroupInvalid)
	}
	return locale, nil
}

// Locale returns the term's locale, "" meaning every locale.
func (s *TermGroupStore) Locale(ctx context.Context, id uuid.UUID) (string, error) {
	var locale string
	err := s.db.Pool().QueryRow(ctx, `SELECT locale FROM hatamoto_terms WHERE id = $1`, id).Scan(&locale)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTermNotFound
	}
	if err != nil {
		return "", fmt.Errorf("term locale: %w", err)
	}
	return locale, nil
}

// SetLocale sets the term's locale ("" = every locale). A term that shares a
// group with others must keep a locale no other member uses.
func (s *TermGroupStore) SetLocale(ctx context.Context, id uuid.UUID, locale string) error {
	locale, err := cleanLocale(locale)
	if err != nil {
		return err
	}
	return s.withGroupTx(ctx, func(tx pgx.Tx) error {
		var group *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT translation_group FROM hatamoto_terms WHERE id = $1 FOR UPDATE`, id).Scan(&group); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTermNotFound
			}
			return fmt.Errorf("load term: %w", err)
		}
		if group != nil {
			var others int
			var taken bool
			if err := tx.QueryRow(ctx, `
				SELECT count(*), COALESCE(bool_or(locale = $3), false)
				FROM hatamoto_terms WHERE translation_group = $1 AND id <> $2`, *group, id, locale).Scan(&others, &taken); err != nil {
				return fmt.Errorf("load group: %w", err)
			}
			if others > 0 && locale == "" {
				return fmt.Errorf("%w: a linked term needs a locale", ErrTermGroupInvalid)
			}
			if taken {
				return ErrTermLocaleTaken
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_terms SET locale = $2 WHERE id = $1`, id, locale); err != nil {
			return fmt.Errorf("set term locale: %w", err)
		}
		return nil
	})
}

// Link puts the terms in one translation group and returns its id. An existing
// group among them is reused (its members count towards the rules); with none
// a new one is created. Rules: at least two ids, one kind, a distinct non-empty
// locale each, at most one existing group.
func (s *TermGroupStore) Link(ctx context.Context, ids ...uuid.UUID) (uuid.UUID, error) {
	if len(ids) < 2 {
		return uuid.Nil, fmt.Errorf("%w: need at least two terms", ErrTermGroupInvalid)
	}
	var result uuid.UUID
	err := s.withGroupTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, kind, locale, translation_group
			FROM hatamoto_terms WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
		if err != nil {
			return fmt.Errorf("load terms: %w", err)
		}
		type row struct {
			kind, locale string
			group        *uuid.UUID
		}
		found := map[uuid.UUID]row{}
		for rows.Next() {
			var id uuid.UUID
			var r row
			if err := rows.Scan(&id, &r.kind, &r.locale, &r.group); err != nil {
				rows.Close()
				return fmt.Errorf("scan term: %w", err)
			}
			found[id] = r
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("load terms: %w", err)
		}

		var group *uuid.UUID
		var kind string
		locales := map[string]bool{}
		for _, id := range ids {
			r, ok := found[id]
			if !ok {
				return fmt.Errorf("%w: term %s not found", ErrTermGroupInvalid, id)
			}
			if r.locale == "" || strings.EqualFold(r.locale, termLocaleShared) {
				return fmt.Errorf("%w: every term needs a locale", ErrTermGroupInvalid)
			}
			if kind == "" {
				kind = r.kind
			} else if kind != r.kind {
				return fmt.Errorf("%w: terms of different kinds", ErrTermGroupInvalid)
			}
			if locales[r.locale] {
				return ErrTermLocaleTaken
			}
			locales[r.locale] = true
			if r.group != nil {
				if group != nil && *group != *r.group {
					return fmt.Errorf("%w: terms are in different groups", ErrTermGroupInvalid)
				}
				group = r.group
			}
		}
		if group != nil {
			// Members of the reused group that are not in ids count too.
			mrows, err := tx.Query(ctx, `
				SELECT locale FROM hatamoto_terms WHERE translation_group = $1 AND NOT (id = ANY($2))`, *group, ids)
			if err != nil {
				return fmt.Errorf("load group: %w", err)
			}
			for mrows.Next() {
				var loc string
				if err := mrows.Scan(&loc); err != nil {
					mrows.Close()
					return fmt.Errorf("scan group: %w", err)
				}
				if locales[loc] {
					mrows.Close()
					return ErrTermLocaleTaken
				}
				locales[loc] = true
			}
			mrows.Close()
			if err := mrows.Err(); err != nil {
				return fmt.Errorf("load group: %w", err)
			}
		} else {
			g := uuid.New()
			group = &g
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_terms SET translation_group = $2 WHERE id = ANY($1)`, ids, *group); err != nil {
			return fmt.Errorf("link terms: %w", err)
		}
		result = *group
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return result, nil
}

// Unlink takes the term out of its group, which is dissolved when one member
// is left. The term keeps its locale. A term without a group is a no-op.
func (s *TermGroupStore) Unlink(ctx context.Context, id uuid.UUID) error {
	return s.withGroupTx(ctx, func(tx pgx.Tx) error {
		var group *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT translation_group FROM hatamoto_terms WHERE id = $1 FOR UPDATE`, id).Scan(&group); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTermNotFound
			}
			return fmt.Errorf("load term: %w", err)
		}
		if group == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_terms SET translation_group = NULL WHERE id = $1`, id); err != nil {
			return fmt.Errorf("unlink term: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE hatamoto_terms SET translation_group = NULL
			WHERE translation_group = $1
			  AND (SELECT count(*) FROM hatamoto_terms WHERE translation_group = $1) <= 1`, *group); err != nil {
			return fmt.Errorf("dissolve group: %w", err)
		}
		return nil
	})
}

// Members returns the terms in the term's group, itself included, ordered by
// locale. It is nil when the term has no group or is alone in it.
func (s *TermGroupStore) Members(ctx context.Context, id uuid.UUID) ([]TermMember, error) {
	rows, err := s.db.Pool().Query(ctx, `
		SELECT m.id, m.kind, m.locale, m.name, m.slug
		FROM hatamoto_terms t JOIN hatamoto_terms m ON m.translation_group = t.translation_group
		WHERE t.id = $1 ORDER BY m.locale, m.slug`, id)
	if err != nil {
		return nil, fmt.Errorf("term members: %w", err)
	}
	defer rows.Close()
	var out []TermMember
	for rows.Next() {
		var m TermMember
		if err := rows.Scan(&m.ID, &m.Kind, &m.Locale, &m.Name, &m.Slug); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("term members: %w", err)
	}
	if len(out) <= 1 {
		return nil, nil
	}
	return out, nil
}

// MapForLocale returns, for each term id, the member of its group in locale,
// else the id itself, without duplicates and in the order of ids.
func (s *TermGroupStore) MapForLocale(ctx context.Context, termIDs []uuid.UUID, locale string) ([]uuid.UUID, error) {
	if len(termIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Pool().Query(ctx, `
		SELECT t.id, m.id
		FROM hatamoto_terms t JOIN hatamoto_terms m
		  ON m.translation_group = t.translation_group AND m.locale = $2
		WHERE t.id = ANY($1)`, termIDs, locale)
	if err != nil {
		return nil, fmt.Errorf("map terms: %w", err)
	}
	defer rows.Close()
	copies := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var from, to uuid.UUID
		if err := rows.Scan(&from, &to); err != nil {
			return nil, fmt.Errorf("scan term map: %w", err)
		}
		copies[from] = to
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("map terms: %w", err)
	}
	out := make([]uuid.UUID, 0, len(termIDs))
	seen := map[uuid.UUID]bool{}
	for _, id := range termIDs {
		if to, ok := copies[id]; ok {
			id = to
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// Candidates lists terms the term can be linked to: same kind, a locale set
// and not used by its group, not in a group, matching q on name or slug.
func (s *TermGroupStore) Candidates(ctx context.Context, id uuid.UUID, q string, limit int) ([]TermMember, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(q)) + "%"
	rows, err := s.db.Pool().Query(ctx, `
		SELECT c.id, c.kind, c.locale, c.name, c.slug
		FROM hatamoto_terms t JOIN hatamoto_terms c ON c.kind = t.kind
		WHERE t.id = $1 AND c.id <> t.id
		  AND c.locale <> '' AND c.locale <> 'und' AND c.translation_group IS NULL
		  AND c.locale <> t.locale
		  AND (c.name ILIKE $2 OR c.slug ILIKE $2)
		  AND NOT EXISTS (SELECT 1 FROM hatamoto_terms g
		                  WHERE t.translation_group IS NOT NULL
		                    AND g.translation_group = t.translation_group AND g.locale = c.locale)
		ORDER BY c.name, c.id LIMIT $3`, id, like, limit)
	if err != nil {
		return nil, fmt.Errorf("term candidates: %w", err)
	}
	defer rows.Close()
	var out []TermMember
	for rows.Next() {
		var m TermMember
		if err := rows.Scan(&m.ID, &m.Kind, &m.Locale, &m.Name, &m.Slug); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetGroupStore installs the store CreateTermCopy needs.
func (s *Service) SetGroupStore(g *TermGroupStore) { s.groups = g }

// CreateTermCopy creates a term of the source's kind in locale and links it to
// the source. A blank name defaults to the source's. The parent is the
// parent's copy in that locale, else none. The source needs a locale of its
// own. A failed locale set or link deletes the new term again.
func (s *Service) CreateTermCopy(ctx context.Context, srcID uuid.UUID, locale, name, slug string) (*Term, error) {
	if s.groups == nil {
		return nil, errors.New("taxonomy: term groups are not configured")
	}
	locale, err := cleanLocale(locale)
	if err != nil {
		return nil, err
	}
	if locale == "" {
		return nil, fmt.Errorf("%w: a copy needs a locale", ErrTermGroupInvalid)
	}
	src, err := s.repo.GetTerm(ctx, srcID)
	if err != nil {
		return nil, err
	}
	srcLocale, err := s.groups.Locale(ctx, srcID)
	if err != nil {
		return nil, err
	}
	if srcLocale == "" {
		return nil, ErrTermLocaleUnset
	}
	if srcLocale == locale {
		return nil, ErrTermLocaleTaken
	}
	members, err := s.groups.Members(ctx, srcID)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if m.Locale == locale {
			return nil, ErrTermLocaleTaken
		}
	}
	if strings.TrimSpace(name) == "" {
		name = src.Name
	}
	in := CreateTermInput{Kind: src.Kind, Name: name, Slug: slug, Description: src.Description}
	if src.ParentID != nil {
		mapped, err := s.groups.MapForLocale(ctx, []uuid.UUID{*src.ParentID}, locale)
		if err != nil {
			return nil, err
		}
		if len(mapped) == 1 && mapped[0] != *src.ParentID {
			in.ParentID = &mapped[0]
		}
	}
	t, err := s.CreateTerm(ctx, in)
	if err != nil {
		return nil, err
	}
	undo := func(cause error) (*Term, error) {
		if delErr := s.repo.DeleteTerm(ctx, t.ID); delErr != nil {
			s.logger.Warn("delete term after failed copy", "term", t.ID, "error", delErr)
		}
		s.notifyChanged(ctx)
		return nil, cause
	}
	if err := s.groups.SetLocale(ctx, t.ID, locale); err != nil {
		return undo(err)
	}
	if _, err := s.groups.Link(ctx, srcID, t.ID); err != nil {
		return undo(err)
	}
	return t, nil
}
