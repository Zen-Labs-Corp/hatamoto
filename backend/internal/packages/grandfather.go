package packages

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// grandfatherEntityFilterKey names the row of hatamoto_plugin_grants that
// holds the snapshot below. It is not a valid plugin name (sdk.NamePattern
// forbids ':'), so no plugin can collide with it.
const grandfatherEntityFilterKey = "core:grandfather:entities.filter"

// entityFilterRuntime is the part of *pluginhost.Runtime the keep needs. It is
// optional so that PluginRuntime and its fakes stay unchanged.
type entityFilterRuntime interface {
	ExportsEntityFilter(name string) bool
	Reregister(name string)
}

var _ entityFilterRuntime = (*pluginhost.Runtime)(nil)

// entityFilterKeep is what the manager remembers between passes. Its fields
// are guarded by Manager.mu.
type entityFilterKeep struct {
	claimed bool // the snapshot row exists
	done    bool // the snapshot is empty: nothing is left to keep
	seen    int  // length of the snapshot at the last read
	// needReload is set when the stored grants changed and cleared once
	// Grants.Load has succeeded.
	needReload bool
}

// claimEntityFilterSnapshot records, once, which plugins were installed before
// entities.filter became a permission: the marker row's permissions hold their
// names (any state). It is one statement, so it is atomic across processes.
// Every Manager with a pool claims before it installs anything, so a plugin
// installed afterwards is never in the snapshot. It reports whether the
// snapshot exists; on an error nothing is kept and the next pass retries.
func (m *Manager) claimEntityFilterSnapshot(ctx context.Context) bool {
	if m.filterKeep.claimed {
		return true
	}
	if m.Pool == nil {
		return false
	}
	_, err := m.Pool.Exec(ctx, `
		INSERT INTO hatamoto_plugin_grants (plugin, permissions)
		SELECT $1, COALESCE(array_agg(name ORDER BY name), '{}') FROM hatamoto_packages WHERE kind = 'plugin'
		ON CONFLICT (plugin) DO NOTHING`, grandfatherEntityFilterKey)
	if err != nil {
		m.Logger.Warn("packages: entity filter snapshot not written", "error", err)
		return false
	}
	m.filterKeep.claimed = true
	return true
}

// forgetEntityFilterSnapshot removes a plugin from the snapshot: a new install
// or an update has to ask for entities.filter like any other permission.
func forgetEntityFilterSnapshot(ctx context.Context, tx pgx.Tx, name string) error {
	_, err := tx.Exec(ctx, `UPDATE hatamoto_plugin_grants SET permissions = array_remove(permissions, $2), updated_at = now()
		WHERE plugin = $1 AND $2 = ANY(permissions)`, grandfatherEntityFilterKey, name)
	return err
}

// grandfatherEntityFilter keeps the entity.before_save filter of plugins that
// were installed when it became a permission. Each pass, a loaded plugin that
// is in the snapshot, exports the filter and does not ask for the permission
// is granted it once (stored grants are unioned, never replaced) and leaves
// the snapshot, so a later revoke sticks. Plugins that are not loaded stay in
// the snapshot until they are. It also rebuilds the hooks of a loaded plugin
// whose registration differs from the grants in memory (refreshed when the
// snapshot changes or a plugin loads).
func (m *Manager) grandfatherEntityFilter(ctx context.Context) {
	if m.Pool == nil || !m.claimEntityFilterSnapshot(ctx) {
		return
	}
	rt, ok := m.Plugins.(entityFilterRuntime)
	if !ok || m.Grants == nil {
		return
	}
	statuses := m.Plugins.Statuses()
	if !m.filterKeep.done {
		var snap []string
		if err := m.Pool.QueryRow(ctx, `SELECT permissions FROM hatamoto_plugin_grants WHERE plugin = $1`, grandfatherEntityFilterKey).Scan(&snap); err != nil {
			m.Logger.Warn("packages: entity filter snapshot not read", "error", err)
			return
		}
		m.filterKeep.needReload = m.filterKeep.needReload || len(snap) != m.filterKeep.seen
		m.filterKeep.seen = len(snap)
		for _, st := range statuses {
			if !st.Loaded || !slices.Contains(snap, st.Name) {
				continue
			}
			if m.keepEntityFilter(ctx, st, rt) {
				m.filterKeep.needReload = true
				m.filterKeep.seen--
			}
		}
		m.filterKeep.done = m.filterKeep.seen == 0
	}
	if m.filterKeep.needReload {
		if g, ok := m.Grants.(interface{ Load(context.Context) error }); ok {
			if err := g.Load(ctx); err != nil {
				m.Logger.Error("packages: grants not reloaded", "error", err)
				return
			}
		}
		m.filterKeep.needReload = false
	}
	for _, st := range statuses {
		if !st.Loaded {
			continue
		}
		want := m.Grants.Has(st.Name, sdk.PermEntitiesFilter)
		if want != slices.Contains(st.Hooks, sdk.EntityBeforeSave.Name) && rt.ExportsEntityFilter(st.Name) {
			rt.Reregister(st.Name)
		}
	}
}

// keepEntityFilter takes one loaded plugin out of the snapshot and, when it
// exports the filter without asking for the permission, grants it, in the same
// transaction. It reports whether this process took the plugin out of the
// snapshot (false when another process did, or on an error).
func (m *Manager) keepEntityFilter(ctx context.Context, st pluginhost.Status, rt entityFilterRuntime) bool {
	keep := rt.ExportsEntityFilter(st.Name) && !slices.Contains(st.Manifest.Asked(), sdk.PermEntitiesFilter) &&
		!m.Grants.Has(st.Name, sdk.PermEntitiesFilter)
	removed := false
	err := pgx.BeginFunc(ctx, m.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hatamoto_plugin_grants SET permissions = array_remove(permissions, $2), updated_at = now()
			WHERE plugin = $1 AND $2 = ANY(permissions)`, grandfatherEntityFilterKey, st.Name)
		if err != nil || tag.RowsAffected() == 0 {
			return err // zero rows: another process took it
		}
		removed = true
		if !keep {
			return nil
		}
		// A union with what is stored, never with this process's copy.
		_, err = tx.Exec(ctx, `
			INSERT INTO hatamoto_plugin_grants (plugin, permissions, updated_at) VALUES ($1, ARRAY[$2]::text[], now())
			ON CONFLICT (plugin) DO UPDATE SET updated_at = now(),
				permissions = (SELECT COALESCE(array_agg(DISTINCT p), '{}') FROM unnest(hatamoto_plugin_grants.permissions || EXCLUDED.permissions) AS p)`,
			st.Name, string(sdk.PermEntitiesFilter))
		return err
	})
	if err != nil {
		m.Logger.Warn("packages: entity filter not kept", "plugin", st.Name, "error", err)
		return false
	}
	if removed && keep {
		m.Audit(ctx, "package.grants", "plugin:"+st.Name, map[string]any{"kept": string(sdk.PermEntitiesFilter)})
		m.Logger.Info("packages: kept entity filter for installed plugin", "plugin", st.Name)
	}
	return removed
}
