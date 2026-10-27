---
title: URL patterns
nav_title: URL patterns
description: Every rule that decides a page's address: the pattern tokens, nested and dated addresses, content type settings, defaults, precedence and redirects.
---

This page lists how Hatamoto decides the public address of every entity: the pattern syntax, nested and dated addresses, the settings of a content type, the defaults, how a request is resolved, and the base URL in front of it all. To change the address of a content type in the admin, follow [Set the addresses of your pages](set-page-addresses.md).

## Overview

Where an entity lives on the public site is data, not code. Every content type (entity schema) can carry a `settings` object that says at which URL its entities are served, which sitemap group lists them, and which other type slugs count as the same content. With empty settings, the built-in defaults apply.

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

The same pattern drives canonical URLs, breadcrumbs, [JSON-LD](json-ld.md), the [sitemaps](sitemaps.md), the renderer's path resolution and the "view on site" links in the admin. An owner edits it at **Settings > Entity types** (`<admin>/settings/types`), in the **Addresses and search** card of a type, or through the schema API.

```json
{
  "url_pattern": "/guides/{slug}",
  "archive_path": "/guides",
  "sitemap_group": "guides"
}
```

## The public base URL

Canonical URLs, sitemap entries, JSON-LD `url` and `@id` values, `llms.txt` links and the `Sitemap:` line in `robots.txt` all start with the same public base URL. Hatamoto picks it in this order:

1. The environment variable `PUBLIC_BASE_URL`, when set (a trailing `/` is removed).
2. The public address the owner saved in the admin (setup wizard or site settings).
3. The scheme and host of the incoming request.

Set `PUBLIC_BASE_URL` explicitly in production. Behind a reverse proxy the request host can be an internal address, and the third fallback would then leak it into every canonical URL and sitemap. Sitemaps and rendered pages are cached only when it is set. See [Reverse proxy and TLS](../operate/reverse-proxy-tls.md) for the proxy side.

```bash
PUBLIC_BASE_URL=https://example.com
```

## Pattern syntax

A pattern is an absolute path made of literal segments and tokens.

| Token | Meaning |
|---|---|
| `{slug}` | The entity slug. Must be the last segment, unless `{path}` is used instead. |
| `{path}` | The whole parent chain, root first, ending with the entity's own slug, joined by `/` (for example `services/seo/local`). It takes 1 to 8 URL segments, replaces `{slug}` as the last segment, and cannot be combined with `{parent}`. |
| `{parent}` | The slug of the entity's parent only. Optional: the segment is dropped when there is no parent. |
| `{year}`, `{month}`, `{day}` | The publication date, zero padded: `2026`, `05`, `09`. See [Dates](#dates). |

The rules are enforced when you save:

- The pattern starts with `/` and ends with `{slug}` or `{path}`, exactly one of the two.
- Each token fills a whole segment, and each token appears at most once. Partial tokens such as `/a-{slug}` are refused.
- Values never contain `/`, except a `{path}` value, whose segments are not empty and at most 8.
- The first literal segment is the **section prefix**: `docs` in `/docs/{slug}`.
- There is no language segment. One install is one site in one language, set by the site setting `language`.

Examples: `/{slug}`, `/blog/{slug}`, `/people/{parent}/{slug}`, `/{path}`, `/news/{year}/{month}/{day}/{slug}`.

The admin field **Address pattern** shows a live example as you type, built with the slug `example-page`, the parent `section` and today's date in the site time zone, such as `Example: /guides/example-page`. An empty field means the built-in address.

## Nested pages with {path}

Set `{"url_pattern": "/{path}"}` on a page-like type to serve the content tree at its natural URLs. A page `local` whose parent is `seo`, whose parent is `services`, lives at `/services/seo/local`. A root page `about` still lives at `/about`. The built-in `page` type keeps `/{slug}`, so existing sites keep their URLs; you opt in on a type of your own.

A documentation type with a hierarchical pattern:

```json
{
  "url_pattern": "/docs/{path}",
  "archive_path": "/docs",
  "sitemap_group": "docs",
  "schema_org_type": "WebPage"
}
```

With it, a `getting-started` page under `/docs` is `/docs/getting-started`, and its child `install` is `/docs/getting-started/install`.

How nested paths behave:

- **Slug uniqueness follows the pattern.** A type whose pattern, or any override pattern, has `{path}` or `{parent}` needs slugs to be unique only among siblings. `/services/seo/local` and `/services/pt/local` can both use the slug `local`, while two root rows of the type still need different slugs. Every other pattern keeps one slug per type, because two rows with one slug would hide each other at one URL. All of this is per locale: the same slug can exist once in each locale. The database enforces it, and the API answers `409 slug_exists`. A pattern change that would make slugs collide is refused with `400`.
- **Strict resolution.** Only the exact chain serves: `/seo/local` and `/services/local` are `404`. Ancestors can be of any type and status, so a draft parent still gives its children their URL. The page itself must be published.
- **Parents** are set with `parent_id` on create or update. A parent must exist and cannot be the entity or one of its descendants, and nesting is capped at 8 levels. An entity with children cannot be deleted (`409 has_children`).

## Moves keep old links alive

A **301 redirect** is a permanent forward from an old address to a new one. Hatamoto writes them in two cases.

**An entity moves.** When an update changes the slug or parent of a published entity, every URL that changed, for the entity and all its published descendants, gets a `301` from the old path to the new one. The rule is stored in the redirect table with `source_tag = entity-move`. Earlier rules that pointed at an old path are repointed so that there are no chains, and internal `data-entity-id` links in stored content are rewritten to the new URL.

**A content type's pattern changes.** When a save changes a type's `url_pattern` or `overrides`, every published entity of the type that now lives at another path gets a `301` from its old path. This only adds rules: a path that already has a redirect row (of any owner, active or not) is skipped, nothing is deleted or repointed, and a move that would loop back through an owner's rule is skipped. An automatic redirect that sends the new path away is switched off first, because the page lives there now. A failure is logged; the save itself has already succeeded.

**A live page beats an automatic redirect.** Redirects are checked first. When you create, publish or import an entity at a path where an `entity-move` redirect points elsewhere (you rename `/about` to `/about-us` and later publish a new `/about`), that redirect is switched off and the page answers. Redirects an owner wrote keep their precedence, so delete them if a page should take the path. Import stores what its source says and creates no move redirects, and changing `published_at` on a dated URL creates none either.

The redirects screen is described in [Media, menus and redirects](../admin/media-menus-redirects.md). The full rules for redirects, wildcards included, are in [Redirect rules](../migrate-contribute/redirect-rules.md).

## Dates

`{year}`, `{month}` and `{day}` come from `published_at`, or from `created_at` while a draft has none, so a preview has a URL. They are read in the site time zone (the site setting `timezone`, an IANA name such as `Australia/Sydney`) and in UTC when it is empty or unknown. A request only serves when its date equals the entity's date, so `/2026/05/hello-world` is `404` for a post published in June. Changing the time zone or an entity's `published_at` changes its dated URL.

## Schema settings

`entity_schemas.settings` is a JSONB object, `{}` by default, exposed as `settings` on the schema API. Every key is optional and unknown keys are ignored. The admin label is the field in the **Addresses and search** card.

| Key | Admin label | Type | Meaning |
|---|---|---|---|
| `url_pattern` | Address pattern | string | Pattern for this type's entities. |
| `archive_path` | Archive address | string | The section index URL, such as `/blog`. Listed first in the sitemap group and used as the breadcrumb section. Must start with `/`. |
| `archive_title` | Archive title | string | Heading of the archive page, up to 200 characters. |
| `archive_description` | Archive description | string | Meta description of the archive page, up to 500 characters. |
| `archive_crumb_label` | Breadcrumb label | string | The section label in an entity's breadcrumb. Falls back to `archive_title`, then the type name. Up to 200 characters. |
| `sitemap_group` | Sitemap group | string | Child sitemap name. Default: the first literal segment of the pattern, else `misc`. |
| `aliases` | Other slugs served by this pattern | string[] | Extra type slugs that resolve to this type, for archives, route lookup and linking. Comma separated in the admin. |
| `overrides` | none | object[] | `{attribute, equals, url_pattern, sitemap_group}`: entities whose `attributes[attribute]` equals `equals` use another pattern and group. |
| `sitemap` | Sitemap priority, Sitemap change frequency, Leave this type out of the sitemap | object | `{priority, changefreq, exclude}`. `exclude` drops the type from the sitemap. |
| `section_pages` | Entities named like the archive address become section pages | bool | On a page-like type: a row whose slug equals the section prefix of another type (a `docs` page next to `/docs/{slug}`) is served at `/{slug}` and becomes that section's index in the sitemap. |
| `schema_org_type` | Structured data type (JSON-LD) | string | JSON-LD `@type` for types without annotations or a plugin builder. See [JSON-LD](json-ld.md#typing-a-content-type-with-schema-org-type). |
| `editor` | Editor | string | `""` (**Form**) for the generic form, or `"post"` (**Post editor**) to edit entities in the post editor, with autosave, preview and the SEO panel. The built-in post family always uses it, and the person type cannot. |

Settings are checked on write. A bad stored value is logged and the type is routed with the defaults, so one bad row cannot take the public site down.

An override sends a variant to its own pattern. This type serves entities whose `format` attribute is `report` at `/reports/{slug}`:

```json
{"url_pattern": "/docs/{slug}", "overrides": [{"attribute": "format", "equals": "report", "url_pattern": "/reports/{slug}", "sitemap_group": "misc"}]}
```

## Built-in types

The built-in `page` and `post` types cannot be changed at all, in the admin or through the API: their settings, and so their addresses, stay at the defaults below. The admin shows them read-only with the note "This is a built-in entity type. It can be viewed, but not changed or deleted." The built-in person type is the exception: you can add fields and change its addresses and archive settings, but not remove its own fields or delete it. To serve content at another address, create a type of your own.

## Defaults

With empty settings:

| Content type | Pattern | Notes |
|---|---|---|
| `page` | `/{slug}` | Sitemap group `misc`. |
| post family: `post`, `blog`, `article`, `blog_post` | `/blog/{slug}` | Archive `/blog`, sitemap group `blog`. |
| any other type | `/{slug}` | |

For the post family, the first member that exists and that no explicit type claims owns the route, and the other unclaimed members are its aliases. A family slug is claimed when an explicit type has that slug or lists it in `aliases`. So a type `blog` at `/journal/{slug}` without a `post` alias leaves `post` on `/blog/{slug}`.

Only new installs get `/blog/{slug}`. An install that already served posts at `/post/{slug}` when it upgraded had that pattern pinned, so its URLs did not change.

## Precedence

URL shape is per type, not per entity. When a path matches several patterns, the one with more literal segments wins, then the one with fewer tokens. Explicit settings take precedence over a plugin URL builder for the same type.

## How a request is resolved

The renderer resolves every public path in the same order:

1. The redirect table is checked: exact rules first, then wildcard rules.
2. The router tries its candidate patterns in precedence order. Each looks up a published base row by type and slug, or, for `{path}`, by the parent chain. Date tokens must match the entity's date.
3. The page is rendered with the canonical URL taken from the match. If nothing serves the path, the answer is `404`.

An archive (`archive_path`) lists the rows of its type and of its aliases.

## Paged builder loops

A builder query loop with paging set to `pages` owns `?page=N` of the page it sits on, on any page that is not itself a listing. Archives, terms, authors and search page their own items, so use `from: archive` there. Only the first paged loop of a page reads `?page=N`, and later ones always show page 1. Page 1 has no parameter. The head of page N is self-canonical (`?page=N` for N of 2 or more) with `rel=prev` and `rel=next`, and there is no extra `noindex`, like archive listings. `?page=N` past the last page is a quiet 404. A request whose query is exactly `page=N` (a plain decimal from 2 to 1000) is cached under its own key only when the page really has a paged loop and N is in range. Every other query string, and every page without a paged loop, stays uncached. Any entity write purges these entries with the rest of the page cache. See [Builder reference](../admin/builder-reference.md).

## Limits and errors

| Error | When |
|---|---|
| Pattern refused on save | It does not start with `/`, does not end with `{slug}` or `{path}`, repeats a token, uses a partial token, or combines `{path}` with `{parent}`. The admin shows the reason under **Address pattern**. |
| `400` on a pattern change | Slugs of existing entities would collide under the new pattern. |
| `409 slug_exists` | Another entity of the type already has the slug in the same locale (among its siblings, for `{path}` and `{parent}` patterns). |
| `409 has_children` | You tried to delete an entity that has children. |
| Built-in type refused | `page` and `post` cannot be changed. |
| `404` for a dated URL | The date in the path is not the entity's date in the site time zone. |

Before rolling a change out:

1. Set `PUBLIC_BASE_URL` first. Without it, canonical URLs and JSON-LD can carry an internal address.
2. Put a site's schema settings in the site's own migration or seed, and add a theme template for the type if the generic one is not enough. See [Layouts](../themes/layout.md).
3. Request each URL and compare the sitemap URL set before and after the change, as described in [Check your sitemap and structured data](check-sitemap-and-structured-data.md).
