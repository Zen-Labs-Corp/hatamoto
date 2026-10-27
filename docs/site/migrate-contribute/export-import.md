---
title: Export a site and import it elsewhere
nav_title: Export and import
description: Save all of a site's content to one file and rebuild it on a new server, to move hosts or keep a copy of your content.
next: redirects-after-migration.md, archive-format.md, ../operate/backups.md
---

In this guide you move a Hatamoto site to a new server. You save the whole site's content to one **archive**, a single compressed file, and rebuild the site from it on the new server. The same archive is also a copy of your content that no one can lock away from you.

## Before you start

- A shell on the old server and on the new one, as the user that runs Hatamoto. Export and import run only on the command line; the admin page **Settings > Import & export > Site export & import** shows these commands but has no button.
- The **database address**, the `DATABASE_URL` setting that tells Hatamoto where its Postgres database is, for each server. Postgres is the database Hatamoto stores everything in. Load the service's settings into your shell so the variable is set.
- On the new server: Hatamoto installed, an **empty** database, and the settings from [Requirements and secrets](../getting-started/requirements-and-secrets.md). The import refuses a database that already has content.
- The same or a newer Hatamoto version on the new server. A new server refuses an archive from a newer version.

## Steps

### Save the archive on the old server

1. On the old server, run the export. It reads `DATABASE_URL` and the media folder `MEDIA_UPLOAD_DIR` (default `./uploads`):

   ```bash
   hatamoto export site.tar.gz
   ```

   ```text
   exported 1834 rows in 19 tables and 212 media files (48211967 bytes) to site.tar.gz
   ```

   The numbers depend on your site. If the export fails, no half-written file is left under the final name.

2. Decide whether the archive may hold visitor data. By default it includes **form submissions**, the messages visitors sent through your forms. If the file will leave your control, export without them instead:

   ```bash
   hatamoto export site.tar.gz --no-submissions
   ```

### Rebuild the site on the new server

1. Copy `site.tar.gz` to the new server, for example with `scp site.tar.gz you@new-server.example.com:`.
2. On the new server, load the service's settings so `DATABASE_URL` points at the new, empty database.
3. Run the import:

   ```bash
   hatamoto import site.tar.gz
   ```

   ```text
   imported 1834 rows in 19 tables and 212 media files
   2 users imported without passwords: set one with `hatamoto user reset-password --email <email>`
   ```

   The import first runs the **migrations**, the steps that set up the database structure for this version, then loads the archive in one go. If anything fails, nothing is kept.

4. Give each user a password. Passwords are not in the archive, so nobody can sign in yet. For each email address, run:

   ```bash
   hatamoto user reset-password --email you@example.com
   ```

   ```text
   password reset for you@example.com
   temporary password: <24 random characters>
   ```

   The temporary password is printed once. Send it to the person through a private channel; they change it after they sign in.

### Finish the new site

1. Install your theme and plugins again. Installed **packages**, the files that hold a theme or a plugin, are not in the archive, because the new server must decide for itself which ones it trusts. Install each with `hatamoto package install <file>.hmp`.
2. Turn your theme on with `hatamoto theme activate <name>`, where `<name>` is the theme's name.
3. Start Hatamoto on the new server and sign in.
4. Change your domain's **DNS records**, the settings that send visitors to a server, to point at the new server once the site looks right. Your addresses stay the same, because the archive holds your redirects and address settings.

## Check it worked

Sign in to the admin on the new server with the temporary password from the reset. Your pages, posts, menus, media and redirects are there. Open a few public pages and check that their images load.

If the import stops with `database is not empty`, the target already has content. Point `DATABASE_URL` at a new, empty database and run the import again.

This archive holds content only. API keys, sessions, statistics and plugin settings are not in it, and it does not replace a full server backup; see [Backups](../operate/backups.md). The full list of what the archive holds and the rules the import follows are in [Archive format](archive-format.md).

If your old site was WordPress rather than Hatamoto, use [Import from WordPress](wordpress-import.md) instead.
