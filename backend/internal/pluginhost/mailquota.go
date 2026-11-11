package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The owner-set mail.send quota, in recipients per hour.
const (
	DefaultMailPerHour = 360
	MaxMailPerHour     = 3600
)

// ErrMailQuotaRange is returned for a quota outside 1..MaxMailPerHour.
var ErrMailQuotaRange = errors.New("mail quota out of range")

// MailQuotas is the owner-set recipients per hour for each plugin's mail.send.
// Plugins can read it (permissions()) but have no way to change it.
type MailQuotas interface {
	// MailPerHour returns the plugin's quota, DefaultMailPerHour when unset.
	MailPerHour(plugin string) int
	// SetMailPerHour stores the quota (1..MaxMailPerHour).
	SetMailPerHour(ctx context.Context, plugin string, n int) error
}

// MailQuotaStore keeps quotas in hatamoto_plugin_grants.mail_per_hour and
// mirrors them in memory. With a nil pool it is memory only (tests).
type MailQuotaStore struct {
	pool *pgxpool.Pool

	mu sync.RWMutex
	m  map[string]int
}

// NewMailQuotaStore returns a store over pool (nil: memory only). Call Load
// once at boot.
func NewMailQuotaStore(pool *pgxpool.Pool) *MailQuotaStore {
	return &MailQuotaStore{pool: pool, m: map[string]int{}}
}

// validMailPerHour reports whether n is a storable quota (1..MaxMailPerHour).
func validMailPerHour(n int) bool { return n >= 1 && n <= MaxMailPerHour }

// Load reads every stored quota.
func (s *MailQuotaStore) Load(ctx context.Context) error {
	if s.pool == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT plugin, mail_per_hour FROM hatamoto_plugin_grants WHERE mail_per_hour IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("pluginhost: load mail quotas: %w", err)
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var plugin string
		var n int
		if err := rows.Scan(&plugin, &n); err != nil {
			return err
		}
		if validMailPerHour(n) {
			m[plugin] = n // out of range (tampered import): treated as unset
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.m = m
	s.mu.Unlock()
	return nil
}

// MailPerHour returns the plugin's quota, DefaultMailPerHour when unset.
func (s *MailQuotaStore) MailPerHour(plugin string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n, ok := s.m[plugin]; ok {
		return n
	}
	return DefaultMailPerHour
}

// SetMailPerHour persists the quota, then mirrors it.
func (s *MailQuotaStore) SetMailPerHour(ctx context.Context, plugin string, n int) error {
	if !validMailPerHour(n) {
		return fmt.Errorf("%w: %d", ErrMailQuotaRange, n)
	}
	if s.pool != nil {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO hatamoto_plugin_grants (plugin, mail_per_hour, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (plugin) DO UPDATE SET mail_per_hour = EXCLUDED.mail_per_hour, updated_at = now()`,
			plugin, n); err != nil {
			return fmt.Errorf("pluginhost: save mail quota for %q: %w", plugin, err)
		}
	}
	s.mu.Lock()
	s.m[plugin] = n
	s.mu.Unlock()
	return nil
}

// Forget drops the in-memory value (the plugin was uninstalled and its row is gone).
func (s *MailQuotaStore) Forget(plugin string) {
	s.mu.Lock()
	delete(s.m, plugin)
	s.mu.Unlock()
}
