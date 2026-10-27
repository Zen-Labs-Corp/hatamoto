---
title: Database migrations
nav_title: Database migrations
description: How the database structure is updated on start-up, how versions are numbered, and the rules and checks that keep going back to an older version safe.
---

This page describes how Hatamoto applies database migrations, the rules a migration must follow so the previous release still runs on the new schema, and the scripts that check those rules. A **migration** is a step that updates the database structure to match the new version. To see the state of your own database, run `hatamoto migrate status` as shown in [Find where your data lives](where-your-data-lives.md).

## Migrations ship inside the binary

The schema is defined by SQL files (and a few Go files) under `backend/migrations/`. They are embedded into the `hatamoto` binary with `go:embed`, so there is no separate migration tool to install and no files to copy next to the binary. `hatamoto serve` applies every pending migration at startup, before the server starts listening. You can also run them on their own:

```bash
hatamoto migrate            # apply pending migrations (same as "migrate up")
hatamoto migrate status     # list migrations and whether they are applied
```

`migrate status` prints one line per migration: the version, `applied <time>` or `pending`, and the file name. Both commands need `DATABASE_URL`.

Hatamoto uses `goose/v3` as a library (a Go package), not the goose command line tool. Applied versions are recorded in the standard `goose_db_version` table, so a database that was migrated earlier with the goose CLI continues from where it was.

## Numbering

| Range | Style | Example |
|---|---|---|
| `001` to `017` | the original sequential migrations; they stay as they are | `001_baseline.sql` |
| after that | a timestamp version, `YYYYMMDDHHMM_name.sql` | `202610150116_drop_pgvector.sql` |

Create a new timestamp with `date +%Y%m%d%H%M`. Two pull requests can then add migrations without fighting over the next number. Goose orders migrations by integer version, so every timestamp sorts after the sequential ones.

A migration file looks like this:

```sql
-- +goose Up
CREATE TABLE example_things (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL
);

-- +goose Down
DROP TABLE example_things;
```

Wrap `DO $$ ... $$` blocks in `-- +goose StatementBegin` and `-- +goose StatementEnd`. Some migrations are Go files in the same directory (the data backfills for authors and people); they register themselves with goose and run in the same ordered sequence.

**Never edit a migration that has shipped.** Add a new one instead. The linter described below enforces this.

## Locking and concurrency

The runner takes a Postgres session-level advisory lock for the whole run. If you start several replicas at once, or run `hatamoto migrate` next to a running `serve`, the migrations are applied by one process at a time and the others find nothing left to do. See [Multi-replica and health](../operate/multi-replica-and-health.md) for the operational side.

## Out-of-order versions

Two branches merged in a different order than they were written can leave a migration whose version is older than the newest one already applied. Goose normally refuses that as a "missing migration". Hatamoto runs it with out-of-order mode on (`WithAllowOutofOrder(true)`), so the older migration is applied on the next boot. The consequence for authors: a migration must not depend on one with a later timestamp, and independent migrations must be safe in any order.

## A database ahead of the binary

If you start an older binary against a database that a newer release already migrated, goose ignores the versions it has no source for. `migrate` does nothing and `serve` starts. This is what makes a rollback possible without restoring a backup: the old binary runs on the new schema. A test in `backend/app` (`TestMigrateDBAhead_Integration`) pins this behaviour.

## Expand-only rules

Because the previous release's binary must keep working on the new schema, a migration added in a patch or minor release may only **expand** the schema:

- new tables,
- new columns that are nullable or have a default (`ADD COLUMN ... NOT NULL` needs a `DEFAULT`),
- non-unique indexes,
- backfills,
- constraints, unique indexes and triggers on a table created in the same file.

A new constraint on an existing table is not expand-only, even with `NOT VALID`: every new `INSERT` and `UPDATE` is checked immediately, so after a rollback the old binary's writes could fail on a rule it has never heard of. Dropping, renaming or retyping a column or table is done over two releases: release N adds the new thing and writes both, release N+1 drops the old one once nothing reads it.

### Markers

When a migration is not expand-only, it must say so in a comment before `-- +goose Down`:

```sql
-- hatamoto:contract unused-since v1.2
-- hatamoto:backfill idempotent
```

`contract unused-since vX.Y` states that no code from the previous minor reads or writes what this migration removes or changes (the version is the last release that still used it). `backfill idempotent` states that the statement can run twice, or on a half-finished earlier run, and leave the same rows. In a Go migration the same markers are `//` comments, and a new Go migration always needs the backfill marker.

## The linter and the n-1 check

Two scripts enforce the rules. Both run in `make check` and in CI.

**`scripts/check-migrations.sh`** needs only `git`, `bash` and `awk`. It judges migrations added since the baseline (the newest `vX.Y.Z` release tag reachable from HEAD, else the merge-base with the main branch; override with `MIGRATIONS_BASE=<ref>`). It flags, among others:

- contract statements: `DROP TABLE`, `DROP COLUMN`, `RENAME`, `ALTER COLUMN ... TYPE`, `SET NOT NULL`, `ADD CONSTRAINT`, `CREATE UNIQUE INDEX` on an existing table, `TRUNCATE` and similar, unless the table is created in the same file;
- backfill statements: `UPDATE ... SET`, `DELETE FROM`, `INSERT ... ON CONFLICT DO UPDATE`;
- any modified, renamed or deleted shipped migration (reported as `history:`).

It is a text scan, not a SQL parser, so it errs on the side of flagging; the marker is the escape hatch.

**`scripts/n-1.sh`** is the backstop. It builds the previous release and the current tree, migrates a throwaway database with the current tree, then lets the **previous** binary write to that schema (create a second user, create, publish and unpublish an entity, create a redirect), boot `serve`, pass a smoke set of URLs, run quiet for a window while the log is scanned for errors, and run the previous release's database-backed tests against the migrated database. Any error line, failed write or skipped database test fails the run. The previous release is the newest `vX.Y.Z` tag reachable from HEAD, else the merge-base with the main branch; `N1_BASE=<ref>` overrides it, and `N1_HOLD` sets how many seconds the previous release keeps serving after `/ready` while its log is watched (default 60). It creates a throwaway `hm_n1_*` database next to `DATABASE_URL` and drops it afterwards, and exits 0 on a pass, 1 on a failure, 2 when no previous release is found and 64 when a prerequisite is missing. It needs `go`, `psql`, `curl`, `git`, `timeout` and a role that can `CREATE DATABASE`:

```bash
bash scripts/check-migrations.sh
DATABASE_URL='postgres://user:pw@localhost:5432/postgres?sslmode=disable' bash scripts/n-1.sh
```

## The `rollback_floor`

`rollback_floor` is the oldest release whose binary passes the n-1 check against this release's schema. Normally that is the previous minor release. A major release sets it to itself, which means a rollback below it needs a restore from backup rather than only running the old binary. See [Backups and rollback](../updates/backups-and-rollback.md).

## One sanctioned edit of history

`001_baseline.sql` no longer creates the pgvector `vector` extension. This was a deliberate one-time exception so a fresh database can be migrated on a plain `postgres:16` image. It is safe because goose stores no checksums and every existing database already has version 1 recorded. It is not a precedent: a shipped migration is never edited again.

## Writing a migration yourself

If you contribute a change that touches the schema, see [Writing migrations](../migrate-contribute/writing-migrations.md) for the contributor checklist.
