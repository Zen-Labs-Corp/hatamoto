---
title: Hooks and events
nav_title: Hooks and events
description: Every event a plugin can react to and every value it can change, with the data each one receives and what happens when it fails.
---

A **hook** is a point where the server calls a plugin. This page lists all fourteen hooks, the data each one receives and the rules for failures. To add your first action and filter, follow [React to events](react-to-events.md).

There are two kinds. An **action** announces that something happened and returns nothing. A **filter** receives a value and returns a possibly modified value. Hook names and payload shapes are part of the SDK's compatibility surface, defined in `backend/pkg/sdk/hooks.go` and `events.go`.

## Export names

A plugin handles a hook by exporting a function named after it: replace `.` with `_` in the hook name and prefix `on_` for an action or `filter_` for a filter. So `entity.published` is `on_entity_published` and `jsonld.enrich` is `filter_jsonld_enrich`. In Go, `sdk.EntityPublished.Export()` returns that name. `//go:wasmexport` must sit on a function in `package main`, so each handled hook needs its own one-line export that calls `guest.Run`. `guest.OnAction` and `guest.OnFilter` take typed hook tokens such as `sdk.EntityPublished`, so the handler receives a decoded payload struct and a filter returns the modified value. The other exports are `http_handle` for routes, `on_cron_<job>` for cron jobs and `func_<name>` for template functions. How the server calls an export is on [The host API](host-api.md#how-calls-work).

## All hooks

| Hook | Kind | Export | Payload | Result |
|---|---|---|---|---|
| `entity.created` | action | `on_entity_created` | EntityEvent | none |
| `entity.updated` | action | `on_entity_updated` | EntityEvent | none |
| `entity.published` | action | `on_entity_published` | EntityEvent | none |
| `entity.deleted` | action | `on_entity_deleted` | EntityEvent | none |
| `import.completed` | action | `on_import_completed` | ImportEvent | none |
| `agent.hit` | action | `on_agent_hit` | AgentHitEvent | none |
| `agent.first_crawl` | action | `on_agent_first_crawl` | AgentFirstCrawlEvent | none |
| `form.submitted` | action | `on_form_submitted` | FormSubmittedEvent | none |
| `jsonld.enrich` | filter | `filter_jsonld_enrich` | JSONLDDoc | JSONLDDoc |
| `sitemap.urls` | filter | `filter_sitemap_urls` | SitemapURLs | SitemapURLs |
| `render.head` | filter | `filter_render_head` | HeadContext | HeadContext |
| `render.page` | filter | `filter_render_page` | PageContext | PageContext |
| `entity.before_save` | filter | `filter_entity_before_save` | EntityDraft | EntityDraft |
| `builder.data` | filter | `filter_builder_data` | BuilderDataRequest | BuilderDataRequest |

The Go tokens are `sdk.EntityCreated`, `sdk.EntityUpdated`, `sdk.EntityPublished`, `sdk.EntityDeleted`, `sdk.ImportCompleted`, `sdk.AgentHit`, `sdk.AgentFirstCrawl`, `sdk.FormSubmitted`, `sdk.JSONLDEnrich`, `sdk.SitemapURLsF`, `sdk.RenderHead`, `sdk.RenderPage`, `sdk.EntityBeforeSave` and `sdk.BuilderData`. The host registers a handler for every hook whose export exists and ignores the rest, so handle only what you need.

## Actions and filters

**Actions** run asynchronously after the event, in a background goroutine. The output is ignored and a failure is only logged. Actions must be fast and must not make slow network calls. A slow call holds one of the plugin's instances, and a few traps open the circuit breaker. Core's outgoing webhooks use the right pattern: the action hook only inserts a row and a scheduled job does the sending (see [Webhooks](../admin/webhooks.md) and the patterns in [First-party plugins](first-party-and-other-languages.md#patterns-worth-copying)).

**Filters** run in order on the request path. Plugins run in plugin-name order, and each filter receives the previous filter's result. Unknown fields in a returned value are ignored, and top-level fields a filter leaves out keep their previous value, so a filter may return only what it changed. A field it does return replaces the old one as a whole.

## Failure handling

A filter that returns non-zero, traps, runs out of memory, exceeds its timeout or returns output that does not decode leaves the value as it was before that plugin. It is logged, the request goes on and the other plugins still run. An empty output from a filter means "unchanged". The instance that failed is discarded, while a guest that only reports an ordinary error (an error message plus non-zero status, no trap) keeps its instance.

A hook that traps or times out three calls in a row is switched off for 30 seconds for that plugin and hook. When a request runs several filters under one deadline (page head, JSON-LD, sitemap), each filter gets at most its fair share of the time left, so a slow plugin cannot starve the ones after it.

Output is bounded. One call returns at most `limits.max_output_bytes` (default 1 MiB, up to 4 MiB). `render.head` may add at most 64 tags and `sitemap.urls` at most 5,000 additions. JSON-LD arrays are capped at 1,000 elements and depth 32. Output over a cap counts as a failure.

Default timeouts are 250 ms for filters and 10 seconds for actions. Override them per hook with `limits.hook_timeouts_ms` (see [The plugin manifest](manifest.md#limits)).

## The one exception: refusing a save

`entity.before_save` runs before an entity is created, updated or restored. To refuse the save, set the error message to `hatamoto:reject:<reason>` and return non-zero. The API client gets HTTP 422 with the reason. Any other failure fails open. In Go, return `guest.Reject("reason")`:

```go
guest.OnFilter(sdk.EntityBeforeSave, func(d sdk.EntityDraft) (sdk.EntityDraft, error) {
	if d.SchemaSlug == "post" && len(d.Name) < 5 {
		return d, guest.Reject("titles need at least five characters")
	}
	return d, nil
})

//go:wasmexport filter_entity_before_save
func filterEntityBeforeSave() int32 { return guest.Run(sdk.EntityBeforeSave.Export()) }
```

`operation` is `create`, `update` or `restore`, and `id` is empty for create. Returning a modified draft changes `name`, `slug` or `attributes` before they are saved; other fields are ignored. The result is validated and sanitised like any save. A page's `page_sections` are derived from `attributes.page_doc`, so to change a page's layout from a hook, edit `attributes.page_doc`. A hook's change to `page_sections` on a page that has a document is discarded, with only a warning in the server log.

## Payloads

IDs are UUID strings and timestamps are RFC 3339.

**EntityEvent** (and `EntityRef`, used in JSON-LD) carries `id`, `schema_slug`, `slug`, `locale`, `name`, `status` and `version`. `locale` arrived with SDK 0.13.0; older plugins ignore it.

**ImportEvent** fires once per import batch (the WordPress importer, the archive import, any bulk load), after the rows were stored. Imported rows fire no `entity.*` action, so a plugin that must see imported content reacts here and lists what changed through the host API.

```json
{ "source": "wordpress", "created": 120, "updated": 3, "failed": 1 }
```

**AgentHitEvent** describes one stored AI-crawler hit. It never includes an IP address or a user agent. `entity_id` is empty off entity pages and `status` is `0` when unknown. See [Crawler analytics](../ai-seo/crawler-analytics.md).

```json
{ "agent_id": "string", "operator": "string", "purpose": "string", "verification": "string",
  "method": "string", "path": "string", "entity_id": "string", "source": "string",
  "status": 0, "bytes": 0, "duration_ms": 0, "occurred_at": "2026-01-02T03:04:05Z" }
```

**AgentFirstCrawlEvent** fires once per entity, on the first served AI fetch: `entity_id`, `agent_id`, `operator`, `purpose`, `first_at` and `published_at` (omitted when the entity has no publish time).

**FormSubmittedEvent** fires after a public form submission was stored. It carries the submitted fields, so it is delivered only to plugins that hold `forms.read`. The visitor's IP address and user agent are never included. `attribution` (`first` and `last` touch with `channel`, `source`, `medium`, `campaign`, `landing`) arrived with SDK 0.14.0; it is left out when none was recorded, and older plugins ignore it.

```json
{ "form_id": "string", "form_slug": "contact", "submission_id": "string",
  "fields": { "email": "a@example.com" }, "source_path": "/contact", "submitted_at": "2026-01-02T03:04:05Z" }
```

## Filters in detail

### jsonld.enrich

Modify and return `doc`, the schema.org document. Keep `entity` and `base_url` as received. The hello plugin adds `x-hello` this way.

```json
{ "entity": { "id": "string" }, "base_url": "https://example.com", "doc": { "@type": "Article" } }
```

### sitemap.urls

The filter is additions only. The host sends `group` (the sitemap file's group slug, `"misc"` for the misc sitemap), `language` (the BCP 47 tag of the locale a per-locale file belongs to, empty on a one-language site), `segment` (that locale's stored URL segment, empty for an unprefixed default locale) and `file` (the full file key such as `xx-misc`; `group` stays the bare name), plus in `add` the entries earlier plugins already added. It never sends the file's own URL list, because decoding and encoding 5,000 URLs costs a Go guest about half a second. Append your entries and return the value. The host adds the ones whose `loc` the file does not already have, so a plugin can neither change nor remove existing entries. At most 5,000 entries in total. Target one file (usually `misc`) when adding site-wide URLs. The result is cached with the sitemap XML, so keep it deterministic.

```json
{ "group": "misc", "language": "en-XX", "segment": "xx", "file": "xx-misc",
  "add": [ { "loc": "https://example.com/a", "lastmod": "2026-01-01", "changefreq": "weekly", "priority": 0.5 } ] }
```

### render.head

The input describes the page and carries no tags: `path`, `language` (the BCP 47 language of the page, such as `en-AU`), `schema_slug` and `entity_id`. On a site with several public locales it also carries `locale` (the page's tag) and `segment` (its stored URL segment, empty for an unprefixed default locale), both left out on a one-language site; they arrived with SDK 0.15.0. The result's `tags` are appended to the page head. The host builds and escapes the markup and drops attributes outside an allowlist per kind (`meta`: `name`, `property`, `content`; `link`: `rel`, `href`, `type`, `hreflang`, `crossorigin`; `script-src`: `src`, `crossorigin`) and unsafe URL schemes. Set `replace_site_graph` when your `jsonld` tags carry their own Organization and WebSite graph.

The filter may run more than once for one render: a page with a paged builder loop renders its head twice and keeps one result. Keep it free of side effects, such as `kv` counters or outbound pings.

```json
{ "tags": [ { "kind": "meta", "attrs": { "name": "robots", "content": "noindex" } } ] }
```

The kinds are `meta`, `link`, `script-src` and `jsonld`. The gtm plugin uses a single `meta` tag.

### render.page

The request-aware filter. It runs for each public view of an entity or listing page, not for 404 and error pages. The payload holds `path`, the `cookies` the plugin declared, the `entity` summary and the page's `blocks`. By default blocks are slim: `id`, `type`, `title`, `hidden` and `fields`, the short scalar values of the content keys `variant`, `anchor` and `label`. There is no `content`, so the call costs well under a millisecond. The host keeps the original content and re-attaches it by id. Set `"render_page": { "full_blocks": true }` to receive whole blocks (slower).

The result may reorder or replace `blocks`, list ids in `hide` to drop them, and add `set_cookies` (each name declared in the manifest, at most 8 cookies and 200 blocks). A result that breaks a rule is dropped whole. In slim mode, return a block as received to keep it, or with `content` to replace that block's content.

```go
guest.OnFilter(sdk.RenderPage, func(p sdk.PageContext) (sdk.PageContext, error) {
	if p.Cookies["unlock"] != "1" {
		p.Hide = append(p.Hide, "members-only")
	}
	return p, nil
})
```

Handle `render.page` only when you need to. While any plugin handles it, pages are served `Cache-Control: private, no-store` with `Vary: Cookie` and are not stored in the page cache, because the answer depends on the request. Declared cookies are namespaced as `hmp_<plugin>_<name>`, so a plugin never sees the session or CSRF cookie. The host forces `HttpOnly`, `SameSite=Lax` and `Secure` on HTTPS. Values are cookie-safe ASCII up to 2 KiB and `max_age` is at most a year.

### builder.data

Answers the data-source requests that one page render collected for the page builder (SDK 0.11 and later). The filter is registered only for a plugin that declares `builder_sources` in its manifest and holds the `builder.data` permission. The input carries only the requests that name the plugin's own sources (`ext.<plugin>.<name>`), plus `path`, `language`, `schema_slug` and `entity_id`. Return the same object with `results` set, one per request, matched by `id`. Results for ids that were not sent are ignored.

```json
{ "path": "/weather", "requests": [ { "id": "r1", "source": "ext.weather.forecast", "limit": 3 } ],
  "results": [ { "id": "r1", "items": [ { "title": "Monday", "url": "https://example.com/mon", "attrs": { "temp": "21" } } ] } ] }
```

A `value` source answers with `value` (one string); an `items` source answers with `items`, each with optional `title`, `url`, `excerpt`, `image`, `category`, `author`, `author_url`, `published_at` and an `attrs` map. A non-empty `error` marks one request as failed, and it renders empty. URLs and images go through the server's usual URL checks.

The server sends at most two batched calls per render (the page, then once more for sources only the theme's parts name). Caps, where a broken cap drops the whole result: 16 requests per render, 48 items per result, strings of at most 2048 bytes of valid UTF-8, 16 `attrs` keys per item, and output of at most 64 elements per array and depth 6. The default timeout is 250 ms (override with `limits.hook_timeouts_ms["builder.data"]`, up to 10 seconds) inside a 500 ms budget for the whole batch. A failure leaves the affected values empty and keeps that page out of the page cache. The repository's `examples/plugins/datasource-demo` is a working example.

## Reading more

Outgoing webhooks for `entity.*`, `form.submitted` and `import.completed` are built into core (see [Webhooks](../admin/webhooks.md)), not a plugin. For the host calls a hook handler can make, continue with [The host API](host-api.md).
