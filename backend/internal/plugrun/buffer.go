package plugrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Per-run buffer limits.
const (
	MaxBufferBytes  = 64 << 20 // total value bytes
	MaxBufferKeys   = 10000
	MaxBufferKeyLen = 256
	MaxBufferValue  = 1 << 20 // one value; requests are capped at 1 MiB anyway
	MaxListPage     = 1000
)

// checkPut validates a write against the limits. usedKeys and usedBytes are
// the buffer's current totals; oldLen is the size of the value being replaced
// (-1 when the key is new).
func checkPut(key string, valLen, usedKeys, usedBytes, oldLen int) error {
	if key == "" || len(key) > MaxBufferKeyLen || !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
		return fmt.Errorf("%w: buffer key must be 1..%d bytes of UTF-8 without NUL", ErrInvalid, MaxBufferKeyLen)
	}
	if valLen > MaxBufferValue {
		return fmt.Errorf("%w: value over %d bytes", ErrQuota, MaxBufferValue)
	}
	keys, bytes := usedKeys, usedBytes+valLen
	if oldLen < 0 {
		keys++
	} else {
		bytes -= oldLen
	}
	if keys > MaxBufferKeys {
		return fmt.Errorf("%w: more than %d buffer keys", ErrQuota, MaxBufferKeys)
	}
	if bytes > MaxBufferBytes {
		return fmt.Errorf("%w: buffer over %d bytes", ErrQuota, MaxBufferBytes)
	}
	return nil
}

// BufferPut stores val under key for a running run, replacing any previous
// value. The quota is checked in the same transaction as the write, under a
// row lock on the run, so concurrent writers cannot exceed it.
func (s *Store) BufferPut(ctx context.Context, id uuid.UUID, key string, val []byte) error {
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockRunning(ctx, tx, id); err != nil {
			return err
		}
		var keys, bytes int
		if err := tx.QueryRow(ctx,
			`SELECT count(*), COALESCE(sum(length(value)), 0) FROM hatamoto_run_buffer WHERE run_id = $1`, id).
			Scan(&keys, &bytes); err != nil {
			return fmt.Errorf("failed to measure buffer: %w", err)
		}
		oldLen := -1
		err := tx.QueryRow(ctx, `SELECT length(value) FROM hatamoto_run_buffer WHERE run_id = $1 AND key = $2`, id, key).Scan(&oldLen)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("failed to read buffer key: %w", err)
		}
		if err := checkPut(key, len(val), keys, bytes, oldLen); err != nil {
			return err
		}
		if val == nil {
			val = []byte{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO hatamoto_run_buffer (run_id, key, value) VALUES ($1, $2, $3)
			ON CONFLICT (run_id, key) DO UPDATE SET value = EXCLUDED.value`, id, key, val); err != nil {
			return fmt.Errorf("failed to write buffer: %w", err)
		}
		return nil
	})
}

// BufferGet returns the value under key, or ErrNotFound.
func (s *Store) BufferGet(ctx context.Context, id uuid.UUID, key string) ([]byte, error) {
	var val []byte
	err := s.DB.Pool().QueryRow(ctx, `SELECT value FROM hatamoto_run_buffer WHERE run_id = $1 AND key = $2`, id, key).Scan(&val)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read buffer: %w", err)
	}
	return val, nil
}

// BufferList returns up to limit keys starting with prefix, in byte order,
// strictly after the key `after` ("" starts at the beginning). limit is
// clamped to 1..MaxListPage.
func (s *Store) BufferList(ctx context.Context, id uuid.UUID, prefix, after string, limit int) ([]string, error) {
	if limit < 1 || limit > MaxListPage {
		limit = MaxListPage
	}
	rows, err := s.DB.Pool().Query(ctx, `
		SELECT key FROM hatamoto_run_buffer
		WHERE run_id = $1 AND left(key, length($2::text)) = $2::text AND key COLLATE "C" > $3::text COLLATE "C"
		ORDER BY key COLLATE "C" LIMIT $4`, id, prefix, after, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list buffer: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
