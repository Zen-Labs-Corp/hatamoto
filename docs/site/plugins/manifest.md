---
title: The plugin manifest
nav_title: Manifest
description: Every field of the plugin.json file that describes a plugin, with its limits, defaults and the rules checked when it loads.
---

`plugin.json` is the plugin's **manifest**, the file that describes a plugin: its name, its version and what it needs. This page lists every field and every rule the server checks. To write your first one, follow [Build the hello plugin](hello-plugin.md).

The manifest sits at the root of the package next to `plugin.wasm`. The server validates it when the package is installed and again when it loads. The Go type is `sdk.Manifest` in `backend/pkg/sdk/manifest.go`, and its `Validate` method is the authority for everything on this page.

## Overview

A manifest that uses most fields:

```json
{
  "name": "leads",
  "version": "0.2.0",
  "sdk": "^0.11",
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
  "ui": { "title": "Leads", "entry": "index.html", "roles": ["owner", "editor"] },
  "limits": { "memory_pages": 256, "hook_timeouts_ms": { "render.page": 100, "entity.published": 5000 }, "max_instances": 4 },
  "settings_schema": { "type": "object", "properties": {} }
}
```

## Fields

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Matches `^[a-z][a-z0-9-]{1,39}$`. It becomes a URL segment, a schema suffix and a settings key. |
| `version` | yes | The plugin's own semantic version. |
| `sdk` | yes | The SDK range the plugin was built against, such as `^0.11` or `>=0.5 <0.9`. See [The sdk range](#the-sdk-range). |
| `description` | no | Free text shown in the admin. |
| `config` | no | A string map the guest reads with `guest.Config(key)` (`pdk.GetConfig`). Use it for fixed values; use `settings_schema` for values the owner edits. |
| `permissions` | no | Capabilities the plugin asks for. Unknown or duplicated names are refused. |
| `allowed_hosts` | no | Allowlist for `http.fetch`. |
| `routes` | no | HTTP routes, at most 64. |
| `cron` | no | Scheduled calls, at most 16. |
| `cookies` | no | Cookie names `render.page` may read and set, at most 8. |
| `render_page` | no | `{ "full_blocks": true }` sends `render.page` whole blocks (SDK 0.9 and later). |
| `funcs` | no | Template functions, at most 64. |
| `ui` | no | An admin page shipped in `ui/`. |
| `builder_sources` | no | Data sources the plugin offers to the page builder, at most 8 (SDK 0.11 and later). |
| `limits` | no | Resource limits; every field is optional. |
| `settings_schema` | no | JSON Schema (draft 2020-12) for the plugin's settings. |

## The sdk range

The range uses npm rules: `>=x`, `>x`, `<=x`, `<x`, `=x`, a bare version, `^x` and `~x`, and several comparators separated by spaces must all hold. Before 1.0 a caret stops at the next minor, so `^0.8` contains `0.8.x` only.

The server serves SDK `0.14.0` (`sdk.Version` in `backend/pkg/sdk/version.go`) and also plugins built for `0.5.0` to `0.13.0` (`sdk.CompatibleWith`). It refuses a plugin whose range contains neither the current version nor one in that list. So `"sdk": "^0.8"` loads because `0.8.0` is in the list. A new plugin declares `^0.11` until your users run Hatamoto `v0.31.0` (the release that ships SDK 0.12.0), then `^0.12`. The `site.scripts` permission belongs to SDK 0.12.0, but the range you declare does not gate it: hosts from v0.30.0 accept it under any range and enforce it, older hosts reject it as unknown. (SDK releases are plain `vX.Y.Z` tags in the public SDK repository.)

## Permissions

The permission names are the constants in `backend/pkg/sdk/permissions.go`. The list is fixed and an unknown name is a load error. What each permission unlocks is listed on [The host API](host-api.md#permissions).

```text
entities.read  entities.write  terms.read  terms.write
menus.read  menus.write  media.read  media.write
redirects.read  redirects.write  forms.read
settings  log  mail.send  http.fetch  http.fetch.any  kv  db
builder.data  builder.island  site.scripts
```

The manifest only lists what the plugin asks for. The owner decides what it gets, and a plugin always loads even with nothing granted.

## allowed_hosts

The list only matters when `http.fetch` is granted. Entries are an exact host, `*.suffix` (subdomains only, not the apex), `host:port`, or `*`. Without a port only 80 and 443 are allowed, `*` included, so `*:8443` lists a port explicitly. An empty entry is refused.

`*` means any public address. It is valid only when the plugin also lists `http.fetch.any`, and `http.fetch.any` in turn needs `http.fetch`. The pair exists for plugins that call addresses the owner types in (a plugin that pings URLs the owner configures). The permission prompt shows it as a warning, and an update that newly asks for it needs the owner's confirmation and clears the plugin's grants. The address guard still refuses private, loopback and link-local addresses whatever the list says.

## routes

Each route has a `method` (GET, POST, PUT, PATCH or DELETE), a `path` that starts with `/` and uses `net/http` patterns (`/leads`, `/leads/{id}`, `/files/{rest...}`), an optional `public` flag and an optional `roles` list of `owner`, `editor` or `viewer`. `viewer` is accepted for forward compatibility, but no account has that role today, so such a route is reachable only by the other roles you list.

- A non-public route is served at `/api/v1/p/<plugin>/<path>` behind admin authentication and CSRF protection. With no `roles` it is owner only. API keys cannot reach it.
- A `public` route is served at `/p/<plugin>/<path>` for anonymous callers, rate limited per client address (10 requests per second, burst 30). A public route may not list `roles`.
- Two routes that overlap, or a malformed pattern, make the manifest invalid.

How a route is called and answered is on [Data, routes and cron](data-routes-cron.md#routes).

## cron

`{ "name": "digest", "every": "15m" }` calls the export `on_cron_digest`. `name` matches `^[a-z][a-z0-9_]{0,31}$` and must be unique. `every` is a Go duration of at least one minute.

## cookies and funcs

Cookie names match `^[A-Za-z0-9_-]{1,64}$`. Names that start with `hatamoto_`, `__Host-`, `__Secure-` or `hmp_` (compared case-insensitively) are reserved and refused. The browser cookie is stored as `hmp_<plugin>_<name>`.

Each func name matches `^[a-z][a-z0-9_]{0,31}$`, is unique, and is called from a theme as `plugin_<name>_<func>`. See [Admin pages and templates](admin-pages-and-templates.md).

## ui

`title` is 1 to 40 characters and is the sidebar label and the page heading. `entry` is a relative `.html` path under `ui/` (default `index.html`; no `..`, no `//`). `roles` lists who may open the page, `owner` and `editor` only. Empty means owner only. The viewer role is valid on routes but not on the admin page.

## builder_sources

Each entry declares one data source that the page builder can bind to, answered by the `builder.data` filter (see [Hooks and events](hooks-and-events.md#builder-data)). Declaring any source needs the `builder.data` permission in `permissions`.

| Key | Rule |
|---|---|
| `name` | Matches `^[a-z][a-z0-9_-]{0,31}$`, unique in the plugin. In the builder the source is `ext.<plugin>.<name>`. |
| `label` | 1 to 60 characters, shown in the editor. |
| `kind` | `value` (one string for a field) or `items` (a list for a loop). |
| `class` | For `value` sources only: `text` (the default), `url` or `image`. |

## limits

| Field | Default | Allowed | Meaning |
|---|---|---|---|
| `memory_pages` | 256 (16 MiB) | 64 to 1024 | Cap on each wasm linear memory, in 64 KiB pages. A standard Go guest needs 64 to start. |
| `hook_timeouts_ms` | 250 for filters and template funcs, 10000 for everything else | each value 1 to 10000 | Milliseconds per call, keyed by hook name, `http_handle`, `on_cron_<job>` or `func_<name>`. |
| `max_instances` | 4 | 1 to 16 | Concurrent calls and so live instances. |
| `max_output_bytes` | 1 MiB | up to 4 MiB | Largest output of one call. |

A timeout of `0` is refused because a guest without a deadline cannot be stopped. Omit the key to get the default. An unknown key is refused: a hook key must be a built-in hook name, a `on_cron_<job>` key must name a declared job and a `func_<name>` key a declared func. The timeout covers the guest's run only. Waiting for a free instance slot (2 seconds for filters and funcs) and starting a cold instance (at most 5 seconds) are bounded separately.

Worst-case memory is `max_instances x memory_pages x 64 KiB x 2`, because the guest module and Extism's kernel module each get the cap. Lower these numbers for small plugins.

Two limits are not in the manifest. The module file `plugin.wasm` may be at most 64 MiB. A Go guest costs about 0.5 ms per hook call, so keep payloads small and handle only the hooks you need.

## settings_schema

A JSON Schema object backs the admin's settings form and the endpoints `GET` and `PUT /api/v1/plugins/{name}/settings`. The guest reads the stored values with the `settings.get` host function (permission `settings`), with the schema's `default` values merged in. A property with `"writeOnly": true` is never returned by the API or the form, which makes it suitable for secrets. An empty value keeps the stored one. The gtm plugin shows the shape:

```json
"settings_schema": {
  "type": "object",
  "properties": {
    "container_id": {
      "type": "string",
      "title": "Container ID",
      "description": "From tagmanager.google.com, e.g. GTM-ABC1234. Leave empty to turn the tag off.",
      "pattern": "^(|\\s*[Gg][Tt][Mm]-[A-Za-z0-9]{4,12}\\s*)$",
      "maxLength": 32
    }
  }
}
```

## Version compatibility notes

Older plugins keep working, with these changes to be aware of:

- **0.7:** the `locale` field of `HeadContext` and `SitemapURLs` became `language`. Plugins built for `^0.5` or `^0.6` load but see an empty `locale`.
- **0.8:** `sitemap.urls` became additions only; a refused host call answers `capability_denied` (it was `permission_denied`); and `limits.hook_timeouts_ms` replaced `limits.timeout_ms`, which is now ignored.
- **0.9:** `render.page` sends slim blocks. A plugin built for an older range keeps receiving whole blocks, and `render_page.full_blocks` opts a newer plugin in.
- **0.10:** the entity host functions gained `parent_id`, `path` and `url`. Purely additive.
- **0.11:** the `builder.data` filter, the `builder.data` and `builder.island` permissions and the `builder_sources` field. Purely additive.
- **0.12:** the `site.scripts` permission: a plugin's browser JavaScript is served only once it is granted. This is not purely additive: an installed plugin's JavaScript stops being injected until the owner grants `site.scripts`, whatever `sdk` range it declares. Enforcement reached hosts in v0.30.0. Declare the permission and ask owners to grant it. Which range to declare is covered in [The sdk range](#the-sdk-range).
- **0.13:** entity payloads carry `locale` (`EntityRef`, `EntityEvent`, `EntitySummary`, `EntityDraft`). Purely additive.
- **0.14:** `FormSubmittedEvent` carries `attribution` (first and last touch). Purely additive.

## Limits and errors

Every rule above is checked by `Validate`, and a broken rule refuses the whole manifest with a message that names the plugin and the field, for example `sdk: plugin "leads": unknown permission "kv.read"`. `hatamoto plugin pack` runs the same check, so a package it writes always has a valid manifest.
