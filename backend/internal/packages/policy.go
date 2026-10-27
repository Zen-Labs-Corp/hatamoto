package packages

import (
	"slices"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// ErrCommunityScripts is the one sentinel for errors.Is across layers.
var ErrCommunityScripts = pluginhost.ErrCommunityScripts

// refuseCommunityScripts is true in production: a community package never gets
// site.scripts. Package tests flip it to exercise the sandbox path.
var refuseCommunityScripts = true

// errCommunityIsland is the refusal for builder.island. It matches
// ErrCommunityScripts under errors.Is, so every caller that maps the one
// sentinel also maps this one, with a message that names the permission.
type errCommunityIsland struct{}

func (errCommunityIsland) Error() string {
	return "builder.island cannot be granted to a community package"
}
func (errCommunityIsland) Is(target error) bool { return target == ErrCommunityScripts }

// communityGrantError is the refusal for perms asked by a community package:
// site.scripts and builder.island both put its JavaScript in the site origin.
func communityGrantError[S ~string](perms []S) error {
	switch {
	case slices.Contains(perms, S(sdk.PermSiteScripts)):
		return ErrCommunityScripts
	case slices.Contains(perms, S(sdk.PermBuilderIsland)):
		return errCommunityIsland{}
	}
	return nil
}

// checkScriptsGrant refuses site.scripts and builder.island in perms for a
// community package.
func checkScriptsGrant(o Origin, perms []string) error {
	if refuseCommunityScripts && o == OriginCommunity {
		return communityGrantError(perms)
	}
	return nil
}

// refreshOrigins rebuilds the plugin origin map from the package rows.
func (m *Manager) refreshOrigins(rows []Row) {
	origins := map[string]Origin{}
	for _, r := range rows {
		if r.Kind == KindPlugin {
			origins[r.Name] = ClassifyRow(r)
		}
	}
	m.origins.Store(&origins)
}

func (m *Manager) originOf(plugin string) Origin {
	if o := m.origins.Load(); o != nil {
		return (*o)[plugin]
	}
	return OriginSelfBuilt
}

// scriptsPolicyRefuses reports whether policy refuses site.scripts for plugin.
func (m *Manager) scriptsPolicyRefuses(plugin string) bool {
	return refuseCommunityScripts && m.originOf(plugin) == OriginCommunity
}

// ScriptsRefused is scriptsPolicyRefuses for other packages (grant policy and
// the pluginhost script deny hook).
func (m *Manager) ScriptsRefused(plugin string) bool { return m.scriptsPolicyRefuses(plugin) }

// CommunityScriptsLive reports whether some community plugin's script is
// linked on pages right now (policy, grant, loaded and manifest all agree).
// It reads in-memory state only, so it is cheap enough for every HTML response.
func (m *Manager) CommunityScriptsLive() bool {
	o := m.origins.Load()
	if o == nil || m.Plugins == nil {
		return false
	}
	for name, origin := range *o {
		if origin == OriginCommunity && m.scriptGate(name) {
			return true
		}
	}
	return false
}

// CommunityPlugin reports whether name is a community-origin plugin, by the same
// origins map as CommunityScriptsLive. It does not say whether its script is
// linked; the render path asks it about scripts a page body already links.
func (m *Manager) CommunityPlugin(name string) bool { return m.originOf(name) == OriginCommunity }

// CheckGrant is the pluginhost grant policy: a refusal when perms ask a
// community plugin for site.scripts or builder.island.
func (m *Manager) CheckGrant(plugin string, perms []sdk.Permission) error {
	if m.ScriptsRefused(plugin) {
		return communityGrantError(perms)
	}
	return nil
}
