---
title: Stack and package map
nav_title: Package map
description: The technology Hatamoto is built on, the order the server starts in, the design rules behind the code and what each Go package does.
---

This page lists what Hatamoto is built with, the order `hatamoto serve` starts its parts in, the design rules the code follows, and the job of every package in the source tree. For a guided look at a running server, follow [See how Hatamoto is put together](overview.md).

## Overview

Hatamoto is one Go program, `hatamoto`, plus one Postgres 16 database. There is no Node runtime, no Redis, no search server and no separate worker process. The single-program decision is recorded as an architecture decision in the repository (`docs/adr/0002-single-binary.md`). Its goal: a person with one file and a database has a working site in minutes, and plugins do not need a compiler.

```text
hatamoto (one Go program)                       Postgres 16
 |- public site     html/template themes         |- content: entities, schemas, terms, media records
 |- admin           html/template + htmx         |- users, sessions, API keys
 |- JSON API        sessions or API keys         |- plugin and theme packages
 |- MCP server      /mcp                         |- plugin_<name> schema + login role per plugin
 |- worker          in-process scheduler         '- analytics rollups, audit logs
 |- migrations      embedded, run on boot
 '- plugins         Extism (wazero) WASM sandbox      media: files on local disk
```

One install is one site in one language. There are no per-language address prefixes or per-language settings. The site has a single `language` setting that feeds `<html lang>`, structured data and feeds. See [One site, one language](../getting-started/one-site-one-language.md).

## Stack

| Layer | What Hatamoto uses |
|---|---|
| Language | Go 1.27 (`go 1.27`, `toolchain go1.27.3` in `backend/go.mod`) |
| HTTP routing | the standard library `net/http.ServeMux`, plus a small route-group helper in `internal/httpx` |
| Database driver | `pgx/v5` |
| Migrations | `goose/v3` used as a library, embedded in the program |
| Public rendering | `html/template` with layered themes |
| Admin rendering | `html/template` embedded with `go:embed`, enhanced with htmx |
| Rich text | Tiptap (a vendored ES module bundle) |
| HTML sanitising | `bluemonday` |
| Plugins | Extism on wazero, WebAssembly, no cgo |
| Caching | in-process memory cache for rendered HTML, sitemaps and site data |
| Logging | `log/slog`, plus `/health` and `/ready` probes |
| Email | SMTP, optional |

The Docker image builds on `golang:1.27-alpine` and runs on `debian:bookworm-slim`. The compose file in the repository uses `postgres:16`. Versions and licences of every module are on [Dependencies](dependencies.md).

## Start-up order

Everything is started by one command. `DATABASE_URL` is the only setting it requires:

```bash
export DATABASE_URL='postgres://hatamoto:secret@localhost:5432/hatamoto?sslmode=disable'
hatamoto serve
```

`hatamoto serve` runs these steps in order (`serve` in `backend/app/app.go`):

1. Make sure a server secret exists. If neither `HATAMOTO_SECRET` nor `JWT_SECRET` is set, it reads or generates one in `secrets.env` under `HATAMOTO_SECRETS_DIR` (default `./data/secrets`).
2. Load and validate the configuration from environment variables.
3. Connect to Postgres.
4. Apply every pending migration (embedded in the program). See [Database migrations](database-migrations.md).
5. Import update events the supervisor recorded, and start watching for new ones.
6. Run the first-boot step for a fresh database (for example the starter home page).
7. Build the server: services, plugin runtime, routes, render engine, update agent.
8. Start the scheduler and the other background loops, then start listening. See [Worker and background jobs](worker-and-background.md).

`hatamoto supervise`, the default command of the Docker image and of the systemd unit in `deploy/systemd/hatamoto-api.service`, runs `serve` as a child process and switches between releases. It is described in [The supervisor](../updates/supervisor.md).

## Design rules

**The route table is fixed at boot.** The `ServeMux` is filled once and never changes while the server runs. Anything dynamic is resolved inside a fixed catch-all: public content goes through `GET /{rest...}` and the URL-pattern registry, and plugin routes live under `/p/{plugin}/...` and `/api/v1/p/{plugin}/...`. Print the fixed table with `hatamoto routes` (no database needed).

**Content is untrusted.** Every write of rich text or HTML goes through server-side sanitising policies before it is stored, and again at render time. The browser editor is never the source of truth for safety. See [Auth and security](auth-and-security.md).

**Admin pages work without JavaScript for reading.** Server-rendered HTML is the base; htmx and a few vendored scripts only enhance editing.

**One way to do each thing.** When a feature replaces another, the old path is deleted. There is no dual rendering path and no feature flag keeping old code alive.

**No cgo.** The program is built with the Go toolchain alone, so cross-compiling to Linux and macOS on amd64 and arm64 works from one machine.

## Packages under `backend/internal`

The server code is grouped by job. Alphabetical:

| Package | Job |
|---|---|
| `admin` | the server-rendered admin UI: `html/template` pages from `backend/web`, htmx, the same session cookie and stores as the JSON API |
| `agents` | the registry of known AI crawlers and assistants, crawler hit capture, verification against published IP ranges, robots policy, access log tailing |
| `analytics` | pageviews and AI-assistant referrals, rolled up per day; no visitor or per-event rows |
| `auth` | passwords (argon2id), login lockout, one-time user tokens, API keys and scopes |
| `author` | the older authors table, kept so a rollback still renders bylines; bylines now come from `person` |
| `builder` | the visual editor's page document model: layout node tree, validator, edit operations, style vocabulary, conversion of older section pages |
| `cache` | the in-process TTL cache for rendered HTML, sitemaps and site data; lost on restart |
| `config` | configuration loading from environment variables |
| `database` | the Postgres connection pool |
| `email` | SMTP sending and embedded mail templates |
| `entity` | content types, entities, versions, sanitising, the URL registry and JSON-LD generation |
| `form` | form definitions and submissions |
| `hooks` | the host-side engine behind the plugin SDK's typed actions and filters |
| `httpx` | route groups over `ServeMux` with middleware, and the record of every pattern behind `hatamoto routes` |
| `importjob` | the admin's WordPress import wizard as resumable background jobs |
| `linking/v2` | the internal link index, link rules, rewriter and preview |
| `mcpserver` | the Model Context Protocol endpoint at `/mcp`, calling the same services as the HTTP API |
| `media` | uploaded files, their records and generated variants |
| `menu` | navigation menus and items |
| `middleware` | authentication, API key route allowlist, CSRF, rate limits, request logging and recovery |
| `packages` | the `.hmp` package format, ed25519 signing, the signed registry index and the store that installs and activates packages |
| `person` | the read model of people (entities of the built-in `person` type) |
| `pluginhost` | connects the plugin runtime to the rest of the server: hooks, routes, cron, template functions, settings, the per-plugin database and the `http.fetch` guard |
| `portability` | the full-site archive behind `hatamoto export` and `hatamoto import` |
| `redirect` | redirect rules, wildcard patterns, the built-in older-address families and the "explain this address" resolver |
| `release` | the signed release record and the signed list of current releases per update channel |
| `render` | the theme engine, template functions, widgets, sitemap and robots output |
| `safefetch` | the guarded outbound HTTP client shared by plugins and the WordPress importer |
| `secretbox` | seals small secrets at rest with AES-256-GCM, keyed from the server secret |
| `sectiontemplate` | reusable page sections saved by editors |
| `server` | wires services and routes; public, API and admin handlers; security headers; the page cache |
| `session` | the server-side store for admin sessions |
| `siteconfig` | the single-row site configuration |
| `spam` | site-wide spam settings and the checks run on every public form submission |
| `supervisor` | runs the server as a child process, picks the release it runs, checks every switch and rolls back on its own |
| `taxonomy` | terms (tags, categories and custom kinds) across every content type |
| `testutil` | small assertion helpers shared by tests |
| `updater` | the update agent: reads the signed list of releases, picks a target under policy and stages it for the supervisor |
| `urlpattern` | the URL pattern language for per-type addresses: parse, render, match |
| `user` | user accounts and roles |
| `wasmhost` | runs plugins as Extism (wazero) modules in a sandbox |
| `webhook` | signed outgoing webhooks and their delivery queue |
| `worker` | the in-process scheduler and its maintenance jobs |
| `wpimport` | the WordPress importer: REST API or WXR file, scan report, runner and address audit |

`supervisor`, `updater` and `release` are covered in [How updates work](../updates/index.md).

## Other directories under `backend`

| Path | Job |
|---|---|
| `cmd/hatamoto` | the `main` package of the `hatamoto` program |
| `app` | the command line: `serve`, `migrate`, `user`, `update` and every other subcommand |
| `migrations` | the embedded SQL and Go migrations |
| `pkg/sdk` | the plugin contract `hatamoto.dev/sdk`, a separate module with no dependencies beyond the standard library |
| `web` | admin templates, CSS and vendored browser files, embedded in the program |
| `tools/changelog` | creates and updates release entries on the changelog |
| `tools/docs-publish` | publishes these docs to a running site |
| `tools/fakesite` | a static file server that logs each request, used by the update end-to-end test |
| `tools/gen-icons` | the development-only script that generates the admin icon map |
| `tools/licenses` | scans the licences of every Go module in the build |
| `tools/release` | makes and checks signed releases; never linked into `hatamoto` |
| `tools/vendor` | fetches and verifies the vendored browser files |
