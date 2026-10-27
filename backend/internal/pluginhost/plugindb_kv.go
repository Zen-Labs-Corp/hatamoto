package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- kv ---------------------------------------------------------------------

// The kv store is a core table (hatamoto_plugin_kv) that only core code
// touches, on the core pool. The plugin role has no right on it.

const (
	maxKVKeyBytes   = 256
	maxKVValueBytes = 256 << 10
	maxKVListLimit  = 1000
	maxKVKeys       = 10000
	maxKVTotalBytes = 64 << 20
)

// ErrKVQuota is returned when a plugin's kv store is full.
var ErrKVQuota = errors.New("plugin kv quota exceeded")

// KVGet returns the value of key, if present and not expired.
func (d *PluginDB) KVGet(ctx context.Context, plugin, key string) (json.RawMessage, bool, error) {
	var raw []byte
	err := d.pool.QueryRow(ctx, `SELECT value FROM hatamoto_plugin_kv
		WHERE plugin = $1 AND key = $2 AND (expires_at IS NULL OR expires_at > now())`, plugin, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

// KVSet stores value under key; ttl > 0 expires it. A plugin holds at most
// maxKVKeys live keys and maxKVTotalBytes of values.
func (d *PluginDB) KVSet(ctx context.Context, plugin, key string, value json.RawMessage, ttl time.Duration) error {
	var exp *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		exp = &t
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('hatamoto.plugin.kv.' || $1::text))`, plugin); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_plugin_kv WHERE plugin = $1 AND expires_at <= now()`, plugin); err != nil {
			return err
		}
		var keys, size int64
		if err := tx.QueryRow(ctx, `SELECT count(*), coalesce(sum(pg_column_size(value)), 0) FROM hatamoto_plugin_kv
			WHERE plugin = $1 AND key <> $2`, plugin, key).Scan(&keys, &size); err != nil {
			return err
		}
		if keys >= maxKVKeys || size+int64(len(value)) > maxKVTotalBytes {
			return fmt.Errorf("%w: at most %d keys and %d bytes", ErrKVQuota, maxKVKeys, maxKVTotalBytes)
		}
		_, err := tx.Exec(ctx, `INSERT INTO hatamoto_plugin_kv (plugin, key, value, expires_at) VALUES ($1, $2, $3::jsonb, $4)
			ON CONFLICT (plugin, key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at, updated_at = now()`,
			plugin, key, []byte(value), exp)
		return err
	})
}

// KVDelete removes key.
func (d *PluginDB) KVDelete(ctx context.Context, plugin, key string) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM hatamoto_plugin_kv WHERE plugin = $1 AND key = $2`, plugin, key)
	return err
}

// KVList returns up to limit live keys with the prefix, sorted.
func (d *PluginDB) KVList(ctx context.Context, plugin, prefix string, limit int) ([]string, error) {
	rows, err := d.pool.Query(ctx, `SELECT key FROM hatamoto_plugin_kv
		WHERE plugin = $1 AND starts_with(key, $2) AND (expires_at IS NULL OR expires_at > now()) ORDER BY key LIMIT $3`, plugin, prefix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
