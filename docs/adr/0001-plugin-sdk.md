# ADR 0001: Plugin SDK

Status: superseded by [ADR 0002](0002-single-binary.md) (2026-10-14)

> **Superseded.** The native, compiled-in plugin runtime decided here
> (`hatamoto build`, blank imports, `backend/plugins/`, chi/pgx types in the
> SDK) is replaced by sandboxed Extism (WASM) plugins in Hatamoto 1.0; it is
> deleted in slug `v1-16`. `pkg/sdk` survives as the plugin contract (DTOs,
> hook names, manifest). See [`docs/ROADMAP.md`](../ROADMAP.md) `v1-16` to `v1-18`.

## Context

Hatamoto core is open source; proprietary behaviour lived in a private extension repo
that imported core `pkg/plugin` and registered through `internal/*` globals.
That coupled plugins to core internals, had no versioning, no hooks, no
migrations, no settings, and no way to ship UI. We need a stable, versioned
surface so third parties (and that private repo) can extend core without forking it.

## Decision

A separate Go module `backend/pkg/sdk` is the plugin contract; the host in
`internal/pluginhost` adapts it. Headline decisions:

- **Bundles (D1):** loaded at runtime. API serves embedded assets; admin
  dynamic-imports and calls `mount(el, ctx)`; public bundle is a same-origin
  module script. No CSP change, no frontend rebuild per plugin set.
- **Hooks (D2, D14):** typed `Action[T]`/`Filter[T]` tokens over an untyped
  registrar. Actions are async with recover and timeout; filters synchronous,
  failure leaves the value unchanged.
- **Build (D3, D18):** `hatamoto build` generates a temp module importing
  `core/app`, with `replace` to local core and SDK; core is private so a
  checkout is mandatory.
- **render.head descoped (D4)** to the roadmap; no hook is declared that never fires.
- **Events (D5, D13):** emitted by `entity.Service` via an injected sink;
  lead events bridge the existing webhook dispatcher.
- **before_save (D6):** filter where only an explicit `*sdk.Reject` rejects the save;
  other errors/panics are logged and dropped (fail open); result
  re-validated and re-sanitized.
- **jsonld.enrich / sitemap.urls (D7, D8):** synchronous filters at the end of
  generation and before caching respectively.
- **Registration in SDK (D9):** `sdk.Register`, `Plugin{Manifest, Wire}`, IDs as strings.
- **Dependencies (D10):** chi, pgx, stdlib; in-SDK semver range check.
- **Surface (D11, D12):** minimal, sized from a census of the private repo's extension points; typed
  `Extensions` registrars; dead `RegisterCityTupleRegenerator` dropped.
- **Admin routes (review fix):** `AdminAPIMounter` routes mount under `/api/v1`
  behind auth/CSRF and a per-plugin role gate from `Manifest.AdminRoles`
  (default owner-only, fail closed; `"*"` opts into any authenticated user).
  `Deps.RequireRole`/`CallerRole` allow finer per-route checks. Additive.
- **Manifest (D15):** name regexp, SDK range, bad plugins unwired not fatal.
- **Migrations (D16):** goose library with per-plugin table; core migrations
  stay external; `migrate plugins` or `HATAMOTO_PLUGIN_AUTOMIGRATE`.
- **Settings (D17):** `GET|PUT /api/v1/plugins/{name}/settings`, JSON Schema
  2020-12 validation, `writeOnly` masking.
- **Frontend (D20):** schema-driven `PluginSettingsForm` reusing `FieldRenderer`.
- **Docs and process (D19, D21, D22):** SDK README, this ADR, root README.

## Consequences

- Plugins depend only on `pkg/sdk`; core internals can change freely.
- Adding a plugin requires building a custom binary (no runtime loading);
  a WASM tier is a possible later step.
- Private core means `hatamoto build` needs a local checkout until the repo is
  public and the SDK tag is published.
- Known limits: cached sitemap results, read-only entities, no `render.head`;
  tracked as roadmap item 12 (sdk-followups).
- The SDK is 0.x; the surface freezes at 1.0 after real third-party use.

## Update 2026-10-21: public SDK repository

The SDK is published to the public repository `github.com/Zen-Labs-Corp/hatamoto-sdk` as a root
module with plain `vX.Y.Z` tags, resolved through the `hatamoto.dev/sdk` go-import. The module
path is unchanged and core keeps `replace hatamoto.dev/sdk => ./pkg/sdk`, so core stays the source
of truth and the repository is a one-way export (`scripts/publish-sdk.sh`). The module-directory
tag scheme (`backend/pkg/sdk/vX.Y.Z`) described above is no longer used. The first-party plugins
(gtm, searchconsole) left core for their own repositories and build against the public SDK.
