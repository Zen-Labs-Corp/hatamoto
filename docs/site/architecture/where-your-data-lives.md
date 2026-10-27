---
title: Find where your data lives
nav_title: Where your data lives
description: Look inside your Postgres database and the server's folders to see where content, settings, files and secrets are kept, and what to back up.
next: data-model.md, ../operate/backups.md, database-migrations.md
---

In this guide you look at the three places Hatamoto keeps its state: the Postgres database, the media folder and the data directory. At the end you know what each one holds, which of them you must back up, and which you can lose without harm.

## Before you start

- A shell on the server, with the service's settings loaded so `DATABASE_URL` is set. [See how Hatamoto is put together](overview.md) shows how, in step 1.
- The `psql` client, version 16 or newer, on the server.
- The paths from your deployment. This guide uses the systemd layout: data directory `/opt/hatamoto/data` (`HATAMOTO_DATA`) and media `/opt/hatamoto/uploads` (`MEDIA_UPLOAD_DIR`). With Docker, the data directory is `/data` and media are in `/app/uploads`, each on its own Docker volume.

Everything here only reads. Nothing you run changes the site.

## Steps

1. Connect to the database with the same address the server uses:

   ```bash
   psql "$DATABASE_URL"
   ```

2. List Hatamoto's tables. Every table the server owns starts with `hatamoto_`:

   ```text
   \dt hatamoto_*
   ```

   You see about 55 tables. Some are monthly parts of one larger table, such as `hatamoto_agent_hits_2026_11`, which holds the visits of AI crawlers for that month. One more table, `goose_db_version`, records which **migrations** have run: the steps that update the database structure to match each new version.

3. See your content types. Hatamoto has no separate table for pages or posts. Each **content type** (the kind of content, which decides its fields and its address) is one row here:

   ```sql
   SELECT slug, name, is_builtin FROM hatamoto_entity_schemas ORDER BY slug;
   ```

   ```text
       slug    |    name    | is_builtin
   ------------+------------+------------
    block      | Block      | t
    page       | Page       | t
    person     | Person     | t
    post       | Post       | t
    theme_part | Theme part | t
   ```

   A fresh site has these five built-in types. Types you add in the admin appear here too, with `is_builtin` set to `f`.

4. Count your content. Every page, post, person and other item is a row in one table, `hatamoto_entities`, with its fields in a JSON column:

   ```sql
   SELECT s.slug AS type, e.status, count(*)
   FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
   GROUP BY 1, 2 ORDER BY 1, 2;
   ```

   ```text
    type |  status   | count
   ------+-----------+-------
    page | published |     1
   ```

   Only `published` rows are visible on the site. Drafts, scheduled and archived items stay here, out of sight.

5. Look for plugin data. A plugin that stores data gets its own schema, named `plugin_<name>`, so its tables never mix with the server's:

   ```text
   \dn
   ```

   On a site without such plugins you see only `public`.

6. Leave `psql`, so the next commands run in your shell again:

   ```text
   \q
   ```

7. List the media folder. Uploaded images and files are stored here, not in the database. The database keeps only a record of each file (name, size, alt text):

   ```bash
   ls /opt/hatamoto/uploads | head
   ```

8. List the data directory:

   ```bash
   ls -la /opt/hatamoto/data
   ```

   Among other things it holds the **server secret**, a long random value the server uses to sign links and protect stored credentials. It must stay the same across restarts. What you may find there:

   | Folder or file | Holds | If you lose it |
   |---|---|---|
   | `secrets/secrets.env` | the server secret, created on first start when you did not set `HATAMOTO_SECRET` | theme preview links and stored credentials, such as the captcha secret, stop working |
   | `cache/wasm` | compiled plugins | nothing: the server rebuilds it |
   | `releases` | the versions the **supervisor** (the process that starts the server and switches to a new version after an update) can switch between, and its state | the next update downloads again; you cannot roll back without it |
   | `backups` | one database copy, `pre-<version>.dump`, taken before each update | only that copy |
   | `install_id` | a random id, kept on this machine, so that not every site gets an update at the same moment | a new one is made, and your turn for updates may move |

   If you set `HATAMOTO_SECRET` in the settings file, it is never written to disk, and no `secrets.env` is created.

## Check it worked

You can now name the three things to back up and where they are:

| What | Where | How |
|---|---|---|
| Content, settings, users, installed plugins and themes | Postgres | `pg_dump` |
| Uploaded media | `MEDIA_UPLOAD_DIR` | copy the folder |
| Server secret | `HATAMOTO_SECRET` in your settings file, or `secrets/secrets.env` | keep a copy with your settings |

Themes and plugins you installed are stored inside the database, so the database backup covers them. The built-in default theme is part of the program itself.

If `\dt hatamoto_*` prints `Did not find any relation`, you connected to a different database than the server. Check that `DATABASE_URL` in your shell matches the settings file.

For a full backup routine, follow [Backups](../operate/backups.md). Every table and column is described on [Data model](data-model.md).
