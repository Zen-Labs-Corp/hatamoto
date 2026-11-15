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
| `publish` | promotes `scheduled` entities whose `published_at` has passed to `published`, emits the `entity.published` event and clears cached pages | 30 seconds | every minute (idle beat while the site is idle with the back-off on) |
| `retention_purge` | deletes expired admin sessions and expired or used one-time user tokens | 6 minutes | daily |
| `agent_hits_partition` | creates monthly partitions of the AI-crawler hits table (2 ahead) and drops partitions that are old enough (hits are kept for at least 90 days) | 45 seconds | daily |
| `agent_hits_maintenance` | truncates stored client IPs older than 30 days (to a /24 for IPv4, a /48 for IPv6) in batches of 5000 | 12 minutes | daily |
| `agent_ranges_refresh` | downloads the published IP ranges of AI operators used to verify crawler hits | 90 seconds | daily |
| `agent_verify` | verifies pending crawler hits against those ranges | 2 minutes | every 5 minutes (idle beat while the site is idle with the back-off on) |
| `prune` | batch-deletes old rows: the 404 log after 90 days, the MCP audit after 90 days, the admin audit after 1 year | 8 minutes | daily |
| `entity_versions` | merges identical consecutive versions and keeps the newest N per entity (setting, default 50) and every version live in the last 30 days | 10 minutes | daily |
| `webhook_deliver` | sends due outgoing webhook deliveries, up to 100 per run within 25 seconds, see [Webhooks](../admin/webhooks.md) | 5 seconds | every 10 seconds, give or take 2 seconds (idle beat while the site is idle with the back-off on) |
| `webhook_prune` | deletes old webhook deliveries (30 days delivered, 90 days other, at most 1,000 per webhook) | 2 minutes | hourly, give or take 5 minutes |
| `changeset_sweep` | closes staged content changesets past their 72 hour expiry (their items, buffer and staged files go), deletes finished runs and removes staged files nothing refers to (left over by a crash) | 3 minutes | hourly, give or take 5 minutes |
| `user_access` | signs out and revokes the API keys of users whose access end date has passed, writes an audit entry, and mails the other owners three days before an end when mail is set up | 10 minutes | hourly |
| `standing_digest` | mails the owners the runs that published themselves in the last day, once a day across replicas, only when mail is set up and something published | 10 minutes | daily, give or take 30 minutes |
| `update_check` | the signed update check, see [How updates work](../updates/index.md); a run may take up to 45 minutes (fetch, download and switch), outside the 10 minute cap, and still stops on shutdown | 1 to 9 minutes (random) | every 6 hours, give or take 30 minutes |

The prune and version jobs work in bounded batches (using `ctid` for deletes; the IP truncation selects by primary key, because `ctid` is unique only within one partition) so a large backlog never holds a long lock.

## Idle back-off

A site that nobody visits would otherwise touch the database every few seconds and keep a connection open for good. So, when you let the pool drain, the frequent checks back off once the install is **idle**. The back-off is on only when `DATABASE_MIN_CONNECTIONS=0` (the default is `5`: connections are kept warm anyway, nothing backs off and every interval on this page applies as written). With it on, the install is idle when no request has been served for `DATABASE_MAX_CONN_IDLE_TIME` (default `30m`). Requests to `/health` and `/ready` do not count, so a monitor pointed at those paths does not keep the site awake (one that fetches a page does count); any other traffic, bots included, does.

While idle, these checks stop running on their own cadence and share one **idle beat** instead, so they run together in a single burst and the database connections can close in the gaps:

- the jobs `publish`, `agent_verify`, `webhook_deliver` and `backup_offsite`,
- the package reconcile,
- the content change poll (see [Multiple replicas](../operate/multi-replica-and-health.md)),
- the email settings check across replicas.

The beat is twice `DATABASE_MAX_CONN_IDLE_TIME`, kept between 5 and 15 minutes, so the gap is always longer than the time a pooled connection may sit idle. The first request after an idle spell wakes all of them for an immediate check that runs alongside it; that request is served as usual, so it can still show the old content when the change came from another process.

Work that is already known is not delayed. A scheduled post, a webhook retry and a scheduled unpublish that exist when the wait starts wake their job at the due time even while idle, and a webhook queued by an event in this process wakes delivery at once. Idleness is per process, though: a process that serves no traffic, such as a worker-only replica, notices posts scheduled and webhooks queued by other processes (other replicas, the command line) only on the beat, 5 to 15 minutes. If you need prompt delivery across processes, keep `DATABASE_MIN_CONNECTIONS` above `0`, which turns the back-off off. Every other job, including all plugin `cron` jobs, and every interval above are unchanged. While the site is active nothing changes either. The Jobs screen shows the real next run.

Changes saved from the admin in the running process (email, theme settings, installing or activating a package) apply at once and never wait for a check. A change made by another replica or by the command line may take up to the idle beat to be noticed while the site is idle; the next request starts an immediate check, but that request itself may still see the old state. For the densest hosting, set `DATABASE_MIN_CONNECTIONS=0` and `DATABASE_MAX_CONN_IDLE_TIME=2m`.

## Other background loops

These run whenever the server runs, whether or not the scheduler is enabled:

- **Analytics flush.** Pageviews and AI referrals are buffered in memory and written to the daily rollup every 30 seconds, and flushed again on shutdown.
- **Agent hit writer.** Crawler hits recorded by the page handlers go through a channel to a writer goroutine that resolves paths to entities and inserts in batches, so the response never waits for the database.
- **Package reconcile.** Every 5 seconds in production (every second otherwise) the server compares installed packages in the database with what is loaded, so a plugin installed from the command line, or by another replica, goes live without a restart. While the site is idle this check moves to the idle beat (see [Idle back-off](#idle-back-off)).
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

The job names accepted by `WORKER_DISABLE_JOBS` are exactly `publish`, `retention_purge`, `prune`, `entity_versions`, `agent_hits_partition`, `agent_hits_maintenance`, `agent_ranges_refresh`, `agent_verify`, `update_check`, `webhook_deliver`, `webhook_prune`, `changeset_sweep` `standing_digest` and `user_access`. The server logs which of them are disabled at start-up.

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
