package comment

import (
	"github.com/google/uuid"

	"hatamoto.dev/core/internal/pluginkey"
)

// PluginCommenterKey is the commenter key one plugin sees, the same value
// pluginkey.Deriver puts on thread items and events (one derivation).
func PluginCommenterKey(secret, plugin string, commenterID uuid.UUID) string {
	return pluginkey.New(secret).Commenter(plugin, commenterID)
}
