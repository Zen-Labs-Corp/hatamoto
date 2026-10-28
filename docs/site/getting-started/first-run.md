---
title: First run
nav_title: First run
description: Claim your new site with the setup code, create your owner account and choose its name, language, email, colour, install stats and starting content.
next: create-users.md, ../admin/tour.md, ../admin/content.md
---

In this guide you claim your new Hatamoto site in the browser. A wizard of eight screens creates your owner account and asks for your site's name, address, language, email, colour, install stats and content. At the end you are signed in and your site shows its name.

## Before you start

- Hatamoto running, from [Install the binary](install-binary.md) or [Install with Docker](install-docker.md).
- Access to the server's log, to read the setup code.
- A browser, and an email address and password for your account.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Find the setup code. The **setup code** is a one-time code the server prints to its log so only someone with access to the server can create the first owner. Look for a line containing:

   ```text
   Setup: open /k7qm-x2rf-9tdc/setup and enter code K7M2-RT9P
   ```

   Where to look:

   - The program file: the terminal where `./hatamoto serve` runs.
   - Docker: run `docker compose logs hatamoto | grep -i setup` in the folder with your `docker-compose.yml` file, the file that describes your Docker setup.
   - A service managed by **systemd**, the service manager on most Linux servers: the service log, as shown in [Deploy with systemd](../operate/deploy-systemd.md).

   The code has eight letters and digits in two groups of four. Case and the dash do not matter. A new code is made at each start until the first account exists. If you set `HATAMOTO_SETUP_CODE` yourself, the log does not print a code: use the value you set.

2. Open the setup address from that log line in your browser. If you lost it, run `hatamoto admin-url` to print your admin address and add `/setup` to it, which is `<admin>/setup`. A fresh install opens the wizard there. A bar at the top shows the eight steps: **Your account**, **Admin address**, **Your site**, **Email**, **Look**, **Stats**, **Content** and **Done**.

3. On **Your account**, enter the setup code, your name, your email address and a password of at least 12 characters, twice. Select **Create account**.

   This creates your account with the owner **role**, the set of things an account may do: an owner can do everything. You are signed in at once.

4. On **Admin address**, keep the private address Hatamoto suggests (select **Regenerate** for another, or choose your own) and select **Continue**. This is where you sign in to manage your site; bookmark it. You can always print it on the server with `hatamoto admin-url`. The step shows the address read-only when the server environment fixes it.

5. On **Your site**, fill in the four fields and select **Continue**. You can change all of them later in **Settings**.

   | Field | What to enter |
   |---|---|
   | Site name | Shown in the header and in page titles. Up to 120 characters. |
   | Site address | Your public address, such as `https://example.com`, with no path. You may leave it empty for now. |
   | Language | The one language of your whole site; see [One site, one language](one-site-one-language.md). It starts as `en`. |
   | Time zone | It starts as `UTC`. Pick the zone your site works in. |

6. On **Email**, enter your mail provider's details so password resets and form notices reach people, and select **Save and continue**. Hatamoto checks the connection first and saves nothing if it fails. Or select **Skip** to set email up later at **Settings > Email**, or to keep using the server's `SMTP_*` settings; see [Set up email](../operate/email.md).

7. On **Look**, pick a **Brand colour** for the built-in theme and select **Continue**. It is a six-digit colour code and starts as `#0060df`.

8. On **Help improve Hatamoto** (the **Stats** step), choose **No thanks** or **Send stats**. Nothing is sent unless you choose **Send stats**, and the step shows the exact data before you choose: see [Install stats](../operate/install-stats.md). You can change your mind any time in **Settings > Updates**. The step is left out when the server sets `HATAMOTO_TELEMETRY=off`.

9. On **Content**, choose what your site starts with and select **Finish setup**:

   - **Start empty**: keep only the starter home page.
   - **Install starter content**: adds a sample home page, About and Contact pages, two blog posts, header and footer menus and a contact form. If it stops part way, choose it again: finished items are skipped.
   - **Import from WordPress**: finishes the setup and opens the WordPress import wizard. See [WordPress import](../migrate-contribute/wordpress-import.md).

10. **Done** links to your site, your content, user invitations and settings. When email is set up, it also offers **Send me a test email**.

After **Finish setup** the wizard is closed for good: `<admin>/setup` and the screens before **Done** answer "Page not found". If you leave before that, sign in again and open `<admin>/setup` to go back to **Admin address**.

## Check it worked

Open `http://localhost:8090/`: it shows the site name you chose. Open the address that `hatamoto admin-url` prints: you land in the admin, already signed in.

Your site also serves `robots.txt` and `llms.txt`, which tell search engines and AI tools what they may use, and a **sitemap**, a list of your pages for search engines. On the server, each command prints a short answer:

```bash
curl -i http://localhost:8090/health
curl -s http://localhost:8090/robots.txt
curl -s http://localhost:8090/sitemap.xml | head
```

Links in the sitemap and in emails need your public address: the **Site address** setting, or `PUBLIC_BASE_URL` on the server. Reset and invitation emails are sent only when that address and email are both set up.

If something goes wrong:

- **The code is gone from the log.** Restart the server to get a new one, or set `HATAMOTO_SETUP_CODE` and restart.
- **"That code is not right."** Use the code from the newest log line: older codes stop working after a restart.
- **"Setup already completed".** An account exists. Sign in at `<admin>/login`.
- **You prefer the command line.** Run `hatamoto user create` instead of the first step; see [Create users](create-users.md). Then sign in and open `<admin>/setup` to finish.
- **A forgotten password, or a lock.** An account locks for 15 minutes after 5 failed sign-ins in a row. On the server, `hatamoto user reset-password --email you@example.com` sets a new password and unlocks it.

What Hatamoto creates on its first start, and every rule of the setup code, is in [Requirements and secrets](requirements-and-secrets.md).
