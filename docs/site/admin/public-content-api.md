---
title: Public content API
nav_title: Public content API
description: Read published posts, pages and term lists as JSON from another program: the three routes, fields, paging, caching and limits.
---

The **public content API** lets another program, such as a mobile app or a separate front end, read the published content of your site as JSON. It is read-only, needs no key and is off until the site owner turns it on. This page lists every route, field, header and limit. To connect programs that write to the site, see [Invite people and create API keys](users-and-api-keys.md).

## Overview

The API serves three `GET` routes under `/api/v1/public` (or under `PUBLIC_API_PREFIX`, when that is set). It lists only what a visitor can already see on the site.

To turn it on, the owner opens **Settings > Integrations > Public content API** (`<admin>/settings/public-api`), switches **Public content API** on and presses **Save**. Your admin address is private to your install; print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`. While the switch is off, every route answers `404`, exactly like an address that does not exist.

```bash
curl -s 'https://example.com/api/v1/public/content/post?per_page=2'
```

```json
{
  "data": [
    {
      "id": "0b7f4f0e-5a52-4a4e-9c0e-2d1f6c1b7a10",
      "schema": "post",
      "slug": "hello-world",
      "url": "https://example.com/blog/hello-world",
      "title": "Hello world",
      "excerpt": "<p>A short first post.</p>",
      "published_at": "2026-01-10T09:00:00Z",
      "updated_at": "2026-01-12T14:30:00Z",
      "author": { "name": "Ada Example", "slug": "ada-example" },
      "terms": [
        { "kind": "category", "slug": "news", "name": "News" }
      ]
    }
  ],
  "meta": { "total": 7, "page": 1, "per_page": 2, "total_pages": 4, "has_next": true }
}
```

## Endpoints

| Route | Answers with |
|---|---|
| `GET /content/{schema}` | One page of the published entries of a content type, such as `post` or `page`. |
| `GET /content/{schema}/{slug}` | One published entry. |
| `GET /terms/{kind}` | One page of the terms of a kind, such as `category` or `tag`, that have at least one published entry. |

`{schema}` is the content type's slug. A type's alias works too, and each entry reports the type's main slug in `schema`. A list answers `{"data": [...], "meta": {...}}`. A single entry answers `{"data": {...}}`.

### Content entry

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The entry's id. |
| `schema` | string | The slug of its content type. |
| `slug` | string | The entry's slug. |
| `url` | string | The absolute address of the page on your site. Left out when the content type has no public page. |
| `title` | string | The entry's name. |
| `excerpt` | string | The first non-empty text among the entry's `excerpt`, `summary` and `description` fields, exactly as stored. Left out when there is none. |
| `published_at` | string | Publication time in UTC, RFC 3339. Left out when unset. |
| `updated_at` | string | Last change in UTC, RFC 3339. |
| `author` | object | `name`, and `slug` when the author is a public person. Left out when the entry has no byline. |
| `terms` | array | The entry's terms, each with `kind`, `slug` and `name`. Empty when there are none. |

No other field of an entry is sent. Content fields other than the excerpt, SEO settings, page layouts, internal notes and user data never appear.

### Term

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | The kind, for example `category`. |
| `slug` | string | The term's slug. |
| `name` | string | The term's name. |
| `description` | string | Left out when empty. |
| `count` | number | How many published entries have the term. |

Terms are ordered by name, then slug. An unknown kind answers `404`.

## Paging

The two list routes take the same query parameters.

| Parameter | Default | Meaning |
|---|---|---|
| `page` | `1` | Page number, a whole number of 1 or more. |
| `per_page` | `20` | Items per page, 1 to 100. A larger number is lowered to 100. |

`meta` holds `total` (all matching items), `page`, `per_page`, `total_pages` (at least 1) and `has_next`. A page past the end answers an empty `data` list. Entries are listed newest first.

## Caching

Every answer carries `Cache-Control: public, max-age=60, s-maxage=60`, so a browser or a shared cache may reuse it for 60 seconds. Every answer also carries a weak `ETag`. Send it back in `If-None-Match` and an unchanged answer is `304 Not Modified` with no body.

```bash
curl -si -H 'If-None-Match: W/"abc123"' 'https://example.com/api/v1/public/terms/category'
```

## What is never listed

- Drafts, scheduled entries and archived entries.
- People who are not public.
- Entries whose **Include in /llms.txt** switch is off. Only a stored `false` hides an entry; any other value keeps it listed.
- Entries marked noindex, and content types marked noindex.
- Entries in a language the site does not show.
- Content types that have no public page.

The opt-out and noindex rules are the same as for [llms.txt and the knowledge graph](../ai-seo/robots-llms.md), so one choice hides an entry from both. A hidden entry, and any entry that does not exist, answers `404` on its own route.

The API follows the site's access mode (**Settings > Access & security > Site access**). It answers `404` in coming soon and password mode and `503` in maintenance mode.

## Security

- **No CORS.** The API sends no `Access-Control-Allow-Origin` header, so a browser page on another site cannot read it. Call it from a server, or from a page on your own site.
- **No keys.** There is nothing to sign in with, and nothing to leak. Anything the API returns is public.
- **Escape the excerpt.** `excerpt` is stored text and can contain HTML. Escape it, or sanitise it, before putting it in a page.
- **Read-only.** Only `GET` routes exist. Nothing here writes to the site.

## Limits and errors

Each client address may send 10 requests per second, with a burst of 30. IPv6 clients are counted by their /64 prefix. A request over the limit answers `429` with `Retry-After: 1`.

Errors are JSON, `{"error": "<message>"}`.

| Status | When |
|---|---|
| `400` | A bad paging value, for example `page=0`, `per_page=abc`, or the unknown parameter `limit` (the message says to use `per_page`). |
| `404` | The switch is off, the content type or term kind is unknown or has no public page, the entry is not listed, or the route does not exist. Also the site access modes above. |
| `429` | The rate limit was hit. |
| `503` | Maintenance mode is on. |
| `500` | The server failed to answer. Try again. |
