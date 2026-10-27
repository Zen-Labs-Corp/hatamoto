package adminpath

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNoSiteConfig means the singleton site config row does not exist yet
// (first boot seeds it).
var ErrNoSiteConfig = errors.New("adminpath: no site config row")

// DB is what Store needs; *pgxpool.Pool satisfies it.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Stored is the persisted admin address. A nil Path means pending: nothing
// has been chosen yet.
type Stored struct {
	Path  *string
	AckBy *string
	AckAt *time.Time
	// SetBy and SetAt record who last chose the address and when. They are
	// nil for a server-generated address (SetIfPending) and for rows that
	// predate the columns.
	SetBy *string
	SetAt *time.Time
}

// Store reads and writes the admin address columns of hatamoto_site_config
// directly. It does not go through siteconfig.Update, whose COALESCE cannot
// set a column back to NULL.
type Store struct{ db DB }

func NewStore(db DB) *Store { return &Store{db: db} }

// Get returns the stored address, or ErrNoSiteConfig when the row is missing.
func (s *Store) Get(ctx context.Context) (Stored, error) {
	var st Stored
	err := s.db.QueryRow(ctx,
		`SELECT admin_path, admin_path_ack_by, admin_path_ack_at, admin_path_set_by, admin_path_set_at FROM hatamoto_site_config WHERE id = TRUE`).
		Scan(&st.Path, &st.AckBy, &st.AckAt, &st.SetBy, &st.SetAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stored{}, ErrNoSiteConfig
	}
	if err != nil {
		return Stored{}, fmt.Errorf("read admin path: %w", err)
	}
	return st, nil
}

// Set stores path, records by as who chose it, and clears any acknowledgement.
func (s *Store) Set(ctx context.Context, path, by string) error {
	return s.exec(ctx,
		`UPDATE hatamoto_site_config SET admin_path = $1, admin_path_set_by = $2, admin_path_set_at = now(),
		 admin_path_ack_by = NULL, admin_path_ack_at = NULL WHERE id = TRUE`, path, by)
}

// SetAck stores path and records who knowingly accepted it as weak, and when.
// The same person is recorded as having chosen it.
func (s *Store) SetAck(ctx context.Context, path, by string) error {
	return s.exec(ctx,
		`UPDATE hatamoto_site_config SET admin_path = $1, admin_path_set_by = $2, admin_path_set_at = now(),
		 admin_path_ack_by = $2, admin_path_ack_at = now() WHERE id = TRUE`, path, by)
}

// Ack records that someone knowingly accepted path as weak, and when, without
// touching who chose it or when. It writes nothing, and returns ErrNoSiteConfig,
// when path is not the stored address.
func (s *Store) Ack(ctx context.Context, path, by string) error {
	return s.exec(ctx,
		`UPDATE hatamoto_site_config SET admin_path_ack_by = $2, admin_path_ack_at = now()
		 WHERE id = TRUE AND admin_path = $1`, path, by)
}

// SetIfPending stores path only while none is stored, so replicas booting
// together agree on one value. It reports whether this call wrote it.
func (s *Store) SetIfPending(ctx context.Context, path string) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE hatamoto_site_config SET admin_path = $1 WHERE id = TRUE AND admin_path IS NULL`, path)
	if err != nil {
		return false, fmt.Errorf("set pending admin path: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) exec(ctx context.Context, q string, args ...any) error {
	tag, err := s.db.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("write admin path: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoSiteConfig
	}
	return nil
}
