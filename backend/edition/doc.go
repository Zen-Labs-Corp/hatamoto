// Package edition lets a wrapper module extend the hatamoto binary with an
// edition: a name and a feature gate that decides which features and plugins
// are enabled. A community build registers nothing: the edition is
// "community" and every feature check returns false.
//
// A wrapper module has its own main package that imports a package whose
// init function calls Register, then calls app.Main:
//
//	package main
//
//	import (
//		_ "example.com/yourmodule/feature" // init calls edition.Register
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
// A wrapper that sells commercial plugins also calls
// edition.RegisterPluginKey(pub) from the same init function, with the ed25519
// public key those plugins are signed with. The Gate's PluginEntitled is called
// for each commercial plugin on every reconcile (every few seconds), so it must
// be cheap: cache the answer. The CLI has no site id, so a gate that needs
// SiteID denies there.
//
// Register must run before app.Main (from an init function); app.Main
// freezes the registration and a later Register panics. A gate that returns
// an error denies the feature (fail closed). SiteID returns a stable random
// id stored in the database; core never sends it anywhere.
package edition
