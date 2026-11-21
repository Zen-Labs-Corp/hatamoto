// Package purge lets a wrapper module tell an edge cache which public pages
// changed. A build that registers nothing sends no purge.
//
// A wrapper module has its own main package that imports a package whose
// init function calls Register, then calls app.Main:
//
//	package main
//
//	import (
//		_ "example.com/yourmodule/cache" // init calls purge.Register
//		"hatamoto.dev/core/app"
//	)
//
//	func main() { app.Main() }
//
// The wrapper module's go.mod needs a replace directive for the plugin SDK,
// because core requires a placeholder sdk version that cannot be resolved
// from outside the repository. Point it at the matching sdk version or
// directory:
//
//	replace hatamoto.dev/sdk => hatamoto.dev/sdk vX.Y.Z
//
// Release builds of a wrapper should set the version and commit explicitly,
// so the binary reports core's release and not the wrapper's:
//
//	go build -ldflags "-X hatamoto.dev/core/app.Version=<core version> -X hatamoto.dev/core/app.Commit=<core commit>"
//
// Without the flags a wrapper build takes the version from its hatamoto.dev/core
// dependency and reports the commit as "unknown".
//
// A Request holds the absolute URLs of changed public pages, or All when the
// owner used Clear cache. Core calls Purge after the change is saved, in the
// background: calls are debounced (about 2 seconds), a Request carries at
// most 500 URLs, and a failed call is retried up to 3 attempts in total. An
// error is logged and never blocks or undoes the change.
//
// In a staging copy (HATAMOTO_ENV=staging) no purge is sent, neither the
// webhook nor a registered purger.
//
// Register must run before app.Main (from an init function); app.Main
// freezes the registration and a later Register panics.
//
// Setting HATAMOTO_PURGE_URL is the no-code alternative: core posts the same
// information as JSON to that URL. Both may be set; each receives every
// purge.
package purge
