# Upgrades

How Hatamoto keeps an upgrade reversible. A rollback is "run the previous release's binary against
the database the new release already migrated". There is no database restore. That only works if
the schema stays usable by the previous binary, which is what the rules below enforce.

## Schema rules

For contributors who add a migration under `backend/migrations/`.

**Expand only in a patch or minor release.** The previous release's binary keeps running on the new
schema, so a migration may only add: new tables, nullable or defaulted columns, non-unique indexes,
backfills, and constraints or unique indexes on a table created in the same file. `ADD COLUMN ...
NOT NULL` needs a `DEFAULT`, or the previous binary's inserts fail; the linter cannot see that, so
check it in review.

**A new constraint on an existing table is not expand-only, `NOT VALID` or not.** `NOT VALID` only
skips the check of rows that already exist. Every new `INSERT` and `UPDATE` is checked at once, so
after a rollback the previous binary's writes can fail on a constraint it has never heard of. The
same goes for a unique index. If the previous minor never writes a row that violates it, say so
with `-- hatamoto:contract unused-since vX.Y`. `VALIDATE CONSTRAINT` itself is allowed.

**Never edit a shipped migration.** Not to fix it, not to reformat it. Add a new one. The linter
fails any modified, deleted or renamed migration with `history:`.

### Markers

A migration that is not expand-only says so in a comment, anywhere before `-- +goose Down`:

```sql
-- hatamoto:contract unused-since v1.2
-- hatamoto:backfill idempotent
```

- `unused-since vX.Y` means: no code of the previous minor reads or writes the thing this migration
  removes or changes. The version is the last release that still used it.
- `backfill idempotent` means: the statement can run twice (or on a half-finished earlier run)
  and leave the same rows.

In a Go migration the same markers are `//` comments (`// hatamoto:backfill idempotent`). A new Go
migration always needs the backfill marker, and the contract marker when it mentions contract SQL.

### What the linter flags

`scripts/check-migrations.sh` judges only the Up section of migrations added since the baseline,
and never the historical ones. Comments are ignored. Everything else is read as text, string
literals included, so `EXECUTE 'DROP TABLE x'` and `DO` bodies are judged like plain statements.
An unterminated quote fails the file.

Contract (needs the contract marker):

- `DROP TABLE`, `TYPE`, `SCHEMA`, `EXTENSION`, `VIEW`, `MATERIALIZED VIEW`, `TRIGGER`, `FUNCTION`,
  `PROCEDURE`, `INDEX`, `SEQUENCE`, `DOMAIN`, `POLICY`, `RULE`
- inside `ALTER TABLE`, any `DROP` action (`DROP [COLUMN] [IF EXISTS] x`, `DROP CONSTRAINT`,
  `DROP DEFAULT`) except `ALTER [COLUMN] x DROP NOT NULL`, which relaxes and is safe
- `RENAME` (table, column, anything), `SET SCHEMA`, `DETACH PARTITION`, `ADD GENERATED`
- `ALTER [COLUMN] x [SET DATA] TYPE`, `ALTER [COLUMN] x SET NOT NULL` (the `COLUMN` keyword is
  optional)
- `ADD CONSTRAINT`, `PRIMARY KEY`, `UNIQUE`, `FOREIGN KEY`, `CHECK`, `EXCLUDE` on an existing table,
  with or without `NOT VALID` or `USING INDEX`; each clause of a multi-action `ALTER` is judged
- `ADD COLUMN ... UNIQUE`, `CHECK (...)`, `REFERENCES` or `PRIMARY KEY` inline on an existing table:
  the same constraint, written inside the column definition
- `CREATE UNIQUE INDEX` on an existing table
- `CREATE [OR REPLACE] TRIGGER` on an existing table: the previous binary's inserts run through it
- `CREATE OR REPLACE FUNCTION`, `PROCEDURE` or `VIEW`, and `ALTER FUNCTION`: a replaced trigger
  function or view changes what the previous binary's writes and reads do
- `TRUNCATE`

A constraint, unique index, trigger, function or view on a table or object created in the same
file is additive. A new function (plain `CREATE FUNCTION`) is additive too.

Backfill (needs the backfill marker): `UPDATE <table> [[AS] alias] SET` and `DELETE FROM` anywhere
in the text (CTEs and `DO` bodies too), `INSERT ... ON CONFLICT DO UPDATE SET`, `MERGE ... THEN UPDATE SET` / `THEN DELETE`. `ON UPDATE CASCADE`,
`FOR UPDATE` and column names like `updated_at` are not backfills.

Not judged, to avoid false positives: `COMMENT ON ... IS '...'` statements (the text is only a
string), and `UPDATE`/`DELETE`/`ON CONFLICT DO UPDATE` inside the body of a `CREATE FUNCTION`
that names a table created in the same file (it cannot rewrite rows that exist today). The same
body that names no new table is still judged, and so is any other string literal. A unique index
on a materialized view created in the same file is additive. Anywhere else, the marker is the
escape hatch for keyword text.

Allowed without a marker: `CREATE TABLE`, `CREATE INDEX` (not unique), `ADD COLUMN` (without
an inline `UNIQUE`, `CHECK` or `REFERENCES`), `ALTER COLUMN ... DROP NOT NULL`, `SET DEFAULT`, `VALIDATE CONSTRAINT`, a plain `CREATE FUNCTION` or `CREATE VIEW`. Replacing an existing
function or view needs the contract marker, like a drop. `DROP ... IF EXISTS` is still a drop; if a replace idiom needs it, add the marker.

### Renaming, dropping or retyping

Do it over two releases. Release N adds the new column or table and writes both; release N+1 (once
N-1 no longer reads the old one) drops the old one, with `-- hatamoto:contract unused-since vN`.
A type change is the same: add a column of the new type, dual-write, backfill, switch reads, drop.

### rollback_floor

`rollback_floor` is the oldest release whose binary passes the n-1 check against this release's
schema. Normally that is the previous minor. A major release sets it to itself.

Rolling back across the release that added the timed unsigned allowance (the
`allow_unsigned_until` column of `hatamoto_package_config`) needs no migration edit, but an older
binary does not know the column: it reads only the on/off flag, so an allowance that was timed
stays on until someone turns it off. After such a rollback, check **Plugins > Package security**
(or turn the allowance off before rolling back).

### DB ahead of the binary

An older binary boots on a database that a newer release migrated: goose ignores versions it has no
source for (past the newest one, or in the middle), so `hatamoto migrate` is a no-op and `serve`
starts. `TestMigrateDBAhead_Integration` in `backend/app` pins this.

### The n-1 check

`scripts/n-1.sh` builds the previous release (the newest `vX.Y.Z` release tag reachable from
HEAD, prereleases excluded; else the merge-base with `origin/$GITHUB_BASE_REF` (default `main`), or
HEAD^1 when that merge-base is HEAD itself, as on a push to main) and this tree, and migrates a throwaway database with this tree. Then
the previous binary's own CLI writes to that schema: it creates a second user, creates an entity,
publishes, unpublishes and publishes it again, and creates a redirect. The previous binary then
boots on the database and serves the smoke set (`/`, a page, `/sitemap.xml`, `/admin/login`, the
entity and the redirect it just wrote). The script watches the log for a window and runs the
previous release's DB-backed tests (`entity`, `linking`, `auth`, `menu`, `admin`, `worker`,
`agents`) on the same database. Any `level=ERROR` line, any failed write and any skipped DB test
fails it. The previous release's golden tests do not touch the database, so they prove only the old
code. The old binary's write paths are covered only by the CLI writes and those packages, not by
HTTP admin writes; the script header lists which packages run and why the others do not.

```bash
bash scripts/check-migrations.sh                      # needs git only; MIGRATIONS_BASE=<ref> to override
DATABASE_URL=postgres://user:pw@localhost:5432/postgres?sslmode=disable bash scripts/n-1.sh   # N1_BASE=<ref> to override
```

`n-1.sh` needs Go, `psql` and a role that can `CREATE DATABASE`; `N1_HOLD` sets the log window in
seconds (default 60). `make check` runs the linter and its fixture tests; the `n-1` job in the CI
workflow runs both scripts with full git history so the baseline resolves.

## Menus: no-link items and rollback

Menu items gained a `none` link type (text only), and the migration turns existing `#` items into it. The schema change is expand-only, so rolling back to the previous release works and public rendering is unaffected. The previous release's admin cannot save a menu that contains a no-link item, though: it asks for an address on those items. Retype them as `#` there, or stay on this release, until you move forward again.

## People (person profiles)

Authors became people: one `person` entity per human, credited through
`entities.byline_person_id` (or `byline_org` for the site organisation). Three
expand-only migrations, run by `hatamoto migrate`:

1. `202610231000_person_profiles` adds `byline_person_id`, `byline_org`, the
   `hatamoto_user_people` link table and the built-in `person` type
   (`/author/{slug}`). `byline_person_id` has no foreign key on purpose (adding
   one to an existing table is a contract change).
2. `202610231005_backfill_people` (idempotent) makes a person for every author
   that is not named like the company, with the author's slug, bio and avatar;
   a person is **public only if the author had published posts**; posts get
   `byline_person_id` (an author named like the company becomes `byline_org`); a
   user is linked to a person of the same name, or, on a site with one user and
   one person, to that person. A site that already has its own content type with
   the slug `person` stops here with a message to rename it.
3. `202610231010_person_schema_order` adds field order, groups and `on_team` to
   the built-in type, only if it has not been edited.

**Rolled back.** `hatamoto_authors` and `entities.author_id` are retained until
a contract release, and the new binary keeps writing `author_id` (a mirror row
per person, named after them). The previous binary therefore still renders
every byline. A person's status follows their Public setting (public is
`published`, anything else is `draft`), written in one place by the entity
service, so the previous binary, which only knows the status, does not show a
hidden person: `/author/{slug}` of a hidden person is the legacy author page
(posts only, or 404 with none) and the sitemap omits them. A *public* person's
`/author/{slug}` is served by the previous binary as a generic entity page
without the post list. What it cannot see: people's job title, photo and
links, `byline_person_id`, and anything edited on a person after the upgrade
beyond the name. Do not publish or unpublish a person while rolled back:
change nothing on people until you upgrade again. Nothing is deleted on
rollback or on the way forward, so upgrading again is safe.

**First sign-in after the upgrade.** A user whose person has no name or no photo
is sent once to "Complete your profile" before using the admin.

**Exports.** Archives carry `hatamoto_user_people`; an archive from before people
imports as it is and gets its people made after the commit (`docs/EXPORT.md`).
The format version stays 1 (the layout is unchanged), but a binary from before
people refuses an archive that lists `hatamoto_user_people` ("unknown table"):
import new archives with the new binary.

## Admin address, sign-in cookie and health checks

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

**Nothing moves on an upgrade.** An install that already has an owner keeps `/admin`. A fresh install
gets a private random address instead.

- Owners see a banner on every admin page: "Your admin is at /admin, the first address bots try."
  It offers **Choose a private address** and **Keep /admin**. Keeping a weak address, or changing to one, asks
  for a typed confirmation. Once you confirm, the banner goes away and Settings > Security shows the
  address as weak, accepted by you.
- `hatamoto admin-url` prints the address. `hatamoto admin-url set <path>` stores another one (a weak
  one needs `--i-understand-weak`) and `hatamoto admin-url random` stores a random one. Both apply after
  a restart. `HATAMOTO_ADMIN_PATH` pins the address and wins over the stored one. See
  [CLI.md](CLI.md) and the [configuration reference](site/operate/configuration-reference.md).
- Bookmarks, scripts and monitors that call `/admin/...` must use the new address once you change it.
  The address is not a stable contract (see [STABILITY.md](../STABILITY.md)).

**Everyone is signed out once.** The session cookie `hatamoto_session` becomes `__Host-s` over HTTPS
and `s` on plain HTTP. The admin sets no CSRF cookie. The JSON API cookie, sent only when
`ENABLE_COOKIE_AUTH=true`, is now its own session kind with its own name, `__Host-a` (or `a`), and
the admin cookie no longer authenticates `/api/v1`. **Existing API cookie users must sign in again**
through `POST /api/v1/auth/login`: an old API cookie is not accepted. The login response body now
carries a `csrf_token`; send it in `X-CSRF-Token` on unsafe requests.

- A browser that still holds a live legacy session has it revoked, and both legacy cookies expired, on
  its first visit after the upgrade. The legacy cookie never signs anyone in.
- A CDN or cache rule keyed on the old cookie name must be changed to the new one.

**Health checks.** `/health` and `/ready` answer `ok` (or `503 not ready`) to anyone. The JSON detail
needs a session or a key. Status codes are unchanged, so container and load balancer checks and
`hatamoto healthcheck` keep working.

**Rolled back.** The rollback floor is unchanged: an older binary reads the database as before. It does
not know the new cookie names, so everyone signs in once more, and it answers JSON on `/health` and
`/ready` again. The stored admin address is ignored by an older binary, which serves `/admin`.

The n-1 smoke set above still requests `/admin/login` because the previous release always served the
admin there, and the checks pin `HATAMOTO_ADMIN_PATH=/admin`.

## The supervisor

`hatamoto supervise` is the process the systemd unit and the image run. It starts `hatamoto serve`
as a child and watches it. The supervisor itself fetches nothing from the network: releases arrive
as local files (`hatamoto update apply --file`) or from the update agent described below.

**What it needs.** A writable data directory (`HATAMOTO_DATA`, default `./data`, `/data` in the
image) with room for about three copies of the binary. Releases live in `$HATAMOTO_DATA/releases/`:
one directory per version, `current` and `previous` links, `state.json` and `events.jsonl`. If that
directory is not writable the supervisor logs why and runs the binary it was started from.

**Which binary runs.** The binary the service was started from (the bootstrap) wins when it changed
since the last start (a new image, a package upgrade, `make deploy`) or is semver-newer than
`current`. Otherwise `current` runs. Re-pointing `current` by hand is honoured at the next start.

**Switching.** `hatamoto update apply --file hatamoto --record release.json [--sig release.json.sig]`
checks the signature and the hash, stages the release and signals the supervisor. The supervisor
stops the old child, starts the new one and gates it: `/ready` within 120 s, then a smoke check of
the home page, the sitemap, a published page and the admin login, repeated for the first 60 s. The
checks are compared with the last good run, not with fixed expectations: a site with no home page
(404 at `/`) passes as long as it stays 404, and a release that turns `/` from 200 into 404 or 500
does not. Any 5xx, a plugin that now fails to load, or more render errors than the last good run
logged fails the gate. Render errors are counted only when they were logged by the smoke check's
own requests; a visitor's errors are never counted, even while a check runs. Faults that belong
to the release rather than to a request (the site's theme failing validation and falling back to
the default theme at boot, a theme that no longer parses and is not applied, a theme rebuild
failing, plugin templates rejected) are listed by the smoke endpoint as boot faults, read after the
check's own requests. The last good run's list is stored, and a release fails on any fault that is
not in it.
Before a switch the supervisor re-probes the running release to refresh that last good run. When it
cannot (an offline apply has no running child), and the stored run is more than a day old, only a
404 on `/` or on the sampled published page (an editor unpublished it since then) is forgiven; every
other check, boot faults, plugin load errors and render errors stay strict.
A page degraded by a plugin template-function failure counts as a render error when a smoke request
rendered it.

**Proven and unproven.** A release you switched to stays *unproven* until it has both passed the
gate and run for the full two-minute boot window without a crash loop. After that it is *proven*:
a later crash loop (the database is down at boot, say) restarts it with backoff and never puts it
on the bad list. A release applied while no supervisor runs is not gated at that moment: the next
supervisor start runs the normal gate on it before anything else, and a failed gate puts the
previous binary back. The usual choice between bootstrap and `current` comes first: a bootstrap
that changed since the last start, or is semver-newer, still wins, and the offline release is then
not gated (the log says so).

**Rolling back.** A failed gate, or three crashes within two minutes of a start of a release that
is still unproven, puts the previous binary back and lists the failed version as bad: applying it
again is refused, a newer version supersedes it. A proven release is never rolled back by crashes.
A rollback changes binaries only. It never touches the database, which is why the schema rules
above exist. `hatamoto update rollback` goes back to the previous release by hand; it refuses a
previous release that is on the bad list, or that was applied offline and never ran, unless you
pass `--force`. `previous` is always the last release that actually ran and passed: a second
offline apply does not move the first, never-run release into it, so neither a manual rollback nor
a crash-loop rollback lands on a release nothing has run. The release a rollback returns to keeps
the proven status it had. An offline apply is logged as `staged-offline` and an offline rollback away from the bootstrap as `rolled-back-offline`; only the gate at the next start logs `success` (or `rolled_back`). Each outcome is appended to `events.jsonl` and a running server
imports it into the `hatamoto_updates` table within a minute.

**Is a supervisor running?** The supervisor holds an exclusive lock on `releases/supervisor.pid`
for as long as it lives, and `update` asks the lock, not the pid in the file. That works when the
supervisor is PID 1 and ignores a stale pid file. The supervisor retries the lock for a few seconds
at start, so a `hatamoto update` running at that moment never makes it exit.

**Restart policy.** The supervisor is PID 1 in the image. The compose file sets
`restart: unless-stopped` on the `hatamoto` service; with plain `docker run` pass
`--restart unless-stopped`, so Docker brings the container back if the supervisor ever exits.
(systemd's unit already has `Restart=always`.)

**Running the command.** `hatamoto update` has to see the same data directory and user as the
service. Run it as the service user, never as root: `cd /opt/hatamoto && sudo -u hatamoto bin/hatamoto update apply ...`
(Docker: `docker exec hatamoto hatamoto update apply ...` runs as the image's `app` user, uid
10001, with the files copied into the container first). The unit sets
`HATAMOTO_DATA=/opt/hatamoto/data`, but a shell does not inherit it, so either export it or start
from `/opt/hatamoto`. With `HATAMOTO_DATA` unset the data dir is `./data` of the current directory,
and the command refuses to work in a directory that has no supervisor state. It also refuses a uid
other than the owner of `releases/` and names the owner to run as. If `releases/` (or a bind-mounted
`/data`) is owned by root the service cannot write it either: chown it to the service uid (10001 in
the image), do not run the command as root. Every result ends with the `releases dir:` it used.

Without a supervisor running, `update apply` stages the release and moves the links itself; restart
the service to use it. That release is gated at the next start, as described above.

Stopping the service (SIGTERM) stops the child first; the unit gives it 60 s and compose 45 s.

## The update agent

`hatamoto serve` runs an update agent on the worker scheduler: a check every 6 hours (give or take
30 minutes), the first a few minutes after start. A check is one anonymous GET of the signed
channel manifest for the site's channel (`https://hatamoto.dev/releases/channels/stable.json` by default); no query, no cookie, no
identifier of the install, nothing reported. In `notify` mode that is the whole check: it reports
the newest release the box is inside the rollout for and fetches nothing else. In `auto` mode,
only when the manifest names a target does the agent fetch the signed record (kept under
`releases/.records/`, verified again on every use, so a box that waits is not asking again) and
the binary. Both are verified against the keys built into the binary,
staged, and handed to the supervisor, which runs its health gate and rolls back on failure. A
rolled-back version is never fetched again on that box.

**What it installs.** Patch and minor releases of the running major, when the box is inside the
rollout (each install has a stable random bucket, kept in `$HATAMOTO_DATA/install_id`, 32 bytes, mode
0600; delete nothing there) and the policy allows. A major is only reported ("available"); install
it with `hatamoto update apply vX.Y.Z`. A halted release is not started on a new box. A yanked
release is left for its replacement (or, when the yank names none, a newer normal release if
there is one), else the previous release or the bootstrap build when the rollback floor allows it
(a bootstrap whose version is not comparable is used only when the release sets no floor). `notify`
mode reports a rollback and never makes it. After a health-gate rollback `update status` says
`rolled back vX: <reason>`. A release that needs a newer stepping stone (`min_from`) is reached in
one hop per check. Before switching, the agent checks that enabled plugins and the active theme
declare an SDK range the new release meets, that there are three times the binary's size free
under the data dir, and that no content import is running; any miss is shown in `update status`
and retried at the next check. Before switching it takes a backup with the same engine as Settings > Backups
([BACKUPS.md](BACKUPS.md)): the whole database, plugin schemas and content, kept as a `pre_update`
backup (the newest 2 are kept). It needs `pg_dump` of the server's major version and enough free
space. **A failed backup blocks the update**: nothing is installed, the status shows
`backup failed: <the real error>`, and `update apply vX.Y.Z` (or an `update check` that would
install) exits non-zero with that error. Fix the cause and retry, or install without the backup:
Settings > Updates shows "Update to vX without a backup" for that version only (later updates are
backed up again); `hatamoto update apply vX.Y.Z --no-backup` does the same for one run; and
`HATAMOTO_UPDATE_BACKUP=0` turns the pre-update backup off for every update. The agent never
restores a backup; see [BACKUPS.md](BACKUPS.md#restoring-a-backup). Older
`$HATAMOTO_DATA/backups/pre-<version>.dump` files are left alone and can be deleted by hand.

**Policy.** Environment variables win over the stored settings:

| Variable | Meaning |
|---|---|
| `HATAMOTO_UPDATES` | `auto` (default; also `on`, `true`, `1`), `notify` (look, never install) or `off` (no network at all; also `false`, `0`, `no`, `disabled`). Any other value is treated as `off` and logged as a warning. |
| `HATAMOTO_UPDATE_CHANNEL` | `stable` (default), `edge` or `canary` |
| `HATAMOTO_UPDATE_URL` | another base URL, https only |
| `WORKER_DISABLE_JOBS=update_check` | no scheduled checks (the CLI still works) |
| `HATAMOTO_WORKER=off` | no scheduler at all, so no scheduled checks |

The stored settings (mode, channel, pin, `defer_days` 0-14, a maintenance window in the site time
zone) live in one row of `hatamoto_update_settings`. Replicas that share a database share that row;
each replica still updates its own binaries. Security releases ignore the defer and the window.
A dev build (a version that is not `vX.Y.Z`) never updates itself.

A site can only choose `canary` with a binary from the release that adds it. If a site stored on
`canary` ends up on an older binary (a rollback, or a yank without `--to`, that lands it on a
binary older than the canary release), it cannot fetch updates until an owner picks `stable` or
`edge` in Settings > Updates, or sets `HATAMOTO_UPDATE_CHANNEL=stable` (or `edge`). An old binary
ignores `HATAMOTO_UPDATE_CHANNEL=canary` with a warning.

**Commands.**

```
hatamoto update status               # version, channel, mode, pin, last check and its outcome
hatamoto update check [--dry-run]    # check now; --dry-run decides but installs nothing
hatamoto update pin v1.4 | unpin     # stay on one minor line; security releases still apply inside the major
hatamoto update apply v2.0.0 [--force]
```

`status`, `check`, `pin` and `unpin` need `DATABASE_URL`. `apply vX.Y.Z` fetches the named version
from the channel, ignores ramp, window, defer and pin, still verifies everything, and refuses a
yanked or bad-listed version; the plugin check runs only when `DATABASE_URL` is set, and `--force`
skips it. Run them as the service user, as described above; `check`, `pin`, `unpin` and `apply vX.Y.Z`
refuse to run as anyone else. They report and decide against the release the supervisor runs, not the
version of the binary you typed. A `serve` that runs without a supervisor stages a release once and
says "staged vX; run under `hatamoto supervise` to apply".

## Automatic updates (self-hosters)

### What happens by default

Every six hours or so your site fetches one public, signed file from hatamoto.dev: the channel
manifest for the site's channel (`stable` by default). It is a plain anonymous GET. It carries no query, no cookie,
no identifier of your install and nothing about your site; the only headers are the ones Go adds
itself (its default `User-Agent` and `Accept-Encoding`). Nothing else is ever reported.

When the manifest offers a newer patch or minor release and your install is inside its rollout,
the site downloads it, checks the signatures and hashes against keys built into your binary, and
installs it inside your maintenance window. After each update a health check runs, and the site
rolls back on its own if anything is wrong. A new major version is only announced; it waits for
you. The details are in [The update agent](#the-update-agent).

### Control it

| Want | Do |
|---|---|
| See status | Admin > Settings > Updates, or `hatamoto update status` |
| Update now | Settings > Updates > Update now, or `hatamoto update apply vX.Y.Z` |
| Only be told about releases | set the mode to *Notify*, or `HATAMOTO_UPDATES=notify` |
| Turn it off | `HATAMOTO_UPDATES=off` (no network call at all; the admin shows a banner after 90 days) |
| Delay releases | Settings > Updates > Defer updates (0 to 14 days; security fixes still apply) |
| Stay on one minor | `hatamoto update pin v1.4` and `hatamoto update unpin` (security releases inside the same major still apply) |
| Pick a time | Settings > Updates > Maintenance window |
| Early releases | `HATAMOTO_UPDATE_CHANNEL=edge` or `canary` (default `stable`). Default schedule: canary (bleeding edge, for testing, may break) gets each release first, edge 2 days later, stable from day 7 (5%, then 25% on day 9, 100% on day 11). The schedule is the operator's convention, and you can also choose the channel in Settings > Updates. |
| Go back | `hatamoto update rollback` (to the previous release, when the schema allows it) |
| No internet | download the binary plus `release.json` and its `.sig`, then `hatamoto update apply --file F --record R --sig S` |

Environment variables win over the stored settings; the full list is in
[The update agent](#the-update-agent). Run the `hatamoto update` commands as the service user.

### Requirements

- `$HATAMOTO_DATA` is writable and has about three times the binary size free.
- Docker: mount `/data` as a volume, as the shipped compose file does.
- systemd: use the shipped unit (`ExecStart=... supervise`), or run `hatamoto supervise` another
  way. Without the supervisor a release is only staged and applied at the next restart.

### If an update rolled back

The admin shows a banner with the reason and `hatamoto update status` says
`rolled back vX: <reason>`. That version is skipped on your box and never fetched again; you get the
fix release automatically. Please open an issue and include the `hatamoto update status` output.

### If the site will not start at all

This is rare (the supervisor itself is broken, or the disk is full). Point the `current` link back at
the previous release and restart:

```sh
cd $HATAMOTO_DATA/releases && ln -sfn "$(readlink previous)" current.new && mv -T current.new current
systemctl restart hatamoto-api     # or: docker compose restart hatamoto
```

If that fails too, reinstall the binary or image of the version you had before. Your data and
schema are untouched: an automatic release only ever adds to the schema (see
[Schema rules](#schema-rules)).

### Support policy

- The latest minor of the current major gets every fix.
- The previous major gets security fixes for six months after the new major is released.
- An install that is too old to update directly (below the release's `min_from`) steps through the
  intermediate releases automatically, one hop per check.

### Trust and keys

Two separate signing keys are involved, and both public keys are built into every binary:

- The **release key** signs each release record (the version, its rollback floor and the hash of
  every binary). It is kept offline and used only when a release is cut.
- The **manifest key** signs the short channel manifest that says which release a channel offers
  and at what rollout. It stays on the maintainer's signing machine, which re-signs the channels daily.

A manifest expires after 14 days and is re-signed daily, so a stale copy cannot hide a fix or keep an
old release on offer. A leaked manifest key could halt or withdraw a release, but could not ship
code: the binary's hash comes from the record, which only the release key signs. A binary that does
not match its signed record is rejected before it is staged.

## Upgrading a self-hosted install

### Docker: pg_dump and the PostgreSQL major

The image includes `pg_dump` 16, which the backup before each update needs. If your database is
not PostgreSQL 16, rebuild with `--build-arg PG_MAJOR=<n>`; see [BACKUPS.md](BACKUPS.md#docker).

### Docker volumes (supervisor)

The data directory is now `/data` (`HATAMOTO_DATA=/data` in the image); the supervisor keeps its
releases in `/data/releases`. Older compose files mounted a `secrets` volume at `/data/secrets`
and a `data` volume at `/app/data`. The compose file keeps both volumes, so **`git pull && docker
compose up -d` needs no migration**:

```yaml
volumes:
  - data:/data                # the old `data` volume, now at /data
  - secrets:/data/secrets     # unchanged: the same `secrets` volume at the same path
  - uploads:/app/uploads
```

The `secrets` volume is mounted inside `/data` (a nested mount), so the server secret is the file
the old stack already had and no new one is generated. The `data` volume was the working data
directory before (`/app/data`); the same contents now appear under `/data`. If you maintain your
own compose file, keep the `secrets:/data/secrets` line. Without it a new secret is generated and
every session and API signature made with the old one stops working. Check with
`docker compose logs hatamoto` that no new secret was generated.
