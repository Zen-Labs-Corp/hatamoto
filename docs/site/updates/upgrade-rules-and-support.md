---
title: Upgrade rules and support
nav_title: Upgrade rules
description: The database rules that make going back a version safe, how major versions differ, and how long each release line gets fixes.
---

This page lists the rules every release follows so that a rollback is safe, what changes with a major version, and the support policy. To move your own server to a newer version, follow [Upgrade a self-hosted install](upgrading-self-hosted.md).

## Overview

A rollback never restores the database. The supervisor only runs the previous release's binary against the database the newer release already migrated. So the schema a patch or minor release leaves behind must stay usable by the release before it.

| Release kind | Installed automatically | Schema changes allowed | `rollback_floor` |
|---|---|---|---|
| patch | yes, under your policy | expand only | normally the previous minor |
| minor | yes, under your policy | expand only | normally the previous minor |
| major | never; install it by hand | may contract (drop, rename, retype) | the major release itself |

## Schema rules

Patch and minor releases follow these rules. The project's CI enforces them with a linter (`scripts/check-migrations.sh`) and an n-1 test (`scripts/n-1.sh`).

- **Expand only.** A migration may add tables, nullable or defaulted columns, non-unique indexes and backfills, and constraints on tables created in the same file. It may not drop, rename or retype anything, add `NOT NULL` without a default, or add a constraint to an existing table.
- **Two releases for a rename or drop.** Release N adds the new column and writes both. Release N+1, once N-1 no longer reads the old column, drops it under an explicit contract marker (`-- hatamoto:contract unused-since vX.Y`). Contract changes are reserved for major releases.
- **Backfills say they are safe to repeat.** A migration that rewrites rows carries `-- hatamoto:backfill idempotent`.
- **`rollback_floor`.** Each release record names the oldest release whose binary passes the n-1 check against this release's schema, normally the previous minor. A major release sets it to itself. Neither the agent nor `hatamoto update rollback` goes below the floor, and `--force` does not override it.
- **A database ahead of the binary is fine.** An older binary that boots on a database a newer release migrated finds nothing to apply in `hatamoto migrate` and starts normally.
- **The n-1 check.** CI builds the previous release and the new tree, migrates a throwaway database with the new tree, makes the previous binary write to it (create a user, an entity, publish, unpublish, a redirect), boots it and serves a smoke set, then watches its log. Any error fails the check.

If you write migrations for the project, see [Writing migrations](../migrate-contribute/writing-migrations.md).

## Major versions

A major release is never installed automatically. The agent reports it as `available major`, and `hatamoto update status` prints:

```text
major       vX.0.0 is available; install it with `hatamoto update apply vX.0.0`
```

Read the release notes first, take your own database backup, then run the apply as the service user, or use **Update now** on **Settings > Updates**, which asks for confirmation. A major may contain contract migrations, so its `rollback_floor` is itself: after a major, neither the agent nor the CLI rolls back to the previous major's binaries.

## Starting points (`min_from`)

Each release record names `min_from`, the oldest version that can move straight to it. An install below it steps through the intermediate releases automatically, one hop per check, oldest first. A manual `hatamoto update apply` refuses with `vX needs at least vY first (running vZ)`, which names the version to install first.

## Support policy

- The latest minor of the current major gets every fix.
- The previous major gets security fixes for six months after the new major is released.

## Related

- [The update agent](update-agent.md) for how a target is chosen.
- [Trust and signing](trust-and-signing.md) for the release record fields.
- [Release process](../migrate-contribute/release-process.md) for how maintainers cut a release.
