// Package review lets a wrapper module decide the review stages that a human
// edit must pass before it is published. A build that registers nothing uses
// the default: no stages while the site's review switch is off, and one
// "Review" stage for the "reviewers" group while it is on.
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
package review
