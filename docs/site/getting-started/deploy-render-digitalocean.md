---
title: Deploy on Render or DigitalOcean
nav_title: Deploy on Render or DigitalOcean
description: Start Hatamoto on Render or DigitalOcean App Platform with one click, and learn which of the two keeps your files between restarts.
next: first-run.md, ../operate/email.md, ../operate/configuration-reference.md
---

In this guide you deploy Hatamoto on **Render** or on **DigitalOcean App Platform**, two hosting services that run containers for you, so you need no server of your own. Both start the signed Hatamoto image from `ghcr.io/zen-labs-corp/hatamoto` together with a managed Postgres 16 database, where Hatamoto stores everything. When you finish, your site answers on a public address and you have created your owner account.

[![Deploy to DigitalOcean](https://www.deploytodo.com/do-btn-blue.svg)](https://cloud.digitalocean.com/apps/new?repo=https://github.com/Zen-Labs-Corp/hatamoto/tree/main)

Only the DigitalOcean button is offered. The Render Blueprint is in the repository but has not been tested, see [Deploy on Render](#deploy-on-render).

The two are not equal. Render gives the service a **persistent disk**, storage that is kept when the service restarts or is redeployed, so it suits a site you keep. DigitalOcean App Platform has no persistent storage: every file Hatamoto writes is lost on each deploy or restart. Treat the App Platform button as a way to try Hatamoto. For a DigitalOcean site you keep, use the droplet image described in [deploy/digitalocean/README.md](https://github.com/Zen-Labs-Corp/hatamoto/blob/main/deploy/digitalocean/README.md), or [Install on a VPS](install-vps.md).

## Before you start

- An account with the service you pick. Render needs a paid plan for the disk; the blueprint uses the `starter` web plan and a small paid database.
- Optional: a domain name you control, if you want the site on your own address.
- Optional: the details of an email service (SMTP), so Hatamoto can send password resets and invitations. You can also add them later in the admin.

## Deploy on Render

**This path is unverified.** Hatamoto runs as an unprivileged user (uid 10001), and Render's documentation does not say who owns a persistent disk mounted at `/data`. If the disk is owned by root, Hatamoto cannot write its secret, uploads or updates and restarts in a loop. Nobody has yet deployed this blueprint to confirm. To check, create a new Blueprint from your fork or copy of the repository (**New > Blueprint** in the Render dashboard, pointing at the repository) and read the service log: a line saying it cannot persist a generated secret or write to `/data` means the disk is not writable. If it starts and prints a `Setup` line, it works.

1. In the Render dashboard choose **New > Blueprint** and select the repository. Render reads the blueprint (`render.yaml`) and lists what it will create: the **hatamoto** web service and the **hatamoto-db** database.
2. Click **Apply**. Render starts the database, then Hatamoto. The first start takes a minute or two: Hatamoto prepares the database and creates its own server secret.

The blueprint mounts a 1 GB disk at `/data` and runs a single instance, because a disk can belong to only one instance. Uploads, the server secret, backups and downloaded updates live on the disk. Back up `/data` together with the database, since the secret on the disk is the key to stored credentials.

### Render settings

| Variable | Value in the blueprint | What it does |
|---|---|---|
| `DATABASE_URL` | the `hatamoto-db` connection string | Where the database is. Render fills it in. Required; leave it. |
| `ENVIRONMENT` | `production` | Turns on secure cookies, HSTS and structured logs. |
| `API_HOST` | `0.0.0.0` | Listen on every network address, so Render can reach the site. |
| `API_PORT` | `8090` | The port Hatamoto listens on. |
| `PORT` | `8090` | The port Render sends traffic to. Hatamoto does not read it, so it must equal `API_PORT`. |
| `MEDIA_UPLOAD_DIR` | `/data/uploads` | Where uploaded files go. Keep it on the disk. |

After the first start, open the service's **Environment** page and add `PUBLIC_BASE_URL` with the address of your service, or of your own domain, for example `https://example.com`. Hatamoto uses it in links, sitemaps and emails.

## Deploy on DigitalOcean App Platform

1. Click **Deploy to DigitalOcean** above. App Platform reads `.do/deploy.template.yaml` from the repository and shows the **hatamoto** service and a development Postgres 16 database named `db`.
2. Fill in `HATAMOTO_SECRET` when asked. It must be at least 32 characters. You can make one with `openssl rand -base64 48`. Write it down: it keeps sessions and stored credentials readable across restarts. Without it Hatamoto would make a new secret on every start.
3. Create the app. The first start takes a minute or two.

App Platform storage is ephemeral. Everything under `/data` is lost on each deploy, restart or scale: downloaded updates and rollback state, backups, and files uploaded to the local disk. Keep media off the local disk by storing it on DigitalOcean Spaces or another S3 service. Add these variables to the service:

- `MEDIA_STORAGE=s3`
- `HATAMOTO_MEDIA_S3_ENDPOINT`, `HATAMOTO_MEDIA_S3_REGION` and `HATAMOTO_MEDIA_S3_BUCKET`
- `HATAMOTO_MEDIA_S3_ACCESS_KEY` and `HATAMOTO_MEDIA_S3_SECRET_KEY`

The development database is a small shared one, meant for trying things out. Do not keep a site you care about on it.

### DigitalOcean settings

| Variable | Value in the template | What it does |
|---|---|---|
| `DATABASE_URL` | `${db.DATABASE_URL}` | Where the database is. App Platform fills it in from the `db` component. Required; leave it. |
| `ENVIRONMENT` | `production` | Turns on secure cookies, HSTS and structured logs. |
| `API_HOST` | `0.0.0.0` | Listen on every network address, so App Platform can reach the site. |
| `API_PORT` | `8090` | The port Hatamoto listens on. The template sends traffic to it. |
| `PUBLIC_BASE_URL` | `${APP_URL}` | The public address of your site, used in links, sitemaps and emails. Change it when you add your own domain. |
| `HATAMOTO_SECRET` | you enter it (encrypted) | The server secret, 32 or more characters. |

## Create your owner account

Your admin is at a private address made for your install, for example `/k7qm-x2rf-9tdc`, not at `/admin`. The service log tells you where it is.

1. Open the service's logs: **Logs** on Render, **Runtime Logs** on App Platform.
2. Search the log for `Setup`. You see a line like this:

   ```text
   Setup: open https://example.com/k7qm-x2rf-9tdc/setup and enter code K7M2-RT9P
   ```

   The **setup code** is a one-time code, printed only in the log, so only someone with access to your project can create the first owner.

3. Open the link, enter the code and create your owner account. [First run](first-run.md) walks through the rest of the wizard.
4. Bookmark your admin address: the link without `/setup` at the end.

On App Platform the log is cleared by a restart, so create the account right after the first start.

## Visitor addresses

Both platforms put a proxy in front of Hatamoto. Without help, Hatamoto sees the proxy's address for every visitor. Set the optional `HATAMOTO_TRUSTED_PROXIES` variable to the address ranges of the platform's proxy, a comma-separated list, so Hatamoto reads each visitor's real address. See the [Configuration reference](../operate/configuration-reference.md).

## Check it worked

- The service shows as live or active. Both platforms check the address `/health` before they send traffic, so a live service is a healthy one.
- The public address opens the starter home page.
- You can sign in at your admin address with the owner account you created.

If the deploy fails, read the service log. A line naming `DATABASE_URL` means the database is not running or the variable was changed. If the site works but links point at the wrong address, check `PUBLIC_BASE_URL`.

Hatamoto installs signed updates in place and rolls back an update that fails its health check. On Render the updates are kept on the disk. On App Platform they are lost at the next restart, and the service goes back to the image it was deployed with. See [How updates work](../updates/index.md).
