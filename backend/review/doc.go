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
// Core does not set Edit.SlugChange or Edit.Delete in this release: the review
// plan is taken before the edit's input is known, so a policy rule that depends
// on them never matches. Do not rely on them yet.
//
// Policy will not gain methods, and Edit may gain fields: build an Edit with
// keyed literals.
package review
