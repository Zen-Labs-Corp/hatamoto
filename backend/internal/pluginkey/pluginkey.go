// Package pluginkey derives the per-plugin pseudonymous commenter key handed
// to plugins that hold comments.read (PLUGIN_API.md "Visitor and commenter
// keys"): HMAC-SHA256 under DeriveKey(secret, "plugin-client/commenter") over
// "<plugin>:<commenter id>", base64url without padding. It also carries the
// commenter id through a context (commenter.go); the id never enters an sdk
// value. Keys are never stored; rotating the server secret changes them.
package pluginkey

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/secretbox"
)

// Deriver holds the commenter derivation key. A nil *Deriver derives nothing.
type Deriver struct{ commenter []byte }

// New returns nil for an empty secret: no secret, no keys.
func New(serverSecret string) *Deriver {
	if serverSecret == "" {
		return nil
	}
	return &Deriver{commenter: secretbox.DeriveKey(serverSecret, "plugin-client/commenter")}
}

// Commenter returns the plugin's key for the commenter, or "" for a nil
// Deriver, an empty plugin or uuid.Nil.
func (d *Deriver) Commenter(plugin string, id uuid.UUID) string {
	if d == nil || plugin == "" || id == uuid.Nil {
		return ""
	}
	m := hmac.New(sha256.New, d.commenter)
	m.Write([]byte(plugin + ":" + id.String()))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
