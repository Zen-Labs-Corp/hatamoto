# Plugin SDK changelog

Version history of the plugin SDK `hatamoto.dev/sdk`, newest first. The current contract is [PLUGIN_API.md](PLUGIN_API.md); the compatibility promises are in [STABILITY.md](../STABILITY.md). Minor versions are additive unless an entry says otherwise; before 1.0 a breaking change bumps the minor.

## 0.29.0

New: the permission `comments.identity`, the host functions `comments.identity.ensure` and `comments.identity.revoke`, and the `commenter_session` field of a public route response (see [Commenter sign-in by a plugin](PLUGIN_API.md#commenter-sign-in-by-a-plugin-sdk-029)). A plugin that relies on them declares `^0.29`: an older host refuses the unknown permission. The host reports `0.29.0`, so declare `"sdk": "^0.29"`.

## 0.28.1

Additive over 0.28.0. `changeset.entity.upsert` takes `status` (`draft` or `published`, default draft). A create that a standing approval's create rule allows is published with that status; anywhere else it waits for review. It applies only when the upsert creates an entry and is ignored for an entry the key already maps to. A 0.28.0 host refuses the unknown member, so a plugin that sends it declares `^0.28.1`. The host reports `0.28.1`, so declare `"sdk": "^0.28"`, or `^0.28.1` to send `status`.

## 0.28.0

New: the manifest field `standing_approval.create.link_fields` (at most 8 top-level keys of plain `format: uri` fields of the create schemas; in an approved run's automatic creates each may hold one strict `https://` link, and the owner sees the fields on the approval line). A plugin that relies on it declares `^0.28`: an older host ignores the field and its creates with links wait for review.

## 0.27.1

`standing_approval.schemas` and `standing_approval.create.schemas` accept content type slugs (the `entity_schema` rule `^[a-z]([a-z0-9]|-[a-z0-9]){0,39}$`) in addition to identifiers, so a run can hold a standing approval on a type such as `calendar-event`. `standing_approval.fields` keeps the identifier rule. A 0.27.0 host refuses such a manifest at load, so a plugin that names a dashed slug declares `"sdk": "^0.27.1"`. Manifests that were valid under 0.27.0 stay valid.

## 0.27.0

New: the manifest fields `page_query` (at most 16 keys handed to `render.page`), `page_path_prefixes` (at most 8 path prefixes that narrow `render.page` like `page_paths`), `entity_schema` + `entity_fields` (the one schema a plugin may write); the permissions `client.key`, `entities.write.own` and `comments.moderate`; the host functions `entities.create|update|delete`, `comments.mark_dead|restore|set_rank|set_viewer|by_author|subtree`; the `comment.updated` action and `comment.draft` filter; `commenter_key`, `addr_key` and `net_key` on `PageContext` and `HTTPRequest`. A plugin that relies on any of them declares `^0.27`: an older host ignores the fields and refuses the new permissions. The `comment.draft` filter, `comment.updated` and `commenter_key` are live, and `entities.create|update|delete` are implemented (`unavailable` there means the host has no database). The four moderation functions `comments.mark_dead|restore|set_rank|set_viewer` are live too, with `rank`, `status` and `include_dead` on `comments.thread` (see [Comment moderation](PLUGIN_API.md#comment-moderation-sdk-027)), and so are `comments.by_author` and `comments.subtree`. `addr_key`, `net_key` and `area_key` are live too, for plugins granted `client.key`. Treat an `unavailable` answer as "feature off", never as a crash. The new manifest fields and permissions validate and install now. `page_query` and `page_path_prefixes` are read by the host: `render.page` receives `query` and runs on pages under the declared prefixes. The SDK repository carries `v0.27.0` (the earlier `v0.27.0-rc.1` differs from it only in doc comments); the host reports `0.27.0`, so declare `"sdk": "^0.27"`.

## 0.26.0

Additive over 0.25. The manifest field `license` (empty or `commercial` for a new install; when loaded, anything but exact `commercial` is a normal plugin). A commercial plugin is installed and run only by a host whose edition holds a plugin key and entitles it. An older host ignores the field, so a commercial plugin declares `^0.26` to keep those hosts from loading it.

## 0.25.0

Additive over 0.24. The actions `comment.created`, `comment.approved`, `comment.spam` and `comment.deleted`, the filter `comment.check` (it may only add to the score, and any failure holds the comment), the host functions `comments.thread` and `comments.counts`, and the permission `comments.read`.

## 0.24.0

New manifest field `page_paths`: a `render.page` plugin declares the exact public paths it acts on (for example `/members`, `/account`) and is then called, and kept out of the page cache, only for those paths. No existing type or wire output changed. An older host ignores the field and calls the filter on every page, so a plugin on a lower `sdk` range must check `p.Path` itself (declare `^0.24` otherwise). See the manifest table in [PLUGIN_API.md](PLUGIN_API.md).

## 0.23.0

Not purely additive. New permission `entities.filter`. A plugin's `entity.before_save` filter is registered only while the owner holds it: the plugin asks for it in `permissions`, and the owner ticks it on the plugin's Permissions tab. This is not purely additive: a plugin built for `^0.22` or older has its `entity.before_save` filter run only with the grant. Authors add `entities.filter` to `permissions` and declare `^0.23`. Plugins that were installed before the permission existed and register the filter keep it: the first start of this version records the installed plugins (whatever their state), and each one is granted `entities.filter` once, when it is next loaded; the owner can revoke it. A plugin installed or updated afterwards is not kept and must ask for the permission. If you roll back to an older release and upgrade again, plugins installed on the older release in between are not kept. An update that widens access or changes the signer clears the plugin's grants, including a kept `entities.filter`, and a plugin whose new manifest does not ask for it cannot get it back until its author ships a manifest that asks. An update that newly asks for it needs the owner's confirmation and clears the grants, like `site.scripts` (a plugin that already holds it, kept from before, is not asked again). A plugin that asks for it declares `^0.23`, because an older host refuses the unknown permission name.

## 0.22.2

The `ui` block gains `"allow": ["camera"]`. When the owner ticks `ui.allow:camera` on the plugin's Permissions tab, the plugin's admin page may call `hatamoto.camera.capture()` and get one photo from the owner's camera. A host older than 0.22.2 ignores the field and the grant, so a plugin that relies on it declares `^0.22.2`. `permissions()` lists `ui.allow:camera` in `requested` (and in `granted` once ticked) for a plugin that asks, so `granted` stays a subset of `requested`. See [Camera](PLUGIN_API.md#camera).

## 0.22.1

`permissions()` gains `"mail": {"recipients_per_hour": N}` for a plugin granted `mail.send`: the number of recipients the plugin may email per hour. The limit is owner-set per plugin (default 360, range 1 to 3600, on the plugin Permissions tab) and the plugin cannot change it. The key is absent without the grant and on hosts older than 0.22.1, so treat a missing `mail` as unknown and assume the default; `granted` and `requested` are unchanged.

## 0.22.0

New filter `request.redirect` and manifest field `redirect_paths`: a plugin declares exact public paths and, for GET and HEAD requests to them, may answer before routing, stored redirects and the page cache with a same-site 302 or 307 and declared cookies, using its declared cookies and the visitor's country. No existing type or wire output changed. See [request.redirect](PLUGIN_API.md#requestredirect).

`standing_approval.create` also arrives in 0.22: it lets an approved run publish new entries by itself (see [Creating content automatically](PLUGIN_API.md#creating-content-automatically)). It needs `^0.22`; an older host ignores it and creates wait for review.

## 0.21.0

New permission `packages.inspect` and host function `packages.inspect`: download a package from an https URL on one of the plugin's `allowed_hosts` and get its kind, name, version, `sha256`, size, manifest summary and whether a given ed25519 key signed it, reported as `problems` and never as an error for a bad package. New helper package `hatamoto.dev/sdk/registry` with the registry index types and `registry.SignIndex`. No existing type, constant or function changed. See **packages.inspect** in [PLUGIN_API.md](PLUGIN_API.md).

## 0.20.0

`entities.get` and each item of `entities.list` carry `terms`, the entity's term assignments as `[{ kind, slug, name }]`, and `entities.list` takes `term: { kind, slug }` to keep only entities carrying that term. Both need `terms.read` (the key is absent without it), and `terms` is returned only to a plugin whose `sdk` range requires 0.20 or later; plugins built for `^0.19` or older get exactly the bytes they got before. See "Terms (SDK 0.20)" in [PLUGIN_API.md](PLUGIN_API.md). `EntityEvent` does not change.

Also in 0.20: an `items` entry of `builder_sources` can declare up to 8 typed `params` (filters), each query loop stores its own values, and the `builder.data` request carries the validated values as `params`. Additive: a source without `params`, and a loop whose values all drop out, produce the same request bytes as before (same `id`, no `params` key). See `builder_sources` in [PLUGIN_API.md](PLUGIN_API.md) and [builder.data](PLUGIN_API.md#builderdata). Rollback floor: a host older than this change refuses a page whose loop stores `params`.

## 0.19.0

The SDK needs Go 1.27 or newer (`hatamoto.dev/sdk` declares `go 1.27`). It uses the JSON v2 packages: `json.RawMessage` is an alias of `jsontext.Value` in Go 1.27, so SDK types that now name `jsontext.Value` (for example the `args` of `guest.OnFunc`) are source compatible, and the bytes exchanged with the host are unchanged. The guest and the host marshal the SDK types and the types you write (call arguments, results, run summaries) with the v1 behaviours pinned (`omitempty`, `time.Duration` as nanoseconds, byte arrays as number arrays, `MarshalJSON` and `MarshalText` methods, repeated member names inside raw JSON), so your tags need no change; the guest reads these types back with the same v1 behaviours (`time.Duration` from nanoseconds, byte arrays, `,string`, names folded by case only, merging into a filled value, so `kv.get` into the struct you stored works); the one difference is that a struct with two fields sharing a JSON name fails to marshal. On the host, a changeset payload that cannot be encoded returns `invalid_argument` instead of staging `{}`, a repeated member in a `plugin.json` list element loads (the last wins), plugin settings validation rejects invalid UTF-8 that v1 replaced, and plugin manifest reads in the admin packages screen, the updater, the settings CLI and at boot use the same decoder as install. No ABI, host function or manifest field changed; `^0.18` plugins keep loading. Additive: `sdk.RemovedPermissions()` returns the sorted list of removed permissions (the five `*.write` names); the release tool copies it into the signed release record as `removed_permissions` so an update can be refused up front when an enabled plugin still declares one.

## 0.18.0

`request_headers` and `csp` in `plugin.json`; `cookies` reach public routes (`HTTPRequest.Cookies`, `HTTPResponse.SetCookies`); `translation_of` on `changeset.entity.upsert`; `caption` and `credit` on `changeset.media.attach`; `changeset.media.alt` (proposal kind `media_alt`); public content reads (`entities.get`, `entities.list`, `entities.translations`, `media.get`, `media.list`) in the run fetch phase, limited to what an anonymous visitor sees (the same visibility rule as public pages; media only when a public page uses the file); and content types shipped as `schemas/<name>.json` without a section template, created when the plugin is enabled.

Needs `^0.18`. A plugin that uses `csp`, `request_headers`, `translation_of`, `changeset.media.alt` or shipped content types must declare `"sdk": "^0.18"`: an older host ignores those fields without an error and the plugin would run without them.

## 0.17.0

Breaking. Content writes move into **runs** and **changesets** (see [Runs and changesets](PLUGIN_API.md#runs-and-changesets) and [ADR 0004](adr/0004-plugin-changesets.md)), shipped with core 1.1.0. The twelve direct write functions are removed with no shim: `entities.create`, `entities.update`, `entities.set_status`, `entities.delete`, `terms.save`, `terms.delete`, `menus.save`, `menus.add_item`, `menus.delete_item`, `media.upload`, `redirects.create` and `redirects.delete`. So are the five permissions `entities.write`, `terms.write`, `menus.write`, `media.write` and `redirects.write`. Replacements: the `changeset.*` functions for content, `media.import_url` with `changeset.media.attach` for media. A manifest that requests a removed permission is refused at load with a message naming the replacement; a call to a removed function returns `unavailable` naming it. New: the `runs` manifest block, the `on_run_step` export, `run.*`, `upload.read`, `buffer.*`, `media.import_url`, `site.info`, `entities.slug_taken`, `changeset.*`, and the error codes `phase_denied` and `not_owned`. The run and changeset functions (`run.*`, `buffer.*`, `upload.read`, `media.import_url`, `site.info`, `entities.slug_taken`, `changeset.*`) reject arguments nested deeper than 32 levels, arrays of more than 10,000 elements and requests of more than 200,000 JSON tokens with `invalid_argument`; the other functions keep their existing limits (a request is at most 1 MiB). Stored grants for a removed permission are left in place and ignored. The changelog entries below are history: they name functions that 0.17 removed. A plugin built for `^0.5` to `^0.16` that never wrote content loads and runs as before. SDK 0.27 brings direct writes back for one case only: `entities.create`, `entities.update` and `entities.delete` under the new permission `entities.write.own`, limited to the one content type the plugin declares in `entity_schema`. The permission `entities.write` stays removed.

## 0.16.0

The `render.site_graph` filter (see [SiteGraph](PLUGIN_API.md#sitegraph)) lets a plugin add keys to the Organization, LocalBusiness and WebSite nodes of the generic site graph and append nodes of its own. Nothing else changes: a plugin built for 0.15 or older loads and runs as before, and on a host older than SDK 0.16 the hook is never called.

## 0.15.0

Locale support for plugins that manage a multi-locale site. `HeadContext` carries `locale` and `segment`, `SitemapURLs` carries `segment` and `file`, entities carry `translation_group`, `entities.create` takes `translation_of` (and, for a plugin declaring 0.15 or later, needs `locale` on a multi-locale site; `entities.create` is removed since 0.17, and `changeset.entity.upsert` with no `locale` takes the site language, or `und` for a shared type on a multi-language site), the `entities.translations` host function lists a translation group (it needs `entities.read`, no new permission), and `entities.get` with a `path` reads a locale-prefixed path in that locale (only for a plugin whose `sdk` range requires 0.15 or later; `^0.14` and older resolve paths as before). All new payload fields are left out when empty, so a one-language site sends the same JSON as before. A plugin built earlier ignores the unknown fields; on a host older than SDK 0.15 they are absent, and `entities.translations` fails with `unknown_function`.

## 0.14.0

`FormSubmittedEvent` carries `attribution` (`first` and `last` touch: `channel`, `source`, `medium`, `campaign`, `landing`), the classified source of the browser tab that submitted the form. It is left out when none was recorded (no-JS posts, API posts, older submissions). It holds no URL, address or identifier. A plugin built earlier ignores the unknown field; on a host older than SDK 0.14 it is absent.

## 0.13.0

Entity payloads carry `locale` (the entity's BCP 47 tag, for example `en` or `fr`): `EntityRef`, `EntityEvent`, `EntitySummary` and the Entity of `entities.get`/`entities.list` have it, `EntityDraft` has it as an optional field (filled on `entity.before_save` from the entity, and empty, so left out of the JSON, on a create that sent no locale, except that it is `und` for a type shared across languages on a multi-language site; the host never reads it back), and `entities.create` / `entities.update` accept `locale` (empty on create means the site language, or `und` for an entity type marked "Shared across languages" on a site with several languages; a locale you send is kept). SDK 0.13 carries it: no rule requires a bump for an additive field, but a published SDK tree cannot change under the same version, and 0.10 and 0.11 shipped additive payload changes as a minor too. A plugin built earlier ignores unknown JSON fields; on a host older than SDK 0.13 the field is absent. On `entity.published` from a scheduled publish the host looks the locale up; if that fails it is `""`.

## 0.12.0

Browser JavaScript needs a grant: the `site.scripts` permission. Without it a plugin's `assets/plugin.js` is not linked on public pages and the `script-src` tags of its `render.head` filter are dropped (see [Public assets](PLUGIN_API.md#packaging)); the same goes for scripts in its `partials/` and `sections/` markup (see [Template functions, partials and sections](PLUGIN_API.md#template-functions-partials-and-sections)). Additive for the manifest, **behaviour-changing for installed plugins**: a plugin that shipped `assets/plugin.js` before 0.12 stops running its script until its author lists `site.scripts` in `plugin.json` and the owner grants it. `plugin.css` is not gated.

## 0.11.0

A page-builder data source: the `builder.data` filter, the `builder.data` permission and the `builder_sources` manifest field (see [builder.data](PLUGIN_API.md#builderdata) and [WIDGET_PACKS.md](WIDGET_PACKS.md)). Purely additive.

## 0.10.0

The entity host functions can build and read trees (see [Host functions](PLUGIN_API.md#host-functions)): `entities.create` / `entities.update` take `parent_id`; an Entity carries `parent_id`, `path` and `url`; `entities.get` also takes `{ "path" }` or `{ "slug", "parent_id", "schema_slug" }`. Purely additive.

## 0.9.0

`render.page` sends slim blocks by default (see [PageContext](PLUGIN_API.md#pagecontext)): `id`, `type`, `title`, `hidden` and a few scalar `fields`, no content; the host puts the original content back by id. Plugins built for an SDK before 0.9 keep receiving whole blocks, and `render_page.full_blocks` in `plugin.json` opts a 0.9 plugin in.

## 0.8.0

`sitemap.urls` is additions-only (see [SitemapURLs](PLUGIN_API.md#sitemapurls)); a host function refused for a missing grant answers `{"code":"capability_denied","capability":"<permission>"}` (was `permission_denied`; `guest.CapabilityDenied(err)` reads it); and `limits.timeout_ms` is replaced by per-hook `limits.hook_timeouts_ms` (see [Sandbox and limits](PLUGIN_API.md#sandbox-and-limits)). A 0.7 plugin that still returns a whole sitemap list adds nothing; one that tests for `permission_denied` must test `capability_denied`; an old `limits.timeout_ms` is ignored (hooks get the defaults).

## 0.7.0

The `locale` JSON field of `HeadContext` and `SitemapURLs` is now `language` (the locale concept is gone; on `HeadContext` it holds the BCP 47 site language, e.g. `en-AU`). Plugins built for `^0.5`/`^0.6` still load but see an empty `locale`; rename the field and rebuild against `^0.7`.

## 0.6.0

Adds capabilities (permissions, host functions, routes, cron, template funcs) and the `form.submitted` and `render.page` hooks.

## 0.5.0

Replaces native Go plugins with sandboxed WebAssembly modules.
