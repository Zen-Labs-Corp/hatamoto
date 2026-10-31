# Packages: `.hmp` files, signing and the registry

Plugins and themes are installed as **packages**: one `.hmp` file (a zip) that is
stored in Postgres (`hatamoto_packages`) and applied while the server runs. No
restart, no directory to copy files into, nothing compiled.

- [Format](#format)
- [Safety rules for the archive](#safety-rules-for-the-archive)
- [Signing](#signing)
- [Trust and the unsigned opt-in](#trust-and-the-unsigned-opt-in)
- [Registry](#registry)
- [Installing, updating, removing](#installing-updating-removing)
- [Uninstall and your data](#uninstall-and-your-data)
- [Configuration](#configuration)
- [Command line](#command-line)

## Format

```
plugin.json | theme.json    exactly one: the manifest (it decides the kind)
plugin.wasm                 plugins only: the module
sections/  schemas/  layouts/  partials/    templates (themes; plugins ship partials/ and sections/ too)
widgets/                    plugins only: page-builder widgets, widgets/<name>/{schema.json,widget.html,widget.css,island.js} (see WIDGET_PACKS.md)
assets/                     public files; plugins: assets/plugin.css and assets/plugin.js load on every page
migrations/                 plugins only: NNN_name.sql (see PLUGIN_API.md "Own data")
ui/                         plugins only: the admin page (see PLUGIN_API.md "Admin page")
signature.json              written by `hatamoto theme|plugin pack --key`
README.md  LICENSE  CHANGELOG.md   allowed at the root, ignored by the host
```

Anything else at the root, any other top-level directory, hidden files
(`.git`, `.env`) and `node_modules` are refused (`pack` skips them). A plugin
package needs `plugin.wasm` (optional when it ships `widgets/`: a widget-only pack has no code) and a valid `plugin.json` (name `^[a-z][a-z0-9-]{1,39}$`,
semver version, `sdk` range, see PLUGIN_API.md). A theme package needs
`theme.json` with `name`, `version` and optionally `description`,
`settings_schema` and `screenshot` (a preview image under `assets/`; an invalid one is ignored on install, and only `--registry` checks and requires it, see [THEMES.md](THEMES.md#writing-and-submitting-a-theme)), and must not hold `plugin.wasm` or `widgets/`. The name `default` is reserved
for the built-in theme. Install and update are keyed by `(kind, name)`:
installing version 1.1.0 of an installed package replaces 1.0.0.
Plugin names `browse`, `security` and `webhooks` are reserved too (the admin serves `/plugins/browse` and `/plugins/security`; webhooks are part of core): a new install under one of them is refused. A plugin already installed as `browse` or `security` can still be updated and uninstalled.

### Widget packs

A plugin package with a `widgets/` directory is a **widget pack**: each `widgets/<name>/` holds `schema.json`,
`widget.html`, `widget.css` and optionally `island.js`, and becomes the builder kind `<plugin>_<name>` in the
**Plugin widgets** palette group. Install validates every widget and refuses the package with
`widgets/<name>/<file>: <rule>` when one breaks a rule: widget directory names (`^[a-z][a-z0-9-]{0,31}$`), at most 16
widgets per package, schema rules (label, `additionalProperties: false`, no array props, at most 24 props, 16 KiB),
template rules (builtins only; no `template`, `define`, `block`, `call`; `{{.Attrs}}` on the root; the parse tree is
checked, so actions print only in text or in plain quoted attribute values; `range` iterates props only; no `script`,
`style`, `iframe`, `object`, `embed`, `link`, `meta`, `base`, `form`, `svg`, `math`; no `on*`, `style=`, `srcdoc`,
`javascript:` or `data:` values; 16 KiB), CSS rules (the minified text is checked; every selector starts with
`.hm-<plugin>_<name>`; only `@media` and `@supports`; no `url(`, `image-set(`, `@import`, `position: fixed|sticky`;
16 KiB) and island rules (32 KiB; no `eval(`, `new Function`, `document.write`, dynamic `import(`, absolute URLs).
A package with an `island.js` must list the `builder.island` permission ("runs JavaScript on your site's pages"); the
island is served only while the owner grants it. The signature covers `widgets/` like every other file. The site registers at most 64
widget kinds. Widgets apply and disappear with install, enable, disable and uninstall, without a restart; saved pages
keep their nodes as placeholders. Details: [WIDGET_PACKS.md](WIDGET_PACKS.md).

## Safety rules for the archive

Parsing is hostile-input safe and runs before anything else (including the
signature check). A package is refused when:

| Rule | Limit |
| --- | --- |
| archive size | 64 MiB |
| entries | 2048 |
| one file, uncompressed | 64 MiB |
| all files, uncompressed | 128 MiB |
| compression ratio of a file over 1 MiB | 200:1 (zip bomb) |
| entry name | at most 200 bytes, valid UTF-8, no backslash, colon or control byte |
| paths | no `..`, no absolute paths, no unclean paths, no hidden segments |
| entry type | regular files only: symlinks, devices, pipes are refused |
| duplicates | none, compared case-insensitively |
| encryption | refused |

Sizes are enforced on the bytes actually read, never on the sizes the archive
declares, so a header that lies cannot get past a limit. Nothing is ever
extracted to disk: files are read from the stored archive in memory. Files served
to browsers (`assets/`, `ui/`) carry `X-Content-Type-Options: nosniff` and a
sandboxing `Content-Security-Policy`, so an SVG or HTML file opened directly runs
without script and without the site's origin.

## Signing

Packages are signed with **ed25519**. The signature covers a *digest* of the
package: SHA-256 over every file name, size and SHA-256 (sorted by name, without
`signature.json`). It therefore survives recompression but not a changed byte.
`signature.json`:

```json
{"signer": "3f9a1c2b7d4e5f60", "algorithm": "ed25519", "signature": "<base64>"}
```

`signer` is the key id: the first 16 hex characters of SHA-256 of the 32-byte
public key.

```sh
hatamoto keys generate --name mykey      # mykey.key (secret, 0600) + mykey.pub
hatamoto theme pack ./mytheme --key mykey.key
hatamoto plugin pack ./myplugin --key mykey.key
```

`pack` output is deterministic (sorted entries, fixed timestamps) and is parsed
again before it is written, so what it builds is installable. Keep the `.key`
file out of version control; CI can pass it with `HATAMOTO_SIGNING_KEY`.

## Trust and the unsigned opt-in

The server trusts the **hatamoto first-party key** (built in: it signs every plugin of the default
registry, so they install with one click; see [first-party plugins](#first-party-plugins)) and the signer keys listed
in `HATAMOTO_TRUSTED_KEYS` (base64 public keys, separated by commas or spaces;
`keys generate` prints the value). Install checks, in order:

1. The package parses (above).
2. If it is signed: the key must be trusted and the signature must verify. An
   unknown signer or a bad signature is **always** refused, whatever settings say.
3. If it is unsigned: it is refused **unless the owner allowed unsigned
   packages** (Admin > Plugins > the menu in the page header > Package security >
   "Advanced: allow unsigned packages"). That setting is off by default and takes
   four gates to switch on: an acknowledgement, the site address typed exactly,
   a duration (1 hour, 24 hours, or until the owner turns it off) and the owner's
   password. While it is on, the Plugins page shows a standing warning with a
   one-click "Turn off now". A timed allowance ends by itself: the end time is
   stored and checked each time the setting is read, so no job has to run for it
   to lapse. An unsigned package can run any code its author wrote with every
   permission you give it: only allow it for code you wrote or read.
4. A `theme dev` push (source `dev`) is an unsigned package allowed without the
   opt-in only when the **server's** `ENVIRONMENT` is not `production`. A
   production server refuses dev packages at install and ignores any dev row in
   its database at load and activate (the admin shows why). The server stores its
   environment in the database at boot; the CLI refuses dev packages when that
   says production or its own `ENVIRONMENT=production`, and otherwise needs an
   explicit `--dev` flag (or `ENVIRONMENT=development`), because the CLI's own
   default environment is development.
5. Replacing an installed package asks the owner to confirm when the new version
   is not newer (a downgrade or the same version again), when another trusted key
   signed it (a plugin's grants are then cleared and must be ticked again), when
   a plugin update lists web addresses (`allowed_hosts`) the installed version did
   not or newly asks for `http.fetch.any` (any public web address; grants are
   cleared here too), or when it is the active theme and would drop section types
   your content uses. The CLI needs `--confirm`.

**The first-party key is trusted by default.** If its private half leaked, every
server would trust packages signed with it until upgraded. Set
`HATAMOTO_TRUST_FIRST_PARTY=false` to drop it and trust only
`HATAMOTO_TRUSTED_KEYS` (the first-party plugins then install only from a registry
you sign yourself). A release that rotates the first-party key ships the new key
in `config.FirstPartyKey` and re-signs the hosted registry; until a server is
upgraded, the way to revoke is that switch.

**Rotation of 2026-10-16.** The previous first-party private key was not
available, so the key was rotated. The new public key is
`HsfRxf3cApV2VddoUEfOM76UrOWbr2yGT2nD/xeesAo=` (key_id `adea8e00f5ac74ca`); the
private half is held by the release manager outside the repository. Servers
older than this release trust only the old key: upgrade them, or add the new
key to `HATAMOTO_TRUSTED_KEYS`. The retired key had key_id `a592a1276b3947b1`.
A package showing Blocked with signer `a592a1276b3947b1` was signed by the
retired first-party key: re-download it from the registry (it is re-signed with
the new key), or add the old public key to `HATAMOTO_TRUSTED_KEYS` temporarily.
To publish the registry, run
`COMMUNITY_DIR=<freshly pulled plugins clone> HATAMOTO_REGISTRY_KEY_FILE=... make registry`
(every plugin, first-party ones included, is rebuilt from its manifest on every run;
`make registry` refuses to run without `COMMUNITY_DIR`), upload `registry.json` and
`packages/` to the host, and serve `/registry.json` and `/packages/*`
statically.

**Rotating or revoking a key.** A package whose signer is no longer in
`HATAMOTO_TRUSTED_KEYS` is not loaded at boot or on reconcile (a plugin stops, an
active theme falls back to the built-in one) and the admin says so. To rotate:
add the new key, re-sign and update the packages, then remove the old key.

Every install, update, uninstall, activation, grant change and every switch of
the unsigned opt-in (with the chosen duration) is written to the admin audit log
(`hatamoto_admin_audit`) with who did it (the signed-in user, `api` or `cli`).

Plugins are additionally sandboxed (WASM, own database role, nothing granted
until the owner ticks it, see PLUGIN_API.md), but a signature says who built a
package, not that it is harmless: read the permission prompt.

## Registry

A registry is a static `registry.json`, default
`https://hatamoto.dev/registry.json` (`HATAMOTO_REGISTRY_URL`; `off` disables
browsing, upload and the CLI still work). It is signed: an envelope holding the
index and an ed25519 signature over its compact JSON.

```json
{
  "index": {
    "version": 1,
    "issued_at": "2026-10-19T00:00:00Z",
    "expires_at": "2026-11-19T00:00:00Z",
    "packages": [
      {"kind": "plugin", "name": "seo-tools", "version": "1.2.0", "description": "...",
       "url": "seo-tools-1.2.0.hmp", "sha256": "<hex of the .hmp>", "size": 123456,
       "permissions": ["entities.read", "kv"], "homepage": "https://...",
       "title": "SEO Tools", "author": "acme", "source": "https://github.com/acme/seo-tools",
       "about": "Adds structured data to every page.\nWorks without configuration.",
       "features": ["JSON-LD for pages", "Sitemap hints"],
       "commit": "<40 hex>", "tier": "community"},
      {"kind": "theme", "name": "kumo", "version": "1.0.0", "description": "...",
       "url": "kumo-1.0.0.hmp", "sha256": "<hex of the .hmp>", "size": 98765,
       "title": "Kumo", "author": "tanaka", "source": "https://github.com/tanaka/kumo",
       "about": "A calm, light theme for blogs.", "features": ["Dark mode"],
       "commit": "<40 hex>", "tier": "community", "screenshot": "screenshots/kumo.webp"}
    ]
  },
  "signer": "3f9a1c2b7d4e5f60",
  "algorithm": "ed25519",
  "signature": "<base64>"
}
```

`screenshot` (themes only) is a path of the form `screenshots/<name>.png`, `.webp` or `.jpg`, relative to the index; a client drops any other value. `title` (1 to 60 characters, no control characters, no leading or trailing space, the card heading in the plugin directory), `author` (shown as "by @author" on plugin cards and "by author" on theme cards, for community packages only; a first-party entry has none), `source` (an `https` link), `commit` and `tier` (`first-party` or `community`) are
optional descriptive fields; older servers ignore them. So are `about` (plain text, 1 to 1500
characters, at most 10 paragraphs separated by a single newline) and `features` (1 to 8 distinct
lines of 1 to 120 characters), the text of a package's detail page. Neither may contain `<`, `>`
or a URL, and no line may start or end with whitespace. `registry index` refuses a `--meta` value
that breaks these rules; a client clears an invalid `about` or `features` and keeps the entry.
`category` and `featured` are optional too and feed the admin Browse page: `category` is one of
`SEO`, `Analytics`, `Marketing`, `Forms`, `Commerce`, `Content`, `Media`, `Developer`, `Security`,
`Integrations` (exact case), and `featured: true` pins the package in the Featured row. `registry
index` refuses any other category; a client clears an unknown one and keeps the entry.
Themes may also set `site_type` (one of `blog`, `business`, `docs`, `landing`, `portfolio`,
`restaurant`, exact case) and `flags` (any of `dark`: has a dark mode; `nojs`: ships no
front-end JavaScript; `builder`: works with the page builder). They feed the theme Browse
filters. `registry index` refuses an unknown value, and either key on a plugin; a client drops an
unknown value (and repeated flags) and keeps the entry, and ignores a value of the wrong JSON type. `issued_at` and `expires_at` (RFC 3339, at most 180 days apart) are signed with
the index. The server refuses an expired index, one issued in the future or one
without both times, so an old copy cannot be replayed to hide newer versions.
`HATAMOTO_REGISTRY_URL` must be `https://` (an explicit test override aside).

`url` may be relative to the index. The server refuses an index that is unsigned,
signed by an untrusted key or altered; it checks the downloaded file against
`sha256`; and the package inside is verified like any other (signature and
name). A registry that serves a different package under a listed name is
refused. Registry fetches go through the server's SSRF guard (no private
addresses) and are cached for 30 seconds.

The admin lists the registry at **Plugins > Browse plugins** (`<admin>/plugins/browse`).
Its query string is `q` (search words), `tier` (`first-party` or `community`, repeatable),
`cat` (a category above, repeatable), `perm` (`nonet`: makes no outgoing requests;
`noscripts`: ships no front-end JavaScript; repeatable), `hide=1` (hide installed plugins),
`sort` (`name`, or `name-desc`) and `page` (12 cards a page). Unknown values are ignored.
A plugin's detail page is `<admin>/plugins/<name>/about`. "Check for updates" in the Plugins
menu re-reads the registry without waiting for the cache.

Themes are listed at **Theme > Browse themes** (`<admin>/theme/browse`), with the detail page at
`<admin>/theme/themes/<name>/about`. It takes `q`, `tier`, `hide`, `sort` and `page` like the plugin page,
plus `type` (a site type above, repeatable; a theme matches any of the chosen types) and `feat`
(a flag above, repeatable; a theme must have every chosen flag). Unknown values are ignored.

To publish: `pack` and sign each package into one directory, host the files, and
build the signed index with `hatamoto registry index <dir> --key F --url-prefix
packages/ --out registry.json` (it lists every `.hmp` in the directory; the
index is valid 90 days, `--valid-days` 1 to 180, so re-run it on a schedule).
`--meta F` adds the descriptive fields (`title`, `about`, `features`, `category`, `featured`, `site_type`, `flags`, `author`, `source`, `commit`,
`tier`, `screenshot`) from a JSON file keyed by package name
(`{"seo-tools": {"title": "SEO Tools", "author": "acme", "source": "https://...", "commit": "...", "tier": "community"}}`).
For a theme, add `"screenshot": "screenshots/<name>.webp"` there; the host serves the image next to the index.
The "Plugin directory" page section (see `docs/site/themes/sections.md`) shows the plugins of
the configured registry inside the site theme; it needs no build step.
`hatamoto registry page registry.json --out plugins/index.html` is for operators without a
CMS page: it renders the plugins of a
signed index as one self-contained HTML directory page; it verifies the index like a
server does and writes nothing if it is unsigned, altered, expired or signed by an
untrusted key. For how outside authors submit plugins, see
[PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md). Anyone can run a registry; users add its signing key to `HATAMOTO_TRUSTED_KEYS`
and point `HATAMOTO_REGISTRY_URL` at it.

## First-party plugins

Two integrations ship as signed plugins instead of core code. Each lives in the
`Zen-Labs-Corp/hatamoto-first-party` monorepo, which is its source of record, and
is listed in the plugins repository by a manifest with `"tier": "first-party"`
(no `author`), like every other plugin:

| Plugin | Repository | What it does |
|---|---|---|
| `searchconsole` | [Zen-Labs-Corp/hatamoto-first-party/plugins/searchconsole](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/searchconsole) | Google Search Console: OAuth, sites, sitemap submit, URL inspection, daily performance import |
| `gtm` | [Zen-Labs-Corp/hatamoto-first-party/plugins/gtm](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/gtm) | Google Tag Manager: loads your container on every public page from a container ID in the plugin settings (`settings` permission only) |

First-party plugins are not mirrored: the plugins repository's
`mirror-plugin.sh` skips them, so they are built from the monorepo (or reused
from the signed build archive) instead of from a mirror.

Their source is the reference for real-world plugins. The `tier` is only the
label in the plugin directory: `registry/build-community.sh` accepts it for a
repository owner listed in `FIRST_PARTY_OWNERS` (the Makefile fixes it to
`zenlabscorp Zen-Labs-Corp` (the first only until the plugin manifests move) with `override`, so an environment variable cannot widen it; to change it, edit that line in a reviewed commit) and refuses it, never downgrading silently, for anyone else; the
names `gtm` and `searchconsole` are reserved for it. Trust is the signature, not
the label.

`make registry COMMUNITY_DIR=<plugins clone>` builds every manifest and the signed
index into `registry/` (`registry/packages/*.hmp`, `registry/registry.json`; see
`registry/README.md`). It reads the signing key from `HATAMOTO_REGISTRY_KEY_FILE`;
the **private first-party key is never in the repository**. The matching public
key is the constant `config.FirstPartyKey`, trusted by every server. The hosted
`registry.json` (hatamoto.dev) is rebuilt and re-signed at release time, before
the previous index expires.

## Installing, updating, removing

**Admin > Settings > Plugins** and **Theme**: browse the registry, install,
update (offered when the registry has a newer version), upload a `.hmp`.

- Installing a **plugin** first shows a permission prompt: every permission its
  manifest asks for, with what it means and whether the package is signed. You
  tick what it may have; nothing is allowed until you tick it. The plugin is live
  as soon as you confirm, with its routes, cron jobs, template functions,
  partials, sections, public assets and admin page.
- An **update** keeps your earlier grants; permissions the new version adds are
  marked and left unticked.
- The plugins list shows each plugin's version. A newer registry version gets
  **Update to vX**: one click, no prompt, grants kept. If the update adds
  permissions the row says **Review update to vX** and lists them (`new: ...`);
  that goes through the prompt. **Update all (N)** updates every plugin that
  needs no review, in name order, names the ones it skipped, and stops at the
  first error. Whatever the button, an update that needs confirmation (another
  signer, new `allowed_hosts`, `http.fetch.any`, `site.scripts`,
  `builder.island`, an older version) is never installed without it. Each update
  is written to the audit log.
- **Turn off** unloads a plugin but keeps its package, grants and data.
- A **theme** asks for no permissions and stays inactive until activated
  (see THEMES.md).

A package installed by the CLI (or by another replica) is picked up by running
servers within about five seconds (one second outside production).

## Uninstall and your data

Uninstalling a plugin marks it *uninstalling* (it unloads at once and cannot be
installed over), revokes its grants, drops the data you chose, and only then
deletes the package. If a step fails (say the database connection drops) the
plugin stays listed as uninstalling and you run the uninstall again. You choose
what happens to its data:

- **Keep its data** (default): its own schema (`plugin_<name>`), database role,
  stored values and settings stay, so installing it again brings everything back.
- **Delete its data too**: drops the plugin's schema (`DROP SCHEMA ... CASCADE`
  as the plugin's own role), its role and credentials (through the same
  provisioning functions that created them), its `kv` rows and its settings.

**Uninstall never changes core data**: pages, posts, media, terms, menus,
redirects, forms, users and the site settings are not touched. This is covered
by a test that checksums every core table before an install and after an
uninstall with a data drop. A theme's uninstall removes the package and its
settings and nothing else; the active theme cannot be uninstalled.

## Configuration

| Variable | Effect |
| --- | --- |
| `HATAMOTO_REGISTRY_URL` | Signed registry index (default `https://hatamoto.dev/registry.json`; `off` disables). |
| `HATAMOTO_TRUSTED_KEYS` | Base64 ed25519 public keys of trusted signers. |
| `HATAMOTO_TRUST_FIRST_PARTY` | `false` stops trusting the built-in first-party key (default: trusted). |
| `HATAMOTO_PLUGIN_FETCH_PER_MINUTE` | Outbound `http.fetch` requests one plugin may make per minute (default 600). |
| `HATAMOTO_PLUGIN_DIR` | Development override: `<dir>/<name>/plugin.json` + `plugin.wasm` load as unsigned in-memory packages, replacing an installed package of the same name; nothing is stored. Unset by default. |
| `HATAMOTO_DATA` | The server's state directory (default `./data`). |
| `HATAMOTO_PLUGIN_CACHE_DIR` | Compiled-code cache across restarts (default `$HATAMOTO_DATA/cache/wasm`, mode 0700; `off` keeps it in memory). |
| `HATAMOTO_SIGNING_KEY` | Default `--key` of the pack commands. |

The unsigned opt-in (an on/off flag and, for a timed allowance, its end time in
`allow_unsigned_until`) and the active theme are stored in the database
(`hatamoto_package_config`), not in the environment.

## Command line

```
hatamoto keys generate [--name N] [--dir D]
hatamoto theme pack <dir> [--key F] [--out F]
hatamoto theme pack (--archive F --sha256 H | --package F) [--registry] [--expect-name N] [--screenshot-out D] [--key F] [--out F]
hatamoto theme check <dir> | --archive F --sha256 H | --package F [--registry] [--expect-name N] [--screenshot-out D]
hatamoto plugin pack <dir> [--key F] [--out F]
hatamoto theme dev <dir> --dev [--no-activate] [--confirm]
hatamoto package install <file|url> [--grant P,P|all] [--source upload|url|dev] [--dev] [--confirm]
hatamoto registry index <dir> --key F [--meta F] [--out F] [--url-prefix P] [--valid-days N]
hatamoto registry page <registry.json> [--out F] [--title T] [--intro T] [--write-url U] [--submit-url U] [--install-hint T] [--trusted-key K]
```

The second `theme pack` and `theme check` are what the registry builder uses: they read the theme from
an author zip (`--archive`, which needs its `--sha256`) or from an already packed `.hmp` (`--package`, re-read, its
signature is not checked) instead of a directory. `--registry` requires a valid screenshot and refuses the
name `default`, `--expect-name` refuses a theme with another name, and `--screenshot-out D` (with `--registry`)
writes the image to `D/<name>.<ext>` and prints `screenshot: <file>`. `theme check` prints `ok: theme <name> v<version>`.

`package install` applies the same policy as the admin (trusted signer, or the
owner's unsigned opt-in) and, for a plugin, grants only what `--grant` names;
without it the command prints the requested permissions and you grant them in
the admin. Packages are part of the full-site archive from v1-20 on; until then
back them up with the database.
