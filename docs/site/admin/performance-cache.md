---
title: Performance and the page cache
nav_title: Performance and cache
description: How your site keeps pages ready for fast visits, the options under Settings and Performance, and what makes a change show at once.
---

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

Owners control the page cache under **Settings > Performance** (`<admin>/settings/performance`). The **page cache** keeps a copy of each public page in the server's memory, so a repeat visit is answered in a few milliseconds instead of being built again, which typically takes tens to a hundred. This page lists what is cached, every option on that screen, what clears the cache and what to expect with plugins or more than one server. Edits you make in the admin clear the cache at once, as described in [Edit and publish content](content.md).

## What is cached

Rendered public pages, archives, feeds and sitemaps are kept in an in-process HTML cache, keyed by theme version and path. The cache is never used for:

- requests that carry a query string (a theme can branch on `?days=7` without any cache key);
- previews;
- pages that failed to render completely;
- any site where `PUBLIC_BASE_URL` is not set. **Without `PUBLIC_BASE_URL` nothing is cached and preload is refused.**

The cache lives in the server process. It starts empty after a restart or deploy.

## The lifetime from the environment

`HATAMOTO_PAGE_CACHE_TTL` sets the default lifetime as a Go duration such as `90s`, `15m` or `6h`. The built-in default is `5m`. The allowed range is `30s` to `720h` (30 days). A value that does not parse or is out of range is ignored: the default is used and a warning is logged at boot.

```bash
HATAMOTO_PAGE_CACHE_TTL=1h hatamoto serve
```

A lifetime saved in the admin (below) wins over this variable.

## Settings, Performance

The page has two cards, Page cache and Preload. Each setting is a row with its help text on the left and the control on the right. Changes apply live, without a restart.

### Page cache

- **Page cache** on or off. Off bypasses every read and write, clears the stored pages and sitemaps once and pauses preload. The other controls are disabled but keep their values. Turning it on again starts cold.
- **Keep pages for**: from 30 seconds to 30 days, with presets (5 minutes, 15, 30 minutes, 1, 2, 6, 12, 24, 48 hours, 5 days, 30 days). The page tells you whether the value is "set here" or the server default (environment or the built-in 5 minutes).
- **Max cache size** in MB, from 1 to 102400. Empty means no limit. Over the limit the oldest pages are evicted first, and a single page larger than the limit is not stored. The limit is applied at boot and on save; lowering it evicts on the next cache write rather than at once.
- **Cached right now** shows the page count and approximate size, with a **Clear cache** button. Every page is rebuilt on its next visit. Clearing never starts a preload.

### Preload

Preload builds pages before visitors ask for them.

- **Preload** on or off, for scheduled runs and runs after a content change.
- **Pages to build**: **Only missing** (default) skips pages that are already cached and unexpired. **Refresh all** re-renders every page and overwrites the cached copies, which also refreshes plugin data.
- **After content changes** (on by default): when an edit, menu or settings change empties the cache, a preload starts about 2 minutes after the last change, and never later than 10 minutes after the first change of a burst. Clear cache and switching the cache off do not trigger it.
- **Preload every**: from 15 minutes to 30 days. The page preselects the shortest interval that is not below the page lifetime. The first scheduled run comes one interval after you save.
- **Preload now** renders the sitemap URLs under `PUBLIC_BASE_URL` in the background, two at a time and one run at a time. A run stops early if the size limit starts evicting pages. Preload goes through the same public page handler a visitor reaches, so a preloaded page is byte for byte what a visit would store, and it writes no analytics rows or 404 log entries.

The last preload result (time, pages built, pages already cached, errors, duration) is kept in memory only. Failed pages are named, for example "Failed: /a (500), /b (panic)", up to 10 and then "and N more".

Saves, clears and manual preloads are written to the admin audit log as `cache.settings`, `cache.clear` and `cache.preload`. With `HATAMOTO_WORKER=off` there is no scheduler, so only **Preload now** works.

## What clears the cache

A long lifetime would make a missed purge visible for hours, so everything that can change public HTML clears the whole cache and the sitemaps immediately:

- entities: create, update, publish, unpublish and delete from the admin, API keys, MCP and plugins, plus a scheduled publish by the worker and imports;
- terms, authors, media (alt text, caption, delete), menus, forms, entity-type address patterns, site settings, design tokens and presets, layout parts, setup and starter content;
- themes and plugins: install, update, uninstall, enable, disable, activating a theme, theme settings and plugin settings;
- internal linking: inserting a suggested link and the link rewrite after a slug or parent change.

A page that is being rendered at the moment of a purge is served to its visitor but not stored, so a purge is never undone by a render that read old data.

**Redirects need no purge.** They are checked on every request before the cache, so a new redirect wins at once and removing one brings the page back.

## When the cache does not see a change

- **Several instances.** Each instance has its own cache, and a purge reaches only the instance that handled the edit. The others show the old page until their lifetime ends. The same goes for CLI commands that write content (for example `entity publish`), which run in their own process. Keep the lifetime short on such setups (the 5 minute default), or restart or clear the other instances after a change.
- **Plugin output.** Output a plugin puts on a page (template functions, `render.page` and `render.head` filters, including data a function reads from storage or fetches over HTTP) is cached with the page. It changes only when the lifetime ends or another purge happens. A plugin cannot clear the cache itself. Design plugin data to tolerate a delay of one lifetime, or save an entity or the plugin's settings to force a purge. A page whose plugin function fails or times out is served but not cached.
- **Browsers and CDNs.** Public pages carry a `Cache-Control` header (60 seconds in the browser and 300 seconds for shared caches by default). A visitor can see an old look for that long after a design change even though the server cache is already clear.

## Choosing a lifetime

A longer lifetime serves more visits from memory and matters most on low-traffic pages that would otherwise expire between visits. Because edits made through this admin clear the cache at once, a one-instance site with mostly static content can safely use hours or days. If you run replicas, plugins that show live data or write content from scripts, keep the default or shorter, and rely on **Preload** to keep pages warm.

Operators can read how the cache fits into the request path in [Request flow](../architecture/request-flow.md), and the related settings in the [configuration reference](../operate/configuration-reference.md).
