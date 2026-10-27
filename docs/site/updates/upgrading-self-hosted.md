---
title: Upgrade a self-hosted install
nav_title: Upgrading self-hosted
description: Move your own server to a newer Hatamoto version with Docker or systemd, keep your data and secret safe, and confirm the new version runs.
next: ../operate/deploy-systemd.md, upgrade-rules-and-support.md
---

In this guide you move a server you run yourself to a newer version, either by letting the updater install it or by replacing the program file or image yourself. At the end the new version runs, your data and server secret are untouched, and you know how to confirm it.

## Before you start

- A server installed with the shipped systemd unit or the Docker files from [Install with Docker](../getting-started/install-docker.md).
- A shell on the server as the service user with the service's settings loaded, as in [Check for and apply an update](check-and-apply-an-update.md).
- The release notes of the version you are moving to. For a new major version, read them first: you cannot roll back past a major version.

## Steps

### Prepare

1. Read the current state with `bin/hatamoto update status` (with Docker, `docker compose exec hatamoto hatamoto update status`). Note the version and the last result.

2. Make your own backup of the database, the server secret and the uploads, as in [Back up and roll back an update](backups-and-rollback.md). The updater's own dump covers only the moment before its own install.

3. Pick a quiet time, or set a maintenance window on **Settings > Updates** so automatic installs happen only in those hours.

### Upgrade with the updater

1. For a patch or minor release, let the updater install it, or start it now:

   ```bash
   bin/hatamoto update check
   ```

2. For a new major version, which is never installed on its own, install it by name as the service user:

   ```bash
   bin/hatamoto update apply v2.0.0
   ```

   When one is available, `update status` prints a line like this one:

   ```text
   major       v2.0.0 is available; install it with `hatamoto update apply v2.0.0`
   ```

   The admin's **Update now** button installs it too, after asking you to confirm. If your version is too old to move straight to the new one, the command names the version to install first.

### Or replace the image (Docker)

1. Set the new version in the `.env` file next to your compose file, then rebuild and restart:

   ```bash
   docker compose up -d --build
   ```

   An image that changed since the last start always runs first, even over a release the updater installed earlier into the `data` volume.

2. Keep these lines under `volumes:` in your compose file. The `secrets` volume holds the server secret inside `/data`, and without it a new secret is generated:

   ```yaml
   volumes:
     - data:/data
     - secrets:/data/secrets
     - uploads:/app/uploads
   ```

### Or replace the program file (systemd)

1. Download the new program file, put it in place and restart:

   ```bash
   sudo install -m 0755 hatamoto /opt/hatamoto/bin/hatamoto
   sudo systemctl restart hatamoto-api
   ```

   A program file that changed since the last start always runs first, even over a newer release the updater installed. The shipped unit runs `hatamoto supervise`; keep it that way, because without the **supervisor**, the process that starts the server and switches versions, an installed release only takes effect at the next restart.

### Watch the switch

1. Follow the log until the new version passes its start-up check and has run for two minutes:

   ```bash
   sudo journalctl -u hatamoto-api -f
   ```

   With Docker, use `docker compose logs -f hatamoto`. The history is also in `releases/events.jsonl` in the data directory.

## Check it worked

Run `bin/hatamoto update status`, or `bin/hatamoto version`, and confirm the new version. On Docker, also look at `docker compose logs hatamoto` and make sure the server did not generate a new secret: if it did, the `secrets` volume is missing from your compose file.

If the status reads `rolled back`, the new version failed its check and the previous one runs again. Keep running it, read the reason in `update status`, and report it in an issue.

If an `update` command refuses to run as root, or the service cannot write `/data`, a bind-mounted directory is owned by root. Change its owner to the service user (uid 10001 in the image) instead of running as root. Container settings such as the restart policy and stop timeout are listed in [The supervisor](supervisor.md), and the rules behind safe rollback and the support policy are in [Upgrade rules and support](upgrade-rules-and-support.md).
