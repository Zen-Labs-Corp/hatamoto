---
title: Packaging and signing
nav_title: Packaging and signing
description: The plugin package file, its size and safety limits, signing keys, trusted signers, registries, and what install, update and uninstall do.
---

Plugins and themes are installed as **packages**: one `.hmp` file, a zip that holds the whole plugin. **Signing** proves who made a package and that nobody changed it. The server stores the package in Postgres and applies it while running. There is no restart, no directory to copy files into and nothing to compile on the server. This page covers the format, signing, trust, the registry and the install lifecycle for plugins. To pack, sign and install your first package, follow [Package and install a plugin](package-and-install.md).

## The package layout

```
plugin.json     manifest
plugin.wasm     the module
migrations/     optional: NNN_name.sql, goose format
partials/       optional: <name>.html template partials
sections/       optional: <type>.html section templates
assets/         optional: public files; assets/plugin.css and assets/plugin.js load on every page
ui/             optional: the plugin's admin page
widgets/        optional: page builder widgets (a widget pack)
signature.json  written by `hatamoto plugin pack --key`
```

`README.md`, `LICENSE` and `CHANGELOG.md` are allowed at the root and ignored by the host. Anything else at the root, any other top-level directory, hidden files and `node_modules` are refused (`pack` skips them). A plugin package needs a valid `plugin.json` and `plugin.wasm`, or at least one file under `widgets/`: a package with widgets and no code is a widget pack (see [Builder contract](../themes/builder-contract.md)). A theme package may hold neither `plugin.wasm` nor `widgets/`. Install and update are keyed by kind and name, so installing 1.1.0 of an installed package replaces 1.0.0.

## Safety rules for the archive

Parsing assumes hostile input and runs before anything else, including the signature check.

| Rule | Limit |
|---|---|
| archive size | 64 MiB |
| entries | 2048 |
| one file, uncompressed | 64 MiB |
| all files, uncompressed | 128 MiB |
| compression ratio of a file over 1 MiB | 200 to 1 (zip bomb) |
| entry name | at most 200 bytes, valid UTF-8, no backslash, colon or control byte |
| paths | no `..`, no absolute or unclean paths, no hidden segments |
| entry type | regular files only |
| duplicates | none, compared case-insensitively |
| encryption | refused |

Sizes are enforced on the bytes actually read, never on the sizes the archive declares. Nothing is extracted to disk. Files served to browsers (`assets/`, `ui/`) carry `nosniff` and a sandboxing Content-Security-Policy.

## Signing

Packages are signed with **ed25519**. The signature covers a digest of the package: SHA-256 over every file name, size and SHA-256, sorted by name and without `signature.json`. It survives recompression but not a changed byte. `signature.json` looks like this:

```json
{"signer": "3f9a1c2b7d4e5f60", "algorithm": "ed25519", "signature": "<base64>"}
```

The `signer` is the key id: the first 16 hex characters of the SHA-256 of the 32-byte public key. Generate a key once, then pack:

```sh
hatamoto keys generate --name mykey        # mykey.key (secret, mode 0600) + mykey.pub
hatamoto plugin pack ./myplugin --key mykey.key
# writes myplugin-1.0.0.hmp  (<name>-<version>.hmp, or choose with --out)
```

`keys generate` refuses to overwrite an existing key and prints the value to set on servers that should trust it. `pack` output is deterministic (sorted entries, fixed timestamps) and is parsed again before it is written, so what it builds is installable. The directory you pack must contain `plugin.json` and `plugin.wasm`; for the example plugins that is the `build` directory made by `make plugin-<name>`. Keep the `.key` file out of version control. CI can pass it with `HATAMOTO_SIGNING_KEY`, which is the default for `--key`.

## Trust

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

The server trusts the Hatamoto first-party key (built in, so the first-party plugins install with one click) and the keys listed in `HATAMOTO_TRUSTED_KEYS`: base64 public keys separated by commas or spaces. Install checks, in order:

1. The package parses.
2. If it is signed, the key must be trusted and the signature must verify. An unknown signer or a bad signature is always refused.
3. If it is unsigned, it is refused unless the owner switched on **Allow unsigned packages** on the Plugins page (`<admin>/plugins`, the Unsigned card). That switch is off by default, needs an acknowledgement tick and then shows a standing warning. An unsigned package runs any code its author wrote with every permission you give it.
4. Replacing an installed package asks the owner to confirm when the new version is not newer, when another trusted key signed it (a plugin's grants are cleared), when the update lists new `allowed_hosts` or newly asks for `http.fetch.any` (grants are cleared), or when a theme would drop section types your content uses. The command line needs `--confirm`.

To stop trusting the built-in key, set `HATAMOTO_TRUST_FIRST_PARTY=false`. Then only `HATAMOTO_TRUSTED_KEYS` is trusted and the first-party plugins install only from a registry you sign yourself. A package whose signer is no longer trusted is not loaded at boot, and the admin says so. To rotate a key, add the new one, re-sign and update the packages, then remove the old key.

Every install, update, uninstall, grant change and every switch of the unsigned opt-in is written to the admin audit log with who did it. A signature says who built a package, not that it is harmless, so read the permission prompt.

## Registry

A registry is a static, signed `registry.json`. The default is the Hatamoto registry, set by `HATAMOTO_REGISTRY_URL`; `off` disables browsing in the admin, while upload and the command line still work. The URL must be `https://` unless `HATAMOTO_REGISTRY_ALLOW_HTTP=true` is set for development.

```json
{
  "index": {
    "version": 1,
    "issued_at": "2026-10-19T00:00:00Z",
    "expires_at": "2026-11-19T00:00:00Z",
    "packages": [
      {"kind": "plugin", "name": "seo-tools", "version": "1.2.0", "description": "...",
       "url": "seo-tools-1.2.0.hmp", "sha256": "<hex of the .hmp>", "size": 123456,
       "permissions": ["entities.read", "kv"], "homepage": "https://example.com"}
    ]
  },
  "signer": "3f9a1c2b7d4e5f60",
  "algorithm": "ed25519",
  "signature": "<base64>"
}
```

The index is signed, and `issued_at` and `expires_at` (at most 180 days apart) are signed with it. The server refuses an expired index, one issued in the future, one without both times and one signed by an untrusted key. It checks each download against `sha256` and then verifies the package like any other. Registry fetches go through the server's address guard and are cached for 30 seconds.

To publish your own, pack and sign each package into one directory, host the files, and build the index:

```sh
hatamoto registry index ./packages --key mykey.key --url-prefix packages/ --out registry.json
```

It lists every `.hmp` in the directory and prints `<out> signed by <key id>, valid <n> days`. The index is valid 90 days by default (`--valid-days` 1 to 180), so re-run it on a schedule. `--meta <file>` adds an author, source, commit and tier per package from a JSON file keyed by package name. Anyone can run a registry: users add its signing key to `HATAMOTO_TRUSTED_KEYS` and point `HATAMOTO_REGISTRY_URL` at it. See [SDK and registry](../migrate-contribute/sdk-and-registry.md).

## Building the example plugins

The example plugins in `examples/plugins` build with `make plugin-<name>` from the repository root. The rule runs `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .` inside the plugin directory and copies `plugin.json` next to it, plus `migrations/`, `ui/` and `assets/` if they exist. The result is `examples/plugins/<name>/build/`, the directory you pass to `hatamoto plugin pack`. `make plugins` builds `hello`, `searchconsole`, `gtm` and `datasource-demo`. Your own plugin builds with the same `go build` line, with no TinyGo needed.

## Development override

For local work, `HATAMOTO_PLUGIN_DIR=./plugins` loads every `./plugins/<name>/` directory that holds a `plugin.json` (plus `plugin.wasm`) as an unsigned, in-memory package. Nothing is stored and a restart reloads it. It replaces an installed package of the same name, and the server logs a warning that it is a development override. It is a development convenience, not a deployment method: production installs packages.

`hatamoto plugins list` prints `name version sdk-range` for each plugin in that directory, or a parenthesised error for one it cannot read. Without the variable it prints `no HATAMOTO_PLUGIN_DIR set: plugins come from installed packages (Admin > Plugins, or hatamoto package install)`.

Plugins load only from installed packages or from this directory. The README of the hello example mentions `./data/plugins/*`, but the server does not read that path.

## Installing, updating and removing

On the **Plugins** page (`<admin>/plugins`) you browse the registry, install, update (offered when the registry has a newer version) or upload a `.hmp`. From the command line:

```sh
hatamoto package install myplugin-1.0.0.hmp --grant kv,db
hatamoto package list
hatamoto plugin settings set myplugin greeting=hello
```

`package install <file|url>` applies the same trust policy as the admin and grants only what `--grant` names (`--grant all` grants everything it requests). It prints `installed plugin <name> v<version>`. Without `--grant` it also prints `it asks for: <permissions>` and you grant them in the admin. A file is recorded with the source `upload` and an `https://` address with `url`. `--source dev` together with `--dev` installs a development package, which a production server refuses. `--confirm` answers the replace questions under [Trust](#trust). A running server, including another replica, picks up a CLI install within about five seconds in production (about one second in development). `plugin settings set <plugin> key=value` validates against the plugin's settings schema and keeps other settings; `key=@FILE` or `key=-` reads the value from a file or standard input, so a secret stays off the command line.

- **Install** shows a permission prompt with every permission the manifest asks for, ticked, and a warning on `http.fetch.any`, `builder.island` and `site.scripts`. Untick anything you do not want; only what stays ticked is granted when you click **Install plugin**. The plugin is live as soon as you confirm, with its routes, cron jobs, template functions, partials, sections, assets and admin page.
- **Update** keeps earlier grants. Permissions the new version adds are marked "(new in this version)" and left unticked. When an update clears the grants (see [Trust](#trust)), every box starts unticked.
- **Turn off** (on the plugin's page) unloads a plugin but keeps its package, grants and data. **Turn on** loads it again.
- **Uninstall** is a button on the plugin's **Permissions** tab, which opens `<admin>/plugins/<name>/uninstall`.

## Uninstall and your data

Uninstalling marks the plugin as *uninstalling* (it unloads at once), revokes its grants, drops the data you chose and then deletes the package. If a step fails, the plugin stays listed as uninstalling and you run the uninstall again. You choose what happens to its data:

- **Keep its data** (the default): its schema `plugin_<name>`, database role, stored values and settings stay, so installing it again brings everything back.
- **Delete its data too**: drops the schema (as the plugin's own role), the role and credentials, the `kv` rows and the settings.

Uninstalling never changes pages, posts, media, terms, menus, redirects, forms, users or site settings. A test checksums every core table before an install and after an uninstall with a data drop.

## Environment variables

| Variable | Effect |
|---|---|
| `HATAMOTO_TRUSTED_KEYS` | Base64 ed25519 public keys of trusted signers. |
| `HATAMOTO_TRUST_FIRST_PARTY` | `false` stops trusting the built-in first-party key. |
| `HATAMOTO_REGISTRY_URL` | Registry index URL; `off` disables browsing. |
| `HATAMOTO_REGISTRY_ALLOW_HTTP` | `true` allows an `http://` registry URL, for development. |
| `HATAMOTO_PLUGIN_FETCH_PER_MINUTE` | Outbound `http.fetch` requests per plugin per minute (default 600). |
| `HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS` | `1` grants `db.*` even when objects are open to `PUBLIC`. |
| `HATAMOTO_PLUGIN_DIR` | Development override: a directory of unsigned, in-memory plugins. |
| `HATAMOTO_PLUGIN_CACHE_DIR` | Compiled-code cache (default `$HATAMOTO_DATA/cache/wasm`, mode 0700; `off` keeps it in memory). |
| `HATAMOTO_SIGNING_KEY` | Default `--key` for the pack and registry commands. |

The compile cache holds native machine code the server executes, so make it writable only by the Hatamoto user. It can be deleted at any time and is rebuilt on the next load. For the full list of settings see the [configuration reference](../operate/configuration-reference.md).
