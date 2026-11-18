---
title: Deploy on Railway
nav_title: Deploy on Railway
description: Run Hatamoto on Railway with one click: the template adds the database and storage, and you create your owner account from the deploy log.
next: first-run.md, ../operate/email.md, ../operate/configuration-reference.md
---

In this guide you deploy Hatamoto on **Railway**, a hosting service that runs containers for you, so you need no server of your own. The Hatamoto template on Railway creates the site, its Postgres database (where Hatamoto stores everything) and its storage in one step. When you finish, your site answers on a public address and you have created your owner account.

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/hatamoto)

The template is at [railway.com/deploy/hatamoto](https://railway.com/deploy/hatamoto). For prices, backups and command-line steps, see [Deploy Hatamoto on Railway](https://hatamoto.dev/deploy-hatamoto-on-railway).

## Before you start

- A Railway account. Sign up at [railway.com](https://railway.com). The free trial is enough to try Hatamoto; a site you keep needs a paid plan.
- Optional: a domain name you control, if you want the site on your own address instead of a Railway one.
- Optional: the details of an email service (SMTP), so Hatamoto can send password resets and invitations. You can also add them later in the admin.

## Steps

### Deploy the template

1. Click **Deploy on Railway** above, or open [railway.com/deploy/hatamoto](https://railway.com/deploy/hatamoto).
2. Railway shows the two services the template creates: **Postgres** and **hatamoto**. Every required setting already has a value. Leave them as they are, or open **hatamoto** to fill in the optional email settings from the table below.
3. Click **Deploy**. Railway creates a project, starts the database, then starts Hatamoto. The first start takes a minute or two: Hatamoto prepares the database and creates its own server secret.

The template also gives the hatamoto service a **volume**, storage that is kept when the service restarts or is redeployed, mounted at `/data`. Uploads, the server secret, backups and downloaded updates live there.

### Environment variables

An **environment variable** is a named setting that Railway passes to Hatamoto when it starts. You see them on the hatamoto service under **Variables**. The template sets these:

| Variable | Value in the template | What it does |
|---|---|---|
| `DATABASE_URL` | `${{Postgres.DATABASE_URL}}` | Where the database is. Railway fills it in from the Postgres service. Required; leave it. |
| `PORT` | `8080` | The port Railway sends traffic to. Required. |
| `API_PORT` | `${{PORT}}` | The port Hatamoto listens on. Must equal `PORT`. Required. |
| `API_HOST` | `0.0.0.0` | Listen on every network address, so Railway can reach the site. Required. |
| `ENVIRONMENT` | `production` | Turns on secure cookies, HSTS and structured logs. Required. |
| `PUBLIC_BASE_URL` | `https://${{RAILWAY_PUBLIC_DOMAIN}}` | The public address of your site, used in links, sitemaps and emails. Change it when you add your own domain. |
| `MEDIA_UPLOAD_DIR` | `/data/uploads` | Where uploaded files go. Keep it on the `/data` volume. |
| `RAILWAY_RUN_UID` | `0` | Runs the container as root so it can write to the Railway volume. Leave it. |
| `HATAMOTO_TRUSTED_PROXIES` | Railway's private address ranges | Lets Hatamoto see each visitor's real address through Railway's proxy. Leave it. |
| `SMTP_HOST` | empty | Optional. Your email server, for example `smtp.example.com`. |
| `SMTP_PORT` | `587` | Optional. The email server's port. |
| `SMTP_USERNAME` | empty | Optional. The email account's user name. |
| `SMTP_PASSWORD` | empty | Optional. The email account's password or API key. |
| `SMTP_FROM` | empty | Optional. The sender address, for example `hello@example.com`. |
| `SMTP_FROM_NAME` | empty | Optional. The sender name, for example your site's name. |

Two more you may add yourself:

- `HATAMOTO_TRUSTED_CDN=cloudflare` if your domain goes through Cloudflare, so visitor addresses are read correctly.
- `HATAMOTO_SETUP_CODE` to choose your own setup code: at least 12 letters or digits. Without it Hatamoto makes a random one.

Every other setting is in the [Configuration reference](../operate/configuration-reference.md). After you change a variable, Railway asks you to deploy the change; Hatamoto restarts with the new value.

### Create your owner account

Your admin is at a private address made for your install, for example `/k7qm-x2rf-9tdc`, not at `/admin`. The deploy log tells you where it is.

1. Open the **hatamoto** service, then **Deployments**, then **Deploy Logs** of the latest deployment.
2. Search the log for `Setup`. You see a line like this:

   ```text
   Setup: open https://example.up.railway.app/k7qm-x2rf-9tdc/setup and enter code K7M2-RT9P
   ```

   The **setup code** is a one-time code, printed only in the log, so only someone with access to your Railway project can create the first owner.

3. Open the link, enter the code and create your owner account. [First run](first-run.md) walks through the rest of the wizard.
4. Bookmark your admin address: the link without `/setup` at the end.

If you lose the admin address, install the Railway command-line tool, run `railway ssh` in your project, then run `hatamoto admin-url`.

### Use your own domain

1. On the hatamoto service, open **Settings**, find **Public Networking** and click **+ Custom Domain**. Type your domain. Railway shows a `CNAME` record and a `TXT` record: create both at your domain provider. Without the `TXT` record the domain answers 404.
2. Under **Variables**, set `PUBLIC_BASE_URL` to `https://` followed by your domain, for example `https://example.com`, and deploy the change.

Railway issues the HTTPS certificate for you.

## Check it worked

- The hatamoto service shows the latest deployment as **Active**. Railway checks the address `/health` before it switches traffic, so an active deployment is a healthy one.
- The public address (under **Settings**, **Public Networking**) opens the starter home page.
- You can sign in at your admin address with the owner account you created.

If the deployment fails, read the deploy log. A line naming `DATABASE_URL` means the Postgres service is not running or the variable was changed; set it back to `${{Postgres.DATABASE_URL}}`. If the site works but links point at the wrong address, check `PUBLIC_BASE_URL`.

Hatamoto updates itself: it installs signed updates in place, on the `/data` volume, and rolls back an update that fails its health check, so you do not need to redeploy for new versions. See [How updates work](../updates/index.md).
