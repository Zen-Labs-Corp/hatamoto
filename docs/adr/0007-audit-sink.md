# ADR 0007: Audit sink

Status: accepted (2026-11-21)

## The rules in plain words

1. Every audit event core writes goes through one recorder. There is one event
   type and one table, `hatamoto_admin_audit`.
2. With nothing registered, behaviour is what it was: the same rows with the
   same actor, action, target and detail, written best-effort.
3. A module can register one sink. It is called in addition to the table, never
   instead of it, and synchronously.
4. Where core has a business transaction (a changeset publish, signing everyone
   out), the sink runs inside it. A sink that returns an error rolls that change
   back. Where the change is already committed, the sink's error is logged.
5. A failed default table row never aborts the business transaction. The row is
   written under a savepoint.
6. Secrets never reach the log: known secret keys in an event's detail are
   replaced with `[redacted]` before anything is written.

## Context

Core had six places that inserted into the audit table by hand, a changeset
publish that was audited from two or three layers at once, and no way for a
compiled-in module to see events or to refuse a change it could not record.
Operators who need to ship audit events elsewhere, or to refuse a write when the
record cannot be kept, had nothing to hook into.

## Decision drivers

- Default behaviour unchanged: no new rows, no new action names, no new failure
  modes for a build that registers nothing.
- A registered implementation fails closed. A write it cannot record is refused
  where core has a transaction to roll back.
- Same extension pattern as `edition`: a public package with its own types, a
  registration from `init`, frozen by `app.Main`.
- The admin Audit log page keeps reading the table, so the table stays.

## Decision

### 1. A public package

`hatamoto.dev/core/audit` has `Event`, `Sink` (`Record(ctx, tx pgx.Tx, Event)
error`), `Register` and `Record`. `Register` is for `init` functions only and
panics after `app.Main` started, on a second call and on a nil sink. The package
documentation has the wrapper `main` example.

An event carries the actor (display name, kind `user`, `api_key`, `plugin`,
`system` or `cli`, and an API key id), the action, the target, a detail map, the
request fields (address, user agent, request id) and a result (`ok`, `denied` or
`error`).

### 2. `Record` needs a transaction

`audit.Record(ctx, tx, event)` writes the default table row through `tx`, under
a savepoint, and then calls the registered sink with the same `tx`. A sink error
comes back wrapped in `audit.ErrSink`: return it from the transaction function
so the change rolls back. A failed table row comes back wrapped in
`audit.ErrTable`; the transaction is still usable and the caller decides.

With a nil `tx` it returns `audit.ErrNoTx` and writes nothing. There is no
process-wide database handle behind the public function. Core's own writers
without a transaction use an internal recorder bound to core's database pool.
The alternative, a pool-backed recorder installed at boot, was rejected: it is
global mutable state, a test could write rows into another test's database, and
it needs boot wiring. Accepting a nil `tx` later is an additive change.

### 3. The sink is called in addition to the table

The table row is written first, then the sink. One sink may be registered;
allowing several later is additive.

### 4. Which writes are transactional

A changeset publish writes its `changeset.publish` row inside the publish
transaction, after the items are applied, so a sink that refuses leaves the
entities and the changeset as they were. Signing everyone out does the same.
All other writers record after their change committed. A sink error there is
logged at error level, because a committed write cannot be undone, except where
the writer already returns errors.

The publish is audited once, by the changeset engine. The admin handler and the
review service no longer write a second row for it.

### 5. A wider table

`hatamoto_admin_audit` gains `actor_kind`, `actor_key_id`, `ip`, `user_agent`,
`request_id` and `result`, plus indexes on `(action, at DESC)`,
`(actor_id, at DESC)` and `(target)`. The migration only adds columns with
defaults and creates non-unique indexes, so the previous binary keeps working
against the new schema. `ip` is text, not `inet`: an unparsable value must never
make an audit insert fail. The user agent is clamped to 512 bytes and the
request id to 128. Request fields are filled for admin requests only.

The actor kind is derived from the actor strings core already stores. The key id
column exists but stays empty for now.

## Consequences

- Index creation takes a write lock on the table during the upgrade. With a
  one-year retention the table is small.
- A registered sink on a post-commit writer is not fail-closed. Widening the
  transactional coverage is follow-up work.
- A module that passes a transaction from another database gets `ErrTable`, not
  a row in core's table.
- Detail redaction is an exact, case-insensitive key list on the top level of
  the detail map. Callers still must not put secrets in detail.
