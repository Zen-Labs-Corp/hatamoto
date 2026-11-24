package comment

import (
	"github.com/google/uuid"

	"hatamoto.dev/core/internal/secretbox"
)

// PluginCommenterKey is the commenter key one plugin sees:
// base64url(HMAC-SHA256(DeriveKey(secret, "plugin-client/commenter"), plugin+":"+commenterID)).
// A plugin cannot link its key to another plugin's, nor to the email hash.
func PluginCommenterKey(secret, plugin string, commenterID uuid.UUID) string {
	return mac(secretbox.DeriveKey(secret, "plugin-client/commenter"), plugin+":"+commenterID.String())
}
