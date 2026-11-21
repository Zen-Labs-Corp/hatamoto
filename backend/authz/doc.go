// Package authz lets a wrapper module decide who may do what. Can answers one
// question: may this subject perform this action on this object? The subject
// is a signed-in session or an API key, the action is one of the catalogue
// constants (ActionEntityPublish and so on, listed by Actions), and the
// object says what it applies to.
//
// With nothing registered, Can answers what core has always allowed:
//
//   - An owner session may do every action.
//   - An editor session may do the actions that editors can do today.
//   - The author, contributor, reviewer and read-only roles may, for now, only
//     read the admin and keep their own account (the same short list for all
//     four). Reading form submissions and comments, and every write, is not
//     in it.
//   - Any other role, and a session without a user, may do nothing.
//   - An API key acts within what its creator's role may do: it may act only
//     for an action that has a key scope, only when the key holds that scope,
//     and only if the creator's role is allowed the action (so a key made by a
//     read-only user can read but never write). A write scope implies the matching read scope, "*"
//     implies every scope, and the owner-only scopes (read:seo, write:seo,
//     write:terms) need an owner as the creator, also through "*".
//   - The object is ignored.
//   - An action outside the catalogue is denied with ErrUnknownAction.
//
// A wrapper module has its own main package that imports a package whose
// init function calls Register, then calls app.Main:
//
//	package main
//
//	import (
//		_ "example.com/yourmodule/policy" // init calls authz.Register
//		"hatamoto.dev/core/app"
//	)
//
//	func main() { app.Main() }
//
// A registered Authorizer replaces the default; it does not wrap it. To
// change only a few answers, delegate the rest to Default:
//
//	func (policy) Can(ctx context.Context, s authz.Subject, a authz.Action, o authz.Object) (bool, error) {
//		// Only a session is narrowed here; a key falls through to the default
//		// rules, which check its scopes.
//		if a == authz.ActionEntityPublish && o.Schema == "page" && s.KeyID == "" {
//			return s.Role == "owner", nil
//		}
//		return authz.Default().Can(ctx, s, a, o)
//	}
//
// For a key subject (KeyID is not empty) Role is ignored: CreatorRole and
// Scopes decide. Whoever builds a Subject must set KeyID for every request made
// with an API key, and a policy that answers for key subjects itself must
// check Scopes (or delegate to Default) instead of looking at Role.
//
// Register must run before app.Main (from an init function); app.Main
// freezes the registration, and a later, second, nil or typed-nil Register
// panics. An authorizer that returns an error denies (fail closed). A panic
// inside an authorizer is not recovered. A registered authorizer receives
// every action string unchanged, including ones a module defines itself.
//
// The admin and the REST API ask ActionSiteWrite before every request that
// changes something (any method but GET, HEAD and OPTIONS), apart from a
// user's own account. A policy that does not delegate to Default must answer
// it, or every such request is refused, owners included.
//
// Some checks stay outside this package: the password recheck before a
// dangerous action, the plugin manifest role lists, the route allowlist that
// decides which REST routes an API key can reach at all, and invariants such
// as keeping at least one open owner account.
//
// Routes, screens and tools ask Can; the default rules answer as core always
// did for owners and editors.
package authz
