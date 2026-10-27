---
title: Package format and commands
nav_title: Package format
description: What a theme package may contain, the limits it must meet, how signatures and trust work, the registry, and every theme command.
---

This page lists the rules for theme packages: the files a package holds, the archive limits, signatures and trusted keys, the settings that control them, registries, and the commands that build and install packages. To package your first theme, follow [Package and share a theme](packaging.md).

## Overview

A theme ships as one `.hmp` file, which is a zip archive. The server stores it in Postgres and applies it while running; nothing is copied onto the server's disk and no restart is needed. A package is signed with an ed25519 key, and a server installs only packages signed by a key it trusts, unless the owner explicitly allowed unsigned packages.

```text
theme.json        the manifest (name, version, optional description, settings_schema and builder)
layouts/  partials/  sections/  schemas/    templates and schemas
assets/           public files
signature.json    written by `hatamoto theme pack --key`
README.md  LICENSE  CHANGELOG.md   allowed at the root, ignored by the host
```

## The manifest

`theme.json` needs `name` and `version`, and may carry `description`, `settings_schema`, `screenshot` and `builder` (see [Builder contract](builder-contract.md)).

| Key | Rule |
| --- | --- |
| `name` | a lower-case letter, then 1 to 39 lower-case letters, digits or hyphens. `default` is reserved for the built-in theme |
| `version` | a semantic version such as `1.0.0` |
| `description` | shown on the theme's card |
| `settings_schema` | a JSON Schema in the supported subset; it becomes the theme's **Settings** form and reaches templates as `.Settings` |
| `screenshot` | optional preview image under `assets/` (for example `assets/screenshot.webp`): PNG, WebP or JPEG, never SVG, at most 2 MiB, 640 to 2560 by 360 to 1600 pixels. An invalid screenshot is ignored on install. The registry requires it, and `hatamoto theme check <dir> --registry`, `theme pack --registry` and the registry build check it strictly |

```json
{
  "name": "my-theme",
  "version": "1.0.0",
  "description": "A calm single-column theme.",
  "settings_schema": {
    "type": "object",
    "properties": {
      "accent": { "type": "string", "title": "Accent colour", "default": "#0a5cff" }
    }
  }
}
```

Install and update are keyed by kind and name, so installing version 1.1.0 of an installed theme replaces 1.0.0. Theme settings are stored per theme and kept across updates.

## What a package may contain

At the root, a package may hold `theme.json` (or `plugin.json` for a plugin), `signature.json`, `README.md`, `LICENSE` and `CHANGELOG.md`. The allowed top-level directories are `layouts`, `partials`, `sections`, `schemas` and `assets` for themes, plus `migrations`, `ui` and `widgets`, which plugins use. A package holds exactly one of `theme.json` or `plugin.json`. A theme package must not hold `plugin.wasm` or a `widgets/` directory.

Anything else at the root, any other top-level directory, hidden files such as `.git` and `.env`, and `node_modules` are refused by the parser and skipped by `pack`. A symbolic link in the folder stops `pack` with `not a regular file (links are not packed)`.

### Safety rules for the archive

Parsing is safe against hostile input and runs before anything else, including the signature check. A package is refused when it breaks one of these limits:

| Rule | Limit |
| --- | --- |
| archive size | 64 MiB |
| entries | 2048 |
| one file, uncompressed | 64 MiB |
| all files, uncompressed | 128 MiB |
| compression ratio of a file over 1 MiB | 200:1 (zip bomb) |
| entry name | at most 200 bytes, valid UTF-8, no backslash, colon or control byte |
| paths | no `..`, no absolute or unclean paths, no hidden segments |
| entry type | regular files only; symlinks, devices and pipes are refused |
| duplicates | none, compared case-insensitively |
| encryption | refused |

Sizes are enforced on the bytes actually read, never on the sizes the archive declares. Nothing is extracted to disk: files are read from the stored archive in memory. Files served to browsers (`assets/`) carry `X-Content-Type-Options: nosniff` and a sandboxing `Content-Security-Policy`, so an SVG or HTML file opened directly runs without script and without the site's origin.

## Signatures and keys

Packages are signed with ed25519. The signature covers a digest of the package: SHA-256 over every file name, size and SHA-256, sorted by name and without `signature.json`. It therefore survives recompression but not a changed byte. `signature.json` looks like this:

```json
{"signer": "3f9a1c2b7d4e5f60", "algorithm": "ed25519", "signature": "<base64>"}
```

The `signer` is the key id: the first 16 hex characters of the SHA-256 of the 32-byte public key.

`hatamoto keys generate` writes two files: `<name>.key`, the secret key (mode `0600`), and `<name>.pub`, a JSON file with `key_id` and `public`. The `public` value, a base64 public key, is what goes into `HATAMOTO_TRUSTED_KEYS`. The command prints it as a ready line. Existing files are never overwritten. Keep the `.key` file out of version control.

## Trust and install checks

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

The server trusts the built-in first-party key and the keys listed in `HATAMOTO_TRUSTED_KEYS`. The CLI reads the same variables, so set them in the shell too when you install from a terminal. Install checks, in order:

1. The package parses within the limits above.
2. If it is signed, the key must be trusted and the signature must verify. An unknown signer or a bad signature is always refused, whatever settings say. The message is `package is signed by a key you do not trust (key <id>)`.
3. If it is unsigned, it is refused unless the owner switched on **Allow unsigned packages** on the Plugins page (`<admin>/plugins`). That setting is off by default, needs an acknowledgement tick, and the page then shows a standing warning.
4. A `theme dev` push is an unsigned development package, allowed only when the server's `ENVIRONMENT` is not `production`. A production server never loads development packages and ignores any such row in its database at load and activate; the admin shows why.
5. Replacing an installed package asks for confirmation when the new version is not newer, when another key (or no key) signed it, or when it is the active theme and would drop section types your content uses. The CLI needs `--confirm` for these.

`HATAMOTO_TRUST_FIRST_PARTY=false` drops the built-in first-party key, so only `HATAMOTO_TRUSTED_KEYS` is trusted. A package whose signer is no longer trusted is not loaded at boot or at the next check. The server logs `installed package not loaded`, the card shows **Not used:** with the reason, and an active theme falls back to the built-in one. To rotate a key, add the new one, re-sign and update your packages, then remove the old key.

Installs, updates, activations and every switch of the unsigned opt-in are written to the admin audit log with who did it (the signed-in user, `api` or `cli`).

## Configuration

| Variable | Default | Effect |
| --- | --- | --- |
| `HATAMOTO_REGISTRY_URL` | `https://hatamoto.dev/registry.json` | signed registry index to browse. `off` disables browsing, while upload and the CLI still work. Must be `https://` |
| `HATAMOTO_REGISTRY_ALLOW_HTTP` | unset | `true` allows an `http://` registry, for development only |
| `HATAMOTO_TRUSTED_KEYS` | empty | base64 ed25519 public keys of trusted signers, separated by commas or spaces. A value that is not a 32-byte key stops the server at boot |
| `HATAMOTO_TRUST_FIRST_PARTY` | trusted | `false` stops trusting the built-in first-party key |
| `HATAMOTO_SIGNING_KEY` | unset | default `--key` of `theme pack`, `plugin pack` and `registry index` |

The unsigned opt-in and the active theme are stored in the database, not in the environment. `HATAMOTO_THEME_DIR` and `HATAMOTO_SITE_THEME_DIR` are no longer read, and the server refuses to boot while either is set (see [Engine details](engine-details.md)).

## The registry

A registry is a static `registry.json`, a signed envelope that holds an index and an ed25519 signature over its compact JSON. Each package entry has `kind`, `name`, `version`, `description`, `url` (relative to the index or absolute), `sha256`, `size` and optionally `permissions` and `homepage`. The index carries `issued_at` and `expires_at` (RFC 3339, at most 180 days apart), which are signed with it, so an old copy cannot be replayed to hide newer versions.

The server refuses an index that is unsigned, signed by an untrusted key, altered, expired or issued in the future. It checks each download against `sha256`, then verifies the package like any other. Registry fetches go through the server's guard against requests to internal addresses and are cached for 30 seconds.

To run your own registry, pack and sign each package into one directory, host the files, and build the signed index with `hatamoto registry index` (below). Re-run it on a schedule, because the index expires. Anyone can run a registry: users add the registry's signing key to `HATAMOTO_TRUSTED_KEYS` and point `HATAMOTO_REGISTRY_URL` at it.

## Commands

All commands are subcommands of the `hatamoto` program. `keys generate`, `theme pack` and `registry index` need no database. The others need `DATABASE_URL`, write to the database, and a running server applies the change on its next check (every second outside production, every five seconds in production).

### `hatamoto keys generate`

| Flag | Default | Meaning |
| --- | --- | --- |
| `--name` | `hatamoto-signing` | file name of the key pair |
| `--dir` | `.` | directory to write to |

### `hatamoto theme pack <dir>`

| Flag | Default | Meaning |
| --- | --- | --- |
| `--key` | `HATAMOTO_SIGNING_KEY` | secret key file that signs the package. Without a key the package is unsigned |
| `--out` | `<name>-<version>.hmp` | output file |

Output is deterministic (sorted entries, fixed timestamps) and is parsed again before it is written, so what `pack` builds is installable. The command prints the file name, kind, name, version, size, SHA-256 and the signer.

### `hatamoto theme dev <dir>`

Packs the directory as an unsigned development package, installs it, activates it on the first push, then checks the directory every 500 milliseconds and pushes again when a file's path, size or modification time changes. A failed push prints `NOT PUSHED` with the reason and the previous good version keeps serving.

| Flag | Meaning |
| --- | --- |
| `--dev` | confirms this is a development site. Needed unless `ENVIRONMENT=development` is set in your shell |
| `--no-activate` | install the theme but leave the active theme alone |
| `--confirm` | replace anyway: an older or signed theme of the same name, or a change that drops section types your content uses |

The command refuses to run when the shell's `ENVIRONMENT` is `production`, or when the database belongs to a server that recorded `production` at boot. The directory must hold `theme.json`; a plugin directory is refused with `holds a plugin; theme dev takes a theme`.

### `hatamoto package install <file|url>`

Installs a package from a file or an `https` URL. It prints `installed theme <name> v<version>`.

| Flag | Meaning |
| --- | --- |
| `--confirm` | replace anyway: an older version, another signer, or a theme that loses sections |
| `--source` | `upload` (a file, the default), `url` or `dev` (needs `--dev`) |
| `--dev` | allow a development package (never in production) |
| `--grant` | plugins only: permissions to grant, comma separated, or `all` |

### `hatamoto theme activate <name|default>`

Makes an installed theme live, with the same checks as the admin. `--force` activates even when your content uses section types the theme lacks. It prints `theme <name> is now live`. A development theme can be activated this way only when the shell allows development packages (`ENVIRONMENT=development`); otherwise use `theme dev`.

### `hatamoto package list`

Lists installed plugins and themes, one per line: kind, name, version, then the source and state, such as `upload, active, signed 303d64f631f1fdf8` or `dev, active, unsigned`. `--json` prints JSON instead.

### `hatamoto registry index <dir>`

Builds a signed `registry.json` from every `.hmp` in the directory:

```bash
hatamoto registry index ./packages --key mykey.key --url-prefix packages/ --out registry.json
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--key` | `HATAMOTO_SIGNING_KEY` | signing key file (required) |
| `--out` | `registry.json` | output file |
| `--url-prefix` | `packages/` | prefix of each package URL, relative to the index or an `https` URL |
| `--valid-days` | `90` | days the signed index stays valid, 1 to 180 |
| `--meta` | none | JSON file that maps package names to author, source, commit and tier |

## Uninstalling

Uninstalling a theme removes the package and its settings and nothing else. Pages, posts, media, terms, menus and the site settings are not touched. The active theme cannot be uninstalled, so activate another one (or `default`) first.
