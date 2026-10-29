# Plugin API (SDK 0.13)

Compatibility promises for everything on this page: [STABILITY.md](../STABILITY.md).

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

To get a plugin from your own GitHub repo into the registry, see [PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md).

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

The bridge serves only the first document loaded in the frame, and every request
carries a per-load nonce. If the page navigates the frame elsewhere, the host
stops relaying and blanks the frame (its ticket is spent, so a reload inside the
frame does not load the page again) and the admin shows a "Plugin page disconnected" notice. **Residual risk:** a sandboxed frame can
still navigate itself once, so a plugin UI can leak what it can read (what the
bridge returned to it) through that navigation; the CSP cannot stop it. Install
only plugins you trust with what their page can see.

The contract is the Go module `hatamoto.dev/sdk` (version `0.13.0`; plugins built for `^0.5` to `^0.12` still load):
hook names, payload types and the manifest. Its subpackage `sdk/guest` is the
Go guest layer. Any language with an [Extism PDK](https://extism.org/docs/concepts/pdk)
can write a plugin from this document alone.

```sh
go get hatamoto.dev/sdk@v0.13.0
```

SDK 0.12.0 ships with Hatamoto `v0.31.0`; until your users run it, declare `^0.11`. The `site.scripts` permission belongs to SDK 0.12.0, but the range you declare does not gate it: hosts from v0.30.0 accept it under any range and enforce it, older hosts reject it as unknown.

`hatamoto.dev/sdk` (and `hatamoto.dev/core`) are vanity import paths: hatamoto.dev
answers `?go-get=1` with a `go-import` meta tag pointing at the code repository
(`deploy/caddy/hatamoto.dev.caddy`), so the import path never changes when the
repository moves. `hatamoto.dev/sdk` is the public repository
[Zen-Labs-Corp/hatamoto-sdk](https://github.com/Zen-Labs-Corp/hatamoto-sdk), a root module with
plain release tags: version `0.13.0` is the git tag `v0.13.0`. The core repository stays
the source of truth for the code (`backend/pkg/sdk`); `scripts/publish-sdk.sh` exports that
directory to the SDK repository and tags it `v<Version>` (the `Version` constant in
`version.go`). Send SDK changes to core. Core release tags are plain `vX.Y.Z` too, in a
different repository.

> **0.12 changes:** browser JavaScript needs a grant: the `site.scripts` permission. Without it a plugin's
> `assets/plugin.js` is not linked on public pages and the `script-src` tags of its `render.head` filter are dropped
> (see [Public assets](#packaging)); the same goes for scripts in its `partials/` and `sections/` markup (see [Template functions, partials and sections](#template-functions-partials-and-sections)). Additive for the manifest, **behaviour-changing for installed plugins**: a plugin
> that shipped `assets/plugin.js` before 0.12 stops running its script until its author lists `site.scripts` in
> `plugin.json` and the owner grants it. `plugin.css` is not gated.
>
> **0.13 changes (additive):** entity payloads carry `locale` (the entity's BCP 47 tag, for example `en` or `fr`):
> `EntityRef`, `EntityEvent`, `EntitySummary` and the Entity of `entities.get`/`entities.list` have it,
> `EntityDraft` has it as an optional field (filled on `entity.before_save` from the entity, and empty, so left out of the JSON, on a create that sent no locale; the host never reads it back), and `entities.create` / `entities.update` accept `locale`
> (empty on create means the site language). SDK 0.13 carries it: no rule requires a bump for an additive field, but a published SDK
> tree cannot change under the same version, and 0.10 and 0.11 shipped additive payload changes as a minor too.
> A plugin built earlier ignores unknown JSON fields; on a host older than SDK 0.13 the field is absent.
> On `entity.published` from a scheduled publish the host looks the locale up; if that fails it is `""`.
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

## Calling convention

- The module is a WASI **reactor** (no `_start`; `_initialize` is run once per instance). For Go:
  `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .`
  with the standard toolchain (Go 1.26+; no TinyGo needed). `make plugin-hello` does this for the reference plugin.
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
| `render.page` | filter | `filter_render_page` | [PageContext](#pagecontext) | PageContext |
| `entity.before_save` | filter | `filter_entity_before_save` | [EntityDraft](#entitydraft) | EntityDraft |
| `builder.data` | filter | `filter_builder_data` | [BuilderDataRequest](#builderdata) | BuilderDataRequest |

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

The one deliberate exception is `entity.before_save`: set the error message to
`hatamoto:reject:<reason>` and return non-zero to **refuse the save** (HTTP 422
with `<reason>`). Any other failure there fails open.

### Payload schemas

All fields are always present unless marked optional. IDs are UUID strings.
Timestamps are RFC 3339. Unknown fields in a returned filter value are ignored, and top-level fields a
filter leaves out keep their previous value, so a plugin may return only what it
changed (a field it does return replaces the old one as a whole).

Output is bounded: at most `limits.max_output_bytes` (default 1 MiB, up to 4 MiB;
more fails the call), `render.head` at most 64 tags, `sitemap.urls` at most 5,000
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
  "fields": { "email": "a@example.com" }, "source_path": "/contact", "submitted_at": "2026-01-02T03:04:05Z" }
```

#### PageContext
The value of `render.page`, the request-aware page filter. It runs for each public page view of an entity or listing page (not for 404 and error pages). `cookies` holds only the cookies the plugin declared in `plugin.json` (`cookies`), under the short name you declared; `blocks` are the page's sections *derived from `page_doc`* (pages that have no doc yet use their stored `page_sections`); a page the section model cannot express (builder-only) is not filtered, and a plugin that needs its component data reads `attributes.page_doc` through the host entity API. Blocks are **slim** by default: `id`, `type`, `title`, `hidden` and `fields`, the short scalar values (string up to 64 bytes, number, boolean) of the content keys `variant`, `anchor` and `label`. There is no `content`, no rich text and no nested data, so the call stays under 1 ms for a typical page (4 to 12 blocks). The host keeps the original content and re-attaches it by id. Set `"render_page": { "full_blocks": true }` in `plugin.json` to receive every block with its whole `content` instead (slower: see the cost below; still bounded by the output and decode caps). A plugin built for an SDK range that does not include 0.9 (`^0.8` and older) gets whole blocks automatically and its result is used as returned, as before.
The result may

- return `blocks` to reorder, drop or replace blocks (a block with `"hidden": true` is dropped). **Slim mode:** return a block as received, without `content`, to keep it (the host re-attaches its content; `fields` in a result are ignored); return it with `content` to **replace** that block's content, optionally with another `type` and `title` (an omitted type keeps the original; a block with an unknown id needs a `type`). The host sanitises replaced content when rendering, like any block. **Full mode:** the returned list is used as it is,
- list block ids in `hide` to drop them without rebuilding the list (return only `hide` and `blocks` stays as received),
- add `set_cookies`. Each name must be declared in the manifest. **Cookies are namespaced:** the browser cookie is `hmp_<plugin>_<name>` (the host maps it both ways), so a plugin never receives or sets the session cookie, the CSRF cookie, `__Host-`/`__Secure-` cookies or another plugin's cookies, and the manifest refuses names starting with `hatamoto_`, `__Host-`, `__Secure-` or `hmp_`; the host forces `HttpOnly`, `SameSite=Lax`, `Secure` on HTTPS and `Path=/` unless a path is given, values must be cookie-safe ASCII (at most 2 KiB), `max_age` at most one year.

A result that breaks a rule (undeclared cookie, too many blocks (200) or cookies (8)) is dropped whole: fail open, the page keeps what it had. Plugins run in name order and each sees the previous result. While any plugin handles `render.page`, pages are served `Cache-Control: private, no-store` with `Vary: Cookie` and are not stored in the page cache, because the answer depends on the request. Handle it only when you need to.
```json
{ "path": "/members/guide", "cookies": { "unlock": "1" },
  "entity": { "id": "string", "schema_slug": "page", "slug": "guide", "locale": "en", "name": "Guide" },
  "blocks": [ { "id": "intro", "type": "content", "title": "Intro", "fields": { "variant": "dark" } },
              { "id": "secret", "type": "content", "title": "Members only" } ],
  "hide": ["secret"],
  "set_cookies": [ { "name": "visited", "value": "1", "max_age": 3600 } ] }
```

#### JSONLDDoc
Modify and return `doc` (the schema.org document). Keep `entity` and `base_url` as received.
```json
{ "entity": { "...EntityRef" }, "base_url": "https://example.com", "doc": { "@type": "Article" } }
```

#### SitemapURLs
Additions only. The host sends `group` and `language` (the group slug of the sitemap file, `"misc"` for the misc sitemap; `language` is reserved and always empty) and in `add` the entries that `sitemap.urls` filters before yours already added (empty for the first). It never sends the file's own URL list: a Go guest needs about 0.5 s to decode and encode 5,000 URLs. Append your entries to `add` and return the value; the host appends the ones whose `loc` the file does not already have, so a plugin can neither change nor remove existing entries. Set at most 5,000 entries in `add` in total (more fails the call, fail open). A filter that adds a site-wide URL should target one file (usually `"misc"`). The result is cached with the sitemap XML, so keep it deterministic.
```json
{ "group": "misc", "language": "",
  "add": [ { "loc": "https://example.com/a", "lastmod": "2026-01-01", "changefreq": "weekly", "priority": 0.5,
             "alternates": [ { "rel": "alternate", "hreflang": "fr", "href": "https://example.com/fr/a" } ] } ] }
```
`alternates` is optional.

#### HeadContext
The input describes the page (`language` is the BCP 47 site language) and carries no tags; the result's `tags` are **appended** to the page head. The host builds and escapes the markup, drops attributes outside the allowlist per kind (`meta`: `name`, `property`, `content`; `link`: `rel`, `href`, `type`, `hreflang`, `crossorigin`; `script-src`: `src`, `crossorigin`) and unsafe URL schemes.
Set `replace_site_graph` when your `jsonld` tags carry their own Organization/WebSite graph; if the chain fails or no valid `jsonld` tag results, the generic graph stays.
```json
{ "path": "/blog/x", "language": "en-AU", "schema_slug": "post", "entity_id": "string",
  "tags": [ { "kind": "meta|link|script-src|jsonld", "attrs": { "name": "robots", "content": "noindex" }, "jsonld": {} } ],
  "replace_site_graph": false }
```
`tags`, `attrs` and `jsonld` are optional; `jsonld` is only used for kind `jsonld`.

#### EntityDraft
Return a modified draft to change `name`, `slug` or `attributes` before they are saved; other fields are ignored. `operation` is `create`, `update` or `restore`; `id` is `""` for create.
```json
{ "operation": "create", "id": "", "schema_slug": "post", "name": "string", "slug": "string", "locale": "string", "attributes": {} }
```

#### builder.data
Answers the data-source requests one page render collected for the page builder. Registered only for a plugin that declares `builder_sources` and holds the `builder.data` permission. The input carries only the requests that name the plugin's own sources; return the same object with `results` set, one per request, matched by `id`.
```json
{ "path": "/", "language": "en", "schema_slug": "page", "entity_id": "",
  "requests": [ { "id": "string", "source": "ext.<plugin>.<name>", "limit": 12 } ],
  "results":  [ { "id": "string", "value": "string", "items": [ { "title": "", "url": "", "excerpt": "", "image": "", "category": "", "author": "", "author_url": "", "published_at": "", "attrs": { "k": "v" } } ], "error": "" } ] }
```
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
| `name` | `^[a-z][a-z0-9-]{1,39}$`; equals the directory name |
| `version` | the plugin's own semver |
| `sdk` | SDK range the plugin was built against (`^0.8`, `>=0.5 <0.9`); the host refuses a plugin whose range does not contain its SDK version. This SDK is 0.13.0 and also serves `^0.5` to `^0.12` plugins |
| `description` | optional |
| `config` | string map readable in the guest (`pdk.GetConfig`) |
| `permissions` | capabilities the plugin asks for; see [Permissions](#permissions). Unknown names are refused |
| `allowed_hosts` | allowlist for `http.fetch`: exact hosts, `*.suffix` (subdomains only, not the apex), `host:port`, or `*` (any public host, for plugins that call URLs the owner configures). Without a port only 80 and 443 are allowed, `*` included; `*:8443` lists a port explicitly. A `*` entry needs the separate `http.fetch.any` permission (below) in `permissions`, and works only while the owner has granted it. The address guard still refuses private addresses. It does nothing unless `http.fetch` is granted |
| `routes` | HTTP routes the plugin serves; see [Routes](#routes) (at most 64) |
| `cron` | scheduled calls; see [Cron](#cron) (at most 16) |
| `cookies` | cookie names `render.page` may read and set (at most 8; `[A-Za-z0-9_-]`) |
| `render_page` | optional; `{ "full_blocks": true }` sends `render.page` the whole block content (SDK 0.9+; older plugins always get it) |
| `builder_sources` | data sources for the page builder (at most 8; needs the `builder.data` permission): `[{ "name", "label", "kind": "value"\|"items", "class": "text"\|"url"\|"image" }]` (`class` defaults to `text`; value sources only); see [builder.data](#builderdata) |
| `funcs` | template functions; see [Templates](#template-functions-partials-and-sections) (at most 64; `^[a-z][a-z0-9_]{0,31}$`) |
| `limits` | see below; all optional |
| `settings_schema` | optional JSON Schema (2020-12); backs `GET/PUT /api/v1/plugins/{name}/settings` and `settings.get` |

## Permissions

A plugin always loads. What it may *do* is decided by the owner. Nothing is granted by default.

- `GET /api/v1/plugins` (owner) lists each plugin with `permissions` (requested), `granted`, `routes`, `cron`, `cookies`, `funcs` and `data_error` (why its own data is not ready, if so).
- `POST /api/v1/plugins/{name}/grant` (owner) with `{"permissions": ["kv", "db"]}` sets the granted set. The list **replaces** the earlier grant (grant fewer to revoke) and must be a subset of what `plugin.json` requests (`400` otherwise). The response is the plugin's view. Grants live in Postgres (`hatamoto_plugin_grants`) and apply at once; granting `kv` or `db` also prepares the plugin's data (see [Own data](#own-data)).
- A host function called without its grant returns the typed error `{"code":"capability_denied","capability":"<permission>"}` (plus a `message`); the denied operation never reaches the service behind it, and the plugin keeps running. In Go, `guest.CapabilityDenied(err)` returns the permission name.

| Permission | Host functions |
|---|---|
| `entities.read` | `entities.get`, `entities.list` |
| `entities.write` | `entities.create`, `entities.update`, `entities.set_status`, `entities.delete` |
| `terms.read` / `terms.write` | `terms.list`, `terms.get` / `terms.save`, `terms.delete` |
| `menus.read` / `menus.write` | `menus.list`, `menus.get` / `menus.save`, `menus.add_item`, `menus.delete_item` |
| `media.read` / `media.write` | `media.get`, `media.list` / `media.upload` |
| `redirects.read` / `redirects.write` | `redirects.list` / `redirects.create`, `redirects.delete` |
| `forms.read` | `forms.list`, `forms.submissions`, and delivery of the `form.submitted` action |
| `settings` | `settings.get` (the plugin's own settings) |
| `log` | `log` |
| `mail.send` | `mail.send` |
| `http.fetch` | `http.fetch`, to the hosts in `allowed_hosts` |
| `http.fetch.any` | lets `http.fetch` honour a `*` / `*:port` entry of `allowed_hosts`: **any public web address**. The prompt shows it as a warning, `package.grants` and the install/update audit record it, and an update that newly asks for it (or lists new hosts) needs the owner's confirmation and clears the plugin's grants, like a change of signer. Needs `http.fetch` too |
| `kv` | `kv.get`, `kv.set`, `kv.delete`, `kv.list` |
| `db` | `db.query`, `db.exec`, and the plugin's migrations |
| `site.scripts` | no host function: the plugin's browser JavaScript runs on your public pages. Needed for `assets/plugin.js` to be linked and for `script-src` tags of a `render.head` filter to be emitted. The prompt shows it as a warning: "Runs its JavaScript on every public page, with full access to the site, including signed-in admins. Without it the plugin's script is not loaded." (an update that newly asks for it, and the plugin's page in the admin, repeat that wording). The warning is deliberately broad. The script has the visitor's page privileges and the admin is sealed against it, with the limits listed in `docs/SECURITY_MODEL.md` (Admin isolation); an update that newly asks for it needs the owner's confirmation and clears the grants. `sections/*.html` and `partials/*.html` that contain scripts are also stopped without it. A plugin that ships a script without the grant shows a note in the admin with a link to grant it |
| `builder.island` | no host function: a widget pack whose `widgets/*/island.js` runs JavaScript on your site's pages. Required in `plugin.json` when the package ships an island; shown as a warning in the prompt; an update that newly asks for it needs the owner's confirmation and clears the grants. Without the grant the widgets render but no island is served |
| `builder.data` | the `builder.data` filter: the plugin answers page-builder data requests for the sources it declares in `builder_sources` (no host function; without the grant the filter is not registered) |

## Host functions

There is one import, `hm_call` in wasm module `extism:host/user`. It takes a pointer to a JSON request in Extism memory and returns a pointer to a JSON response. The Go guest wraps it as `guest.Call(fn, args, &result)`; another language declares the import with its PDK's host-function support (`extism:host/user`, one `i64` pointer in, one `i64` pointer out) and frees the response.

```json
{ "fn": "kv.get", "args": { "key": "k" } }
```
```json
{ "ok": true,  "result": { "found": false } }
{ "ok": false, "error": { "code": "capability_denied", "capability": "kv", "message": "kv.get needs the \"kv\" permission, which the owner has not granted" } }
```

Error codes: `capability_denied` (the error also has `capability`: the missing permission), `invalid_argument` (bad or unknown argument, validation failure, a conflict), `not_found`, `rate_limited`, `blocked` (refused by policy: http.fetch allowlist or address guard, SQL guard, database privileges), `reentrant`, `timeout`, `unavailable` (the backing service is not configured, or the plugin's database role is missing), `internal`, `unknown_function`. Arguments are strict: an unknown field is `invalid_argument`. Every call has a deadline (at most 15 s and never longer than the hook call that is running), a request is at most 1 MiB and a result at most 2 MiB (ask for less: `limit`/`cursor`). IDs are UUID strings.

| Function | Arguments | Result |
|---|---|---|
| `permissions` (always allowed) | none | `{ "granted": [..], "requested": [..] }` |
| `entities.get` | `{ "id" }`, `{ "path": "/a/b" }`, or `{ "slug", "parent_id"?, "schema_slug"? }` (`schema_slug` needs `parent_id`; send one selector only) | Entity |
| `entities.list` | `{ "schema_slug"?, "status"?, "search"?, "limit"? (50, max 200), "cursor"? }` | `{ "items": [Entity], "next_cursor" }` |
| `entities.create` | `{ "schema_slug", "name", "slug"?, "parent_id"?, "attributes"? }` | Entity |
| `entities.update` | `{ "id", "name"?, "slug"?, "parent_id"? (`null` = make a root), "attributes"? }` | Entity |
| `entities.set_status` | `{ "id", "status" }` (`draft`, `published`, ...) | Entity |
| `entities.delete` | `{ "id" }` | `{}` |
| `terms.list` | `{ "kind"?, "search"?, "limit"? }` | `{ "items", "total" }` |
| `terms.get` | `{ "kind", "slug" }` | Term |
| `terms.save` | `{ "kind", "slug"?, "name" }` (updates when kind+slug exists, else creates) | Term |
| `terms.delete` | `{ "kind", "slug" }` | `{}` |
| `menus.list` / `menus.get` | `{}` / `{ "slug" }` | menus / menu with items |
| `menus.save` | `{ "slug"?, "name", "location"? }` | Menu |
| `menus.add_item` | `{ "menu_slug", "label", "link_type", "url"?, "entity_id"?, "parent_id"?, "open_in_new_tab"? }` | MenuItem |
| `menus.delete_item` | `{ "id" }` | `{}` |
| `media.get` / `media.list` | `{ "id" }` / `{ "search"?, "folder"?, "mime_type"?, "limit"?, "offset"? }` | media (id, filename, mime_type, file_size, public_url, width, height, alt_text) |
| `media.upload` | `{ "filename", "mime_type", "data_b64" }` (the 1 MiB request cap applies: about 750 KB of file) | media. The stored type is **sniffed from the bytes** (`mime_type` is advisory) and must be an allowed type (images except SVG, PDF, MP4/WebM) whose extension matches the filename, as for uploads through the admin; anything else is `invalid_argument` |
| `redirects.list` | `{ "search"?, "page"?, "per_page"? }` | `{ "items", "total" }` |
| `redirects.create` | `{ "source_path", "target_url", "status_code"? (301) }` | Redirect |
| `redirects.delete` | `{ "id" }` | `{}` |
| `forms.list` | `{}` | `{ "items": [ { id, name, slug, status, fields } ] }` |
| `forms.submissions` | `{ "form_slug", "limit"?, "offset"? }` | `{ "items": [ { id, status, data, created_at } ], "total" }`. IP address and user agent are never included |
| `settings.get` | `{}` | the plugin's own settings object, schema defaults merged in (write-only values included: they are the plugin's own secrets) |
| `log` | `{ "level": "debug|info|warn|error", "message" }` | `{}`. At most 10 lines/s (burst 20), then `rate_limited`; messages are cut at 4 KiB |
| `mail.send` | `{ "to": [1..10 plain addresses], "subject", "text"?, "html"? }` | `{ "sent": n }`. Goes through the site's mail service and From identity; 6 recipients per minute (burst 10: one token per recipient), then `rate_limited`; `unavailable` when mail is not configured |
| `http.fetch` | `{ "method"?, "url", "headers"?, "body"? or "body_b64"?, "timeout_ms"? }` | `{ "status", "headers", "body" or "body_b64", "truncated"? }`. At most 600 requests per plugin per minute (`HATAMOTO_PLUGIN_FETCH_PER_MINUTE`, burst 60), then `rate_limited`. `truncated` means the body was cut at 1 MiB: treat it as an error, ask for less (the Search Console plugin sends `prettyPrint=false` and pages of 1,000 rows) |
| `kv.get` | `{ "key" }` | `{ "found": bool, "value"? }` |
| `kv.set` | `{ "key", "value" (any JSON, at most 256 KiB), "ttl_seconds"? }` | `{}` |
| `kv.delete` / `kv.list` | `{ "key" }` / `{ "prefix"?, "limit"? (100, max 1000) }` | `{}` / `{ "keys": [..] }` |
| `db.query` | `{ "sql", "args"? }` | `{ "columns", "rows": [[..]], "truncated"? }` (at most 1,000 rows and 1 MiB) |
| `db.exec` | `{ "sql", "args"? }` | `{ "rows_affected" }` |

Menu items: `link_type` is `entity`, `custom`, `external`, `archive` or `none`. A `none` item has no link and is shown as plain text, for example a heading above a group of links (existing `#` items were migrated to it). Every item carries an `attrs` object, `{}` when empty, with `style`, `color`, `badge` and `icon` (ids of presets the active theme declares; an id the theme does not know is kept and not shown), `show_on` (`desktop` or `mobile`; empty means all devices), `id`, `title`, `aria_label` and `rel` (a list of `nofollow`, `sponsored`, `ugc`, `me`). `css_classes` is at most 200 characters; it must not contain control characters, quotes, backticks or angle brackets.

**`render.head` runs once per render.** A page served from the cache does not run it at all, so keep the filter free of side effects (no `kv` counters or outbound pings).

**Cached pages and plugin data.** What a plugin renders into a public page (template funcs, `render.head` and `render.page` output) is cached with the page for `HATAMOTO_PAGE_CACHE_TTL` (default 5 minutes, up to 24 hours), including values a func reads with `kv.get` or `db.query`. A plugin cannot purge the page cache (there is no `cache.invalidate` host function): new `kv`/`db` data reaches cached pages when the TTL expires or when anything else clears the cache (an entity write, even by `entities.update`, a settings save, a menu or theme change). See [Page cache](THEMES.md#page-cache).

**`page_sections` is derived.** `entities.get`/`entities.list` and the `entity.before_save` input carry `attributes.page_sections` computed from `attributes.page_doc` at read time (the same list the public JSON shows), and the key is absent for a builder-only page. The stored key is a frozen legacy mirror that is never read here. A write that sends back the derived `page_sections` unchanged is accepted as a no-op; one that changes it on a page that has a `page_doc` is refused (`page_sections_removed`), and a `before_save` edit to it is discarded with a warning: change `page_doc` instead.

**Entity writes** go through the same service as the admin API: schema validation, HTML sanitising, slug rules, `entity.before_save`, and events all apply (there is no way around them). **Re-entrancy guard:** a plugin write sets a depth of 1 on the context of everything it triggers. A plugin that writes from inside a hook (`entity.before_save`, `entity.created`, ...) of an editor's save is allowed one level; a write made while already inside the hooks of a plugin write fails with `reentrant`, so plugins cannot recurse. Reads are never guarded.

**Entity trees (SDK 0.10).** An Entity is `{ id, schema_slug, slug, locale, name, status, version, parent_id?, path?, url?, attributes, published_at?, updated_at }`. `parent_id` is the parent entity (absent for a root); `path` is the canonical public path the site serves it at (`/services/seo/local`, from the schema's URL pattern and the parent chain); `url` is `path` behind the public origin when `PUBLIC_BASE_URL` (or the owner's site address) is set, otherwise the path alone. `entities.create` and `entities.update` take `parent_id` and apply the rules of the admin and REST API (the entity service, not a copy): the parent must exist, the move may not make the entity its own ancestor, and the chain may not be deeper than a URL can express (8 levels); a refused parent is `invalid_argument`. On update, omit `parent_id` to keep the parent, send `null` (or `""`) to make the entity a root. Slugs are unique per schema and locale, not among all siblings (a page and a post can share a slug at the root), so `entities.get` with `{ "slug", "parent_id", "schema_slug" }` can match one row per locale. It returns the site-language row when there is one (`"parent_id": null` means a root row), otherwise the one other-locale row, and it finds drafts; when no site-language row exists and two other-locale rows match, it is `invalid_argument` (no row is guessed). There is no `locale` selector: reach another locale's row by `id` (from `entities.list` or the entity payloads), never by slug. Without `schema_slug`, rows of several schemas sharing the slug give `invalid_argument` instead of a guess, so always send it when updating what you find; `schema_slug` without `parent_id`, or mixing `id`/`path`/`slug`, is `invalid_argument`; a bare `{ "slug" }` returns the site-language row first, then a root row before a nested one, then the oldest. `{ "path": "/a/b" }` resolves a URL with the router the public site uses, so it finds exactly what a visitor would get (published entities only; a trailing slash is ignored; redirects are not followed). Any other path is `not_found`.

**http.fetch** is the only network a plugin has. In order: the URL host and port must match `allowed_hosts` (`blocked` otherwise; `http` and `https` only, no credentials in the URL, methods GET/HEAD/POST/PUT/PATCH/DELETE); the host is **resolved by the host** and every answer must be a public address: loopback, private (10/8, 172.16/12, 192.168/16, fc00::/7), link-local (169.254/16 including cloud metadata, fe80::/10), CGNAT, multicast, unspecified, documentation and reserved ranges, and IPv4-in-IPv6 forms (mapped, NAT64, 6to4) are refused, and one bad answer refuses the host; the connection is then made to the checked IP, never to the name again, so DNS cannot rebind between check and connect; every redirect hop (at most 5) repeats all of this. Request body at most 1 MiB, response at most 1 MiB (`truncated`), 10 s by default and 30 s at most. Redirects are followed (at most 5 hops, each re-checked as above); cookies and proxies from the environment are off; `Set-Cookie` and other response headers outside a short allowlist are not passed back.

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

**Needs password authentication for plugin roles.** The plugin pool connects over the same host as `DATABASE_URL`, as a different user, with a password. `pg_hba.conf` must allow that, for example (this is what hatamoto.dev uses):

```
host  all  all  127.0.0.1/32  scram-sha-256
```

A deployment that reaches Postgres over a Unix socket with `peer` authentication only must add a `host`/`scram-sha-256` (or `md5`) rule for the plugin roles and point `DATABASE_URL` at that TCP address. If a plugin pool cannot connect, `db.*` and migrations answer `unavailable` with that message (kv is not affected); the host retries on the next call.

**What the database user needs.** Creating `hm_p_<id8>_<name>` takes a privilege (`CREATEROLE`) that the application's own database role should not hold: on a shared PostgreSQL cluster that role may own other databases. The host therefore tries, in order:

1. **The bootstrap functions** (self-hosted installs on a shared or locked-down cluster). Run one command, once, as the PostgreSQL superuser, in the Hatamoto database (the `<application user>` is the user in `DATABASE_URL`):

   ```sh
   sudo -u postgres psql -X -v ON_ERROR_STOP=1 -v app_role=<application user> \
     -d <hatamoto database> -f deploy/sql/role-admin-bootstrap.sql
   ```

   It is idempotent (rerun it any time) and needs PostgreSQL 16+. It creates the role `hatamoto_role_admin_<id8>` (per database; `NOLOGIN`, `CREATEROLE`, plus `SET` on the `temp_file_limit` parameter; PG16 `CREATEROLE` can manage only roles it created) and, in the schema `hatamoto_admin`, the `SECURITY DEFINER` functions `provision_plugin_role(name, scram_verifier)` and `drop_plugin_role(name)`. They pin `search_path = pg_catalog, pg_temp`, accept only `^[a-z][a-z0-9_]{0,40}$`, derive the role name themselves, record each role they create in `hatamoto_admin.plugin_roles` and refuse to touch, grant, rotate or drop any role not recorded for this database, and are executable by the application role alone (not `PUBLIC`, not the plugin roles). The application role gains no attribute and the cluster's other databases and roles are not affected. The function also grants the new role to the application role `WITH INHERIT FALSE, SET TRUE` (membership runs app -> plugin only), so the app can create the plugin's schema but does not inherit its rights. `scripts/ship.sh` runs this in its preflight on the production host.
2. **Direct `CREATE ROLE`**, when the application role itself has `CREATEROLE` or is a superuser. This is the case for dockerised installs where the app user owns the database or is the cluster superuser, and for CI: they need nothing.
3. Otherwise `db.*` and migrations answer `unavailable` (the reason, including this instruction, is shown as `data_error`); `kv` and everything else still work.

## Routes

Declare routes in `plugin.json`; the plugin's `http_handle` export serves them.

- Non-public routes are served at `/api/v1/p/<plugin>/<path>` behind the normal admin authentication and CSRF protection. `roles` lists who may call (`owner`, `editor`, `viewer`); the default is **owner only**. API keys cannot reach plugin routes.
- `"public": true` routes are served at `/p/<plugin>/<path>`: anonymous, rate limited per client address (10 requests/s, burst 30). A public route has no `roles`.
- `path` uses `net/http` patterns (`/leads`, `/leads/{id}`, `/files/{rest...}`); two routes that overlap are refused when the manifest loads. A wrong method on a known path is `405`, an unknown path `404`.
- The two mount points are fixed (`/api/v1/p/{plugin}/{rest...}` and `/p/{plugin}/{rest...}`), so a plugin cannot shadow a core route. Per-plugin routers sit behind an atomic pointer: a plugin's routes appear, change and disappear when it is loaded, reloaded or unloaded, without a restart. (`/p/...` is reserved on the public site.)

The request is JSON on the `http_handle` input and the response JSON on its output:

```json
{ "method": "POST", "path": "/hooks/42", "query": { "a": ["1"] },
  "headers": { "content-type": "application/json" },
  "body": "{...}", "body_b64": "",
  "user": { "id": "uuid", "role": "owner" } }
```
```json
{ "status": 200, "headers": { "Content-Type": "application/json" }, "body": "{...}", "body_b64": "" }
```

`user` is absent on public routes. `pattern` is the manifest route that matched (e.g. `/items/{id}`) and `path_values` its wildcard values; `path` is exactly the path the route was matched and authorised on: a path the router would clean or redirect (`..`, `//`, a missing trailing slash) is `404`, never another route. Request headers passed are `Accept`, `Accept-Language`, `Content-Type`, `User-Agent`, `Referer`, `Origin`, `X-Request-Id` (never `Authorization`, cookies or the client address); the body is at most 1 MiB (`413` beyond) and arrives as `body`, or as `body_b64` when it is not UTF-8. Response headers passed are `Content-Type`, `Cache-Control`, `Location`, `Content-Disposition`, `ETag` (no `Set-Cookie`); `status` defaults to 200 and must be 200 to 599. Every plugin response is `Cache-Control: no-store` unless the plugin sets it (on the authenticated `/api/v1/p/` mount `public`/`s-maxage` are replaced by `no-store` and the rest is made `private`), and carries `Content-Security-Policy: sandbox; default-src 'none'; ...` and `X-Content-Type-Options: nosniff`, so HTML a plugin returns runs without scripts in an opaque origin. `Location` may only be a same-site path (`/x`, not `//host` or an absolute URL): anything else is a `502`, so a route cannot become an open redirect on the site's origin. A trap, timeout or error is a `502` (`503` when the plugin is saturated or switched off by the circuit breaker) with `{"error":"plugin error"}`; details go to the host log.

## Cron

`"cron": [ { "name": "digest", "every": "15m" } ]` calls the export `on_cron_digest` on the in-process scheduler (the one that runs core maintenance) every `every`, a Go duration of **at least 1 minute**. The first call is one period after the plugin loads or the server starts; a run that is still going when the next is due delays it instead of overlapping. Input: `{ "name": "digest", "scheduled_at": "<RFC 3339>" }`; output ignored; a failure is logged. Jobs are added and removed with the plugin, no restart. They do not run when the process runs with `WORKER_DRAIN` or the worker off.

## Template functions, partials and sections

Plugin code never runs inside a template. The theme can call:

- **Functions** declared in `funcs`: `{{ plugin_<name>_<func> "arg" 1 }}` (a `-` in the plugin name is `_`). The host calls the export `func_<func>` with the arguments as a JSON array and uses the output as **plain text**: it is escaped by `html/template` like any string, so it can never inject markup. Each call runs for the func's own timeout (250 ms, or `limits.hook_timeouts_ms["func_<name>"]` up to 10 s); output up to 64 KiB. An error, a timeout or an oversized answer renders as an **empty string** (logged), never a failed page, and after 3 consecutive failures of the same func that func (not the plugin's others) is skipped for 30 s; a busy plugin (no free instance slot) and open hook breakers do not count as failures. One call can take at most slot wait (2 s) + cold start (5 s) + the func's timeout (≈7.25 s with defaults, 17 s at the 10 s maximum); neither a slow func that succeeds nor a busy plugin is ever paused, so a page calling funcs N times can take N times that. A page rendered while a func was blank or skipped by that pause (and one where a `render.head`, `render.page` or `jsonld.enrich` filter failed, timed out or was skipped by the circuit breaker) is served `Cache-Control: private, no-store` and is **not stored in the page cache**, so a bad moment is never frozen into cached pages. The signal is process-wide: while a plugin is failing, pages rendered at the same time may also stay uncached.
- **Partials**: `partials/<name>.html` in the plugin directory is available as `{{ template "plugin/<name>/<partial>" . }}` (`-` is `_` in the plugin name).
- **Section packs**: `sections/<type>.html` defines a section type (`[a-z][a-z0-9_]*`); the file must define `"section-<type>"`, like a theme's own sections. A section type the active theme defines wins; a type two plugins both ship is refused for the second.

**Plugin templates may only define names of their own** (partials: `plugin/<name>/...`; sections: `section-<type>` and `plugin/<name>/...`). The host parses each plugin template in a scratch set first. A `{{define "head"}}` (or any other theme template), a syntax error, an unknown function or a section that does not define its own entry rejects **all of that plugin's templates** (logged); the theme is never touched, and a broken plugin template can neither replace a theme template nor stop the theme from compiling. Template text is trusted markup like the theme's own, so install only template packs you would put in a theme, **except for scripts**: a plugin's partials and sections get the same markup check as widget packs (the same code: `<script>`, `<iframe>`, `<object>`, `<embed>`, `<base>`, `<meta>`, `<link>`, `<svg>`, `<math>`, `<template>`, `<noscript>`, `<dialog>`, `<textarea>`, `<title>`, `on*` handler attributes, `srcdoc`, `formaction`, `http-equiv`, `xlink:href`, and `javascript:`, `data:` or `vbscript:` URLs, with the template's actions cut out and in place, plus a sample render of the output), with two exceptions: `<form>` with an `action` that is an http(s) or relative URL (and `method`), and inline `<style>` and `style=""`. Beyond that markup check, a **structural rule** applies. Each branch of the template (the body and every `{{if}}`/`{{else}}`/`{{range}}`/`{{with}}` body) is read by the HTML tokenizer of `golang.org/x/net/html`, so tags, quotes, comments and raw-text elements are found the way a browser finds them. A **printing action** (`{{.X}}`, `{{.X | f}}`, `{{print ...}}`) is allowed **only in text content or entirely inside a quoted attribute value** (single or double quotes). It is refused in a tag name, between attributes, in an attribute name, in an unquoted value (`<p class={{.X}}>`), in an end tag, in a comment, in `<!DOCTYPE>`, and anywhere inside `<style>`, `<textarea>`, `<title>`, `<xmp>`, `<iframe>`, `<noembed>`, `<noframes>`, `<noscript>` or `<plaintext>`. `{{if}}`, `{{range}}`, `{{with}}`, `{{break}}`, `{{continue}}` and `{{template}}` calls are allowed **only in text content** (never inside a tag, a quoted value, a comment or `<style>`), and every branch must start and end in text. A branch that ends inside a tag, a comment or a raw-text element is refused. Accepted: `<p class="a {{.X}}" title='{{.Y}}'>`, `{{if .X}}<input checked>{{else}}<input>{{end}}`, `<ul>{{range .Xs}}<li>{{.}}</li>{{end}}</ul>`, `<div>{{template "plugin/me/card" .}}</div>`, `<style>.a{color:red}</style>` (no action inside). Refused: `<scr{{"ipt"}}>`, `<{{.Tag}}>`, `<p {{.Attr}}>`, `<p on{{.X}}="...">`, `<input {{if .X}}checked{{end}}>`, `<p class="{{if .X}}a{{end}}">`, `<div class="{{template "x" .}}">`, `<style>.a{color:{{.X}}}</style>`, `<!-- {{.X}} -->`. **Author guidance:** to switch an attribute on or off, wrap the whole element in text (`{{if .X}}<input checked>{{else}}<input>{{end}}`); to vary an attribute's value, print it with an action or a func inside the quotes (`class="{{.X}}"`, `class="{{pick .X}}"`), not with `{{if}}`; for dynamic CSS, set a `style="..."` attribute value or a class instead of an action inside `<style>`; or ask for `site.scripts`. The rule reads the template source, so it does not depend on what data the template meets. A partial or section that carries anything else is **not registered** unless the owner granted the plugin `site.scripts`. A stopped section type renders nothing and a stopped partial renders empty; the plugin's page in the admin says which and why. The grant is read on every render, so a revoke takes effect without reinstalling and a grant brings them back. A grant counts only while the plugin's current manifest still lists `site.scripts`: after an update that drops it, the stored grant is ignored. **With more than one replica**, a revoke or grant made on one replica clears that replica's page cache at once, but the others keep serving their cached pages (and the plugin.js link) until their cache entries expire or they next reload plugins, which is when they re-read the grants; plan for that window after a revoke. A `render.page` block may only use core and theme section types or the plugin's own, never another plugin's. The compiled set is cached; a failed rebuild is not retried until the registry or the theme changes.

Partials, sections and funcs are removed again when the plugin is unloaded or reloaded.

## Sandbox and limits

| Limit | Default | Range | Notes |
|---|---|---|---|
| `limits.memory_pages` | 256 (16 MiB) | 64 to 1024 | cap on each linear memory, in 64 KiB pages. A standard Go guest needs 64 (4 MiB) to start. The host module and the guest each get the cap, so one instance can use up to about 2x `memory_pages` (the guest module plus Extism's kernel module). The host's default allocator also holds the old and the new buffer while a memory grows, so the host heap can briefly reach twice that; `TestMemoryCeilingIsTwiceMaxPages` and `TestConcurrentMemoryBombsBounded` pin both bounds |
| `limits.hook_timeouts_ms` | filters and template funcs 250, everything else 10000 | each value 1 to 10000 | a map of hook to milliseconds, per hook call, always enforced (a cancelled request context also stops the guest). The timeout covers the guest's run only: waiting for a free instance slot and starting a cold instance are bounded separately (below) and never count against it. Keys: a hook name (`render.page`, `entity.published`, ...), `http_handle`, `on_cron_<job>` or `func_<name>`; an unknown key is refused. **A timeout of 0 is refused at load** (a guest without a deadline cannot be stopped), so omit a hook to get its default. The default is 250 ms for filters (they run on the request path) and 10 s for actions, cron jobs and routes |
| `limits.max_instances` | 4 | 1 to 16 | concurrent calls of the plugin and so live instances. Callers beyond it wait for a slot until their own deadline or the slot-wait cap, whichever is first: 2 s for filters and template funcs, `max(2 s, the hook's timeout)` for actions, routes and cron, then fail (`saturated`, fail open) without touching any instance; a cold instance may take at most 5 s to start. Worst case for one call without a caller deadline: slot wait + 5 s + the hook's timeout (a filter with the 250 ms default: 2 s + 5 s + 250 ms; a func with a 8 s timeout: 2 s + 5 s + 8 s; an action: 10 s + 5 s + 10 s), so queueing is always bounded. Calls made with a caller deadline (`render.head`, `jsonld.enrich`, `sitemap.urls`) share that deadline with the slot wait and the cold start; if it cuts the guest short before the hook's own timeout, the instance is discarded, and the circuit breaker is charged unless the guest ran for less time than the call spent waiting for its slot and instance (then the wait, not the guest, ate the budget). A guest that hangs with the time to itself is always charged, whatever timeout it declares. A reload or unload waits for in-flight calls, so it can be delayed by the same worst case, and while it waits new calls to that plugin queue behind it. Worst-case memory: `max_instances x memory_pages x 64 KiB x 2` |
| `limits.max_output_bytes` | 1 MiB | up to 4 MiB | largest output of one call; larger output fails the call and discards the instance |

Also fixed:
- **No filesystem**: nothing is mounted (empty `AllowedPaths`); stdout and stderr are discarded (unless the host process itself runs with `EXTISM_ENABLE_WASI_OUTPUT` set, which routes them to the host's own stdout and stderr).
- **No network** except the guarded `http.fetch`: Extism's own `http_request` is never granted.
- **No host functions** beyond Extism's built-ins (`config`, `log`, input/output, vars with a 64 KiB cap) and the one `hm_call` import (see [Host functions](#host-functions)), whose every function is permission checked. Logs written with the PDK (`pdk.Log`) reach the host log tagged with the plugin name; each line is cut at 4 KiB and a plugin gets a burst of 20 lines, then 10 per second (the rest is dropped, with one notice).
- **Module size**: `plugin.wasm` up to 64 MiB.
- A Go guest costs about 0.5 ms per hook call, so keep hook payloads small; a first compile takes about a second per Go module, a cached load about 50 ms (`TestCompileCacheWarmLoad` asserts at most 100 ms). A `render.page` round trip on the default slim payload costs about 0.5 to 0.8 ms (p50 480 µs for 4 blocks, 660 µs for 8, 810 µs for 12; `TestRenderPageP50` asserts under 1 ms). With `render_page.full_blocks` it costs about 0.5 ms plus 0.5 ms per KB of blocks: 1.2 ms for 4 short blocks, 4.1 ms for a 7 KB page of 12 blocks (`BenchmarkRenderPage`), so use it only when the plugin must read block content.

Environment: `HATAMOTO_PLUGIN_DIR` (development override only, unset by default), `HATAMOTO_DATA` (the server's state directory, default `./data`; `/app/data` in the Docker image) and `HATAMOTO_PLUGIN_CACHE_DIR` (default `$HATAMOTO_DATA/cache/wasm`; `off` keeps the cache in memory). The compile cache is created with mode 0700, can be deleted at any time (it is rebuilt on the next load) and falls back to memory, with a warning, if the directory cannot be used. It holds native machine code that the server executes: make it writable only by the hatamoto user.

Plugin directories may be symlinks. A Go guest's `init()` runs on the first hook call, not at load, so a guest whose `init()` panics loads but fails every call.

**Settings** (`settings_schema`) drive the admin settings form and are read by the guest with `settings.get` (permission `settings`). Use `config` in the manifest for fixed values.

## Writing a plugin in Go

Outside this repository (your own module, no `replace`) see [PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md).

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

The registry builds Go plugins only (see [PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md)); a plugin in another language can still be packed and installed on your own server.

Use that language's Extism PDK: export the functions named above (hook exports, `http_handle`, `on_cron_<name>`, `func_<name>`), read the input as JSON, write the result as JSON, and set the error message plus a non-zero return for failures. To call a host function, import `hm_call` from `extism:host/user` (one `i64` memory offset in, one out) and send `{"fn": ..., "args": ...}` as described under [Host functions](#host-functions). Mind the limits: the module must start within `memory_pages`, and must not spin (the call is killed at the hook's timeout).
