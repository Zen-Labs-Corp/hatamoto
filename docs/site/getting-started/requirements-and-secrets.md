---
title: Requirements and secrets
nav_title: Requirements
description: What Hatamoto needs to run, where to download it, what it creates on first start and the settings worth knowing on day one.
---

This page lists what the server needs, the release files you download, the one secret it relies on, what it creates the first time it starts, and the environment variables worth knowing before your first deploy. To use it step by step, follow [Before you install](before-you-install.md) and then [Install the binary](install-binary.md). Every variable is in the [Configuration reference](../operate/configuration-reference.md).

## Overview

| Term | Meaning on this page |
|---|---|
| Postgres | The database Hatamoto stores everything in. |
| `DATABASE_URL` | The database address: a Postgres connection string that says where the database is and how to sign in. |
| Environment variable | A named setting a program reads when it starts, outside its files. |
| Server secret | A long random value the server uses to sign links and protect stored credentials. It must stay the same across restarts. |
| Migration | A step that updates the database structure to match the program version. |
| Content type | The kind of content, such as a page or a blog post, which decides its fields and its address. |
| Slug | The short name in a page's address, such as `hello` in `/blog/hello`. |
| Setup code | A one-time code the server prints to its log so only someone with access to the server can create the first owner. |
| `serve` | The command that runs the site, admin, API and background worker in one process. |
| `supervise` | The command for a long-running service: it runs `serve` as a child process and manages updates and rollbacks. See [The supervisor](../updates/supervisor.md). |

## Requirements

| Need | Detail |
|---|---|
| Postgres | Version 16 is what Hatamoto is built and tested against. An empty database and a role that owns it. |
| `DATABASE_URL` | The only required variable. A standard Postgres connection string. |
| A server secret | At least 32 characters. Generated for you unless you provide one. |
| A writable directory | `./data/secrets` for the generated secret (or `HATAMOTO_SECRETS_DIR`), `./uploads` for media (or `MEDIA_UPLOAD_DIR`), and `$HATAMOTO_DATA` (default `./data`) when run under `supervise`. Relative paths start from the directory you start the program in. |
| One free port | `8090` by default (`API_PORT`). |
| A supported platform | Linux or macOS, on 64-bit x86 or 64-bit ARM. See [Release files](#release-files). |
| Memory and CPU | No special demands. The server is one program file. A small virtual server is enough for a typical site. |

You do not need Node, Redis, a separate web server or a Go toolchain on the target machine. No telemetry unless you turn it on. On its own Hatamoto makes the signed update check (`HATAMOTO_UPDATES=off` removes it) and the daily download of AI-crawler IP ranges (`HATAMOTO_AGENT_RANGES=off`) and reverse-DNS lookups of addresses that claim to be known crawlers (`WORKER_DISABLE_JOBS=agent_verify`); it reads the plugin registry only when an admin browses or installs from it, or a page shows the plugin directory. The one optional report, [install stats](../operate/install-stats.md), is off until an owner opts in, and `HATAMOTO_TELEMETRY=off` rules it out. [Outbound connections](../operate/outbound-connections.md) lists every call. See also [How updates work](../updates/overview.md).

## Release files

Release builds are published for four platforms. Each file is a single Go binary named `hatamoto-<os>-<arch>`:

| File | Platform | `uname -sm` prints |
|---|---|---|
| `hatamoto-linux-amd64` | Linux, 64-bit x86 | `Linux x86_64` |
| `hatamoto-linux-arm64` | Linux, 64-bit ARM | `Linux aarch64` |
| `hatamoto-darwin-amd64` | macOS, Intel | `Darwin x86_64` |
| `hatamoto-darwin-arm64` | macOS, Apple silicon | `Darwin arm64` |

Files live at `https://hatamoto.dev/releases/<version>/hatamoto-<os>-<arch>`, where `<version>` has the form `vX.Y.Z` with the leading `v`. The `releases` address has no index page, so you need the version first.

### The current version

The current versions are listed in the stable channel file, `https://hatamoto.dev/releases/channels/stable.json`. It is a signed document: the list sits under `index`, next to `signer`, `algorithm` and `signature`. Take the highest version number in `index.releases` that is not marked `"halted": true` and does not appear in `index.yanked`. Both flags mean the release was withdrawn because of a problem. A withdrawn version stays in `index.releases`, and `index.yanked` comes after it in the file. `index.releases` is in the order the releases were published, not in version order: a fix for an older line published later is listed after a newer version, so the last entry is not always the one to take. `stable` is the default channel; `edge` and `canary` files exist at the same place for the other channels.

To build the program from source instead, see [Contributing](../migrate-contribute/contributing.md); a source checkout also has its own `Dockerfile` and `docker-compose.yml` that build from source.

To list the versions and their flags from a terminal:

```bash
curl -fsSL https://hatamoto.dev/releases/channels/stable.json | grep -E '"(version|halted|yanked)"'
```

This prints each release's `"version"` line followed by its `"halted"` line, then the `"yanked"` line. With one release halted and one yanked, the output looks like this:

```text
        "version": "v0.26.0",
        "halted": false
        "version": "v0.27.1",
        "halted": false
        "version": "v0.28.0",
        "halted": true,
        "version": "v0.27.2",
        "halted": false
    "yanked": [
        "version": "v0.27.1",
```

Here `v0.28.0` is halted and `v0.27.1` is yanked (every version printed after the `"yanked": [` line is yanked), so the version to install is `v0.27.2`. When nothing is yanked the last line is `"yanked": []`.

In a `Dockerfile`, BuildKit (the default builder of Docker Compose v2) fills the `TARGETARCH` build argument from the machine, so the `Dockerfile` in [Install with Docker](install-docker.md) downloads `hatamoto-linux-amd64` or `hatamoto-linux-arm64` with no extra setting. It reads the version from the `VERSION` line of `.env`.

### Check a download

Each release directory also holds `release.json`, a signed record with the size and SHA-256 hash of all four binaries, and its signature `release.json.sig`. To check your download, compare the output of `sha256sum hatamoto` with the hash for your platform in `release.json`. This catches a corrupted download only, because both files come from the same host.

The signature is verified by `hatamoto update apply` (given `--sig release.json.sig` for a file-based install) and by the supervisor, against the release keys built into the binary. The same files are what a manual update needs; see [Manual update CLI](../updates/manual-update-cli.md).

### The version command

`hatamoto version` prints the version, the commit and the plugin SDK version stamped into the build:

```text
hatamoto v0.31.0 (commit 15887307b221388a8a86d939775cdaa10ad0f8ca, sdk 0.12.0)
```

`hatamoto version --json` prints the same as JSON and adds the database schema head and the older SDK versions the build still serves, which is useful in a bug report.

## The server secret

Hatamoto uses one secret to sign the flash cookie and theme preview links, to protect stored import credentials and to encrypt the SMTP password saved in **Settings > Email**. It must stay the same across restarts, otherwise preview links and stored credentials stop working, and you are asked to re-enter the saved SMTP password.

The variable is `HATAMOTO_SECRET`. `JWT_SECRET` is the old name of the same setting and is still read. If both are set, `HATAMOTO_SECRET` wins. A value shorter than 32 characters stops the server with `HATAMOTO_SECRET must be at least 32 characters`.

### How it is resolved

At `serve` start, before the configuration is loaded, in this order:

1. **The environment wins.** If `HATAMOTO_SECRET` or `JWT_SECRET` is set, it is used as is and never written to disk.
2. **A saved file.** Otherwise Hatamoto reads `secrets.env` in `HATAMOTO_SECRETS_DIR` (default `./data/secrets`). It accepts a `HATAMOTO_SECRET` line or an old `JWT_SECRET` line.
3. **Generate and save.** If the file has neither, Hatamoto generates 48 random bytes, encodes them as URL-safe base64, logs `generated secret on first boot` with the file path, and writes the file atomically with mode `0600`. The directory is created with mode `0700`.

If the file cannot be written, the server stops and tells you to either set `HATAMOTO_SECRET` in the environment or make `HATAMOTO_SECRETS_DIR` writable.

The file looks like this:

```text
HATAMOTO_SECRET=<64 characters of base64>
```

### Which one should I use?

- **Local and evaluation:** let Hatamoto generate it. Keep the secrets directory on a persistent disk or volume. In Docker it lives at `/data/secrets`, in its own volume.
- **Production:** provide your own from a secret manager. A long random value works:

```bash
openssl rand -base64 48
```

Pass it to the process through the service manager's environment or an `EnvironmentFile`, never on a command line that others can read in `ps`.

### Rotating the secret

Changing the secret invalidates the signed flash cookie and theme preview links, and stored import credentials that were keyed by the old value can no longer be read, so re-enter them afterwards. Pick a value and keep it.

## What happens on first boot

`serve` prepares a new database on its own. Each step only fills what is missing, so running it again changes nothing, and restarting or starting several processes at once is safe. In order:

1. **A server secret.** If neither `HATAMOTO_SECRET` nor `JWT_SECRET` is set, one is generated and saved, as described in [How it is resolved](#how-it-is-resolved).
2. **Configuration.** The environment is loaded and checked. A missing `DATABASE_URL` or a bad value stops the boot with a message naming the variable.
3. **Migrations.** The migrations built into the program are applied under a Postgres advisory lock, so two processes starting together cannot race and the second finds nothing to do. They create the tables and the built-in `page` and `post` content types.
4. **Site configuration.** A single site configuration row is inserted. Its site name comes from `HATAMOTO_SITE_NAME` (default `My Hatamoto site`). After you save a name in the admin, the stored name wins and the variable is ignored.
5. **A starter home page.** If there is no content at all, Hatamoto inserts a published page with the slug `home`, a hero section and the text "Your site is running. Sign in to the admin to edit this page." So `/` renders instead of returning 404. If you later delete that page, Hatamoto does not bring it back, because it only fills a database with no content.
6. **A setup code.** While the database has no users, a setup code is created and logged, and a second warning suggests `hatamoto user create` as the alternative.
7. **Listening.** The server listens on `API_HOST:API_PORT` (default `0.0.0.0:8090`) and starts the background worker, unless `HATAMOTO_WORKER=off`.

### The setup code

The setup code stops whoever reaches a brand-new install first from claiming the owner account: the person who creates it must prove they can read the server's log or its environment.

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

| Rule | Detail |
|---|---|
| Default | A random code of eight characters, shown as two groups of four such as `K7M2-RT9P`, generated on every boot while there are no users. |
| Alphabet | Capital letters and digits without look-alikes: no `0`, `O`, `1`, `I` or `L`. |
| Log line | `Setup: open /k7qm-x2rf-9tdc/setup and enter code K7M2-RT9P`, a warning that also carries the code as `setup_code`. The address is your own, with your site address in front when one is set. |
| Your own code | Set `HATAMOTO_SETUP_CODE`. It must be at least 12 characters (case, spaces and dashes do not count) and not one repeated character or pattern. While the site has no users, a weaker code makes the server refuse to start with `HATAMOTO_SETUP_CODE must be at least 12 characters`; once the site has users the code is unused and only logs a warning. The code is then not printed; the log says `Setup: open /k7qm-x2rf-9tdc/setup and enter the setup code from HATAMOTO_SETUP_CODE`. |
| Matching | Case, spaces and dashes are ignored, so `k7m2 rt9p` works. |
| Storage | The server keeps only a hash of the code in memory. |
| After the first user | Once any user exists there is no code, and the wizard's first step refuses new accounts with "Setup already completed". |
| In the env file | A VPS install made with this installer version or later removes the `HATAMOTO_SETUP_CODE` line from the env file a few minutes after the owner account exists, so the code is not kept there or in backups of that file. On an older install, delete that line by hand once the owner exists. |
| Later wizard steps | Steps 2 to 5 of the wizard are open to owners only. Once setup is finished, `<admin>/setup` and the steps before **Done** answer "Page not found". |

The wizard itself is described in [First run](first-run.md).

## Environment variables for day one

These are the ones most people set before opening a site to the world. Defaults come from `backend/internal/config`.

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | none (required) | Postgres connection string. |
| `API_HOST` and `API_PORT` | `0.0.0.0` and `8090` | Listen address. |
| `ENVIRONMENT` | `development` | `production` switches to JSON logs and sends `Strict-Transport-Security`. Only use it behind HTTPS. |
| `PUBLIC_BASE_URL` | empty | Pins the public origin for canonical links, sitemaps and emailed links. The page cache only preloads pages when it is set. |
| `HATAMOTO_SITE_NAME` | `My Hatamoto site` | Seeds the site name on first boot only. |
| `HATAMOTO_SETUP_CODE` | random each boot | First-run wizard code, at least 12 characters. |
| `HATAMOTO_SECRET` | generated | Server secret, at least 32 characters. `JWT_SECRET` is its old name. |
| `HATAMOTO_SECRETS_DIR` | `./data/secrets` | Where the generated secret is kept. |
| `HATAMOTO_DATA` | `./data` (`/data` in the image) | State directory for the supervisor and the plugin compile cache. |
| `MEDIA_UPLOAD_DIR` | `./uploads` | Where uploaded files are stored. |
| `HATAMOTO_UPDATES` | `auto` | `auto`, `notify` or `off`. |
| `HATAMOTO_WORKER` | on | Set to `off` to stop the background worker in this process. |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | host `localhost`, port `587` | Outgoing mail, used only while nothing is saved in **Settings > Email**. Email is off while `SMTP_HOST` is empty or `localhost` and nothing is saved there, and form notifications and other emails are skipped. `SMTP_FROM` is required once a real host is set. |

### Notes on a few of them

- **`ENVIRONMENT=production` and HSTS.** Browsers cache the header for a year and apply it to every subdomain. Confirm nothing under your domain is HTTP only first.
- **`PUBLIC_BASE_URL` and the Site address setting.** Either one lets password reset and invitation emails be sent, as long as email is configured. Setting it in the environment pins it.
- **`HATAMOTO_SITE_NAME`.** It is a seed. After you save a name in the admin, changing the variable does nothing.
- **Removed variables.** `HATAMOTO_THEME_DIR` and `HATAMOTO_SITE_THEME_DIR` no longer exist, and the server refuses to start if either is set. Themes and plugins are packages stored in Postgres.

## Checking a running server

`GET /health` answers `200` while the server is up. `hatamoto healthcheck` requests `http://127.0.0.1:<API_PORT>/health` (port `8090` unless `API_PORT` is set) with a 4 second timeout. It exits with status 0 and no output on a `200`, and logs the error and exits with status 1 otherwise. A container image can use it as its health check without `curl` or `wget`.

## Files Hatamoto writes

| Path | Written by | Contents |
|---|---|---|
| `$HATAMOTO_SECRETS_DIR/secrets.env` | `serve` | The generated server secret, mode `0600`. |
| `$HATAMOTO_DATA/releases/` | `supervise` | Installed releases and update state. |
| `$HATAMOTO_DATA/cache/wasm` | the plugin runtime | Compiled plugin cache (`HATAMOTO_PLUGIN_CACHE_DIR` overrides it). |
| `MEDIA_UPLOAD_DIR` | the media library | Uploaded files. |

Back up the database, the secrets file if you rely on the generated secret, and the media directory. See [Backups](../operate/backups.md).

## Limits and errors

The server logs one line and exits when it cannot start. The common messages:

| Message | Cause | Fix |
|---|---|---|
| `invalid configuration: DATABASE_URL is required` | `DATABASE_URL` is not set. | Set it in the environment the program starts with. |
| `connect to database: failed to ping database` | Postgres is not reachable, or the user, password or database name is wrong. | Check that Postgres runs and that every part of the address is right. |
| `HATAMOTO_SECRET must be at least 32 characters` | The secret you set is too short. | Use a longer value, or unset it and let Hatamoto generate one. |
| `cannot persist generated secret` | The secrets directory is not writable. | Set `HATAMOTO_SECRET`, or make `HATAMOTO_SECRETS_DIR` writable. |
| `HATAMOTO_SETUP_CODE must be at least 12 characters` | The chosen setup code is too short and the site has no users yet. | Use at least 12 characters, or unset it. |
| `SMTP_FROM is required when SMTP_HOST is set` | Email is turned on without a sender address. | Set `SMTP_FROM`. |
| `HATAMOTO_THEME_DIR is no longer read` (or `HATAMOTO_SITE_THEME_DIR`) | A removed variable is set. | Pack the theme, install it as a package, then unset the variable. |
