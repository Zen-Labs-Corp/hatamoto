// Package review lets a wrapper module decide the review stages that a human
// edit must pass before it is published. A build that registers nothing uses
// the default: no stages while the site's review switch is off, and one
// "Review" stage for the "reviewers" group while it is on, unless the
// requester may publish without review. Owners and reviewers have that right
// by default.
//
// Edit carries the site's switch (ReviewEnabled), the requester's role
// (RequesterRole; for an API key, its creator's role) and Bypass, true when
// the requester holds the right to publish without review. The default honours
// Bypass; a registered policy sees both fields and may ignore Bypass.
//
// A wrapper module has its own main package that imports a package whose
// init function calls Register, then calls app.Main:
//
//	package main
//
//	import (
//		_ "example.com/yourmodule/policy" // init calls review.Register
//		"hatamoto.dev/core/app"
//	)
//
//	func main() { app.Main() }
//
// Register must run before app.Main (from an init function); app.Main
// freezes the registration and a later Register panics. A policy that
// returns an error, or a stage without a name or group, makes StagesFor
// return ErrPolicy: the edit is held and never published (fail closed).
//
// RegisterGroups sets a GroupResolver, also from an init function and before
// app.Main. For a stage whose group is not DefaultGroup, core asks
// MayDecide(ctx, userID, role, group, entityID); an error or a panic denies.
// The decide right entity.review.decide is still required and a requester
// never decides their own change. A resolver that wants owners to see every
// stage answers true for role "owner". It must honour ctx. Without a
// resolver such a stage cannot be decided; an owner can discard the change.
//
// Pending lists the staged edits waiting at a stage, oldest first, for module
// jobs such as reminders. It works in serve only (ErrUnavailable elsewhere).
// Index is 0-based and EnteredAt is when the stage was reached (the request
// time for stage 0). Edits that are approved but not live, sent back, or
// discarded are not listed.
//
// Edit.Locale is the entry's locale. Edit.Section is the first segment of the
// entry's public path without the locale prefix, such as "/pricing"; it is
// empty for the home page, entries without a public page, and the CLI.
//
// Core does not set Edit.SlugChange or Edit.Delete in this release: the review
// plan is taken before the edit's input is known, so a policy rule that depends
// on them never matches. Do not rely on them yet.
//
// Policy will not gain methods, and Edit may gain fields: build an Edit with
// keyed literals.
package review
