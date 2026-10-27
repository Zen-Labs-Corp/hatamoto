---
title: The supervisor
nav_title: Supervisor
description: How the process that runs your server picks a version, checks every switch, rolls back on its own and records what it did.
---

`hatamoto supervise` is the process the shipped systemd unit and the Docker image run. It starts `hatamoto serve` as a child, watches it, and is the only part of Hatamoto that switches binaries. The supervisor itself fetches nothing from the network and has no database access. Releases reach it as local files (`hatamoto update apply --file`) or from the update agent inside the server. This page lists its files, rules and checks. To watch a switch or roll back by hand, follow [Back up and roll back an update](backups-and-rollback.md).

## What it needs

A writable data directory: `HATAMOTO_DATA`, default `./data`, and `/data` in the image, with room for about three copies of the binary. Releases live under `$HATAMOTO_DATA/releases/`:

```text
releases/
  v1.4.2/hatamoto  release.json  release.json.sig
  current  -> v1.4.2          (relative symlink)
  previous -> v1.4.1          (relative symlink)
  state.json                  supervisor bookkeeping
  state.lock                  lock shared with the update CLI
  events.jsonl                one JSON line per outcome
  supervisor.pid              held under an exclusive lock while it runs
```

If the directory is not writable, the supervisor logs why and runs the binary it was started from. The child gets `HATAMOTO_DATA` set to the resolved absolute path, plus private smoke settings on a loopback port, so the data dir cannot drift between supervisor and server. The health URL it probes is the child's own `/ready` on `API_PORT`.

## Which binary runs

The binary the service was started from is called the **bootstrap**. At each start the supervisor chooses, in this order:

1. If the releases directory is unusable, run the bootstrap.
2. If the bootstrap file changed since the last start (a new image, a package upgrade), the bootstrap wins.
3. If there is no valid `current` release, run the bootstrap.
4. If the bootstrap won earlier and `current` has not changed, stay on the bootstrap. A `current` that changed (an apply, or a link you moved by hand) is honoured.
5. If the bootstrap version is a semver newer than `current`, run the bootstrap. A version that is not `vX.Y.Z` never counts as newer.
6. Otherwise run `current`.

So upgrading the image or the package always takes effect, and re-pointing `current` by hand is honoured at the next start.

## Switching

`hatamoto update apply` (or the agent) verifies the release, stages it in `releases/<version>`, records a pending switch in `state.json` and signals the supervisor with SIGHUP. The supervisor stops the old child, starts the new one and gates it.

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

The gate has three parts:

1. `/ready` must return 200 within 120 seconds.
2. A smoke check runs against a private loopback endpoint of the child: `GET /`, `GET /sitemap.xml`, a sampled published page and `GET <admin>/login`.
3. The smoke check repeats for the first 60 seconds, polling every 5 seconds.

The checks are compared with the last good run, not with fixed expectations. A site with no home page (404 at `/`) passes as long as it keeps returning 404, while a release that turns `/` from 200 into 404 or 500 fails. The gate also fails on:

- any 5xx answer;
- a plugin that now fails to load;
- more render errors than the last good run logged;
- a new boot fault. Boot faults belong to the release rather than to a request, for example the site theme failing validation and falling back to the default, a theme rebuild failing, or plugin templates being rejected. The last good run's list is stored, and a release fails on any fault that is not in it.

Render errors are counted only when logged by the smoke check's own requests; a visitor's errors are never counted. Before a switch the supervisor re-probes the running release to refresh that baseline. When it cannot (an offline apply has no running child) and the stored baseline is older than a day, only a 404 on `/` or on the sampled published page is forgiven, because an editor may have unpublished it since. Everything else stays strict.

## Proven and unproven releases

A release you switched to is *unproven* until it has both passed the gate and run for the full two-minute boot window without crash looping. After that it is *proven*: a later crash loop, for example the database being down at boot, restarts it with exponential backoff (500 ms doubling up to 30 s) and never puts it on the bad list.

## Automatic rollback

A failed gate, or three crashes within two minutes of the start of an unproven release, puts the previous binary back and lists the failed version as **bad**. Applying a bad version is refused (a newer version supersedes it), and the update agent never fetches it again. A proven release is never rolled back by crashes.

A rollback changes binaries only. It never touches the database. The outcome is appended to `events.jsonl` and a running server imports it into the `hatamoto_updates` table within a minute. The event phases are `success`, `rolled_back`, `failed`, `manual_rollback`, `staged-offline` and `rolled-back-offline`.

`previous` always names the last release that actually ran and passed. A second offline apply does not move a never-run release into it, so neither a manual rollback nor a crash-loop rollback lands on something nothing has run. The release a rollback returns to keeps its proven status.

## Applying with no supervisor running

The supervisor holds an exclusive lock on `releases/supervisor.pid` for as long as it lives, and the CLI asks the lock, not the PID written in the file. That works when the supervisor is PID 1 and ignores a stale file. At start the supervisor retries the lock for a few seconds so a concurrent `hatamoto update` never makes it exit.

If no supervisor runs, `update apply` stages the release and moves the links itself, then tells you to restart. That release is not gated at that moment. The next supervisor start runs the normal gate on it first, and a failed gate puts the previous binary back. The usual choice between bootstrap and `current` comes first, so a changed or semver-newer bootstrap still wins, and the offline release is then not gated (the log says so).

## Stopping and restarting

SIGTERM stops the child first, with a 40 second grace before SIGKILL. The shipped systemd unit sets `TimeoutStopSec=60`, `Restart=always` and `RestartSec=5`; the compose file sets `stop_grace_period: 45s` and `restart: unless-stopped`. With plain `docker run`, pass `--restart unless-stopped` so the container comes back if the supervisor ever exits.

## Running it under Docker and systemd

The image sets `HATAMOTO_DATA=/data` and `HATAMOTO_SECRETS_DIR=/data/secrets`, declares `VOLUME /data`, runs as the unprivileged `app` user (uid 10001) and starts `hatamoto supervise` as PID 1. The supervisor keeps releases in `/data/releases`. The shipped compose file mounts:

```yaml
volumes:
  - data:/data                # releases, backups, install_id
  - secrets:/data/secrets     # the server secret, nested inside /data
  - uploads:/app/uploads      # media uploads
```

- The `secrets` volume is mounted inside `/data` (a nested mount), so the server secret lives in its own volume. Keep the `secrets:/data/secrets` line in your own compose file. Without it a new secret is generated, and flash cookies, theme preview links and stored WordPress import credentials made with the old one stop working; sessions and API keys are not affected.
- Older compose files mounted a `data` volume at `/app/data`. The data directory is now `/data`; the same volume contents appear there, so updating the compose file and running `docker compose up -d` needs no migration.
- The supervisor is PID 1, so the restart policy and stop grace in [Stopping and restarting](#stopping-and-restarting) matter: the 45 second `stop_grace_period` leaves the 40 second child shutdown room to finish.
- Bind mounts must be owned by uid 10001. A root-owned `/data` cannot be written by the service, and `hatamoto update` tells you to change its owner rather than run as root.
- The image health check is `hatamoto healthcheck`, which needs no `wget` or `curl`.
- A rebuilt image is a bootstrap change, so it runs at the next start even over a release the agent installed earlier in `/data/releases`. When automatic updates are on, you can also leave the image alone and let the agent move you along.

The shipped systemd unit (`deploy/systemd/hatamoto-api.service`) runs `ExecStart=/opt/hatamoto/bin/hatamoto supervise` as the `hatamoto` user from `/opt/hatamoto`, sets `HATAMOTO_DATA=/opt/hatamoto/data`, reads `/opt/hatamoto/config/hatamoto.env`, and uses `KillMode=mixed`, so systemd stops only the supervisor, which forwards SIGTERM to the child. Without the supervisor a release is only staged and takes effect at the next restart. Replacing the binary in place (a package upgrade or a copy to `/opt/hatamoto/bin/hatamoto`) is a bootstrap change, so it wins at the next start, even over a newer `current` that the agent installed.

## Seeing what it did

```bash
# the last events, one JSON object per line
tail -n 5 "$HATAMOTO_DATA/releases/events.jsonl"
hatamoto update status
```

Each event has `id`, `from`, `to`, `phase`, `error`, `started_at` and `finished_at`. **Settings > Updates** shows the same history from the database, labelling the phases **Updated**, **Rolled back**, **Failed**, **Rolled back by hand** and **Staged**. Continue with [The update agent](update-agent.md) for how releases arrive.
