---
title: Export a site and import it elsewhere
nav_title: Export and import
description: Save all of a site's content to one file and rebuild it on a new server, to move hosts or keep a copy of your content.
next: redirects-after-migration.md, archive-format.md, ../operate/backups.md
---

In this guide you move a Hatamoto site to a new server. You save the whole site's content to one **archive**, a single compressed file, and rebuild the site from it on the new server. The same archive is also a copy of your content that no one can lock away from you.

## Before you start

- A shell on the old server and on the new one, as the user that runs Hatamoto. Export and import run only on the command line; the admin page **Settings > Tools > Export** shows these commands but has no button.
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

## CSV: export and import one content type

A CSV file holds the entries of one content type, one per row, so you can edit them in a spreadsheet or load them from another system. It is separate from the archive above: it carries entries only, no media, menus or settings.

### Export

Open a content type under **Entities** and choose **Export CSV**. Pick the columns and the status to include, then **Download CSV**. Owners and editors can export.

- The fixed columns are `key`, `name`, `slug`, `status`, `published_at`, `locale`, `parent_key`, `order` and `id`. `order` is for reading only and is not imported.
- Each attribute of the type is a column named `attr:` plus its path, for example `attr:price` or `attr:address.city`. A list of values is written with `|` between the items.
- A cell that starts with `=`, `+`, `-`, `@`, a tab or a carriage return gets a leading `'`, so a spreadsheet does not run it as a formula. The import removes that `'` again.
- The file starts with a byte order mark so a spreadsheet reads the unicode text correctly.

### Import

Open **Settings**, **Import**, then **Import a CSV file instead**. Only owners can import. There are three steps, and nothing is stored until the last one.

1. **Upload.** Choose the content type and the file. The first row names the columns. A file you exported works as it is.
2. **Map the columns.** Say what each column means: Key, Name, Slug, Status, Published at, Locale, Parent key, an attribute, or Ignore. Key and Name are required and each target can be used once. Columns named like an export are mapped for you. New rows without a status become drafts, or the status you choose; the choice applies to new rows only.
3. **Dry run, then import.** The dry run runs every row through the real import inside a transaction that is rolled back, and lists the rows that would fail, with the row number (the header is row 1), the column and the reason. Rows with an error are skipped when you import; the others are stored.

The **Key** identifies a row. It is stored as `csv:<type>:<key>`, so importing the same file again updates the entries instead of adding them again. A blank Key is not allowed. An entry that was not created by a CSV import has no such key, so an export writes `id:<id>` for it. A key of that form updates the entry of this content type with that id, never creates one, and fails the row when the id is unknown. That makes an exported file safe to import again unchanged. Keys that start with `id:` are reserved for this. New entries are drafts unless the file says otherwise, so check them before you publish.

An import of a key that exists is an update of what the file carries. A column you do not map and an empty cell both keep the stored value, and so do the status, dates, parent and author of the entry. A cell with JSON text for an object replaces that object, so a key you remove from the text is removed. Clearing a value is not possible through CSV: edit the entry in the admin instead.

Limits: 32 MiB per file, 50 000 rows, and rows are stored in batches of 500. A larger file has to be split. A dry run still runs the `entity.before_save` plugin hook, like a real import.
