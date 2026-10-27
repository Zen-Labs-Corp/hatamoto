---
title: Make your first contribution
nav_title: Contributing
description: Build Hatamoto from source, make a focused change that follows the project rules, run the checks and open a pull request.
next: testing.md, writing-migrations.md, how-these-docs-are-made.md
---

Hatamoto is licensed under Apache-2.0 and developed in the open. In this guide you build it from source, make a change that follows the project's rules, run the same checks the project runs before a merge, and open a pull request. A fix to a doc page is as welcome as a code change: every page of these docs is a Markdown file in the repository.

## Before you start

- Git and a fork of the repository.
- Go. It is the only toolchain: there is no Node step in the build, and the program embeds the admin templates, the default theme and the migrations. The Go version is pinned in `backend/go.mod` (a `go` line and a `toolchain` line), and CI reads the same file.
- Postgres 16, only for the database tests and for running the server. Docker is the quickest way to get one.
- For anything larger than a fix, an issue or discussion opened first, so the direction is agreed before you write code.

## Steps

### Build and run

1. Clone your fork and build:

   ```bash
   git clone <your-fork-url> hatamoto
   cd hatamoto
   make build
   ```

   ```text
   Binary built: backend/bin/hatamoto
   ```

   `make build` cross-compiles for linux/amd64 with `CGO_ENABLED=0`.

2. To run on your own machine, build with the plain Go command instead, from `backend/`, and start the server:

   ```bash
   cd backend && go build -o bin/hatamoto ./cmd/hatamoto
   DATABASE_URL='postgres://hatamoto:hatamoto@localhost:5432/hatamoto?sslmode=disable' \
     ./bin/hatamoto serve
   ```

   `docker compose up` at the repository root starts a database and Hatamoto with working defaults, which is the quickest way to see a change running. See [Install with Docker](../getting-started/install-docker.md).

3. Find where your change goes. Run server commands from `backend/`. The main paths:

   | Path | Contents |
   |---|---|
   | `backend/cmd/hatamoto` | The program's entry point. |
   | `backend/app` | Command dispatch: `serve`, `supervise`, `migrate`, `user`, `import`, `export`, `update` and the rest. |
   | `backend/internal/*` | The server packages, such as `server`, `admin`, `entity`, `render`, `redirect`, `pluginhost`, `packages`, `wpimport`, `portability` and `updater`. |
   | `backend/pkg/sdk` | The plugin SDK, its own Go module. See [SDK and registry](sdk-and-registry.md). |
   | `backend/migrations` | Embedded schema migrations. See [Writing migrations](writing-migrations.md). |
   | `backend/web` | Admin templates, CSS and JavaScript, plus byte-pinned vendored browser assets. |
   | `backend/tools` | Developer tools such as `release`, `licenses`, `vendor` and `docs-publish`, never linked into the program. |
   | `examples/plugins` | The reference plugin `hello` and the demo plugins. The first-party plugins have their own repositories. |
   | `deploy`, `Dockerfile`, `docker-compose.yml` | Packaging and deployment files. |
   | `docs`, `docs/site` | Developer documents, and these published docs. |
   | `scripts` | Guards and release helpers. |

### Make the change

1. Branch from `main`, keep the change focused, and read the code around it first. Follow the patterns that are already there.
2. Keep to the project rules:
   - **One site, one language.** Never add a language or region segment to a URL, a path example or a schema.
   - **Where content lives is data, not code.** URL patterns, archive paths and aliases are schema settings. Never add a per-schema URL switch: use the URL registry in `backend/internal/entity`. See [URL patterns](../ai-seo/url-patterns.md).
   - **No site-specific code.** Features for one site are plugins built on the SDK, in their own repositories.
   - **Never edit an applied migration.** Add a new one that the previous release can still run against.
   - **Themes and plugins are installed packages**, stored in Postgres, not directories in the install.
   - **Admin UI rules.** Templates and CSS follow `docs/ADMIN_UI_RULES.md`. The admin's browser security rules forbid inline scripts and script evaluation, so templates have no inline scripts or event attributes, and no `hx-on`, `js:` values or trigger filters.
   - **Prose:** no em or en dashes, plain words, example hosts on `example.com`.
3. Add or update tests. See [Testing](testing.md).
4. If users see the change, update the matching page under `docs/site` and the developer document it came from.

### Check and send

1. In `backend/`, run the vet and the unit tests. They need no database. The SDK and the example plugins are separate modules, so this does not cover them.

   ```bash
   go vet ./...
   go test ./...
   ```

2. At the repository root, run the guards:

   ```bash
   make check
   ```

   It runs the neutrality check, the migration linter, the script tests, the vendored asset check, the vulnerability scan (needs the network), the licence scan and the docs lint. Each guard and what it catches is listed in [Testing](testing.md).

3. Sign off every commit (`git commit -s`): Hatamoto uses the Developer Certificate of Origin, not a contributor licence agreement. If an AI tool helped, you are still the author and answer for every line. `CONTRIBUTING.md` at the repository root has the details and the response times to expect.

4. Push your branch and open a pull request that says what changed and why. CI runs the backend tests with Postgres, the SDK tests, the admin UI checks and the WordPress importer job.

## Check it worked

`make check` ends without an error, and your pull request shows green CI checks. A failing guard is a message to act on, not an obstacle to route around: the neutrality check, for example, keeps the repository usable by anyone, so if it bans a word you need, rephrase.

If `go test ./...` passes locally but the CI backend job fails, the failure is usually in a test that needs Postgres and skipped on your machine. Run the integration tests as shown in [Testing](testing.md).
