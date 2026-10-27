---
title: See how Hatamoto is put together
nav_title: Overview
description: Look at a running Hatamoto server to see what runs, what it answers and where it keeps its state, in about fifteen minutes.
next: how-a-page-is-served.md, where-your-data-lives.md, package-map.md
---

In this guide you look at a running Hatamoto server from the shell and see each of its parts for yourself. At the end you know what process runs, which addresses it answers, what it needs from Postgres and the disk, and where to look when something fails.

The one idea to keep in mind: Hatamoto is one program, `hatamoto`, plus one Postgres database. The public site, the admin, the JSON API, the **MCP** endpoint (the Model Context Protocol, a standard way for AI tools to read and change your site), the background jobs and the plugin runtime all run inside that one process. There is no Node runtime, no Redis, no search server and no separate worker.

## Before you start

- A running Hatamoto install, deployed with systemd ([Deploy with systemd](../operate/deploy-systemd.md)) or Docker ([Deploy with Docker](../operate/deploy-docker.md)).
- A shell on the server, with `sudo` rights.
- `curl` on the server.

The commands below assume the systemd layout: the program at `/opt/hatamoto/bin/hatamoto`, its settings in `/opt/hatamoto/config/hatamoto.env` and its data directory at `/opt/hatamoto/data`. On Docker, skip steps 1 and 2, put `docker compose exec hatamoto` in front of each `hatamoto` command, read the log with `docker compose logs hatamoto`, and look for the data directory at `/data` inside the container.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Open a shell as the service user, with the service's settings loaded. Commands that read the database need `DATABASE_URL` from the settings file, and the data directory must match the service's:

   ```bash
   sudo -u hatamoto bash
   set -a; . /opt/hatamoto/config/hatamoto.env; set +a
   export HATAMOTO_DATA=/opt/hatamoto/data
   export PATH=/opt/hatamoto/bin:$PATH
   ```

2. Look at the process. The unit runs `hatamoto supervise`. The **supervisor** is the process that starts the server, watches it and switches to a new version after an update. It runs `hatamoto serve` as its child, and the child does all the work:

   ```bash
   systemctl status hatamoto-api --no-pager
   ```

   You see two `hatamoto` processes in the unit's tree: `supervise` and `serve`.

3. Print the version of the program you run. The `--json` form adds the plugin SDK version and the newest **migration** the program carries. A migration is a step that updates the database structure to match the new version:

   ```bash
   hatamoto version --json
   ```

   ```text
   {"version":"v1.4.2","commit":"...","sdk":"0.13.0","schema_head":202610251000,"sdk_compatible_with":["0.5.0","0.6.0","0.7.0","0.8.0","0.9.0","0.10.0","0.11.0","0.12.0"]}
   ```

   Your version, commit and schema head will differ.

4. List the addresses the server answers. The route table is fixed when the server starts, so this command needs no database:

   ```bash
   hatamoto routes | head -5
   ```

   ```text
   GET /health	hatamoto.dev/core/internal/server.(*Server).handleHealth-fm
   GET /ready	hatamoto.dev/core/internal/server.(*Server).handleReady-fm
   GET /media/{rest...}	hatamoto.dev/core/internal/server.(*Server).handleServeMedia-fm
   ```

   The full list has about 500 lines: `/api/v1/...` for the JSON API, `<admin>/...` for the admin, `/mcp` for AI tools, `/p/{plugin}/...` for plugins, and `GET /{rest...}` at the end, which serves every public page.

5. Ask the server whether it is up, and whether it can reach the database. It listens on `API_PORT`, 8090 unless you changed it:

   ```bash
   curl -s http://127.0.0.1:8090/health
   curl -s http://127.0.0.1:8090/ready
   ```

   ```text
   ok
   ok
   ```

6. Check the database structure. Migrations are built into the program and run each time the server starts:

   ```bash
   hatamoto migrate status | tail -3
   ```

   Every line should say `applied` and a date. A line that says `pending` means the server has not started on this version yet.

7. Read the start-up lines in the log. They show each part coming up in order:

   ```bash
   journalctl -u hatamoto-api --no-pager | grep -E 'connected to database|schema|scheduler|starting server' | tail -5
   ```

   With `ENVIRONMENT=production` the log is JSON, and the lines end like this (the timestamps are left out here):

   ```text
   "level":"INFO","msg":"connected to database"}
   "level":"INFO","msg":"database schema is up to date"}
   "level":"INFO","msg":"starting scheduler"}
   "level":"INFO","msg":"scheduler started"}
   "level":"INFO","msg":"starting server","addr":"0.0.0.0:8090"}
   ```

   In any other environment the log is plain text, such as `level=INFO msg="starting server" addr=0.0.0.0:8090`.

   `starting scheduler` is the in-process job runner, which publishes scheduled posts, prunes old logs and checks for updates. It is missing when `HATAMOTO_WORKER=off` is set.

8. List the data directory. It holds the state that is not in Postgres:

   ```bash
   ls /opt/hatamoto/data
   ```

   You see `secrets` (the **server secret**, a long random value the server uses to sign links and protect stored credentials), `cache` (compiled plugins, safe to delete) and, once an update has run, `releases` and `backups`. Uploaded media sit in `MEDIA_UPLOAD_DIR`, `/opt/hatamoto/uploads` in this layout. [Find where your data lives](where-your-data-lives.md) goes through each one.

## Check it worked

You can now answer three questions about your server:

- What runs? One `hatamoto` program, started by the supervisor, serving the site, admin, API and jobs.
- What does it need? Postgres (step 5 shows `connected`) and its data directory (step 8).
- What can fail? The database (`/ready` answers 503 with `"database":"unavailable"`), a migration (the server stops at start-up and logs `migrate up:`), or the secret (the server stops with `HATAMOTO_SECRET` in the error).

If `hatamoto migrate status` prints `DATABASE_URL is required`, the settings file was not loaded in your shell: repeat step 1.

The full technology list, the start-up order and the job of each source package are on [Stack and package map](package-map.md).
