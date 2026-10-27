package person

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors.
var (
	ErrNotFound = errors.New("person not found")
	// ErrAlreadyLinked means the user already owns a person, or the person
	// already belongs to another user.
	ErrAlreadyLinked = errors.New("user or person already linked")
)

// DB is the slice of pgx the store needs; *pgxpool.Pool and pgx.Tx satisfy it,
// so the same store works inside a migration or import transaction.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store reads people and their user links.
type Store struct{ db DB }

// NewStore returns a store over a pool or transaction.
func NewStore(db DB) *Store { return &Store{db: db} }

// NewPoolStore is NewStore for a pool.
func NewPoolStore(pool *pgxpool.Pool) *Store { return &Store{db: pool} }

const selectPerson = `
	SELECT e.id, e.slug, e.name, e.attributes, e.published_at IS NOT NULL
	FROM hatamoto_entities e
	JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person'`

func scan(row pgx.Row) (*Person, error) {
	var (
		id         uuid.UUID
		slug, name string
		attrs      []byte
		fixed      bool
	)
	if err := row.Scan(&id, &slug, &name, &attrs, &fixed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p := fromAttributes(id, slug, name, attrs)
	p.SlugFixed = fixed
	return p, nil
}

func (s *Store) query(ctx context.Context, where string, args ...any) ([]*Person, error) {
	rows, err := s.db.Query(ctx, selectPerson+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Person{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SchemaID returns the id of the person content type.
func (s *Store) SchemaID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'person'`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errors.New("the person content type is missing")
	}
	return id, err
}

// Get returns a person by entity id.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Person, error) {
	return scan(s.db.QueryRow(ctx, selectPerson+` WHERE e.id = $1`, id))
}

// GetBySlug returns a person by slug.
func (s *Store) GetBySlug(ctx context.Context, slug string) (*Person, error) {
	return scan(s.db.QueryRow(ctx, selectPerson+` WHERE e.slug = $1`, slug))
}

// ByIDs returns the given people in one query, keyed by id. Unknown ids are
// absent. Use it to resolve the bylines of a whole listing.
func (s *Store) ByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*Person, error) {
	out := map[uuid.UUID]*Person{}
	if len(ids) == 0 {
		return out, nil
	}
	list, err := s.query(ctx, ` WHERE e.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		out[p.ID] = p
	}
	return out, nil
}

// List returns every person by name.
func (s *Store) List(ctx context.Context) ([]*Person, error) {
	return s.query(ctx, ` ORDER BY e.name, e.id`)
}

// PostCounts returns how many published entities each person is credited on.
// People without any are absent.
func (s *Store) PostCounts(ctx context.Context) (map[uuid.UUID]int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT byline_person_id, count(*) FROM hatamoto_entities
		WHERE byline_person_id IS NOT NULL AND status = 'published'
		GROUP BY byline_person_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// LinkedUsers returns the user id that owns each person; people without a
// login are absent.
func (s *Store) LinkedUsers(ctx context.Context) (map[uuid.UUID]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT entity_id, user_id FROM hatamoto_user_people`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var person, user uuid.UUID
		if err := rows.Scan(&person, &user); err != nil {
			return nil, err
		}
		out[person] = user
	}
	return out, rows.Err()
}

// LinkedUser returns the user that owns a person, or ErrNotFound for a person
// without a login.
func (s *Store) LinkedUser(ctx context.Context, personID uuid.UUID) (uuid.UUID, error) {
	var user uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT user_id FROM hatamoto_user_people WHERE entity_id = $1`, personID).Scan(&user)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return user, err
}

// ForUser returns the person a user owns, or ErrNotFound when the user has none yet.
func (s *Store) ForUser(ctx context.Context, userID uuid.UUID) (*Person, error) {
	return scan(s.db.QueryRow(ctx, selectPerson+`
		JOIN hatamoto_user_people up ON up.entity_id = e.id WHERE up.user_id = $1`, userID))
}

// Link makes personID the person of userID. It returns ErrAlreadyLinked when
// the user already owns a person or the person already has a login, and
// ErrNotFound when personID is not a person entity.
func (s *Store) Link(ctx context.Context, userID, personID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_user_people (user_id, entity_id)
		SELECT $1, e.id FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person'
		WHERE e.id = $2`, userID, personID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrAlreadyLinked
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Credit is who an entity credits: a person, the site organisation, or (both
// zero) nobody.
type Credit struct {
	PersonID *uuid.UUID
	Org      bool
}

// Credits returns the byline of each entity in one query. Entities without a
// person or organisation byline are absent.
func (s *Store) Credits(ctx context.Context, entityIDs []uuid.UUID) (map[uuid.UUID]Credit, error) {
	out := map[uuid.UUID]Credit{}
	if len(entityIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, byline_person_id, byline_org FROM hatamoto_entities
		WHERE id = ANY($1) AND (byline_person_id IS NOT NULL OR byline_org)`, entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var c Credit
		if err := rows.Scan(&id, &c.PersonID, &c.Org); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}
