---
title: Writing migrations
nav_title: Writing migrations
description: Add a timestamped schema migration that is safe for the previous release, use the contract and backfill markers and run the linter and n-1 check.
---

Hatamoto migrations are embedded in the binary and applied by `hatamoto serve` on boot and by `hatamoto migrate`. There is no separate goose install. This page lists how to add one and the rules that keep a rollback safe: because an update can be rolled back to the previous release, the previous binary has to keep working on the schema you create. It is the reference behind the migration rule in [Make your first contribution](contributing.md).

## Numbering

The files `001` to `017` are the original sequential migrations and stay as they are. **New migrations use a timestamp version**, `YYYYMMDDHHMM_name.sql`, for example `202610150116_drop_pgvector.sql`. Generate the prefix with `date +%Y%m%d%H%M`. Two pull requests can then add migrations without colliding on a number.

Mixed numbering works because goose orders by integer version: `17` is smaller than `202610150116`, so every timestamp migration sorts after the sequential ones.

### Out-of-order timestamps

Two branches merged in a different order from the one they were written in leave a migration older than the newest one already applied. Hatamoto runs goose with out-of-order migrations allowed, so the older migration is applied the next time the app boots. As a result, a migration must not depend on one with a later timestamp, and must be safe whatever order independent migrations run in.

### Concurrency and existing databases

The runner holds a Postgres session advisory lock for the whole run, so replicas, or a `hatamoto migrate` next to `hatamoto serve`, migrate one at a time and the others find nothing left to do. Databases that were migrated earlier with the goose CLI use the same `goose_db_version` table and continue from where they were.

## The file shape

```sql
-- +goose Up
CREATE TABLE hatamoto_example (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX hatamoto_example_name_idx ON hatamoto_example (name);

-- +goose Down
DROP TABLE hatamoto_example;
```

Wrap `DO $$ ... $$` blocks in `-- +goose StatementBegin` and `-- +goose StatementEnd`. **Never edit a migration that has shipped**, not to fix it and not to reformat it. Add a new one. The single sanctioned exception in history was removing the pgvector extension from the baseline so that a fresh database could be migrated on a plain Postgres image. It is not a precedent.

Go migrations are also supported, as `.go` files in the same directory, but they are rarer and have extra marker rules (below).

## Expand only

In a patch or minor release a migration may only **add**: new tables, nullable or defaulted columns, non-unique indexes, backfills, and constraints or unique indexes on a table created in the same file.

Two traps are worth knowing:

- `ADD COLUMN ... NOT NULL` needs a `DEFAULT`, or the previous binary's inserts fail. The linter cannot see this, so check it in review.
- A new constraint on an existing table is **not** expand-only, `NOT VALID` or not. `NOT VALID` only skips checking rows that already exist, and every new `INSERT` and `UPDATE` is checked at once. After a rollback, the previous binary's writes could fail on a constraint it has never heard of. The same goes for a unique index.

## Markers

A migration that is not expand-only says so in a comment, anywhere before `-- +goose Down`:

```sql
-- hatamoto:contract unused-since v1.2
-- hatamoto:backfill idempotent
```

- `unused-since vX.Y` means that no code of the previous minor reads or writes what this migration removes or changes. The version is the last release that still used it.
- `backfill idempotent` means that the statement can run twice, or on a half-finished earlier run, and leave the same rows.

In a Go migration the markers are `//` comments. A new Go migration always needs the backfill marker, and the contract marker when it mentions contract SQL.

## What the linter flags

`scripts/check-migrations.sh` judges only the Up section of migrations **added since the baseline** and never the historical ones. It needs git, bash and awk, and no Go or database. Comments are ignored and everything else is read as text, string literals included, so `EXECUTE 'DROP TABLE x'` is judged like a plain statement. An unterminated quote fails the file.

**Needs the contract marker:**

- `DROP` of a table, type, schema, extension, view, trigger, function, index, sequence and similar objects, and any `DROP` action inside `ALTER TABLE`, except `DROP NOT NULL`, which relaxes a column and is safe.
- `RENAME`, `SET SCHEMA`, `DETACH PARTITION`, `ADD GENERATED`.
- `ALTER COLUMN ... TYPE` and `ALTER COLUMN ... SET NOT NULL`.
- `ADD CONSTRAINT`, `PRIMARY KEY`, `UNIQUE`, `FOREIGN KEY`, `CHECK` and `EXCLUDE` on an existing table, including inline in `ADD COLUMN`.
- `CREATE UNIQUE INDEX` on an existing table.
- `CREATE TRIGGER` on an existing table, `CREATE OR REPLACE FUNCTION` or `VIEW`, and `ALTER FUNCTION`.
- `TRUNCATE`.

A constraint, unique index, trigger, function or view on an object created in the same file is additive and needs no marker.

**Needs the backfill marker:** `UPDATE <table> SET`, `DELETE FROM`, `INSERT ... ON CONFLICT DO UPDATE SET`, and `MERGE ... THEN UPDATE` or `THEN DELETE`, wherever they appear, including in CTEs and `DO` bodies. `ON UPDATE CASCADE`, `FOR UPDATE` and a column named `updated_at` are not backfills.

**Allowed without a marker:** `CREATE TABLE`, non-unique `CREATE INDEX`, `ADD COLUMN` without an inline constraint, `ALTER COLUMN ... DROP NOT NULL`, `SET DEFAULT`, `VALIDATE CONSTRAINT`, and a plain `CREATE FUNCTION` or `CREATE VIEW`. The linter is best effort, which is why the n-1 check exists as a backstop.

Run it locally:

```bash
bash scripts/check-migrations.sh            # MIGRATIONS_BASE=<ref> overrides the baseline
```

The baseline is the newest release tag of the form `vX.Y.Z` reachable from HEAD, otherwise the merge-base with `origin/main`. A shallow clone cannot resolve it, so fetch full history or set `MIGRATIONS_BASE`. The working tree counts, so committed, staged and untracked files are all seen. Editing, deleting or renaming a shipped migration fails with `history:`.

## Renaming, dropping or retyping

Do it over two releases. Release N adds the new column or table and writes both. Release N+1, once release N-1 no longer reads the old one, drops it with `-- hatamoto:contract unused-since vN`. A type change is the same: add a column of the new type, write both, backfill, switch reads and drop the old column.

## The n-1 check

`scripts/n-1.sh` builds the previous release and this tree, and migrates a throwaway database with this tree. Then the previous binary's own CLI writes to that schema: it creates a second user, creates an entity, publishes, unpublishes and publishes it again, and creates a redirect. The previous binary then boots on the database and serves a smoke set (the home page, a page, `/sitemap.xml`, `/admin/login` (the previous release always serves the admin there), the new entity and the redirect). It watches the log for a window and runs the previous release's database tests for several packages on the same database. Any `level=ERROR` line, failed write or skipped database test fails the run.

```bash
DATABASE_URL=postgres://user:pw@localhost:5432/postgres?sslmode=disable bash scripts/n-1.sh
```

It needs Go, `psql` and a role that can `CREATE DATABASE`. `N1_BASE=<ref>` overrides the previous release and `N1_HOLD` sets the log window in seconds (default 60). `make check` runs the linter and its fixture tests, and the `n-1` CI job runs both scripts with full git history.

## `rollback_floor`

Each release record carries a `rollback_floor`: the oldest release whose binary passes the n-1 check against this release's schema. Normally it is the previous minor, and a major release sets it to itself. See [Release process](release-process.md) and [Backups and rollback](../updates/backups-and-rollback.md).

## A database ahead of the binary

An older binary boots on a database that a newer release migrated. Goose ignores versions it has no source for, so `hatamoto migrate` is a no-op and `serve` starts. A test in `backend/app` pins this behaviour.
