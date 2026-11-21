package app

import (
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/oidcsignin"
)

// The free OIDC sign-in provider is part of every binary that runs Main. It
// shows nothing until an owner turns it on in Settings > Sign-in.
func init() { authstate.RegisterProvider("oidc", "Single sign-on", oidcsignin.Default()) }

// The provider hides itself while it is off.
var _ authstate.ProviderAvailability = (*oidcsignin.Provider)(nil)
