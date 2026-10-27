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
