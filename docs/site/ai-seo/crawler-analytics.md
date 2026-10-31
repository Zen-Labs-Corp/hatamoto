---
title: Crawler analytics
nav_title: Crawler analytics
description: How AI crawler visits and AI assistant referrals are recorded, checked, kept and reported in the admin, the API, plugins and the command line.
---

This page lists how Hatamoto records requests from AI crawlers and assistants, how it checks that they are genuine, how long it keeps them, and every place you can read the results. To read the reports in the admin, follow [See AI crawler visits](see-ai-crawler-visits.md).

## Overview

Hatamoto records every request from a known AI crawler or assistant, with the operator, the purpose, the response status and, for pages, the entity that was fetched. It also counts human visits that an AI assistant sends you. Nothing leaves your server.

| What | Stored where | Kept |
|---|---|---|
| One row per crawler request | `hatamoto_agent_hits`, monthly partitions | about 90 to 120 days |
| Daily totals per agent | `hatamoto_agent_daily` | indefinitely |
| Daily pageviews and AI-referred visits | `hatamoto_analytics_daily` | indefinitely |

For people, Hatamoto keeps almost nothing: a daily pageview total and, for visits that arrive from an AI assistant, a daily count per assistant. It stores no cookie, no visitor identifier and no landing path for those visits. Crawlers are counted per request, because a crawler is a machine and there is no visitor to protect.

## Data flow

The server renders the whole public site itself, so every request already carries the real `User-Agent`. A middleware records the hit in process after the handler has answered, with the real status, byte count and duration.

```text
pages, robots.txt, sitemaps, llms.txt, feeds
   -> capture middleware -> hit writer -+-> hatamoto_agent_hits   (monthly partitions)
      (non-blocking, batched)           +-> hatamoto_agent_daily  (daily rollup)
```

## The agent registry

The agent registry, `backend/internal/agents/registry.json`, is the single source of truth: user-agent patterns, operator, purpose, `robots.txt` token, the published IP-range sources and the AI referrer list. It is embedded in the program and lists 43 agents. A request whose user agent matches no registry agent is not recorded.

| Purpose | Meaning | Examples |
|---|---|---|
| `training` | Collecting content for model training. | GPTBot, ClaudeBot, CCBot, Google-Extended |
| `search_index` | Building an AI search index. | OAI-SearchBot, Claude-SearchBot, PerplexityBot |
| `user_fetch` | Fetching a page because a user asked an assistant. | ChatGPT-User, Claude-User, Perplexity-User |
| `other_ai` | Other AI-related crawling. | GoogleOther |
| `search` | Classic search engines. | Googlebot, Bingbot |
| `social` | Link previews. | Slackbot, Twitterbot |
| `seo` | SEO tools. | AhrefsBot, SemrushBot |
| `archive` | Web archives. | Internet Archive |

"AI" in the reports means the first four purposes. Some agents, mostly user-triggered fetchers, are flagged as ignoring `robots.txt`, and the admin marks them. The full token list per purpose is in [robots.txt and llms.txt](robots-llms.md#purposes-and-tokens).

## What is captured

| Path | Source |
|---|---|
| Pages, `/robots.txt`, `/sitemap.xml`, `/sitemap-<group>.xml`, `/llms.txt`, `/feed.xml`, `/atom.xml` | Recorded in process by the server, with the real status. |
| `/api/*` and `/media/*` | Not recorded by the server. Media can be added from an access log, see [Access-log ingest](access-log-ingest.md). |

Each source records a path once, so counts are never doubled. A `HEAD` request to a captured route is recorded like a `GET`.

## Verification

Anyone can forge a user-agent string, so a background job checks each hit against the operator's published method and stores the result in `hatamoto_agent_hits.verification`.

| State | Meaning |
|---|---|
| `pending` | Not checked yet, or a DNS lookup failed and will be retried. The default on insert. |
| `verified` | The IP is inside the operator's published ranges, or forward-confirmed reverse DNS matches: the PTR name ends in an operator suffix and resolves back to the IP. |
| `spoofed` | It claims to be the agent, but the IP fails the operator's check. |
| `unverifiable` | There is nothing to check against: the agent publishes no ranges or reverse DNS, the hit has no IP, the ranges could not be downloaded, or the hit stayed `pending` for 24 hours. |

`pending` is never final. Hits still undecided after 24 hours become `unverifiable`, which keeps the 30-day IP truncation safe. Hits older than 7 days are no longer examined.

IP ranges come from the operators' own JSON lists: Google, Bing, OpenAI (three lists), Perplexity (two lists), Anthropic, CCBot, DuckDuckGo, Apple and Ahrefs. They are downloaded only by a background job, never at request time, with a 20 second timeout and a 4 MiB cap, and stored in the database so a restart does not forget them. A failed download keeps the previous ranges. If a list can never be fetched, the agents that depend on it end up `unverifiable`, not `spoofed`.

The IP of a hit is the client IP the server sees: the rightmost untrusted `X-Forwarded-For` entry. Behind Cloudflare that would be the edge address, so set `HATAMOTO_TRUSTED_CDN=cloudflare` to read the visitor from `CF-Connecting-IP` instead (see [Trusted CDN](../operate/configuration-reference.md#trusted-cdn) and [Access-log ingest](access-log-ingest.md#the-client-ip-caveat)). A truncated IP identifies only a network, so it is never checked against ranges and is marked `unverifiable`, not `spoofed`.

An upgrade runs a one-time migration over the hits of the last 7 days that were marked `spoofed`: they go back to `pending` and are judged again with the address now in use. Older verdicts cannot be re-checked and stay, and so do truncated ones.

### Verification health

The top of **Crawlers** shows a banner once at least 20 fetches from agents with published IP ranges were decided (`verified` or `spoofed`) in the selected range. It is green, **Verification is healthy**, when at most 20% of them were spoofed, and red, **Verification looks misconfigured**, above that. It says which header client IPs are read from, how many distinct IPs the spoofed fetches came from, and links to the details on **Health**. A red banner that mostly names Cloudflare addresses tells you to set `HATAMOTO_TRUSTED_CDN=cloudflare`. After you do, the banner stays red until the fetches recorded earlier leave the selected date range. With fewer than 20 decided fetches there is no banner.

AI agents get the same numbers from the MCP tool `analytics_crawler_verification` (date range in; level `ok`, `bad` or `unknown`, verified, spoofed and unverifiable counts, `spoofed_share`, distinct spoofed IPs (`spoofed_ips`) and the header in use out).

## Background jobs and retention

The jobs run in the in-process worker. Turn one off with the comma-separated environment variable `WORKER_DISABLE_JOBS`, using these names:

| Job | Schedule | Purpose |
|---|---|---|
| `agent_ranges_refresh` | About 90 seconds after start, then daily | Download operator IP ranges. |
| `agent_verify` | About 2 minutes after start, then every 5 minutes | Verify up to 500 distinct (agent, IP) pairs per run, oldest first. |
| `agent_hits_maintenance` | Daily | Truncate client IPs older than 30 days. |
| `agent_hits_partition` | Daily | Keep monthly partitions ahead and drop old ones. |

- **IP truncation.** After 30 days a client IP is cut to its `/24` (IPv4) or `/48` (IPv6) network.
- **Raw hits.** Monthly partitions are created two months ahead and three are kept, so raw hits live roughly 90 to 120 days.
- **Rollup.** The daily rollup `hatamoto_agent_daily` is kept, so reports over any range stay complete.
- **Startup safety net.** On every start the server creates the current and next month's partitions before it accepts hits, so nothing lands in the default partition, which is never trimmed. It logs a warning if that default partition already holds rows.

With `HATAMOTO_WORKER=off` (meant for multi-replica setups where one replica runs the worker) hits stay `pending`, IPs are not truncated and old partitions are not dropped. Capture itself keeps working, but restart such a replica at least monthly. See [Multi-replica and health](../operate/multi-replica-and-health.md).

## Never blocking a request

Recording a hit never delays a response. The writer stores hits in batches of up to 100, about every second. It uses a bounded queue of 2000 hits, a non-blocking send, a drop counter and at most one warning per 30 seconds. Shutdown drains buffered hits for up to 5 seconds. The admin overview and the API report the number of dropped hits since the process started.

## AI referral attribution

A person who clicks a link inside an AI assistant is counted in the daily table `hatamoto_analytics_daily`, with `kind = 'ai_referral'` and the assistant as the key, for example `chatgpt`. The same table holds the daily pageview total (`kind = 'pageview'`), fed by one cookie-less `POST /api/v1/track` beacon per page load, sent by the default theme's script.

- **Where.** After a page route has answered, a `GET` that returned `200` with an HTML content type to a user agent that is not a registry agent is classified. Sitemaps, feeds, `robots.txt`, error pages and `HEAD` requests are never counted.
- **Classification.** `utm_source` (lowercased) is checked first, then the host of the `Referer`. A referrer matches when its host equals a listed host or is a subdomain of it, so `gemini.google.com` never matches `www.google.com`, and `evilchatgpt.com` never matches `chatgpt.com`.
- **No dedupe.** Each qualifying page load counts once. Navigation inside your site carries your own referrer and is not counted.
- **Privacy.** Only the assistant name and the day are stored. The landing path, referrer, IP address and user agent are discarded. Counts are held in memory and written about every 30 seconds and on shutdown.

The `ai_referrers` list names nine assistants:

| Key | Referrer hosts | `utm_source` values |
|---|---|---|
| `chatgpt` | `chatgpt.com`, `chat.openai.com` | `chatgpt.com`, `chatgpt` |
| `perplexity` | `perplexity.ai` | `perplexity.ai`, `perplexity` |
| `claude` | `claude.ai` | `claude.ai`, `claude` |
| `gemini` | `gemini.google.com` | `gemini.google.com`, `gemini` |
| `copilot` | `copilot.microsoft.com` | `copilot.microsoft.com`, `copilot` |
| `grok` | `grok.com` | `grok.com`, `grok` |
| `meta_ai` | `meta.ai` | `meta.ai`, `meta_ai` |
| `deepseek` | `chat.deepseek.com` | `chat.deepseek.com`, `deepseek` |
| `mistral` | `chat.mistral.ai` | `chat.mistral.ai`, `mistral` |

To check it locally, with `DATABASE_URL` set:

```bash
curl -s -o /dev/null -H 'Referer: https://chatgpt.com/' -H 'User-Agent: Mozilla/5.0' http://localhost:8090/
sleep 35
psql "$DATABASE_URL" -c "SELECT day, kind, key, count FROM hatamoto_analytics_daily ORDER BY day DESC LIMIT 3;"
```

## The admin screens

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

**Analytics** in the sidebar has six sections: **Overview**, **Content**, **Crawlers** (`<admin>/analytics/crawlers`), **Google** (`<admin>/analytics/google`), **Referrals** (`<admin>/analytics/referrals`) and **Health**. Every view except **Robots policy** has a date range, the last 30 days by default and at most 366 days.

| Crawlers view | Shows |
|---|---|
| **Overview** | AI fetches, the verified share, spoofed fetches, visits from AI assistants and pageviews; fetches over time grouped by operator or purpose; the verification split; the top agents. |
| **Agents** | Every agent seen, with operator, purpose, fetches and verification. One agent's page adds its activity, daily fetches, most fetched pages, response status classes, its `robots.txt` name and a note when it may ignore `robots.txt`. |
| **Pages** | The ten most fetched pages, every page fetched, and published pages no AI agent has fetched. |
| **First crawls** | Pages first fetched in the range, which agent fetched them and how long after publishing. |
| **Errors** | Fetches with status 400 or above, grouped by page and status. A 404 row has a **Create redirect** action. |
| **Spoofed** | Recent hits that failed verification, with the claimed agent and the masked network. |
| **Robots policy** | The policy per purpose. See [robots.txt and llms.txt](robots-llms.md#the-admin-screen). |

### Search engines on the Crawlers section

Besides the AI purposes, the Crawlers section has a **Search engines** group (Googlebot, Bingbot and the other search crawlers seen in the range), with its own Allow and Block control, and the summary row counts them. The hero chart has a toggle: **AI only**, or **AI + search** (the default), which adds one grey series for the search engines. Google's AI features (AI Overviews, Gemini) use the same Googlebot crawl; the `Google-Extended` token only controls training use.

### The Google section

The **Google** section answers "what is Google doing on my site?" from your own server logs.

- **Key figures:** Googlebot crawls and the verified share, pages crawled out of published pages, and the median time from publishing to the first crawl.
- **Crawling chart:** Googlebot fetches per day, stacked by response class (2xx, 3xx, 4xx, 5xx).
- **Pages through Google's eyes:** every published page and every other path Googlebot fetched, with crawls, last crawl and last status. Filter by **All**, **Not crawled** (published pages with no crawl in the range), **Errors** (last status 400 or above) or **Stale** (the page was edited on a later day than its last crawl).
- Spoofed fetches are left out of the page numbers, so a forged user agent cannot make a page look crawled. Dates are whole UTC days: the table shows the day of the last crawl, not the time.
- **First crawl** counts only the first verified, served (below 400) search engine fetch of a page. It is tracked for pages published since tracking began, and the history still held in the raw hit table is back-filled once after the upgrade.
- Impressions, clicks, average position and index status appear only when a search performance integration is connected. Without one, a single notice says so. Nothing is shown as zero in its place.

### Verification

Search engines are verified by the address ranges they publish and, when an address is not in the list, by forward-confirmed reverse DNS (the reverse name ends in the operator's domain and resolves back to the same address). After upgrading, fetches that were marked spoofed only because they arrived from a CDN edge address are relabelled **unverifiable**: from an edge address the real origin cannot be known. Page attribution of past fetches is also repaired in the background after the upgrade; no action is needed.

**Referrals** shows visits from AI assistants, pageviews, the AI share of traffic and visits by assistant. Any signed-in admin can read these screens; only an owner can change the robots policy. The old addresses under `<admin>/analytics/agents`, and `<admin>/analytics` itself, redirect to `<admin>/analytics/crawlers`.

The post editor has an **AI crawlers** card on its **SEO and AEO** tab: assistant fetches of the post in the last 7 and 30 days, when it was first crawled (only for posts published after tracking began), and the last fetch and status per AI agent.

## Admin API

All routes are under `/api/v1/agent-analytics` and need a signed-in admin session. Errors are `{"error": "..."}`, arrays are never `null`, and no response contains an IP address: the spoof log returns `ip_masked`, the `/24` or `/48` network.

Dates are `from` and `to` as `YYYY-MM-DD` in UTC, inclusive, defaulting to the 30 days ending today. A bad date, `from` after `to`, or a span over 366 days is `400`. Paged routes take `page` (at most 10000) and `per_page` (default 50, maximum 200).

| Route | Returns |
|---|---|
| `GET /overview?group=operator` or `group=purpose` | Totals, verification split, AI-referred visits, pageviews, a daily series, top agents and `dropped_hits`. |
| `GET /pages` | Top fetched paths and published entities never fetched. |
| `GET /agents`, `GET /agents/{id}` | Agents seen in the range, and one agent's detail. |
| `GET /errors` | Status 400 and above, grouped by path and status. |
| `GET /spoofed` | The newest spoofed hits with `ip_masked`. |
| `GET /entities/{id}` | The editor card data for one entity. |
| `GET /robots-policy`, `PUT /robots-policy` | Read the policy. Replace it (owner only, otherwise `403`). See [robots.txt and llms.txt](robots-llms.md#saving-the-policy-through-the-api). |

## Plugin hooks

Two asynchronous actions let a plugin react, and neither carries an IP address or user agent. See [Hooks and events](../plugins/hooks-and-events.md).

- `agent.hit` fires for every stored hit with the agent id, operator, purpose, verification (normally `pending`), method, path, entity id, source, status, bytes, duration and time.
- `agent.first_crawl` fires once per entity on its first served (status 200) AI fetch, with the agent, operator, purpose, the time of the fetch and the publish time.

## Command line

Three read-only commands print the same data as JSON. They read the database directly, so they need `DATABASE_URL`.

```bash
hatamoto analytics crawlers --from 2026-01-01 --to 2026-01-31 --limit 20
hatamoto analytics first-crawls --limit 20
hatamoto analytics referrals --from 2026-01-01
```

| Command | Prints |
|---|---|
| `crawlers` | The top pages and the per-agent totals. |
| `first-crawls` | Pages by first AI crawl. |
| `referrals` | Visits per day and assistant. |

| Flag | Default | Meaning |
|---|---|---|
| `--from` | 29 days before `--to` | First day, `YYYY-MM-DD`, UTC. |
| `--to` | today, UTC | Last day, `YYYY-MM-DD`. |
| `--limit` | `20` | Rows, 1 to 100. Not used by `referrals`. |

The full command list is in the [CLI reference](../operate/cli-reference.md). The same data is available to AI agents through the [MCP tools](mcp.md#tools).
