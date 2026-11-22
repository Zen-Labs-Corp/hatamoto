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
//   - The read-only role may only read the admin and keep their own account.
//     Reading form submissions and comments, and every write, is not in it.
//   - The author, contributor and reviewer roles have the read-only rights
//     plus content rules. An author creates entities and changes, publishes,
//     unpublishes, deletes and restores versions of their own; a contributor
//     creates entities and changes, deletes and restores versions of their
//     own drafts, and never publishes; a reviewer changes, publishes and
//     unpublishes any entity. All three upload media and change or delete
//     the media they uploaded. They never reach menus, settings, users and
//     the like. With the site's review switch on, authors, contributors and
//     editors send changes to published content for review
//     (entity.review.request); owners and reviewers publish without review
//     (entity.review.bypass); owners, editors and reviewers decide
//     (entity.review.decide).
//   - Any other role, and a session without a user, may do nothing.
//   - An API key acts within what its creator's role may do: it may act only
//     for an action that has a key scope, only when the key holds that scope,
//     and only if the creator's role is allowed the action (so a key made by a
//     read-only user can read but never write). A write scope implies the matching read scope, "*"
//     implies every scope, and the owner-only scopes (read:seo, write:seo,
//     write:terms) need an owner as the creator, also through "*".
//   - Owners and editors ignore the object; the author, contributor and
//     reviewer roles decide some actions from CreatedBy and Status.
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
// user's own account. They ask ActionContentWrite instead for the content
// screens and routes (entities, editors and media). A policy that does not
// delegate to Default must answer both, or every such request is refused,
// owners included.
// To stop all writes, deny ActionContentWrite as well as ActionSiteWrite:
// denying only ActionSiteWrite no longer blocks entity and media writes.
//
// The entity service asks ActionEntityCreate, ActionEntityUpdate,
// ActionEntityPublish, ActionEntityUnpublish, ActionEntityDelete and
// ActionEntityVersionRestore with the entry's Object for every caller made
// with a subject, owners included (admin, REST API, MCP and the CLI with
// --as). A strict allow-list policy must answer them. Work without a subject
// (the scheduler, workers, the CLI without --as) is not asked.
//
// # Objects
//
// The zero Object (empty ID) asks a capability question: may this subject
// perform the action on some object at all. It is what route gates and menus
// ask. A call about one entity or media item passes that item's Object, with
// CreatedBy set to the user who created it (the creator, not the byline) and,
// for entities, Status. A registered authorizer receives the same Object
// unchanged.
//
// Some checks stay outside this package: the password recheck before a
// dangerous action, the plugin manifest role lists, the route allowlist that
// decides which REST routes an API key can reach at all, and invariants such
// as keeping at least one open owner account.
//
// Routes, screens and tools ask Can; the default rules answer as core always
// did for owners and editors.
package authz
