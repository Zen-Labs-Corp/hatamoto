# Running one site on several servers

This page is for an operator who puts two or more Hatamoto servers behind a
load balancer. One server is enough for most sites. For the operator view of
the same topic, with the health endpoints and the worker switches, see
[site/operate/multi-replica-and-health.md](site/operate/multi-replica-and-health.md).

## What every server shares

Every server must use:

- the same Postgres database;
- the same `HATAMOTO_SECRET`;
- the same media store: S3-compatible object storage (see
  [site/operate/media-object-storage.md](site/operate/media-object-storage.md))
  or one shared directory;
- the same binary version. Restart the servers one at a time, as the operator
  page describes.

Sessions, content, settings and installed packages are in the database, so a
visitor or an admin can be sent to any server.

## Jobs

Each server runs its own scheduler. A job is one of three kinds:

- **Claimed**: it runs once per interval across all servers. The server that
  wins the claim in `hatamoto_job_claims` runs it; the others skip that tick.
- **Own claims**: the job decides in the database which work belongs to this
  server, so servers do not repeat each other's work.
- **Every server**: it runs on each server by design.

| Job id | Kind |
|---|---|
| `retention_purge` | claimed |
| `agent_hits_partition` | claimed |
| `agent_hits_maintenance` | claimed |
| `prune` | claimed |
| `entity_versions` | claimed |
| `changeset_sweep` | claimed |
| `traffic_prune` | claimed |
| `webhook_prune` | claimed |
| `comment_drafts_purge` | claimed |
| plugin cron jobs | claimed |
| `module:*` jobs | claimed through the same table; a failed claim skips the run |
| `publish` | own claims |
| `agent_verify` | own claims |
| `agent_attribution_backfill` | own claims |
| `webhook_deliver` | own claims |
| `standing_digest` | own claims |
| `user_access` | own claims |
| `comment_notify` | own claims |
| `stats_ping` | own claims |
| `backup_offsite` | own claims |
| `core:preload` | every server |
| `agent_ranges_refresh` | every server |
| `update_check` | every server |

`core:preload` warms the page cache of its own process, `agent_ranges_refresh`
keeps a per-process copy of crawler address ranges, and `update_check` exists
because each server replaces its own binary.

How a claim works:

- The lease is just under one interval of the job. The server that ran the job
  last gets no priority: the lease ends before the next tick, so every tick is
  claimable by any server.
- The claim is made on the database clock, in one statement.
- If the claim query fails, the job runs anyway and a warning is logged. A
  database error never stops scheduled work, at the cost of a possible double
  run for that tick.
- After a restart, a claimed job that ran within its last lease skips its
  startup run and waits for its next tick. Shortening a plugin cron interval
  can wait out the old lease once.
- `module:*` jobs are claimed in the same table, but a failed claim query
  refuses the run for them; core jobs run anyway.
- Run now in Settings > Jobs is not claimed. It runs on the server that
  received the click.
- `HATAMOTO_WORKER=off` still works: a server with it off runs no jobs and
  never claims. You may leave the worker on everywhere or on some servers only.

Off-site backups: the time slot is claimed, so one server per interval makes
the backup. The local backup files are on whichever server ran it.

## Page cache

The rendered page cache is per process. A write to a page or a menu adds a row
to the content epoch log in the database, and every server reads it about every
3 seconds and clears its own cache when it changed. So a page or menu change
reaches every server within about one poll.

The content epoch covers pages (entities) and menus only. Settings, theme,
media, forms and schema changes do not bump it: the other servers keep their
cached copy until `HATAMOTO_PAGE_CACHE_TTL` runs out. Keep the TTL short, or
restart the servers after a change that must show at once.

## CDN purge

Set `HATAMOTO_PURGE_URL` to have a server tell your CDN which pages changed.
Core sends one request from the server that made the change:

```
POST https://cdn.example.com/purge
Content-Type: application/json
Authorization: Bearer <HATAMOTO_PURGE_TOKEN>

{"urls":["https://example.com/a"],"all":false}
```

- The request is sent about 2 seconds after the change, so several changes
  share one request. A request holds at most 500 URLs; a longer list is split.
- A failed request is tried up to 3 times in total. Network errors, 429 and
  5xx responses are retried. A failure is logged and never blocks or undoes a
  save.
- `HATAMOTO_PURGE_TOKEN` is optional and is sent as `Authorization: Bearer`.
  It needs `HATAMOTO_PURGE_URL`.
- The URL is fetched through the outbound fetch guard, so a private or loopback
  address is refused unless `HATAMOTO_PURGE_ALLOW_PRIVATE=true`.
- Redirects are not followed. The timeout is 10 seconds.
- With `HATAMOTO_ENV=staging` no purge is sent (see [STAGING.md](STAGING.md)).

What is in the list:

- the page's own URL, and its archive page, when a page is published,
  unpublished, edited while published, deleted or moved (the old URL), and for
  each published page of an import;
- `{"urls":[],"all":true}` when an owner uses Settings > Performance > Clear
  cache.

What is not in the list:

- listing pages, feeds and the home page, even when a change alters them. Use
  the CDN's own TTL for those, or treat any purge as a purge of everything;
- changes made with the `hatamoto` command line tool, which send no purge;
- settings, theme, media, forms and schema changes.

A module compiled into a custom build can register a purger in code, instead
of or as well as the webhook. See the `backend/purge` package documentation and
[adr/0013-cache-purge-hook.md](adr/0013-cache-purge-hook.md).

## Previews

The one-time tickets for the draft preview, plugin pages and change set
previews are kept in the database for 60 seconds, so a ticket minted on one
server opens on another. No sticky sessions are needed. Share links
(`/preview/...`) were already stored in the database.

## State that stays per process

- The package permission prompt: the staged package is held for ten minutes on
  the server that showed it. If the confirmation reaches another server, repeat
  the install.
- The locale-convert review: the review is held on the server that built it.
  Repeat the step if it reports that nothing is staged.
- The two-factor attempt counter: the limit applies per server, so the total
  allowed attempts grow with the number of servers.
- Rate limiters: limits are per process and scale with the number of servers.

If one of these matters for you, route `/admin` to a single server. Public
traffic does not need that.
