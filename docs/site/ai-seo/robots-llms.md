---
title: robots.txt and llms.txt
nav_title: robots and llms.txt
description: The exact robots.txt and llms.txt your site serves, the crawler policy per purpose, the per-page opt-outs and the knowledge-graph feed.
---

This page lists everything Hatamoto writes into `robots.txt` and `llms.txt`, the crawler policy behind them, the per-page switches that remove a page, and the JSON-LD feed that `llms.txt` points to. To choose a policy in the admin, follow [Control AI crawlers](control-ai-crawlers.md).

## Overview

Hatamoto generates these files from live data. You never edit them by hand.

| File or feed | Address | Built from | `Cache-Control` |
|---|---|---|---|
| `robots.txt` | `/robots.txt` | Fixed default rules plus the saved robots policy | `public, max-age=300, s-maxage=300` |
| `llms.txt` | `/llms.txt` | A summary of the site and its listed pages, rebuilt on change | `public, max-age=300, s-maxage=300` |
| `llms-full.txt` | `/llms-full.txt` | The Markdown text of every listed page, rebuilt on change | `public, max-age=300, s-maxage=300` |
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

## ads.txt and app-ads.txt

Ad networks such as Google AdSense read `/ads.txt` to see who may sell your ad space. `/app-ads.txt` does the same for mobile apps. You write both by hand in Settings > SEO > ads.txt (owners only). One file serves the whole site, for every language.

Put one record on each line: the ad system's domain, your account id, `DIRECT` or `RESELLER`, and an optional certification id. Comments start with `#`. The lines `CONTACT=`, `SUBDOMAIN=`, `INVENTORYPARTNERDOMAIN=`, `OWNERDOMAIN=` and `MANAGERDOMAIN=` are also allowed.

```text
# Our AdSense account
google.com, pub-1234567890123456, DIRECT, f08c47fec0942fa0
CONTACT=ads@example.com
```

- **Errors.** Hatamoto checks every line when you save and names the first bad one, for example "Line 2: the account type must be DIRECT or RESELLER." Nothing is saved until the text is valid. A file can have up to 5,000 lines and 256 KiB.
- **Empty means 404.** While a file is empty, its address answers a plain 404, not your themed 404 page.
- **Changes take up to a minute** to reach visitors, because the file may be cached for 60 seconds.
- **Check it.** Run `curl -i https://example.com/ads.txt` and look for `200` and `text/plain`.
- **AdSense shortcut.** After you save an AdSense id in Settings > Privacy & tracking > Tracking, the "Add the ads.txt line" link opens this page with the AdSense line added to the box. Press Save to keep it.

## Per-page controls

| Control | Stored as | Default | Effect |
|---|---|---|---|
| **Ask search engines not to list this page** (generic editor, **Advanced** tab of the **SEO** panel) or **this post** (post editor, **Advanced** tab of the **SEO and AEO** panel) | `attributes.no_index` | off | Left out of the sitemap, site search and the RSS and Atom feeds. The page head carries `noindex,follow`. |
| **Include in /llms.txt** (post editor, **SEO and AEO** tab) | `attributes.seo.include_in_llms_txt` | on | Off removes the entity from `llms.txt`, from the knowledge-graph feed and from the AI crawler comment. |

Other values have the same no-index effect: `attributes.seo.robots` containing `noindex`, or `metadata.no_index` set to true. The page head follows the two attribute values; the sitemap, search and feed listings also follow `metadata.no_index`. Switching **Ask search engines not to list** off also removes `noindex` from `attributes.seo.robots`, keeping its other directives.

The **Include in /llms.txt** switch exists only in the post editor. For other content types, set `attributes.seo.include_in_llms_txt` to `false` through the API or the MCP tool `entities_update`. A missing or unreadable value counts as included.

Pages shown in preview always carry `noindex,follow`, so a draft preview cannot be indexed by accident.

## llms.txt

`/llms.txt` follows the [llmstxt.org](https://llmstxt.org) shape: a short Markdown summary of the site for language models. It contains, in order:

1. A title: the company name from site settings, then the business profile name, then the host name.
2. A summary line: the **Summary override** from the AI files tab, else the site tagline, else the business profile description. Left out when all three are empty.
3. A details paragraph from the business profile: location, contact and opening hours. Left out when the profile has none.
4. One list per section, each line a link with a one-line description (the description field, else the first sentence of the page text). Empty sections are left out.
5. An **Optional** list: the knowledge-graph feed, the sitemap, the posts feed at `/feed.xml` when the site has a posts type, and `llms-full.txt` when that file is on.

The sections, in order:

| Section | What it lists |
|---|---|
| **Key pages** | The home page, then the top-level pages, in the order of the header menu (pages the menu does not link come after, by title). |
| **Pages** | Pages that sit under another page, by title. |
| One section per other content type | Entries of that type, by title. The heading is the type's archive title, else the type name. |
| **Posts** | The newest posts, newest first. The count is **Posts listed in llms.txt** (20 by default, at most 200). Older posts appear only in `llms-full.txt`. |
| **People** | People marked public, by name, with their job title. |

A trimmed example for a fictional site with two public locales:

```text
# Acme Studio

> Acme Studio builds websites for small firms in Lisbon.

Based in Lisbon, Portugal.
hello@acme.example, +351 21 000 0000.
Open Mon-Fri 9:00-18:00.

## Key pages
- [Home](https://acme.example/): Websites for small firms.
- [Services](https://acme.example/services): What we build.

## Services
- [Online shops](https://acme.example/service/online-shops): Shops with simple checkout.
- [Web design](https://acme.example/service/web-design): Sites that load fast.

## Posts
- [Shop checklist](https://acme.example/blog/shop-checklist): Ten things to check before opening a shop.

## People
- [Elena Voss](https://acme.example/author/elena-voss): Lead designer

## Key pages (Français)
- [Accueil](https://acme.example/fr/accueil): Des sites web pour les petites entreprises.

## Optional
- [Knowledge graph](https://acme.example/api/v1/public/knowledge-graph): JSON-LD
- [Sitemap](https://acme.example/sitemap.xml)
- [Posts feed](https://acme.example/feed.xml)
- [Full text](https://acme.example/llms-full.txt): older posts and every page
```

### Locales

There is one `/llms.txt`, not one per locale. The default locale's sections come first with plain headings. Each other public locale follows with the same sections, the locale's name added to the heading, such as `## Posts (Français)`. Each locale lists its own newest posts.

### What is left out

`llms.txt` and `llms-full.txt` list the same pages. A page is left out when it:

- has **Include in /llms.txt** off;
- is set to no-index, or its content type is hidden from search engines (see [Per-page controls](#per-page-controls));
- is a draft or otherwise not published;
- is a person who is not marked public;
- is anything else the [sitemap](sitemaps.md) leaves out, such as a page with its own **hide from sitemap** setting.

Hatamoto has no password-protected content feature, so there is nothing of that kind to exclude. While the whole site is in coming soon, maintenance or password mode, the files follow the sitemap: see [Settings, users and email](../admin/settings-users-email.md).

### Freshness

The built files are kept in the render cache. Any content or settings change clears it, and the next request builds the files again. Answers carry `Cache-Control: public, max-age=300, s-maxage=300`, so a browser or shared cache may show the previous file for up to five minutes. **Recently changed** and **Last updated** on the AI files tab update when a file is next built, that is on its next fetch after a change.

## llms-full.txt

`/llms-full.txt` holds the text of every page `llms.txt` lists, including all posts, not only the newest. It starts with `# <site name>: full text` and the summary, then one block per page in the same order as `llms.txt`:

```text
# Web design

URL: https://acme.example/service/web-design
Updated: 2026-03-11

Sites that load fast. ...

---
```

The text is Markdown made from the page content, never the themed HTML. A page built with the page builder is turned into headings, paragraphs and lists; a person's text is their bio.

The file stops at a size cap, 2 MB by default. A block is never cut in half: the first page that would pass the cap and every page after it are left out, and the file ends with a line such as:

```text
This file was truncated at 2048 KB. 14 more pages are left out; see https://acme.example/sitemap.xml for the full list.
```

The cap is the `full_cap_kb` field of the `ai_files` section of the SEO API (64 to 20480, in KB). The AI files tab has no field for it.

## Files for AI tools

Settings > SEO > **AI files** (owners only) is the card **Files for AI tools**:

| Control | Effect |
|---|---|
| **Publish llms.txt** | On by default. Off answers `/llms.txt` with 404 and removes it from the static export. |
| **Publish llms-full.txt** | On by default. Off answers `/llms-full.txt` with 404, removes it from the static export and from the Optional list of `llms.txt`. |
| **Summary override** | Up to 300 characters. Empty uses the site tagline, then the business profile description. |
| **Posts listed in llms.txt** | The newest posts per locale, 1 to 200, 20 by default. |

The card also shows a **Files** table with each file's last update time (and the size of `llms-full.txt`) and **Recently changed**, the five newest changes to pages, people and the summary. The same settings are the `ai_files` section of `GET|PATCH /api/v1/settings/seo`: `llms_off`, `full_off`, `summary`, `posts` and `full_cap_kb`. See [MCP server](mcp.md#tools).

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
| `page` | `1` | Page number, 1 or more. Anything else is answered with `400`. |
| `per_page` | `50` | Items per page, 1 to 100. A larger number is clamped to 100; zero, a negative number or text is answered with `400`. |
| `schema` | none | Only entities of this content type slug. |

`_meta` holds `total`, `page`, `per_page`, `total_pages` and `has_next`. The feed honours the same opt-out as `llms.txt`. With `?schema=` the opt-out is applied after paging, so a page can hold fewer items than `per_page` and `total` counts the opted-out entities too. The same **Include in /llms.txt** opt-out and the same noindex also hide an entry from the [public content API](../admin/public-content-api.md).

```bash
curl -s 'https://example.com/api/v1/public/knowledge-graph?schema=post&per_page=20' | head -c 600
```

## Single pages as JSON-LD

Send `Accept: application/ld+json` to an entity page and the server answers with the bare JSON-LD for that entity instead of HTML. See [JSON-LD](json-ld.md#content-negotiation).

## Limits and errors

- When the published entities cannot be listed, `/llms.txt` answers `500` with `failed to build llms.txt`, `/llms-full.txt` answers `500` with `failed to build llms-full.txt`, and the feed answers `500` with `failed to build knowledge graph`.
- When the robots policy cannot be read, `/robots.txt` falls back to the default output; it never fails.
- A file switched off in the AI files tab answers `404`.
- The MCP tool `llms_txt_preview` returns `/llms.txt` exactly as a crawler gets it now, in the shape described above. See [MCP server](mcp.md#tools).
