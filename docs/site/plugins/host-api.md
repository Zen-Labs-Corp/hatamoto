---
title: The host API
nav_title: Host API
description: Every function a plugin can call on the server, the permission each one needs, its arguments, its result and its errors.
---

The **host API** is the set of functions a plugin calls to work with your site: read content, stage content changes in a run, store data, send mail or fetch a web address. This page lists the SDK, how a call works, every permission and every function. To make your first call, follow [Keep data and add a route](keep-data-and-routes.md).

A plugin reaches the server through one imported function, `hm_call`, in the wasm module `extism:host/user`. Every capability behind it is checked against a permission the owner granted. The definitions live in `backend/pkg/sdk/hostapi.go` and `permissions.go`.

## SDK version

The contract between the server and a plugin is the Go module `hatamoto.dev/sdk`. The current SDK version is `0.17.0`. The manifest declares the range a plugin was built against, and the server refuses a plugin whose range does not contain a version it serves. This server serves `0.17.0` and also plugins built for `0.5.0` to `0.16.0` (see [The sdk range](manifest.md#the-sdk-range)). Minor versions are additive, and before 1.0 a breaking change bumps the minor.

```go
// backend/pkg/sdk/version.go
const Version = "0.17.0"
var CompatibleWith = []string{"0.5.0", "0.6.0", "0.7.0", "0.8.0", "0.9.0", "0.10.0", "0.11.0"}
```

Fetch it with `go get hatamoto.dev/sdk@v0.17.0`. SDK 0.12.0 ships with Hatamoto `v0.31.0`; until your users run it, declare `^0.11`. The `site.scripts` permission belongs to SDK 0.12.0, but the range you declare does not gate it: hosts from v0.30.0 accept it under any range and enforce it, older hosts reject it as unknown. The import path is a vanity path that points at the public SDK repository, so it does not change if the repository moves. It is a root module with plain tags: version `v0.17.0` is the git tag `v0.17.0`. The SDK root package has no dependencies beyond the standard library, and its `guest` subpackage is the Go layer over the Extism PDK. The Go helpers are `guest.OnAction`, `guest.OnFilter`, `guest.OnHTTP`, `guest.OnCron`, `guest.OnRunStep`, `guest.OnFunc`, `guest.Call`, `guest.Config`, `guest.Log`, `guest.Reject`, `guest.CapabilityDenied` and `guest.Handle`.

## How calls work

The host uses [Extism](https://extism.org) on top of the wazero runtime, so a plugin has no native code, is not compiled into the Hatamoto program and shares no memory with the server. The module is a WASI **reactor**: it has no `_start`, and `_initialize` runs once per instance. The server registers a handler for every hook whose export exists (see [Export names](hooks-and-events.md#export-names)). Input and output are JSON, passed through the Extism input and output buffers. An export returns `0` on success. On failure it sets the Extism error message and returns non-zero.

In Go, `guest.Run(export)` does this: it reads the input, runs the handler registered for that export and writes the output. `guest.Handle(export, input)` runs the same handler without the runtime, so unit tests work on any platform. How the server loads and isolates modules is on [Plugin runtime](../architecture/plugin-runtime.md).

## Permissions

A plugin always loads. What it may do is decided by the owner, and nothing is granted by default. The owner's view comes from `GET /api/v1/plugins`, which lists each plugin's requested and granted permissions. `POST /api/v1/plugins/{name}/grant` with `{"permissions": ["kv", "db"]}` sets the granted set. The list replaces the earlier grant (grant fewer to revoke) and must be a subset of what the manifest requests. Grants live in Postgres and apply at once.

| Permission | Host functions |
|---|---|
| `entities.read` | `entities.get`, `entities.list`, `entities.translations` |
| `terms.read` | `terms.list`, `terms.get` |
| `menus.read` | `menus.list`, `menus.get` |
| `media.read` | `media.get`, `media.list` |
| `redirects.read` | `redirects.list` |
| removed in 0.17 | `entities.write`, `terms.write`, `menus.write`, `media.write` and `redirects.write` no longer exist. A manifest that lists one is refused at load. Content is staged in a run with the `changeset.*` functions, which need no permission (see [Runs and changesets](#runs-and-changesets)) |
| `forms.read` | `forms.list`, `forms.submissions`, and delivery of the `form.submitted` action |
| `settings` | `settings.get` (the plugin's own settings) |
| `log` | `log` |
| `mail.send` | `mail.send` |
| `http.fetch` | `http.fetch`, to the hosts in `allowed_hosts` |
| `http.fetch.any` | lets `http.fetch` honour a `*` entry of `allowed_hosts`; needs `http.fetch` too |
| `kv` | `kv.get`, `kv.set`, `kv.delete`, `kv.list` |
| `db` | `db.query`, `db.exec`, and the plugin's migrations |
| `builder.data` | no function: lets the plugin answer the `builder.data` filter for the sources it declares; without the grant the filter is not registered |
| `builder.island` | no function: needed by a package whose widgets ship an `island.js`, a script that runs on your public pages; without the grant the widgets render but the script is not served |
| `site.scripts` | no function: links `assets/plugin.js`, emits `script-src` tags from `render.head` and lets partials and sections that contain scripts register; runs JavaScript on every public page with full access to the site, including signed-in admins; ungranted plugins' `plugin.js` is not linked and their sections/partials must pass the markup check; warned in the install prompt |

The function `permissions` is always allowed and returns `{ "granted": [..], "requested": [..] }`.

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

A plugin never learns the admin address: no host function returns it. In the admin, the owner grants and revokes on the plugin's **Permissions** tab (`<admin>/plugins/<name>/permissions`). `guest.Log` is not a host function: it writes through the PDK's own log call and needs no permission. Its lines reach the server log tagged with the plugin name, each cut at 4 KiB, with a burst of 20 lines and then 10 per second, and `debug` lines are dropped. The `log` host function below is the permission-checked alternative.

## Calling a function

The request is JSON: `{ "fn": "kv.get", "args": { "key": "k" } }`. The response is either a result or a typed error:

```json
{ "ok": true,  "result": { "found": false } }
{ "ok": false, "error": { "code": "capability_denied", "capability": "kv", "message": "kv.get needs the \"kv\" permission, which the owner has not granted" } }
```

The Go guest wraps this as `guest.Call(fn, args, &result)`. The function names are constants such as `sdk.FnKVGet`. A call with a missing grant returns the typed error and the denied operation never reaches the service behind it. `guest.CapabilityDenied(err)` returns the permission name.

```go
var kv struct{ Found bool `json:"found"` }
err := guest.Call(sdk.FnKVGet, map[string]string{"key": "hits"}, &kv)
if name, denied := guest.CapabilityDenied(err); denied {
	// ask the owner to grant `name`
}
```

### Error codes

| Code | Meaning |
|---|---|
| `capability_denied` | The permission is not granted (the error also has `capability`). |
| `invalid_argument` | Bad or unknown argument, a validation failure or a conflict. Arguments are strict: an unknown field is refused. |
| `not_found` | The target does not exist. |
| `rate_limited` | A per-plugin rate limit was hit. |
| `blocked` | Refused by policy: the `http.fetch` allowlist or address guard, the SQL guard, or database privileges. |
| `reentrant` | No longer returned since 0.17 (plugins do not write directly). |
| `timeout` | The call's deadline passed. |
| `unavailable` | The backing service is not configured, or the plugin's database role is missing. Also a call to a function removed in 0.17; the message names its replacement. |
| `internal` | An unexpected host error. |
| `unknown_function` | No such function. |
| `phase_denied` | The function is not available in the current run phase, or is a run function called outside a run. |
| `not_owned` | A run tried to change an object the owner or another source owns. |

Every call has a deadline of at most 15 seconds and never longer than the hook call that is running. A request is at most 1 MiB and a result at most 2 MiB, so ask for less with `limit` and `cursor`.

## Function reference

| Function | Arguments | Result |
|---|---|---|
| `entities.get` | `{ "id" }`, `{ "path" }`, or `{ "slug", "parent_id"?, "schema_slug"? }` (one selector only) | Entity |
| `entities.list` | `{ "schema_slug"?, "status"?, "search"?, "limit"? (50, max 200), "cursor"? }` | `{ "items", "next_cursor" }` |
| `entities.translations` | `{ "id" }` | `[ { "id", "locale", "status", "path"? } ]`, the entity's translation group ordered by locale, `[]` when it has none or is alone |
| `terms.list` / `terms.get` | `{ "kind"?, "search"?, "limit"? }` / `{ "kind", "slug" }` | `{ "items", "total" }` / Term |
| `menus.list` / `menus.get` | `{}` / `{ "slug" }` | menus / menu with items |
| `media.get` / `media.list` | `{ "id" }` / `{ "search"?, "folder"?, "mime_type"?, "limit"?, "offset"? }` | media |
| `redirects.list` | `{ "search"?, "page"?, "per_page"? }` | `{ "items", "total" }` |
| `forms.list` | `{}` | `{ "items": [ { id, name, slug, status, fields } ] }` |
| `forms.submissions` | `{ "form_slug", "limit"?, "offset"? }` | `{ "items", "total" }`, never with IP or user agent |
| `settings.get` | `{}` | the plugin's settings, schema defaults merged in |
| `log` | `{ "level", "message" }` (level is `debug`, `info`, `warn` or `error`) | `{}` |
| `mail.send` | `{ "to": [1 to 10 addresses], "subject", "text"?, "html"? }` | `{ "sent": n }` |
| `http.fetch` | `{ "method"?, "url", "headers"?, "body"? or "body_b64"?, "timeout_ms"? }` | `{ "status", "headers", "body" or "body_b64", "truncated"? }` |
| `kv.get` / `kv.set` | `{ "key" }` / `{ "key", "value", "ttl_seconds"? }` | `{ "found", "value"? }` / `{}` |
| `kv.delete` / `kv.list` | `{ "key" }` / `{ "prefix"?, "limit"? (100, max 1000) }` | `{}` / `{ "keys" }` |
| `db.query` | `{ "sql", "args"? }` | `{ "columns", "rows", "truncated"? }` |
| `db.exec` | `{ "sql", "args"? }` | `{ "rows_affected" }` |

Notes on specific functions:

- **Term `seo`** is read-only. A Term result carries it; the search appearance of a term is edited on its admin page only.
- **`log`** allows 10 lines per second (burst 20), then `rate_limited`. Messages are cut at 4 KiB.
- **`mail.send`** goes through the site's mail service and From identity, allows 6 recipients per minute (burst 10), and answers `unavailable` when mail is not configured.
- **`kv.set`** values are any JSON up to 256 KiB. A plugin may hold 10,000 keys and 64 MiB of values.
- **`db.query`** returns at most 1,000 rows and 1 MiB. See [Data, routes and cron](data-routes-cron.md).

## Runs and changesets

Content changes are made by a **run** the owner starts. The plugin declares it in the manifest (`runs`, see [The manifest](manifest.md#runs)) and exports `on_run_step`. The host calls the export in two phases, **fetch** (network, the owner's upload and a buffer, no site data) and **transform** (the buffer and the published site, no network), until the plugin calls `run.finish` in both. The plugin **stages** objects into a changeset; nothing is live until the owner publishes it. Runs need no permission. Full contract, including every argument: [Plugin API](https://github.com/Zen-Labs-Corp/hatamoto/blob/main/docs/PLUGIN_API.md#runs-and-changesets).

| Functions | fetch | transform |
|---|---|---|
| `permissions`, `log`, `settings.get`, `run.info`, `run.checkpoint`, `run.progress`, `run.finish`, `run.fail` | yes | yes |
| `http.fetch`, `upload.read`, `buffer.put`, `media.import_url` | yes | no |
| `buffer.get`, `buffer.list`, `site.info`, `entities.slug_taken`, published reads, `changeset.*` | no | yes |
| `kv.*`, `db.*`, `mail.send`, `forms.list`, `forms.submissions` | no | no |

A function not allowed in the current phase fails with `phase_denied`, and so does a run function called outside a run.

| Function | Phase | Arguments | Result |
|---|---|---|---|
| `run.info` | both | `{}` | `{ "run_id", "name", "phase", "cursor", "params"?, "hosts"?, "upload_id"? }` |
| `run.checkpoint` / `run.progress` | both | `{ "cursor" }` / `{ "done", "total"?, "message"? }` | `{}` |
| `run.finish` / `run.fail` | both | `{ "summary"? }` / `{ "reason" }` | `{}` |
| `upload.read` | fetch | `{ "upload_id", "offset", "len" }` (at most 256 KiB) | `{ "data_b64", "size", "eof" }` |
| `buffer.put` | fetch | `{ "key", "value_b64" }` | `{}` |
| `buffer.get` / `buffer.list` | transform | `{ "key" }` / `{ "prefix"?, "after"?, "limit"? }` | `{ "found", "value_b64"? }` / `{ "keys", "next"? }` |
| `media.import_url` | fetch | `{ "url", "filename"? }` | `{ "handle" }` |
| `site.info` | transform | `{}` | `{ "title", "tagline"?, "time_zone", "language", "locales"?, "url_patterns"? }` |
| `entities.slug_taken` | transform | `{ "schema", "slug", "locale"? }` | `{ "taken" }`, published public entities only |
| `changeset.entity.upsert` | transform | `{ "source_key", "schema", "slug"?, "parent_key"?, "translation_of"?, "locale"?, "name", "attributes"?, "seo"? }` | `{ "source_key", "created" }` |
| `changeset.term.upsert` / `changeset.person.upsert` | transform | `{ "source_key", "kind", "slug", "name", "description"? }` / `{ "source_key", "slug", "name", "bio"? }` | `{ "source_key", "created" }` |
| `changeset.media.alt` | transform | `{ "media_id", "alt" }`; the run declares `proposes: ["media_alt"]` | `{ "source_key", "created" }` |
| `changeset.media.attach` | transform | `{ "source_key", "handle", "alt"?, "caption"?, "credit"? }` | `{ "source_key", "created" }` |
| `changeset.menu.upsert` | transform | `{ "source_key", "slug", "name", "items"? }` | `{ "source_key", "created" }` |
| `changeset.redirect.upsert` | transform | `{ "source_key", "from", "to", "status_code"? }` | `{ "source_key", "created" }` |
| `changeset.delete` | transform | `{ "kind", "source_key" }` | `{ "source_key", "created" }` |
| `changeset.propose` | transform | `{ "kind", "payload" }` | `{}` |

- **One active run per plugin.** A failed step, `run.fail` or a crash fails the run and discards its changeset.
- **Hosts.** The owner confirms the hosts at start. In the fetch phase they replace `allowed_hosts` for `http.fetch` (which still needs the `http.fetch` grant) and bound `media.import_url`.
- **Ownership.** A run changes only objects it created, found by `source_key`. An object made by the owner or another source gives `not_owned`.
- **`changeset.person.upsert`** stages a byline, never a user account; it is a term of the byline kind, and is `unavailable` on a site without one.
- **`changeset.propose`** kinds are `site_settings` (title, tagline, time zone, language only), `url_patterns`, `home_page` and `menu_location`, and must be listed in the run's `proposes`.
- **Buffer quota.** 64 MiB, 10,000 keys per run; a value is at most 1 MiB.

## Entity trees and reads

A plugin does not write entities directly. It stages them in a run and the owner publishes the changeset, through the same service as the admin API, so schema validation, HTML sanitising, slug rules and events all apply. `entity.before_save` filters are not run on changeset content yet. Reads are never guarded.

An Entity is `{ id, schema_slug, slug, locale, name, status, version, parent_id?, path?, url?, attributes, published_at?, updated_at }`. `path` is the canonical public path the site serves it at, and `url` is that path behind the public origin when it is known. Staged entities follow the same page rules as MCP: send `attributes.page_doc`, not `page_sections` (see [Behaviour worth knowing](../ai-seo/mcp.md#behaviour-worth-knowing)). A staged entity names its parent with `parent_key` (the source key of an entity staged earlier in the run or created by an earlier run), under the admin's rules: an entity cannot become its own ancestor and the chain may be at most 8 levels deep. `locale` is a BCP 47 tag such as `en` or `fr`. Slugs are unique per schema and locale, so a lookup by `{ "slug", "parent_id", "schema_slug" }` can match one row per locale: the site-language row comes first (`"parent_id": null` means a root), and rows in other locales are reached by `id` or by their locale-prefixed path only. `{ "path": "/a/b" }` resolves a URL with the router the public site uses and finds published entities only; for a plugin declaring 0.15 or later, on a multi-locale site a locale-prefixed path such as `/xx/a/b` is read in that locale and a hidden locale's path is `not_found`; plugins built for `^0.14` or older get no prefix handling. An Entity also carries `translation_group` (SDK 0.15), the id its translations share, absent when it has none (a missing field always means ungrouped; a failed group lookup in `entities.get` or `entities.list` returns `internal`).

## http.fetch

`http.fetch` is the only network a plugin has. In order:

1. The URL host and port must match `allowed_hosts`, else `blocked`. Only `http` and `https` are allowed, with no credentials in the URL and the methods GET, HEAD, POST, PUT, PATCH and DELETE.
2. The host resolves the name itself and every answer must be a public address. Loopback, private, link-local (including cloud metadata addresses), carrier-grade NAT, multicast, reserved ranges and IPv4-in-IPv6 forms are refused, and one bad answer refuses the host.
3. The connection goes to the checked IP, never to the name again, so DNS cannot rebind between check and connect.
4. Redirects are followed up to 5 hops, and each hop repeats all of this.

Request and response bodies are capped at 1 MiB (`truncated` means the response was cut: treat it as an error and ask for less). The default timeout is 10 seconds and the maximum 30, but a call never outlives the 15 second host-call deadline or the running hook's timeout. Cookies and environment proxies are off, and `Set-Cookie` plus other response headers outside a short allowlist are not passed back. Each plugin may make 600 requests per minute (burst 60), tunable with `HATAMOTO_PLUGIN_FETCH_PER_MINUTE`; beyond it calls answer `rate_limited`.

## Cached pages and plugin data

What a plugin renders into a public page (template functions, `render.head` and `render.page` output) is cached with the page for `HATAMOTO_PAGE_CACHE_TTL` (default 5 minutes, allowed 30 seconds to 30 days), including values a function read with `kv.get` or `db.query`. A plugin cannot purge the page cache. New data reaches cached pages when the TTL expires or when anything else clears the cache, such as publishing a changeset, a settings save or a menu change. See [Performance and cache](../admin/performance-cache.md).
