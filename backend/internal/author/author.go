// Package author is the legacy authors table, kept for rollback. It is NOT the
// source of bylines any more: people are entities of the built-in `person`
// type (internal/person) and a post credits one through byline_person_id.
//
// What remains here: the Store the person resolver uses to keep a mirror row
// per person (so the previous binary still renders bylines after a rollback),
// the slug helpers (Slugify, ValidSlug) and the deprecated /api/v1/authors
// handlers. Rows are never read to render a page, a feed or JSON-LD. The table
// and entities.author_id stay until a contract release drops them
// (docs/UPGRADES.md).
package author

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"hatamoto.dev/core/internal/database"
)

// Errors.
var (
	ErrNotFound     = errors.New("author not found")
	ErrSlugConflict = errors.New("author slug already exists")
	ErrInvalid      = errors.New("invalid author")
)

// Limits mirroring the columns.
const (
	MaxNameLen = 255
	MaxSlugLen = 128
	MaxBioLen  = 5000
)

// Author is a byline.
type Author struct {
	ID            uuid.UUID  `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Bio           string     `json:"bio"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
	// AvatarURL is the public URL of the avatar media row, "" without one.
	AvatarURL string    `json:"avatar_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is the writable part of an author. Slug defaults to the slug of Name.
type Input struct {
	Name          string     `json:"name"`
	Slug          string     `json:"slug,omitempty"`
	Bio           string     `json:"bio"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
}

// Slugify makes a slug of a name: lower-case ASCII letters and digits, runs of
// anything else as one hyphen, at most MaxSlugLen. Non-ASCII letters and digits
// are dropped, which is the rule the public byline page has always used, so
// existing /author/{slug} URLs do not change.
//
// A name with no ASCII letter or digit left (李明, Ελένη, 🙂) never gives "":
// accents are stripped first where Unicode allows ("Ñ" is "n"), and otherwise
// the slug is "author-" and eight hex digits of a hash of the lower-cased name,
// so the same name always lands on the same author. Only a blank name gives "".
func Slugify(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if s := slugifyASCII(name); s != "" {
		return s
	}
	if plain, _, err := transform.String(stripMarks(), name); err == nil {
		if s := slugifyASCII(plain); s != "" {
			return s
		}
	}
	sum := sha256.Sum256([]byte(strings.ToLower(name)))
	return "author-" + hex.EncodeToString(sum[:4])
}

// stripMarks decomposes accented letters and drops the accents. A transformer
// keeps state, so each call needs its own: a shared one panics when two
// requests slugify at once.
func stripMarks() transform.Transformer {
	return transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
}

func slugifyASCII(name string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.TrimSpace(strings.ToLower(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if r > 127 {
				continue
			}
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > MaxSlugLen {
		s = strings.TrimRight(s[:MaxSlugLen], "-")
	}
	return s
}

// ValidSlug reports whether s is a well-formed author slug.
func ValidSlug(s string) bool { return s != "" && len(s) <= MaxSlugLen && Slugify(s) == s }

// Store is the persistence of authors.
type Store struct{ db *database.DB }

// NewStore returns a store over db.
func NewStore(db *database.DB) *Store { return &Store{db: db} }

const selectAuthor = `
	SELECT a.id, a.slug, a.name, a.bio, a.avatar_media_id, COALESCE(m.public_url, ''), a.created_at, a.updated_at
	FROM hatamoto_authors a LEFT JOIN hatamoto_media m ON m.id = a.avatar_media_id`

func scan(row pgx.Row) (*Author, error) {
	var a Author
	if err := row.Scan(&a.ID, &a.Slug, &a.Name, &a.Bio, &a.AvatarMediaID, &a.AvatarURL, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// normalise trims and checks the input. A missing slug becomes keepSlug (an
// update keeps the stored one, so renaming does not move /author/{slug}), else
// the slug of the name.
func (in *Input) normalise(keepSlug string) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Bio = strings.TrimSpace(in.Bio)
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	if in.Slug == "" {
		in.Slug = keepSlug
	}
	if in.Slug == "" {
		in.Slug = Slugify(in.Name)
	}
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > MaxNameLen:
		return fmt.Errorf("%w: name is required (at most %d characters)", ErrInvalid, MaxNameLen)
	case !ValidSlug(in.Slug):
		return fmt.Errorf("%w: slug must be lower-case letters, digits and hyphens", ErrInvalid)
	case len(in.Bio) > MaxBioLen:
		return fmt.Errorf("%w: bio is longer than %d characters", ErrInvalid, MaxBioLen)
	}
	return nil
}

func mapWriteErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return ErrSlugConflict
		case "23503":
			return fmt.Errorf("%w: avatar media not found", ErrInvalid)
		}
	}
	return err
}

// Create stores a new author.
func (s *Store) Create(ctx context.Context, in Input) (*Author, error) {
	if err := in.normalise(""); err != nil {
		return nil, err
	}
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	var id uuid.UUID
	if err := conn.QueryRow(ctx, `INSERT INTO hatamoto_authors (slug, name, bio, avatar_media_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		in.Slug, in.Name, in.Bio, in.AvatarMediaID).Scan(&id); err != nil {
		return nil, mapWriteErr(err)
	}
	return s.Get(ctx, id)
}

// Get returns an author by id.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Author, error) {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	return scan(conn.QueryRow(ctx, selectAuthor+` WHERE a.id = $1`, id))
}

// GetBySlug returns an author by slug.
func (s *Store) GetBySlug(ctx context.Context, slug string) (*Author, error) {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	return scan(conn.QueryRow(ctx, selectAuthor+` WHERE a.slug = $1`, slug))
}

// List returns every author by name.
func (s *Store) List(ctx context.Context) ([]*Author, error) {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, selectAuthor+` ORDER BY a.name, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Author{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Update replaces an author's fields. A renamed author keeps its entities; the
// plain-text byline stored in their attributes follows the new name.
func (s *Store) Update(ctx context.Context, id uuid.UUID, in Input) (*Author, error) {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var old, oldSlug string
	if err := tx.QueryRow(ctx, `SELECT name, slug FROM hatamoto_authors WHERE id = $1 FOR UPDATE`, id).Scan(&old, &oldSlug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := in.normalise(oldSlug); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_authors SET slug = $2, name = $3, bio = $4, avatar_media_id = $5, updated_at = now() WHERE id = $1`,
		id, in.Slug, in.Name, in.Bio, in.AvatarMediaID); err != nil {
		return nil, mapWriteErr(err)
	}
	if old != in.Name {
		// Keep the plain-text byline (JSON-LD, exports) in step with the row.
		for _, key := range []string{"author", "author_name"} {
			if _, err := tx.Exec(ctx, `
				UPDATE hatamoto_entities SET attributes = jsonb_set(attributes, ARRAY[$3::text], to_jsonb($2::text))
				WHERE author_id = $1 AND attributes ? $3::text`, id, in.Name, key); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete removes an author and its /author/{slug} page. Its entities lose the
// reference but keep the plain-text byline in their attributes (it is content),
// so saving one of them again recreates the author; clear the byline on the
// entity to remove it for good.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	tag, err := conn.Exec(ctx, `DELETE FROM hatamoto_authors WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// EnsureByName returns the id of the author whose slug is the slug of name,
// creating the author when there is none. Only blank or over-long names are
// invalid; a name without ASCII letters gets a hash slug (see Slugify).
func (s *Store) EnsureByName(ctx context.Context, name string) (uuid.UUID, error) {
	name = strings.TrimSpace(name)
	slug := Slugify(name)
	if slug == "" || utf8.RuneCountInString(name) > MaxNameLen {
		return uuid.Nil, fmt.Errorf("%w: author name must be 1 to %d characters", ErrInvalid, MaxNameLen)
	}
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer conn.Release()
	var id uuid.UUID
	err = conn.QueryRow(ctx, `
		INSERT INTO hatamoto_authors (slug, name) VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET slug = EXCLUDED.slug
		RETURNING id`, slug, name).Scan(&id)
	return id, err
}

// Name returns an author's name.
func (s *Store) Name(ctx context.Context, id uuid.UUID) (string, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return a.Name, nil
}

// ForEntities returns the author of each given entity id (entities without one
// are absent).
func (s *Store) ForEntities(ctx context.Context, entityIDs []uuid.UUID) (map[uuid.UUID]*Author, error) {
	out := map[uuid.UUID]*Author{}
	if len(entityIDs) == 0 {
		return out, nil
	}
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		SELECT e.id, a.id, a.slug, a.name, a.bio, a.avatar_media_id, COALESCE(m.public_url, ''), a.created_at, a.updated_at
		FROM hatamoto_entities e
		JOIN hatamoto_authors a ON a.id = e.author_id
		LEFT JOIN hatamoto_media m ON m.id = a.avatar_media_id
		WHERE e.id = ANY($1)`, entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var eid uuid.UUID
		var a Author
		if err := rows.Scan(&eid, &a.ID, &a.Slug, &a.Name, &a.Bio, &a.AvatarMediaID, &a.AvatarURL, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out[eid] = &a
	}
	return out, rows.Err()
}
