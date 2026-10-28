---
title: The update agent
nav_title: Update agent
description: Every setting, default, check and status of the part of the server that looks for, downloads and prepares new releases.
---

The update agent runs inside `hatamoto serve`. It decides what, if anything, to install, and it never switches binaries itself: it downloads, verifies and stages a release, then hands it to the [supervisor](supervisor.md). This page lists its schedule, settings, decisions and the pre-update database dump. To change the settings, follow [Pin or turn off updates](pin-or-turn-off-updates.md).

## Overview

```text
signed channel manifest  --->  update agent (in serve)
  stable.json / edge.json        decides: ramp, pin, defer, window
                                        |
                                        v
                               signed release record + binary
                               (verified against built-in keys)
                                        |
                                        v
                               staged under $HATAMOTO_DATA/releases/<version>
                                        |
                                        v
                               supervisor stops old child, starts new child,
                               runs /ready and smoke checks
                                  |                         |
                               passes                     fails
                                  |                         |
                             new release runs        previous binary back,
                                                     version bad-listed
```

Two things make this safe. Nothing runs unless it verifies: the record signature, the binary hash and size, and the freshness of the manifest are all checked against public keys compiled into your binary (see [Trust and signing](trust-and-signing.md)). And a rollback only swaps binaries, never the database, so the schema rules in [Upgrade rules and support](upgrade-rules-and-support.md) guarantee that the previous binary still works on the migrated schema.

With no configuration, the mode is `auto` and the channel is `stable`. When the manifest offers a newer patch or minor release of the major version you run, and your install is inside that release's rollout, the agent fetches the signed record and the binary, verifies them, takes a database dump and asks the supervisor to switch. A new major version is only announced.

## Schedule and network use

The scheduled job is named `update_check`. It runs every 6 hours with up to 30 minutes of jitter, and the first check happens one to nine minutes after the server starts. A run may take up to 45 minutes in total (fetch, download and switch together).

A check makes one request before it has a target: an anonymous GET of the signed channel manifest, `<base>/channels/<channel>.json`. The default base URL is `https://hatamoto.dev/releases`, so the default check fetches `https://hatamoto.dev/releases/channels/stable.json`. The request carries no query string, no cookie and no identifier of your install, and its only headers are the ones Go adds itself (its default `User-Agent` and `Accept-Encoding`). The optional [install stats](../operate/install-stats.md) are a separate request that is off until an owner opts in; they never ride on the check.

In `notify` mode that is the whole check: it reports the newest release your box is inside the rollout for and fetches nothing else. In `auto` mode, only when the manifest names a target does the agent fetch the signed record (`<base>/<version>/release.json` and `release.json.sig`) and the binary (`<base>/<version>/hatamoto-<platform>`). Records are kept under `releases/.records/` and verified again on every use, so a box that is waiting is not asking again.

Fetch limits: the manifest is capped at 1 MiB with a 30 second timeout, a record at 64 KiB, a signature at 4 KiB, and any artifact at 256 MiB or the size its record states. A download is abandoned after two minutes without bytes or 30 minutes in total. Redirects may not leave the original host or fall back from https to http.

## Choosing a target

The agent evaluates the manifest in this order:

1. If the version you run was **yanked** (withdrawn), move to its named replacement, or to a newer normal release if the yank names none. Otherwise roll back to the previous release (or the bootstrap build) when the rollback floor allows it. `notify` mode only reports a rollback and never makes it.
2. Consider releases newer than the one you run, in the same major, that are not yanked, not **halted** (paused by the maintainers), and not on this box's bad list.
3. Keep those whose rollout bucket for that version is below the current ramp percentage.
4. Apply the pin, then the defer days and the maintenance window. Security releases skip all three, as long as they stay inside the same major.

A release that needs a newer starting point (the record's `min_from`) is reached in one hop per check, oldest first. A release from a newer major is never installed automatically; it is shown in `update status` as `available major`. A dev build, meaning a binary whose version is not of the form `vX.Y.Z`, never updates itself.

### Rollout buckets

Each install has a random 32-byte id in `$HATAMOTO_DATA/install_id` (mode 0600). It never leaves the machine. A release's bucket for your box is the first two bytes of `sha256(id || version)` modulo 100. A release is offered when the bucket is below the ramp percentage, and the ramp is a list of `{from, pct}` steps, so a release can go to 10 percent of installs, then 50, then 100 over days. Do not delete or edit `install_id`: a damaged file is reported rather than silently replaced, because a new id would move you to another bucket.

## Policy

| Setting | Values | Default | Notes |
|---|---|---|---|
| Mode | `auto`, `notify`, `off` | `auto` | Shown in the admin as **Automatic**, **Notify only** and **Off**. |
| Channel | `stable`, `edge`, `canary` | `stable` | Stable is for live sites (day 7+). Edge gets each release on day 2. Canary is bleeding edge for testing and may break: it gets each release first. |
| Pin | `vMAJOR.MINOR` | none | Only releases on that minor line, plus security releases inside the same major. Set with `hatamoto update pin`; removed with `unpin` or the admin's **Unpin**. |
| Defer days | 0 to 14 | 0 | Hold back non-security releases after first seeing them. Values outside the range are clamped. |
| Maintenance window | start hour and end hour, 0 to 23 | any time | Evaluated in the site time zone (UTC when unset). The window is `[start, end)`, wraps midnight when start is greater than end, and means any time when either bound is empty or both are equal. |

| Mode | Behaviour |
|---|---|
| `auto` | Check, download and install (default). |
| `notify` | Check the manifest and report the newest release in your rollout. Nothing else is fetched and nothing is installed. |
| `off` | No network call at all. Owners see a banner in the admin once updates have been off for 90 days or more. |

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

These are stored in one row of `hatamoto_update_settings`. Replicas that share a database share that row, while each replica still updates its own binaries. Edit them in **Settings > Updates** (`<admin>/settings/updates`, owners only) or with `hatamoto update pin`.

### Environment variables

Environment variables win over the stored settings, and the admin shows those fields as locked (**Set by HATAMOTO_UPDATES on the server**):

| Variable | Default | Meaning |
|---|---|---|
| `HATAMOTO_UPDATES` | `auto` | `auto` (also `on`, `true`, `1`), `notify`, or `off` (also `false`, `0`, `no`, `disabled`), case-insensitive. Any other value fails closed to `off` and logs a warning. |
| `HATAMOTO_UPDATE_CHANNEL` | `stable` | `stable`, `edge` or `canary`. An invalid value is logged and ignored. |
| `HATAMOTO_UPDATE_URL` | `https://hatamoto.dev/releases` | Another base URL for a mirror. Must be a plain `https://host/path` with no credentials, query or fragment. |
| `HATAMOTO_UPDATE_BACKUP` | on | Set to `0` to skip the pre-update database dump. |
| `WORKER_DISABLE_JOBS` | empty | A comma-separated list of jobs; include `update_check` to stop scheduled checks. The CLI still works. |
| `HATAMOTO_WORKER` | on | `off` runs no scheduler, so no scheduled checks. |

A mirror serves the same layout and the same signed files. Trust does not depend on where the bytes came from, because every file is verified against built-in keys.

### Where to control it

| You want to | Do this |
|---|---|
| See the state | **Settings > Updates**, or `hatamoto update status` |
| Check now without installing | **Check now**, or `hatamoto update check --dry-run` |
| Update now | **Update now**, or `hatamoto update check` (under policy) or `hatamoto update apply vX.Y.Z` (named) |
| Only be told about releases | Mode **Notify only**, or `HATAMOTO_UPDATES=notify` |
| Turn it off | Mode **Off**, or `HATAMOTO_UPDATES=off` |
| Delay non-security releases | **Defer updates** (0 to 14 days) |
| Stay on one minor line | `hatamoto update pin v1.4`, undo with `hatamoto update unpin` |
| Choose a time of day | **Maintenance window** (hours in the site time zone) |
| Follow early releases | **Channel**, or `HATAMOTO_UPDATE_CHANNEL=edge` |
| Go back one release | `hatamoto update rollback` |
| Install with no internet | `hatamoto update apply --file F --record R --sig S` |

The commands are described in [The update CLI](manual-update-cli.md).

## Requirements

- `HATAMOTO_DATA` points at a writable directory with about three times the binary size free. It defaults to `./data` and is `/data` in the image.
- Docker: mount `/data` as a volume, as the shipped compose file does.
- systemd: use the shipped unit, whose `ExecStart` runs `hatamoto supervise`.

## Preflight before a switch

Before switching, the agent checks that:

- the release has a build for your platform;
- the enabled plugins and the active theme declare an SDK range that the new release meets (its `sdk` or any `sdk_compatible_with` version); an unparsable range blocks;
- there is at least three times the binary size free under the data directory;
- no content import is running.

Any miss is shown in `update status` and retried at the next check. `hatamoto update apply vX.Y.Z --force` skips the SDK and disk checks. The agent also skips a run, reporting `busy`, while a switch is in progress or the running release is still unproven.

It then takes the pre-update database dump described below. A failed dump never stops an update.

## The pre-update database dump

Before a scheduled install, `hatamoto update check` or the admin's **Update now**, the agent runs `pg_dump` against the database in `DATABASE_URL` and writes:

```text
$HATAMOTO_DATA/backups/pre-<version>.dump
```

For example `pre-v1.4.2.dump`. `hatamoto update apply vX.Y.Z` from the command line takes no dump.

- The format is `pg_dump --format=custom`, so restore it with `pg_restore`.
- The `backups` directory is created with mode 0700 and the file is 0600, because it holds the whole database.
- Only one dump is kept. Writing a new `pre-*.dump` removes older ones. Other file names in the directory are left alone.
- The connection is passed to `pg_dump` through `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE` and `PGSSLMODE`, so the password never shows in a process listing.
- A dump has a ten minute limit.
- The dump is taken only when `pg_dump` is installed and its major version equals the server's major version (and is at least 10). Otherwise the status notes `no database backup` with the reason. The official image does not bundle `pg_dump`; install a matching `postgresql-client` on a systemd host if you want it.
- The dump is a copy for you. The agent never restores it, and a failure never stops an update.
- Set `HATAMOTO_UPDATE_BACKUP=0` to turn it off (the status notes `database backup is off`), for example if your own backups already run on a tighter schedule.

The dump is not your backup strategy. It covers one point in time before an update and lives next to the data it protects.

Restoring the dump into a scratch database, as an example to adapt:

```bash
createdb hatamoto_restore
pg_restore --no-owner --dbname hatamoto_restore "$HATAMOTO_DATA/backups/pre-v1.4.2.dump"
```

Do not restore over a live database unless you also restore the binary the dump was taken with.

### What to back up for a full restore

| What | Where |
|---|---|
| Database | `pg_dump` of `DATABASE_URL` (the pre-update dump is one example) |
| Server secret | `$HATAMOTO_SECRETS_DIR` (default `./data/secrets` under the working directory, `/data/secrets` in its own volume in the Docker image). Without it, a new secret is generated, and flash cookies, theme preview links and stored WordPress import credentials made with the old one stop working. Sessions and API keys live in the database and are not affected. |
| Uploads | `MEDIA_UPLOAD_DIR` (default `./uploads`, `/app/uploads` in the image) |
| Install id | `$HATAMOTO_DATA/install_id` (optional; it only sets your rollout bucket) |

Restore the database and secrets first, then bring up the binary, which runs `migrate` at start.

## Decisions you will see

`update status` and the admin page show one of these decisions:

| Decision | Meaning |
|---|---|
| `up to date` | Running the newest release offered, or the newest is outside your pin. |
| `scheduled` | A release exists but your bucket has not reached it yet, or a check without install found it ready. |
| `waiting window` | Waiting for the maintenance window. |
| `waiting defer` | Held back by the defer days, with the date it is due. |
| `blocked` | The preflight failed; the reason names the plugin, theme, disk or `min_from`. |
| `available major` | A newer major exists; install it by hand. |
| `applying` | A switch was handed to the supervisor. |
| `staged` | A release was staged but no supervisor runs to apply it. |
| `rolled back` | The health gate rejected the newest release on this box. |
| `rolled back yanked` | The running release was withdrawn and the box moved away from it. |
| `rollback pending` | A check without install found the running release withdrawn; the next scheduled run rolls back. |
| `update failed` | An **Update now** from the admin stopped with an error. |
| `notify` | Mode notify: a release is reported, not installed. |
| `off` | Updates are off. |
| `busy` | A switch, an unproven release, an import or another check is in progress. |
| `dev` | A dev build, which never updates. |

A release that failed its health gate is never fetched again on that box, and a newer release replaces it automatically.

### When an update did not take

| Symptom | Check |
|---|---|
| `update status` says `blocked` | The reason names the plugin, theme, disk or `min_from` that blocked it. Fix it and run `hatamoto update check`. |
| The new version never starts | `releases/events.jsonl` and the service log. Look for `rolled_back` and the `error` field. |
| Update refuses to apply as root | Run as the service user. If `/data` is owned by root, change its owner to uid 10001 in the image. |
| A release is applied but stays on the old version | There was no supervisor. Restart the service so the next start gates the staged release. |
| Disk full | The agent needs three times the binary size free under `HATAMOTO_DATA`. |

## Running without a supervisor

If `serve` runs without `supervise` (for example during development), the agent stages a release once and reports `staged vX`, adding that it must run under `hatamoto supervise` to apply. It does not move any links, and later checks do not download it again.

## Failure behaviour

An agent problem, such as an unusable update URL, is logged as `automatic updates are unavailable` and never stops the site from starting. Network errors, expired manifests and signature failures are recorded as an error result in `update status` and retried at the next scheduled check.
