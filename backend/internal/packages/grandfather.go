package packages

import (
	"context"
	"slices"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// grandfatherEntityFilterKey marks that the one-time sweep below has run. It is
// stored as a row of hatamoto_plugin_grants whose name is not a valid plugin
// name (sdk.NamePattern forbids ':'), so no plugin can collide with it.
const grandfatherEntityFilterKey = "core:grandfather:entities.filter"

// entityFilterRuntime is the part of *pluginhost.Runtime the sweep needs. It is
// optional so that PluginRuntime and its fakes stay unchanged.
type entityFilterRuntime interface {
	ExportsEntityFilter(name string) bool
	Reregister(name string)
}

var _ entityFilterRuntime = (*pluginhost.Runtime)(nil)

// grandfatherEntityFilter keeps the entity.before_save filter of plugins that
// were already installed when it became a permission. The first time it runs
// it grants entities.filter to every loaded plugin that exports the filter
// without asking for the permission; plugins installed or updated afterwards
// are never granted it. Existing grants are kept.
//
// The marker is claimed first, so the sweep runs once even across replicas. If
// the claim fails nothing is granted and the next pass retries.
func (m *Manager) grandfatherEntityFilter(ctx context.Context) {
	rt, ok := m.Plugins.(entityFilterRuntime)
	if !ok || m.Grants == nil || m.Pool == nil {
		return
	}
	tag, err := m.Pool.Exec(ctx, `INSERT INTO hatamoto_plugin_grants (plugin, permissions) VALUES ($1, '{}') ON CONFLICT (plugin) DO NOTHING`, grandfatherEntityFilterKey)
	if err != nil {
		m.Logger.Warn("packages: entity filter marker not written", "error", err)
		return
	}
	if tag.RowsAffected() == 0 {
		return
	}
	for _, st := range m.Plugins.Statuses() {
		if !st.Loaded || slices.Contains(st.Manifest.Asked(), sdk.PermEntitiesFilter) ||
			m.Grants.Has(st.Name, sdk.PermEntitiesFilter) || !rt.ExportsEntityFilter(st.Name) {
			continue
		}
		next := append(slices.Clone(m.Grants.Granted(st.Name)), sdk.PermEntitiesFilter)
		if err := m.Grants.Set(ctx, st.Name, next); err != nil {
			m.Logger.Warn("packages: entity filter not kept", "plugin", st.Name, "error", err)
			continue
		}
		m.Audit(ctx, "package.grants", "plugin:"+st.Name, map[string]any{"kept": string(sdk.PermEntitiesFilter)})
		m.Logger.Info("packages: kept entity filter for installed plugin", "plugin", st.Name)
		rt.Reregister(st.Name)
	}
}
