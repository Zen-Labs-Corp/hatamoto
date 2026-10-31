---
title: Testing
nav_title: Testing
description: Every test layer and repository check: unit and database tests, pinned output files, the guards, the WordPress fixture and the manual pass.
---

This page lists every test layer and repository check in Hatamoto: fast unit tests that need nothing, integration tests that need Postgres, golden-file tests that pin rendered output, the guards that `make check` runs, and a short manual checklist for browser behaviour that code cannot see. It shows how to run each and how CI does it. For the order to run them in while you work on a change, follow [Make your first contribution](contributing.md).

## Unit tests

From the `backend/` directory:

```bash
go vet ./...
go test ./...
```

These need no database. Tests that need Postgres skip themselves when no database is configured, so a plain run is always safe. The plugin SDK is its own module and has its own run:

```bash
cd backend/pkg/sdk
go vet ./... && go test ./...
GOOS=wasip1 GOARCH=wasm go vet ./guest
```

## Integration tests against Postgres

Database-backed tests carry the build tag `integration`. Start a Postgres 16 or 18 instance (CI tests both), point the environment at it and add the tag:

```bash
docker run -d --name hm-test-db -p 5432:5432 \
  -e POSTGRES_USER=hatamoto -e POSTGRES_PASSWORD=hatamoto -e POSTGRES_DB=hatamoto \
  postgres:16

export DATABASE_URL='postgres://hatamoto:hatamoto@localhost:5432/hatamoto?sslmode=disable'
export HATAMOTO_TEST_DB_URL="$DATABASE_URL"
export EMAIL_DISABLED=true

cd backend
go run ./cmd/hatamoto migrate
go vet -tags=integration ./...
go test -tags=integration ./...
```

Two variables select the database. Most integration tests read `DATABASE_URL`. A smaller group (the updater settings test and the entity concurrency test, for instance) reads `HATAMOTO_TEST_DB_URL`. These tests skip when it is unset. CI sets both to the same value, and so should you. Use a scratch database: tests create and delete rows. Many tests migrate a database of their own, but do not point the suite at data you care about.

Run one package or one test while you work:

```bash
go test -tags=integration -run TestPackagesIntegration -v ./internal/server
go test -tags=integration ./internal/entity
```

### Tests that must really run

A skipped test proves nothing. CI therefore re-runs the safety-critical groups by name and fails if a named test did not print `--- PASS`. These are the plugin sandbox (hostile guests, spinning plugins, oversized output), the runtime limit and leak tests, the plugin capability tests (a plugin cannot read the server's own tables, even with the SQL guard off), the package tests (hostile archives, zip bombs, wrong signers, signed-registry install without a restart) and the first-party plugin tests. When you add a test of this kind, add its name to the matching step so a future skip is loud.

The sandbox suite also runs under the race detector:

```bash
go test -race -count=1 ./internal/wasmhost ./internal/pluginhost
```

## Golden files

Rendering and some admin output are pinned by golden files in `testdata/` directories. A golden test compares the output with the stored file and fails with a diff. After an intended change, regenerate with the package's `-update` flag, then read the diff before you commit:

```bash
cd backend
go test ./internal/render -update
git diff --stat internal/render/testdata
```

The flag is defined per package, for example in `internal/render`, `internal/admin` and the schema form package. A golden diff you did not expect is a bug, not noise. Do not update to make a test pass until you can explain every changed line.

## The admin UI checks

Admin template, CSS and component rules are enforced by static tests that need no database and run in a few seconds:

```bash
make design-check
```

They are the tests of `backend/internal/admin/designcheck` and `backend/internal/admin/a11y`. A new static design check goes in `designcheck`; the target picks it up with no list to update.

A second check measures computed styles in a real browser against a local instance. It needs Chrome, Postgres and the small QA harness under `scripts/admin-qa`, and CI runs it as the `admin-ui` job. The harness is the only place Node is used, and it is not part of the build.

```bash
make ui-check
```

## The WordPress fixture

The importer is tested against a real WordPress seeded by `test/wpfixture`: nested pages, dated permalinks, Yoast, a gallery, YouTube and Vimeo embeds, Contact Form 7, a private post, an author with an avatar, a custom post type and menus. CI runs it in Docker as the `wordpress-importer` job. Without Docker, a local script downloads a pinned static PHP and WordPress and serves them with PHP's built-in server:

```bash
test/wpfixture/run-local.sh up
set -a; . ~/.cache/hatamoto-wpfixture/site/wpfixture.env; set +a
cd backend && go test -tags=integration -run WordPress -v ./app
go test -tags=integration -run WordPressWizard -v ./internal/server   # the wizard, end to end
test/wpfixture/run-local.sh down
```

The tests check the done criteria of the importer: every old URL answers 200 or exactly one 301, no `wp-content` URL remains, imported SEO titles equal the old pages' own titles, and a re-run adds no rows. They run once from the REST API, once from a WXR export and once with the permalink structure left alone so that every post redirects. See [Import from WordPress](wordpress-import.md).

## The guards

`make check` at the repository root runs the repository guards. Run it before you open a pull request. Guards are code, so a failing guard is a message to act on, not an obstacle to route around. The neutrality check, for example, exists so that the repository stays usable by anyone. If you need a word that it bans, rephrase.

| Guard | What it catches |
|---|---|
| `make design-check` | Admin template, CSS, token and component rule violations, with no database. |
| `scripts/check-neutral.sh` | Site-specific or fork-specific names in tracked files, locale-code path literals, a few retired schema names, and `hx-on`, `js:` values and trigger filters in admin templates (they need script evaluation, which the strict content security policy and the htmx setting `allowEval: false` forbid). |
| `scripts/check-migrations.sh` and `scripts/test-check-migrations.sh` | New migrations that would break the previous release, and the linter's own fixture tests. |
| `scripts/test-version.sh`, `scripts/test-publish-release.sh`, `scripts/test-staging-update-check.sh`, `deploy/test-apply-caddy.sh` | Fixture tests for the release scripts and the Caddy splice. |
| `make vendor-verify` | Vendored browser assets whose bytes no longer match `vendor.json`. |
| `make vuln` | Known vulnerabilities in the code the server and the SDK call. Needs the network. |
| `go run ./tools/licenses` (from `backend/`) | Any dependency with a non-permissive or unknown licence. |
| `make docs-publish-test` and the docs lint | The docs tool's own tests, and the lint of every page under `docs/site` (see [How these docs are made](how-these-docs-are-made.md)). |

## The previous-release check

The schema linter and the n-1 check prove that a new migration does not break the release before it. They are described in [Writing migrations](writing-migrations.md). The n-1 script needs Go, `psql` and a database role that can create databases.

## Manual checks

Some behaviour only a person can see: keyboard order and focus rings, screen reader announcements, zoom and reflow, dark mode, the page editor canvas, sandboxed plugin pages and theme preview. `docs/QA.md` is the checklist. Run the relevant part before a release that changes the admin. The no-browser accessibility checker in the admin package covers names, landmarks, headings, tables, dialogs and contrast, and the checklist covers behaviour.

## Writing good tests

- Prefer table-driven tests with `t.Parallel()` where nothing is shared.
- Test behaviour through the same services the API uses, not private helpers.
- Never let a database test pass by skipping when the database is misconfigured in CI.
- Use `example.com` hosts and neutral names in fixtures.
- Keep fixtures small. A large corpus belongs in a `testdata/` directory.
