---
title: Command-line reference
nav_title: Command-line reference
description: Every hatamoto command and flag, from running the server to users, content, redirects, packages, API keys and updates.
---

This page lists every command of the `hatamoto` program and its flags, grouped by what they do. To run them on a server with the service's settings, see [Deploy with systemd](deploy-systemd.md) or [Deploy with Docker](deploy-docker.md).

## Overview

`hatamoto` is one program with many commands. Run with `help`, `-h` or `--help`, it prints the usage text and exits 0. Run with no arguments, with an unknown command or with a malformed argument list, it prints the usage and exits with status 2. A command that runs and fails logs the error and exits with status 1.

Most commands open the database named by `DATABASE_URL` and use the same services as the web admin, the API and the MCP server. Output goes to standard output, warnings and errors to standard error. Dates are `YYYY-MM-DD` in UTC.

```bash
DATABASE_URL=postgres://user:pw@localhost:5432/hatamoto?sslmode=disable hatamoto serve
hatamoto version --json
```

## Run commands as the service

Commands read the same environment variables as the server (see the [configuration reference](configuration-reference.md)). Run them as the service user and with the service's environment, or they look for `./data` in the current directory and may leave files the service cannot read.

Under systemd, with the layout from [Deploy with systemd](deploy-systemd.md):

```bash
sudo -u hatamoto bash -c 'set -a; . /opt/hatamoto/config/hatamoto.env; set +a
  cd /opt/hatamoto && HATAMOTO_DATA=/opt/hatamoto/data exec bin/hatamoto migrate status'
```

Under Docker Compose, the image's entry point is the program, so name the command after the service. It runs as the image's `app` user (uid 10001):

```bash
docker compose exec hatamoto hatamoto migrate status
```

## Running the server

| Command | What it does |
|---|---|
| `hatamoto serve` | Ensures the server secret exists, loads and checks the configuration, connects to the database, applies migrations, seeds a fresh database (site configuration and a starter home page), then runs the site, API, admin and worker on `API_HOST:API_PORT`. `HATAMOTO_WORKER=off` turns the worker off. |
| `hatamoto supervise` | The process to run as the service. Runs `serve` as a child process, keeps releases in `$HATAMOTO_DATA/releases`, switches to a newly applied release only after it starts and answers, and rolls back on its own. Needs no database. The systemd `ExecStart` and the Docker `CMD` use it. |
| `hatamoto healthcheck` | Exits 0 when `http://127.0.0.1:$API_PORT/health` answers 200 within 4 seconds, otherwise non-zero. The Docker image's `HEALTHCHECK`. |
| `hatamoto version [--json]` | Prints the version and commit. `--json` adds the plugin SDK version, the database schema head and the SDK versions this release stays compatible with. |
| `hatamoto routes` | Lists the built-in HTTP routes as `PATTERN<TAB>handler`. |
| `hatamoto help` | Prints the usage text. |

### Serve or supervise

| Command | Use it for |
|---|---|
| `hatamoto serve` | Trying Hatamoto, development, or when another tool already manages the process and you want no update machinery. |
| `hatamoto supervise` | The long-running service. It runs `serve` as its child and manages releases and rollbacks. |

`supervise` keeps its state in `$HATAMOTO_DATA` (default `./data`, relative to the directory you start it from), and is what makes signed updates possible. It stops its child within 40 seconds, so give the service manager a longer stop timeout. See [The supervisor](../updates/supervisor.md).

## Migrations

| Command | What it does |
|---|---|
| `hatamoto migrate` or `hatamoto migrate up` | Applies pending migrations. They are embedded in the program. A Postgres advisory lock lets only one run at a time. |
| `hatamoto migrate status` | Lists each migration as `VERSION  STATE  FILE`, so you can see which are applied. |

`serve` runs the same migrations at boot, so running `migrate` by hand is for checking, or for applying them before you start a new release. A database that a newer release already migrated further than this program knows is accepted: `migrate` finds nothing to apply.

## Users

```text
hatamoto user create --email E [--name N] [--role owner|editor] [--password P | --password-stdin]
hatamoto user reset-password --email E [--password P | --password-stdin]
```

| Flag | Commands | Meaning |
|---|---|---|
| `--email` | both | Required. The login address. |
| `--name` | `create` | Display name. |
| `--role` | `create` | `owner` or `editor`. Default `owner`. Any other value is rejected with `invalid role`. |
| `--password-stdin` | both | Read the password from the first line of standard input. |
| `--password` | both | Pass the password as an argument. It shows up in the process list and shell history, so prefer `--password-stdin`. |

Rules for both commands:

- `--password` and `--password-stdin` cannot be combined.
- A password you supply must be at least 12 characters. Without one, a random 24-character password is generated and printed once as `temporary password: ...`.
- An unexpected extra argument is an error.

`user create` prints `created <role> user <email>`. An email that already exists fails with an error and changes nothing.

`user reset-password` does three things at once: it sets the new password, clears the login lockout (five failed sign-ins in a row lock an account for 15 minutes), and signs the user out everywhere by revoking all their sessions. It prints `password reset for <email>`. If the address has no account, it says `no user with email ...` and changes nothing. If the password changed but revoking the sessions failed, it says so and exits with an error; run it again.

Examples:

```bash
hatamoto user create --email editor@example.com --name "Sam Editor" --role editor
printf '%s\n' "$NEW_PASSWORD" | hatamoto user create --email you@example.com --password-stdin
printf '%s\n' "$NEW_PASSWORD" | hatamoto user reset-password --email you@example.com --password-stdin
docker compose exec hatamoto hatamoto user create --email you@example.com --role editor
```

These commands also work on an imported database, where users have no password until you reset one. For when to use the admin instead, see [Create users](../getting-started/create-users.md).

## Admin address

```text
hatamoto admin-url
hatamoto admin-url set <path> [--i-understand-weak]
hatamoto admin-url random
```

`hatamoto admin-url` prints your admin address, with your public site address in front when one is set. It reads the database, so run it with the service's settings loaded. A fresh install picks a random address at its first start, so before that start the command says no address is chosen yet. `set` stores an address you choose (one segment of 4 to 64 lower-case letters, digits and dashes) and refuses a guessable one unless you add `--i-understand-weak`; `random` stores a new random address. Both print the new link and ask you to restart `hatamoto`. When `HATAMOTO_ADMIN_PATH` is set it decides the served address, and the command prints that one.

## Plugins in development

`hatamoto plugins list` lists the plugins found in the directory `HATAMOTO_PLUGIN_DIR` (the development override) as `name version sdk`, or the reason a manifest is unusable. When the variable is not set, it says so: plugins then come from installed packages. For installed plugins use `package list`.

## Content

```text
hatamoto entity list [--schema S] [--status S] [--search Q] [--limit N]
hatamoto entity get <id>
hatamoto entity create --schema S --name N [--slug SLUG] [--attributes JSON | --attributes-file F] [--publish] [--as EMAIL]
hatamoto entity publish <id> [--as EMAIL]
hatamoto entity unpublish <id> [--as EMAIL]
```

`entity list` prints one tab-separated row per item (id, status, schema, slug, name), up to `--limit` rows (default 50, at most 100), filtered by schema slug, status (`draft`, `scheduled`, `published` or `archived`) and a name search. `entity create` makes a draft unless `--publish` is given; `--slug` defaults to one made from the name. It checks the attributes against the schema and sanitises rich-text HTML as the API does. `--as EMAIL` attributes the change to an existing user; without it the author columns stay empty. `get`, `create`, `publish` and `unpublish` print the item as JSON.

```bash
hatamoto entity create --schema page --name "About us" --slug about --attributes '{"content":"<p>Hello</p>"}' --publish --as you@example.com
```

```text
hatamoto sanitize-html-sections [--dry-run]
```

Rewrites every stored HTML section (and page-builder component content) to its sanitised form in one transaction. With `--dry-run` the transaction is rolled back and the counts show what would change.

```text
hatamoto builder convert [--dry-run] [--verify-render] [--entity ID]
```

Moves pages from `page_sections` to the page-builder document tree. Each page is rendered both ways and compared before any write; a page that differs is not written and the command exits 1. `--dry-run` writes nothing, `--entity ID` converts one page. `--revert` is no longer supported and exits 1 with `unsupported`. Output is one row per page followed by a summary line. Pages also convert on their own when saved in the builder or written through the API, so the command is for converting everything at once; `serve` logs `pages without a page_doc` at boot with the number still to convert.

### What the command line does not do

The command line is a separate process. Its page and menu writes make a running server clear its page cache within a few seconds (see [Multiple replicas and health checks](multi-replica-and-health.md#the-page-cache)). It does not fire `entity.*` plugin events (so [webhooks](../admin/webhooks.md) send nothing for command-line changes), does not run plugin `before_save` checks and does not reindex internal links. Use the API or the MCP server when those must run. See [Multiple replicas and health checks](multi-replica-and-health.md).

## Redirects

```text
hatamoto redirect list [--search Q]
hatamoto redirect create --from /old --to /new [--status 301]
hatamoto redirect delete <id>
```

`list` prints up to 200 rules as `id  status  source -> target` and says how many more there are; narrow it with `--search`, which matches text in the source or target. `--status` is one of `301` (default), `302`, `307` or `308`. The same checks as the admin apply. Redirects are checked before the page cache, but each running server reads its redirect rules again every 60 seconds and the command line cannot clear that copy, so a new redirect shows up on a running server within about a minute. The rules themselves are described in [Redirect rules](../migrate-contribute/redirect-rules.md).

## Analytics

```text
hatamoto analytics crawlers [--from D] [--to D] [--limit N]
hatamoto analytics first-crawls [--from D] [--to D] [--limit N]
hatamoto analytics referrals [--from D] [--to D]
```

`crawlers` shows AI-crawler fetches per page and agent, `first-crawls` the first time each page was fetched, and `referrals` AI-assistant visits per day, all as JSON. `--to` defaults to today, `--from` to 29 days before `--to`, and `--limit` to 20 (at most 100). See [Crawler analytics](../ai-seo/crawler-analytics.md).

## Export, import and WordPress

```text
hatamoto export <file.tar.gz> [--no-submissions]
hatamoto import <file.tar.gz>
hatamoto import wordpress (--url URL [--user U --app-password P] | --wxr FILE) [--scan-only] [--resume RUN] [--audit-url URL]
```

`export` writes the whole site as a portable archive; `-` writes to standard output, and `--no-submissions` leaves out form submissions. `import` loads an archive into an empty database only, after which users need `user reset-password`. `import wordpress` reads a live WordPress site over its REST API, with an application password when one is needed (the `WP_APP_PASSWORD` environment variable can supply it), or a WXR export file. `--scan-only` reports without writing, `--resume RUN` continues an interrupted run, and `--audit-url URL` checks every old URL against a running Hatamoto site.

Further flags of `import wordpress`: `--allow-private-hosts`, `--no-permalinks`, `--keep-edited`, `--json`, `--media-concurrency N` (default 4) and `--max-items N` (default limit 50000). See [WordPress import](../migrate-contribute/wordpress-import.md) and [Export and import](../migrate-contribute/export-import.md).

## Packages, keys and themes

```text
hatamoto keys generate [--name N] [--dir D]
hatamoto theme pack <dir> [--key F] [--out F]
hatamoto plugin pack <dir> [--key F] [--out F]
hatamoto registry index <dir> --key F [--out F] [--url-prefix P] [--valid-days N] [--meta F]
hatamoto registry page <registry.json> [--out F] [--title T] [--intro T] [--write-url U] [--submit-url U] [--install-hint T] [--trusted-key K]
hatamoto package install <file|url> [--grant P,P|all] [--source upload|url|dev] [--dev] [--confirm]
hatamoto package list [--json]
hatamoto theme activate <name|default> [--force]
hatamoto theme dev <dir> --dev [--no-activate] [--confirm]
```

- `keys generate` writes an ed25519 key pair, `N.key` (secret, mode 0600) and `N.pub` (public), into `--dir` (default `.`), named `hatamoto-signing` unless you pass `--name`. It refuses to overwrite an existing key, and prints the `HATAMOTO_TRUSTED_KEYS` line a server needs to trust it.
- `theme pack` and `plugin pack` build a `.hmp` package from a directory. `--key` names the signing key file and defaults to the `HATAMOTO_SIGNING_KEY` environment variable. `--out` defaults to `<name>-<version>.hmp`. A plugin directory holds `plugin.json` and `plugin.wasm`, a `widgets/` directory, or both.
- `registry index` builds a signed registry index for the `.hmp` files in a directory: `--out` defaults to `registry.json`, `--url-prefix` to `packages/`, and `--valid-days` to 90 (1 to 180). `--meta F` names a JSON file with each package's author, source, commit and tier.
- `registry page` renders the plugins of a signed registry index as one static HTML page. It is for operators without a CMS page: a Hatamoto site normally shows the registry with the "Plugin directory" section instead ([Section library](../themes/sections.md)). It writes `plugins/index.html` unless you pass `--out`. The index must verify against the keys a server trusts plus any `--trusted-key` (repeatable); nothing is written otherwise. The other flags set the title (default `Plugins`), the intro text, optional "Write a plugin" and "Submit a plugin" links and a line under the list.
- `package install` installs from a file or an https URL. `--grant` grants plugin permissions in advance (comma separated, or `all`). `--source` records where it came from (`upload`, the default for a file, `url` or `dev`). `--dev` allows a development package (never in production) and `--confirm` replaces an older version, another signer, or a theme that would lose sections the content uses.
- `package list` shows installed plugins and themes with version, source, signer and the active theme; `--json` prints JSON.
- `theme activate` runs the checks of **Themes** in the admin. It refuses while the content uses sections the theme lacks, unless `--force`. `default` is the built-in theme.
- `theme dev` pushes a theme directory as an unsigned development package, and again on every change. `--no-activate` installs it but leaves the active theme alone. It needs `--dev` (or `ENVIRONMENT=development` in the shell).

Development packages are refused when `ENVIRONMENT=production` is set in the shell, or when the database belongs to a server that last booted in production. All of these commands except `keys`, the two `pack` commands and the two `registry` commands use the database. A change they write is picked up by a running server within about five seconds (one second outside production), with no restart. Signing, trust and the registry are described in [Packaging themes](../themes/packaging.md) and [Packaging and signing plugins](../plugins/packaging-signing.md).

## API keys and plugin settings

```text
hatamoto apikey create --name N --scopes a,b [--out FILE] [--as EMAIL] [--description D] [--rate-limit N]
hatamoto plugin settings set <plugin> key=value...
```

`apikey create` prints the new key once: to the new file `FILE` with mode 0600 (an existing file is never overwritten), or alone on standard output when `--out` is omitted. Scopes are checked as in the admin, and `--rate-limit` is in requests per minute (the admin default when omitted). The key acts with its creator's authority: `--as EMAIL`, or the oldest owner. The audit log records the name, scopes and prefix, never the key.

`plugin settings set` takes top-level `key=value` pairs, checks them against the plugin's `settings_schema` and saves them, merging over the stored settings. Keep secrets off the command line: `token=@/run/secrets/acme` reads the value from a file and `token=-` from standard input. A literal value that starts with `@` is written `@@x`.

## Updates

```text
hatamoto update status
hatamoto update check [--dry-run]
hatamoto update pin vX.Y
hatamoto update unpin
hatamoto update apply vX.Y.Z [--force]
hatamoto update apply --file F --record R [--sig S]
hatamoto update rollback [--force]
```

| Verb | Needs | What it does |
|---|---|---|
| `status` | database | Shows the mode, channel, pin and the last check. |
| `check` | database, network | Checks the release channel now. `--dry-run` decides but installs nothing. |
| `pin` and `unpin` | database | Stay on one minor line (security releases still apply), or release the pin. |
| `apply vX.Y.Z` | network, and the database when `DATABASE_URL` is set | Downloads, checks and installs a named release, majors included. `--force` skips the plugin compatibility check. |
| `apply --file --record` | nothing | Installs a signed release from local files. The signature file defaults to `<record>.sig`. |
| `rollback` | nothing | Switches back to the previous release. `--force` allows a previous release that is on the withdrawn list. |

Run every `update` verb with `HATAMOTO_DATA` set to the service's data directory, or from the service's working directory: without the variable, they refuse a `./data` that has no supervisor state. Every verb except `status` also refuses to run as a user other than the owner of the releases directory (or of the data directory, before it has one), so run it as the service user and never as root. `HATAMOTO_UPDATE_URL` overrides the release location. See [Manual update command](../updates/manual-update-cli.md).

## Environment used by the command line

| Variable | Used by |
|---|---|
| `DATABASE_URL` | Everything except `supervise`, `healthcheck`, `version`, `routes`, `help`, `plugins list`, `keys`, the `pack` commands, `registry`, `update apply --file` and `update rollback`. |
| `HATAMOTO_DATA` | `serve`, `supervise` and the update commands. |
| `HATAMOTO_SECRETS_DIR` | `serve` only. |
| `HATAMOTO_SIGNING_KEY` | Default for `--key` in the `pack` and `registry index` commands. |
| `HATAMOTO_TRUSTED_KEYS` | `package install`, `theme activate`, `theme dev` and `registry page`. |
| `API_PORT` | `healthcheck`. |
| `WP_APP_PASSWORD` | Default password for `import wordpress`. |
| `MEDIA_UPLOAD_DIR` | `export`, `import` and `import wordpress`. |
| `ENVIRONMENT` | `package install` and `theme dev` (development packages). |
