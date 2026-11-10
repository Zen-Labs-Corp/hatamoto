# Backups

Hatamoto can back itself up: from Settings > Backups & recovery > Backups in the admin, from the command line, and
automatically before an update. This page says what a backup holds, where it lives, and how to
restore one: `hatamoto backup restore` rebuilds a clean machine in one command, and the manual
procedure further down is the fallback. The server never restores a backup by itself; restoring is
always a command you run.

## What one backup contains

One backup is one tar file, `<id>.tar`, with these parts:

| Part | What it is |
|---|---|
| `core.dump` | `pg_dump --format=custom` of the whole database except the plugin schemas and the privileged `hatamoto_admin` schema. |
| `plugin_<name>.dump` | One `pg_dump` per plugin schema (`plugin_<name>`), so a plugin's own tables travel with the backup. |
| `content.tar.gz` | The content export (the same format as `hatamoto export`: content, menus, settings, media files). |
| `media-manifest.json` | Only for a site whose media is in object storage (`MEDIA_STORAGE=s3`): `{"version":1,"files":[{"key","size","sha256"}]}`, one entry per file the media library references, with the object name relative to the bucket prefix. A referenced file that is not in the bucket is left out and counted in `media_missing`. It is a normal part: listed in `parts` and written before `manifest.json`. |
| `secrets.env` | A verbatim copy of `<secrets dir>/secrets.env` (`HATAMOTO_SECRETS_DIR`, default `./data/secrets`), when that file exists. If the secret lives only in the environment (`HATAMOTO_SECRET` set, nothing written to disk) there is nothing to copy and the part is left out. |
| `config.env` | A verbatim copy of your environment file, when you name one with `--config` or `HATAMOTO_CONFIG_FILE` (see "Taking a backup"). Left out otherwise. |
| `manifest.json` | What is in the archive and where it came from (below). |

`manifest.json` fields: `format`, `version`, `created`, `kind` (`manual`, `pre_update` or `scheduled`), `target`
(the release a pre-update backup was taken for), `hatamoto_version`, `server_major` (the PostgreSQL
major version), `pg_dump` (the client version), `parts` (name, size and SHA-256 of each file),
`plugins` (the plugin schemas that were dumped), `content_rows` (rows per table in the content
export), `media_files`, `media_bytes` and `media_missing` (referenced files that were not on disk or in the bucket; left out when 0), `media_storage` (`s3` when the media was in object storage; left out for local disk),
`trust` (the key IDs of the release and manifest signing keys built into the binary that made the
backup, and of the keys in `HATAMOTO_TRUSTED_KEYS`) and `config_path` (the absolute path the config file had; restore shows it as a hint and never writes there).
Backups made before `trust`, `secrets.env` and `config.env` existed lack them; they still restore,
with warnings.

### Media in object storage

With `MEDIA_STORAGE=s3` the archive holds the manifest instead of the media bytes: `content.tar.gz` has no `media/` members, and `media_files` and `media_bytes` stay 0. Bucket versioning is the media history. `hatamoto backup create --with-media` also copies the bytes into `content.tar.gz` under `media/`, as for local disk. `backup create` and the backup taken before an update read the bucket with the same `HATAMOTO_MEDIA_S3_*` variables as the server; invalid settings fail the backup with the reason. The free-space check counts the media directory only for local media.

Restore with `MEDIA_STORAGE=s3` and the same variables set:

- An archive with a media manifest and no media bytes is checked against the bucket, size and SHA-256 per object (an object with no recorded checksum is read and hashed). Missing and differing objects are listed as warnings, in `--dry-run` too. They never stop the restore. An object with a matching size and recorded checksum is not read again.
- An archive with media bytes is written into the bucket. The bucket prefix must be empty: the restore refuses otherwise, and says how many objects it found. The plan line then reads "restore N media files to object storage".
- A manifest-only archive restored without `MEDIA_STORAGE=s3` cannot check anything and warns.

Fonts the owner added live in the database table `hatamoto_fonts`, so they travel with every backup, export and import.

## Where backups are kept

Under `$HATAMOTO_DATA/backups`. The directory is mode 0700 and every file in it is 0600. A backup
runs `pg_dump`, so the `pg_dump` on the server's `PATH` must have the same major version as the
database server; a mismatch fails the backup with that message.

Retention keeps the newest finished backups of each kind and removes older ones. Each kind has its
own count, from 1 to 50, resolved in this order:

1. The environment variable, when it is set to a whole number of 1 or more. A number above 50 counts as 50 (the page shows 50 and a warning is logged once).
2. `$HATAMOTO_DATA/backups/retention.json`, which the owner writes from Settings > Backups & recovery > Backups > Backups to keep (the ⋯ menu on the Backups page).
3. The default.

| Variable | Default | Meaning |
|---|---|---|
| `HATAMOTO_BACKUP_KEEP` | `10` | Manual backups kept. |
| `HATAMOTO_BACKUP_KEEP_PRE_UPDATE` | `5` | Pre-update backups kept. |
| `HATAMOTO_BACKUP_KEEP_SCHEDULED` | `7` | Scheduled backups kept (the ones the off-site schedule takes). |

An environment value that is zero, negative or not a whole number is ignored (with a warning in the log), and so is a file value that is not a whole number from 1 to 50; the next source in the order is used. The file looks like
`{"manual":10,"pre_update":5,"scheduled":7}`; a missing key means the default, and unknown keys are
ignored. Failed and running backups are never counted and never pruned.

Owners set the three counts on Settings > Backups & recovery > Backups. A count fixed by an environment variable shows as
a read-only field that names the variable; change it in the server config. A save that would delete
backups first asks you to confirm and says how many of each kind go (the oldest finished ones); a
save that deletes nothing applies at once. Saving prunes straight away, and every finished backup
prunes by the counts as they are at that moment, so a change needs no restart. `hatamoto backup
create` prunes by the same counts and, like the file, works with the database down. The file is mode
0600, replaced atomically, never part of a backup archive, and a restore never writes it.

Before it starts, a backup checks free space: the dump parts and the finished archive sit side
by side in the backups directory, so it needs twice the database size plus the media files, plus
64 MB of margin. The temp directory (the content export spools there) needs one and a half times that
size. If either is short, the backup is refused with the numbers. Only one backup runs at a time: a
second manual request is refused while one is running, and a pre-update backup waits its turn.

**A backup holds secrets.** It carries password hashes, API keys, sessions, the credentials of
plugin database roles (`hatamoto_plugin_credentials`) and everything else in the database, plus
`secrets.env` (when present) and your config file if you stored one. Treat
the file like the database itself: keep it private, and encrypt it before it leaves the server.
Anyone who can read a backup can read all of that.

Backups made before this feature (`$HATAMOTO_DATA/backups/pre-<version>.dump`) are left alone: they
are not listed, pruned or deleted. Delete them by hand when you no longer need them.

## Taking a backup

- **Admin:** Settings > Backups & recovery > Backups (owners only). "Back up now" starts one; the page shows progress,
  lists the backups with their size, and offers a download. The Off-site copy card on the same page
  sets up the upload described below.
- **CLI:** `hatamoto backup create [--config <path>] [--with-media]` prints the media directory it reads, then the id, size and path,
  and names `secrets.env` and `config.env` when they went in. `hatamoto backup list [--json]`
  and `hatamoto backup delete <id>` work with the database down. Run them as the service user with
  `HATAMOTO_DATA` set. Run `backup create` from the service's working directory (the media default
  `./uploads` is relative to it) or set `MEDIA_UPLOAD_DIR`. If the media directory is missing, or
  none of the files the database references are in it, the backup fails instead of leaving the media
  out. If only some files are missing (an upload lost earlier), the backup succeeds without them. The
  Backups page shows "N media files missing" under the OK badge, `hatamoto backup list` shows it in
  the NOTE column (`media_missing` in `--json`), and the log names up to 10 of the paths.
  `backup create` and the backup before an update accept every `DATABASE_URL` that `serve` accepts. A
  host list is passed to the tools as `PGHOST` and `PGPORT` lists, `service` and `servicefile` are
  ignored (the host, port, user and database the URL resolves to are used), keys with a libpq
  environment variable are passed on, and the pgx-only and other unknown keys are ignored.
  See [CLI.md](CLI.md).
- **Config file:** `--config <path>` stores your environment file (the one your service manager or
  compose file loads) verbatim as `config.env`. When the flag is not given, the path in
  `HATAMOTO_CONFIG_FILE` is used if it is set. Hatamoto reads that variable only to make backups; it
  does not load the file. A named file that cannot be read fails the backup, and with neither set
  there is no `config.env`. Pre-update backups take no flags, so they use `HATAMOTO_CONFIG_FILE` only.
- **Before an update:** see [UPGRADES.md](UPGRADES.md#automatic-updates-self-hosters).

## Off-site copy

A backup on the same machine as the site does not survive losing the machine. Settings > Backups & recovery > Backups has
an **Off-site copy** card that uploads each scheduled backup to an S3-compatible bucket (Amazon S3,
Cloudflare R2, Backblaze B2, MinIO or any server that speaks the S3 API), proves the copy there, and
removes the oldest copies beyond the number you keep. Owners only. It needs no extra software on the
server: the client is part of the binary.

**What is uploaded.** The same tar a local backup produces, byte for byte, under the key
`<prefix><id>.tar` (for example `prod/20261105T030000Z-scheduled.tar`). Only scheduled backups and
"Back up and upload now" are uploaded. Pre-update backups stay on the machine.

**Schedule.** Off, every day at a chosen hour, or every week on a chosen day and hour. Times are UTC.
Each run takes a local backup of kind `scheduled` (kept on disk per the scheduled count above: `HATAMOTO_BACKUP_KEEP_SCHEDULED`, `retention.json` or the Settings > Backups & recovery > Backups > Backups to keep form (the ⋯ menu on the Backups page))
and then uploads it while it still holds the backup lock, so an upload never overlaps another backup
or an update's backup. If another backup is running when a slot falls due, the run waits 10 minutes
and tries again; that is not recorded as a failure. A slot is claimed in the database, so with several
replicas only one runs it, and a restart does not repeat it. The scheduler job is `backup_offsite`
(`WORKER_DISABLE_JOBS` can switch it off).

**Verification.** The upload sends the real SHA-256 of each request body, so the provider rejects bytes
that do not match. The tar's own SHA-256 is stored on the object (`x-amz-meta-sha256`). After the
upload hatamoto reads the object's size and that value back; a copy that does not match is deleted and
the run is recorded as failed. This is a transfer check, not proof of authenticity: anyone with
write access to the bucket can replace the tar and its recorded checksum together, so keep the write
key private. The result (time, size, checksum or error) shows on the card, in the
Off-site column of the backups table and in the dashboard health card. The health row is red when the
last upload failed and amber when it is older than one and a half schedule periods (36 hours for daily,
10 days for weekly).

**Retention.** After an upload whose size and checksum read back correctly the bucket is pruned to the newest N copies (1 to 365). Only
keys of the form `<prefix><backup id>.tar` count. Anything else under the prefix, such as other files
or nested folders, is never listed for deletion.

**Settings.** Service, endpoint (required except for Amazon S3, which derives it from the region),
region (Cloudflare R2 uses `auto`), bucket, folder prefix, access key, secret key, schedule and copies
to keep. The endpoint must use `https` unless it is a local or private address. Redirects are not
followed. The secret key is stored sealed with the server secret, is never shown again, and never
appears in the page, the audit log, the log or the backup sidecar; leave the field blank to keep the
saved key. "Test connection" checks the bucket with the values in the form without saving them.
Changing the server secret makes the saved key unreadable; enter it again.

**Audit.** `backups.offsite.update` (names of the changed fields and whether the key changed, never
values), `backups.offsite.run` ("Back up and upload now") and `backups.offsite.upload` (each result).

**Security.** The tar holds your database and `secrets.env`. Use a private bucket with server-side
encryption turned on, and an access key limited to that bucket (write, read, list and delete on the
prefix). Hatamoto does not encrypt the tar itself. A clock that is more than a few minutes off makes
the provider answer `RequestTimeTooSkewed`; fix the server's time.

### Restoring from the bucket on a clean machine

`hatamoto backup restore` leaves the off-site card switched off on the restored install. A restored
copy shares the original's bucket and prefix, and its first scheduled upload would add to, and then
prune, the original's copies. Before you turn it on again in Settings > Backups & recovery > Backups, change the prefix
or be sure the original is gone. A restore with the manual `pg_restore` steps does not do this:
run `UPDATE hatamoto_offsite_settings SET enabled = false` before you start the server, or switch the card off as soon as it is up.

The command line never reads the saved settings (the machine has no database), so name the target in
the environment:

| Variable | Meaning |
|---|---|
| `HATAMOTO_OFFSITE_ENDPOINT` | Endpoint URL. Empty means Amazon S3 in the region. |
| `HATAMOTO_OFFSITE_REGION` | Region (required). |
| `HATAMOTO_OFFSITE_BUCKET` | Bucket (required). |
| `HATAMOTO_OFFSITE_PREFIX` | Key prefix, as in the card. |
| `HATAMOTO_OFFSITE_ACCESS_KEY` | Access key (required). |
| `HATAMOTO_OFFSITE_SECRET_KEY` | Secret key (required). Environment only: there is no flag, so it stays out of process lists and shell history. |

The flags `--offsite-endpoint`, `--offsite-region`, `--offsite-bucket`, `--offsite-prefix` and
`--offsite-access-key` override their variables.

```bash
hatamoto backup list --remote
hatamoto backup restore --from-offsite 20261105T030000Z-scheduled --dry-run --admin-url postgres://postgres@localhost/postgres
hatamoto backup restore --from-offsite 20261105T030000Z-scheduled --admin-url postgres://postgres@localhost/postgres
```

`--from-offsite <id>` replaces the id or path argument. The tar is downloaded to
`$HATAMOTO_DATA/backups/.tmp-<id>.tar`, its SHA-256 is compared with the one recorded on the object
when it was uploaded, and then the normal restore checks and steps run on it. The temporary file is
removed afterwards, also after `--dry-run` or a failure, which writes nothing else.

## Docker

The image ships `pg_dump` from the PostgreSQL apt repository, major 16 to match the `postgres:16`
in the shipped `docker-compose.yml`. `pg_dump` refuses a server newer than itself, and a client
older than the server major is not supported here either, so if you run another PostgreSQL major,
rebuild the image with `docker build --build-arg PG_MAJOR=<n> .` (in compose, `build.args`). Without
a usable `pg_dump` every backup fails, and so does every update that takes one first.

## Restoring a backup

### Restoring with the CLI

`hatamoto backup restore` rebuilds a clean machine from one backup: the core database, each
plugin's database role, schema and data, the media files, `secrets.env` and the config file. It
never runs on a live server and never starts one.

Before you run it:

1. Run the **same Hatamoto version** that made the backup (the manifest's `hatamoto_version`).
   Restoring into another version is refused; restore with the matching release, then update.
2. Have a server whose major is the backup's `server_major` or newer (a PostgreSQL 16 backup restores
   into a PostgreSQL 18 server; a backup from a newer major than the server is refused, and so is one
   from before PostgreSQL 16). `psql` and `pg_restore` on `PATH` must have the major of the
   server you restore into, not of the backup: `pg_restore` reads older dumps but not newer ones.
   The Docker image has them.
3. Create the app role and an empty database it owns (as a superuser). The role name may use
   lowercase letters, digits and underscores:
   `CREATE ROLE "<app role>" LOGIN PASSWORD '...'; CREATE DATABASE "<db>" OWNER "<app role>";`
4. Use a cluster that no longer has the original plugin roles (`hm_p_<id>_<name>`) or the
   `hatamoto_role_admin_<id>` role: those names are cluster-wide, and restoring into the cluster
   that still holds the original site is not supported.
5. Run as the service user with `DATABASE_URL` set to the **app role** on the empty database,
   `HATAMOTO_DATA` (where `backups/<id>.tar` lives), and `MEDIA_UPLOAD_DIR` and `HATAMOTO_SECRETS_DIR`
   pointing at where the media and `secrets.env` should go, as for `serve`.

```sh
hatamoto backup restore 20261025T101500Z-manual --dry-run --admin-url postgres://postgres@localhost/postgres --config-out /opt/hatamoto/config/hatamoto.env
hatamoto backup restore 20261025T101500Z-manual --admin-url postgres://postgres@localhost/postgres --config-out /opt/hatamoto/config/hatamoto.env
```

| Argument | Meaning |
|---|---|
| `<id\|path.tar>` | A backup id from `hatamoto backup list`, or the path of a `.tar` (anything ending in `.tar` or holding a `/` is a path). |
| `--admin-url <DSN>` | Required. A superuser DSN, used only to run the role bootstrap (`deploy/sql/role-admin-bootstrap.sql`, built into the binary). Restore passes the tools the DSN's parts through `PG*` environment variables, so no password is on their command lines. The `--admin-url` you type is on restore's own command line, though, and visible in the process list and your shell history: leave the password out (`postgres://postgres@host/postgres`) and give it through `PGPASSWORD` or a `~/.pgpass` entry, which the tools and restore's own checks read. A password in the DSN still works. `PGPASSWORD` beats `~/.pgpass` for every connection restore makes, including the app role's, so if `DATABASE_URL` has no password of its own, use `~/.pgpass` entries for both roles rather than `PGPASSWORD`. Restore hands the tools the DSN's host, port, user, database and password, plus the query parameters that have a libpq environment variable (`sslmode`, `sslrootcert`, `sslcert`, `sslkey`, `channel_binding`, `krbsrvname`, `connect_timeout`, `application_name`, `options`, `target_session_attrs`, `require_auth`, `sslnegotiation`, `min_protocol_version`, `max_protocol_version`, `passfile`). pgx-only parameters (`pool_*`, `default_query_exec_mode`, `statement_cache_capacity`, `description_cache_capacity`, `min_read_buffer_size`) are dropped. Restore refuses a list of hosts, `service` and `servicefile`, and any query parameter with no libpq environment variable (for example `search_path` or `sslpassword`), in `DATABASE_URL` as well, because it must write to the one server it checked. `backup create` refuses none of these. A `user` or `dbname` query parameter wins over the URL's user and path, as it does for the server. Restore also refuses while `PGSERVICE`, `PGSERVICEFILE` or `PGHOSTADDR` is set in the environment, because libpq would let them send the tools to another server than the one checked; unset them first. |
| `--dry-run` | Verify the archive and run every check, print the plan, write nothing. |
| `--config-out <path>` | Where to write the saved config. An absolute, clean path whose directory already exists; restore creates no directories for it. Without it the config is not written: a warning names the recorded `config_path` and the flag to pass. The recorded path is a hint only, because a backup archive must not choose where this machine writes a file. |

A dry run prints what the real run would do:

```
backup   20261025T101500Z-manual (hatamoto "v0.9.3", pg 16, 1 plugin)
checks   version ok, pg major ok (backup 16, server 18), trust keys ok, admin url ok, target db empty, media dir empty
would    restore core.dump, run role bootstrap, provision plugin_forms, restore plugin_forms.dump,
         restore 42 media files (3.1 MiB), write secrets.env,
         write config to /opt/hatamoto/config/hatamoto.env
dry run: nothing written
```

The real run prints `done` instead of `would` and ends with `restore complete`. Warnings print as
`warning  <text>` lines before `checks`. The order is: core dump, role bootstrap, each plugin
(role and schema, then its data), media, `secrets.env`, config.

**Refusals.** Every check runs first; all reasons are reported together, the command exits non-zero,
and nothing is written:

| Refusal | Meaning |
|---|---|
| version | The backup's `hatamoto_version` differs from this binary's, or either is empty. |
| PostgreSQL major | The backup is from a newer major than the server, or from before PostgreSQL 16; or `pg_restore` or `psql` does not have the server's major. Each is reported on its own. |
| signing keys | The release or manifest key IDs built into this binary differ from the ones recorded. Run the release that made the backup. |
| admin URL | `--admin-url` is missing, is not a postgres URL, cannot connect to the target database, is not a superuser, or reaches a different PostgreSQL cluster than `DATABASE_URL`. |
| `DATABASE_URL` | It names no app role or no database, the role has an unusual name, it is not a postgres URL, or it holds a host list, `service`, or a query parameter the tools cannot be given. |
| `--config-out` | The path is not absolute and clean, or its directory does not exist. |
| target database not empty | Any table, view, sequence or similar outside the system schemas, including `hatamoto_admin` from an early bootstrap. The message names the schemas. |
| media directory not empty | `MEDIA_UPLOAD_DIR` holds any file or directory. A missing directory counts as empty. |
| media prefix not empty | With `MEDIA_STORAGE=s3` and an archive that holds media bytes: the bucket prefix already holds objects. |
| different `secrets.env` | The secrets directory already holds a `secrets.env` with other contents. The same contents are accepted. |
| unsafe media path | The archive holds a media file that would land outside the media directory. |
| damaged archive | A part is missing or does not match its SHA-256 in the manifest. |

**Warnings** do not stop a restore: an old backup without `trust`, `secrets.env` or `config.env`;
`HATAMOTO_TRUSTED_KEYS` here differing from the recorded key IDs (the restored config carries the
backup's); no `secrets.env` in the backup (set `HATAMOTO_SECRET` yourself); a config that is not
written because `--config-out` was not given (the warning names the recorded path) or the target
already exists. Restore never overwrites a config file, and
writes `secrets.env` with mode 0600 in a 0700 directory.

**If a step fails** the error names the step reached. Nothing is rolled back: drop and recreate the
empty database (and empty the media directory), then run the command again. Plugin roles made by a
failed run belong to the cluster; drop them before retrying on the same cluster.

### Manual procedure (fallback)

Use this when `hatamoto backup restore` cannot run, for example on a machine where you cannot install
the matching release. Tested on PostgreSQL 16. The installer installs PostgreSQL 18 by default and checks that `pg_dump` matches the installed major. The steps below were run end to end on a fresh PostgreSQL 16 cluster
with one plugin installed. Use a `pg_restore` and `psql` of the same major version as the server you restore into; the server may be newer than the one that made the backup.
`<app role>` is the role the application connects as (it owns the database and has no superuser,
CREATEDB or CREATEROLE). `<db>` is the new database. Run the Hatamoto commands as `<service-user>`.

1. Unpack the archive: `tar -xf <id>.tar`.
2. Create the app role (if the cluster is new) and an empty database owned by it:
   `CREATE ROLE "<app role>" LOGIN PASSWORD '...'; CREATE DATABASE "<db>" OWNER "<app role>";`
3. As the app role, restore the core dump:
   `pg_restore --no-owner --role=<app role> -d <db> core.dump`
4. As a superuser, run the role-admin bootstrap. It adopts the instance id that step 3 restored, so
   it must come after step 3:
   `psql -d <db> -v app_role=<app role> -f deploy/sql/role-admin-bootstrap.sql`
5. Start `hatamoto serve` once against the restored database and stop it again. The server creates
   each plugin's database role and schema (the restored `hatamoto_plugin_credentials` keeps the
   role passwords) and runs the plugin migrations, which create empty tables.
6. For each plugin part, connect as the app role and restore it into the schema from step 5. The
   plugin dump also holds `CREATE SCHEMA`, which the plugin role may not run, so drop that entry
   from the list first:
   ```sh
   pg_restore -l plugin_<name>.dump | grep -v ' SCHEMA - ' > plugin_<name>.list
   pg_restore -L plugin_<name>.list --clean --if-exists --no-owner \
     --role=<plugin role> -d <db> plugin_<name>.dump
   ```
   `<plugin role>` is the role step 5 created for that plugin (`hm_p_<id>_<name>`, visible in
   `\du`, or as the owner of the `plugin_<name>` schema).
7. Files that visitors uploaded to forms are in neither the database dump nor `content.tar.gz`
   (see "Form uploads"). Copy `<data>/private/form-uploads` back if you saved it.
8. The database dump holds no media files. They are in `content.tar.gz` under `media/`; copy them
   back to `MEDIA_UPLOAD_DIR` if that directory is new, then start the service.

The restored site keeps its users and passwords. Restoring leaves `backups/retention.json` as it is.

### Form uploads

A form with a file field stores each upload under `<data>/private/form-uploads/<form id>/`
(`HATAMOTO_DATA`), not in the media folder. **Backups do not include these files**, and neither
do the content export and the restore: a submission keeps only the file's name, size and a
reference. Back up that directory yourself (copy it with the database backup) and put it back in
the same place after a restore. A submission whose file is missing still opens; its Download
answers "That file does not exist.". Deleting a submission, a form or expired submissions
removes the files, so the directory only holds files of submissions that still exist.

### Content only

To move only the content into a new site, import `content.tar.gz` into an **empty** database with
`hatamoto import content.tar.gz`. This is an alternative to steps 3 to 6, not an addition to them.
It brings content, menus, settings and media; it does not bring plugin data, sessions, API keys or
audit rows. Imported users get an unusable password and must reset it (`hatamoto user
reset-password --email E`).
