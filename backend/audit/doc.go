// Package audit lets a wrapper module observe every audit event core writes.
// A build that registers nothing keeps today's behaviour: core writes its own
// audit table row and nothing else.
//
// A wrapper module has its own main package that imports a package whose
// init function calls Register, then calls app.Main:
//
//	package main
//
//	import (
//		_ "example.com/yourmodule/auditsink" // init calls audit.Register
//		"hatamoto.dev/core/app"
//	)
//
//	func main() { app.Main() }
//
// Core still writes its own audit table; the registered sink is called
// synchronously after that row. tx is the business transaction when core has
// one: an error returned by the sink then rolls the change back. When core
// has no transaction (the change is already committed) tx is nil and a sink
// error is logged. Detail never carries secrets. Do not commit or roll back
// tx.
//
// Register must run before app.Main (from an init function); app.Main
// freezes the registration and a later Register panics. Only one sink may be
// registered.
//
// # Recording from a module
//
// Record writes an event inside tx: the default table row through tx (a
// failed row does not abort tx) and then the registered sink with the same
// tx. Return its error from your transaction function so the change rolls
// back when the sink refuses (errors.Is(err, ErrSink)). Record needs a
// transaction: with a nil tx it returns ErrNoTx and writes nothing; core's own
// writers without a transaction use an internal recorder bound to core's
// database.
//
//	err := audit.Record(ctx, tx, audit.Event{
//		Actor:     "reports",
//		ActorKind: audit.KindPlugin,
//		Action:    "report.export",
//		Target:    "report:42",
//	})
package audit
