# Migrations

Core schema migrations, applied by `hatamoto serve` on boot and by
`hatamoto migrate` (they are embedded in the binary via `migrations.go`). There
is no separate goose install.

## Numbering

- `001`–`017` are the original sequential migrations. They stay as they are.
- **New migrations use a timestamp version: `YYYYMMDDHHMM_name.sql`**
  (for example `202610150116_drop_pgvector.sql`; `date +%Y%m%d%H%M`). Two PRs
  can then add migrations without colliding on a number.

Mixed numbering works because goose orders by integer version: `17` < `202610150116`,
so every timestamp migration sorts after the sequential ones.

## Out-of-order timestamps

Two branches merged in a different order than they were written leave a
migration whose version is *older* than the newest one already applied.
By default goose refuses that ("missing migration"). Hatamoto runs goose with
`WithAllowOutofOrder(true)` (the CLI's `allowMissing`), so the older migration
is applied the next time the app boots. Consequence: a migration must not
depend on one with a *later* timestamp, and must be safe whatever order the
independent migrations run in.

## Concurrency

The runner holds a Postgres session advisory lock for the whole run, so
replicas or a `hatamoto migrate` next to `hatamoto serve` migrate one at a
time; the others find nothing left to do.

## Existing databases

Databases migrated earlier with the goose CLI use the same `goose_db_version`
table, so they continue from where they were: only migrations not yet recorded
are applied.

## Writing one

```sql
-- +goose Up
...
-- +goose Down
...
```

Use `-- +goose StatementBegin` / `StatementEnd` around `DO $$ ... $$` blocks.
Never edit a migration that has shipped.

A new migration must also be safe for the previous release's binary (expand only, or
marked). The rules, the markers and the checks are in
[docs/UPGRADES.md](../../docs/UPGRADES.md#schema-rules).

## Squashing (major releases only)

Why the chain is kept instead of one schema file: every self-hosted install
updates itself from whatever version it runs, and only the individual
migrations can take it from there to the current schema. A fresh database
runs the whole chain in a few seconds, so a separate schema file for new
installs would save little and add a second source of truth that can drift
from the chain. `001_baseline.sql` is the result of the last squash (it
folded the original migrations 001-127).

The chain is squashed again only at a major release (`vN.0.0`), never in a
patch or minor, and at most once per major. The cut is the last release of
the previous major (`vN-1.x.y`, called the floor below): every migration that
shipped in it or before it is folded.

The squash PR does all of this:

1. **Generate the baseline.** Migrate an empty database with the floor
   release's binary, then `pg_dump --schema-only` it, plus `--data-only` for
   the rows a fresh migrate inserts (the default schemas and settings rows).
   Do not write it by hand.
2. **Give it the version of the newest folded migration**, for example
   `202610311110_baseline_v2.sql`. Every database at the floor already has
   that version recorded, so goose skips the file there and runs it only on
   an empty database. `max(version_id)` keeps rising, so export archives
   (`internal/portability`) still compare versions correctly.
3. **Delete the folded files**, their Go migrations and the integration tests
   that only test a folded migration. Tests that check current behaviour move
   to the package that owns it.
4. **Add the floor guard** to `backend/app/migrate.go`. Before `Up`, a
   database that has applied migrations but is below the baseline version is
   refused with `this database is at version X; install vN-1.x.y first, then
   this release`. Without the guard, goose (out of order allowed) would run
   the baseline on a populated database and fail halfway with "relation
   already exists". An empty database, or one at or above the floor, passes.
5. **Let the linter accept the deletions.** `scripts/check-migrations.sh`
   fails any deleted migration with `history:`. The squash PR teaches it one
   exception: the deletion of files with a version at or below the new
   baseline's, only in the change that adds that baseline.
6. **Prove the two schemas match.** In CI, migrate one database with the
   floor binary and another with this tree, then diff a normalised
   `pg_dump --schema-only` and the seed rows of both. Any difference fails.
   Keep this check for the life of the major.
7. **Set the release record** of `vN.0.0`: `min_from` is the floor, and
   `rollback_floor` is `vN.0.0` itself, as for every major. Installs below the
   floor then step through it automatically, one hop per check
   ([docs/UPGRADES.md](../../docs/UPGRADES.md#squashing-migrations)).
8. **Update the docs** that name the old files: this README (numbering, the
   sanctioned edits below), `CLAUDE.md` rule 5 and
   `docs/site/migrate-contribute/writing-migrations.md`.

Plugin migrations live in each plugin's own schema and are not affected.

The guard, the linter exception and the CI diff do not exist yet. They land
with the first squash, in the same PR.

## The one sanctioned edit of a shipped migration

`001_baseline.sql` no longer creates the pgvector `vector` extension (or its
`COMMENT ON EXTENSION`). This was a deliberate, one-time exception so a fresh
database can be migrated on a plain `postgres:16` image (compose, CI).
It is safe because goose stores no checksums, every existing database already
has version 1 recorded (so 001 never runs there again), and production never
had `vector` installed. `202610150116` still drops the extension where it
exists. Do not treat this as precedent: never edit a migration that has shipped.

A second one-time exception: `001_baseline.sql` names its four `hatamoto_leads`
indexes (`intent_score`, `intent_tier`, `merged_into`, `sales_engaged_at`)
`idx_leads_*` instead of carrying a private name segment. `202610261000` renames the
old names on existing databases. `scripts/check-migrations.sh` accepts exactly
this rename of those four identifiers in that file and nothing else.
