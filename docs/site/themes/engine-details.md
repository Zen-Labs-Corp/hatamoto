---
title: Engine details
nav_title: Engine details
description: What Hatamoto checks when a theme loads, how rendered pages are cached and cleared, and the known gaps of the built-in theme.
---

This page lists what the theme engine does behind the scenes: the checks a theme must pass to load, how it falls back when a theme breaks, how rendered pages are cached, and the gaps of the built-in theme. For the big picture, read [Understand how themes work](overview.md) first.

## Overview

The public site is built by the server. For each request, the program reads the stored content, builds a view and renders it through the active theme's Go `html/template` files. The result is a complete HTML document. There is no separate front end and no public JSON page endpoint: the public routes are registered at boot next to the JSON API, and rendering is always on.

The theme is found through the **lookup chain**: the active theme package first, then the embedded `default` theme. The first layer that has a file wins. For layouts, the engine tries every candidate name in the highest layer before it moves to the next layer (see [Layout and template hierarchy](layout.md)).

The theme's `version` is a content hash over every file in the chain. It keys the HTML cache, so changing a theme never serves stale markup.

## What the engine checks when a theme loads

A theme that breaks one of these rules is rejected, and the previous good version keeps serving:

| Rule | Error when it fails |
| --- | --- |
| `layouts/base.html` exists somewhere in the chain, and every layout defines `content` | `layouts/<name>.html must define "content"` |
| The templates `head`, `header`, `nav`, `footer`, `section` and `scripts` are defined, normally in `partials/<name>.html` | `theme must define template "<name>" (usually in partials/<name>.html)` |
| Every `sections/<type>.html` defines `section-<type>`, where `<type>` matches `[a-z][a-z0-9_]*` | `sections/<type>.html must define "section-<type>"` |
| Every `schemas/<type>.json` has a matching template | `schemas/<type>.json: schema has no sections/<type>.html template` |
| Schemas stay inside the supported subset, with known `x-hm-field` kinds (see [Sections and schemas](sections.md)) | for example `schema $.linkUrl: invalid x-hm-field "link"` |
| Every file parses as a template | the parser's message, with file and line |

Files in `layouts/` that are not a layout name, such as an old `layouts/<schema>.html`, are not read. The engine logs `render: layout file is not read; rename it` with the file name.

Templates are `html/template`, so values are escaped for their context. Do not build `data-hm-*` attributes by hand; use the marker functions in [Template functions](template-funcs.md).

### The smoke render

A theme that loads is also trial-rendered against fixed test data for every layout kind: page, single, home, archive, taxonomy, author, search, 404 and error. This catches what parsing cannot, such as a partial called with the wrong argument or a missing field.

- Installing a theme, activating it and every `hatamoto theme dev` push run this check first, and refuse a theme that fails. The message starts with `the theme does not render:` and names the view, for example `search view: render search: ...`.
- If a theme that is already active fails the check when the engine rebuilds (for example after a plugin changes), the engine logs `render: theme templates failed validation, falling back to the default theme` and serves the embedded default theme, instead of a 500 on every page. So this safety net should rarely trigger.
- Files that fail to parse always reject the theme.

## Installing and activating in detail

Themes are packages (`.hmp`) stored in Postgres. There is no theme directory and no environment variable that points at one. `HATAMOTO_THEME_DIR` and `HATAMOTO_SITE_THEME_DIR` are no longer read, and the server refuses to boot while either is set. The error message names the replacement: pack the directory with `hatamoto theme pack`, install it with `hatamoto package install`, activate it in the admin, then unset the variable.

- **Install** never changes the site. Trust rules are in [Package format and commands](package-format.md).
- **Activate** in the admin first lists the section types your content uses that the theme does not render, with a count per type; you can still activate. The theme then serves every visitor at once, with no restart. An active theme cannot be uninstalled.
- `hatamoto theme activate <name>` runs the same checks: the theme must exist, render, and cover the section types your content uses. Unlike the admin, it refuses while sections are missing unless you pass `--force`.
- The CLI runs no plugin code, but it knows the template functions, partials and sections of installed, enabled plugins. A plugin function renders empty there, which is enough to parse and smoke-render. Install plugins first and the theme that calls them second.
- A package installed by the CLI, or by another replica, is picked up by running servers on their next check: every second when `ENVIRONMENT` is not `production`, every five seconds in production.

Theme preview is described in [Preview API](preview-api.md).

## The HTML cache

Rendered public pages, archives, feeds and sitemaps are kept in an in-process HTML cache, keyed by theme version and path. It is not used:

- for requests with a query string, except a plain `?page=N` (N of 2 or more) on a page whose builder loop is paged;
- for theme previews and admin previews;
- for pages that failed to render fully, such as a page with a failed section or plugin function;
- while a plugin handles the `render.page` filter, because pages then depend on the request;
- when `PUBLIC_BASE_URL` is unset, because canonical URLs would otherwise follow the request `Host`;
- when the owner switched the cache off under **Settings > Performance**.

`HATAMOTO_PAGE_CACHE_TTL` is a Go duration such as `90s`, `15m` or `6h`. The default is `5m`, and the allowed range is `30s` to `720h` (30 days). A value that does not parse or is out of range is ignored with a warning at boot. A value saved under **Settings > Performance** wins over the variable. [Performance and cache](../admin/performance-cache.md) describes that screen.

Everything that can change public HTML clears the whole cache at once: entity create, update, publish, unpublish and delete (including a scheduled publish by the worker), terms, authors, media, menus, forms, schemas, site settings, design tokens, and theme or plugin install, update, enable, disable and activation. Redirects need no purge, because they are checked on every request before the cached copy is served.

The cache is per process, but page and menu writes (from the admin, the worker, another replica or a CLI command such as `entity publish`) add a row to a change log in the database, and every server clears its own cache within about one poll interval (3 seconds, 1 outside production) of an isolated change, or up to about 15 seconds while writes keep arriving, with a final purge once they stop. Settings, terms, media, forms, schema, theme and plugin changes made on another replica do not: keep the TTL short there, or restart after the change.

Output a plugin puts on a page, including template functions that read `kv` or `db` or fetch over `http.fetch`, is cached with the page for the TTL. A plugin cannot clear the page cache itself, so keep such data tolerant of a delay of one TTL. A page whose plugin function fails, times out or is skipped by its circuit breaker is served but never cached.

## Known gaps

- **`related_articles` is not provided.** There is no such section in the default theme. Use `related` or `call .Related` in a layout instead (see [Template functions](template-funcs.md)).
- **Archive listing is basic.** The default archive layout shows the heading, the description, category chips, a list of cards and pagination. It has no filters, sorting controls or per-item customisation. Build those in your own `archive.html` with `query`.
- **The CLI verifies with the built-in theme.** Commands such as `builder convert --verify-render` do not load the active theme package; repeat the check on the host that serves the site.
