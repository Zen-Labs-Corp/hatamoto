# Hatamoto Plugin SDK

`hatamoto.dev/sdk` is the plugin contract: hook names and
payload types (`events.go`, `hooks.go`), the `plugin.json` manifest
(`manifest.go`) and the export-name rules of the WASM ABI. The root package depends
on nothing but the standard library, never imports Hatamoto core packages, and the
module has its own `go.mod`.

```sh
go get hatamoto.dev/sdk@v0.16.0
```

Releases are plain `vX.Y.Z` tags of this repository, where `X.Y.Z` is the `Version`
constant in `version.go`. This repository is a one-way export of the `backend/pkg/sdk`
directory of the Hatamoto core repository, which stays the source of truth, so
changes are made upstream in Hatamoto core and exported here. Report SDK bugs and
send proposals as issues or pull requests on this repository
([github.com/Zen-Labs-Corp/hatamoto-sdk](https://github.com/Zen-Labs-Corp/hatamoto-sdk));
a pull request is applied upstream and arrives back in the next release.

`hatamoto.dev/sdk/guest` is the Go guest layer: a thin wrapper
over the Extism Go PDK (`github.com/extism/go-pdk`, the module's only dependency) that provides
`OnAction` / `OnFilter` registration and the JSON plumbing. It only builds the
PDK side for `GOOS=wasip1`.

Everything about writing a plugin (export names, JSON schemas of every payload,
manifest, limits, the Go quick start) is in the plugin documentation on
<https://hatamoto.dev/docs> (the plugin API reference ships with Hatamoto core). The first-party plugins
[gtm](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/gtm) and
[searchconsole](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/searchconsole) are real
examples built on this module.

## Versioning

Semantic, with the pre-1.0 rules in [STABILITY.md](STABILITY.md):
while the SDK is 0.x a minor release may change behaviour, but only after the change was announced as a
deprecation notice in an earlier SDK minor and recorded in the release notes. A patch never breaks a covered surface, except a security exception, which says so in its
release note and advisory. `Version` is the current version; `CompatibleWith` lists every
earlier 0.x minor from 0.5.0, and a host keeps serving all of them until 1.0. A plugin declares the oldest range it
needs in `plugin.json` (today `"sdk": "^0.11"`, so hosts before v0.31.0 still load it); a host refuses a plugin whose range matches neither.

| SDK | Change |
|---|---|
| 0.16.0 | `render.site_graph` filter (`SiteGraph`): add keys to the Organization, LocalBusiness and WebSite nodes of the site graph and append nodes. Purely additive |
| 0.15.0 | `locale` and `segment` on `HeadContext`, `segment` and `file` on `SitemapURLs`, `translation_group` on entities, `translation_of` on `entities.create`, `entities.translations`, locale-prefixed `entities.get` paths (only for plugins requiring 0.15 or later). Additive for plugins on 0.14 or older; a plugin declaring 0.15 or later must pass `locale` to `entities.create` on a multi-locale site |
| 0.14.0 | `attribution` (first and last touch) on `FormSubmittedEvent`. Purely additive |
| 0.13.0 | `locale` field on the entity payloads (`EntityRef`, `EntityEvent`, `EntitySummary`, `EntityDraft`). Purely additive |
| 0.12.0 | `site.scripts` permission: a plugin's browser JavaScript (`assets/plugin.js`) is served and injected only once it is granted |
| 0.11.0 | `builder.data` filter + permission and `builder_sources` manifest field: plugin data sources (values and item lists) for the page builder |
| 0.10.0 | Entity host functions: `parent_id` on create/update, `parent_id`/`path`/`url` on Entity, `entities.get` by `path` or `parent_id`+`slug`(+`schema_slug`) |
| 0.5.0 | Native Go plugins removed. Plugins are sandboxed WASM modules; payloads are JSON with snake_case fields; `guest` package added; no `pgx` or router types |
| 0.4.0 and earlier | Native, compiled-in plugins (gone) |

## Test

```sh
go vet ./... && go test ./...
GOOS=wasip1 GOARCH=wasm go vet ./guest
```
