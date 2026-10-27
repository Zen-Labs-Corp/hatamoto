package pluginhost

import (
	"fmt"
	"strings"

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
