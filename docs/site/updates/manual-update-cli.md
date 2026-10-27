---
title: The update CLI
nav_title: Manual update CLI
description: Every hatamoto update command, flag, output line and refusal, including installs from local files on a server with no internet access.
---

The `hatamoto update` command inspects the update agent, changes the pin, installs a named release, installs from local files with no network, and goes back one release. This page lists every verb, flag and message. For the everyday task, follow [Check for and apply an update](check-and-apply-an-update.md).

## Overview

```text
hatamoto update status
hatamoto update check [--dry-run]
hatamoto update pin vMAJOR.MINOR
hatamoto update unpin
hatamoto update apply vMAJOR.MINOR.PATCH [--force]
hatamoto update apply --file F --record R [--sig S]
hatamoto update rollback [--force]
```

`hatamoto help` prints one-line summaries of the same verbs.

| Verb | Needs `DATABASE_URL` | Needs network | Owner check |
|---|---|---|---|
| `status` | yes | no | no |
| `check` | yes | yes | yes |
| `pin`, `unpin` | yes | no | yes |
| `apply vX.Y.Z` | only for the plugin and theme check | yes | yes |
| `apply --file` | no | no | yes |
| `rollback` | no | no | yes |

## Running it correctly

`hatamoto update` has to see the same data directory and run as the same user as the service.

- **Run as the service user, never as root.** Every verb except `status` refuses to run as a uid other than the owner of `releases/` and names the owner to run as. Running as root would leave files the service cannot read. If `releases/` or a bind-mounted `/data` is owned by root, the service cannot write it either: change its owner to the service uid (10001 in the image) rather than running the command as root.
- **Point it at the data directory.** The data directory is `HATAMOTO_DATA`, or `./data` of the current directory when unset. With the variable unset, the command refuses to work in a directory that has no supervisor state (`releases/state.json` or `releases/supervisor.pid`):

  ```text
  no supervisor state under /srv/site/data (HATAMOTO_DATA is not set, so this is ./data of the current directory). Run from the service's working directory (cd /opt/hatamoto) or set HATAMOTO_DATA to the service's data dir
  ```

  The shipped unit sets `HATAMOTO_DATA=/opt/hatamoto/data`, but your shell does not inherit it, so export it or start from `/opt/hatamoto`.
- **Database verbs need `DATABASE_URL`.** `status`, `check`, `pin` and `unpin` read the stored update settings. Without it they stop with `DATABASE_URL is required`.
- `apply --file` and `rollback` end with a `releases dir:` line naming the directory they used, so you can confirm they touched the right place.

```bash
# systemd install: the service user, with the service's settings loaded
sudo -u hatamoto bash -c 'set -a; . /opt/hatamoto/config/hatamoto.env; set +a
  cd /opt/hatamoto && HATAMOTO_DATA=/opt/hatamoto/data exec bin/hatamoto update status'

# Docker: the image's app user (uid 10001) owns /data
docker compose exec hatamoto hatamoto update status
```

## status

Prints the effective settings and what the agent last decided:

```text
version     v1.4.1 (channel stable, mode auto)
pin         v1.4
status      scheduled: v1.4.2 is rolling out (bucket 61, ramp 25%)
last check  2026-11-02T09:14:07Z ok
```

| Line | Meaning |
|---|---|
| `version` | The release the supervisor runs (not the version of the binary you typed), `dev` when unknown, then the channel and mode in force. Environment variables are already applied. |
| `pin` | Shown only when a pin is set. |
| `status` | The decision and its reason, for example `up to date: running the newest release offered`. `no check yet` before the first check. After a health-gate rollback: `rolled back vX: <reason>`. The decisions are listed in [The update agent](update-agent.md). |
| `major` | Shown when a new major version is available, with the `apply` command that installs it. See [Upgrade rules and support](upgrade-rules-and-support.md). |
| `last check` | The time (UTC) and result (`ok`, `error` or `skipped`) of the last check, or `never`. |

## check

Checks the channel now, stores the outcome like a scheduled check and, when policy allows, installs, taking the pre-update database dump first. In mode `off` it makes no request and the status line reads `off: updates are off`.

Add `--dry-run` to decide without installing or storing anything: the status line then ends with `(dry run: nothing installed)`.

The admin's **Check now** button on **Settings > Updates** is a third variant: it checks and stores the outcome, but never downloads or installs. A release that is due reads as `scheduled` with the reason `vX is ready to install`.

## pin and unpin

```bash
hatamoto update pin v1.4
hatamoto update unpin
```

```text
pinned to v1.4 (security releases still apply inside the major)
unpinned
```

A pin must have the form `vMAJOR.MINOR`. While pinned, only releases on that minor line are installed, plus security releases inside the same major. Pin and unpin write the same stored settings as the admin page, whose **Unpin** button removes a pin. A pin can only be set from the command line.

## apply vX.Y.Z

```bash
hatamoto update apply v2.0.0
```

Downloads the named version from the channel, verifies the manifest, record and binary against the built-in keys, runs the preflight and hands the release to the supervisor. It ignores the ramp, the maintenance window, the defer days, the pin and mode `off`, so it is how you install a major version. It prints the status lines, with a status such as `applying: staged v2.0.0; supervisor notified (health-gated switch in progress; see logs)`.

It refuses a version that:

- is not on your channel (`vX is not on channel stable`);
- was withdrawn (`vX was withdrawn (<reason>)`);
- is on this box's bad list (`vX is on this box's bad list (it failed health gating before)`);
- is already running;
- needs a newer starting point first (`vX needs at least vY first (running vZ)`);
- is below the running release's rollback floor;
- has no build for your platform.

The preflight checks the SDK range of the enabled plugins and the active theme (only when `DATABASE_URL` is set) and the free disk space. `--force` skips both. When another check is running the command stops with `not installing vX now: <reason>`.

Unlike a scheduled install and the admin's **Update now**, this route takes no pre-update database dump, and it does not touch the stored status.

## apply --file

For servers with no internet access, download three files on another machine from `https://hatamoto.dev/releases/<version>/`: the binary for your platform (`hatamoto-linux-amd64`, `hatamoto-linux-arm64`, `hatamoto-darwin-amd64` or `hatamoto-darwin-arm64`), `release.json` and `release.json.sig`. Copy them over and run:

```bash
cd /opt/hatamoto && sudo -u hatamoto bin/hatamoto update apply \
  --file ./hatamoto-linux-amd64 \
  --record ./release.json \
  --sig ./release.json.sig
```

| Flag | Default | Meaning |
|---|---|---|
| `--file` | required | The release binary. |
| `--record` | required | The signed release record, `release.json`. |
| `--sig` | the record path plus `.sig` | The record's signature file. |

This verb needs no database and no network. It verifies the record signature and the binary hash and size against the embedded keys, stages the release in `$HATAMOTO_DATA/releases/<version>` and signals the supervisor to switch. It refuses a version that:

- is on the bad list (`vX is on the bad list (rolled back on this box before); a newer version supersedes it`);
- is already running;
- needs a newer starting point (`min_from`) first;
- is below the running release's rollback floor;
- has no artifact for the platform you run on;
- while another switch is still in progress.

Whether a supervisor is running is decided by its lock on `releases/supervisor.pid`. With one, the message is `staged vX; supervisor notified (health-gated switch in progress; see logs)`. Without one, the command moves the links itself, logs the event as `staged-offline`, and prints `staged and switched to vX; restart hatamoto (systemctl restart hatamoto-api / docker compose restart) to use it`. That release is health-gated at the next supervisor start.

In Docker, copy the files into the container first, then apply:

```bash
docker compose cp hatamoto-linux-amd64 hatamoto:/tmp/
docker compose cp release.json hatamoto:/tmp/
docker compose cp release.json.sig hatamoto:/tmp/
docker compose exec hatamoto hatamoto update apply --file /tmp/hatamoto-linux-amd64 --record /tmp/release.json
```

## rollback

```bash
hatamoto update rollback
```

Goes back to the `previous` release by hand, with the same health-gated switch. It needs no database and no network. It refuses when:

- there is no previous release (`no previous release`);
- the previous release is on the bad list, or it was applied offline and never ran or passed a gate (add `--force` to go back anyway);
- the previous release is below the running release's `rollback_floor` (`--force` does not override this);
- the previous release is the bootstrap build and its version is unknown, so it cannot be compared with the floor;
- another switch is still in progress.

A manual rollback bad-lists the release you rolled back from, so the agent will not reinstall it, and is logged as `manual_rollback`. An offline rollback away from the bootstrap is logged as `rolled-back-offline`. The database is not changed. For the whole procedure, see [Back up and roll back an update](backups-and-rollback.md).

## Usage errors

A malformed `update` command, such as `hatamoto update pin 1.4` or `hatamoto update apply 1.4.2`, is not accepted as a command at all. The program prints `unknown command` with the arguments it got, followed by the full command list:

```text
unknown command ["update" "pin" "1.4"]
usage: hatamoto <command>
```

A pin needs `vMAJOR.MINOR` (for example `v1.4`), and an apply version needs the canonical `vMAJOR.MINOR.PATCH` form (for example `v1.4.2`).

## Related settings

`HATAMOTO_UPDATES`, `HATAMOTO_UPDATE_CHANNEL`, `HATAMOTO_UPDATE_URL` and `HATAMOTO_UPDATE_BACKUP` apply to the CLI as well as the agent. They are described in [The update agent](update-agent.md).
