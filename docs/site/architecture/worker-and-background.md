---
title: Worker and background jobs
nav_title: Worker and background
description: Every background job and loop the server runs on its own, when each one runs, and the settings that switch them off.
---

This page lists the work the server does in the background, without a request: scheduled jobs, buffers that are written out, and watchers. To see the scheduler start on your own server, follow step 7 of [See how Hatamoto is put together](overview.md).

## No separate worker process

Hatamoto does not need a queue server or a second process for background work. `hatamoto serve` starts a small in-process **scheduler** next to the HTTP server, and a few dedicated loops for tasks that are not scheduler jobs. All of them watch the server's lifecycle context. On shutdown the context is cancelled, the scheduler stops accepting runs and waits for running jobs to finish (the whole shutdown has a 30 second budget), and then the HTTP server stops.

The scheduler hosts **core maintenance jobs** only. Plugin background work is declared as `cron` entries in the plugin manifest and runs in the plugin sandbox, but those jobs are registered with the same scheduler and obey the same switches (see [Plugin runtime](plugin-runtime.md)).

## Scheduler jobs

Each job is a function the scheduler calls on a timer. Every run has a 10 minute timeout (1 minute for `publish`), errors are logged as `scheduled job failed` and never fatal, and a run that outlasts its interval delays the next tick instead of overlapping with it. The "first run" is a staggered delay after boot so jobs do not start together.

| Job name | What it does | First run | Interval |
|---|---|---|---|
| `publish` | promotes `scheduled` entities whose `published_at` has passed to `published`, emits the `entity.published` event and clears cached pages | 30 seconds | every minute |
| `retention_purge` | deletes expired admin sessions and expired or used one-time user tokens | 6 minutes | daily |
| `agent_hits_partition` | creates monthly partitions of the AI-crawler hits table (2 ahead) and drops partitions that are old enough (hits are kept for at least 90 days) | 45 seconds | daily |
| `agent_hits_maintenance` | truncates stored client IPs older than 30 days (to a /24 for IPv4, a /48 for IPv6) in batches of 5000 | 12 minutes | daily |
| `agent_ranges_refresh` | downloads the published IP ranges of AI operators used to verify crawler hits | 90 seconds | daily |
| `agent_verify` | verifies pending crawler hits against those ranges | 2 minutes | every 5 minutes |
| `prune` | batch-deletes old rows: the 404 log after 90 days, the MCP audit after 90 days, the admin audit after 1 year | 8 minutes | daily |
| `entity_versions` | merges identical consecutive versions and keeps the newest 20 per entity | 10 minutes | daily |
| `webhook_deliver` | sends due outgoing webhook deliveries, up to 100 per run within 25 seconds, see [Webhooks](../admin/webhooks.md) | 5 seconds | every 10 seconds, give or take 2 seconds |
| `webhook_prune` | deletes old webhook deliveries (30 days delivered, 90 days other, at most 1,000 per webhook) | 2 minutes | hourly, give or take 5 minutes |
| `update_check` | the signed update check, see [How updates work](../updates/index.md); a run may take up to 45 minutes (fetch, download and switch), outside the 10 minute cap, and still stops on shutdown | 1 to 9 minutes (random) | every 6 hours, give or take 30 minutes |

The prune and version jobs work in bounded batches (using `ctid` for deletes; the IP truncation selects by primary key, because `ctid` is unique only within one partition) so a large backlog never holds a long lock.

## Other background loops

These run whenever the server runs, whether or not the scheduler is enabled:

- **Analytics flush.** Pageviews and AI referrals are buffered in memory and written to the daily rollup every 30 seconds, and flushed again on shutdown.
- **Agent hit writer.** Crawler hits recorded by the page handlers go through a channel to a writer goroutine that resolves paths to entities and inserts in batches, so the response never waits for the database.
- **Package reconcile.** Every 5 seconds in production (every second otherwise) the server compares installed packages in the database with what is loaded, so a plugin installed from the command line, or by another replica, goes live without a restart.
- **Page cache preload.** When the owner enables preload, pages are re-rendered to warm the cache, with a concurrency of 2, either all pages or only the missing ones. It runs on the scheduler at the preload interval (by default the cache lifetime, at least one minute; this part needs the scheduler) and, when "after changes" is on, a short while after content changes. It stops when the cache size limit is reached. See [Performance and the page cache](../admin/performance-cache.md).
- **Access log tailer.** Only when `AGENT_LOG_INGEST` is on and `AGENT_LOG_CADDY_PATH` points at a reverse-proxy access log: it follows the file read-only and feeds crawler hits that never reached the application. See [Access log ingest](../ai-seo/access-log-ingest.md).
- **Update events watcher.** Picks up update outcomes that the supervisor wrote to `releases/events.jsonl` in the data directory and records them in the database.

Partitions for the crawler hits table are also ensured at boot, independently of the scheduler, so hits never land in the default partition even when the worker is off.

## Turning things off

| Setting | Effect |
|---|---|
| `HATAMOTO_WORKER=off` | do not create the scheduler at all: no scheduler jobs run in this process (case-insensitive; any other value, or unset, leaves it on) |
| `WORKER_DISABLE_JOBS=publish,prune` | skip the named jobs; names are comma separated and case-insensitive |
| `WORKER_DRAIN=true` | create the scheduler but start none of its loops (also accepts `1`, `yes`, `on`); meant for the moments you run a risky migration and want no background writes |

The job names accepted by `WORKER_DISABLE_JOBS` are exactly `publish`, `retention_purge`, `prune`, `entity_versions`, `agent_hits_partition`, `agent_hits_maintenance`, `agent_ranges_refresh`, `agent_verify`, `update_check`, `webhook_deliver` and `webhook_prune`. The server logs which of them are disabled at start-up.

```bash
# a second replica that should serve traffic but not run maintenance
HATAMOTO_WORKER=off hatamoto serve

# keep the site running but never check for updates from this process
WORKER_DISABLE_JOBS=update_check hatamoto serve
```

## Running more than one replica

The scheduler has no leader election. Run with the worker on in exactly one replica, or accept that every replica runs each job. Most jobs are idempotent and operate in set-based SQL (promoting due entities, deleting expired rows) so duplicates are wasteful but safe. When you run several replicas, set `HATAMOTO_WORKER=off` on all but one. Page caches and rate limiters are per process as well; see [Multi-replica and health](../operate/multi-replica-and-health.md).

## Health and visibility

Scheduler and job activity is written to the structured log (`starting scheduler`, per-job `... failed` or `purged ...` lines). `GET /health` reports that the process is up and `GET /ready` that it can serve, including database reachability; neither depends on a job having run. The admin has screens for the page cache and for updates that show the state of the cache preload and the update check.
