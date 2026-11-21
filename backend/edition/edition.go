package edition

import (
	"context"
	"crypto/ed25519"

	"hatamoto.dev/core/internal/editionstate"
)

// Community is the edition name when nothing is registered.
const Community = editionstate.Community

// Gate decides which features and plugins an edition enables. Return an
// error to deny: core treats an error as false.
//
//   - FeatureEnabled reports whether the named feature is enabled.
//   - PluginEntitled reports whether the plugin id may be used.
type Gate = editionstate.Gate

// ErrNoSiteID is returned by SiteID outside "hatamoto serve".
var ErrNoSiteID = editionstate.ErrNoSiteID

// Register sets the edition name and gate. Call it from an init function.
// It panics when called after app.Main started, a second time, with a nil
// gate, with the reserved name "community", or with a name that does not
// match ^[a-z][a-z0-9-]{0,31}$.
func Register(name string, g Gate) { editionstate.Register(name, g) }

// RegisterPluginKey sets the public key that commercial plugins must be signed
// with. It is independent of Register and is never added to
// HATAMOTO_TRUSTED_KEYS: a package declaring license "commercial" installs and
// loads only when signed by this key and PluginEntitled(ctx, name) is true.
// Call it from an init function. It panics after app.Main started, on a second
// call, or when pub is not an ed25519 public key.
func RegisterPluginKey(pub ed25519.PublicKey) { editionstate.RegisterPluginKey(pub) }

// Name returns the registered edition name, or "community".
func Name() string { return editionstate.Name() }

// FeatureEnabled reports whether feature is enabled. It is false when no
// gate is registered, the gate says false, or the gate returns an error.
func FeatureEnabled(ctx context.Context, feature string) bool {
	return editionstate.Feature(ctx, feature)
}

// PluginEntitled reports whether pluginID is entitled, with the same
// fail-closed rules as FeatureEnabled.
func PluginEntitled(ctx context.Context, pluginID string) bool {
	return editionstate.Plugin(ctx, pluginID)
}

// SiteID returns the stable random site id stored in the database. It
// returns ErrNoSiteID outside "hatamoto serve" (before the database is
// migrated). Core never sends the site id anywhere.
func SiteID(ctx context.Context) (string, error) { return editionstate.SiteID(ctx) }
