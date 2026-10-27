---
title: Sitemaps
nav_title: Sitemaps
description: How the sitemap index and its child files are built, what each entry holds, what is left out, how caching works and how plugins add addresses.
---

This page lists how Hatamoto builds its sitemaps: the files, the groups, the order and values of each entry, what is left out, caching, and plugin additions. To check your own sitemap, follow [Check your sitemap and structured data](check-sitemap-and-structured-data.md).

## Overview

A sitemap is a file that lists your pages for search engines. Hatamoto generates its sitemaps from the database on request and caches the result; you do not maintain a file. What appears is decided by each content type's [URL pattern](url-patterns.md) and settings, by each entity's publish state and no-index flag, and by your redirect rules.

| URL | Content |
|---|---|
| `/sitemap.xml` | The index: one `<sitemap>` entry per child, with a `lastmod`. |
| `/sitemap-<group>.xml` | The URLs of one group, for example `/sitemap-blog.xml` or `/sitemap-misc.xml`. |
| `/sitemap.xsl` | A stylesheet that makes the XML readable in a browser. The default theme ships it, and a theme cannot replace it. It is served with `Cache-Control: public, max-age=2592000`. |

The index is also announced on the `Sitemap:` line of `/robots.txt`. An unknown child name answers `404`. Sitemap responses are served as `application/xml; charset=utf-8` with `Cache-Control: public, max-age=3600, s-maxage=3600`, and begin with an `xml-stylesheet` instruction pointing at `/sitemap.xsl`.

A trimmed index for a site with pages and posts:

```xml
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap>
    <loc>https://example.com/sitemap-blog.xml</loc>
    <lastmod>2026-01-14</lastmod>
  </sitemap>
  <sitemap>
    <loc>https://example.com/sitemap-misc.xml</loc>
    <lastmod>2026-01-10</lastmod>
  </sitemap>
</sitemapindex>
```

## Groups

Each content type belongs to a sitemap group, and each group becomes one child file. The group is, in order of precedence:

1. The `sitemap_group` setting of the content type, or of a matching override. In the admin it is the **Sitemap group** field of the entity type.
2. The first literal segment of the URL pattern, so `/docs/{slug}` is in group `docs`.
3. `misc`, when the pattern starts with a token such as `/{slug}`.

The built-in defaults put pages and anything else with a root pattern in `misc`, and blog posts in `blog` (see [URL patterns](url-patterns.md#defaults)).

**`misc` is special.** It always exists, is always last in the index, and is listed even when it has no entries. It also holds the homepage (listed as `/`), overrides that name group `misc`, and the static routes that plugins register. Other groups appear only when they have at least one URL.

## What goes into a child file

Within a group, the order is:

1. In `misc`, the plugin static routes first.
2. The group's index page: the `archive_path` of the content type (for example `/blog`), or a section page. It is listed only when the group has at least one row, so an empty archive is never advertised.
3. Section pages, then every other entity, newest update first.

Each `<url>` has `loc`, `lastmod`, `changefreq` and `priority`. The `lastmod` is the entity's last update date. The archive and section index pages use today's date, a `daily` change frequency and priority `0.9`. In the index, a child's `lastmod` is the most recent update among its rows.

## Priority and change frequency

Unless the content type's `sitemap` setting says otherwise, an entity gets these values from its content type:

| Content type | Priority | `changefreq` |
|---|---|---|
| `homepage` | `1.0` | `daily` |
| `page` | `0.8` | `monthly` |
| `blog`, `post`, `article` | `0.6` | `weekly` |
| any other type | `0.5` | `monthly` |

The entity that is served at `/` is listed with `1.0` and `daily`. A content type's `sitemap` object overrides both values. In the admin these are the **Sitemap priority** (0 to 1) and **Sitemap change frequency** fields of the entity type.

```json
{
  "url_pattern": "/docs/{slug}",
  "sitemap": { "priority": 0.7, "changefreq": "weekly" }
}
```

## What is left out

An entity is not listed when any of the following is true:

- It is not published. Drafts, scheduled and archived entities never appear.
- It is marked no-index: `metadata.no_index` or `attributes.no_index` is true, or `attributes.seo.robots` contains `noindex`. The editor's **Ask search engines not to list this page** switch sets this (see [robots.txt and llms.txt](robots-llms.md#per-page-controls)).
- Its content type has `"sitemap": {"exclude": true}` (in the admin, **Leave this type out of the sitemap**). Such a type gets no entries and no index page.
- A redirect rule claims its path. A path that answers with a redirect is not an address to index.
- Its slug is reserved. The slug `home` is never advertised at `/{slug}`, and neither is a section prefix or the site's own canonical slug, because those URLs are dead or duplicates.
- Its URL cannot be built, for instance because a required date or parent is missing.
- It is a hidden person, or a built-in block or theme part. These are used by reference and never get a page.

Entities with nested paths or dated URLs are listed at their canonical path, the same one used by the page's canonical link and JSON-LD `url`. A move or rename that creates a redirect therefore removes the old path from the sitemap and lists the new one.

## Caching and invalidation

The index and every valid child are cached for one hour, but only when `PUBLIC_BASE_URL` is set. The XML holds the base URL, and without that variable the base comes from the request host, so each request is built fresh. A cached answer carries the header `X-Cache: HIT`.

Sitemaps are cleared together with the rendered page cache: whenever an entity is created, updated, published, unpublished or deleted, after an import, after a menu edit, when schema settings change, and when plugin settings change. Only valid children are ever cached.

## Plugin additions

Plugins can add URLs in two ways. See [Hooks and events](../plugins/hooks-and-events.md).

- **Archive groups and static routes**, registered by a plugin when it starts. A group adds its own child sitemap, and a static route adds one URL to `misc`.
- **The `sitemap.urls` filter**, run for one file at a time. The host sends the group name (`misc` for the misc file); the `language` field of the payload is left empty. The plugin returns the URLs it wants to add, up to 5000 per call. Hatamoto appends those whose `loc` is not empty and not already in the file. A plugin cannot remove or change existing entries. The filter chain has a 2 second budget, and because the result is cached, a filter must give the same answer each time. An hreflang alternate on an added URL adds the `xhtml` namespace to that file.

## The preview tool

The MCP tool `sitemap_preview` serves the real route in process and returns the XML with a count of `<url>` and `<sitemap>` entries. Pass `sub` to read one child, for example `blog`. `sub` may hold only lowercase letters, digits, hyphens and underscores, up to 64 characters. See [MCP server](mcp.md#tools).

## Limits and errors

| Symptom | Cause and fix |
|---|---|
| A page is missing | It is not published, it is marked no-index, a redirect rule claims its path, or its content type is excluded. |
| A child file is missing from the index | A group with no rows is dropped, except `misc`. |
| A URL still shows after unpublishing | The cached file lives for up to an hour unless the change cleared it. Check that the unpublish was saved. |
| `localhost` or an internal host in the URLs | Set `PUBLIC_BASE_URL`. See [URL patterns](url-patterns.md#the-public-base-url). |
| `404` for `/sitemap-<name>.xml` | No content type or plugin uses that group. |

Submit `https://example.com/sitemap.xml` once in each search engine's webmaster tool. The robots file already points to it.
