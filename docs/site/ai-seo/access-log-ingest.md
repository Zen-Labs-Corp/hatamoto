---
title: Access-log ingest
nav_title: Access-log ingest
description: Every setting, field, limit and answer of the optional feed that adds AI crawler fetches of your images and files from a web server or CDN log.
---

This page lists the optional way to see AI crawlers fetching your images and documents: Hatamoto reads a Caddy JSON access log or receives Cloudflare Logpush batches. It is for the developer or administrator who sets it up; for the reports it feeds, see [See AI crawler visits](see-ai-crawler-visits.md).

## Overview

Media files are not page requests, and the server does not record `/media/*` as crawler hits (see [Crawler analytics](crawler-analytics.md#what-is-captured)). The only way to see crawlers fetching media is a web server or CDN access log. Nothing is enabled by default: with the flag unset, the endpoint does not exist (it answers `404`) and no file is read.

| Way in | Needs | Reads |
|---|---|---|
| HTTP endpoint `POST /api/v1/public/agent-logs` | `AGENT_LOG_INGEST=true` and a secret | NDJSON batches from Cloudflare Logpush or any sender |
| File tailer | `AGENT_LOG_INGEST=true` and a file path | A local Caddy JSON access log |

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `AGENT_LOG_INGEST` | `false` | Set to `true` to turn log ingest on. Any value Go reads as a boolean works (`true`, `1`, `t`). |
| `AGENT_LOG_INGEST_SECRET` | empty | Secret for `POST /api/v1/public/agent-logs`, sent in the header `X-Agent-Log-Secret`. It is never derived from another secret. |
| `AGENT_LOG_CADDY_PATH` | empty | Optional. With the flag on, Hatamoto tails this Caddy JSON access log, read-only. The file does not have to exist at start. |

How these combine:

- The HTTP endpoint is mounted only when the flag is on **and** the secret is not empty. A flag with no secret logs a startup warning (`AGENT_LOG_INGEST is on but AGENT_LOG_INGEST_SECRET is empty: agent-logs endpoint disabled`) and leaves the route unmounted.
- The file tailer needs the flag and a path, and does not use the secret. It reads a local file, so there is nothing to authenticate.

See the [configuration reference](../operate/configuration-reference.md) for the full variable list.

## What a log may contribute

To avoid counting a request twice, a log contributes very little. Only requests whose path starts with `/media/` are stored, at any status, with `source = 'log'`. Media is the one surface no other capture path sees. Every other line is skipped and counted as `skipped`, including 5xx answers on pages, because the server already records pages, sitemaps, `llms.txt` and `robots.txt`, failures included.

Other rules applied to every line:

- Query strings and fragments are removed from the path, and the logged status is stored as it is.
- A line with a user agent that is not in the agent registry is skipped, like everywhere else in analytics.
- A line whose timestamp is more than 7 days old, or more than 5 minutes in the future, is skipped. This keeps a wrong clock from polluting the data.
- A line with no user agent, no path, or a status outside 100 to 599 is skipped.

The stored hit then goes through the same verification and retention as any other. See [Crawler analytics](crawler-analytics.md#verification).

## The HTTP endpoint

`POST /api/v1/public/agent-logs` takes NDJSON: one JSON record per line. Blank lines are ignored. The shape is detected per line, so a body can mix both.

| Source | Fields read |
|---|---|
| Cloudflare Logpush, dataset `http_requests` | `ClientIP`, `ClientRequestURI`, `ClientRequestMethod`, `ClientRequestUserAgent`, `EdgeResponseStatus`, `EdgeResponseBytes`, `EdgeStartTimestamp` |
| Caddy JSON | `ts`, `status`, `size`, `duration` (seconds), `request.client_ip` or `request.remote_ip`, `request.method`, `request.uri`, `request.headers["User-Agent"]` |

Caddy's `ts` may be float seconds (the default) or a string, either RFC 3339 or numeric, when you have customised `time_format`. A request with `Content-Encoding: gzip` is accepted, which is the Logpush default.

### Limits

| Limit | Value |
|---|---|
| One line | 1 MiB |
| One body, after decompression | 8 MiB |
| Lines per request | 1000 |
| Rate | 2 requests per second per IP, with a burst of 10 |

The size limits are all or nothing on purpose, so that a sender notices instead of silently losing the tail of a batch.

### Answers

| Answer | When |
|---|---|
| `202` with `{"accepted": n, "skipped": m, "dropped": d}` | The body was read. `dropped` counts kept hits that the writer's queue refused. |
| `401 unauthorized` | The secret is wrong or missing. |
| `400 invalid gzip body` or `400 invalid body` | The body is not valid gzip or cannot be read. |
| `413 too many lines` or `413 payload too large` | A line, the body or the line count is over its limit. Nothing is recorded. |
| `503 writer queue full` with `Retry-After: 30` | The queue refused every kept hit, so the sender should retry the whole batch. |
| `404` | Ingest is off, or on without a secret. |

An example with one Caddy line. Use a current timestamp when you try it, because anything older than 7 days is skipped.

```bash
printf '%s\n' '{"ts":1768400000.1,"status":200,"size":48211,"duration":0.004,"request":{"client_ip":"203.0.113.9","method":"GET","uri":"/media/2026/logo.png","headers":{"User-Agent":["GPTBot/1.1"]}}}' \
  | gzip \
  | curl -s -X POST https://example.com/api/v1/public/agent-logs \
      -H "X-Agent-Log-Secret: $AGENT_LOG_INGEST_SECRET" \
      -H 'Content-Encoding: gzip' \
      --data-binary @-
```

## Tailing a Caddy log

Hatamoto never edits or reloads your Caddy configuration. Add a JSON access log to the site block yourself, for example:

```text
example.com {
    log {
        output file /var/log/caddy/example.access.log
        format json
    }
    reverse_proxy 127.0.0.1:8090
}
```

Then set the variables for the Hatamoto process and make sure its user can read the file:

```bash
AGENT_LOG_INGEST=true
AGENT_LOG_CADDY_PATH=/var/log/caddy/example.access.log
```

The tailer starts at the end of the file, polls every second, and follows rotation by rename or truncation. It keeps no offset, so lines written while Hatamoto is down are not read. Add the secret too if you also want the HTTP endpoint. See [Reverse proxy and TLS](../operate/reverse-proxy-tls.md) for a complete Caddy setup.

## Cloudflare Logpush

Create an HTTP destination job for the zone's `http_requests` dataset and select at least the seven fields in the table above. Three settings must match the endpoint:

- `max_upload_records=1000`. This equals the per-request line cap. Logpush's default of 100,000 records would produce batches that the endpoint rejects with `413` on every retry.
- The secret goes in the destination URL as a header parameter, which Logpush sends as a request header, as in the example below.
- gzip stays on, which is the default for HTTP destinations.

```text
https://example.com/api/v1/public/agent-logs?header_X-Agent-Log-Secret=<secret>
```

## The client IP caveat

Verification trusts the IP stored with a hit. The server takes the rightmost untrusted `X-Forwarded-For` entry. If a CDN or proxy sits in front of the site, that entry is the CDN's address, and real crawlers would look `spoofed`. The log is the fix: it carries its own client address, which is Caddy's `client_ip` when you have set `trusted_proxies`, or Logpush's `ClientIP`, which is the original visitor.
