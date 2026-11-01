---
title: Plugin runtime
nav_title: Plugin runtime
description: How the server runs plugins in a closed space: what they may do, their limits, their own database space, outbound requests and signing.
---

This page describes what the server does with a plugin once it is installed: the layers it runs through, the permissions and limits it gets, its own database space and how it reaches the network. A **plugin** here is a WebAssembly module, a portable compiled program the server runs in a **sandbox**, a closed space where it can do only what its permissions allow. To write a plugin, start with the [plugins section](../plugins/index.md).

## Why a sandbox

Hatamoto plugins are not compiled into the server and they do not run as trusted Go code. Each plugin is a WebAssembly module executed by [Extism](https://extism.org) on the pure-Go `wazero` runtime. An earlier design linked native Go plugins into a custom build, which needed a Go toolchain, gave plugins full access to the database and process, and could not be installed with a click. The sandboxed design keeps one binary, needs no compiler on the server, and limits a plugin to what the owner granted.

The plugin contract is a small Go module, `hatamoto.dev/sdk` (version `0.16.0` in `backend/pkg/sdk/version.go`; plugins built against 0.5 to 0.15 are still accepted, and `hatamoto version --json` prints both). It holds the manifest format, hook names, payload types, the permission list and the export-name rules. It is published as the public repository `Zen-Labs-Corp/hatamoto-sdk` (plain `vX.Y.Z` tags; core is the source of truth). Its root package depends only on the standard library, so the server and guest code share it. Plugins are written in standard Go with the SDK's `guest` package, or in any language with an Extism plugin kit. How to write one is in the plugins section; this page describes what the server does with it.

## Layers

```text
 admin installs a .hmp package  (signature checked, bytes stored in Postgres)
          |
          v
 pluginhost    loads manifest + wasm, registers hooks, routes, cron, template funcs,
               grants, settings, per-plugin database, http.fetch guard
          |
          v
 hooks engine  typed actions (async) and filters (synchronous, ordered by priority)
          |
          v
 wasmhost      Extism + wazero: instance pools, memory caps, timeouts, circuit breaker
          |
          v
 plugin.wasm   guest code, no filesystem, no network, only host functions it was granted
```

## What a plugin can do

A plugin declares in `plugin.json` which hooks it handles, which permissions it needs, which routes, cron jobs, template functions and admin page it provides, and its resource limits. The server maps **exports** of the module onto those declarations using a naming rule: the hook name with every `.` replaced by `_`, prefixed `on_` for actions and `filter_` for filters.

| Kind | Hooks |
|---|---|
| Actions (fire and forget) | `entity.created`, `entity.updated`, `entity.published`, `entity.deleted`, `import.completed`, `agent.hit`, `agent.first_crawl`, `form.submitted` |
| Filters (return a modified value) | `jsonld.enrich`, `sitemap.urls`, `render.head`, `render.site_graph`, `render.page`, `entity.before_save`, `builder.data` |
| Other exports | `http_handle` (routes), `on_cron_<job>` (scheduled jobs), `func_<name>` (template functions) |

**Actions** run in their own goroutine with panic recovery and a timeout, and cannot delay the request that triggered them. **Filters** run synchronously in ascending priority order and each receives the previous filter's result. A failing, timing-out or oversized filter leaves the value unchanged, so a broken plugin degrades its own feature, not the site. The only filter that can block something is `entity.before_save`, and only by returning an explicit rejection (an error starting with `hatamoto:reject:`); any other error is logged and ignored. `builder.data` answers the data requests of the visual editor's blocks for the sources the plugin declares, once per page render; it is registered only with the `builder.data` permission, and a failure leaves those values empty.

## Permissions

Nothing is available to a plugin by default. The manifest lists the permissions it needs and the owner grants them, possibly a subset, when installing. A host function called without its grant fails with a `capability_denied` error and the plugin carries on. The set is:

| Permission | Allows |
|---|---|
| `entities.read`, `entities.write` | read or change content |
| `terms.read`, `terms.write` | tags and categories |
| `menus.read`, `menus.write` | navigation menus |
| `media.read`, `media.write` | media records |
| `redirects.read`, `redirects.write` | redirect rules |
| `forms.read` | form definitions and submissions |
| `settings` | the plugin's own settings |
| `log` | write to the server log |
| `mail.send` | send mail through the site's SMTP settings |
| `http.fetch` | outbound HTTP to hosts listed in the manifest's `allowed_hosts` |
| `http.fetch.any` | outbound HTTP to any public address; a separate permission so the install prompt can warn about it |
| `kv` | a small key-value store |
| `db` | SQL on the plugin's own schema |
| `builder.data` | answer data requests for the plugin's declared builder sources |
| `builder.island` | serve the `island.js` script of the plugin's widgets on public pages, where it runs with the visitor's privileges; without it the widget renders but its script is not served |
| `site.scripts` | link `assets/plugin.js` and emit `script-src` tags from `render.head`; runs JavaScript on every public page with full access to the site, including signed-in admins; ungranted plugins' `plugin.js` is not linked and their sections/partials must pass the markup check; warned in the install prompt |

A plugin update that newly asks for a permission, or for `http.fetch.any`, is reviewed again by the owner.

## The sandbox

`wasmhost` applies these limits to every plugin (the defaults come from `backend/pkg/sdk/manifest.go`):

| Limit | Default | Allowed range | Notes |
|---|---|---|---|
| Memory per instance | 256 pages (16 MiB) | 64 to 1024 pages | a standard Go guest does not start below 4 MiB |
| Filter and template func timeout | 250 ms | up to 10 s | per hook via `limits.hook_timeouts_ms` |
| Action, cron and route timeout | 10 s | up to 10 s | zero is refused at load |
| Concurrent instances | 4 | 1 to 16 | also bounds live memory |
| Output size per call | 1 MiB | up to 4 MiB | larger output fails the call |

On top of the manifest limits the host enforces fixed ones: no filesystem, no network except through `http.fetch`, a 64 KiB variable store, guest log lines truncated at 4 KiB and rate limited (a burst of 20 lines, then 10 per second; the rest are dropped), and an instance that saw any error is discarded because a trapped Go runtime is poisoned. A manifest may declare at most 64 routes, 16 cron jobs (each at most once a minute), 64 template functions and 8 cookies.

A request-path call (filter or template function) waits at most two seconds for a free instance slot, and a cold start is bounded to five seconds.

**Circuit breaker.** A hook export that traps or times out three calls in a row is skipped for 30 seconds, per plugin and per export. A failed template function renders blank, and a page that depended on a degraded plugin is served but not cached.

**Compilation cache.** Compiled code is cached on disk under `HATAMOTO_PLUGIN_CACHE_DIR` (default `$HATAMOTO_DATA/cache/wasm`, mode 0700 because it holds native code), so a restart does not recompile every module. The cache is rebuildable; `HATAMOTO_PLUGIN_CACHE_DIR=off` turns it off.

**Hot loading.** The registry of loaded plugins is an immutable map behind an atomic pointer. Installing, updating or removing a plugin swaps the pointer, so the request path takes no host-wide lock and no restart is needed.

## Routes

Plugins cannot register routes with the main mux, since it is fixed at boot. Instead two catch-alls exist:

- `/p/{plugin}/...` for **public** routes, anonymous and rate limited (10 requests per second per IP, burst 30);
- `/api/v1/p/{plugin}/...` for **admin** routes, behind session or key authentication and CSRF, with a per-route role gate.

Each plugin has its own small router in an immutable map, so changing a plugin's routes is a pointer swap. Built-in paths are never shadowed. Only a short allowlist of headers passes, bodies are capped at 1 MiB, and every response carries a `sandbox` Content-Security-Policy. `/p/{plugin}/assets/` serves the static files shipped in the package.

## Plugin data: schema and role per plugin

A plugin that holds `kv` or `db` gets its own space in Postgres:

- **Schema** `plugin_<name>` (a hyphen in the name becomes an underscore).
- **Login role** `hm_p_<id8>_<name>`, where `<id8>` is the first 8 hex digits of this database's id from `hatamoto_instance`. Postgres roles are cluster-wide, so including the database id lets several Hatamoto databases share one cluster safely.
- **Own connection pool**, two connections (the role may hold four so a replaced pool can drain), with a random password stored in `hatamoto_plugin_credentials`.
- **Role settings**: `statement_timeout` 10 s, `idle_in_transaction_session_timeout` 15 s, `lock_timeout` 2 s, and `temp_file_limit` 64 MB when the provisioning user is allowed to set it (superuser only).

Plugin SQL runs only on the plugin's own pool, whose **session user** is the plugin role. `SET ROLE` and similar tricks can only land on the plugin's own role, which belongs to nothing and owns nothing outside its schema. Server code never uses a plugin pool and plugin SQL never runs on the server's own pool, so nothing a plugin leaves in a session can reach a core query. An SQL guard additionally restricts statements (one DML statement per call, and one table-shaped DDL statement in a migration) and refuses functions that run SQL strings; it is defence in depth, and the tests run the escape suite with the guard switched off to prove the database boundary does the work.

Each call returns at most 1000 rows or 1 MiB, with at most 64 parameters and 64 KiB of SQL. Plugin migrations ship in the package as `migrations/NNN_name.sql`.

**Operator requirements.** Creating roles needs a user with `CREATEROLE` or the role-admin function from `deploy/sql/role-admin-bootstrap.sql`, and `pg_hba.conf` must allow password authentication for the plugin roles, because the plugin pool connects with a password. If either is missing, `kv` and the rest of the site keep working and `db` reports the role as unavailable. Objects open to `PUBLIC` that the application role does not own also block `db` until they are closed; `HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1` accepts the exposure.

## Outbound HTTP

`http.fetch` is the only network access. The host checks the URL against `allowed_hosts`, resolves the name itself, rejects non-public addresses, connects to the verified address and repeats this for each of at most five redirects. Bodies are limited to 1 MiB and calls to 10 seconds by default (30 at most). Each plugin has a budget of `HATAMOTO_PLUGIN_FETCH_PER_MINUTE` requests per minute, default 600. See [Auth and security](auth-and-security.md) for the shared guard.

## Packages and signing

A plugin or theme is distributed as a `.hmp` zip with a manifest, `plugin.wasm`, optional templates, `assets/`, `migrations/` and `ui/`. Packages are stored in `hatamoto_packages`, verified by SHA-256 digest, and signed with **ed25519**. The server trusts the first-party project key plus any keys you list in `HATAMOTO_TRUSTED_KEYS` (base64 ed25519 public keys, separated by commas or spaces); `HATAMOTO_TRUST_FIRST_PARTY=false` drops the first-party key. An unsigned package is refused unless the owner allows it in **Admin > Plugins**. A package whose signer is unknown or whose signature fails is always refused. Plugins marked live are loaded at boot. See [Packaging and signing](../plugins/packaging-signing.md).
