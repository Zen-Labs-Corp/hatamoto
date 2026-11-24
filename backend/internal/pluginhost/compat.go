package pluginhost

import (
	"fmt"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/sdk"
)

// SDKRefusal is nil when this host serves the manifest's sdk range, as decided
// by Manifest.Compatible. Otherwise it names the plugin, the range, the host's
// SDK version and the SDK minors the host serves. Load and install both report it.
func SDKRefusal(m sdk.Manifest) error {
	ok, err := m.Compatible()
	return sdkRefusal(m.Name, m.SDK, sdk.Version, sdk.CompatibleWith, ok, err)
}

// sdkRefusal builds the refusal for a decision (ok, err) already made by the
// SDK, with the host values passed in so tests can model another host. list is
// ascending.
func sdkRefusal(name, rng, version string, list []string, ok bool, err error) error {
	if err != nil {
		return fmt.Errorf("plugin %q has an sdk range %q this host cannot read: %w", name, rng, err)
	}
	if ok {
		return nil
	}
	lowest := version
	if len(list) > 0 {
		lowest = list[0]
	}
	return fmt.Errorf("plugin %q requires SDK %q but this host provides %s (it serves plugins built for SDK %s to %s)",
		name, rng, version, minor(lowest), minor(version))
}

// minor shortens "0.11.0" to "0.11".
func minor(v string) string { return strings.TrimSuffix(v, ".0") }

// commercialSince is the first SDK version whose hosts read Manifest.License.
const commercialSince = "0.26.0"

// CommercialRangeRefusal is nil unless m is commercial and its sdk range admits a host
// older than 0.26, which ignores the license field. Install reports it.
func CommercialRangeRefusal(m sdk.Manifest) error {
	if m.License != sdk.LicenseCommercial {
		return nil
	}
	if rangeRequires(m.SDK, append(slices.Clone(sdk.CompatibleWith), sdk.Version), commercialSince) {
		return nil
	}
	return fmt.Errorf("plugin %q declares a commercial license, so its sdk range must exclude hosts before 0.26 (for example \"^0.26\")", m.Name)
}

// RedirectPathsRefusal is nil when none of the manifest's redirect_paths is
// owned by core (admin, API, assets, well-known and verification files, ...).
// The SDK checks the shape of the paths; only the host knows what it serves.
// Load and install both report it.
func RedirectPathsRefusal(m sdk.Manifest) error {
	for _, p := range m.RedirectPaths {
		if why := adminpath.RedirectPathRefusal(p); why != "" {
			return fmt.Errorf("plugin %q cannot declare redirect path %q: %s", m.Name, p, why)
		}
	}
	return nil
}

// PagePathsRefusal is nil when none of the manifest's page_paths is a path
// core serves itself (same list as redirect_paths) and none ends in a slash
// (except "/"), which serve never matches. Load and install both
// report it.
func PagePathsRefusal(m sdk.Manifest) error {
	for _, p := range m.PagePaths {
		if len(p) > 1 && strings.HasSuffix(p, "/") {
			return fmt.Errorf("plugin %q cannot declare page path %q: drop the trailing slash (the request path is matched without it)", m.Name, p)
		}
		if why := adminpath.RedirectPathRefusal(p); why != "" {
			return fmt.Errorf("plugin %q cannot declare page path %q: %s", m.Name, p, why)
		}
	}
	return nil
}

// PagePathPrefixesRefusal is nil when no page_path_prefixes entry covers a path
// core serves: "/" covers all of them, and any other prefix is refused when its
// first segment is core's (the redirect_paths list). Load and install both report it.
func PagePathPrefixesRefusal(m sdk.Manifest) error {
	for _, p := range m.PagePathPrefixes {
		if p == "/" {
			return fmt.Errorf("plugin %q cannot declare page path prefix %q: it covers every path core serves", m.Name, p)
		}
		if why := adminpath.RedirectPathRefusal(p); why != "" {
			return fmt.Errorf("plugin %q cannot declare page path prefix %q: %s", m.Name, p, why)
		}
	}
	return nil
}

// builtinSchemas are core's own entity types; no plugin may own one.
var builtinSchemas = []string{"page", "post", entity.PersonSchemaSlug, entity.BlockSchemaSlug}

// EntitySchemaRefusal is nil unless entity_schema names a built-in entity type.
// Load and install both report it.
func EntitySchemaRefusal(m sdk.Manifest) error {
	if slices.Contains(builtinSchemas, m.EntitySchema) {
		return fmt.Errorf("plugin %q cannot declare entity_schema %q: it is a built-in content type", m.Name, m.EntitySchema)
	}
	return nil
}
