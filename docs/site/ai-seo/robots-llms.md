---
title: robots.txt and llms.txt
nav_title: robots and llms.txt
description: The exact robots.txt and llms.txt your site serves, the crawler policy per purpose, the per-page opt-outs and the knowledge-graph feed.
---

This page lists everything Hatamoto writes into `robots.txt` and `llms.txt`, the crawler policy behind them, the per-page switches that remove a page, and the JSON-LD feed that `llms.txt` points to. To choose a policy in the admin, follow [Control AI crawlers](control-ai-crawlers.md).

## Overview

Hatamoto generates both files from live data on every request. You never edit them by hand.

| File or feed | Address | Built from | `Cache-Control` |
|---|---|---|---|
| `robots.txt` | `/robots.txt` | Fixed default rules plus the saved robots policy | `public, max-age=300, s-maxage=300` |
| `llms.txt` | `/llms.txt` | The published entities that are not opted out | `public, max-age=3600, s-maxage=3600` |
| Knowledge graph | `/api/v1/public/knowledge-graph` | The same entities, as JSON-LD | `public, max-age=3600, s-maxage=3600` |

Every absolute address in these files uses the public base URL. See [URL patterns](url-patterns.md#the-public-base-url).

## robots.txt

`robots.txt` is the file at the root of a site that tells crawlers, programs that fetch pages automatically, which paths they may fetch. The default output, for a site at `https://example.com`, is:

```text
User-agent: *
Allow: /
Disallow: /auth
Disallow: /api
Disallow: /client
Disallow: /*?*_rsc=
Sitemap: https://example.com/sitemap.xml
```

On an install that still serves the admin at the old address `/admin`, the file also lists `Disallow: /admin`. On any other address it lists nothing about the admin, so the file does not give your admin address away.

The file is served as `text/plain; charset=utf-8`. Because of the five minute cache lifetime, a changed policy can take about five minutes to reach a browser or a CDN.

### Purposes and tokens

Every agent in the crawler registry has a purpose (see [Crawler analytics](crawler-analytics.md#the-agent-registry)). The policy is set per purpose, not per agent. A purpose controls the `robots_token` of every registry agent with that purpose, de-duplicated and sorted. The admin lists the eight purposes in this order:

| Purpose | Value | `robots.txt` tokens it controls | Agents known to ignore `robots.txt` |
|---|---|---|---|
| Training | `training` | Amazonbot, Applebot-Extended, Bytespider, CCBot, ClaudeBot, Diffbot, FacebookBot, GPTBot, Google-Extended, Meta-ExternalAgent, cohere-ai | none |
| Search index | `search_index` | Claude-SearchBot, OAI-SearchBot, PerplexityBot | none |
| User fetch | `user_fetch` | ChatGPT-User, Claude-User, DuckAssistBot, Meta-ExternalFetcher, MistralAI-User, Perplexity-User | all six |
| Other AI | `other_ai` | GoogleOther | none |
| Search | `search` | Applebot, Baiduspider, Bingbot, DuckDuckBot, Googlebot, PetalBot, Sogou, YandexBot | none |
| Social | `social` | Discordbot, LinkedInBot, Pinterestbot, Slackbot, TelegramBot, Twitterbot, facebookexternalhit | none |
| SEO | `seo` | AhrefsBot, DotBot, MJ12bot, SemrushBot | none |
| Archive | `archive` | ia_archiver | none |

Agents without a `robots_token` (WhatsApp and Screaming Frog) are not written. Token-only entries such as `Google-Extended`, which never appears as a crawler user agent, do count, since a robots token is exactly what they respond to.

### How the policy is written

- **Only deny writes anything.** For each denied token, a `User-agent: <token>` group with `Disallow: /` and a blank line is added after the default rules and before the `Sitemap:` line. Allow writes nothing. An agent-specific group replaces the `*` group for that agent, so an explicit allow group would silently drop your default `Disallow` lines. That is why allow is never written.
- **Nothing is preselected.** With no saved policy every purpose is allowed and the output is byte for byte the default above. The same is true when the policy cannot be read. The default rules are never changed, and there are no presets.
- **Owner only.** Only the owner role can save the policy. The admin shows other roles the policy with the note "Only the site owner can change the robots policy."

Denying only the Training purpose produces this output. The tokens are sorted, with capital letters first.

```text
User-agent: *
Allow: /
Disallow: /auth
Disallow: /api
Disallow: /client
Disallow: /*?*_rsc=
User-agent: Amazonbot
Disallow: /

User-agent: Applebot-Extended
Disallow: /

User-agent: Bytespider
Disallow: /

User-agent: CCBot
Disallow: /

User-agent: ClaudeBot
Disallow: /

User-agent: Diffbot
Disallow: /

User-agent: FacebookBot
Disallow: /

User-agent: GPTBot
Disallow: /

User-agent: Google-Extended
Disallow: /

User-agent: Meta-ExternalAgent
Disallow: /

User-agent: cohere-ai
Disallow: /

Sitemap: https://example.com/sitemap.xml
```

### The admin screen

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

The policy lives at **AI analytics > Crawlers**, tab **Robots policy** (`<admin>/analytics/crawlers?tab=robots`). Each purpose has a switch labelled **Allow** and the purpose name. Under it, "robots.txt names:" lists the tokens and "May ignore robots.txt:" lists the agents flagged as ignoring it. **Save robots policy** stores the switches that are off as denied purposes and clears the page cache. The form posts to `<admin>/analytics/crawlers/robots`.

### Saving the policy through the API

`GET` and `PUT /api/v1/agent-analytics/robots-policy` read and replace the policy. The API stores only denied purposes and deletes every other row on each save, so a `PUT` is a full replace: a purpose you leave out of the body is allowed.

```bash
curl -s https://example.com/api/v1/agent-analytics/robots-policy -b cookies.txt

curl -s -X PUT https://example.com/api/v1/agent-analytics/robots-policy \
  -b cookies.txt -H 'Content-Type: application/json' \
  -d '{"purposes":[{"purpose":"training","action":"deny"}]}'
```

Both answer with one row per purpose: `purpose`, `action` (`allow` or `deny`), `tokens` and `ignores_robots_agents`. `GET` needs a signed-in admin session. `PUT` needs an owner session, plus the CSRF token your session uses for other state-changing API calls; any other role gets `403`. The body is limited to 16 KiB.

| Answer | When |
|---|---|
| `400 invalid body` | The body is not JSON or is too large. |
| `400 unknown purpose: <name>` | A purpose is not one of the eight values. |
| `400 duplicate purpose: <name>` | A purpose appears twice. |
| `400 action must be allow or deny` | Any other action. |

### Limits of robots.txt

`robots.txt` is advisory. A well-behaved crawler obeys it, and a rude one does not. Agents flagged `ignores_robots` in the registry, mostly assistants that fetch a page because a user asked, are listed under "May ignore robots.txt", and denying their purpose does not stop them. To see who is actually fetching, read [Crawler analytics](crawler-analytics.md). Verified and spoofed hits are labelled separately, so you can tell a real crawler from a forged user agent.

## Per-page controls

| Control | Stored as | Default | Effect |
|---|---|---|---|
| **Ask search engines not to list this page** (generic editor, **Search and social** card) or **this post** (post editor, **Advanced** tab of the **SEO and AEO** panel) | `attributes.no_index` | off | Left out of the sitemap, site search and the RSS and Atom feeds. The page head carries `noindex,follow`. |
| **Include in /llms.txt** (post editor, **SEO and AEO** tab) | `attributes.seo.include_in_llms_txt` | on | Off removes the entity from `llms.txt`, from the knowledge-graph feed and from the AI crawler comment. |

Other values have the same no-index effect: `attributes.seo.robots` containing `noindex`, or `metadata.no_index` set to true. The page head follows the two attribute values; the sitemap, search and feed listings also follow `metadata.no_index`. Switching **Ask search engines not to list** off also removes `noindex` from `attributes.seo.robots`, keeping its other directives.

The **Include in /llms.txt** switch exists only in the post editor. For other content types, set `attributes.seo.include_in_llms_txt` to `false` through the API or the MCP tool `entities_update`. A missing or unreadable value counts as included.

Pages shown in preview always carry `noindex,follow`, so a draft preview cannot be indexed by accident.

## llms.txt

`/llms.txt` follows the [llmstxt.org](https://llmstxt.org) shape: a Markdown index for language models. It contains, in order:

1. A title: the company name from site settings, or `Knowledge Base` when none is set.
2. A summary line with the last-updated date (the newest update among the listed entities, or today), the number of listed entities and the number of content types.
3. A **Knowledge Graph** list: the JSON-LD feed, the sitemap and, when at least one listed post exists, the RSS feed at `/feed.xml` with a note that an Atom version is at `/atom.xml`.
4. A **Content Types** list, one line per content type with listed entities, in order of first appearance, with the entity count and a link to the feed filtered to that type.
5. A **Per-Page Structured Data** note: every page embeds JSON-LD, and sending `Accept: application/ld+json` returns the structured payload only.

A trimmed example:

```text
# Example Co

> Machine-readable index of all published content for LLM and AI crawler consumption. Last updated 2026-01-14. 42 total published entities across 3 content types.

## Knowledge Graph

- [All entities (JSON-LD @graph)](https://example.com/api/v1/public/knowledge-graph): paginated `application/ld+json` feed of every published entity. Use `?page=N&per_page=50` (max 100) to walk the full set.
- [Sitemap (XML)](https://example.com/sitemap.xml): canonical URL index.

## Content Types

- [Page](https://example.com/api/v1/public/knowledge-graph?schema=page): 12 entities as JSON-LD.
```

Only published entities appear; drafts never do. An entity with **Include in /llms.txt** off is left out and not counted.

## The AI crawler comment

Every published entity page can carry an HTML comment in the document head, for crawlers that read the raw source and do not parse JSON-LD. It lists:

- `ENTITY`: the entity name (the `name` attribute when set).
- `PROVIDER`: your site name, when set.
- `TYPE`: the content type slug.
- `LAST VERIFIED`: the month and year of the last update, such as `January 2026`.
- Key facts, only those the content has: name, provider, `years_experience`, `rating` with `reviews_count`, and a one-line summary from `description` (up to 200 characters).
- A pointer to your `llms.txt`.

The comment is left out when the entity has **Include in /llms.txt** switched off.

## The knowledge-graph feed

`GET /api/v1/public/knowledge-graph` returns an `application/ld+json` document with a top-level `@context`, an `@graph` array of entity documents (each without its own `@context`) and `_meta`.

| Parameter | Default | Meaning |
|---|---|---|
| `page` | `1` | Page number. Values below 1 are ignored. |
| `per_page` | `50` | Items per page, 1 to 100. A value outside that range is ignored and 50 is used. |
| `schema` | none | Only entities of this content type slug. |

`_meta` holds `total`, `page`, `per_page`, `total_pages` and `has_next`. The feed honours the same opt-out as `llms.txt`. With `?schema=` the opt-out is applied after paging, so a page can hold fewer items than `per_page` and `total` counts the opted-out entities too.

```bash
curl -s 'https://example.com/api/v1/public/knowledge-graph?schema=post&per_page=20' | head -c 600
```

## Single pages as JSON-LD

Send `Accept: application/ld+json` to an entity page and the server answers with the bare JSON-LD for that entity instead of HTML. See [JSON-LD](json-ld.md#content-negotiation).

## Limits and errors

- When the published entities cannot be listed, `/llms.txt` answers `500` with `failed to build llms.txt`, and the feed answers `500` with `failed to build knowledge graph`.
- When the robots policy cannot be read, `/robots.txt` falls back to the default output; it never fails.
- The MCP tool `llms_txt_preview` returns `/llms.txt` exactly as a crawler gets it now. See [MCP server](mcp.md#tools).
