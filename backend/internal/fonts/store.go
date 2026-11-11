// Package fonts stores the fonts an owner uploaded or downloaded once from
// Google Fonts, validates them, and serves them from the site itself.
package fonts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/database"
)

// ErrNotFound reports an unknown font file hash.
var ErrNotFound = errors.New("fonts: not found")

// ErrTooMany reports an insert that would pass MaxFonts stored files.
var ErrTooMany = errors.New("fonts: too many font files")

// Font is one stored face. List leaves the bytes out; Size is their length.
type Font struct {
	ID           uuid.UUID
	Family       string
	Weight       int
	Style        string
	Source       string
	Subset       string
	UnicodeRange string
	Hash         string
	Size         int
	CreatedAt    time.Time
}

// NewFont is a face to store.
type NewFont struct {
	Family       string
	Weight       int
	Style        string
	Source       string
	Subset       string
	UnicodeRange string
	Hash         string
	Data         []byte
}

// Store reads and writes the fonts table.
type Store struct {
	pool *pgxpool.Pool

	// flight shares one list query between concurrent callers.
	flight database.Flight[[]Font]
	// list overrides query; nil means query. A test seam.
	list func(context.Context) ([]Font, error)
}

// NewStore returns a store on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// List returns every face without its bytes, by family, weight and style.
// Concurrent callers share one query; each gets its own copy of the slice.
func (s *Store) List(ctx context.Context) ([]Font, error) {
	load := s.list
	if load == nil {
		load = s.query
	}
	out, err := s.flight.Do(ctx, load)
	if err != nil {
		return nil, err
	}
	return slices.Clone(out), nil
}

func (s *Store) query(ctx context.Context) ([]Font, error) {
	conn, err := database.AcquireFrom(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("fonts: list: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
SELECT id, family, weight, style, source, subset, unicode_range, hash, octet_length(data), created_at
FROM hatamoto_fonts ORDER BY lower(family), weight, style, subset, created_at`)
	if err != nil {
		return nil, fmt.Errorf("fonts: list: %w", err)
	}
	defer rows.Close()
	var out []Font
	for rows.Next() {
		var f Font
		if err := rows.Scan(&f.ID, &f.Family, &f.Weight, &f.Style, &f.Source, &f.Subset, &f.UnicodeRange, &f.Hash, &f.Size, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("fonts: list: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Data returns the bytes of the file with the given sha256 hex hash.
func (s *Store) Data(ctx context.Context, hash string) ([]byte, error) {
	var b []byte
	err := s.pool.QueryRow(ctx, `SELECT data FROM hatamoto_fonts WHERE hash = $1 LIMIT 1`, hash).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fonts: data: %w", err)
	}
	return b, nil
}

// Insert stores all faces in one transaction, or none. It refuses a batch
// that would take the table past MaxFonts rows.
func (s *Store) Insert(ctx context.Context, fonts []NewFont) error {
	if len(fonts) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("fonts: insert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialise inserts so the cap cannot be passed by two requests at once.
	if _, err := tx.Exec(ctx, `LOCK TABLE hatamoto_fonts IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("fonts: insert: %w", err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_fonts`).Scan(&n); err != nil {
		return fmt.Errorf("fonts: insert: %w", err)
	}
	if n+len(fonts) > MaxFonts {
		return ErrTooMany
	}
	for _, f := range fonts {
		if _, err := tx.Exec(ctx, `
INSERT INTO hatamoto_fonts (family, weight, style, source, subset, unicode_range, hash, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			f.Family, f.Weight, f.Style, f.Source, f.Subset, f.UnicodeRange, f.Hash, f.Data); err != nil {
			return fmt.Errorf("fonts: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.flight.Forget() // a list load already running predates these faces
	return nil
}

// DeleteFamily removes every face of family (case-insensitive) and returns
// how many rows went.
func (s *Store) DeleteFamily(ctx context.Context, family string) (int, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM hatamoto_fonts WHERE lower(family) = lower($1)`, strings.TrimSpace(family))
	if err != nil {
		return 0, fmt.Errorf("fonts: delete: %w", err)
	}
	s.flight.Forget() // a list load already running predates the delete
	return int(tag.RowsAffected()), nil
}

// Families returns the distinct family names, sorted without regard to case.
func (s *Store) Families(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (lower(family)) family FROM hatamoto_fonts ORDER BY lower(family), family`)
	if err != nil {
		return nil, fmt.Errorf("fonts: families: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("fonts: families: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
