// Package auth lets a compiled-in module add sign-in providers and a sign-in
// policy to hatamoto.
//
// Register from an init function in a wrapper main; app.Main freezes the
// registry before anything else runs:
//
//	package main
//
//	import (
//		"hatamoto.dev/core/app"
//		"hatamoto.dev/core/auth"
//	)
//
//	func init() {
//		auth.RegisterProvider("example", "Example", &exampleProvider{})
//		auth.RegisterSignInPolicy(examplePolicy{})
//	}
//
//	func main() { app.Main() }
//
// # Providers
//
// Each provider shows as a "Continue with <name>" link on the login page.
// Core sends the browser to <admin>/login/provider/<id>/start, calls
// Provider.Start and redirects to the URL it returns. The provider sends the
// browser back to <admin>/login/provider/<id>/callback with a GET request;
// POST callbacks are not supported. Core owns the state value: it generates
// it, keeps it in a sealed single-use cookie and checks it before Finish is
// called. StartResult.Data is kept in that cookie and handed back to Finish;
// it is at most MaxDataLen bytes. A site address must be configured so core
// can build the callback URL.
//
// A provider may implement ProviderAvailability to hide itself (no button,
// start and callback answer 404) or to change its label at run time.
//
// Finish returns the Identity the provider asserts. MapUser then decides which
// user that identity is. Core links an identity to a user only when the module
// asks for it with Provisioner.LinkIdentity, and after MapUser core checks
// that the returned user really is linked to the identity. A module must link
// an existing account only on a verified email in a domain the site owner has
// allowed, never silently otherwise.
//
// Every provider sign-in ends in core's session issue, so the user's access
// end date, the lockout and the sign-in policy apply. When Identity.MFA is
// false and the user has a second factor, core asks for that factor before it
// signs the user in. Set Identity.MFA only when the provider itself
// performed a second factor.
//
// # Sign-in policy
//
// A SignInPolicy sees every sign-in by password, passkey or provider. It must
// be free of side effects. It fails closed: a false answer or an error refuses
// the sign-in, and the user sees the same neutral message as for a wrong
// password. It does not apply to command line password resets, to the
// password re-confirmation of an existing session or to API keys. A policy
// that refuses passwords therefore leaves sudo on the account's password, which
// reconfirms an existing session and is not a sign-in.
//
// # Provisioning
//
// Provisioning returns core's implementation of Provisioner. Do not implement
// Provisioner yourself. Disable ends a user's access and revokes their
// sessions and API keys in the same call; the last open owner cannot be
// disabled.
package auth
