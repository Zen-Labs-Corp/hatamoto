# Plugin API (SDK 0.27)

Compatibility promises for everything on this page: [STABILITY.md](../STABILITY.md).

> **0.27 changes (additive over 0.26).** New: the manifest fields `page_query` (at most 16 keys handed to `render.page`), `page_path_prefixes` (at most 8 path prefixes that narrow `render.page` like `page_paths`), `entity_schema` + `entity_fields` (the one schema a plugin may write); the permissions `client.key`, `entities.write.own` and `comments.moderate`; the host functions `entities.create|update|delete`, `comments.mark_dead|restore|set_rank|set_viewer|by_author|subtree`; the `comment.updated` action and `comment.draft` filter; `commenter_key`, `addr_key` and `net_key` on `PageContext` and `HTTPRequest`. A plugin that relies on any of them declares `^0.27`: an older host ignores the fields and refuses the new permissions. `entities.create|update|delete` are implemented (`unavailable` there means the host has no database). The four moderation functions `comments.mark_dead|restore|set_rank|set_viewer` are live (see [Comment moderation](#comment-moderation-sdk-027)). Until the change that implements them ships, each other new host function answers `unavailable` (`<fn> is not available`), each new hook is registered but not called, and the new payload fields are absent: treat `unavailable` as "feature off", never as a crash. The new manifest fields and permissions validate and install now. `page_query` and `page_path_prefixes` are read by the host: `render.page` receives `query` and runs on pages under the declared prefixes. The SDK repository carries `v0.27.0-rc.1`, the same tree as the final `v0.27.0`; the host reports `0.27.0`, so declare `"sdk": "^0.27"`.
>
> **Host limits (compat, no SDK change).** Core now bounds what a guest module may declare or do while it is instantiated, and the same checks apply every time a plugin is loaded, at boot included: a module may declare at most 1,048,576 table entries in total (Go guests declare about 8,000) and no duplicate section; the WebAssembly reference-types feature is off (see [Writing a plugin in another language](#writing-a-plugin-in-another-language)); instantiating a module, start section included, must finish within the 5 s start bound; and a protocol message above 4 MiB from the plugin's database fails the call and replaces the connection (results stay capped at 1000 rows and 1 MiB). No manifest field, host function or SDK type changed, and Go `wasip1` plugins, including all first-party ones, are unaffected. An installed plugin that exceeds a limit is not loaded after the upgrade: it is reported with an error (the plugin's status and the server log, for example `tables declare N entries (max 1048576)`), and it loads again once rebuilt within the limits.
>
> **0.23.0 changes (not purely additive).** New permission `entities.filter`. A plugin's `entity.before_save` filter is registered only while the owner holds it: the plugin asks for it in `permissions`, and the owner ticks it on the plugin's Permissions tab. This is not purely additive: a plugin built for `^0.22` or older has its `entity.before_save` filter run only with the grant. Authors add `entities.filter` to `permissions` and declare `^0.23`. Plugins that were installed before the permission existed and register the filter keep it: the first start of this version records the installed plugins (whatever their state), and each one is granted `entities.filter` once, when it is next loaded; the owner can revoke it. A plugin installed or updated afterwards is not kept and must ask for the permission. If you roll back to an older release and upgrade again, plugins installed on the older release in between are not kept. An update that widens access or changes the signer clears the plugin's grants, including a kept `entities.filter`, and a plugin whose new manifest does not ask for it cannot get it back until its author ships a manifest that asks. An update that newly asks for it needs the owner's confirmation and clears the grants, like `site.scripts` (a plugin that already holds it, kept from before, is not asked again). A plugin that asks for it declares `^0.23`, because an older host refuses the unknown permission name.
>
> **0.22.2 changes (additive over 0.22.1).** The `ui` block gains `"allow": ["camera"]`. When the owner ticks `ui.allow:camera` on the plugin's Permissions tab, the plugin's admin page may call `hatamoto.camera.capture()` and get one photo from the owner's camera. A host older than 0.22.2 ignores the field and the grant, so a plugin that relies on it declares `^0.22.2`. `permissions()` lists `ui.allow:camera` in `requested` (and in `granted` once ticked) for a plugin that asks, so `granted` stays a subset of `requested`. See [Camera](#camera).
>
> **0.22.1 changes (additive over 0.22.0).** `permissions()` gains `"mail": {"recipients_per_hour": N}` for a plugin granted `mail.send`: the number of recipients the plugin may email per hour. The limit is owner-set per plugin (default 360, range 1 to 3600, on the plugin Permissions tab) and the plugin cannot change it. The key is absent without the grant and on hosts older than 0.22.1, so treat a missing `mail` as unknown and assume the default; `granted` and `requested` are unchanged.
>
> **0.24 changes (additive over 0.23).** New manifest field `page_paths`: a `render.page` plugin declares the exact public paths it acts on (for example `/members`, `/account`) and is then called, and kept out of the page cache, only for those paths. No existing type or wire output changed. An older host ignores the field and calls the filter on every page, so a plugin on a lower `sdk` range must check `p.Path` itself (declare `^0.24` otherwise). See the manifest table below.
>
> **0.22 changes (additive over 0.21).** New filter `request.redirect` and manifest field `redirect_paths`: a plugin declares exact public paths and, for GET and HEAD requests to them, may answer before routing, stored redirects and the page cache with a same-site 302 or 307 and declared cookies, using its declared cookies and the visitor's country. No existing type or wire output changed. See [request.redirect](#requestredirect).
>
> `standing_approval.create` also arrives in 0.22: it lets an approved run publish new entries by itself (see [Creating content automatically](#creating-content-automatically)). It needs `^0.22`; an older host ignores it and creates wait for review.
>
> **0.21 changes (additive over 0.20).** New permission `packages.inspect` and host function `packages.inspect`: download a package from an https URL on one of the plugin's `allowed_hosts` and get its kind, name, version, `sha256`, size, manifest summary and whether a given ed25519 key signed it, reported as `problems` and never as an error for a bad package. New helper package `hatamoto.dev/sdk/registry` with the registry index types and `registry.SignIndex`. No existing type, constant or function changed. See **packages.inspect** below.
>
> **0.20 changes (additive over 0.19).** `entities.get` and each item of `entities.list` carry `terms`, the entity's term assignments as `[{ kind, slug, name }]`, and `entities.list` takes `term: { kind, slug }` to keep only entities carrying that term. Both need `terms.read` (the key is absent without it), and `terms` is returned only to a plugin whose `sdk` range requires 0.20 or later; plugins built for `^0.19` or older get exactly the bytes they got before. See "Terms (SDK 0.20)" below. `EntityEvent` does not change.
>
> Also in 0.20: an `items` entry of `builder_sources` can declare up to 8 typed `params` (filters), each query loop stores its own values, and the `builder.data` request carries the validated values as `params`. Additive: a source without `params`, and a loop whose values all drop out, produce the same request bytes as before (same `id`, no `params` key). See `builder_sources` below and [builder.data](#builderdata). Rollback floor: a host older than this change refuses a page whose loop stores `params`.
>
> **0.19 changes.** The SDK needs Go 1.27 or newer (`hatamoto.dev/sdk` declares `go 1.27`). It uses the JSON v2 packages: `json.RawMessage` is an alias of `jsontext.Value` in Go 1.27, so SDK types that now name `jsontext.Value` (for example the `args` of `guest.OnFunc`) are source compatible, and the bytes exchanged with the host are unchanged. The guest and the host marshal the SDK types and the types you write (call arguments, results, run summaries) with the v1 behaviours pinned (`omitempty`, `time.Duration` as nanoseconds, byte arrays as number arrays, `MarshalJSON` and `MarshalText` methods, repeated member names inside raw JSON), so your tags need no change; the guest reads these types back with the same v1 behaviours (`time.Duration` from nanoseconds, byte arrays, `,string`, names folded by case only, merging into a filled value, so `kv.get` into the struct you stored works); the one difference is that a struct with two fields sharing a JSON name fails to marshal. On the host, a changeset payload that cannot be encoded returns `invalid_argument` instead of staging `{}`, a repeated member in a `plugin.json` list element loads (the last wins), plugin settings validation rejects invalid UTF-8 that v1 replaced, and plugin manifest reads in the admin packages screen, the updater, the settings CLI and at boot use the same decoder as install. No ABI, host function or manifest field changed; `^0.18` plugins keep loading. Additive: `sdk.RemovedPermissions()` returns the sorted list of removed permissions (the five `*.write` names); the release tool copies it into the signed release record as `removed_permissions` so an update can be refused up front when an enabled plugin still declares one.
>
> **0.18 needs `^0.18`.** A plugin that uses `csp`, `request_headers`, `translation_of`, `changeset.media.alt` or shipped content types must declare `"sdk": "^0.18"`: an older host ignores those fields without an error and the plugin would run without them.
>
> **0.18 changes (additive over 0.17).** `request_headers` and `csp` in `plugin.json`; `cookies` reach public routes (`HTTPRequest.Cookies`, `HTTPResponse.SetCookies`); `translation_of` on `changeset.entity.upsert`; `caption` and `credit` on `changeset.media.attach`; `changeset.media.alt` (proposal kind `media_alt`); public content reads (`entities.get`, `entities.list`, `entities.translations`, `media.get`, `media.list`) in the run fetch phase, limited to what an anonymous visitor sees (the same visibility rule as public pages; media only when a public page uses the file); and content types shipped as `schemas/<name>.json` without a section template, created when the plugin is enabled.

> **0.17 changes (breaking).** Content writes move into **runs** and **changesets** (see [Runs and changesets](#runs-and-changesets) and [ADR 0004](adr/0004-plugin-changesets.md)), shipped with core 1.1.0. The twelve direct write functions are removed with no shim: `entities.create`, `entities.update`, `entities.set_status`, `entities.delete`, `terms.save`, `terms.delete`, `menus.save`, `menus.add_item`, `menus.delete_item`, `media.upload`, `redirects.create` and `redirects.delete`. So are the five permissions `entities.write`, `terms.write`, `menus.write`, `media.write` and `redirects.write`. Replacements: the `changeset.*` functions for content, `media.import_url` with `changeset.media.attach` for media. A manifest that requests a removed permission is refused at load with a message naming the replacement; a call to a removed function returns `unavailable` naming it. New: the `runs` manifest block, the `on_run_step` export, `run.*`, `upload.read`, `buffer.*`, `media.import_url`, `site.info`, `entities.slug_taken`, `changeset.*`, and the error codes `phase_denied` and `not_owned`. The run and changeset functions (`run.*`, `buffer.*`, `upload.read`, `media.import_url`, `site.info`, `entities.slug_taken`, `changeset.*`) reject arguments nested deeper than 32 levels, arrays of more than 10,000 elements and requests of more than 200,000 JSON tokens with `invalid_argument`; the other functions keep their existing limits (a request is at most 1 MiB). Stored grants for a removed permission are left in place and ignored. The changelog entries below are history: they name functions that 0.17 removed. A plugin built for `^0.5` to `^0.16` that never wrote content loads and runs as before. SDK 0.27 brings direct writes back for one case only: `entities.create`, `entities.update` and `entities.delete` under the new permission `entities.write.own`, limited to the one content type the plugin declares in `entity_schema`. The permission `entities.write` stays removed.

Hatamoto plugins are WebAssembly modules run by the host with
[Extism](https://extism.org) on wazero. A plugin ships as a **package**, a signed
`.hmp` zip you install from the admin, the registry or the CLI (see
[PACKAGES.md](PACKAGES.md) for the format, signing and trust):

```
plugin.json     manifest
plugin.wasm     the module
migrations/     optional: NNN_name.sql, goose format (see "Own data")
partials/       optional: <name>.html template partials
sections/       optional: <type>.html section templates
assets/         optional: public files; assets/plugin.css and assets/plugin.js load on every page
ui/             optional: the plugin's admin page (see "Admin page")
```

## Packaging

To publish a plugin from your own GitHub repo, see [PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md#part-2-publish-your-plugin).

```sh
hatamoto keys generate --name mykey                       # once
hatamoto plugin pack ./myplugin --key mykey.key           # myplugin-1.0.0.hmp
hatamoto package install myplugin-1.0.0.hmp               # or upload it in Admin > Plugins
```

Installing shows the owner your manifest's permissions (nothing is granted until
ticked), loads the plugin at once and, with `db` granted, provisions its schema
and runs `migrations/`. Updating a package replaces the module and keeps grants;
permissions a new version adds need a new tick. Uninstalling unloads it, revokes
its grants and, if the owner chooses, drops its schema, role, `kv` rows and
settings; core content is never touched. Tell users which signer key to trust
(`HATAMOTO_TRUSTED_KEYS`), or publish in a registry that does.

**Public assets.** `assets/` is served publicly at `/p/<plugin>/assets/<file>`
(GET and HEAD, `nosniff`, sandboxing CSP) and takes priority over your own
`/p/<plugin>/...` routes. If `assets/plugin.css` / `assets/plugin.js` exist,
every public page links them (the `plugin-head` / `plugin-body` slots of the
theme; see THEMES.md). **`assets/plugin.js` and `render.head` `script-src` tags
need the `site.scripts` permission** (listed in `plugin.json`, granted by the
owner): the script runs on every public page with the visitor's page privileges
(it can change what visitors see and send, but it cannot read an admin page or
send an admin request that passes the guard, and the builder canvas and previews do
not run it; see `docs/SECURITY_MODEL.md`), so without the grant it is neither linked nor
emitted, and the markup of its `sections/` and `partials/` may not contain scripts either. The file is still served at its `/p/<plugin>/assets/` URL (it is a
static asset), it is just never referenced. `plugin.css` needs no grant.
On a theme with `csp.strictScripts` (the default theme) the public `script-src`
is `'self'` only: plugins must load code with `<script src>` (a file under
`/p/<plugin>/assets/` or a `script-src` head tag), never inline script, handler
attributes (`onclick=...`) or `javascript:` URLs, including ones created at
runtime. Those are reported (Report-Only) or blocked (`CSP_ENFORCE=true`).

**Development.** `HATAMOTO_PLUGIN_DIR=./plugins` loads `./plugins/<name>/` as an
unsigned in-memory package (nothing stored, restart to reload). It is a
development override, not a way to deploy: production installs packages.

## Admin page

A manifest may declare a page of the plugin's own:

```json
"ui": {"title": "Leads", "entry": "index.html", "roles": ["owner", "editor"]}
```

`ui/<entry>` (default `ui/index.html`) is shown on the plugin's Settings tab,
`<admin>/plugins/<name>/settings`, for the listed roles (owner only by default),
once the plugin is loaded. (`<admin>` is your install's private admin address, printed by `hatamoto admin-url`; tests and the QA harness pin it to `/admin`.) The old `/admin/plugins/<name>/ui` address redirects there. It runs in `<iframe sandbox="allow-scripts">` **without**
`allow-same-origin`: an opaque origin, so no admin cookies, no admin DOM, no
storage. It is served from `/plugin-ui/<name>/` (outside the admin address) with:

```
Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src 'self';
  style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none';
  form-action 'none'; base-uri 'none'; frame-ancestors 'self'
```

No inline script or style, no `eval`, no network of its own. The HTML document
needs a signed-in user of an allowed role and the one-time ticket in its URL
(`?t=`, valid 60 seconds, bound to that session and file) that the plugin page
minted when it rendered; without it, with a reused or expired one, or from
another session, the answer is the admin 404. Scripts, styles and images are
static files and need no ticket. Everything the page does goes through the bridge:

```html
<script src="/plugin-ui/_sdk/bridge.js"></script>
<script src="app.js"></script>   <!-- app.js: -->
```
```js
await hatamoto.ready;                                   // {plugin, functions}
const r = await hatamoto.route("GET", "/leads", null, {page: 1});
//   -> {status, content_type, body}  one of the plugin's own routes
const h = await hatamoto.host("kv.get", {key: "last"});
//   -> {ok, result} | {ok:false, error:{code, message}}  a granted host function
```

The bridge is `postMessage` to the admin page, which relays to
`POST <admin>/plugins/<name>/bridge` (session + CSRF). `route` runs the plugin's
own route through the same registry as `/api/v1/p/<name>/...` as the **signed-in
user** (the route's `roles` apply, so a route that lists only `owner` refuses an
editor who opened the page). `host` is **owner only**: the owner granted those
permissions to the plugin's code, not to every editor, so an editor's bridge call
to `host` is refused with 403 and an editor reaches only the plugin's own routes
(where the plugin's `roles` and validation apply). For the owner a host function
runs only if the plugin holds its permission (checked in the admin script and
again on the server); functions it was not granted answer `capability_denied`.
The bridge is rate limited per user and plugin. Nothing else crosses: the page
cannot read the user, the session, the CSRF token or other plugins.

**Run inputs.** A plugin whose page lets the owner fill in a run's inputs calls `hatamoto.runInputs({name: value, ...})`. The admin page keeps the values in its own start form, outside the frame, and makes no request: only the inputs the run declares are kept (anything else is ignored), each value is cut at 4096 characters, and a value that is not an object is refused. The owner then starts the run with the admin's Start button. The hosts the run may reach are ticked by the owner in the admin, never chosen by the page.

### Camera

A plugin whose page needs a photo (a scan, a receipt, a profile picture) declares it in the manifest:

```json
"ui": {"title": "Scanner", "entry": "index.html", "allow": ["camera"]}
```

```js
await hatamoto.ready;
if (hatamoto.camera.available) {            // the owner granted it; false otherwise
  const r = await hatamoto.camera.capture(); // call it from a click handler
}
//   -> {ok:true, image:"data:image/jpeg;base64,...", width, height}
//   |  {ok:false, error:"cancelled" | "denied" | "unavailable"}
```

The call opens a dialog that **core** draws on the admin page, outside the frame: the person sees the live preview, clicks **Capture** and gets one JPEG, at most 1280 px on the longer side and about 1 MB. `cancelled` means they closed the dialog, `denied` that the browser refused the camera (the dialog stays open with the reason until the person closes it), `unavailable` that there is no camera or the browser cannot open one. The result shape and these three codes are stable.

The owner must tick `ui.allow:camera` on the plugin's Permissions tab. It is unticked by default, kept when the plugin is updated and revoked when it is uninstalled. Without the grant the call does not return `{ok:false}`: the promise **rejects** with an `Error`, so check `hatamoto.camera.available` (also `camera` in the `hatamoto.ready` value) before showing a scan button, or wrap the call in `try`/`catch`. The call also needs a person's click in the page just before it: from a timer or on load the promise rejects with an `Error` too, and only one capture can be open at a time (a second call returns `cancelled`). After a capture ends without a photo (cancelled, denied, unavailable), new calls return `cancelled` without opening the dialog for about 5 seconds, so a page that calls again on `cancelled` cannot trap the person in it. If the browser's own camera prompt is left unanswered, further captures stay blocked until it is answered or the page is reloaded. Clicks on **Capture** are ignored for half a second after the preview appears. Revoking the grant applies the next time the page loads. The frame's sandbox and Content-Security-Policy stay as above: the plugin never touches the camera itself, and the camera stops when the dialog closes. Declare `^0.22.2` to rely on it: `^0.22` also matches a 0.22.1 host, which ignores `allow` and the grant.

The bridge serves only the first document loaded in the frame, and every request
carries a per-load nonce. If the page navigates the frame elsewhere, the host
stops relaying and blanks the frame (its ticket is spent, so a reload inside the
frame does not load the page again) and the admin shows a "Plugin page disconnected" notice. **Residual risk:** a sandboxed frame can
still navigate itself once, so a plugin UI can leak what it can read (what the
bridge returned to it) through that navigation; the CSP cannot stop it. Install
only plugins you trust with what their page can see.

The contract is the Go module `hatamoto.dev/sdk` (version `0.27.0`; plugins built for `^0.5` to `^0.26` still load, unless they request a removed write permission):
hook names, payload types and the manifest. Its subpackage `sdk/guest` is the
Go guest layer. Any language with an [Extism PDK](https://extism.org/docs/concepts/pdk)
can write a plugin from this document alone.

```sh
go get hatamoto.dev/sdk@v0.27.0   # before the release: @v0.27.0-rc.1
```

SDK 0.19.0 ships with Hatamoto `v1.1.0`, which serves every range back to `^0.5`. Declare the oldest range your plugin needs, so it also loads on older servers. The `site.scripts` permission belongs to SDK 0.12.0, but the range you declare does not gate it: hosts from v0.30.0 accept it under any range and enforce it, older hosts reject it as unknown.

`hatamoto.dev/sdk` (and `hatamoto.dev/core`) are vanity import paths: hatamoto.dev
answers `?go-get=1` with a `go-import` meta tag pointing at the code repository
(`deploy/caddy/hatamoto.dev.caddy`), so the import path never changes when the
repository moves. `hatamoto.dev/sdk` is the public repository
[Zen-Labs-Corp/hatamoto-sdk](https://github.com/Zen-Labs-Corp/hatamoto-sdk), a root module with
plain release tags: version `0.27.0` is the git tag `v0.27.0`. The core repository stays
the source of truth for the code (`backend/pkg/sdk`); `scripts/publish-sdk.sh` exports that
directory to the SDK repository and tags it `v<Version>` (the `Version` constant in
`version.go`), or `--tag v<Version>-rc.N` for a release candidate of the same tree. Send SDK changes to core. Core release tags are plain `vX.Y.Z` too, in a
different repository.

> **0.12 changes:** browser JavaScript needs a grant: the `site.scripts` permission. Without it a plugin's
> `assets/plugin.js` is not linked on public pages and the `script-src` tags of its `render.head` filter are dropped
> (see [Public assets](#packaging)); the same goes for scripts in its `partials/` and `sections/` markup (see [Template functions, partials and sections](#template-functions-partials-and-sections)). Additive for the manifest, **behaviour-changing for installed plugins**: a plugin
> that shipped `assets/plugin.js` before 0.12 stops running its script until its author lists `site.scripts` in
> `plugin.json` and the owner grants it. `plugin.css` is not gated.
>
> **Public locales:** on a site with several public locales
> (see [ADR 0003](adr/0003-public-locales.md)) the existing fields carry the page's locale; SDK 0.15 adds the fields listed below.
> `HeadContext.language` is the locale of the page being rendered, `EntitySummary.locale` is the entity's locale, and the
> `sitemap.urls` filter gets the file's locale tag in `language` (it stays empty on a one-language site). The host prints
> hreflang itself, and drops a `render.head` link with `rel` `alternate` and an `hreflang` that duplicates one it computed.
> SDK 0.27 is current (0.27.0).
>
> **0.13 changes (additive):** entity payloads carry `locale` (the entity's BCP 47 tag, for example `en` or `fr`):
> `EntityRef`, `EntityEvent`, `EntitySummary` and the Entity of `entities.get`/`entities.list` have it,
> `EntityDraft` has it as an optional field (filled on `entity.before_save` from the entity, and empty, so left out of the JSON, on a create that sent no locale, except that it is `und` for a type shared across languages on a multi-language site; the host never reads it back), and `entities.create` / `entities.update` accept `locale`
> (empty on create means the site language, or `und` for an entity type marked "Shared across languages" on a site with several languages; a locale you send is kept). SDK 0.13 carries it: no rule requires a bump for an additive field, but a published SDK
> tree cannot change under the same version, and 0.10 and 0.11 shipped additive payload changes as a minor too.
> A plugin built earlier ignores unknown JSON fields; on a host older than SDK 0.13 the field is absent.
> On `entity.published` from a scheduled publish the host looks the locale up; if that fails it is `""`.
>
> **0.14 changes (additive):** `FormSubmittedEvent` carries `attribution` (`first` and `last` touch: `channel`, `source`, `medium`, `campaign`, `landing`), the
> classified source of the browser tab that submitted the form. It is left out when none was recorded (no-JS posts, API posts, older submissions). It holds no URL, address or identifier.
> A plugin built earlier ignores the unknown field; on a host older than SDK 0.14 it is absent.
>
> **0.15 changes (additive for plugins built for 0.14 or older):** locale support for plugins that manage a multi-locale site.
> `HeadContext` carries `locale` and `segment`, `SitemapURLs` carries `segment` and `file`, entities carry `translation_group`,
> `entities.create` takes `translation_of` (and, for a plugin declaring 0.15 or later, needs `locale` on a multi-locale site; `entities.create` is removed since 0.17, and `changeset.entity.upsert` with no `locale` takes the site language, or `und` for a shared type on a multi-language site), the `entities.translations` host function lists a
> translation group (it needs `entities.read`, no new permission), and `entities.get` with a `path` reads a locale-prefixed path in that locale (only for a plugin whose `sdk` range requires 0.15 or later; `^0.14` and older resolve paths as before).
> All new payload fields are left out when empty, so a one-language site sends the same JSON as before. A plugin built earlier ignores the unknown
> fields; on a host older than SDK 0.15 they are absent, and `entities.translations` fails with `unknown_function`.
>
> **0.16 changes (additive):** the `render.site_graph` filter (see [SiteGraph](#sitegraph)) lets a plugin add keys to the
> Organization, LocalBusiness and WebSite nodes of the generic site graph and append nodes of its own. Nothing else changes: a plugin built for 0.15 or older
> loads and runs as before, and on a host older than SDK 0.16 the hook is never called.
>
> **0.11 changes:** a page-builder data source: the `builder.data` filter, the `builder.data` permission and the
> `builder_sources` manifest field (see [builder.data](#builderdata) and [WIDGET_PACKS.md](WIDGET_PACKS.md)). Purely additive.
>
> **0.10 changes:** the entity host functions can build and read trees (see [Host functions](#host-functions)):
> `entities.create` / `entities.update` take `parent_id`; an Entity carries `parent_id`, `path` and `url`;
> `entities.get` also takes `{ "path" }` or `{ "slug", "parent_id", "schema_slug" }`. Purely additive.
>
> **0.9 changes:** `render.page` sends slim blocks by default (see [PageContext](#pagecontext)):
> `id`, `type`, `title`, `hidden` and a few scalar `fields`, no content; the host puts
> the original content back by id. Plugins built for an SDK before 0.9 keep receiving
> whole blocks, and `render_page.full_blocks` in `plugin.json` opts a 0.9 plugin in.
>
> **0.8 changes:** `sitemap.urls` is additions-only (see [SitemapURLs](#sitemapurls));
> a host function refused for a missing grant answers
> `{"code":"capability_denied","capability":"<permission>"}` (was `permission_denied`;
> `guest.CapabilityDenied(err)` reads it); and `limits.timeout_ms` is replaced by
> per-hook `limits.hook_timeouts_ms` (see [Sandbox and limits](#sandbox-and-limits)).
> A 0.7 plugin that still returns a whole sitemap list adds nothing; one that tests
> for `permission_denied` must test `capability_denied`; an old `limits.timeout_ms` is
> ignored (hooks get the defaults).
>
> **0.7 change:** the `locale` JSON field of `HeadContext` and `SitemapURLs` is
> now `language` (the locale concept is gone; on `HeadContext` it holds the BCP 47
> site language, e.g. `en-AU`). Plugins built for `^0.5`/`^0.6` still load but see
> an empty `locale`; rename the field and rebuild against `^0.7`.

Entity reads also carry `sort_order` (the stored manual order among siblings, `0` until an owner orders them). It is additive and not filled by every read path.

## Calling convention

- The module is a WASI **reactor** (no `_start`; `_initialize` is run once per instance). For Go:
  `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .`
  with the standard toolchain (Go 1.27+; no TinyGo needed). `make plugin-hello` does this for the reference plugin.
- A hook is handled by **exporting a function** named after it. The host registers a handler for every hook whose export exists and ignores the rest.
- Input and output are **JSON** through the Extism input and output buffers (`pdk.Input()`, `pdk.Output()`).
- The export returns `0` on success. On failure it sets the Extism error message and returns non-zero.
- Export names: replace `.` with `_` in the hook name and prefix `on_` for an action or `filter_` for a filter.

## Hooks and exports

| Hook | Kind | Export | Payload (input) | Result (output) |
|---|---|---|---|---|
| `entity.created` | action | `on_entity_created` | [EntityEvent](#entityevent) | none |
| `entity.updated` | action | `on_entity_updated` | EntityEvent | none |
| `entity.published` | action | `on_entity_published` | EntityEvent | none |
| `entity.deleted` | action | `on_entity_deleted` | EntityEvent | none |
| `import.completed` | action | `on_import_completed` | [ImportEvent](#importevent) | none |
| `agent.hit` | action | `on_agent_hit` | [AgentHitEvent](#agenthitevent) | none |
| `agent.first_crawl` | action | `on_agent_first_crawl` | [AgentFirstCrawlEvent](#agentfirstcrawlevent) | none |
| `jsonld.enrich` | filter | `filter_jsonld_enrich` | [JSONLDDoc](#jsonlddoc) | JSONLDDoc |
| `sitemap.urls` | filter | `filter_sitemap_urls` | [SitemapURLs](#sitemapurls) | SitemapURLs |
| `form.submitted` | action | `on_form_submitted` | [FormSubmittedEvent](#formsubmittedevent) | none |
| `render.head` | filter | `filter_render_head` | [HeadContext](#headcontext) | HeadContext |
| `render.site_graph` | filter | `filter_render_site_graph` | [SiteGraph](#sitegraph) | SiteGraph |
| `render.page` | filter | `filter_render_page` | [PageContext](#pagecontext) | PageContext |
| `request.redirect` | filter | `filter_request_redirect` | [RedirectContext](#requestredirect) | RedirectContext |
| `entity.before_save` | filter (needs the `entities.filter` grant) | `filter_entity_before_save` | [EntityDraft](#entitydraft) | EntityDraft |
| `builder.data` | filter | `filter_builder_data` | [BuilderDataRequest](#builderdata) | BuilderDataRequest |
| `comment.created` | action (needs `comments.read`) | `on_comment_created` | [CommentEvent](#commentevent) | none |
| `comment.approved` | action (needs `comments.read`) | `on_comment_approved` | CommentEvent | none |
| `comment.spam` | action (needs `comments.read`) | `on_comment_spam` | CommentEvent | none |
| `comment.updated` | action (needs `comments.read`, SDK 0.27) | `on_comment_updated` | CommentEvent | none |
| `comment.deleted` | action (needs `comments.read`) | `on_comment_deleted` | CommentEvent (ids and `status` only) | none |
| `comment.check` | filter (needs `comments.read`) | `filter_comment_check` | [CommentCheck](#commentcheck) | CommentCheck (`score`, `reasons` only) |
| `comment.draft` | filter (needs `comments.read`, SDK 0.27) | `filter_comment_draft` | [CommentDraft](#commentdraft) | CommentDraft (`author_name` or `reject` only) |

**0.27 hooks.** `comment.updated` (Go constant `sdk.CommentUpdated`) fires when a comment is edited, marked dead or restored, or anonymised by an erase, and never when only its rank changes. `comment.draft` (Go filter token `sdk.CommentDraftF`, payload type `sdk.CommentDraft`) is described under [CommentDraft](#commentdraft). `comment.updated` is delivered now; your own `comments.mark_dead` and `comments.restore` calls echo back to you as `comment.updated`, so a handler must be idempotent. `comment.draft` is registered, but core does not call it until the change that implements it ships. `comment.deleted` also fires, for comments that were once approved, when a comment is purged, when a commenter's data is erased (the comments deleted, not the anonymised ones; held comments too, since they went out with `comment.created`), when an admin deletes a comment together with its replies and when an entity's comments are removed with it.

**Actions** run asynchronously after the event, in a background goroutine; the
output is ignored and a failure is only logged. **Filters** run in order on the
request path; each receives the previous filter's result and returns the value
to pass on. Plugins run in plugin-name order.

### Failure handling (fail open)

A filter that returns a non-zero status, traps, runs out of memory, exceeds its
timeout, or returns output that does not decode leaves the value **unchanged**
(as it was before that plugin) and is logged; the request goes on, and the
other plugins still run. An action failure is logged. The instance that failed
is discarded, never reused. An empty output from a filter means "unchanged". A guest that only reports an
ordinary error (error message plus non-zero status, no trap) keeps its
instance. A hook that traps or times out 3 calls in a row is switched off for
30 seconds (logged once; calls fail open at once), per plugin and hook.

When a request runs several filters under one deadline (page head, JSON-LD,
sitemap), each filter gets at most its fair share of the time that is left, so
a slow plugin cannot starve the ones after it.

The one deliberate exception is `entity.before_save`, which runs only while the plugin holds the `entities.filter` permission: set the error message to
`hatamoto:reject:<reason>` and return non-zero to **refuse the save** (HTTP 422
with `<reason>`). One failed call there still fails open. While the circuit breaker has this hook
switched off (3 failed calls in a row, see above), the save is **refused**
instead, with the message that the plugin hook is disabled after repeated
failures; it works again when the 30 seconds are over. Filters of other hooks
stay fail open.

`comment.check` is the second deliberate exception, and it fails closed in the other direction: it runs on a comment a visitor is submitting, and any error, trap, timeout, `hatamoto:reject:` answer, busy plugin or open circuit breaker **holds the comment for moderation** (it is not lost and not published). All plugins share one budget of 500 ms for the whole chain, and each call also has the plugin's own hook timeout (default 250 ms, `limits.hook_timeouts_ms["comment.check"]`). A cold plugin instance may not start within the budget, so the first comment after a restart can be held. The filter is registered only while the plugin holds `comments.read`.

`comment.draft` (SDK 0.27) is the third deliberate exception and fails closed too: any error, trap or timeout **rejects the draft** with the public message "Sign-in name unavailable, try again". A `reject` answer is shown to the visitor as it is, at most 200 bytes. The filter is called only for a content type with the community policy on and the author name locked, and is registered only while the plugin holds `comments.read`. Until it ships it is never called.

### Payload schemas

All fields are always present unless marked optional. IDs are UUID strings.
Timestamps are RFC 3339. Unknown fields in a returned filter value are ignored, and top-level fields a
filter leaves out keep their previous value, so a plugin may return only what it
changed (a field it does return replaces the old one as a whole).

Output is bounded: at most `limits.max_output_bytes` (default 1 MiB, up to 4 MiB;
more fails the call), `render.head` at most 64 tags, `render.site_graph` at most 64 elements per array and 20,000 tokens, `sitemap.urls` at most 5,000
additions, and arrays and nesting in other payloads are capped (JSON-LD: 1,000
elements per array, depth 32). Output over a cap is treated as a failure.

#### EntityRef
```json
{ "id": "string", "schema_slug": "string", "slug": "string", "locale": "string", "name": "string", "status": "string", "version": 0 }
```

#### EntityEvent
Same fields as EntityRef.
```json
{ "id": "string", "schema_slug": "string", "slug": "string", "locale": "string", "name": "string", "status": "string", "version": 0 }
```

#### ImportEvent
Fired **once per import batch** (`entity.Service.Import`: the WordPress importer, the
archive import, any bulk load), after the rows were stored. Imported rows fire **no**
`entity.created` / `entity.updated` / `entity.published`, so a plugin that must see
imported content reacts to this action and lists what changed through its host API.
Core webhooks deliver it as the webhook event `import.completed` with the same counts. Failed items
are only counted.
```json
{ "source": "wordpress", "created": 120, "updated": 3, "failed": 1 }
```

#### CommentEvent
Fired for `comment.created`, `comment.approved`, `comment.spam` and `comment.deleted`, and delivered **only to plugins that hold `comments.read`** (checked at each delivery). Since SDK 0.25.0.
```json
{ "id": "string", "entity_id": "string", "parent_id": "string", "author_name": "Ada", "staff": false,
  "body_html": "<p>Nice post.</p>", "status": "pending", "locale": "en", "commenter_key": "string", "created_at": "2026-01-02T03:04:05Z" }
```
`parent_id`, `author_name`, `staff`, `body_html`, `locale` and `commenter_key` are left out when empty. `commenter_key` (SDK 0.27) is the pseudonymous key of the commenter for this plugin (see [Visitor and commenter keys](#visitor-and-commenter-keys-sdk-027)); it is absent for staff and never sent to webhooks. `status` is `pending`, `approved`, `spam` or `trash`, and, since SDK 0.27, `dead`; `staff` is true for a comment written by a signed-in owner or editor. `body_html` is sanitised once when the comment is written.

- `comment.created` fires when a comment is stored as pending or approved, or confirmed by its author. A comment that is auto-approved also fires `comment.approved`; the two may arrive in either order, so key on the comment `id` and read `status`. A comment the spam filters store as spam fires nothing, and no event fires for an unconfirmed draft.
- `comment.approved` fires when a comment becomes approved.
- `comment.spam` fires when a moderator marks a comment as spam.
- `comment.deleted` fires when a comment is trashed or deleted. For comments that were once approved it also fires (since SDK 0.27) when the retention job purges spam or trash, when a comment is deleted together with its replies (once for each reply), when an entity is deleted with its comments, and when a commenter's data is erased and the comment is deleted with it. A comment that was never approved (held, or stored as spam by the filters) is removed in those cases without an event, except in an erase, which also announces held (pending) comments, because their name and body went out with `comment.created`; a comment the spam filters stored as spam and an unconfirmed draft are still removed silently. A comment kept for its replies is anonymised instead and fires no `comment.deleted`; plugins get `comment.updated` for it (empty `author_name`, `body_html` `[deleted]`), webhooks get nothing, so a webhook receiver that mirrored its name or body is not told. The payload holds ids and `status` only. It may arrive twice for one comment (trash, then delete). It is not sent for unconfirmed drafts purged after 24 hours. Events from one purge or cascade are delivered one after another on a single worker, not all at once. Use `id` as the idempotency key.
- Edits, unapprovals and restoring a trashed comment send no event. From SDK 0.27, `comment.updated` fires on an edit, on mark dead and on restore (never on a rank change); the payload `status` is the new status.

A comment event never carries the commenter's email, a hash, the IP address, the user agent, the author's web address, a spam score or reasons, or the raw body.

#### CommentCheck
The value of the `comment.check` filter, run on a new comment before it is stored, only for plugins that hold `comments.read`. Since SDK 0.25.0.
```json
{ "body": "string", "link_domains": ["example.com"], "body_hash": "string", "commenter_key": "string", "schema": "story", "addr_key": "string", "area_key": "string", "score": 0, "reasons": ["string"] }
```
`body` is the raw comment text, `link_domains` the domains it links to and `body_hash` a site-keyed hash of the normalised body. SDK 0.27 adds `commenter_key` (needs `comments.read`), `schema` (the slug of the content type the comment is on; the key is `schema`, not `entity_schema`) and, only with `client.key`, `addr_key` (the visitor's address) and `area_key` (the visitor's /24 or /64 network). See [Visitor and commenter keys](#visitor-and-commenter-keys-sdk-027). The payload never holds the IP address, a network hash, the email, the name or the entity. `score` and `reasons` are **output only** and arrive empty; every other field of the answer is ignored.

A plugin can only add to the spam score. `score` is `0` to `sdk.CommentCheckMaxScore` (100): a negative value counts as 0 and a larger one is cut to 100. The host adds the scores of all plugins (cut to 100 again) and maps the sum onto its own scale; 100 alone marks the comment as spam, lower sums may hold it for moderation. `reasons` keeps at most 8 entries of 200 bytes, each prefixed with the plugin name, and is shown to the moderator. Failures hold the comment, see [Failure handling](#failure-handling-fail-open).

#### CommentDraft
The value of the `comment.draft` filter (SDK 0.27, Go type `sdk.CommentDraft`, filter token `sdk.CommentDraftF`). Core calls it while a visitor submits a comment on a content type with the community policy on, where the form's name field is hidden and the author name is filled by the plugin. Only plugins that hold `comments.read` are called.
```json
{ "commenter_key": "string", "author_name": "string", "schema": "story" }
```
`commenter_key` is the pseudonymous key of the signed-in commenter, `author_name` arrives empty and `schema` is the content type slug. The plugin answers with `author_name` (the canonical name the comment is stored under) or with `reject`, a public message of at most 200 bytes that is shown to the visitor instead. Every other field of the answer is ignored. Any error or timeout rejects the draft, see [Failure handling](#failure-handling-fail-open). Until the change that implements it ships, the filter is never called.

#### AgentHitEvent
No IP address and no user agent are ever included. `entity_id` is `""` when the path is not an entity page; `status` is `0` when unknown.
```json
{ "agent_id": "string", "operator": "string", "purpose": "string", "verification": "string",
  "method": "string", "path": "string", "entity_id": "string", "source": "string",
  "status": 0, "bytes": 0, "duration_ms": 0, "occurred_at": "2026-01-02T03:04:05Z" }
```

#### AgentFirstCrawlEvent
```json
{ "entity_id": "string", "agent_id": "string", "operator": "string", "purpose": "string",
  "first_at": "2026-01-02T03:04:05Z", "published_at": "2026-01-01T00:00:00Z" }
```
`published_at` is omitted when the entity has no publish time.

#### FormSubmittedEvent
Fired after a public form submission was stored. It carries the submitted fields, so it is delivered **only to plugins that hold `forms.read`**; the visitor's IP address and user agent are never included.
```json
{ "form_id": "string", "form_slug": "contact", "submission_id": "string",
  "fields": { "email": "a@example.com" }, "source_path": "/contact", "submitted_at": "2026-01-02T03:04:05Z",
  "attribution": { "first": { "channel": "ai_assistants", "source": "perplexity", "landing": "/pricing" },
                   "last": { "channel": "email", "source": "newsletter", "medium": "email" } } }
```
`fields` is a `map[string]any`. The value of a file field is an object `{ "id": "string", "name": "string", "size": 0, "type": "string" }` (the reference to the stored file: the original name cleaned, the size in bytes, the detected content type). It carries no path and no address, and the plugin cannot read the file. Read it as a map and ignore keys you do not know; a plugin written before file fields only sees one more key whose value is an object.

#### PageContext
The value of `render.page`, the request-aware page filter. It runs for each public page view of an entity or listing page (not for 404 and error pages). `cookies` holds only the cookies the plugin declared in `plugin.json` (`cookies`), under the short name you declared; `blocks` are the page's sections *derived from `page_doc`* (pages that have no doc yet use their stored `page_sections`); a page the section model cannot express (builder-only) is not filtered, and a plugin that needs its component data reads `attributes.page_doc` through the host entity API. Blocks are **slim** by default: `id`, `type`, `title`, `hidden` and `fields`, the short scalar values (string up to 64 bytes, number, boolean) of the content keys `variant`, `anchor` and `label`. There is no `content`, no rich text and no nested data, so the call stays under 1 ms for a typical page (4 to 12 blocks). The host keeps the original content and re-attaches it by id. Set `"render_page": { "full_blocks": true }` in `plugin.json` to receive every block with its whole `content` instead (slower: see the cost below; still bounded by the output and decode caps). A plugin built for an SDK range that does not include 0.9 (`^0.8` and older) gets whole blocks automatically and its result is used as returned, as before.
The result may

- return `blocks` to reorder, drop or replace blocks (a block with `"hidden": true` is dropped). **Slim mode:** return a block as received, without `content`, to keep it (the host re-attaches its content; `fields` in a result are ignored); return it with `content` to **replace** that block's content, optionally with another `type` and `title` (an omitted type keeps the original; a block with an unknown id needs a `type`). The host sanitises replaced content when rendering, like any block. **Full mode:** the returned list is used as it is,
- list block ids in `hide` to drop them without rebuilding the list (return only `hide` and `blocks` stays as received),
- add `set_cookies`. Each name must be declared in the manifest. **Cookies are namespaced:** the browser cookie is `hmp_<plugin>_<name>` (the host maps it both ways), so a plugin never receives or sets the session cookie, the CSRF cookie, `__Host-`/`__Secure-` cookies or another plugin's cookies, and the manifest refuses names starting with `hatamoto_`, `__Host-`, `__Secure-` or `hmp_`; the host forces `HttpOnly`, `SameSite=Lax`, `Secure` on HTTPS and `Path=/` unless a path is given, values must be cookie-safe ASCII (at most 2 KiB), `max_age` at most one year.

A result that breaks a rule (undeclared cookie, too many blocks (200) or cookies (8)) is dropped whole: fail open, the page keeps what it had. Plugins run in name order and each sees the previous result. A plugin that declares `page_paths` in its manifest (SDK 0.24; exact public request paths, no trailing slash (except `/`); the request path is matched after the trailing slash is dropped, at most 16, otherwise the same rules as `redirect_paths`, core paths refused) is called only for those paths, and only they are served `Cache-Control: private, no-store` with `Vary: Cookie` and kept out of the page cache. Every other page is cached as if the plugin handled nothing. Without `page_paths` the filter runs for every page and no page is shared-cached, so declare the paths whenever the filter only acts on a few pages. Several plugins may declare the same path. An older host ignores `page_paths` and calls the filter on every page, so a plugin that keeps a lower `sdk` range must still return early in its filter unless `p.Path` is one of its paths; declare `^0.24` to rely on the host doing the scoping. `page_paths` are request paths, not pages: a locale-prefixed or alternate URL of the same content is not covered, the plugin is not called there and that response is page-cached, so list every URL the filter must act on.
```json
{ "path": "/members/guide", "query": { "p": "2" }, "cookies": { "unlock": "1" },
  "entity": { "id": "string", "schema_slug": "page", "slug": "guide", "locale": "en", "name": "Guide" },
  "blocks": [ { "id": "intro", "type": "content", "title": "Intro", "fields": { "variant": "dark" } },
              { "id": "secret", "type": "content", "title": "Members only" } ],
  "hide": ["secret"],
  "set_cookies": [ { "name": "visited", "value": "1", "max_age": 3600 } ] }
```

SDK 0.27 adds `query` (`map[string]string`), `commenter_key`, `addr_key` and `net_key`, all left out when empty. `query` holds only the keys the plugin declared in `page_query`, with the first value of each; a value over 256 bytes is dropped; values are passed as received (escape on output); a plugin without `page_query` gets no `query`; the query never changes which pages are filtered or cached. `commenter_key` needs `comments.read`; `addr_key` and `net_key` need `client.key`. See [Visitor and commenter keys](#visitor-and-commenter-keys-sdk-027).

A plugin that declares `page_path_prefixes` (SDK 0.27; up to 8, each in the `/x/` form) is called for every public request path under one of them, and those pages are uncached like exact `page_paths`: `Cache-Control: private, no-store`, `Vary: Cookie`, never stored in the page cache. `/item/` matches `/item/<anything>` and not `/item` or `/items/x`. A plugin that declares only prefixes is not site-wide: it is not called on other pages. `/` and prefixes whose first segment core serves (`/p/`, `/api/`, `/uploads/`, `/media/`, `/theme/`, `/-/`, `/admin/`, `/mcp/`, ...) are refused at load and at install. Locale-prefixed URLs are not matched, as for `page_paths`. A URL under a prefix that resolves to no page is a 404 rendered afresh on every request (it is not stored in the 404 cache) and does not call the plugin. `page_query` is a list of query keys (at most 16; declaring `page` is allowed): `render.page` gets `query` with the first value of each declared key present, a value over 256 bytes is dropped (the key is absent, never truncated) and undeclared keys never reach the plugin. A host before this change (including 0.27 hosts that only carry the contract) ignores both fields: it passes no query, and a plugin that declares only `page_path_prefixes` is called on every page there (site-wide, uncached). Declare `^0.27` to rely on them.

#### request.redirect
The value of `request.redirect` (SDK 0.22). The host calls it for a `GET` or `HEAD` request whose path is exactly one the plugin declared in `redirect_paths`, before routing, stored redirects and the page cache. It is never called for another path, another method, or a request the site's access gate holds back, and a path that differs only by a trailing slash is a different path. A declared path must start with `/`, carry no query, fragment, wildcard, whitespace, `..` or `//`, and be at most 256 bytes (16 paths per plugin). The host refuses a path it serves itself: the admin, the API, `/p/` plugin routes, media, uploads and static assets, `/health`, `robots.txt`, sitemaps and feeds, the root icons, the locale switcher `/locale` and any root-level `.txt` or `.xml` file. `/` is allowed. Paths that core already answers as scanner probes never reach the filter.

`path` is the request path and `query` the raw query string. `cookies` holds only the cookies the plugin declared (`cookies`), under the short name. `country` is the visitor's ISO 3166-1 alpha-2 code, or empty when there is no country database or the address is unknown; the address itself is never passed. The database is the file named by `HATAMOTO_GEOIP_DB` (the same one the analytics country report uses); the host reloads it when the file changes, so a replaced file takes effect without a restart.

The result may

- set `redirect` to `{ "status": 302 | 307, "location": "/path?query" }`. `location` must be a same-site path with an optional query (no scheme, host, `//`, backslash or control byte, at most 2048 bytes), the same rule as a public route's `Location`. The response is the redirect with `Cache-Control: no-store`.
- add `set_cookies`, with the same rules as `render.page` (declared names only, namespaced `hmp_<plugin>_<name>`, `HttpOnly`, `SameSite=Lax`).
- answer with cookies only. The cookies are set and the page is rendered normally, but that response is `Cache-Control: no-store` with `Vary: Cookie`, whichever handler answers the path (page, stored redirect, 404), and is neither read from nor written to the page cache.

An empty answer leaves the request to normal handling, including the page cache. Because the answer depends on the visitor, the normal response on a declared path is never shared-cacheable: `public` becomes `private`, `s-maxage` is dropped and `Vary: Cookie` is added (the rule plugin routes with declared cookies follow), so a CDN or proxy cache in front of the site cannot replay one visitor's response and skip the hook. The page cache inside core is still used. A result that breaks a rule (status other than 302 or 307, bad `location`, undeclared cookie) is dropped whole. Fail open applies: an error, a trap or a timeout is logged and the request is handled as if the plugin were not installed. The default timeout is 250 ms (the host also bounds the whole call), tunable with `limits.hook_timeouts_ms["request.redirect"]`. A plugin that uses `redirect_paths`, the `request.redirect` filter or that timeout key must declare `"sdk": "^0.22"`: a host older than 0.22 then refuses it visibly, where a lower range would load it with the field ignored (or, for the timeout key, refuse the manifest as an unknown hook) and the redirects would silently stop.

Two plugins cannot declare the same path: the install, an upgrade and enabling a plugin are refused with a message naming the path, whether or not the other plugin is enabled. Declaring `redirect_paths` is shown to the owner in the plugin's permissions as "Pages it may redirect".
```json
{ "path": "/go", "query": "utm=x", "cookies": { "pref": "b" }, "country": "XX",
  "redirect": { "status": 302, "location": "/start?utm=x" },
  "set_cookies": [ { "name": "pref", "value": "b", "max_age": 31536000 } ] }
```

#### JSONLDDoc
Modify and return `doc` (the schema.org document). Keep `entity` and `base_url` as received. `doc` is the entity node, never a `@graph`; on an entry page it already carries the `author_from` author, and a plugin can override it. The FAQPage wrap, the canonical address, the byline credits and the `emit_webpage` switch are applied afterwards.
```json
{ "entity": { "...EntityRef" }, "base_url": "https://example.com", "doc": { "@type": "Article" } }
```

#### SitemapURLs
Additions only. The host sends `group`, `language`, `segment` and `file` (the group slug of the sitemap file, `"misc"` for the misc sitemap; `language` is the BCP 47 tag of the locale a per-locale file belongs to, and empty on a one-language site; `segment` is that locale's stored URL segment, empty for a default locale served without a prefix; `file` is the full file key, for example `xx-misc`, while `group` stays the bare name; with the site setting `sitemap_naming` on `legacy` a prefixed locale has one file holding all its groups, so `file` is the segment alone (`xx`) and the filter runs once per group in it, with `group` the bare group name each time and the results merged without repeated `loc`; the shared bare files send `file` `blog` or `misc` and no `language` or `segment`; the payload shape and the SDK version do not change; `segment` and `file` are empty on a one-language site and since SDK 0.15) and in `add` the entries that `sitemap.urls` filters before yours already added (empty for the first). It never sends the file's own URL list: a Go guest needs about 0.5 s to decode and encode 5,000 URLs. Append your entries to `add` and return the value; the host appends the ones whose `loc` the file does not already have, so a plugin can neither change nor remove existing entries. Set at most 5,000 entries in `add` in total (more fails the call, fail open). A filter that adds a site-wide URL should target one file (usually `"misc"`). The result is cached with the sitemap XML, so keep it deterministic.
```json
{ "group": "misc", "language": "en-XX", "segment": "xx", "file": "xx-misc",
  "add": [ { "loc": "https://example.com/a", "lastmod": "2026-01-01", "changefreq": "weekly", "priority": 0.5,
             "alternates": [ { "rel": "alternate", "hreflang": "fr", "href": "https://example.com/fr/a" } ] } ] }
```
`alternates` is optional.

#### HeadContext
The input describes the page (`language` is the BCP 47 language of the page: the site language, or the page's locale on a site with several public locales) and carries no tags; since SDK 0.15 `locale` is the page's BCP 47 tag and `segment` its stored URL segment, both empty on a one-language site, and `segment` also empty for a default locale served without a prefix (`language` holds the same tag as `locale` on a multi-locale site); the result's `tags` are **appended** to the page head. The host builds and escapes the markup, drops attributes outside the allowlist per kind (`meta`: `name`, `property`, `content`; `link`: `rel`, `href`, `type`, `hreflang`, `crossorigin`; `script-src`: `src`, `crossorigin`) and unsafe URL schemes.
Set `replace_site_graph` when your `jsonld` tags carry their own Organization/WebSite graph; if the chain fails or no valid `jsonld` tag results, the generic graph stays. The generic graph includes the per-locale `LocalBusiness` node of a site with several public locales, so replacing it drops that node too. The payload of this filter is unchanged by per-locale business details. To extend the generic graph instead of replacing it, use [`render.site_graph`](#sitegraph); when a valid `replace_site_graph` result is accepted the generic graph is not built and `render.site_graph` does not run. Hreflang de-duplication is exact: a plugin link is dropped only when its language (case-insensitive) and its address both equal a computed alternate, so any difference prints both.
```json
{ "path": "/blog/x", "language": "en-XX", "locale": "en-XX", "segment": "xx", "schema_slug": "post", "entity_id": "string",
  "tags": [ { "kind": "meta|link|script-src|jsonld", "attrs": { "name": "robots", "content": "noindex" }, "jsonld": {} } ],
  "replace_site_graph": false }
```
`tags`, `attrs` and `jsonld` are optional; `jsonld` is only used for kind `jsonld`.

#### SiteGraph
Since SDK 0.16.0. The input describes the page (`path`, `language`, `schema_slug`, `entity_id` and, on a site with several public locales, `locale` and `segment`, as in [HeadContext](#headcontext)) and carries the `nodes` of the generic site graph: the Organization, the per-locale LocalBusiness (only on a site with several public locales, for a locale with an office) and the WebSite. Return the nodes; the host merges your answer into its own graph:
- A node with the `@id` of a core node may add keys the core node does not have. It cannot change `@id` or `@type`, and it cannot remove or overwrite a key the core node already prints.
- A node with a new, unique `@id` is appended after the core nodes. A node without an `@id`, or one that repeats an `@id`, is dropped.
- The host does not re-encode core values, so numbers and strings print as before.

Output is bounded like the head filter: more than 64 elements in any one array (at any depth) or more than 20,000 tokens fails the call, so a plugin that echoes a large core array back must trim it or return only the nodes it changes. The filter is fail-open: on an error, a timeout or a panic the generic graph is printed unchanged and the page is not cached (a degraded page, like a failed `render.head`). With `replace_site_graph` accepted from `render.head`, the generic graph is not built and this filter does not run. Like `render.head` it can run more than once for one render (a page with a paged builder loop builds its head twice), so keep it free of side effects.
```json
{ "path": "/", "language": "en-XX", "locale": "en-XX", "segment": "xx", "schema_slug": "page", "entity_id": "string",
  "nodes": [ { "@type": "Organization", "@id": "https://example.test#organization", "name": "Example Co" } ] }
```
`locale` and `segment` are left out on a one-language site.

#### EntityDraft
Return a modified draft to change `name`, `slug` or `attributes` before they are saved; other fields are ignored. `operation` is `create`, `update` or `restore`; `id` is `""` for create.
```json
{ "operation": "create", "id": "", "schema_slug": "post", "name": "string", "slug": "string", "locale": "string", "attributes": {} }
```

#### builder.data
Answers the data-source requests one page render collected for the page builder. Registered only for a plugin that declares `builder_sources` and holds the `builder.data` permission. The input carries only the requests that name the plugin's own sources; return the same object with `results` set, one per request, matched by `id`. Treat `id` as opaque: for a loop with params it carries a `:p<16 hex>` suffix (a hash of the values), so echo it unchanged and never parse it. `params` is present only on an items request whose loop set at least one valid value, see [Source params](#source-params).
```json
{ "path": "/", "language": "en", "schema_slug": "page", "entity_id": "",
  "requests": [ { "id": "string", "source": "ext.<plugin>.<name>", "limit": 12, "params": { "category": "news" } } ],
  "results":  [ { "id": "string", "value": "string", "items": [ { "title": "", "url": "", "excerpt": "", "image": "", "category": "", "author": "", "author_url": "", "published_at": "", "attrs": { "k": "v" } } ], "error": "" } ] }
```
##### Source params
An `items` source can declare up to 8 `params`, each `{ "name", "label", "type", "options" }`: `name` follows the source name rule (`^[a-z][a-z0-9_-]{0,31}$`, unique in the source), `label` is 1 to 60 characters, `type` is `text`, `select`, `bool` or `number`. A `select` has 1 to 50 `options`, each `{ "value", "label" }` (`label` defaults to `value` and is at most 60 characters, values unique, non-empty, at most 200 bytes); no other type takes `options`. `params` on a `value` source, a ninth param, an unknown type, or a bad option list refuses the manifest at load.

The host sanitizes before your plugin sees a value, against your declaration at request time (so it is authoritative, and a plugin update that changes its options takes effect at once): unknown names are dropped; `text` must be a non-empty string of at most 200 bytes; `select` must equal one option value; `bool` must be a JSON boolean and only `true` is sent (`false` and empty values are dropped, so absent means off); `number` must be a finite JSON number and always arrives as a float64 (`3` and `3.0` are the same value). `params` is omitted when nothing survives, and then the request is byte-identical to a request without params. Two loops with the same source, limit and sanitized values share one request whatever the key order; different values give separate requests, and each counts toward the 16-request cap. Saving a page only checks the shape of a loop's stored `params` (an object of at most 32 scalars, strings up to 200 bytes), never your declaration, so a plugin update cannot make a saved page unsaveable.

Caps (the whole result is dropped when broken): at most 16 requests per render, 48 items per result, strings of at most 2048 bytes of valid UTF-8, 16 `attrs` keys per item, output JSON of at most 64 elements per array and depth 6. The default filter timeout is 250 ms (`limits.hook_timeouts_ms["builder.data"]`, up to 10 s) inside a 500 ms budget for the batch; a failure leaves the affected props empty and keeps the page out of the page cache. Full contract, ownership and freshness rules: [WIDGET_PACKS.md](WIDGET_PACKS.md#data-sources).

## plugin.json

```json
{
  "name": "leads",
  "version": "0.2.0",
  "sdk": "^0.8",
  "description": "optional",
  "config": { "greeting": "konnichiwa" },
  "permissions": ["forms.read", "kv", "db", "mail.send", "http.fetch"],
  "allowed_hosts": ["api.example.com", "*.googleapis.com"],
  "routes": [
    { "method": "GET",  "path": "/leads", "roles": ["owner", "editor"] },
    { "method": "POST", "path": "/hooks/{id}", "public": true }
  ],
  "cron": [ { "name": "digest", "every": "15m" } ],
  "cookies": ["unlock"],
  "funcs": ["badge"],
  "limits": { "memory_pages": 256, "hook_timeouts_ms": { "render.page": 100, "entity.published": 5000 }, "max_instances": 4 },
  "settings_schema": { "type": "object", "properties": {} }
}
```

| Field | Meaning |
|---|---|
| `name` | `^[a-z][a-z0-9-]{1,39}$`; equals the directory name. `browse`, `security` and `webhooks` are reserved: a new install under them is refused (the admin serves `/plugins/browse` and `/plugins/security`), while a plugin already installed as `browse` or `security` can still be updated and uninstalled |
| `version` | the plugin's own semver |
| `sdk` | SDK range the plugin was built against (`^0.8`, `>=0.5 <0.9`); the host refuses a plugin whose range does not contain its SDK version. This SDK is 0.27.0 and also serves `^0.5` to `^0.26` plugins |
| `license` | `""` (default) or `"commercial"` (SDK 0.26). A commercial plugin installs and runs only on a host whose edition registered a plugin key (`edition.RegisterPluginKey`), is signed by that key and is entitled (`edition.PluginEntitled`). Declare `"sdk": "^0.26"` or later: an older host ignores the field, so install refuses a range that admits one. A new install refuses any other value (`"Commercial"`, `"MIT"`, an object); a plugin already installed with one keeps loading as a normal plugin, because only the exact string `"commercial"` has meaning when a manifest is loaded |
| `description` | optional |
| `config` | string map readable in the guest (`pdk.GetConfig`) |
| `permissions` | capabilities the plugin asks for; see [Permissions](#permissions). Unknown names are refused |
| `allowed_hosts` | allowlist for `http.fetch` and `packages.inspect`: exact hosts, `*.suffix` (subdomains only, not the apex), `host:port`, or `*` (any public host, for plugins that call URLs the owner configures). Without a port only 80 and 443 are allowed, `*` included; `*:8443` lists a port explicitly. A `*` entry needs the separate `http.fetch.any` permission (below) in `permissions`, and works only while the owner has granted it. The address guard still refuses private addresses. It does nothing unless `http.fetch` or `packages.inspect` is granted |
| `routes` | HTTP routes the plugin serves; see [Routes](#routes) (at most 64) |
| `cron` | scheduled calls; see [Cron](#cron) (at most 16) |
| `cookies` | cookie names `render.page` and the plugin's public routes may read and set (at most 8; `[A-Za-z0-9_-]`) |
| `redirect_paths` | exact public paths the `request.redirect` filter is called for, at most 16 (SDK 0.22; the plugin must declare `"sdk": "^0.22"`); see [request.redirect](#requestredirect). Paths are matched against the decoded request path, so declare them decoded: a `%` is refused. The host refuses paths it serves itself and a path another plugin already declares |
| `page_paths` | exact public paths the `render.page` filter is called for and that stay uncached, at most 16 (SDK 0.24; no trailing slash except `/`; otherwise the shape rules of `redirect_paths`; the host refuses core paths and duplicates within the manifest; several plugins may declare the same path). Request paths, not pages: locale or alternate URLs are not covered. An older host ignores it and calls the filter on every page, so the filter must check `p.Path` itself unless you declare `^0.24` |
| `page_query` | query keys the plugin's `render.page` filter reads (SDK 0.27, declare `^0.27`): at most 16 keys of `[A-Za-z0-9_-]`, 1 to 64 bytes each, no duplicates. The host hands `PageContext.query` the **first value** of each declared key only; a value over 256 bytes is dropped (the key is absent). Keys not declared are never passed |
| `page_path_prefixes` | path prefixes the `render.page` filter is called for and that stay uncached, at most 8 (SDK 0.27, declare `^0.27`). Each starts and ends with `/`, is 2 to 256 bytes and follows the character rules of `page_paths`. `/` and any prefix that equals or covers a core path (`/p/`, `/admin`, `/api/`, `/uploads/`, `/media/`, `/theme/`, `/-/`, the sitemap and robots paths) are refused. `/item/` matches `/item/<x>` and not `/item`. Prefix pages are uncached like exact ones. A 404 under a prefix renders afresh each request and does not call the plugin |
| `entity_schema` | the one content type the plugin owns (SDK 0.27, declare `^0.27`): a slug matching `^[a-z]([a-z0-9]\|-[a-z0-9]){0,39}$` (dashes, no underscores, at most 40 bytes). `entities.write.own` or `comments.moderate` requires it, and it requires `entities.write.own`. The built-in types `page`, `post`, `person` and `block` are refused. The package ships `schemas/<slug>.json`, and install is refused without it |
| `entity_fields` | attribute keys the plugin may write on its `entity_schema` (SDK 0.27, declare `^0.27`): at most 16, each `[a-z][a-z0-9_]*`. The entity name and `status` are always writable, nothing else is. Requires `entity_schema` |
| `request_headers` | extra request header names passed to the plugin's routes, at most 8 (SDK 0.18). `authorization`, `cookie`, `proxy-authorization`, `x-forwarded-for`, `x-real-ip`, `forwarded` and `cf-connecting-ip` are refused by name, and so is any header whose name carries an address (`ip`, `client`, `real`, `for`, `addr`, `forwarded` as a word, such as `true-client-ip` or `fastly-client-ip`), `proxy-*` and the hop-by-hop headers |
| `csp` | `{ "script_src": [], "connect_src": [], "frame_src": [], "img_src": [] }`, each at most 8 entries of `https://host` or `https://*.host` (SDK 0.18; no wildcard in `script_src`, none over a shared suffix such as `*.github.io`, no IP address). The hosts join the public pages' Content-Security-Policy only while the plugin is installed and enabled: `script-src` and `connect-src` change what a page may load; `img-src` and `frame-src` are accepted and shown but change nothing while the base policy allows every https origin there. A community plugin may only list `img_src` (other lists are refused at install). An update that adds a host asks the owner to confirm. Composed in one place (`cspWithExtras` in the render handlers), per response, so enabling or disabling applies at once |
| `render_page` | optional; `{ "full_blocks": true }` sends `render.page` the whole block content (SDK 0.9+; older plugins always get it) |
| `builder_sources` | data sources for the page builder (at most 8; needs the `builder.data` permission): `[{ "name", "label", "kind": "value"\|"items", "class": "text"\|"url"\|"image", "params": [...] }]` (`class` defaults to `text`; value sources only; `params`, at most 8 typed filters, items sources only, see [Source params](#source-params)); see [builder.data](#builderdata) |
| `funcs` | template functions; see [Templates](#template-functions-partials-and-sections) (at most 64; `^[a-z][a-z0-9_]{0,31}$`) |
| `limits` | see below; all optional |
| `settings_schema` | optional JSON Schema (2020-12); backs `GET/PUT /api/v1/plugins/{name}/settings` and `settings.get` |

## Permissions

A plugin always loads. What it may *do* is decided by the owner. Nothing is granted by default.

- `GET /api/v1/plugins` (owner) lists each plugin with `permissions` (requested), `granted`, `routes`, `cron`, `cookies`, `redirect_paths`, `funcs` and `data_error` (why its own data is not ready, if so) and `templates_refused` (the sections and partials that are not registered; absent when none). Each entry is a string that carries its reason, and it also lists a template stopped by the output limit. `templates_paused` (templates the range breaker skips for now, each with `kind`, `name` and `until`, an RFC 3339 UTC time; absent when none). The list is filled when an install, an upgrade or an enable finishes, and when a server picks up a CLI install, so no page view is needed first.
- `POST /api/v1/plugins/{name}/grant` (owner) with `{"permissions": ["kv", "db"]}` sets the granted set. The list **replaces** the earlier grant (grant fewer to revoke) and must be a subset of what `plugin.json` requests (`400` otherwise). The response is the plugin's view. Grants live in Postgres (`hatamoto_plugin_grants`) and apply at once; granting `kv` or `db` also prepares the plugin's data (see [Own data](#own-data)).
- A host function called without its grant returns the typed error `{"code":"capability_denied","capability":"<permission>"}` (plus a `message`); the denied operation never reaches the service behind it, and the plugin keeps running. In Go, `guest.CapabilityDenied(err)` returns the permission name.

| Permission | Host functions |
|---|---|
| `entities.read` | `entities.get`, `entities.list`, `entities.translations` |
| `terms.read` | `terms.list`, `terms.get` |
| `menus.read` | `menus.list`, `menus.get` |
| `media.read` | `media.get`, `media.list` |
| `redirects.read` | `redirects.list` |
| removed in 0.17 | `entities.write`, `terms.write`, `menus.write`, `media.write`, `redirects.write`: no longer exist. Use a run and the `changeset.*` functions (no permission needed). A manifest that lists one is refused at load. `entities.write` stays removed; `entities.create\|update\|delete` return under `entities.write.own` (SDK 0.27) |
| `forms.read` | `forms.list`, `forms.submissions`, and delivery of the `form.submitted` action |
| `comments.read` | `comments.thread`, `comments.counts`, `comments.by_author`, `comments.subtree`, delivery of the `comment.created`, `comment.approved`, `comment.spam`, `comment.updated` and `comment.deleted` actions, and the `comment.check` and `comment.draft` filters (without the grant none of these is registered). With the grant, comment payloads and items also carry a pseudonymous `commenter_key` for each commenter (never the email; see [Visitor and commenter keys](#visitor-and-commenter-keys-sdk-027)). Plugins never create comments; moderation is `comments.moderate`. Not a warning permission. A plugin that asks for it declares `^0.25`; `comments.by_author`, `comments.subtree`, `comment.updated`, `comment.draft` and `commenter_key` need `^0.27` |
| `settings` | `settings.get` (the plugin's own settings) |
| `log` | `log` |
| `mail.send` | `mail.send` |
| `http.fetch` | `http.fetch`, to the hosts in `allowed_hosts` |
| `http.fetch.any` | lets `http.fetch` and `packages.inspect` honour a `*` / `*:port` entry of `allowed_hosts`: **any public web address**. The prompt shows it as a warning, `package.grants` and the install/update audit record it, and an update that newly asks for it (or lists new hosts) needs the owner's confirmation and clears the plugin's grants, like a change of signer. Needs `http.fetch` too |
| `packages.inspect` | `packages.inspect`: download a Hatamoto package from an https URL and report what it is and whether a key signed it. It reaches only the hosts the plugin may reach with `http.fetch` (its `allowed_hosts`, redirects included). The prompt reads "Download and check Hatamoto packages from the web" and lists those hosts as "Web addresses it may call". It installs nothing and is not a warning permission |
| `kv` | `kv.get`, `kv.set`, `kv.delete`, `kv.list` |
| `db` | `db.query`, `db.exec`, and the plugin's migrations |
| `site.scripts` | no host function: the plugin's browser JavaScript runs on your public pages. Needed for `assets/plugin.js` to be linked and for `script-src` tags of a `render.head` filter to be emitted. The prompt shows it as a warning: "Runs its JavaScript on every public page, with full access to the site, including signed-in admins. Without it the plugin's script is not loaded." (an update that newly asks for it, and the plugin's page in the admin, repeat that wording). The warning is deliberately broad. The script has the visitor's page privileges and the admin is sealed against it, with the limits listed in `docs/SECURITY_MODEL.md` (Admin isolation); an update that newly asks for it needs the owner's confirmation and clears the grants. `sections/*.html` and `partials/*.html` that contain scripts are also stopped without it. A plugin that ships a script without the grant shows a note in the admin with a link to grant it |
| `builder.island` | no host function: a widget pack whose `widgets/*/island.js` runs JavaScript on your site's pages. Required in `plugin.json` when the package ships an island; shown as a warning in the prompt; an update that newly asks for it needs the owner's confirmation and clears the grants. Without the grant the widgets render but no island is served |
| `builder.data` | the `builder.data` filter: the plugin answers page-builder data requests for the sources it declares in `builder_sources` (no host function; without the grant the filter is not registered) |
| `entities.filter` | the `entity.before_save` filter: the plugin sees every entity draft as it is saved and may change it or refuse the save (no host function; without the grant the filter is not registered). A plugin that asks for it declares `^0.23` |
| `client.key` | no host function: the plugin sees a pseudonymous `addr_key` and `net_key` for each visitor on its pages and routes, and `area_key` in `comment.check` (SDK 0.27, declare `^0.27`). A warning permission: "Pseudonymous visitor keys." The keys are not the address, but they are stable, so the plugin can recognise a returning visitor and block a network; they are personal data and your privacy notice should say so. An update that newly asks for it needs the owner's confirmation and clears the grants |
| `entities.write.own` | `entities.create`, `entities.update`, `entities.delete` on the one content type declared in `entity_schema`, and only on entries the plugin created (SDK 0.27, declare `^0.27`). A warning permission: entries publish at once, without the owner's review. 60 writes per minute per plugin, burst 20, fixed and not owner-tunable; audit actions `plugin.entity.create`, `plugin.entity.update`, `plugin.entity.delete`. An update that newly asks for it needs the owner's confirmation and clears the grants |
| `comments.moderate` | `comments.mark_dead`, `comments.restore`, `comments.set_rank`, `comments.set_viewer`, only for comments on the plugin's own `entity_schema` (SDK 0.27, declare `^0.27`). A warning permission: it changes what visitors see without the owner's review. It cannot post comments or touch other content types. Same limiter as `entities.write.own` (60 per minute per plugin, burst 20, fixed); audit action `plugin.comment.moderate`. An update that newly asks for it needs the owner's confirmation and clears the grants |

## Host functions

There is one import, `hm_call` in wasm module `extism:host/user`. It takes a pointer to a JSON request in Extism memory and returns a pointer to a JSON response. The Go guest wraps it as `guest.Call(fn, args, &result)`; another language declares the import with its PDK's host-function support (`extism:host/user`, one `i64` pointer in, one `i64` pointer out) and frees the response.

```json
{ "fn": "kv.get", "args": { "key": "k" } }
```
```json
{ "ok": true,  "result": { "found": false } }
{ "ok": false, "error": { "code": "capability_denied", "capability": "kv", "message": "kv.get needs the \"kv\" permission, which the owner has not granted" } }
```

Error codes: `capability_denied` (the error also has `capability`: the missing permission), `invalid_argument` (bad or unknown argument, validation failure, a conflict), `not_found`, `rate_limited`, `blocked` (refused by policy: http.fetch allowlist or address guard, SQL guard, database privileges), `reentrant`, `timeout`, `unavailable` (the backing service is not configured, or the plugin's database role is missing; also a call to a function removed in 0.17, and, since SDK 0.27, a call to a 0.27 contract function whose implementation has not shipped: `<fn> is not available`, which a plugin treats as "feature off"), `internal`, `unknown_function`, `phase_denied` (the function is not available in the current run phase, or is a run function called outside a run) and `not_owned` (a run tried to change an object the owner or another source owns, see [Runs and changesets](#runs-and-changesets); or, for `comments.mark_dead`, `comments.restore` and `comments.set_rank`, a comment that is not on a content type the plugin created). Arguments are strict: an unknown field is `invalid_argument`. Every call has a deadline (at most 15 s and never longer than the hook call that is running), a request is at most 1 MiB and a result at most 2 MiB (ask for less: `limit`/`cursor`). IDs are UUID strings.

| Function | Arguments | Result |
|---|---|---|
| `permissions` (always allowed) | none | `{ "granted": [..], "requested": [..], "mail"?: { "recipients_per_hour": n } }`; `mail` only when `mail.send` is granted (SDK 0.22.1). |
| `entities.get` | `{ "id" }`, `{ "path": "/a/b" }`, or `{ "slug", "parent_id"?, "schema_slug"? }` (`schema_slug` needs `parent_id`; send one selector only) | Entity |
| `entities.list` | `{ "schema_slug"?, "status"?, "search"?, "term"? (`{ "kind", "slug" }`, needs `terms.read`), "limit"? (50, max 200), "cursor"? }` | `{ "items": [Entity], "next_cursor" }` |
| `entities.translations` | `{ "id" }` | `[ { "id", "locale", "status", "path"? } ]` (the entity's translation group, itself included, ordered by locale; `[]` when it has none or is alone) |
| `terms.list` | `{ "kind"?, "search"?, "limit"? }` | `{ "items", "total" }` |
| `terms.get` | `{ "kind", "slug" }` | Term |
| `menus.list` / `menus.get` | `{}` / `{ "slug" }` | menus / menu with items |
| `media.get` / `media.list` | `{ "id" }` / `{ "search"?, "folder"?, "mime_type"?, "limit"?, "offset"? }` | media (id, filename, mime_type, file_size, public_url, width, height, alt_text). Folders are one level (`/Name`, 1 to 60 letters, digits, spaces, hyphens or underscores); `folder` matches ignoring case and a leading slash, and a legacy name that no longer follows the rule can still be filtered on. |
| `redirects.list` | `{ "search"?, "page"?, "per_page"? }` | `{ "items", "total" }` |
| `forms.list` | `{}` | `{ "items": [ { id, name, slug, status, fields } ] }` |
| `forms.submissions` | `{ "form_slug", "limit"?, "offset"? }` | `{ "items": [ { id, status, data, created_at } ], "total" }`. IP address and user agent are never included |
| `comments.thread` | `{ "entity_id", "page"?, "include_dead"? }` | `{ "items": [ CommentItem ], "page", "has_more"? }`, with `CommentItem` = `{ id, parent_id?, depth?, author_name, staff?, body_html, commenter_key?, rank?, status?, created_at }` (`commenter_key`, `rank` and `status` since SDK 0.27; `status` is `approved` or `dead`; `rank` and `status` are omitted when zero-valued: an absent `status` means `approved` and an absent `rank` means 0). `include_dead` is honoured only with `comments.moderate`, and then only on the plugin's own content type: its dead comments come with their body and author. Otherwise a dead comment appears as a placeholder (empty `author_name` and `body_html`, `status` `dead`), so replies keep their parent. Approved and dead comments, of a published entity whose comments are shown; an entity that is not public, or whose comments are hidden or off, gives an empty thread, never an error. `page` is clamped. The email, hashes, IP address, user agent and author web address are never included |
| `comments.counts` | `{ "entity_ids": [1 to 100 ids] }` | `{ "counts": { "<id>": n } }`: approved comments per entity, `0` for an entity that is not public or has comments hidden or off. An empty list or more than 100 ids is `invalid_argument` |
| `comments.by_author` (`sdk.FnCommentsByAuthor`, SDK 0.27) | `{ "commenter_key", "page"?, "include_dead"? }` | `CommentThread`: a flat list of `CommentItem`, newest first, 50 per page, approved only (dead ones too with `include_dead` and `comments.moderate`). Needs `comments.read` |
| `comments.subtree` (`sdk.FnCommentsSubtree`, SDK 0.27) | `{ "comment_id", "page"?, "include_dead"? }` | `CommentSubtree` = `{ "ancestors": [CommentItem], "items": [CommentItem], "page", "has_more" }`: the comment and its subtree in path order, and its ancestors, root first. `include_dead` is honoured only with `comments.moderate`. Needs `comments.read` |
| `entities.create` (`sdk.FnEntitiesCreate`, SDK 0.27) | `EntityWrite` = `{ "name", "slug"?, "locale"?, "attributes", "status"? }` | Entity. `status` is `published` (default) or `draft`. Created in the plugin's `entity_schema` only, with origin `plugin:<name>`. Needs `entities.write.own` |
| `entities.update` (`sdk.FnEntitiesUpdate`, SDK 0.27) | `EntityWrite` = `{ "id", "name"?, "attributes"?, "status"? }` | Entity. Only the keys in `entity_fields`, the name and the status, and only on entries the plugin created in its schema. Needs `entities.write.own` |
| `entities.delete` (`sdk.FnEntitiesDelete`, SDK 0.27) | `{ "id" }` | `{}`. A hard delete of an entry the plugin created in its schema. Needs `entities.write.own` |
| `comments.mark_dead` (`sdk.FnCommentsMarkDead`, SDK 0.27) | `{ "ids": [1 to 100 ids] }` | `{ "changed": n }`. All or nothing: an unknown id or one on a content type the plugin did not create is `not_owned` and nothing changes. Needs `comments.moderate` |
| `comments.restore` (`sdk.FnCommentsRestore`, SDK 0.27) | `{ "ids": [1 to 100 ids] }` | `{ "changed": n }`. All or nothing: an unknown id or one on a content type the plugin did not create is `not_owned` and nothing changes. Needs `comments.moderate` |
| `comments.set_rank` (`sdk.FnCommentsSetRank`, SDK 0.27) | `{ "items": [ CommentRankItem ] }`, 1 to 200 items of `{ "id", "rank" }` (`rank` is a number) | `{ "changed": n }`. Higher rank sorts first among siblings, at every level (top-level comments and the replies under each comment), when the content type orders by rank; ties and unranked comments (rank 0) are newest first, so rank the replies too if their order matters. Fires no event; each entity's cached page is invalidated at most once per batch and once per 10 seconds. All or nothing, `not_owned` as above. Needs `comments.moderate` |
| `comments.set_viewer` (`sdk.FnCommentsSetViewer`, SDK 0.27) | `{ "commenter_key", "show_dead" }` | `{}`. Stores the show-dead flag for that commenter; it affects only the plugin's own content type and takes effect within 30 seconds. `show_dead` false clears the flag. Needs `comments.moderate` |
| `settings.get` | `{}` | the plugin's own settings object, schema defaults merged in (write-only values included: they are the plugin's own secrets) |
| `log` | `{ "level": "debug|info|warn|error", "message" }` | `{}`. At most 10 lines/s (burst 20), then `rate_limited`; messages are cut at 4 KiB |
| `mail.send` | `{ "to": [1..10 plain addresses], "subject", "text"?, "html"? }` | `{ "sent": n }`. Goes through the site's mail service and From identity; owner-set recipients per hour (default 360, which is 6 a minute; 1 to 3600, set by the owner on the plugin Permissions tab; burst clamp(N/36, 10, 100), so a very low quota still allows a short burst of up to 10; one token per recipient), then `rate_limited` "mail quota (N recipients per hour) reached"; `unavailable` when mail is not configured. The plugin cannot change the limit |
| `http.fetch` | `{ "method"?, "url", "headers"?, "body"? or "body_b64"?, "timeout_ms"? }` | `{ "status", "headers", "body" or "body_b64", "truncated"? }`. At most 600 requests per plugin per minute (`HATAMOTO_PLUGIN_FETCH_PER_MINUTE`, burst 60), then `rate_limited`. `truncated` means the body was cut at 1 MiB: treat it as an error, ask for less (the Search Console plugin sends `prettyPrint=false` and pages of 1,000 rows) |
| `packages.inspect` | `{ "url", "signer_key"? }` | `{ "kind", "name", "version", "sha256", "size", "signer_key_id", "signature_ok", "sdk"?, "permissions"?, "allowed_hosts"?, "description"?, "problems" }`. Spends one token of the plugin's `http.fetch` rate limit; see **packages.inspect** below |
| `kv.get` | `{ "key" }` | `{ "found": bool, "value"? }` |
| `kv.set` | `{ "key", "value" (any JSON, at most 256 KiB), "ttl_seconds"? }` | `{}` |
| `kv.delete` / `kv.list` | `{ "key" }` / `{ "prefix"?, "limit"? (100, max 1000) }` | `{}` / `{ "keys": [..] }` |
| `db.query` | `{ "sql", "args"? }` | `{ "columns", "rows": [[..]], "truncated"? }` (at most 1,000 rows and 1 MiB) |
| `db.exec` | `{ "sql", "args"? }` | `{ "rows_affected" }` |

**0.27 functions.** The nine functions above marked SDK 0.27 (`entities.create`, `entities.update`, `entities.delete`, `comments.mark_dead`, `comments.restore`, `comments.set_rank`, `comments.set_viewer`, `comments.by_author`, `comments.subtree`) are part of the contract now. `entities.create`, `entities.update` and `entities.delete` are implemented (see [Entity writes by a plugin](#entity-writes-by-a-plugin-entitieswriteown)); the four moderation functions `comments.mark_dead`, `comments.restore`, `comments.set_rank` and `comments.set_viewer` are implemented (see [Comment moderation](#comment-moderation-sdk-027)); `comments.by_author` and `comments.subtree` answer `unavailable` until the change that implements each one ships; without the grant the usual `capability_denied` comes first. The write functions share one limiter: 60 calls per minute per plugin, burst 20, then `rate_limited`; the audit actions are `plugin.entity.create`, `plugin.entity.update`, `plugin.entity.delete` and `plugin.comment.moderate`. The usual error codes apply (`rate_limited`, `capability_denied`, `not_owned`, `invalid_argument`, `not_found`, `unavailable`). A plugin never creates comments.

### Entity writes by a plugin (`entities.write.own`)

With `entities.write.own` and a manifest `entity_schema`, a plugin creates, edits and deletes entries of that one content type with `entities.create`, `entities.update` and `entities.delete` (SDK 0.27). The entries go through the same entity service as the admin: schema validation, HTML sanitising, slug rules, link index and events all apply. Nothing is held for review: a created entry is **published at once** unless the plugin asks for `draft`.

- **One content type.** `entity_schema` names it and the package ships `schemas/<slug>.json`. Ownership is checked on every call against the plugin that installed the type, never trusted from the manifest alone: a plugin loaded from `HATAMOTO_PLUGIN_DIR` (never installed) can write only a type that an installed package of the same name created. A built-in type, an owner-made type or another plugin's type answers `not_owned` and writes nothing.
- **Own entries only.** An entry created by a plugin has origin `plugin:<name>`. `entities.update` and `entities.delete` refuse (`not_owned`) an entry the owner or another source made; an unknown id is `not_found`. The message names neither the content type nor the origin. The owner keeps the last word on publishing: a plugin may set `published` only on an entry that has never been published. A plugin cannot republish an entry that was published and then unpublished (the owner or the plugin unpublished it): only the owner can publish it again, and an update that asks for `published` is `invalid_argument` ("only the owner can republish this entry"). It cannot update or delete an archived or scheduled entry either (`invalid_argument`). It can still delete its own unpublished drafts and create new entries, so this is not a takedown guarantee: against a misbehaving plugin the owner revokes `entities.write.own` or uninstalls it. An owner's body or SEO edit takes the entry over under the ownership rule below; a rename or an edit of other fields does not, and a later plugin update may overwrite it. Uninstalling the plugin and keeping its content hands the entries in the plugin's own content types to the owner (their `updated_at` is not changed), so a later package with the same name cannot change them; entries a changeset imported into other types keep their origin.
- **Writable.** `name`, the attribute keys listed in `entity_fields` (create and update) and `status`: `published` (the create default) or `draft`. `slug` and `locale` are set at create only (without a slug one is derived from the name, a taken slug gets a numeric suffix; the locale defaults as for the admin). An update merges `attributes` into the stored ones at the top level, and a `null` value removes the key; author, parent, publish date and origin cannot be set. Any other attribute key, or a status other than `published` or `draft`, is `invalid_argument`, and so is an update that sets nothing or a blank `name`.
- **Size.** At most 64 KiB per string, 4,096 strings (map keys included) and 256 KiB of attributes per call, else `invalid_argument`.
- **Delete** is a hard delete: the entry and its comments are removed. An entry that has child entries cannot be deleted (`invalid_argument`).
- **Rate limit.** One limiter per plugin shared by the three functions and the comment moderation functions: 60 writes per minute, burst 20, not owner-tunable; over it `rate_limited` ("entities.create: 60 per minute"). A call refused for its arguments or its grant spends no token; one the service refuses (`not_owned`) does.
- **Audit.** Each successful write adds one row to the audit log: `plugin.entity.create`, `plugin.entity.update` or `plugin.entity.delete`, actor `plugin:<name>` (kind `plugin`), target the entry id, detail with the plugin, the content type and the name (create, delete) or the changed fields (update). The row is written in the same transaction as the write, so a registered audit sink that refuses it refuses the write; a call that is cancelled or times out before the commit rolls back and stores nothing (no audit row, no events), and once the commit has happened the reload and events still run. Refusals write no row, and a plugin write adds no `entity.*` row.
- **Hooks and events.** No `entity.before_save` filter runs and no visitor's session applies to the write. A create emits `entity.created` and, when published, `entity.published`; an update emits `entity.updated` (or `entity.published` when it takes a draft live); a delete emits `entity.deleted`. A plugin that reacts to those actions in the same plugin can write again; the limiter bounds the loop.
- **Updates.** An update of the plugin that changes `entity_schema`, or adds keys to `entity_fields`, needs the owner's confirmation and clears the plugin's grants, like any widened access. Removing keys or the permission never asks.
- **Uninstall.** Uninstalling with "remove content types" deletes the type and its entries, as for any package that ships a schema.

**Page-cache cost.** Every rendered page carries the any-entity tag, so each write drops every cached page and all sitemaps at once (the entity hook; a published create fires two events and so two drops). The hook's drop is by tag, not a flush of the whole cache: an in-flight store that carries no entity tag is unaffected. On top of that the content-change poller adds full purges, coalesced: the first change after a quiet period purges at once, then at most one per gap while writes keep coming, plus one trailing purge. The gap is 15 s in production (poll every 3 s) and 5 s in development, so the sustained limit of 60 writes a minute costs at most 60 / 15 + 2 = 6 poller purges a minute in production. A published page stays cold only until its next render. Keep votes, scores, ranks and counters in the plugin's own database (`db`), never in entity attributes: every entity update purges the page cache, a `db` write does not. `TestPluginEntityWriteCacheCost` pins these numbers.

### Comment moderation (SDK 0.27)

`comments.mark_dead`, `comments.restore`, `comments.set_rank` and `comments.set_viewer` need `comments.moderate` and act only on comments of the plugin's own `entity_schema`. Ownership is the content type's creator, the plugin that installed it, and is checked on every call: a plugin that names a type another plugin created gets `not_owned`. A plugin loaded only from the development directory (`HATAMOTO_PLUGIN_DIR`) owns no content type, because that load does not install its types, so every moderation call answers `not_owned` until the same plugin has been installed once.

- **Limits.** 1 to 100 ids per `mark_dead` or `restore`, 1 to 200 items per `set_rank`; more or fewer is `invalid_argument`. The batch is all or nothing: one unknown or foreign id gives `not_owned` and changes nothing.
- **Rate.** 60 calls per minute per plugin, burst 20, shared with `entities.write.own`, one token per call whatever the batch size, then `rate_limited`.
- **Audit.** Each successful call writes the audit action `plugin.comment.moderate`.
- **Dead.** A dead comment stays in its thread as a placeholder, its replies stay, and it is excluded from comment counts. It is never purged. Only approved comments can be marked dead, so a held (pending) comment stays in the moderation queue; others are skipped and not counted in `changed`. `restore` makes a dead comment approved again. Visitors see the placeholder; the author sees their own text, and a visitor the plugin flagged with `set_viewer` sees every dead comment of that content type. The same applies to `comments.thread` with `include_dead`.
- **Events.** `mark_dead` and `restore` fire `comment.updated` for each changed comment, also to the plugin that made the call. `set_rank` fires nothing.
- **Caches.** A `set_viewer` change takes effect within 30 seconds.

A **Term** may carry an optional `seo` object, `{ "meta_title", "meta_description", "canonical_url", "og_image", "no_index" }` (every member optional, the whole object left out when nothing is saved): the search appearance an owner set on the term's edit page. It is an additive field of `terms.get` and `terms.list` results and is read-only through the host API, the REST term routes and MCP; edit it on the term's admin page (`changeset.term.upsert` does not set it).

Menu items: `link_type` is `entity`, `custom`, `external`, `archive` or `none`. A `none` item has no link and is shown as plain text, for example a heading above a group of links (existing `#` items were migrated to it). Menu items are staged with `changeset.menu.upsert`, whose items carry a `label` and a `url` or `entity_key`; `menus.get` returns the item types. An item of another type saved without its target also renders as plain text. Every item carries an `attrs` object, `{}` when empty, with `style`, `color`, `badge` and `icon` (ids of presets the active theme declares; an id the theme does not know is kept and not shown), `show_on` (`desktop` or `mobile`; empty means all devices), `id`, `title`, `aria_label` and `rel` (a list of `nofollow`, `sponsored`, `ugc`, `me`). `css_classes` is at most 200 characters; it must not contain control characters, quotes, backticks or angle brackets.

**`render.site_graph` and `render.head` can run more than once per render** (a page with a paged builder loop builds its head twice). A page served from the cache does not run them at all, so keep the filter free of side effects (no `kv` counters or outbound pings).

**Cached pages and plugin data.** What a plugin renders into a public page (template funcs, `render.head`, `render.site_graph` and `render.page` output) is cached with the page for `HATAMOTO_PAGE_CACHE_TTL` (default 5 minutes, up to 24 hours), including values a func reads with `kv.get` or `db.query`. A plugin cannot purge the page cache (there is no `cache.invalidate` host function): new `kv`/`db` data reaches cached pages when the TTL expires or when anything else clears the cache (an entity write, a publish of a changeset, a settings save, a menu or theme change). See [Page cache](THEMES.md#page-cache). A `render.page` plugin that declares `page_paths` or `page_path_prefixes` makes only those pages uncached; without it, no page is cached.

**`page_sections` is derived.** `entities.get`/`entities.list` and the `entity.before_save` input carry `attributes.page_sections` computed from `attributes.page_doc` at read time (the same list the public JSON shows), and the key is absent for a builder-only page. The stored key is a frozen legacy mirror that is never read here. A write that sends back the derived `page_sections` unchanged is accepted as a no-op; one that changes it on a page that has a `page_doc` is refused (`page_sections_removed`), and a `before_save` edit to it is discarded with a warning: change `page_doc` instead.

**Entity writes** happen only when the owner publishes a changeset, through the same service as the admin API: schema validation, HTML sanitising, slug rules and events all apply. A plugin writes directly only through `entities.write.own` (SDK 0.27, its own content type: see [Entity writes by a plugin](#entity-writes-by-a-plugin-entitieswriteown)); otherwise it does not write directly, so the old re-entrancy guard for plugin writes is gone and `reentrant` is no longer returned (the code stays in the list for older plugins). Reads are never guarded. `entity.before_save` filters are not run on changeset content (see [Runs and changesets](#runs-and-changesets)).

**Entity trees (SDK 0.10).** An Entity is `{ id, schema_slug, slug, locale, name, status, version, parent_id?, path?, url?, attributes, published_at?, updated_at, terms? }`. `parent_id` is the parent entity (absent for a root); `path` is the canonical public path the site serves it at (`/services/seo/local`, from the schema's URL pattern and the parent chain); `url` is `path` behind the public origin when `PUBLIC_BASE_URL` (or the owner's site address) is set, otherwise the path alone. A changeset entity upsert takes `parent_key` (the entity service applies the rules of the admin and REST API when the changeset is published): the parent must exist, the move may not make the entity its own ancestor, and the chain may not be deeper than a URL can express (8 levels); a refused parent is `invalid_argument`. Slugs are unique per schema and locale, not among all siblings (a page and a post can share a slug at the root), so `entities.get` with `{ "slug", "parent_id", "schema_slug" }` can match one row per locale. It returns the site-language row when there is one (`"parent_id": null` means a root row), otherwise the one other-locale row, and it finds drafts; when no site-language row exists and two other-locale rows match, it is `invalid_argument` (no row is guessed). There is no `locale` selector for slugs: reach another locale's row by `id` (from `entities.list`, the entity payloads or `entities.translations`), or by its locale-prefixed `path`, never by slug. Without `schema_slug`, rows of several schemas sharing the slug give `invalid_argument` instead of a guess, so always send it when you act on what you find; `schema_slug` without `parent_id`, or mixing `id`/`path`/`slug`, is `invalid_argument`; a bare `{ "slug" }` returns the site-language row first, then a root row before a nested one, then the oldest. `{ "path": "/a/b" }` resolves a URL with the router the public site uses, so it finds exactly what a visitor would get (published entities only; a trailing slash is ignored; redirects are not followed). For a plugin whose `sdk` range requires 0.15 or later, on a multi-locale site a locale-prefixed path such as `/xx/a/b` is read in that locale, as the public site does, and a path of a hidden locale is `not_found`; an unprefixed path is read as before. A plugin built for `^0.14` or older gets no prefix handling and resolves paths exactly as before 0.15. Any other path is `not_found`.

**Translations (SDK 0.15).** An Entity carries `translation_group`, the id its translations share; it is absent when the entity has none or the host has no translation groups (a missing field always means the entity is ungrouped; a failed lookup in `entities.get` or `entities.list` returns `internal`). `entities.translations` with `{ "id" }` returns the members of that entity's group, the entity included, ordered by locale, each as `{ id, locale, status, path? }` (`path` is the canonical public path, absent when the host cannot compute paths). A group of one entity counts as no group: the result is `[]`. The host does not copy or inherit fields between translations: a plugin that wants inheritance reads the source with `entities.get`, and stages the copy with `changeset.entity.upsert`.

**Terms (SDK 0.20).** For a plugin whose `sdk` range requires 0.20 or later and that holds `terms.read`, an Entity carries `terms`: `[{ kind, slug, name }]`, ordered by kind, then name. An entity with no terms has `"terms": []`; every other plugin gets no `terms` key, so plugins built for `^0.19` or older, and plugins without `terms.read`, see the same result as before. A failed lookup returns `internal`, never a missing key. `entities.list` takes `term: { "kind", "slug" }` and keeps only entities carrying exactly that term (not entities that carry one of its child terms). It needs `terms.read` (`capability_denied` without it, whatever the `sdk` range); a term that does not exist gives `{ "items": [], "next_cursor": null }`, and a malformed `kind` is `invalid_argument`. The terms of a page are read with one batched query. In a run's fetch phase the terms and the filter are available as well, on public entities only (the visitor rule applies before terms are loaded); `terms.list` and `terms.get` stay transform-phase functions. `EntityEvent` and the `entity.*` event payloads do not carry terms: read them with `entities.get`.

**http.fetch** and **packages.inspect** are the only server-side network a plugin has, and both are limited to the same `allowed_hosts` list (markup and CSS it ships can still make visitors' browsers load resources from other hosts; see `docs/SECURITY_MODEL.md`). In order: the URL host and port must match `allowed_hosts` (`blocked` otherwise; `http` and `https` only, no credentials in the URL, methods GET/HEAD/POST/PUT/PATCH/DELETE); the host is **resolved by the host** and every answer must be a public address: loopback, private (10/8, 172.16/12, 192.168/16, fc00::/7), link-local (169.254/16 including cloud metadata, fe80::/10), CGNAT, multicast, unspecified, documentation and reserved ranges, and IPv4-in-IPv6 forms (mapped, NAT64, 6to4) are refused, and one bad answer refuses the host; the connection is then made to the checked IP, never to the name again, so DNS cannot rebind between check and connect; every redirect hop (at most 5) repeats all of this. Request body at most 1 MiB, response at most 1 MiB (`truncated`), 10 s by default and 30 s at most. Redirects are followed (at most 5 hops, each re-checked as above); cookies and proxies from the environment are off; `Set-Cookie` and other response headers outside a short allowlist are not passed back.

**packages.inspect** (SDK 0.21) downloads one package and returns a verdict; it needs the `packages.inspect` permission. It reaches only the hosts `http.fetch` may reach: the URL host must match the manifest's `allowed_hosts` (a `*` entry counts only with the `http.fetch.any` grant, and during the fetch phase of a run only the hosts the owner confirmed for that run count), and so must the host of every redirect hop; a host outside the list is `blocked`, as for `http.fetch`. A package on a GitHub release therefore needs `github.com` and the host GitHub redirects release downloads to, currently `release-assets.githubusercontent.com` (older links used `objects.githubusercontent.com`), in `allowed_hosts`. `url` must be `https` (`invalid_argument` if it is another scheme or empty, or longer than 2,048 bytes). Credentials in the URL, a port other than 443 or a missing host are `blocked`. The host resolves the name itself and refuses non-public addresses and every redirect hop that is not https on port 443 (`blocked`), exactly like `http.fetch`. The host runs at most two inspections at once (`rate_limited` "busy" while both are taken); a call that gets a slot then spends one token of the plugin's `http.fetch` rate limit (`rate_limited`). The whole call is subject to the 15 s host call ceiling (`timeout`). The archive is read up to 64 MiB; a longer body stops the read and is reported as a problem. `signer_key` is the base64 ed25519 public key the package is expected to be signed by; without it the signature is not accepted. The result is never an error for a bad package: an unreachable URL, a non-2xx answer ("download answered 404"), an oversized or corrupt archive, an unsigned package, a signature by another key (the problem names the package's own signer id, also returned as `signer_key_id`) or a changed file all give `signature_ok: false` and one or more strings in `problems` (never null). `kind`, `name`, `version`, `sdk`, `permissions`, `allowed_hosts` and `description` come from the package manifest and are filled only when the archive parsed; `kind` is `plugin`, `theme` or `kit` (a kit is applied with `hatamoto kit apply`, never installed); there is no `title`. `sha256` and `size` describe the archive. The package body, or any part of it, is never returned or logged, and nothing is installed.

**Registry helper (SDK 0.21).** The package `hatamoto.dev/sdk/registry` has the types of a registry index (`Index`, `Entry`, `Revocation`) and `registry.SignIndex(index, privateKey)`, which signs an index JSON document into the same signed envelope the server verifies, byte for byte the same as the server's own signer, plus `registry.KeyID(publicKey)`. It uses the standard library only, so a plugin can import it. `Index.Revoked`, `Revocation` and `Entry.SignerKey` are enforced for the developer index only: a revocation there disables (does not delete) the installed developer package on the next read of that index, which happens about every 6 hours while a developer package is installed. The first-party registry ignores them.

## Visitor and commenter keys (SDK 0.27)

Plugins that hold `client.key` or `comments.read` get pseudonymous keys instead of addresses or emails: `addr_key` (the visitor's address), `net_key` (the visitor's network), `area_key` (the same network key, under the name the comment payloads use) and `commenter_key` (a commenter). Each is

```
HMAC-SHA256(DeriveKey(serverSecret, "plugin-client/<purpose>"), plugin name + ":" + input)
```

encoded as base64url. The purposes are `addr`, `net` and `commenter`. For `addr` and `net` the input is the address after IPv4-mapped addresses are unmapped, and for `net` the /24 (IPv4) or /64 (IPv6) network; the derivation keys differ from the ones core uses for its comment hashes, so a plugin's key never matches core's block list.

- One key space per plugin: two plugins cannot link a visitor.
- Keys are never rotated. Rotating the server secret changes them.
- They are never sent on webhooks.
- They are personal data: your privacy notice should say so.

Until the change that implements them ships, the fields are absent.

## Reserved by the 0.27 contract

These names are fixed now; core does not serve or read them until the change that implements each one ships.

- Routes: `GET|POST /-/comments/signin` (the email form; the link is mailed), `GET|POST /-/comments/signin/{token}` (a GET shows a button that POSTs; the POST sets the device cookie), and `POST /-/comments/signout` (a form protected by a token). The theme layout for the sign-in page is `comment-signin`.
- Comment status `dead`, and the comment column `rank` (a double-precision number, `0` by default).
- Content type settings: `settings.comments.order` is `newest`, `oldest` or `rank`; `settings.comments.policy` is `{ enabled, max_depth, edit_window_minutes, slow_mode_seconds }` with `max_depth` 1 to 32 (default 10), `edit_window_minutes` default 15 and at most 1440, and `slow_mode_seconds` 0 for off.
- The owner-only action `kit.apply`, and the kit package format: see [KITS.md](KITS.md).

## Runs and changesets

Since SDK 0.17 a plugin no longer writes content directly. The owner starts a **run**; the plugin's `on_run_step` export is called by the host, collects data, and **stages** objects into a **changeset**. Nothing is live until the owner reviews and publishes the changeset (see [ADR 0004](adr/0004-plugin-changesets.md)). No host function, flag or environment variable writes live content outside a published changeset, except `entities.create|update|delete` under the warned `entities.write.own` grant, limited to the plugin's own content type.

Runs need **no permission**. They are gated by the run itself: a run function called outside a run is `phase_denied`, and every function is checked against the current phase.

### The `runs` block

```json
"runs": [
  {
    "name": "import",
    "title": "Import from a file",
    "inputs": [ { "name": "source", "label": "Source address", "type": "url", "required": true } ],
    "stages": ["fetch", "transform"],
    "proposes": ["site_settings"]
  }
]
```

| Field | Meaning |
|---|---|
| `name` | `^[a-z][a-z0-9_-]*$` style identifier (the plugin name rule), unique within the plugin |
| `title` | shown to the owner when starting the run; required |
| `inputs` | values the owner enters at start (at most 16): `{ "name", "label", "type"?, "required"?, "options"? }`; `type` is `text` (default), `url`, `number`, `bool` or `select` (with `options`) |
| `stages` | must be exactly `["fetch", "transform"]` |
| `proposes` | proposal kinds the run may submit with `changeset.propose`: `site_settings`, `url_patterns`, `home_page`, `menu_location` |
| `cron` | optional: the name of a job in the manifest's `cron` block. When that job fires, the host starts this run instead of calling `on_cron_<job>`. Needs `standing_approval` |
| `standing_approval` | optional: the rule the owner may approve once so this cron-started run publishes itself; see [Standing approvals](#standing-approvals) |

A plugin declares at most 8 runs. A plugin with a `runs` block must export `on_run_step`; otherwise it is refused at load.

### Standing approvals

Two ways to keep a value current without a review each time.

**Layer 1: the data path (no approval).** Keep the value (a price, a count, a rate) in the plugin's own store (`kv` or `db`), refresh it from `on_cron_<job>`, and show it in the theme with `{{ plugin_<name>_<func> ... }}` (see [Template functions](#template-functions-partials-and-sections)). The output is escaped as text, never markup, and no content is written, so nothing needs approval. Use this first.

**Layer 2: a standing approval.** When the value must live in an entity attribute, a cron-started run may publish itself, but only inside a rule the owner approved:

```json
"cron": [ { "name": "prices", "every": "1h" } ],
"runs": [
  {
    "name": "refresh_prices",
    "title": "Refresh prices",
    "stages": ["fetch", "transform"],
    "cron": "prices",
    "standing_approval": { "fields": ["price"], "schemas": ["product"], "max_per_hour": 100 }
  }
]
```

| Field | Meaning |
|---|---|
| `cron` | names a declared cron job; required with `standing_approval`, and an error without it |
| `standing_approval.fields` | attribute keys the run may change, 1 to 16, each matching the identifier pattern, no duplicates |
| `standing_approval.schemas` | entity schema slugs the rule applies to, 1 to 16, same rules |
| `standing_approval.max_per_hour` | most automatic changes an hour; 0 or absent means 200, and more than 200 is refused |
| `standing_approval.create.schemas` | optional: entity schema slugs whose new entries the run may publish by itself, 1 to 16, each also listed in `standing_approval.schemas` |
| `standing_approval.create.max_per_hour` | most new entries an hour, 1 to 100; there is no default, so it must be set |

A run with a `standing_approval` takes no `inputs`, and a manifest may have only one such run (the host approves one rule per plugin; a second is refused at load). A cron-started run has no owner to confirm hosts, so its fetch phase may call the manifest's `allowed_hosts` (the owner saw them at install), except the `*` wildcard, which a cron-started run never gets. A manifest with a `standing_approval` may list at most 32 `allowed_hosts` besides `*`. The block is additive: older hosts ignore it, and there is no new host function or permission. Cron, hooks and routes still cannot write content except through `entities.write.own`: to change content on a schedule, use a cron-started run with a standing approval.

Nothing happens until the owner approves the rule on the plugin's **Automatic updates** tab (a step-up sign-in is required). Without an active approval the cron tick does not start the run. With one, the run publishes itself only when every item is an update of a listed field on an object the plugin owns (`origin` is `plugin:<name>`), the field is a number, boolean, date or enum in the entity schema, nothing else in the item changed, there is no proposal and the hourly cap holds. Anything else stays staged for review as usual. Never published automatically: HTML, links, slugs, free text, deletes, terms, menus, redirects and media. New entries wait for review too, unless the rule opts in with `create`, described next.

Because the item replaces the whole attributes object, a price-only update must resend `slug`, `name` and `parent` unchanged (and the other attributes as they are); a changed slug, name or parent is outside the rule and the changeset waits for review.

The approval is void when the plugin's version or manifest changes, when a schema the rule names changes, or when the plugin is uninstalled. The owner then sees the plugin as updated, with the rule paused until they review and allow it again. A once a day summary lists what published automatically, and Settings, Changes marks those changesets as published automatically. New entries are listed in their own section of the summary, apart from the updates.

#### Creating content automatically

Add `create` to the rule to let the approved run also publish new entries of the schemas it lists:

```json
"standing_approval": {
  "fields": ["price"], "schemas": ["product"], "max_per_hour": 100,
  "create": { "schemas": ["product"], "max_per_hour": 20 }
}
```

The owner sees one more line on the Automatic updates tab ("May publish new product entries on its own, up to 20 an hour") before approving. New entries go through the same path as a change the owner reviews: validation, HTML sanitising, slug rules, events, revision history and undo. Their origin is `plugin:<name>`, so later updates to them follow the update rule.

The hourly create cap is separate from the update cap: creates do not use the update budget, and updates do not use the create budget. Adding or changing `create` in a plugin update changes the manifest, so an existing approval is void until the owner allows the new rule.

A changeset stays staged for review as a whole, with nothing published, when any item:

- deletes anything, or is a redirect, a menu, a media item or alt text, a term to create, or a proposal;
- creates an entry in a schema that `create.schemas` does not list, or sets a status other than draft or published;
- touches content another source owns;
- sets `translation_of` to an entry the plugin does not own, or to one outside the listed schemas;
- gives a slug that is already taken in the schema;
- carries a link, a path or a file in the entry's name, slug, locale or search text, or in any attribute value or key, at any depth: text with `://`, a scheme such as `http:`, `data:`, `javascript:`, `mailto:` or `tel:` directly followed by a character other than whitespace (`tel:+44...`), a leading `/` or `\`, a leading `www.`, a file name ending in an image, audio, video or PDF extension (`.jpg`, `.png`, `.svg`, `.mp4`, `.pdf` and so on), or CSS `url(` / `image-set(`. In HTML, a link attribute (`href`, `src`, `srcset`, `action`, `formaction`, `cite`, `data`, `poster`, `background`, `xlink:href`, `ping`, `longdesc`, `manifest`, `codebase`, `data-entity-id`) with a non-blank value makes the create wait whatever its target, relative ones included (`<a href="tickets">`, `<a href="#more">`), and any other attribute is checked like plain text. A link written in the middle of plain text counts only when it has `://` or one of the scheme forms above. This holds for every field, whatever the schema says, so a create with a ticket link waits for review. The names (top-level attribute keys only; `cover` is not one, since it can be a cover charge, and a cover image is caught as a link) `hero_image`, `thumbnail`, `headshot`, `logo`, `image`, `photo`, `featured_image` and `og_image` are treated as images and wait whenever they hold anything. Plain prose, numbers, times (`19:30`), labels with whitespace after the colon (`Doors: 7pm`, `Tel: 020 7946 0000`, `About: the venue`) and fractions (`see page 3/4`) are fine, as is HTML without links or link attributes (`<p class="lead">Doors 7pm</p>`);
- is too large to publish on its own: a single string over 64 KiB, more than 4,096 strings (map keys included), or attributes over 256 KiB;
- would take the hour past `create.max_per_hour`.

A changeset that mixes a create with anything outside the rule waits as a whole. A new entry cannot carry terms: an entity item has no terms field, and a changeset with a term item waits for review.

### `on_run_step`

The host calls `on_run_step` with `{ "run_id", "phase", "cursor" }` (`phase` is `fetch` or `transform`; `cursor` is the last `run.checkpoint` of the phase, empty at the start) until the plugin calls `run.finish` in both phases. The output carries nothing (`{}`): progress and completion are reported with `run.*` calls. In Go, register it with `guest.OnRunStep`.

- A step runs under the usual hook timeout (10 s by default, `limits.hook_timeouts_ms["on_run_step"]`) and the sandbox limits below. State that must survive a step goes into `run.checkpoint` (at most 4 KiB) or the buffer, never into the instance.
- A step that fails, times out or trips the circuit breaker fails the run; steps are not retried. `run.fail {reason}` fails it deliberately. A failed run discards its changeset and buffer.
- `run.finish` ends the current phase: fetch moves to transform (the cursor resets), transform finishes the run and leaves the changeset staged for the owner. A step cap bounds a plugin that never finishes.
- **One run per plugin is active at a time.** Starting a second is refused. A run left `running` by a server crash is failed and discarded at the next start.
- The owner starts a run from the admin with the plugin's inputs, the **hosts** it may fetch from (confirmed at start) and, when the run needs one, an uploaded file.

### Phases

The fetch phase collects data from the outside and sees no site data; the transform phase sees the published site but has no network. Inside a run a function that is not listed is denied.

| Functions | fetch | transform |
|---|---|---|
| `permissions`, `log`, `settings.get`, `run.info`, `run.checkpoint`, `run.progress`, `run.finish`, `run.fail` | yes | yes |
| `http.fetch`, `packages.inspect`, `upload.read`, `buffer.put`, `media.import_url` | yes | `phase_denied` |
| `buffer.get`, `buffer.list`, `site.info`, `entities.slug_taken`, `changeset.*` | `phase_denied` | yes |
| `entities.get`, `entities.list`, `entities.translations`, `terms.list`, `terms.get`, `menus.get`, `menus.list`, `media.get`, `media.list`, `redirects.list` (published reads) | `phase_denied` | yes |
| `kv.*`, `db.*`, `mail.send`, `forms.list`, `forms.submissions` | `phase_denied` | `phase_denied` |

Outside a run, the run-only functions (`run.*`, `upload.read`, `buffer.*`, `media.import_url`, `site.info`, `entities.slug_taken`, `changeset.*`) return `phase_denied` ("only available inside a run"); every other function behaves as before.

`http.fetch` inside a run still needs the `http.fetch` grant. Only the host list changes: in the fetch phase the hosts the owner confirmed for the run **replace** `allowed_hosts`. `media.import_url` needs no grant, shares the same guarded client and per-plugin rate limit, and fetches only from the run's hosts.

### Run functions

| Function | Phase | Arguments | Result |
|---|---|---|---|
| `run.info` | both | `{}` | `{ "run_id", "name", "phase", "cursor", "params"?, "hosts"?, "upload_id"? }` (`params` holds the owner's inputs by name) |
| `run.checkpoint` | both | `{ "cursor" }` (at most 4 KiB) | `{}`; passed back on the next step |
| `run.progress` | both | `{ "done", "total"?, "message"? }` | `{}`; shown in the run view |
| `run.finish` | both | `{ "summary"? }` | `{}`; ends the phase |
| `run.fail` | both | `{ "reason" }` | `{}`; fails the run |
| `upload.read` | fetch | `{ "upload_id", "offset", "len" }` (len at most 256 KiB) | `{ "data_b64", "size", "eof" }`; only the run's own upload. The first read of a large upload may return `timeout` while the host fetches it; retry the same read, also in a later step |
| `buffer.put` | fetch | `{ "key", "value_b64" }` | `{}` |
| `buffer.get` | transform | `{ "key" }` | `{ "found", "value_b64"? }` |
| `buffer.list` | transform | `{ "prefix"?, "after"?, "limit"? }` | `{ "keys", "next"? }` |
| `media.import_url` | fetch | `{ "url", "filename"? }` | `{ "handle" }`; a staged file to pass to `changeset.media.attach` |
| `site.info` | transform | `{}` | `{ "title", "tagline"?, "time_zone", "language", "locales"?, "url_patterns"? }`; never secrets |
| `entities.slug_taken` | transform | `{ "schema", "slug", "locale"? }` | `{ "taken" }`; published, public entities only |

The buffer is per run: at most 64 MiB, 10,000 keys, keys of 256 bytes, values of 1 MiB (the request cap). A put over a quota is `invalid_argument`. No function takes a filesystem path.

### Changeset functions

All run in the transform phase and stage an item; each returns `{ "source_key", "created" }` (`created` is true when no earlier run mapped the key). `source_key` is the plugin's own stable key: the same key on a later run updates the object the first run created, so runs are idempotent. Stage parents before children.

| Function | Arguments |
|---|---|
| `changeset.entity.upsert` | `{ "source_key", "schema", "slug"?, "parent_key"?, "translation_of"? (an entity id or the source key of an entity staged earlier or published by an earlier run: at publish the entity joins the source's translation group, so `locale` is required and must differ from the source's; a missing source or a refused link skips the item as a conflict, and a revert puts the previous group back; SDK 0.18), "locale"? (a new entity with none gets the site language, or `und` for a type marked "Shared across languages"), "name", "attributes"?, "seo"? }`; `parent_key` is the source key of a parent staged earlier in the run or mapped by an earlier run; `seo` is `{ "meta_title", "meta_description", "canonical", "robots" }` |
| `changeset.term.upsert` | `{ "source_key", "kind", "slug", "name", "description"? }` |
| `changeset.person.upsert` | `{ "source_key", "slug", "name", "bio"? }`: a byline, never a user account. It is a term of the byline kind; where the site has none, the call is `unavailable` |
| `changeset.media.alt` | `{ "media_id", "alt" }` (SDK 0.18): stages a new alt text for media the owner uploaded; the run must declare `proposes: ["media_alt"]`; media another source owns is `not_owned`, a missing one `not_found`; publish applies it and revert restores the old text |
| `changeset.media.attach` | `{ "source_key", "handle", "alt"?, "caption"?, "credit"? (stored on the media record at publish; SDK 0.18) }`; the handle comes from this run's `media.import_url` |
| `changeset.menu.upsert` | `{ "source_key", "slug", "name", "items"? }`, items `{ "label", "url"?, "entity_key"?, "children"? }`; the plugin's own menus, never a menu location |
| `changeset.redirect.upsert` | `{ "source_key", "from", "to", "status_code"? }`; the target is on the same site, and `from` may not be a live path |
| `changeset.delete` | `{ "kind", "source_key" }`; `kind` is `entity`, `term`, `menu`, `media` or `redirect`; only objects a run created |
| `changeset.propose` | `{ "kind", "payload" }`; `kind` must be in the run's `proposes` |

Proposals are changes to site settings the owner signs off separately, not content: `site_settings` is limited to the title, tagline, time zone and language; the other kinds are `url_patterns`, `home_page` and `menu_location`. A kind the run did not declare is `invalid_argument`.

### Ownership

Every staged object remembers its origin. A run may create and update only objects it (or an earlier run of the same plugin) created, found by `source_key`. A call that would change an object created by the owner or by another source returns `not_owned`; ownership is decided by the host from the `source_key`, a plugin cannot pass an origin. The changeset errors map to the usual codes: an unknown kind, a bad item or an unsupported operation is `invalid_argument`; a missing object is `not_found`; an expired or unstaged changeset is `unavailable`.

**When a person takes an object over.** An entity a run created stays tracked by that plugin until someone edits what the run imported. A write by a person, an API key (REST `PUT` or `PATCH /api/v1/entities/{id}`, a version restore) or MCP (`entities_update`) that changes the body (`content`, `body`, `page_doc`, `page_sections`), the slug, the parent or an SEO field (`meta_title`, `meta_description`, `canonical_url`, `no_index`, `schema_type`, `custom_jsonld`, `jsonld_webpage`, `jsonld_breadcrumb`, `hide_from_sitemap`, `breadcrumb_title`, `primary_term`, the `og_*` fields, `seo.*`) takes the entity over, in the same transaction as the write. The comparison ignores empty values (an absent key, `""`, `false` and an empty object are the same) and surrounding whitespace, line breaks count the same in CRLF and LF, and a write that sends the stored values back, or leaves out `page_doc` (which the core keeps), changes nothing. Other attributes (a featured flag, an excerpt, custom fields), the name, the locale, the status, terms and a run's own publish never take an entity over. From then on a changeset item for that entity gets `not_owned` until the owner hands it back, from the editor's "Who owns this page" card (owner and editor roles). The takeover is recorded in the audit log (`took_ownership` on the update, or an `entity.take_over` row), and for REST and MCP in the server log and the MCP audit as `entities_take_over`.

Two behaviours to know:

- **`entity.before_save` filters do not see changeset content.** Content staged by a run is written when the owner publishes it, by the core, and plugin `entity.before_save` filters are not run on it yet. Do not rely on that hook to adjust imported content.
- The owner's review decides what goes live; a plugin cannot publish.

### Errors added in 0.17

`phase_denied`: the function is not available in the current phase, or is a run function called outside a run. `not_owned`: the object belongs to the owner or another source. Both are in the error-code list under [Host functions](#host-functions).

## Own data

A plugin that holds `kv` or `db` gets its own Postgres schema `plugin_<name>` (a `-` in the name becomes `_`) and its own `LOGIN` role `hm_p_<id8>_<name>` (`<id8>`: the first 8 hex digits of the database's id, so two Hatamoto databases in one cluster never share a role). **The database is the boundary, not the SQL guard.**

- **Own role and own pool.** The role has a random 32-byte password (stored in the core table `hatamoto_plugin_credentials`, which no plugin role can read; rotated whenever the plugin is re-provisioned, i.e. when it loads or its grants change; never logged, and handed to Postgres only as a SCRAM-SHA-256 verifier). Each plugin has its **own small connection pool** (at most 2 connections, to the same host and database as `DATABASE_URL`, authenticating as the plugin role). The *session user* of every plugin SQL statement is therefore the plugin role, which is a member of no other role: `SET ROLE`, `RESET ROLE`, `SET SESSION AUTHORIZATION`, `set_config('role', ...)` or `query_to_xml('...set_config...')` can only land on the plugin role itself. Core code never uses a plugin pool, and plugin SQL never runs on the core pool, so nothing a plugin leaves in its session (role, `search_path`, temp tables, advisory locks) can reach a core query. A connection returns to the plugin's pool after `DISCARD ALL`.
- **Privileges.** The role owns its schema and nothing else. It is `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT`, `CONNECTION LIMIT 4`, with per-role `statement_timeout` (10 s), `idle_in_transaction_session_timeout`, `lock_timeout`, `temp_file_limit` (when the provisioning user may set it) and `search_path = plugin_<name>`. Core tables, sequences and (non-extension) routines in `public` grant nothing to `PUBLIC` (migration `202610161000_plugin_isolation.sql` revokes any such grant the application role owns and makes new objects start closed; objects it does not own and that are open to `PUBLIC` make the host refuse `db.*` until their owner closes them, see docs/deploy/plugin-database.md), so it cannot read `hatamoto_users` or any other core table. It sees names in the catalogs but not other sessions' query text (`pg_stat_activity` shows `<insufficient privilege>`), cannot read server files, run programs or load extensions.
- `kv` is the core table `hatamoto_plugin_kv`, touched only by the host, at most 10 000 keys and 64 MiB of values per plugin (a full store answers `invalid_argument`).
- `db.query` and `db.exec` run in one transaction on the plugin's pool that begins with `SET LOCAL search_path = plugin_<name>` and the call's timeouts. Use unqualified table names; arguments are `$1`, `$2`, ... filled from the JSON `args` array (numbers, strings, booleans, `null`; objects and arrays are sent as JSON text).
- **SQL guard (defence in depth only, NOT the boundary).** The host also sends exactly one statement per call, on the extended protocol, and only if it passes a lexical guard: it must start with `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `WITH` or `VALUES`, and must not use `set_config`, `current_setting`, `session_user`, `copy`, `public`, `information_schema`, `SELECT ... INTO`, any `pg_*` name, `dblink*`, `query_to_xml*`/`table_to_xml*`/`cursor_to_xml*`/`schema_to_xml*`/`database_to_xml*`, `ts_stat`, `ts_rewrite` or the large-object functions. (Words inside string literals and comments are ignored; Unicode escapes and non-ASCII outside literals are refused.) A refused statement returns `blocked` and is never sent. The test suite runs every escape attempt a second time with the guard switched off.
- **Migrations.** `migrations/NNN_name.sql` in the plugin directory, in goose format (only the `-- +goose Up` section runs). They run through goose **on the plugin's own pool, as the plugin role**, with a version table in the plugin's own schema (`plugin_<name>.goose_db_version`), once, in version order, when the plugin loads with `db` granted or when `db` is granted. A migration may `CREATE`, `ALTER` or `DROP` tables, indexes, views, sequences and types and use the DML above; functions, procedures, triggers, extensions, roles, schemas, policies, `GRANT` and temp tables are refused (the whole directory is checked before anything runs; a file that fails the check fails the plugin's load). A failing migration rolls back and is reported as the plugin's `data_error`; the plugin stays loaded.
- Uninstalling a plugin keeps its data unless the owner chooses "delete its data too": then the host drops the schema (as the plugin role), the role and credentials, the `kv` rows and the settings (see [PACKAGES.md](PACKAGES.md#uninstall-and-your-data)).
- There is no disk quota on a plugin's tables yet.

**Needs password authentication for plugin roles.** The plugin pool connects over the same host as `DATABASE_URL`, as a different user, with a password. `pg_hba.conf` (or, on a managed cluster, the provider's role login rules) must allow that, for example (this is what hatamoto.dev uses):

```
host  all  all  127.0.0.1/32  scram-sha-256
```

A deployment that reaches Postgres over a Unix socket with `peer` authentication only must add a `host`/`scram-sha-256` (or `md5`) rule for the plugin roles and point `DATABASE_URL` at that TCP address. If a plugin pool cannot connect, `db.*` and migrations answer `unavailable` with that message (kv is not affected); the host retries on the next call.

**What the database user needs.** Creating `hm_p_<id8>_<name>` takes a privilege (`CREATEROLE`) that the application's own database role should not hold: on a shared PostgreSQL cluster that role may own other databases. The host therefore tries, in order:

1. **The bootstrap functions** (self-hosted installs on a shared or locked-down cluster). Run one command, once, as the PostgreSQL superuser or as a role with `CREATEROLE` and `CREATEDB` that has `CREATE` on the database with the grant option (inherited from the database owner role it is a member of with `INHERIT TRUE`; for example the admin user a managed provider gives you; see the [managed PostgreSQL guide](site/operate/managed-postgresql.md)), in the Hatamoto database (the `<application user>` is the user in `DATABASE_URL`):

   ```sh
   sudo -u postgres psql -X -v ON_ERROR_STOP=1 -v app_role=<application user> \
     -d <hatamoto database> -f deploy/sql/role-admin-bootstrap.sql
   ```

   It is idempotent (rerun it any time) and needs PostgreSQL 16+. It creates the role `hatamoto_role_admin_<id8>` (per database; `NOLOGIN`, `CREATEROLE`, plus `SET` on the `temp_file_limit` parameter; PG16 `CREATEROLE` can manage only roles it created) and, in the schema `hatamoto_admin`, the `SECURITY DEFINER` functions `provision_plugin_role(name, scram_verifier)` and `drop_plugin_role(name)`. They pin `search_path = pg_catalog, pg_temp`, accept only `^[a-z][a-z0-9_]{0,40}$`, derive the role name themselves, record each role they create in `hatamoto_admin.plugin_roles` and refuse to touch, grant, rotate or drop any role not recorded for this database, and are executable by the application role alone (not `PUBLIC`, not the plugin roles). The application role gains no attribute and the cluster's other databases and roles are not affected. The function also grants the new role to the application role `WITH INHERIT FALSE, SET TRUE` (membership runs app -> plugin only), so the app can create the plugin's schema but does not inherit its rights. A role that is not a superuser may be refused `SET` on `temp_file_limit`: the script then skips that grant with a notice, plugin roles run without the cap and the plugin's settings page shows a note. `scripts/ship.sh` runs this in its preflight on the production host.
2. **Direct `CREATE ROLE`**, when the application role itself has `CREATEROLE` or is a superuser. This is the case for dockerised installs where the app user owns the database or is the cluster superuser, and for CI: they need nothing.
3. Otherwise `db.*` and migrations answer `unavailable` (the reason, including this instruction, is shown as `data_error`); `kv` and everything else still work.

## Routes

Declare routes in `plugin.json`; the plugin's `http_handle` export serves them.

- Non-public routes are served at `/api/v1/p/<plugin>/<path>` behind the normal admin authentication and CSRF protection. `roles` lists who may call (`owner`, `editor`, `viewer`); the default is **owner only**. API keys cannot reach plugin routes.
- `"public": true` routes are served at `/p/<plugin>/<path>`: anonymous, rate limited per client address (10 requests/s, burst 30). A public route has no `roles`.
- `path` uses `net/http` patterns (`/leads`, `/leads/{id}`, `/files/{rest...}`); two routes that overlap are refused when the manifest loads. A wrong method on a known path is `405`, an unknown path `404`.
- In static hosting mode `/p/<plugin>/...` answers CORS for the static origin: the preflight allows `GET`, `POST`, `PUT`, `PATCH` and `DELETE` with the headers `Content-Type` and `X-Page-URL`, and never credentials. Declared cookies therefore do not travel from static pages. Plugin markup with root-relative `/p/` links works on a static page only if the plugin builds the URLs from the content of `<meta name="hm-live-origin">`. This is no contract change.
- The two mount points are fixed (`/api/v1/p/{plugin}/{rest...}` and `/p/{plugin}/{rest...}`), so a plugin cannot shadow a core route. Per-plugin routers sit behind an atomic pointer: a plugin's routes appear, change and disappear when it is loaded, reloaded or unloaded, without a restart. (`/p/...` is reserved on the public site.)

The request is JSON on the `http_handle` input and the response JSON on its output:

```json
{ "method": "POST", "path": "/hooks/42", "query": { "a": ["1"] },
  "headers": { "content-type": "application/json" },
  "body": "{...}", "body_b64": "",
  "user": { "id": "uuid", "role": "owner" } }
```

Today `user.role` is one of the six built-in roles, never `viewer` (a route's `roles` may list `viewer`, which no user role matches); a route's `roles` accept `owner`, `editor` or `viewer`, `ui.roles` only `owner` and `editor`, and the plugin UI needs an action the four newer built-in roles (`author`, `contributor`, `reviewer`, `read-only`) do not have, so people with those roles never reach plugin routes or pages. Treat `role` as an open set anyway and do not assume it is one of the first two.
```json
{ "status": 200, "headers": { "Content-Type": "application/json" }, "body": "{...}", "body_b64": "" }
```

`user` is absent on public routes. `pattern` is the manifest route that matched (e.g. `/items/{id}`) and `path_values` its wildcard values; `path` is exactly the path the route was matched and authorised on: a path the router would clean or redirect (`..`, `//`, a missing trailing slash) is `404`, never another route. Request headers passed are `Accept`, `Accept-Language`, `Content-Type`, `User-Agent`, `Referer`, `Origin`, `X-Request-Id` (never `Authorization`, cookies or the client address); plus the names in the manifest's `request_headers` (passed unchanged); on public routes the declared `cookies` arrive in `cookies` (short names) and the response may carry `set_cookies` (same names, limits and flags as `render.page`: namespaced `hmp_<plugin>_<name>`, `HttpOnly`, `SameSite=Lax`, `Secure` on HTTPS; an invalid set is a `502`, a response that sets a cookie is never cached, and with declared `cookies` a public response is never shared-cacheable: `public`/`s-maxage` become `no-store`, the rest `private`, plus `Vary: Cookie`); the body is at most 1 MiB (`413` beyond) and arrives as `body`, or as `body_b64` when it is not UTF-8. Response headers passed are `Content-Type`, `Cache-Control`, `Location`, `Content-Disposition`, `ETag` (no `Set-Cookie`); `status` defaults to 200 and must be 200 to 599. Every plugin response is `Cache-Control: no-store` unless the plugin sets it (on the authenticated `/api/v1/p/` mount `public`/`s-maxage` are replaced by `no-store` and the rest is made `private`), and carries `Content-Security-Policy: sandbox; default-src 'none'; ...` and `X-Content-Type-Options: nosniff`, so HTML a plugin returns runs without scripts in an opaque origin. `Location` may only be a same-site path (`/x`, not `//host` or an absolute URL): anything else is a `502`, so a route cannot become an open redirect on the site's origin. A trap, timeout or error is a `502` (`503` when the plugin is saturated or switched off by the circuit breaker) with `{"error":"plugin error"}`; details go to the host log.

**Visitor keys on routes (SDK 0.27).** The request also carries `commenter_key` (needs `comments.read`), `addr_key` and `net_key` (need `client.key`) on public routes, left out when empty. They are empty on admin routes and in background hooks. See [Visitor and commenter keys](#visitor-and-commenter-keys-sdk-027).

## Cron

`"cron": [ { "name": "digest", "every": "15m" } ]` calls the export `on_cron_digest` on the in-process scheduler (the one that runs core maintenance) every `every`, a Go duration of **at least 1 minute**. The first call is one period after the plugin loads or the server starts; a run that is still going when the next is due delays it instead of overlapping. Input: `{ "name": "digest", "scheduled_at": "<RFC 3339>" }`; output ignored; a failure is logged. With several servers on one database, each cron job runs on one of them per interval. Jobs are added and removed with the plugin, no restart. They do not run when the process runs with `WORKER_DRAIN` or the worker off. Cron cannot write content except through `entities.write.own`. To keep a theme value fresh, store it in the plugin store and show it with `{{ plugin_<name>_<func> ... }}`; to change content on a schedule, use a cron-started run with a standing approval (see [Standing approvals](#standing-approvals)).

## Template functions, partials and sections

Plugin code never runs inside a template. The theme can call:

- **Functions** declared in `funcs`: `{{ plugin_<name>_<func> "arg" 1 }}` (a `-` in the plugin name is `_`). The host calls the export `func_<func>` with the arguments as a JSON array and uses the output as **plain text**: it is escaped by `html/template` like any string, so it can never inject markup. Each call runs for the func's own timeout (250 ms, or `limits.hook_timeouts_ms["func_<name>"]` up to 10 s); output up to 64 KiB. An error, a timeout or an oversized answer renders as an **empty string** (logged), never a failed page, and after 3 consecutive failures of the same func that func (not the plugin's others) is skipped for 30 s; a busy plugin (no free instance slot) and open hook breakers do not count as failures. One call can take at most slot wait (2 s) + cold start (5 s) + the func's timeout (≈7.25 s with defaults, 17 s at the 10 s maximum); neither a slow func that succeeds nor a busy plugin is ever paused, so a page calling funcs N times can take N times that. A page rendered while a func was blank or skipped by that pause (and one where a `render.head`, `render.page` or `jsonld.enrich` filter failed, timed out or was skipped by the circuit breaker) is served `Cache-Control: private, no-store` and is **not stored in the page cache**, so a bad moment is never frozen into cached pages. The signal is process-wide: while a plugin is failing, pages rendered at the same time may also stay uncached.
- **Partials**: `partials/<name>.html` in the plugin directory is available as `{{ template "plugin/<name>/<partial>" . }}` (`-` is `_` in the plugin name).
- **Section packs**: `sections/<type>.html` defines a section type (`[a-z][a-z0-9_]*`); the file must define `"section-<type>"`, like a theme's own sections. A section type the active theme defines wins; a type two plugins both ship is refused for the second.

**Plugin templates may only define names of their own** (partials: `plugin/<name>/...`; sections: `section-<type>` and `plugin/<name>/...`). The host parses each plugin template in a scratch set first. A `{{define "head"}}` (or any other theme template), a syntax error, an unknown function or a section that does not define its own entry rejects **all of that plugin's templates** (logged); the theme is never touched, and a broken plugin template can neither replace a theme template nor stop the theme from compiling. Template text is trusted markup like the theme's own, so install only template packs you would put in a theme, **except for scripts**: a plugin's partials and sections get the same markup check as widget packs (the same code: `<script>`, `<iframe>`, `<object>`, `<embed>`, `<base>`, `<meta>`, `<link>`, `<svg>`, `<math>`, `<template>`, `<noscript>`, `<dialog>`, `<textarea>`, `<title>`, `on*` handler attributes, `srcdoc`, `formaction`, `http-equiv`, `xlink:href`, and `javascript:`, `data:` or `vbscript:` URLs, with the template's actions cut out and in place, plus a sample render of the output), with two exceptions: `<form>` with an `action` that is an http(s) or relative URL (and `method`), and inline `<style>` and `style=""`. The check is about scripts, not network: URLs to other https hosts (`<img src>`, CSS `url()`) are allowed, so a plugin's markup and CSS, `assets/plugin.css` included, can make visitors' browsers request other hosts without any grant (see `docs/SECURITY_MODEL.md`). Beyond that markup check, a **structural rule** applies. Each branch of the template (the body and every `{{if}}`/`{{else}}`/`{{range}}`/`{{with}}` body) is read by the HTML tokenizer of `golang.org/x/net/html`, so tags, quotes, comments and raw-text elements are found the way a browser finds them. A **printing action** (`{{.X}}`, `{{.X | f}}`, `{{print ...}}`) is allowed **only in text content or entirely inside a quoted attribute value** (single or double quotes). It is refused in a tag name, between attributes, in an attribute name, in an unquoted value (`<p class={{.X}}>`), in an end tag, in a comment, in `<!DOCTYPE>`, and anywhere inside `<style>`, `<textarea>`, `<title>`, `<xmp>`, `<iframe>`, `<noembed>`, `<noframes>`, `<noscript>` or `<plaintext>`. `{{if}}`, `{{range}}`, `{{with}}`, `{{break}}`, `{{continue}}` and `{{template}}` calls are allowed **only in text content** (never inside a tag, a quoted value, a comment or `<style>`), and every branch must start and end in text. A branch that ends inside a tag, a comment or a raw-text element is refused. Accepted: `<p class="a {{.X}}" title='{{.Y}}'>`, `{{if .X}}<input checked>{{else}}<input>{{end}}`, `<ul>{{range .Xs}}<li>{{.}}</li>{{end}}</ul>`, `<div>{{template "plugin/me/card" .}}</div>`, `<style>.a{color:red}</style>` (no action inside). Refused: `<scr{{"ipt"}}>`, `<{{.Tag}}>`, `<p {{.Attr}}>`, `<p on{{.X}}="...">`, `<input {{if .X}}checked{{end}}>`, `<p class="{{if .X}}a{{end}}">`, `<div class="{{template "x" .}}">`, `<style>.a{color:{{.X}}}</style>`, `<!-- {{.X}} -->`. **Author guidance:** to switch an attribute on or off, wrap the whole element in text (`{{if .X}}<input checked>{{else}}<input>{{end}}`); to vary an attribute's value, print it with an action or a func inside the quotes (`class="{{.X}}"`, `class="{{pick .X}}"`), not with `{{if}}`; for dynamic CSS, set a `style="..."` attribute value or a class instead of an action inside `<style>`; or ask for `site.scripts`. The rule reads the template source, so it does not depend on what data the template meets. **URL attributes** (exactly `href`, `src`, `action`, `formaction`, `poster`, `cite`, `data`, `background` and `xlink:href`; `data-*` attributes do not count): a template action may not contribute to the URL scheme. Looking at the value up to its first literal `/`, `?` or `#`, it is accepted when that part holds no action, when it is exactly one action at the start (optionally followed by literal text with no `:` and no `&`, such as `.html`), or when literal text before the first action starts with `http:`, `https:`, `mailto:` or `tel:`. So `href="{{.X}}"`, `href="/{{.Lang}}/x"`, `href="mailto:{{.X}}"` and `href="{{.Base}}/x"`, `href="{{.Content.slug}}.html"` and `src="{{.Content.file}}.webp"` pass, while `href="post-{{.X}}"`, `href="java{{.X}}:x"`, `href="{{.X}}:y"` and `href="{{.A}}{{.B}}"` need `site.scripts`. A `{{range}}` in a plugin template may iterate any value: content data, a variable, a number or a function result. Every plugin `range` in a render draws on one budget of 100000 iterations per page render; builder blocks, layout parts and the trial render of sections share the first budget and the page render gets a second; the compile-time sample render gets one budget for the whole sample render, not one per template; theme templates are not counted. When the budget is spent the loop stops, the rest of that loop and of every later plugin loop in the render is left out, the page is served but not cached, and a warning that names the plugin and the template is logged (at most once a minute for each). A template that runs out of the budget in 3 renders within 10 minutes is paused: for the next 10 minutes a paused **section** that starts and ends in text renders nothing (its whole output, not only its loops), while every other template only has its loops stopped and keeps the rest of its output: a **partial** (`plugin/<name>/...`, which a theme may call from an attribute or a script, so its escaping must not change) and a section with actions outside text content (a `site.scripts` template with actions inside `<script>`, or one that opens a `<script>` another template closes). A stopped loop still evaluates its range pipeline. The page is served but not cached, and one warning names the plugin, the template and the time it is tried again. After the pause one render tries it again; if that render stays within the budget the pause ends, otherwise it is paused again at once. The pause is kept in memory per process (a restart clears it), the plugin's page in the admin shows the paused templates under "templates paused" and `GET /api/v1/plugins` lists them in `templates_paused`. Because the budget is shared per render, only the template whose loop takes the last iteration and then asks for one more is counted (a loop that ends exactly at zero is not charged): templates that meet the empty budget later stop their loops as before but are not counted and are never paused for it. Plugin template text is trusted for CPU and memory like theme markup (install only plugins you trust). The range budget bounds loop iterations and nothing else is bounded: the rules that follow are static guards against accidental recursion, fan-out and nesting, not a bound on what a template can cost. They apply across all of the plugin's templates together: `{{template}}` calls to the plugin's own templates may not form a cycle (a template that calls itself, or two that call each other, a partial and a section included), a template may not call another plugin's templates (`plugin/<other>/...`, or another plugin's `section-<type>` or `sections/<type>.html`), no template may expand to more than 1000 template calls (every call counts, whichever `{{if}}` branch it is in, and each call adds what its callee expands to), and ranges that start again from the content (`$.Content...`, or `.Content...` at a template root, also under a `{{with $.Content}}`) may nest at most two deep, counting such ranges in a template called from inside a `{{range}}`. Ranges over the current item's children (`{{range .Content.groups}}{{range .items}}{{range .tags}}`) add no depth. Calls to theme templates count as one call each. The call rules are checked on every load: a plugin that breaks one (a cycle, a call to another plugin, too many calls, ranges nested more than two deep) has **all** its templates rejected, so an installed plugin can lose them after a host upgrade. Every section or partial that is not registered, at registration or at compile, is listed with its reason in `templates_refused` of `GET /api/v1/plugins` and on the plugin's admin page ("Needs attention"), where each reason is a plain sentence. The list is current right after an install, an upgrade or an enable. A preview, a smoke render or a theme check never changes it, and a removed plugin is never listed. For a plugin without `site.scripts`, the output of the sample render is also capped at 256 KiB: a template that goes over it is not registered, and it is listed as an output-limit stop among the templates that are not registered, not as a scripts stop. With `site.scripts` that cap does not apply. Also, the sample render represents the output of the string-building builtins (`printf`, `print`, `println`, `slice`, `html`, `js`, `urlquery`) but caps each result at 4 KiB so it stays cheap, whereas the output of plugin funcs is not represented (it runs only for a plugin without `site.scripts`). A partial or section that carries anything else is **not registered** unless the owner granted the plugin `site.scripts`. A stopped section type renders nothing and a stopped partial renders empty; the plugin's page in the admin says which and why. The grant is read on every render, so a revoke takes effect without reinstalling and a grant brings them back. A grant counts only while the plugin's current manifest still lists `site.scripts`: after an update that drops it, the stored grant is ignored. **With more than one replica**, a revoke or grant made on one replica clears that replica's page cache at once, but the others keep serving their cached pages (and the plugin.js link) until their cache entries expire or they next reload plugins, which is when they re-read the grants; plan for that window after a revoke. A `render.page` block may only use core and theme section types or the plugin's own, never another plugin's. The compiled set is cached; a failed rebuild is not retried until the registry or the theme changes.

Partials, sections and funcs are removed again when the plugin is unloaded or reloaded.

## Search performance provider (internal, not yet a stable plugin surface)

The Google section of Analytics can show impressions, clicks, average position
and index status when something supplies them. That is an in-process Go
interface in `internal/searchperf`, filled through `GrowthDeps.SearchPerf`
(nil by default, which shows a "not connected" notice):

```go
type Provider interface {
	Summary(ctx context.Context, rg Range) (s Summary, ok bool, err error)
	Pages(ctx context.Context, rg Range, paths []string) (map[string]PageStats, error)
}
```

`ok=false` means not connected or no data. Nothing in the SDK, the hooks, the
manifest or the host functions changed: a plugin cannot supply this data yet,
and the interface may change until a plugin surface is designed for it.

## Visitor consent

When the owner turns on the cookie banner (Settings > Privacy & tracking > Cookie consent), scripts a plugin adds to public pages can read the visitor's choice from `window.hm.consent`: `get()` returns `{necessary, analytics, marketing, functional, decided}`, `allows(category)` returns a boolean, and the `hm:consent` event on `document` fires on every change. If `hm.consent` is undefined the banner is off and every category is granted. Do not set cookies for analytics or marketing before `allows` says yes. The full contract is in the Settings reference under "Visitor consent API".

## Sandbox and limits

| Limit | Default | Range | Notes |
|---|---|---|---|
| `limits.memory_pages` | 256 (16 MiB) | 64 to 1024 | cap on each linear memory, in 64 KiB pages. A standard Go guest needs 64 (4 MiB) to start. The host module and the guest each get the cap, so one instance can use up to about 2x `memory_pages` (the guest module plus Extism's kernel module). The host's default allocator also holds the old and the new buffer while a memory grows, so the host heap can briefly reach twice that; `TestMemoryCeilingIsTwiceMaxPages` and `TestConcurrentMemoryBombsBounded` pin both bounds |
| `limits.hook_timeouts_ms` | filters and template funcs 250, everything else 10000 | each value 1 to 10000 | a map of hook to milliseconds, per hook call, always enforced (a cancelled request context also stops the guest). The timeout covers the guest's run only: waiting for a free instance slot and starting a cold instance are bounded separately (below) and never count against it. Keys: a hook name (`render.page`, `entity.published`, ...), `http_handle`, `on_cron_<job>` or `func_<name>`; an unknown key is refused. **A timeout of 0 is refused at load** (a guest without a deadline cannot be stopped), so omit a hook to get its default. The default is 250 ms for filters (they run on the request path) and 10 s for actions, cron jobs and routes |
| `limits.max_instances` | 4 | 1 to 16 | concurrent calls of the plugin and so live instances. Callers beyond it wait for a slot until their own deadline or the slot-wait cap, whichever is first: 2 s for filters and template funcs, `max(2 s, the hook's timeout)` for actions, routes and cron, then fail (`saturated`, fail open) without touching any instance; a cold instance may take at most 5 s to start. Worst case for one call without a caller deadline: slot wait + 5 s + the hook's timeout (a filter with the 250 ms default: 2 s + 5 s + 250 ms; a func with a 8 s timeout: 2 s + 5 s + 8 s; an action: 10 s + 5 s + 10 s), so queueing is always bounded. Calls made with a caller deadline (`render.head`, `jsonld.enrich`, `sitemap.urls`) share that deadline with the slot wait and the cold start; if it cuts the guest short before the hook's own timeout, the instance is discarded, and the circuit breaker is charged unless the guest ran for less time than the call spent waiting for its slot and instance (then the wait, not the guest, ate the budget). A guest that hangs with the time to itself is always charged, whatever timeout it declares. A caller that goes away (the client disconnects and the request is cancelled) is not charged, since it says nothing about the guest, but the instance is still discarded; a caller deadline and the hook's own timeout are charged as described above. A reload or unload waits for in-flight calls, so it can be delayed by the same worst case, and while it waits new calls to that plugin queue behind it. Worst-case memory: `max_instances x memory_pages x 64 KiB x 2` |
| `limits.max_output_bytes` | 1 MiB | up to 4 MiB | largest output of one call; larger output fails the call and discards the instance |

Also fixed:
- **No filesystem**: nothing is mounted (empty `AllowedPaths`); stdout and stderr are discarded (unless the host process itself runs with `EXTISM_ENABLE_WASI_OUTPUT` set, which routes them to the host's own stdout and stderr).
- **No network** except the guarded `http.fetch`: Extism's own `http_request` is never granted.
- **No host functions** beyond Extism's built-ins (`config`, `log`, input/output, vars with a 64 KiB cap) and the one `hm_call` import (see [Host functions](#host-functions)), whose every function is permission checked (run functions need no permission and are checked against the run phase). Logs written with the PDK (`pdk.Log`) reach the host log tagged with the plugin name; each line is cut at 4 KiB and a plugin gets a burst of 20 lines, then 10 per second (the rest is dropped, with one notice).
- **Module size**: `plugin.wasm` up to 64 MiB.
- **Tables**: a module declares at most 1,048,576 table entries in total (Go guests declare about 8,000). Reference types are not enabled, so a module cannot grow a table at run time.
- **Instantiation**: creating an instance, including a start section, must finish within the 5 s start bound, like a cold instance (compiling is not part of that bound).
- **Database messages**: a protocol message above 4 MiB from the database fails the call and replaces the connection; results stay capped at 1000 rows and 1 MiB.
- A Go guest costs about 0.5 ms per hook call, so keep hook payloads small; a first compile takes about a second per Go module, a cached load about 50 ms (`TestCompileCacheWarmLoad` asserts at most 100 ms). A `render.page` round trip on the default slim payload costs about 0.5 to 0.8 ms (p50 480 µs for 4 blocks, 660 µs for 8, 810 µs for 12; `TestRenderPageP50` asserts under 1 ms). With `render_page.full_blocks` it costs about 0.5 ms plus 0.5 ms per KB of blocks: 1.2 ms for 4 short blocks, 4.1 ms for a 7 KB page of 12 blocks (`BenchmarkRenderPage`), so use it only when the plugin must read block content.

Environment: `HATAMOTO_PLUGIN_DIR` (development override only, unset by default), `HATAMOTO_DATA` (the server's state directory, default `./data`; `/app/data` in the Docker image) and `HATAMOTO_PLUGIN_CACHE_DIR` (default `$HATAMOTO_DATA/cache/wasm`; `off` keeps the cache in memory). The compile cache is created with mode 0700, can be deleted at any time (it is rebuilt on the next load) and falls back to memory, with a warning, if the directory cannot be used. It holds native machine code that the server executes: make it writable only by the hatamoto user.

Plugin directories may be symlinks. A Go guest's `init()` runs on the first hook call, not at load, so a guest whose `init()` panics loads but fails every call.

**Settings** (`settings_schema`) drive the admin settings form and are read by the guest with `settings.get` (permission `settings`). Use `config` in the manifest for fixed values.

## Go module extension points

For wrapper modules, not wasm plugins: a Go program with its own `main` that
imports `hatamoto.dev/core/app` and calls `app.Main` (see the `edition`
package). The packages below are public API. Names and behaviour are frozen
until the next major release, like the rest of this document. Register from an
`init` function: `app.Main` freezes every registry, and a later call panics, as
do a nil value, an invalid name and (where only one is allowed) a second call.
Nothing registered means no change. Module code that errors or panics fails
closed. Design records: [ADR 0007](adr/0007-module-extension-points.md), [ADR 0012](adr/0012-module-runtime-hooks.md), [ADR 0016](adr/0016-agent-publishing-policy.md).

**Agent publishing policy.** `agentpolicy.Register(p)` installs one `Policy` whose `Decide(ctx, Request) (Decision, error)` is asked for a write made with an API key (REST and MCP) that publishes, schedules, restores or edits live content, unpublishes or archives it (`ActionUnpublish`), edits a reusable block, theme part or person (`ActionEdit`), and for the REST link insert. Those last three cannot be staged, so `RequireReview` refuses them. An API key cannot delete entities, so delete is not asked. It answers `Allow()`, `RequireReview(reason)` or `Deny(reason)`; the site's own review rule still applies and the stricter answer wins. A change held only by the agent policy has the requester `api-key:<key id>`, so the key's creator may approve it; a change the site's own review rule held keeps the creator as requester. A `Deny`, an error, a panic or a zero `Decision` refuses the write and stages nothing; REST answers 403 with the reason and MCP returns it as the error text. People are not asked. `agentpolicy.Changes(ctx, keyID, since)` lists what a key changed (audit rows, oldest first, at most `agentpolicy.MaxChanges`) and works in serve only. `Request` and `Entity` may gain fields, so build them with keyed literals. Cache slow lookups in the policy: it is called on the request path. See [ADR 0016](adr/0016-agent-publishing-policy.md).

**Edition plugin key.** `edition.RegisterPluginKey(pub ed25519.PublicKey)` sets the one key that plugins declaring `license: "commercial"` must be signed with. Register it from the same `init` as `edition.Register`; it panics after `app.Main` started, on a second call or on a key that is not an ed25519 public key. The key is never added to `HATAMOTO_TRUSTED_KEYS`, and trusted keys never sign a commercial plugin. `PluginEntitled(ctx, plugin)` on the `Gate` is asked for each commercial plugin on every reconcile, so cache the answer. With no key registered, or no entitlement, every commercial plugin is refused. See [Trust and the unsigned opt-in](PACKAGES.md#trust-and-the-unsigned-opt-in) and [ADR 0015](adr/0015-plugin-license.md).

| Package | Use |
|---|---|
| `module/cli` | `Register(Command{Name, Usage, Run})` adds a `hatamoto <name>` command; `Run` gets `*Env{DB, Logger, DataDir, Out, Err}`; `Exit(code, err)` sets the exit code. A core command name always wins: a module command with a core command's name never runs, including a name core adds in a later release, so give module commands a module-specific prefix (`reports-export`, not `export`). |
| `module/migrate` | `Register(module, fsys)` adds goose SQL migrations that run after core's, in `serve` and `hatamoto migrate`, recorded in `module_<name>_goose_db_version` (never `goose_db_version`). Modules migrate in registration (`init`) order; files use timestamp versions, run with out-of-order allowed, and core never runs a down migration (only `-- +goose Up` matters). `Err(module)` is the last run's result. A failed module migration does not stop `serve` (that module's services are not started) but makes `hatamoto migrate` exit non-zero. Name every table, index and function `module_<name>_...` or use your own schema; never alter core objects. `Err` is always nil inside a CLI command. |
| `module/secret` | `Seal`/`Open(module, purpose, aad, ...)` with a key derived under `module/<module>/<purpose>`; `ErrNoSecret` when the process has no server secret. |
| `module/service` | `Register(module, run)` starts `run(ctx)` in `serve` only, after migrations and before the HTTP listener accepts; it runs in every serve process (each replica, and both processes briefly during an update switch), so once-per-install work needs a transaction-scoped advisory lock (`pg_advisory_xact_lock`) or a row claim; `ctx` is cancelled at shutdown and shutdown waits up to 10 s; panics are recovered; no restart. |
| `module/route` | `Register(Mount{Module, Prefix, Handler, MaxBody})` serves `Handler` for every request under a public prefix such as `/example/`, before the site access gate, locale handling and redirect rules. Core applies no authentication (the module checks its callers), its sign-in rate limit (5 requests a second per client address, burst 10) and a body cap (`MaxBody`, default 1 MiB, at most 32 MiB). Methods: GET, HEAD, POST, PUT, PATCH, DELETE. The session cookie reaches the handler and core adds no CSRF protection to module routes: the module must authenticate its own callers and must not trust the core session cookie for state-changing requests. One auth rate limiter is shared by all module mounts. A module whose migrations failed is not mounted. |
| `module/job` | `Register(module, name, every, run)` runs `run(ctx)` about once per `every` (at least one minute) across all servers sharing the database and across restarts, claimed in the table `hatamoto_job_claims`. Id `module:<module>:<name>`; shown on the Jobs screen; not run from the admin, when the worker is off, in drain mode or when `WORKER_DISABLE_JOBS` lists the id. A panic is recovered. A run longer than `every` can overlap the next claim on another server, so make jobs safe to repeat. |
| `module/mail` | `ToOwners(ctx, module, Message)` and `ToUser(ctx, module, userID, Message)` send through core's mail path: logged with kind `module` (body not kept), suppressed when mail is off or in staging mode (the call returns nil). `ErrNoMailer` outside `serve`. |
| `module/db` | `Pool()` returns core's database pool (`*pgxpool.Pool` from `github.com/jackc/pgx/v5`), the one core uses: same role and connection limit, so never `Close` it and keep queries short. It connects with `DATABASE_URL`, which may go through a transaction-mode pooler (`DATABASE_DIRECT_URL` is only for core's session-bound work), so keep no session state on it: no session advisory locks, `SET`, `LISTEN` or temp tables; use `pg_advisory_xact_lock` inside a transaction or a row claim. Because it is published before migrations run, do not query at registration or init time and do not rely on your own tables until your service or route code runs. In `serve` it is available from just after the database opens (before migrations) until shutdown, so services, routes, admin pages, authorizers and hooks can use it; it is cleared after services stop and before the pool closes. In a module command it is the same pool as `Env.DB`. Elsewhere (core's own commands, `init`, tests that set nothing) it returns `ErrNotReady`: fail closed. Call `Pool()` where you use it rather than keeping the result. There is no setter: reach it through a variable your tests can replace. |

**Review policy.** `review.Register(policy)` decides the review stages of a human edit (see [ADR 0011](adr/0011-review-policy.md)). `review.Edit` carries `RequesterRole` (the requester's role; for an API key, its creator's role) and `Bypass`, true when the requester holds the core action `entity.review.bypass` (Owner and Reviewer by default). The default policy stages nothing while the site's review switch is off or `Bypass` is true; a registered policy sees both fields and may ignore `Bypass`, for example to stage an owner's change. `Edit` also has `SlugChange` and `Delete`; core does not set them yet (always false), so a policy must not rely on them in this release. `Edit` may gain fields: build it with keyed literals. `Edit.Locale` is the entry's locale as read before the change (empty on a single-locale site with no locale). `Edit.Section` is the first segment of the entry's public path without the locale prefix, for example `/pricing`; it is empty for the home page, for an entry with no public page, and where core cannot tell (the CLI with `--as`), and core only works it out while a policy is registered. When the URL registry cannot be read the section is unknown: the edit is held (as for a policy error) instead of reaching the policy with an empty section. `review.RegisterGroups(resolver)` installs one `GroupResolver` (init only, single slot; it panics after `app.Main` started, on a second call, on nil and on a typed nil). Its `MayDecide(ctx, userID, role, group, entityID) (bool, error)` is asked for a stage whose group is not `reviewers`; an error or a panic denies. The decide right `entity.review.decide` is still required, and a requester never decides their own change. The resolver owns group membership and must honour `ctx`; it is called for each queue row, each view of a change and each decision, so keep it fast or cache. With a resolver, a user who is not in the stage's group does not see that change in the queue and cannot open or decide it, an owner included, so answer true for the role `owner` if owners should see every stage. Without a resolver such a stage cannot be decided in core (a holder of the decide right can discard it). `review.Pending(ctx)` returns the staged edits waiting at a stage, oldest first, at most `review.MaxPending`, as `PendingStage`: `ChangesetID` (the admin page is `<admin>/review/<ChangesetID>`), `EntityID`, `EntityName`, `Schema`, `Requester` (user id), `Stage`, `Index` (0-based, so "stage Index+1 of Total"), `Total`, `RequestedAt` and `EnteredAt` (UTC; when the change reached `Stage`: the request time for the first stage, else the time of the previous stage's approval). It checks no rights, works in serve only and returns `review.ErrUnavailable` elsewhere. It is meant for a `module/job` reminder that mails through `module/mail`. The admin shows "Stage n of m: Name" for a change with several stages or a renamed stage.

**Reserved prefixes.** A route prefix may not start with a path core serves (the admin, `api`, plugin, media, form, health and MCP paths and the other paths core reserves), may not overlap another module's, and `hatamoto serve` exits at boot for a prefix under the admin address, under or over `PUBLIC_API_PREFIX` or on a scanner probe path. Under `/auth/`, `/auth/<name>/` is allowed except the paths core serves: `/auth/login` and `/auth/register`.

**Trust.** A wrapper module is fully trusted, compiled-in code: it runs with the core database role and the same process environment (including the server secret). The secret key domain and the separate migrations table prevent accidental clashes, not a malicious module. `cli.Env.DB` and `db.Pool()` return a `*pgxpool.Pool` from `github.com/jackc/pgx/v5`, which is part of the contract.

Module names match `^[a-z][a-z0-9_]{0,30}$`; command names `^[a-z][a-z0-9-]{0,31}$`.

## Writing a plugin in Go

Outside this repository (your own module, no `replace`) see [PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md) to write and publish a plugin.

```go
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.EntityPublished, func(ev sdk.EntityEvent) error {
		guest.Log(guest.LogInfo, "published "+ev.Slug)
		return nil
	})
	guest.OnFilter(sdk.JSONLDEnrich, func(d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
		d.Doc["x-hello"] = "hi"
		return d, nil
	})
}

//go:wasmexport on_entity_published
func onEntityPublished() int32 { return guest.Run(sdk.EntityPublished.Export()) }

//go:wasmexport filter_jsonld_enrich
func filterJSONLDEnrich() int32 { return guest.Run(sdk.JSONLDEnrich.Export()) }

func main() {}
```

`//go:wasmexport` must sit on a function in `package main`, so each handled hook needs its one-line export. `guest.Reject("reason")` is the error to return from an `entity.before_save` filter to refuse the save. `guest.Handle(export, input)` runs a handler without the PDK, for unit tests on any platform. The complete example is `examples/plugins/hello`.

Capabilities in Go:

```go
func init() {
	// A route of plugin.json: export http_handle.
	guest.OnHTTP(func(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
		var kv struct{ Found bool `json:"found"` }
		if err := guest.Call(sdk.FnKVGet, map[string]string{"key": "hits"}, &kv); err != nil {
			var he *sdk.HostError // he.Code is "capability_denied" until granted; guest.CapabilityDenied(err) names the permission
			return sdk.HTTPResponse{Status: 503, Body: err.Error()}, nil
		}
		return sdk.HTTPResponse{Body: "ok"}, nil
	})
	guest.OnCron("digest", func(ev sdk.CronEvent) error { return nil })            // on_cron_digest
	guest.OnFunc("badge", func(args []json.RawMessage) (string, error) { return "x", nil }) // func_badge
	guest.OnAction(sdk.FormSubmitted, func(ev sdk.FormSubmittedEvent) error { return nil })
	guest.OnFilter(sdk.RenderPage, func(p sdk.PageContext) (sdk.PageContext, error) {
		if p.Cookies["unlock"] != "1" {
			p.Hide = append(p.Hide, "members-only")
		}
		return p, nil
	})
}

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }
```

## First-party plugins (reference code)

The first-party plugins live in the public `Zen-Labs-Corp/hatamoto-first-party` monorepo, one folder each:

- [`hatamoto-first-party/plugins/searchconsole`](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/searchconsole)
  is a complete, signed plugin that exercises most of the above; it has a README.
- [`hatamoto-first-party/plugins/gtm`](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/gtm) is the smallest real
  one: a `settings_schema`, a `render.head` filter that adds a `<meta>` tag, and an
  `assets/plugin.js` the core links on every public page.

The smaller reference in core is `examples/plugins/hello`. Patterns worth
copying:

- **Logic in plain Go, exports in one file.** Everything but `exports_wasip1.go`
  (the `//go:wasmexport` lines) builds and tests on the host: the host-function
  call is a package variable (`callHost`) that `host_wasip1.go` sets to
  `guest.Call` and tests replace.
- **One document per query.** `SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (...) t`
  returns a single cell the guest decodes into a struct, so the plugin never
  deals with column types.
- **A queue in a table, drained by cron.** Action hooks must be fast and must not
  touch the network: a slow call there holds one of the plugin's instances, and a
  few traps open the circuit breaker (30 s of dropped events). Core webhooks follow
  the same rule: events only insert delivery rows. The cron job claims due rows one at a time atomically
  (`... FOR UPDATE SKIP LOCKED` in a `WITH ... UPDATE ... RETURNING`) and leases
  them for a minute, so two callers never send one delivery twice, sends within a
  time budget (each request's `timeout_ms` is cut to the time left, so the call
  finishes inside `timeout_ms`), and counts a claim whose call died as an attempt
  when its lease expires.
- **Secrets are write-only settings.** `"writeOnly": true` in `settings_schema`:
  the API and admin form never return them (the form renders a password input
  that says a value is stored and never fills it in, not even after a failed
  save); the plugin reads them with `settings.get`. An empty value keeps the
  stored one; the form's "Clear the stored value" box, or `null` in the JSON API,
  removes it.
- **OAuth through plugin routes.** An authenticated route starts the flow (state
  and PKCE verifier in `kv` with a TTL), a *public* route is the redirect target
  and trusts only the one-time state.
- **Verifying a webhook** (receiver side): see [WEBHOOKS.md](WEBHOOKS.md).

## Writing a plugin in another language

The first-party registry builds Go plugins only; the developer index takes any signed `.hmp` (see [PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md#part-2-publish-your-plugin)). A plugin in another language can also be packed and installed on your own server.

Use that language's Extism PDK: export the functions named above (hook exports, `http_handle`, `on_cron_<name>`, `func_<name>`), read the input as JSON, write the result as JSON, and set the error message plus a non-zero return for failures. To call a host function, import `hm_call` from `extism:host/user` (one `i64` memory offset in, one out) and send `{"fn": ..., "args": ...}` as described under [Host functions](#host-functions). The host does not enable the WebAssembly reference-types feature for guest modules. A module with one funcref table, `call_indirect` (including the padded, multi-byte immediates a linker leaves) and bulk-memory instructions loads; a module that uses `ref.*` or `table.*` instructions, declares a second table or a table that is not funcref, or has an element segment for a table other than 0, is refused at load (`TestReferenceTypesAreOff` loads small modules of each shape). Such modules are only emitted when the code or the target features ask for them. If your toolchain emits them, switch the feature off: for Rust, `-C target-feature=-reference-types` (or `-C target-cpu=mvp`). These tests use hand-built modules, not output of a real Rust, TinyGo or AssemblyScript build, so check your own build by installing it on a test server. Go `wasip1` guests are unaffected (their modules use one funcref table and none of these instructions). Mind the limits: the module must be instantiated within `memory_pages` and the 5 s start bound, and must not spin (the call is killed at the hook's timeout).
