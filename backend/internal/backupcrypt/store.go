package backupcrypt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store reads and writes the single hatamoto_backup_encryption row.
type Store struct{ db Querier }

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db} }

// Get returns the saved settings; found is false when no row exists.
func (s *Store) Get(ctx context.Context) (set Settings, found bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT recipient, passphrase_sealed, updated_at
		FROM hatamoto_backup_encryption WHERE id`).Scan(&set.Recipient, &set.PassphraseSealed, &set.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("load backup encryption: %w", err)
	}
	return set, true, nil
}

// SavePublicKey stores an age public key and drops any passphrase. A private
// key is refused with ErrPrivateKey and nothing is written.
func (s *Store) SavePublicKey(ctx context.Context, recipient string) error {
	if _, err := ParseRecipient(recipient); err != nil {
		return err
	}
	return s.upsert(ctx, strings.TrimSpace(recipient), nil)
}

// SavePassphrase stores a sealed passphrase (see SealPassphrase) and drops any public key.
func (s *Store) SavePassphrase(ctx context.Context, sealed []byte) error {
	if len(sealed) == 0 {
		return errors.New("no passphrase to save")
	}
	return s.upsert(ctx, "", sealed)
}

// Clear turns encryption off.
func (s *Store) Clear(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM hatamoto_backup_encryption`); err != nil {
		return fmt.Errorf("clear backup encryption: %w", err)
	}
	return nil
}

func (s *Store) upsert(ctx context.Context, recipient string, sealed []byte) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_backup_encryption (id, recipient, passphrase_sealed, updated_at)
		VALUES (TRUE, $1, $2, now())
		ON CONFLICT (id) DO UPDATE SET recipient = EXCLUDED.recipient,
			passphrase_sealed = EXCLUDED.passphrase_sealed, updated_at = now()`, recipient, sealed)
	if err != nil {
		return fmt.Errorf("save backup encryption: %w", err)
	}
	return nil
}
