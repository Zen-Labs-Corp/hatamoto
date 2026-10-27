---
title: Back up and restore
nav_title: Backups
description: Back up and restore a site with one command, or by hand: the database, the server secret and uploaded media, in the right order, and test the restore before you need it.
next: ../updates/backups-and-rollback.md, hardening.md, configuration-reference.md
---

In this guide you back up the three things a Hatamoto site keeps its state in, restore them in the right order, and prove the backup works with a test restore. The commands are examples: adapt hosts, users, paths, retention and encryption to your environment.

## Back up and restore with the CLI

`hatamoto backup` does the whole job with one command each way. It writes one tar file under `$HATAMOTO_DATA/backups` that holds the database, every plugin's own schema, the media files, `secrets.env` (when the server wrote one) and, if you name it, your environment file.

```bash
hatamoto backup create --config /opt/hatamoto/config/hatamoto.env
hatamoto backup list
```

`create` needs `DATABASE_URL` and a `pg_dump` of the server's major version. Every `DATABASE_URL` that the server accepts works: a host list is passed to `pg_dump` as a list, `service` and `servicefile` are ignored in favour of the host, port, user and database the URL resolves to, and parameters the tools cannot take are ignored. Restore is stricter, see below. Run it from the service's working directory, or set `MEDIA_UPLOAD_DIR`. The file holds secrets, so keep it private and encrypt it before it leaves the machine. Owners can also use Settings > Backups in the admin, and Hatamoto takes one before each update.

To restore onto a clean machine, create an empty database and the application role that owns it, run the **same Hatamoto version** that made the backup, and give it the new database in `DATABASE_URL`. Pass a superuser connection string once, for the plugin role setup. `--dry-run` runs every check and writes nothing:

```bash
hatamoto backup restore 20261025T101500Z-manual --dry-run --admin-url postgres://postgres@localhost/postgres --config-out /opt/hatamoto/config/hatamoto.env
hatamoto backup restore 20261025T101500Z-manual --admin-url postgres://postgres@localhost/postgres --config-out /opt/hatamoto/config/hatamoto.env
```

Give `--admin-url` without a password and export `PGPASSWORD` (or use a `~/.pgpass` entry) so the superuser password stays out of the process list and shell history. `PGPASSWORD` also overrides `~/.pgpass` for the app role, so if `DATABASE_URL` has no password, use `~/.pgpass` entries for both roles instead. The id comes from `hatamoto backup list`; a path to a `.tar` works too. The command refuses, and writes nothing, when `--admin-url` is missing, cannot connect, is not a superuser or reaches another cluster, when `DATABASE_URL` names no role or database or carries a list of hosts, `service`, `servicefile` or a query parameter with no libpq environment variable (such as `search_path` or `sslpassword`; `sslmode`, `sslrootcert`, `sslcert`, `sslkey`, `connect_timeout`, `application_name`, `options`, `target_session_attrs`, `require_auth`, `sslnegotiation`, `min_protocol_version`, `max_protocol_version`, `passfile` and the like are passed on, and the pgx-only `pool_*`, `default_query_exec_mode` and cache settings are dropped; a `user` or `dbname` query parameter wins over the URL's user and path), when `--config-out` is not an absolute path in an existing directory, when `PGSERVICE`, `PGSERVICEFILE` or `PGHOSTADDR` is set in the environment (unset them first), when the version differs, when the PostgreSQL major version of the server or of `psql` and `pg_restore` differs from the backup's, when the update signing keys built into the binary differ, when the target database or the media directory is not empty, or when a different `secrets.env` is already in place. It writes the saved config file only where you say: pass `--config-out <absolute path>` (its directory must exist). Without the flag it prints the path recorded in the backup as a hint and writes nothing, and it never overwrites a file. If a step fails, drop and recreate the empty database and empty the media directory before you run it again. Restore into a cluster that no longer holds the original site's plugin roles.

## Manual procedure (fallback)

The rest of this page is the manual way to back up and restore. Use it when the CLI cannot run, or to understand what the CLI does.

## Before you start

- A shell on the server, as a user that can read the data and upload directories.
- `pg_dump` and `pg_restore` whose major version matches your Postgres server.
- The service's `DATABASE_URL` in your shell.
- A place off the machine to copy backups to.

A complete backup has three parts, taken as close together in time as you can:

| What | Where | Why |
|---|---|---|
| The database | the database in `DATABASE_URL` | Content, users, settings, installed themes and plugins, plugin data, redirects, forms, analytics. |
| The server secret | `$HATAMOTO_SECRETS_DIR` (default `./data/secrets`, `/data/secrets` in the image), or your secret manager | Keys flash cookies, theme preview links and stored import credentials, and encrypts the SMTP password saved in Settings > Email. It must stay the same. |
| Uploaded media | `MEDIA_UPLOAD_DIR` (default `./uploads`, `/app/uploads` in the image) | The files behind the media library. The database only stores their records. |

The other files Hatamoto writes, most of which can be rebuilt, are listed in the [configuration reference](configuration-reference.md).

## Steps

### Back up

1. Dump the database in custom format, which is compact and restores selectively. The second command is the Docker Compose version:

   ```bash
   pg_dump --format=custom --no-owner --file hatamoto-$(date +%F).dump "$DATABASE_URL"
   docker compose exec -T db pg_dump -U hatamoto --format=custom hatamoto > hatamoto-$(date +%F).dump
   ```

2. Protect the dumps like the database: tight file permissions and encryption at rest. They hold every user's email, password hashes, form submissions and stored credentials. For large sites, physical backups with point-in-time recovery (your host's tools, or a tool such as pgBackRest) beat logical dumps; Hatamoto only needs the restored database to be complete.

3. Back up the secret. If you set `HATAMOTO_SECRET` (or its old name `JWT_SECRET`), make sure the environment file or secret manager is in your backup. If the server generated it, copy the directory; the file inside, `secrets.env`, has mode 0600, so treat the archive the same way. Under Docker it is the `secrets` volume, and `docker compose cp hatamoto:/data/secrets/secrets.env .` copies it out.

   ```bash
   tar --create --gzip --file hatamoto-secrets-$(date +%F).tar.gz -C /opt/hatamoto/data secrets
   ```

4. Copy the media files, after the database dump: a file with no database row is harmless, but a row whose file is missing shows a broken image. Any file-level tool works: `rsync`, restic, a filesystem snapshot or a volume backup.

   ```bash
   rsync -a --delete /opt/hatamoto/uploads/ backup-host:/backups/hatamoto/uploads/
   ```

5. Run steps 1 to 4 from a scheduler (cron or a systemd timer) as often as you can afford to lose content, copy the files off the machine, and keep several generations.

The dump Hatamoto takes before each update lives on the same disk and only exists around updates, so it does not replace these steps; see [Backups and rollback](../updates/backups-and-rollback.md). Neither does `hatamoto export`: it leaves out password hashes, sessions, API keys, installed packages and plugin data, and is for moving a site ([Export and import](../migrate-contribute/export-import.md)).

### Restore

1. Stop Hatamoto so nothing writes during the restore (`sudo systemctl stop hatamoto-api` or `docker compose stop hatamoto`).
2. Create an empty database and a role for the application, or drop and recreate the existing one.
3. Restore the dump. `--no-owner` with `--role` set to the role in `DATABASE_URL` works when the original owner does not exist on the new server:

   ```bash
   pg_restore --no-owner --role=hatamoto --dbname hatamoto hatamoto-2026-01-15.dump
   ```

4. Put `secrets.env` back in the secrets directory with mode 0600, or set `HATAMOTO_SECRET` to the original value. Without it, stored import credentials and the saved SMTP password cannot be decrypted, and preview links stop working.
5. Restore the media files to `MEDIA_UPLOAD_DIR`, owned by the service user.
6. Start Hatamoto. It applies any **migrations**, the steps that update the database structure to match the program, that the restored database lacks. A database that a newer release migrated further does not stop an older program, which is what makes rolling back one release safe.

Plugins that use their own database role need it created again after a restore into another Postgres cluster: reload or grant the plugin again. Details are under plugin database access in the [configuration reference](configuration-reference.md). For an update that went wrong, rolling back to the previous release is usually faster than a restore.

## Check it worked

After a real restore, run `hatamoto migrate status` and `curl -s http://127.0.0.1:8090/ready`, sign in, open the media library, view a page with an image, and send a test email from **Settings > Email**.

Test the restore before you need it, once a quarter. Restore the latest dump into a scratch database and start a throwaway instance against it with its own settings, so it cannot touch the real site:

```bash
API_PORT=8091 MEDIA_UPLOAD_DIR=/tmp/restore-uploads HATAMOTO_UPDATES=off HATAMOTO_WORKER=off \
HATAMOTO_SECRET=<original-secret> \
DATABASE_URL=postgres://hatamoto:<password>@127.0.0.1:5432/hatamoto_restore?sslmode=disable \
hatamoto serve
```

Run it from an empty directory, because it may create a `./data` directory there. `HATAMOTO_WORKER=off` keeps the copy from running scheduled jobs such as publishing. Sign in on port 8091 and check that your content is there. If `pg_restore` reports a version error, your `pg_restore` is older than the `pg_dump` that wrote the file: use matching client tools.
