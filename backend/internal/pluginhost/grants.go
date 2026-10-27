package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/sdk"
)

// Grants is the owner's decision on which of a plugin's requested capabilities
// it may use. Nothing is granted by default.
type Grants interface {
	Has(plugin string, perm sdk.Permission) bool
	Granted(plugin string) []sdk.Permission
	// Set replaces the plugin's granted set.
	Set(ctx context.Context, plugin string, perms []sdk.Permission) error
}

// GrantStore keeps grants in hatamoto_plugin_grants and mirrors them in
// memory, so the permission check on every host call costs a map lookup. With
// a nil pool it is memory only (tests).
type GrantStore struct {
	pool *pgxpool.Pool

	mu      sync.RWMutex
	m       map[string][]sdk.Permission
	gen     uint64            // counts Set calls
	touched map[string]uint64 // plugin -> gen of its last Set
}

// NewGrantStore returns a store over pool (nil: memory only). Call Load once
// at boot.
func NewGrantStore(pool *pgxpool.Pool) *GrantStore {
	return &GrantStore{pool: pool, m: map[string][]sdk.Permission{}, touched: map[string]uint64{}}
}

// Load reads every stored grant.
func (g *GrantStore) Load(ctx context.Context) error {
	if g.pool == nil {
		return nil
	}
	g.mu.RLock()
	startGen := g.gen
	g.mu.RUnlock()
	rows, err := g.pool.Query(ctx, `SELECT plugin, permissions FROM hatamoto_plugin_grants`)
	if err != nil {
		return fmt.Errorf("pluginhost: load grants: %w", err)
	}
	defer rows.Close()
	m := map[string][]sdk.Permission{}
	for rows.Next() {
		var plugin string
		var perms []string
		if err := rows.Scan(&plugin, &perms); err != nil {
			return err
		}
		m[plugin] = perms
	}
	if err := rows.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	// A Set that landed while the query ran is newer than what it read.
	for plugin, at := range g.touched {
		if at > startGen {
			m[plugin] = g.m[plugin]
		}
	}
	g.m = m
	g.mu.Unlock()
	return nil
}

// Has reports whether plugin holds perm.
func (g *GrantStore) Has(plugin string, perm sdk.Permission) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return slices.Contains(g.m[plugin], perm)
}

// Granted returns the plugin's granted permissions, sorted.
func (g *GrantStore) Granted(plugin string) []sdk.Permission {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return slices.Clone(g.m[plugin])
}

// ErrUnknownPermission is returned for a permission name the SDK does not define.
var ErrUnknownPermission = errors.New("unknown permission")

// Set stores the plugin's granted set (deduplicated, sorted).
func (g *GrantStore) Set(ctx context.Context, plugin string, perms []sdk.Permission) error {
	perms = append([]sdk.Permission{}, perms...) // never nil: the column is NOT NULL, and revoking everything sends none
	slices.Sort(perms)
	perms = slices.Compact(perms)
	for _, p := range perms {
		if !sdk.ValidPermission(p) {
			return fmt.Errorf("%w: %q", ErrUnknownPermission, p)
		}
	}
	if g.pool != nil {
		if _, err := g.pool.Exec(ctx, `
			INSERT INTO hatamoto_plugin_grants (plugin, permissions, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (plugin) DO UPDATE SET permissions = EXCLUDED.permissions, updated_at = now()`,
			plugin, perms); err != nil {
			return fmt.Errorf("pluginhost: save grants for %q: %w", plugin, err)
		}
	}
	g.mu.Lock()
	g.m[plugin] = perms
	g.gen++
	g.touched[plugin] = g.gen
	g.mu.Unlock()
	return nil
}
