---
title: Check for and apply an update
nav_title: Check and apply
description: See whether a new Hatamoto release is waiting, find out what installing it would do, and install it now from the server or the admin.
next: backups-and-rollback.md, manual-update-cli.md
---

In this guide you check whether a newer release is available for your server and install it now instead of waiting for the next scheduled check. At the end your site runs the new version, or you know exactly why it did not move.

## Before you start

- A Hatamoto server started by the **supervisor**, the process that starts the server, watches it and switches to a new version after an update. The shipped systemd unit and Docker image both do this. See [See how updates work](overview.md).
- A shell on the server. With systemd, open one as the service user with the service's settings loaded, so the commands find the database and the data directory:

  ```bash
  sudo -u hatamoto bash
  set -a; . /opt/hatamoto/config/hatamoto.env; set +a
  cd /opt/hatamoto && export HATAMOTO_DATA=/opt/hatamoto/data
  ```

  With Docker, run each command below as `docker compose exec hatamoto hatamoto update <verb>` instead of `bin/hatamoto update <verb>`.

- A recent backup of your own. See [Back up and roll back](backups-and-rollback.md).

Never run these commands as root. They refuse to, because files root creates could not be read by the service.

## Steps

1. See what the **update agent**, the part of the server that checks for and downloads new releases, last decided:

   ```bash
   bin/hatamoto update status
   ```

   ```text
   version     v1.4.1 (channel stable, mode auto)
   status      scheduled: v1.4.2 is rolling out (bucket 61, ramp 25%)
   last check  2026-11-02T09:14:07Z ok
   ```

   The `version` line is the release that runs now. A `scheduled` status means a release exists but has not reached your server yet, because releases reach installs gradually. A `major` line appears when a new major version is available.

2. Ask what a check would do now, without installing anything:

   ```bash
   bin/hatamoto update check --dry-run
   ```

   The status line ends with `(dry run: nothing installed)`, and nothing is stored. In the admin, **Settings > Updates > Check now** does the same and stores the result, so the page shows it.

3. Install what your settings allow:

   ```bash
   bin/hatamoto update check
   ```

   This is the same as a scheduled check, run now. It respects the release's gradual rollout, your maintenance window, your defer days and your **pin**, which keeps the server on one release line (see [Pin or turn off updates](pin-or-turn-off-updates.md)). When it installs, the agent first saves a database dump to `$HATAMOTO_DATA/backups/pre-<version>.dump`.

4. Or install one named version now, whatever your settings say:

   ```bash
   bin/hatamoto update apply v1.4.2
   ```

   ```text
   version     v1.4.1 (channel stable, mode auto)
   status      applying: staged v1.4.2; supervisor notified (health-gated switch in progress; see logs)
   last check  2026-11-02T09:20:31Z ok
   ```

   This route ignores the gradual rollout, the window, the defer days, the pin and even `HATAMOTO_UPDATES=off`. It is also how you install a new major version. It still refuses a withdrawn release and one that already failed on this server. It does not take the database dump, so make your own backup first.

   In the admin, **Update now** appears on **Settings > Updates** when a release is waiting. It installs that release, takes the database dump first when it can, and asks you to confirm a major version.

5. Watch the switch. The supervisor stops the old version, starts the new one and checks that it starts and answers, which takes up to about three minutes:

   ```bash
   tail -f /opt/hatamoto/data/releases/events.jsonl
   ```

   A line with `"phase":"success"` means the new version passed. With Docker, read `docker compose logs -f hatamoto`.

## Check it worked

Run `bin/hatamoto update status` again. The `version` line shows the new release, and **Settings > Updates** lists the switch as **Updated** under **History**.

If the status reads `blocked`, the reason names what stopped it: a plugin or theme that needs a different version, too little disk space (the agent needs three times the program's size free in the data directory), or a version so old that an intermediate release must be installed first. Fix the cause and run `bin/hatamoto update check` again.

If it reads `rolled back`, the new version failed its check and your site is back on the previous one. Follow [Back up and roll back](backups-and-rollback.md).

If `apply` answers `is on this box's bad list`, that version already failed here; wait for the next release. Every message and flag is in [The update CLI](manual-update-cli.md).
