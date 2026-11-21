// Package audit lets a wrapper module observe the events core writes to its
// audit table, hatamoto_admin_audit. MCP tool calls are not included: they go
// to hatamoto_mcp_audit and do not reach the sink. A build that registers
// nothing keeps today's behaviour: core writes its own audit table row and
// nothing else.
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
// synchronously after core has attempted that row, and also when the row
// failed. A changeset publish and signing everyone out call the sink inside
// their transaction: tx is that transaction and an error returned by the sink
// rolls the change back. The sink is the last step before COMMIT, so a sink
// that writes outside tx can record an event whose commit then fails. Every
// other core writer (admin saves, packages, imports, the CLI) records after
// its change committed: tx is nil, the sink's ctx is not cancelled by the
// request, and a sink error is only logged. A publish is one event, written
// by the changeset engine. Known secret keys in Detail are replaced with
// [redacted] before the table row and the sink see it (core's log line is not
// redacted). Do not commit or roll back tx. A sink's error text can reach the
// caller of the write, so keep internal addresses out of it.
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
//
// # Evolution
//
// Sink has one method and stays that way. New capabilities arrive as optional
// interfaces that core type-asserts on the registered sink, so an existing
// sink keeps compiling. Event gains fields over time: build it with keyed
// fields, never positionally.
package audit
