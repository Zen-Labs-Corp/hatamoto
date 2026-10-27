---
title: Back up and roll back an update
nav_title: Backups and rollback
description: Take your own backup before an update, go back to the previous version by hand, and get a site running again when it will not start.
next: upgrading-self-hosted.md, ../operate/backups.md
---

Hatamoto undoes a failed update on its own, but you stay in charge of your data. In this guide you take a backup before an update, go back to the previous version by hand when a release misbehaves after it passed its checks, and recover a server that will not start at all.

A **rollback**, going back to the previous version, swaps program files only and never changes the database. That is safe because a patch or minor release may only add to the database structure, never remove or rename, so the previous version still works on it. [Upgrade rules and support](upgrade-rules-and-support.md) explains the rules, including the oldest version you can roll back to.

## Before you start

- A shell on the server as the service user, with the service's settings loaded. With systemd:

  ```bash
  sudo -u hatamoto bash
  set -a; . /opt/hatamoto/config/hatamoto.env; set +a
  cd /opt/hatamoto && export HATAMOTO_DATA=/opt/hatamoto/data
  ```

  With Docker, run each `hatamoto` command as `docker compose exec hatamoto hatamoto <command>`.

- `pg_dump`, from a `postgresql-client` package whose major version matches your Postgres server.
- Somewhere off the server to keep copies.

## Steps

### Back up before an update

1. Dump the database:

   ```bash
   mkdir -p -m 0700 /opt/hatamoto/data/backups
   pg_dump --format=custom --file "/opt/hatamoto/data/backups/manual-$(date +%F).dump" "$DATABASE_URL"
   ```

   Before each automatic install the **update agent**, the part of the server that checks for and downloads new releases, also saves `$HATAMOTO_DATA/backups/pre-<version>.dump`. That copy covers only the moment before its own update, lives next to the data it protects and is skipped when `pg_dump` is missing, so it does not replace yours. The official image does not include `pg_dump`.

2. Copy three things off the server: the dump, the server secret directory (`HATAMOTO_SECRETS_DIR`, by default `/opt/hatamoto/data/secrets` with the shipped unit and `/data/secrets` in the image) and the uploads directory (`MEDIA_UPLOAD_DIR`, by default `/opt/hatamoto/uploads` with the shipped unit and `/app/uploads` in the image). Without the secret, a new one is generated and some links and stored credentials stop working. For a regular schedule, see [Backups](../operate/backups.md).

### Roll back by hand

1. Go back to the previous release:

   ```bash
   bin/hatamoto update rollback
   ```

   ```text
   staged v1.4.1; supervisor notified (health-gated switch in progress; see logs)
   releases dir: /opt/hatamoto/data/releases
   ```

   The **supervisor**, the process that starts the server and switches versions, makes the switch with the same start-up check as an update. That is what "health-gated" means: the switch only sticks once the release you go back to starts and answers its health check. The release you left is marked as failed on this server, so the agent never installs it again. The command refuses when there is no previous release, or when the previous one is older than the oldest version the running release allows. Add `--force` only to go back to a release that failed before or never ran; it does not lower that limit.

2. Stay on the previous release while you investigate. Pin its line or set the mode to notify only, as in [Pin or turn off updates](pin-or-turn-off-updates.md):

   ```bash
   bin/hatamoto update pin v1.4
   ```

### If the site will not start at all

1. This is rare: the supervisor itself is broken, or the disk is full. Point the `current` link back at the previous release and restart. As the service user:

   ```bash
   cd /opt/hatamoto/data/releases
   ln -sfn "$(readlink previous)" current.new && mv -T current.new current
   ```

   Then, as an administrator:

   ```bash
   sudo systemctl restart hatamoto-api
   ```

   The `mv -T` makes the swap atomic, as the supervisor does. A `current` link you move by hand is used at the next start. With Docker, run `docker compose restart hatamoto` after the swap.

2. If that fails too, reinstall the program file or the image of the version you had before. A program file or image that changed since the last start always runs first, and your data is untouched.

## Check it worked

Run `bin/hatamoto update status`. The `version` line shows the release you went back to, and **Settings > Updates** lists the switch as **Rolled back by hand** under **History**.

When the supervisor rolled back on its own, the status reads `rolled back v1.4.2: <reason>` and owners see a banner in the admin. Your site already runs the previous version, and the fix release installs automatically when it is published. Please open an issue with the `update status` output.

If `rollback` says `is below the rollback floor`, the previous release cannot run on the current database. Restore your database dump together with the program version it was taken with instead. The full list of refusals is in [The update CLI](manual-update-cli.md), and the dump details and restore commands are in [The update agent](update-agent.md).
