package pluginhost

import (
	"context"

	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/sdk"
)

// clientKeys returns the plugin's visitor keys for the request in ctx: only with
// a deriver (server secret), the client.key grant (checked per call, so a revoke
// is immediate) and a client address in ctx. They are never logged.
func (r *Runtime) clientKeys(ctx context.Context, plugin string) (addr, net string) {
	if r.caps.keys == nil || r.caps.grants == nil || !r.caps.grants.Has(plugin, sdk.PermClientKey) {
		return "", ""
	}
	return r.caps.keys.For(plugin, clientkey.IPFrom(ctx))
}
