# ADR 0007: Module extension points

Status: accepted (2026-11-21).

## The rules in plain words

1. A wrapper module is a Go program with its own `main` that imports core and
   calls `app.Main`. It extends core only through public packages under
   `hatamoto.dev/core/module/...`. It never imports `internal/`.
2. Every extension point is registered from an `init` function. `app.Main`
   freezes all registries before it does anything else; a later registration
   panics.
3. Every extension point has a no-op default. With nothing registered, the
   community binary behaves exactly as before: same output, same headers, same
   help text, same schema.
4. Every extension point fails closed. A missing registration, a nil, an error
   or a recovered panic from module code means the behaviour core had without
   the module.
5. Core stays neutral. No product checks, edition names or vendor wording live
   in these packages.

## Context

[`edition`](../PLUGIN_API.md) already lets a wrapper module name itself and
answer feature questions. A module that adds real functionality (a command, its
own tables, sealed credentials, a background worker) needs more than a
gate, and wasm plugins are the wrong tool: they are sandboxed, per-install
packages, not compiled-in code with database access.

Calling `internal/` is impossible across modules, so core publishes small,
frozen seams instead.

## Trust model

A wrapper module is compiled into the binary and is fully trusted code. It
runs in the core process with the core database role and the same process
environment, so it can read and change anything core can. The hooks below
shape the API (separate key domains, a separate migrations table); they are
not a sandbox, and the key domain prefix prevents accidental key reuse, not a
malicious module. Install only modules you trust as you trust core.

Every table, index, type or function a module creates must be named with the
prefix `module_<name>_` or live in its own schema, and a module never alters
core objects. A bare name such as `reports` can collide with a table a later
core release adds, and a failing core migration stops `serve`.

## Decision

State lives in one internal package, `internal/modhost`, with one file per
hook. Each public package is a thin wrapper (types are aliases of the
`modhost` types, as `edition.Gate` is) with a `doc.go`, test hooks in
`export_test.go` and an external test package. Names are
`^[a-z][a-z0-9_]{0,30}$`.

### Process and infrastructure hooks (`module/...`)

| Package | What it gives a module | Default |
|---|---|---|
| `module/cli` | `Register(Command{Name, Usage, Run})`: a new `hatamoto <name>` command with a database pool, logger, data directory and output streams; `Exit(code, err)` picks the exit code. Core command names always win. | Unknown commands print usage and exit 2 |
| `module/migrate` | `Register(module, fsys)`: embedded goose SQL migrations. They run after core in `serve` and in `hatamoto migrate`, on the same advisory lock, and are recorded in `module_<name>_goose_db_version`, never in `goose_db_version`. `migrate status` lists them. `Err(module)` reports the last run. | No module tables |
| `module/secret` | `Seal`/`Open(module, purpose, aad, ...)`: authenticated encryption with a key derived under the fixed domain `module/<module>/<purpose>`. The server secret is installed by core and not handed out by this API; core keys cannot be derived through it. | `ErrNoSecret` |
| `module/service` | `Register(module, run)`: a background function started by `serve` (never by a CLI command) after migrations and before the listener accepts connections, in every serve process. A module name registers at most one service; a duplicate panics. Its context is cancelled at shutdown. Panics are recovered and logged; there is no restart loop. | None started |
| `module/db` | `Pool()`: core's own database pool in `serve` (from database open to shutdown) and in module commands (the same pool as the command's `DB`). Core publishes and clears it; modules cannot replace or close it. | `ErrNotReady` |

A failed module migration is logged at error level and recorded. `serve`
continues (an optional module must not take the site down) and the services of
that module are not started; `hatamoto migrate` exits non-zero.

A core command word added in a later release shadows a module command of the
same name, because core names always win.

## Consequences

- The public packages are a contract (see "Go module extension points" in
  `docs/PLUGIN_API.md`): names and behaviour are frozen until the next major
  release.
- A module gets its own migrations table, its own secret domain and the
  services it registered. That is a convention, not isolation: see "Trust
  model".
- HTTP routes, jobs and mail for modules: ADR 0012. Admin pages: ADR 0008.
