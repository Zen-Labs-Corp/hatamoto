---
title: Pin or turn off updates
nav_title: Pin or turn off
description: Stay on one release line, delay updates, pick the hours they may install, or switch automatic updates to notify only or off.
next: update-agent.md, upgrading-self-hosted.md
---

By default your server installs new patch and minor releases on its own. In this guide you change that: stay on one release line, hold releases back for a few days, limit installs to quiet hours, or have the server only tell you about releases or stop checking altogether.

## Before you start

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

- An owner account in the admin. The update settings are on **Settings > Updates** (`<admin>/settings/updates`).
- For pinning, a shell on the server as the service user with the service's settings loaded. With systemd:

  ```bash
  sudo -u hatamoto bash
  set -a; . /opt/hatamoto/config/hatamoto.env; set +a
  cd /opt/hatamoto && export HATAMOTO_DATA=/opt/hatamoto/data
  ```

  With Docker, run each command as `docker compose exec hatamoto hatamoto update <verb>` instead of `bin/hatamoto update <verb>`.

- For locking a setting, access to the service's settings file (`/opt/hatamoto/config/hatamoto.env` with systemd) or to your compose file.

## Steps

### Stay on one release line

1. **Pin** the server to one minor line, so it installs only fixes for that line:

   ```bash
   bin/hatamoto update pin v1.4
   ```

   ```text
   pinned to v1.4 (security releases still apply inside the major)
   ```

   The pin has the form `vMAJOR.MINOR`. While pinned, the server installs only `v1.4.x` releases, plus security releases of the same major version. The admin shows the pin, but you set it only from the command line.

2. Remove the pin when you are ready to move on, with **Unpin** on **Settings > Updates** or:

   ```bash
   bin/hatamoto update unpin
   ```

   ```text
   unpinned
   ```

### Change when and how updates install

1. Open **Settings > Updates** and, under **Update settings**, choose a **Mode**:

   - **Automatic** checks, downloads and installs. This is the default.
   - **Notify only** checks and shows the newest release, but downloads and installs nothing. You install with **Update now** or `bin/hatamoto update apply`.
   - **Off** makes no network request at all.

2. To delay updates, set **Defer updates** to 1 to 14 days. A release then waits that many days after your server first sees it. Security fixes still install at once.

3. To limit installs to quiet hours, set the two hours of **Maintenance window**, the start and the end. The hours are in the site's time zone, shown under the name. A window from 22:00 to 04:00 crosses midnight. Leave both on **Any time**, or set the same hour twice, for no limit. Security fixes ignore the window.

4. To change the **update channel**, the stream of releases you follow, pick `stable` (the default, for live sites), `edge` (2 days early) or `canary` (bleeding edge for testing, may break). The hint next to the field says how many days earlier each channel gets a release.

5. Select **Save settings**.

### Lock a setting on the server

1. To stop anyone from changing the mode in the admin, set it as an environment variable. With systemd, add one line to `/opt/hatamoto/config/hatamoto.env`:

   ```bash
   HATAMOTO_UPDATES=notify
   ```

   With Docker, add `HATAMOTO_UPDATES: notify` under `environment:` in the compose file. The values are `auto`, `notify` and `off`. `HATAMOTO_UPDATE_CHANNEL` locks the channel in the same way.

2. Restart so the server reads it:

   ```bash
   sudo systemctl restart hatamoto-api
   ```

   With Docker, run `docker compose up -d`. The admin now shows **Set by HATAMOTO_UPDATES on the server** and the field cannot be changed there.

## Check it worked

Run `bin/hatamoto update status`. The first line shows the mode and channel in force, after the environment is applied, and a `pin` line appears when you pinned:

```text
version     v1.4.1 (channel stable, mode notify)
pin         v1.4
```

If `update pin` prints `unknown command` followed by the command list, the version had the wrong shape. Use two numbers, such as `v1.4`, not `1.4` or `v1.4.2`.

If the mode shows `off` although you set something else, the value of `HATAMOTO_UPDATES` was not recognised. An unknown value counts as `off` and the server log has a warning, so a typo never leaves updates running. Check the spelling.

When updates have been off for 90 days or more, owners see a banner in the admin as a reminder. Every setting, its default and its exact effect are in [The update agent](update-agent.md).
