---
title: See how updates work
nav_title: Overview
description: Get a working picture of how your server finds, checks, installs and, if needed, undoes a new Hatamoto release.
next: check-and-apply-an-update.md, backups-and-rollback.md
---

In this guide you look at the parts of your server that handle updates, one at a time, so you know what happens when a new release comes out and where to look when something goes wrong. It takes about ten minutes and changes nothing.

## Before you start

- A Hatamoto server installed with the shipped Docker image or the shipped systemd unit. See [Deploy with Docker](../operate/deploy-docker.md) or [Deploy with systemd](../operate/deploy-systemd.md).
- An owner account in the admin.
- A shell on the server, as the user that runs the service (`hatamoto` with systemd, the container's `app` user with Docker).

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Learn the two parts. The **update agent** is the part of the server that checks for and downloads new releases. The **supervisor** (`hatamoto supervise`) is the process that starts the server, watches it and switches to a new version after an update. The shipped unit and image both start the supervisor, which starts the server as its child.

2. Learn what a check does. About every 6 hours (give or take 30 minutes, the first one a few minutes after the server starts) the agent downloads one file, the list of current releases for your **update channel**, the stream of releases you follow. The default channel is `stable`:

   ```text
   https://hatamoto.dev/releases/channels/stable.json
   ```

   It is a plain anonymous request with no query string, cookie or install identifier. Nothing about your site is sent, and Hatamoto sends no other telemetry.

3. Learn what an install does. When the list offers a newer patch or minor release of the major version you run, the agent downloads it, checks its signature against keys built into your program, takes a database dump and hands it to the supervisor. Not every site gets a release at the same moment: releases reach installs gradually over a few days. A new major version is never installed on its own. [Trust and signing](trust-and-signing.md) lists every check.

4. Learn what a switch does. The supervisor stops the old version, starts the new one and runs a check that the new version starts and answers. If it passes, the new version stays. If it fails, the supervisor puts the previous version back, marks the failed one so it is never fetched again, and the admin shows a banner. This **rollback**, going back to the previous version, swaps program files only and never touches the database.

5. Open **Settings > Updates** in the admin (`<admin>/settings/updates`, owners only). The page shows your version and channel, the last check, the update settings and a history of past updates. The fields there are covered in [Pin or turn off updates](pin-or-turn-off-updates.md).

6. On the server, run the status command. With systemd, load the service settings first so the command finds the database:

   ```bash
   sudo -u hatamoto bash -c 'set -a; . /opt/hatamoto/config/hatamoto.env; set +a
     cd /opt/hatamoto && HATAMOTO_DATA=/opt/hatamoto/data exec bin/hatamoto update status'
   ```

   With Docker:

   ```bash
   docker compose exec hatamoto hatamoto update status
   ```

   ```text
   version     v1.4.1 (channel stable, mode auto)
   status      up to date: running the newest release offered
   last check  2026-11-02T09:14:07Z ok
   ```

   On a new install the status line reads `no check yet` and the last check reads `never` until the first check has run.

7. Look at the releases directory, where the supervisor keeps each installed version. It sits in the data directory, `HATAMOTO_DATA` (`/opt/hatamoto/data` in the unit, `/data` in the image):

   ```bash
   ls -l /opt/hatamoto/data/releases
   ```

   After the first update you see one directory per version and two links, `current` and `previous`. Before that, the server runs the program it was installed with and the directory may be empty. [The supervisor](supervisor.md) describes every file.

8. Read the history of switches. Each outcome is one line of JSON:

   ```bash
   tail -n 5 /opt/hatamoto/data/releases/events.jsonl
   ```

## Check it worked

You can now answer three questions for your own server: which version runs and on which channel (step 6), when the last check ran and what it decided (step 6 or the admin page), and where past updates are recorded (step 8).

If `update status` says `no supervisor state under ...`, the command did not find the data directory: run it from `/opt/hatamoto` or set `HATAMOTO_DATA` as shown. If it says `DATABASE_URL is required`, load the service's settings first. The full list of messages is in [The update CLI](manual-update-cli.md), and every setting and default is in [The update agent](update-agent.md).
