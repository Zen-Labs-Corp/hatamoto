// Package oidcsignin is the OpenID Connect sign-in provider: the settings an
// owner saves in Settings > Sign-in, and an authstate.Provider that signs users
// in through an outside identity service (Entra ID, Okta, Google Workspace).
//
// The provider is compiled in but invisible until the owner turns it on; it
// keeps no sign-in state in process memory (state, nonce and PKCE verifier
// travel in core's sealed provider cookie).
package oidcsignin
