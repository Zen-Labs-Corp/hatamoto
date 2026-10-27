---
title: Multiple replicas and health checks
nav_title: Replicas and health
description: What is shared and what is per process when several Hatamoto processes use one database, the worker switches and the health endpoints.
---

This page lists how Hatamoto behaves when more than one process, or replica, serves the same site from one Postgres database, the switches that control the background worker, and the health endpoints a load balancer or container runtime can probe. One process is enough for most sites, because pages are built on the server and cached in memory. To set up a single instance first, follow [Deploy with Docker](deploy-docker.md) or [Deploy with systemd](deploy-systemd.md).

## What is shared and what is per process

| Concern | Behaviour with several replicas |
|---|---|
| Content, settings, users, sessions | All in Postgres, so every replica sees the same data. Admin sessions are stored in the database, so a sign-in works on any replica. |
| Migrations | Applied at boot under a Postgres session-level advisory lock. Replicas that boot together migrate one at a time, and the second finds nothing to do. `hatamoto migrate` next to a running `serve` is safe for the same reason. |
| First-boot seeding | Idempotent (`ON CONFLICT DO NOTHING`), so two replicas starting on an empty database do not race. |
| Installed themes and plugins | Stored in Postgres. A package installed on one replica, or by the CLI, is picked up by the others within about five seconds in production. |
| Update settings | One row in the database, shared. Each replica still replaces its own binaries. |
| Uploaded media | Files on disk under `MEDIA_UPLOAD_DIR`. All replicas need to see the same directory (a shared volume), or uploads made on one are missing on the others. |
| HTML page cache | Per process. See below. |
| Rate limiters | Per process (in memory), so effective limits scale with the number of replicas. |
| Background worker | In process; decide which replica runs it. See below. |

## The page cache

Rendered public pages are cached in memory for `HATAMOTO_PAGE_CACHE_TTL` (default `5m`, between `30s` and `720h`). A content, menu, settings, theme or plugin change clears the cache in the process that handled it. Page and menu writes also add a row to a change log in the database (one per transaction, none for a statement that changes nothing), and every process reads it every 3 seconds (every second outside production) and clears its own cache when a new row appeared. An isolated change, such as a page published through another replica, the worker or a CLI command such as `entity publish`, shows on every replica within about one poll interval (3 seconds). While writes keep arriving, such as an import, it clears at most once per five polls, so a change can take up to about 15 seconds to show, and it clears once more when the writes stop. Settings, terms, media, forms, schema, theme and plugin changes do not bump it: with several replicas the others keep serving the old page until their own entry expires. The poll is one small query per process and each writing transaction costs one insert into an append-only table, with no lock shared between writers.

So keep the lifetime short when you run several replicas (the default is a good value), or restart the replicas after a settings or theme change that must show at once. Redirects are the exception: they are checked on every request, before the cache, from a copy of the rules that each process reads again every 60 seconds. A lifetime saved in **Settings > Performance** wins over the environment value. The cache needs `PUBLIC_BASE_URL`.

## The worker

Each process runs an in-process scheduler for maintenance jobs unless you turn it off:

- `publish`: publishes scheduled entities.
- `retention_purge`, `prune`, `entity_versions`: retention and housekeeping.
- `agent_hits_partition`, `agent_hits_maintenance`, `agent_ranges_refresh`, `agent_verify`: crawler analytics upkeep.
- `update_check`: the scheduled update check.
- `webhook_deliver`, `webhook_prune`: outgoing webhook delivery and log retention. See [Webhooks](../admin/webhooks.md).
- Plugin cron jobs run on the same scheduler.

Three switches control it:

| Variable | Effect |
|---|---|
| `HATAMOTO_WORKER=off` | No scheduler in this process: no scheduled publishing, no retention, no plugin cron, no scheduled update checks. Any other value (or unset) leaves it on. The setting is case-insensitive. |
| `WORKER_DISABLE_JOBS` | Comma-separated job names to skip, from the list above (for example `retention_purge,prune`). The names are matched case-insensitively. A log line at startup shows which jobs are disabled. |
| `WORKER_DRAIN` | `1`, `true`, `yes` or `on` skips starting the worker loops, intended for the duration of a destructive maintenance step. It does not remove jobs permanently. |

The recommended pattern is one replica with the worker on and the rest with `HATAMOTO_WORKER=off`:

```bash
# replica A (runs scheduled work)
HATAMOTO_WORKER=on

# replicas B and C (serve requests only)
HATAMOTO_WORKER=off
```

If the worker replica is down, scheduled posts are published late rather than lost: they publish on the next run of an enabled worker. With the worker off everywhere, nothing is published on schedule, nothing is purged, crawler hits stay unverified, and nothing truncates old IP addresses or drops old partitions. The server still creates the current and next month's analytics partition at startup regardless, so hits never land in the default partition.

## Secrets and configuration

Every replica must use the same `HATAMOTO_SECRET`. Set it in the environment on all of them. If you let each replica generate its own secret into its own secrets directory, flash cookies, theme preview links and stored import credentials do not work across replicas. Email set up in **Settings > Email** is stored in the database, sealed with `HATAMOTO_SECRET`, and every replica picks up a change within about 30 seconds; that is another reason the secret must match. The `SMTP_*` variables and all other environment settings are per-process values and need to match. There is no shared mail queue: each process sends its own mail.

## Load balancing

Any HTTP load balancer works; there is no session affinity requirement because sessions are in the database. Forward everything to each replica's port, and list the load balancer's address range in `HATAMOTO_TRUSTED_PROXIES` so client IPs and the HTTPS scheme are read from `X-Forwarded-*` correctly ([Reverse proxy and TLS](reverse-proxy-tls.md)). Use `/ready` as the balancer's health probe and `/health` for liveness.

## Health endpoints

| Endpoint | Success | Failure | Use for |
|---|---|---|---|
| `GET /health` | `200`, body `ok` | none: it only proves the process answers | Liveness, container health check. With an admin session, an `X-API-Key` or a Bearer token it returns `{"status":"healthy"}`. |
| `GET /ready` | `200`, body `ok` | `503`, body `not ready`, when the database ping fails | Load balancer and orchestrator readiness. With an admin session, an `X-API-Key` or a Bearer token it returns `{"status":"ready","database":"connected"}`, or `{"status":"not ready","database":"unavailable"}` with the `503`. |

Both are open to anyone and answer plain text, so a probe learns nothing about the software behind it; the JSON detail is for a signed-in caller. The status codes are the same either way, and `HEAD` works too. Examples:

```bash
curl -s http://127.0.0.1:8090/health
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8090/ready
curl -s -H "X-API-Key: $HATAMOTO_API_KEY" http://127.0.0.1:8090/ready
```

### The healthcheck command

`hatamoto healthcheck` exits 0 when `http://127.0.0.1:$API_PORT/health` answers 200 within 4 seconds, otherwise non-zero. It uses `API_PORT` (default `8090`) and always checks the loopback address. The Docker image runs it as its `HEALTHCHECK` (every 30 seconds, 5 second timeout, 30 second start period, 3 retries), so the image needs neither `curl` nor `wget`. For Kubernetes, use an HTTP probe on `/health` for liveness and `/ready` for readiness instead.

### The supervisor's own health gate

When `hatamoto supervise` switches to a new release, it starts the new child and only keeps it if it passes a health gate; otherwise it rolls back. That mechanism is separate from the endpoints above. See [The supervisor](../updates/supervisor.md).

## Rolling restarts

Because migrations take a lock and an older binary tolerates a database that is ahead of it, you can restart replicas one at a time. The supervisor stops a process within 40 seconds, so give containers a grace period of at least 45 seconds (`stop_grace_period: 45s` in the compose file) and systemd `TimeoutStopSec=60`. Wait for `/ready` on a replica before moving on to the next.

## Logs

Logs go to stdout: text in development, JSON when `ENVIRONMENT=production`. Set `LOG_LEVEL=debug` for verbose output (any other value means info). Each request has a request ID, and warnings about configuration problems (for example an invalid `HATAMOTO_PAGE_CACHE_TTL`) are logged once at boot. Ship the logs with whatever your platform provides; Hatamoto has no log files of its own.
