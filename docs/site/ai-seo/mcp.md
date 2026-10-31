---
title: MCP server
nav_title: MCP server
description: Every tool, scope, limit and audit record of the endpoint AI agents use to read, draft and publish on your site.
---

This page lists everything about the Model Context Protocol (MCP) endpoint at `/mcp`: how it authenticates, the scope each tool needs, every tool and its inputs, and the limits and the audit log. To connect your first agent, follow [Connect an AI agent](connect-an-ai-agent.md).

## Overview

Hatamoto serves [MCP](https://modelcontextprotocol.io), a standard way for AI tools to call functions on a server, at `/mcp`. It uses the streamable HTTP transport of the official Go SDK, and accepts `POST`, `GET` and `DELETE`. An AI agent connected to it can read your content, draft and publish entities, upload media, edit menus and redirects, and read crawler analytics, always inside the limits of the API key it presents.

| Item | Value |
|---|---|
| Address | `https://example.com/mcp` |
| Authentication | `Authorization: Bearer <key>` on every request |
| Key format | `nxk_` followed by 64 hex characters |
| Server name and version | `hatamoto`, `1` |
| Idle session timeout | 30 minutes |
| Default rate limit | 1000 requests per hour per key |

The server's instructions to the agent read: "Hatamoto CMS. Create drafts with entities_create, review them, then entities_publish. Read schemas_get first to learn which attributes a content type takes."

## Keys

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

An API key is a secret string a program uses to act on your site. You create one in three ways:

- In the admin at **Settings > API** (`<admin>/settings/api-keys`), owner only, with **New API key**. The form has a name, the **Access** checkboxes (one per scope) and **Expires**: never, in 30 days, in 90 days or in a year. The key is shown once after **Create key**.
- With `POST /api/v1/api-keys` as an owner. The same owner-only group has `GET /` (list), `GET /scopes`, `GET /{id}`, `PATCH /{id}` (name, description, scopes, rate limit, active), `POST /{id}/revoke`, `POST /{id}/regenerate` and `DELETE /{id}`. The admin cannot change a key's scopes; the API can.
- With the command line, which needs `DATABASE_URL`:

```bash
hatamoto apikey create --name "docs agent" \
  --scopes read:entities,write:entities,read:reports \
  --out ./agent.key
```

| Flag | Default | Meaning |
|---|---|---|
| `--name` | required | The key name. It appears in the audit log and in version history. |
| `--scopes` | required | Comma-separated scopes. An unknown scope is refused with the list of valid ones. |
| `--out` | none | Write the key to this new file with mode `0600`. Refuses to overwrite an existing file. Without it the key alone is printed on standard output, once. |
| `--as` | the oldest owner | Email of the user the key acts as. |
| `--description` | empty | What the key is for. |
| `--rate-limit` | `0` (the default, 1000) | Requests per hour. The flag help says per minute, but the limiter counts per hour. |

With `--out` the command prints:

```text
created API key <id> (docs agent, scopes read:entities,write:entities,read:reports)
key written to ./agent.key (mode 0600)
```

The plain key is shown once and never stored. In the admin, **New secret** replaces a key's secret (the old one stops working at once), **Revoke** stops a key for good and **Delete** removes it. The list shows each key's prefix, access, last use and status (**Active**, **Revoked** or **Expired**).

## Who a key acts as

A key acts with the authority of the user who created it, never more. Hatamoto looks up that user's current role on every request, and the key stops working if the creator is deleted or no longer has the owner or editor role. Content written through MCP is attributed to the key's creator, and the version history tags the change with `[mcp:<key name>]`, followed by the optional `change_summary`.

A request with no key, or with an invalid, revoked or expired key, answers `401`.

## Scopes

Every tool needs exactly one scope. A `read:` scope can never call a write tool. A `write:X` scope implies `read:X`, and `*` allows everything. The media, menu, redirect and term scopes are separate: `write:entities` does not grant them, and keys created before they existed do not gain them.

| Scope | Admin label | Tools |
|---|---|---|
| `read:entities` | Read entities | `entities_list`, `entities_get`, `search`, `schemas_list`, `schemas_get`, `terms_list`, `menus_list`, `menus_get`, `redirects_list`, `sitemap_preview`, `llms_txt_preview`, `jsonld_validate` |
| `write:entities` | Write entities | `entities_create`, `entities_update`, `entities_publish`, `entities_unpublish` |
| `write:media` | Write media | `media_upload` |
| `write:terms` | Write terms | `terms_create` |
| `write:menus` | Write menus | `menus_update` |
| `write:redirects` | Write redirects | `redirects_create`, `redirects_delete` |
| `read:reports` | Read reports | `analytics_crawler_hits`, `analytics_first_crawls`, `analytics_google`, `analytics_ai_referrals`, `analytics_traffic`, `analytics_conversions` |
| `*` | Everything (full access) | all of the above |

The scope list also contains `read:citations`, `write:reports`, `read:query_suites` and `write:query_suites`. No MCP tool uses them.

The four scopes `write:media`, `write:menus`, `write:redirects` and `write:terms` are reachable only through MCP. A REST API key reaches only a fixed list of routes: entities and their versions, the theme render preview, and internal-link suggestions and inserts.

A tool call without the needed scope fails with `forbidden: the API key lacks the <scope> scope`. The call is still written to the audit log.

## Tools

Lists return at most 100 rows. The default page size is 20 for `entities_list` and the analytics tools, 10 for `search`, and 50 for `terms_list` and `redirects_list` (which take `per_page`).

| Tool | Notable inputs | What it does |
|---|---|---|
| `entities_list` | `schema`, `status`, `search`, `page`, `limit` | Newest first, drafts included. `search` matches part of the name. |
| `entities_get` | `id` | One entity with its attributes. |
| `search` | `query`, `page`, `limit` | Full-text search over published content, best match first, as the site search shows it. |
| `entities_create` | `schema`, `name`, `slug`, `locale`, `attributes` | Creates a draft. The slug is made from the name when empty. Nothing is public until `entities_publish`. |
| `entities_update` | `id`, `name`, `slug`, `locale`, `attributes`, `change_summary`, byline fields | Stores a new version. `attributes` replaces the whole object. |
| `entities_publish`, `entities_unpublish` | `id` | Publish puts the entity at its URL, in the sitemap and in `llms.txt`. Unpublish returns it to draft. |
| `schemas_list`, `schemas_get` | `slug` | Content types, their JSON Schema and URL settings. Read `schemas_get` first to learn which attributes a type takes. |
| `terms_list`, `terms_create` | `kind` (`tag` or `category`), `name`, `slug`, `search` | Tags and categories with usage counts. A listed term carries its `seo` when one is saved, but `terms_create` takes none: a term's search appearance is edited on its admin page only. |
| `menus_list`, `menus_get`, `menus_update` | `id`, `slug` or `location` (`header`, `footer` or `sidebar`); `name`, `items` | Menus and their nested items. |
| `redirects_list`, `redirects_create`, `redirects_delete` | `source_path`, `target_url`, `status_code`, `id` | Redirect rules. Status is `301` (default), `302`, `307` or `308`. Whole-segment `*` and a trailing `**` are wildcards. |
| `media_upload` | `filename`, `data_base64`, `alt_text`, `caption` | Adds a file to the media library. |
| `sitemap_preview` | `sub` | The sitemap XML as crawlers get it now, or one child sitemap, with counts of `<url>` and `<sitemap>` entries. |
| `llms_txt_preview` | none | The `/llms.txt` text as crawlers get it now. |
| `jsonld_validate` | `entity_id` | Renders and checks a page's JSON-LD. See [JSON-LD](json-ld.md#the-checker). |
| `analytics_crawler_hits` | `from`, `to`, `entity_id`, `limit` | AI crawler fetches per page and agent. With `entity_id`, also that page's last fetch per agent, first crawl, and `search_first_crawl_at` (first search engine crawl). |
| `analytics_first_crawls` | `from`, `to`, `limit` | Pages first crawled in the range, which agent, and how long after publishing. |
| `analytics_google` | `from`, `to`, `filter`, `limit` | Googlebot crawling: crawls, verified share, pages crawled of published, median publish to first crawl, and per-page rows. `filter` is `all`, `not-crawled`, `errors` or `stale`. |
| `analytics_ai_referrals` | `from`, `to` | Visits sent by AI assistants, per day, with `total_visits`. |
| `analytics_traffic` | `from`, `to`, `by`, `limit` | Visits, pageviews and engaged share by `channel`, `source`, `campaign`, `page`, `device` or `country`. Counts only; no visitor, IP or user agent fields. See [Crawler analytics](crawler-analytics.md). |
| `analytics_conversions` | `from`, `to`, `touch`, `by`, `limit` | Form submissions (leads) by `channel`, `source`, `campaign`, `page` or `form`, for the `first` (default) or `last` touch of the submitter's tab, with `total_leads` and `unattributed_leads`. Spam is not counted. Counts only: no submitter emails or field values. |

The analytics tools take dates as `YYYY-MM-DD`, inclusive and in UTC. The default range is the 30 days ending today; with only `to`, `from` is 29 days before it. `from` after `to`, or a range longer than 366 days, is refused.

Four tools are marked destructive in their annotations, so well-behaved clients ask before running them: `entities_update`, `entities_unpublish`, `menus_update` and `redirects_delete`. All tools that only read are marked read-only.

## Behaviour worth knowing

- **Same services as the API.** Tools call the internal services directly, not the HTTP API, so validation, slug rules, rich-text sanitising, plugin `before_save` hooks and cache invalidation behave the same way.
- **Page documents.** `attributes.page_doc`, the node tree of the visual builder, can be sent to create and update. It is validated and its component content sanitised. An invalid document fails with `invalid page_doc: ...`. Sending a changed `page_sections` for a page that already has a document fails with `page_sections can no longer change a page that has a page_doc; send attributes.page_doc (docs/BUILDER.md)` (REST: 422 `page_sections_removed`, so match on that `code` rather than the message); sending the stored or derived value back is accepted, and a page without a document that is sent `page_sections` is converted to a document once. Omitting `page_doc` on update keeps the stored one. `entities_update` replaces `attributes` as a whole, so read the entity, change it and send it back.
- **Bylines.** The byline is separate from `attributes`: use `byline_person_id` (an entity of the person type), `byline_org` (`true` credits the site organisation, `false` clears it) or `clear_byline`. Omit all three to leave it alone. Once an entity is credited, `attributes.author` and `attributes.author_name` are overwritten with the credited name on update, so a stale copy cannot revert the credit.
- **Media.** `media_upload` takes standard base64. The encoded size is checked against `MEDIA_MAX_SIZE_MB` (default 10) before decoding. The type is sniffed from the bytes and must match the file extension: jpg, png, gif, webp, pdf, mp4 and webm are accepted, SVG never.
- **Menus.** `menus_update` with `items` replaces every item; without `items` the items are left alone. Items are a flat list with `key` and `parent_key`, and a parent must come before its children. Each item has a `label` and a `link_type` (`entity` with `entity_id`, `external` or `custom` with `url`, or `archive` with `schema_slug`). The list is validated first, then everything is written in one transaction, so a bad item leaves the live menu untouched. The result is the saved menu plus a `warnings` list (always present, empty when all is well): an item with no target, such as an `entity` item without `entity_id`, is saved and renders as plain text, and each one is named there.
- **Previews are real.** `sitemap_preview` and `llms_txt_preview` fetch the real route in process, with the user agent `hatamoto-mcp-preview` and the host of `PUBLIC_BASE_URL`. Output over 64 KB is cut and flagged with `truncated`. `sub` may hold only lowercase letters, digits, `-` and `_`, up to 64 characters.
- **Locale.** Entity results carry a `locale` (a BCP 47 tag such as `en` or `fr`). `entities_create` takes an optional `locale` and stores the site language when it is empty; `entities_update` keeps the stored one when it is omitted. A slug is unique per schema and locale, and the public site serves only the site language.
- **Errors.** Known errors (not found, slug taken, validation failed, a bad parent, a rejected redirect pattern, a file too large) keep their text. Anything else is logged on the server and the agent sees `internal error`.

## Rate limit and sessions

- **Rate limit.** Each key has an hourly request limit, `1000` by default (a value that is not positive is treated as the default). A token bucket also smooths bursts, allowing about a tenth of the hourly limit at once, with a floor of ten. The limit applies to every HTTP request to `/mcp`, including `initialize`, list and ping calls. Over the limit the answer is `429` with `Retry-After: 1`.
- **Sessions.** The `initialize` answer carries an `Mcp-Session-Id` header; send it back on later requests. Idle sessions close after 30 minutes.
- **Request size.** A request body may hold a base64 upload of the largest allowed media file plus 1 MiB.

## Origin check

A request that carries an `Origin` header, which means a browser, must come from the site's own origin (taken from `PUBLIC_BASE_URL`) or from an origin listed in `HATAMOTO_MCP_ALLOWED_ORIGINS`, a comma-separated list. Anything else is refused with `403 Forbidden: origin not allowed`. Without `PUBLIC_BASE_URL`, every browser origin not on the list is refused. Agents and command-line clients send no `Origin` header and are not affected.

```bash
HATAMOTO_MCP_ALLOWED_ORIGINS=https://tools.example.com,https://app.example.com
```

Behind a reverse proxy on the same host, such as Caddy or nginx, nothing else is needed. The SDK's loopback `Host` check is switched off, because every request already needs a secret bearer key. See the [configuration reference](../operate/configuration-reference.md) for the variable.

## Audit log

Every tool call, allowed or refused, writes a row to the `hatamoto_mcp_audit` table: time, key id and name, tool, success, error text and the arguments. Strings longer than 200 bytes are shortened, and arguments over 4 KB are replaced by a note with their size, so a base64 upload never lands whole. Each call is also written to the server log as `mcp tool call`. A failed audit insert is logged and never fails the call. The worker deletes audit rows older than 90 days.

```sql
SELECT at, api_key_name, tool, ok, error
FROM hatamoto_mcp_audit
ORDER BY at DESC
LIMIT 20;
```

## Good practice

Give each agent its own key with the fewest scopes it needs. A research agent needs `read:entities` and perhaps `read:reports`. `write:entities` includes `entities_publish` and `entities_unpublish` as well as `entities_create` and `entities_update`. No scope allows drafting without publishing, so an agent holding it can put pages live or take them down with no human step. To keep a person in the loop, do not give an agent `write:entities` at all, or accept that risk and review the audit log regularly. Set an expiry, replace secrets with **New secret** in the admin, and watch the audit table.
