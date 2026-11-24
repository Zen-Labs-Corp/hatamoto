package pluginhost

import (
	"context"

	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/sdk"
)

// commenterKeyFor returns the plugin's pseudonymous key for the commenter the
// context carries, or "" without keys, without the comments.read grant, or
// without a commenter. The grant is checked first so a plugin that may not see
// the key never triggers the commenter lookup.
func (r *Runtime) commenterKeyFor(ctx context.Context, plugin string) string {
	if r.caps.commenterKey == nil || !r.caps.grants.Has(plugin, sdk.PermCommentsRead) {
		return ""
	}
	id, ok := pluginkey.CommenterFrom(ctx)
	if !ok {
		return ""
	}
	return r.caps.commenterKey(plugin, id)
}
