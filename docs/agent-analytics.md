# Agent analytics (capture, verification, AI referrals, admin UI, robots policy)

`<admin>` stands for the install's private admin address, the path that `hatamoto admin-url` prints. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address.

Core records every hit from a known AI crawler/assistant per request, with
operator, purpose, status and (for pages) the entity it hit. Hits start `pending` and a background job verifies them (below).
AI-referred visits are covered under "AI referral attribution" below; the admin UI, robots.txt policy, plugin hooks and the editor sidebar are under "Admin UI and API (Phase 4)".

## Data flow

```
Go backend (pages, robots.txt, ─► agentCaptureMiddleware ─► agents.Writer ─┬─ hatamoto_agent_hits   (monthly partitions)
 sitemaps, llms.txt) in-process     (non-blocking, batched)               └─ hatamoto_agent_daily (rollup)
```

The Go server renders the whole public site, so every request already carries
the visitor's real User-Agent and is recorded in-process. (There used to be a
`POST /api/v1/public/agent-hits` endpoint for the Next middleware; it is gone.)

- **Registry**: `backend/internal/agents/registry.json` is the single source of
  truth (UA patterns, operator, purpose, IP-range sources). Go loads it at start.
- **Legacy table removed**: migration 014 drops `hatamoto_bot_visits` (its aggregates were copied into `hatamoto_agent_daily` by 010; the table was no longer written). The `/bot-visits*` routes and `/track-visit` alias are gone.
- **Status**: the real response status, recorded after the handler ran.

## Capture matrix (no double counting)

| Path | Source |
|---|---|
| Go-rendered site: pages, `/robots.txt`, root `/sitemap*.xml`, `/llms.txt` | Go backend in-process (`agentCaptureMiddleware`, `Source=backend`, real status) |
| `/media/*` | access-log source only (optional, off by default; see "Access-log ingest") |

## Configuration

| Var | Meaning |
|---|---|
| `HATAMOTO_WORKER=off` | Opt-out of the in-process worker (default on; only for multi-replica setups where one replica runs it). With it off nothing truncates IPs or drops old partitions (the server still creates the current and next month's partition at startup; see Retention). `WORKER_DISABLE_JOBS` can disable `agent_hits_partition` / `agent_hits_maintenance` / `agent_ranges_refresh` / `agent_verify`. |

## Verification (Phase 2)

A user agent string is trivially forged, so each hit is checked against the
operator's published method and stored in `hatamoto_agent_hits.verification`:

| State | Meaning |
|---|---|
| `pending` | Not checked yet, or a DNS lookup failed transiently (retried every run). Default on insert. |
| `verified` | IP is inside the operator's published ranges, or forward-confirmed reverse DNS matches (PTR ends in an operator suffix **and** resolves back to the IP). |
| `spoofed` | Claims the agent but the IP fails the operator's check (outside loaded ranges, or PTR missing/mismatched). |
| `unverifiable` | Nothing to check against: the agent publishes no ranges or rDNS (e.g. MistralAI-User), the hit has no IP, the operator's ranges could never be downloaded, or the hit stayed `pending` for 24h. |

`pending` is never terminal: hits still undecided after 24h become
`unverifiable`, so the 30-day IP truncation is safe. Rules are in
`internal/agents/verify.go`; the per-agent ranges and rDNS suffixes live in
`registry.json` (`ip_range_sources`, `ip_ranges`, `rdns`).

**Range sources** are the operators' own JSON lists (Google, Bing, OpenAI,
Perplexity, Anthropic, CCBot, DuckDuckGo, Apple, Ahrefs; see
`ip_range_sources`). The parser accepts the googlebot `{"prefixes":[...]}`
shape, `{"ips":[...]}` and plain string arrays. Lists are downloaded by the
`agent_ranges_refresh` job only (never at request time; 20s timeout, 4 MiB cap)
and persisted in `hatamoto_agent_ip_ranges` (migration 011), so a restart does
not forget them.

**Failure semantics.** A failed download keeps that key's previous ranges and
is logged; other keys still refresh; the job never fails and never touches
stored hits. Verification waits until ranges are loaded from the database or a
refresh has been attempted, so a cold start cannot mislabel hits. If the lists
can never be fetched, range-only agents end up `unverifiable` (not `spoofed`).
Verdicts only move rollup counters (`verified_hits`, `spoofed_hits`,
`unverifiable_hits` in `hatamoto_agent_daily`) for hits that were still
pending, in the same SQL statement that flips the hits, so they move once.
Hits older than 7 days are not looked at.

**Jobs** (run in the in-process worker; disable individually with
`WORKER_DISABLE_JOBS`):

| Job | Schedule |
|---|---|
| `agent_ranges_refresh` | first run ~90s after start (so ranges exist), then daily |
| `agent_verify` | every 5 minutes, up to 500 distinct (agent, IP) pairs per run, oldest first |

With `HATAMOTO_WORKER=off`, hits stay `pending` (capture itself is unaffected).

## Retention

- Client IP truncated to /24 (IPv4) or /48 (IPv6) after **30 days**
  (`agent_hits_maintenance`, daily, batched).
- Partitions: 2 months ahead, 3 kept → raw hits live ~90 to 120 days
  (`agent_hits_partition`, daily). The daily rollup is kept.
- Startup safety net: regardless of the worker, the server synchronously
  runs `CREATE TABLE IF NOT EXISTS` for the current and next month's
  `hatamoto_agent_hits` partitions before the writer accepts hits, so hits never
  land in the DEFAULT partition (which is never retention-dropped or
  IP-truncated). Failures are logged, not fatal. It also logs a WARN if
  `hatamoto_agent_hits_default` already holds rows; move them into monthly
  partitions by hand. Restart at least monthly (or enable the worker) on
  deployments with `HATAMOTO_WORKER=off`, since the safety net only runs at boot.

## Hot path and drops

`Writer.Record` never blocks a request: a bounded channel, non-blocking send,
a drop counter (`Writer.Stats().Dropped`) and a warning log at most once per
30s. Shutdown drains buffered hits for up to 5s. The counter is surfaced in the
admin overview in a later phase.

## P1 ship checklist (ops, done by the ship stage)

1. Confirm the worker is not disabled (`HATAMOTO_WORKER` is not `off`).
2. Deploy the binary (`scripts/ship.sh`; migrations 009/010 run, legacy rows are backfilled).
3. Smoke: `curl -A GPTBot https://<host>/sitemap.xml /llms.txt /robots.txt
   <entity page>`, wait ~3s, then
   `SELECT path, purpose, status, entity_id FROM hatamoto_agent_hits ORDER BY occurred_at DESC LIMIT 4;`
4. Plugin note: the `bot.visit` hook is removed (it carried raw IPs); its
   replacement `agent.hit` ships in Phase 4 (SDK 0.2.1).

## AI referral attribution (Phase 3)

A human who clicks a link inside ChatGPT, Perplexity, Claude, Gemini, Copilot,
etc. is counted in the daily rollup `hatamoto_analytics_daily` (`kind = 'ai_referral'`,
`key` = assistant, e.g. `chatgpt`). Nothing about the visitor is stored. The same
table holds the daily pageview total (`kind = 'pageview'`), fed by one cookie-less `POST /api/v1/track` beacon per page load (sent by `hm.js` on the Go-rendered pages). Counts are buffered in memory and flushed to the table about every 30 seconds and on shutdown; crawlers are ignored.

```
Go page render ──► aiReferralMiddleware ──► classify (registry) ──► analytics.RecordAIReferral
 (HTML GET, 200)    skips bots/non-HTML      ai_referrers            hatamoto_analytics_daily
```

- **Where**: `aiReferralMiddleware` wraps every Go-rendered public route.
  After the handler ran, a successful (200) `GET` that returned `text/html`
  from a non-bot user agent is classified by Referer host or `utm_source` and
  counted in the same in-memory buffer as pageviews. Sitemaps, feeds, robots,
  404s and HEAD requests are never counted. (The old `POST /public/ai-visits`
  endpoint and its `BOT_TRACK_SECRET` are gone.)
- **Classification** is the backend's job (`Registry.ClassifyReferral`, from
  `registry.json` `ai_referrers`): the Referer host equals or is a subdomain of
  a listed host (`gemini.google.com` never matches `www.google.com`,
  `evilchatgpt.com` never matches `chatgpt.com`), or `utm_source`
  (lowercased) is in the source's `utm` list. `utm_source` wins over the
  Referer. Add new assistants by editing `ai_referrers`.
- **No dedupe**: each qualifying page load counts once; a reload from the
  same AI link counts again. Navigation inside the site carries the site's own
  Referer and is not counted.
- **Privacy**: only the assistant name and the day are stored. The landing
  path, referrer, IP and user agent are discarded.

### Ops

1. Smoke: `curl -s -o /dev/null -H 'Referer: https://chatgpt.com/' -H 'User-Agent: Mozilla/5.0' http://localhost:8090/`,
   wait ~3s, then
   `SELECT day, kind, key, count FROM hatamoto_analytics_daily ORDER BY day DESC LIMIT 1;`
   should return `ai_referral | chatgpt`.

## Admin UI and API (Phase 4)

**Admin > Analytics > AI Agents** (`<admin>/analytics/agents`) has tabs:
Overview (AI fetches -> AI-referred visits, time series by
operator or purpose, top agents), Pages (most fetched, and published entities
never fetched by an AI agent), Agents (per-agent detail), Errors (agent fetches
with status >= 400, with a "create redirect" action for 404s), Spoofed, and
Robots policy. The old Settings > Bot Visits page redirects here; its nav item
is gone.

### Admin API

All under `/api/v1/agent-analytics`, authenticated admin group (any signed-in
admin) unless noted. Errors are `{"error": "..."}`; arrays are never `null`; no
response contains an IP (the spoof log returns `ip_masked`, the /24 or /48
network). Date range: `from`/`to` as `YYYY-MM-DD` (UTC, inclusive, default last
30 days, max span 366 days); paged endpoints take `page` and `per_page`
(default 50, max 200). "AI" means the purposes training, search_index,
user_fetch and other_ai. Counts come from the daily rollup, so they are complete
for any range even though raw hits live ~90 days. The response shapes are pinned by
the handlers in `backend/internal/agents/admin_handlers.go`.

| Route | Returns |
|---|---|
| `GET /overview?group=operator\|purpose` | totals (fetches, verification split, `verified_percent`, AI-referred visits, pageviews), daily series, top agents, `dropped_hits` since process start |
| `GET /pages` | `top` fetched paths (served, status < 400) and `never_fetched` published entities (ignores the date range) |
| `GET /agents`, `GET /agents/{id}` | every agent seen in range (any purpose); detail with series, top paths, status split |
| `GET /errors` | status >= 400 grouped by (path, status) |
| `GET /spoofed` | newest spoofed raw hits with `ip_masked` |
| `GET /entities/{id}` | editor card data: time to first crawl after publish, `user_fetch_7d/30d`, last fetch per AI agent (`last_fetch_at: null` = never) |
| `GET /robots-policy` | one row per purpose (fixed order) with `action`, affected `tokens`, `ignores_robots_agents` |
| `PUT /robots-policy` | **owner role only** (403 otherwise); full replace |

### robots.txt policy

- The policy is per **purpose** (Training, Search index, User fetch, Other AI,
  Search, Social, SEO, Archive); each purpose maps to the robots tokens of the
  registry agents with that purpose.
- **Only `deny` emits groups.** For each denied token the route adds
  `User-agent: <token>` / `Disallow: /` after the default rules and before the
  `Sitemap:` line. Allow emits nothing.
- **Nothing is seeded.** With no saved policy every purpose is allow and the
  output is byte-identical to the pre-policy robots.txt, and the same holds if
  the policy store errors. The default rules are never modified, and the UI has
  no "recommended" or preset buttons.
- `PUT` stores only the deny rows (allow or omitted purposes are cleared);
  400 for an unknown purpose, an action other than allow/deny, or a duplicate.
- robots.txt is advisory. Agents flagged `ignores_robots` in the registry get a
  "May ignore robots.txt" badge; blocking them there does not stop them.
- robots.txt is cached for 5 minutes by `Cache-Control`, so a saved change can
  take a few minutes to show.

### Plugin hooks

Both are async actions in `backend/pkg/sdk` (SDK 0.2.1) and carry no IP or user agent.

- `agent.hit` (`AgentHitEvent`): one per stored hit: `AgentID`, `Operator`,
  `Purpose`, `Verification` (normally `pending`), `Method`, `Path`, `EntityID`
  (empty off entity pages), `Source`, `Status` (0 unknown), `Bytes`,
  `DurationMS`, `OccurredAt`.
- `agent.first_crawl` (`AgentFirstCrawlEvent`): once per entity, on its first
  served (status 200) AI fetch: `EntityID`, `AgentID`, `Operator`, `Purpose`,
  `FirstAt`, `PublishedAt` (nil when the entity has no publish time).

### Editor sidebar

The V4 entity editors have a collapsible right sidebar (280px, or a 36px rail;
the choice is kept in `localStorage` under `hatamoto.editor.rightSidebar`). Its
AI Agents card, backed by `GET /entities/{id}`, shows the time from publish to
first AI crawl, user-fetch counts for 7 and 30 days, and the last fetch and
status per AI agent ("never" when none).

## Access-log ingest (Phase 5, optional, default OFF)

Media files are not page requests, so the
only way to see them is a web-server or CDN access log. Core can ingest one,
but **nothing is enabled by default**: with the flag unset the endpoint does
not exist (404) and no file is read.

| Var | Meaning |
|---|---|
| `AGENT_LOG_INGEST` | `true` turns log ingest on (default off). |
| `AGENT_LOG_INGEST_SECRET` | Secret for `POST /api/v1/public/agent-logs` (header `X-Agent-Log-Secret`). Never derived from another secret. Flag on with an empty secret: the route stays unmounted and a startup warning is logged. |
| `AGENT_LOG_CADDY_PATH` | Optional. With the flag on, the backend tails this Caddy JSON access log (read-only). The file does not have to exist at boot. |

**Dedup policy (what a log may contribute).** To avoid double counting against
the other capture paths, only `/media/*` requests (any status) are stored from
a log, with `source = 'log'`: media is the one surface no other source sees.
Every other line, 5xx included, is skipped (counted as `skipped`) because the
backend (pages, sitemaps, `llms.txt`, robots.txt) already records those requests, failures too. Ingesting 5xx from logs is a v2
item that needs reconciliation with those sources. Query strings are stripped
and the logged status is stored as-is (the path resolver is not used). Unknown
user agents are ignored like everywhere else. Lines whose timestamp is more
than 7 days old or more than 5 minutes in the future are skipped (hit rows are
never retention-dropped). Caddy `ts` may be float seconds (default) or a string
(RFC3339 or numeric) when `time_format` is customised.

**Endpoint.** `POST /api/v1/public/agent-logs` takes NDJSON, one record per
line, in either Cloudflare Logpush `http_requests` shape (`ClientIP`,
`ClientRequestURI`, `ClientRequestMethod`, `ClientRequestUserAgent`,
`EdgeResponseStatus`, `EdgeResponseBytes`, `EdgeStartTimestamp`) or Caddy JSON
shape (`ts`, `status`, `size`, `duration` in seconds, `request.client_ip` or
`request.remote_ip`, `request.method`, `request.uri`, `request.headers["User-Agent"]`);
the shape is detected per line. `Content-Encoding: gzip` is accepted (Logpush
default). Limits: 1 MiB per line, 8 MiB per body (decompressed), 1000 lines per
request; a body over any limit gets **413 and nothing is recorded**. Success is
`202 {"accepted": n, "skipped": m, "dropped": d}`, where `dropped` counts kept
hits the writer queue refused. If it refused **all** of them the answer is
`503` with `Retry-After`, so the sender retries the batch. A wrong or missing
secret is 401. The route has its own per-IP rate limit.

**Caddy (text only, YOU apply it).** Core never edits or reloads Caddy. To feed
the tailer, add a JSON access log to the site block yourself, for example:

```
hatamoto.dev {
    log {
        output file /var/log/caddy/hatamoto.access.log
        format json
    }
    # ... existing config ...
}
```

and set `AGENT_LOG_INGEST=true` plus `AGENT_LOG_CADDY_PATH=/var/log/caddy/hatamoto.access.log`
in the API environment (the API user needs read access to the file).
The tailer starts at the end of the file, polls every second and follows
rotation (rename or truncation). It keeps **no offset**: lines written while
the backend is down are not ingested.

**Cloudflare Logpush.** Create an HTTP destination job for the zone's
`http_requests` dataset and select at least the seven fields above. Settings
that must match the endpoint:

- `max_upload_records=1000`: equal to the 1000-line per-request cap. Logpush's
  default (100,000 records) would produce batches the endpoint rejects with
  413 on every retry.
- The secret goes in the `destination_conf` URL as a header parameter:
  `https://<host>/api/v1/public/agent-logs?header_X-Agent-Log-Secret=<secret>`
  (Logpush sends `header_<Name>` query parameters as request headers).
- gzip on (the default for HTTP destinations, accepted via `Content-Encoding: gzip`).

**Client IP caveat.** Verification (Phase 2) trusts the IP stored with a hit.
The server uses the rightmost untrusted `X-Forwarded-For` entry. Behind
Cloudflare that entry is a Cloudflare edge address and real crawlers would look
`spoofed`; set `HATAMOTO_TRUSTED_CDN=cloudflare` so the visitor is read from
`CF-Connecting-IP` instead (only when the hop is a Cloudflare edge address; the
ranges are refreshed daily from cloudflare.com, with a built-in fallback).
Caddy log lines are handled the same way: when the logged address is an edge,
the logged `Cf-Connecting-Ip` header is used, and when there is none the hit is
stored with no IP and verifies as `unverifiable`, never as `spoofed`. Logpush's
`ClientIP` is already the original visitor. A truncated IP is a network, so it
is never checked against published ranges and ends as `unverifiable`. A
migration re-verifies the last 7 days of hits that were judged against edge
addresses (truncated ones become `unverifiable`); older untruncated verdicts
stay. The Crawlers page shows a verification banner, and the MCP tool
`analytics_crawler_verification` reports the same numbers.
