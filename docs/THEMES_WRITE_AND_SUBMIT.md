# Write and submit a theme

How to build a Hatamoto theme in your own repo and get it approved for the
registry. Part 1 covers writing and testing, Part 2 covers the submission. The
full theme reference (layouts, template funcs, sections, markers) is
[THEMES.md](THEMES.md); packaging and trust are in
[PACKAGES.md](PACKAGES.md). Writing a plugin instead? See
[PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md).

> **Status:** submissions to the registry open when Hatamoto core goes public
> (roadmap item v1-24), the same moment as plugin submissions. Until then the
> steps below describe the path you will follow. Everything in Part 1 works
> today with the `hatamoto` binary.

Approved themes are listed at <https://hatamoto.dev/themes> and install with
one click from Admin > Settings > Theme > Browse registry.

## Part 1: Write a theme

A theme is data: Go `html/template` files, CSS, JavaScript and JSON schemas,
plus a `theme.json`. There is no SDK, no Go code and no build step. It lives in
your own repo.

### Prerequisites

- The `hatamoto` binary, for `theme check` and `theme pack` (and `theme dev`,
  which also needs a running development server, see below).
- A public git repo for your theme, or a place to host a zip over https.

### 1. Lay out the files

A theme may be partial: every file it lacks comes from the built-in default
theme. This is the smallest tree the engine accepts on its own, with the
partials the engine requires:

```
hatamoto-kumo/
  theme.json
  layouts/
    base.html
    page.html
    404.html
    error.html
  partials/
    head.html header.html nav.html footer.html section.html scripts.html
  assets/
    screenshot.webp     the registry preview, see below
    css/base.css
```

The rules for each file, the layout kinds you can add (`home.html`,
`single.html`, `archive.html`, ...), section templates in `sections/` and their
schemas in `schemas/` are in [THEMES.md](THEMES.md#directory-layout). Read that
chapter before you start; this guide does not repeat it. The quickest start is to
copy the files of the default theme that ships with Hatamoto and change them.

A theme submitted to the registry must be self-contained. Ship your CSS, fonts,
images and scripts inside `assets/`; do not load anything from another host
(see [Review criteria](#review-criteria)).

Hidden files and folders (a leading dot) and links are refused in a package, so
keep `.git` out of the folder you pack, and keep to the top-level folders the
package format allows ([PACKAGES.md](PACKAGES.md)).

### 2. Write theme.json

```json
{
  "name": "kumo",
  "version": "0.1.0",
  "description": "One sentence a site owner will read in the theme directory.",
  "screenshot": "assets/screenshot.webp"
}
```

| Field | Meaning |
|---|---|
| `name` | `^[a-z][a-z0-9-]{1,39}$`; also the name of your manifest file in Part 2. `default` is reserved |
| `version` | your theme's semver; bump it for every release |
| `description` | shown on the directory card |
| `screenshot` | **required for the registry**: path to a preview image under `assets/` |
| `settings_schema` | optional, a JSON Schema that becomes a settings form, see [THEMES.md](THEMES.md#installing-and-activating-themes) |

### 3. Make the screenshot

The directory card shows your screenshot, so it has to be a real picture of your
theme. The screenshot is required and strictly checked only by
`hatamoto theme check --registry`, `hatamoto theme pack --registry` and the
registry build; installing a package ignores an invalid screenshot. The rules:

- A file under `assets/`, named in `theme.json` as `screenshot`.
- PNG, WebP or JPEG (`.png`, `.webp`, `.jpg`, `.jpeg`), and the contents must
  match the extension. **Never SVG.**
- At most 2 MiB.
- Between 640 and 2560 pixels wide and between 360 and 1600 pixels high, and at
  most 4,200,000 pixels in total.

Take it from the real theme running on sample content, not a mock-up. A
screenshot that does not show what a site owner will get is a reason to refuse
the submission.

### 4. Try it on a development server

`hatamoto theme dev` pushes your folder to a running development server as an
unsigned dev package, activates it, and pushes again every time a file changes:

```sh
hatamoto theme dev ./hatamoto-kumo --dev
```

It needs the same `DATABASE_URL` as the server (so a local Hatamoto and its
PostgreSQL must be set up) and refuses anything that looks like production:
`--dev` (or `ENVIRONMENT=development`) is required and `ENVIRONMENT=production`
refuses. Add `--no-activate` to install without switching the live theme. If a
push fails (a layout that does not parse, a partial called with the wrong
argument) the command prints `NOT PUSHED` with the reason and keeps watching.
Dev packages are for development only, never for deployment.

### 5. Check it for the registry

```sh
hatamoto theme check ./hatamoto-kumo --registry
```

This needs no key and no database, and writes nothing. It parses the folder as a
theme package (name, semver version, the package safety rules in
[PACKAGES.md](PACKAGES.md#safety-rules-for-the-archive) and the screenshot
rules above) and, with `--registry`, also requires a valid screenshot and
refuses the name `default`. On success it prints `ok: theme kumo v0.1.0`. This is
the same check the registry runs, so if it passes here it passes there.

`theme check` does not render your templates; step 4 does. Run both.

If you will submit a zip instead of a git repo, check the zip too:

```sh
hatamoto theme check --archive theme.zip --sha256 "$(sha256sum theme.zip | cut -d' ' -f1)" --registry
```

Add `--expect-name kumo` to either form to fail when `theme.json` names a
different theme.

### 6. Pack and try it as a package

```sh
hatamoto keys generate --name mykey      # mykey.key (secret) + mykey.pub
hatamoto theme pack ./hatamoto-kumo --key mykey.key --out kumo.hmp
```

Keep `mykey.key` out of git. This key is only for your own trials: the registry
repacks your theme and signs it with the Hatamoto key, so your signature is never
shipped. To install the package on a development server, trust your key with
`HATAMOTO_TRUSTED_KEYS` or allow unsigned packages, as described in
[THEMES.md](THEMES.md#installing-and-activating-themes) and
[PACKAGES.md](PACKAGES.md).

### 7. Publish your repo and submit

Push your theme to a public git host, copy the 40 character commit id you want
reviewed (or build the zip and its sha256), and follow
[Part 2](#part-2-submit-a-theme).

## Part 2: Submit a theme

Submissions go to the public plugins repository <https://github.com/Zen-Labs-Corp/hatamoto-plugins>, the
same repository as plugins, in the folder `manifests/themes/`.

### How it works

1. You keep your theme in your own public repo, or as a zip on a public https
   URL.
2. You open a pull request that adds one small manifest pinning a git commit or
   a zip archive and its sha256.
3. CI checks the manifest. A maintainer reviews your theme at that pin.
4. A merge is the approval. After the merge the release manager fetches the pin,
   repacks it, signs the result with the Hatamoto key and publishes it. Every
   signed build is kept.

We never accept binaries or pre-signed packages. The registry builds from the
files you pinned. There is nothing to compile and no author code runs during the
build. By submitting you grant the project the right to repack, sign and
redistribute your theme and its builds (the `archive_grant` field below).

### The manifest

Add `manifests/themes/<name>.json`, where `<name>` is the `name` in your
`theme.json`. Use one of two shapes.

A git source:

```json
{
  "name": "kumo",
  "repo": "https://codeberg.org/tanaka/hatamoto-kumo",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "subdir": "",
  "title": "Kumo",
  "author": "tanaka",
  "license": "MIT",
  "archive_grant": true,
  "homepage": "https://example.com/kumo"
}
```

An archive source:

```json
{
  "name": "kumo",
  "archive": "https://example.com/downloads/kumo-0.1.0.zip",
  "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "author": "tanaka",
  "license": "MIT",
  "archive_grant": true
}
```

| Field | Required | Rule |
|---|---|---|
| `name` | yes | `^[a-z][a-z0-9-]{1,39}$`; equals the file name and the `name` in `theme.json`; not reserved and unique across plugins and themes (see below) |
| `repo` | git source | an `https://` URL on any public host (GitHub, Codeberg, GitLab, your own server) |
| `commit` | git source | full 40 character lowercase hex commit id |
| `subdir` | git source, optional | theme folder inside the repo; only letters, digits, `.`, `_`, `-` and `/`; no `..`, no leading `/`; not allowed with an archive |
| `archive` | archive source | an `https://` URL of a `.zip` |
| `sha256` | archive source | 64 lowercase hex characters, the hash of exactly the file at `archive` |
| `title` | no | display name for the directory card, 1 to 60 characters, no control characters, no leading or trailing space; without it the card shows `name` |
| `author` | yes | your name or handle, shown as "by author"; letters, digits, space and `. _ @ -`, starting with a letter or digit, at most 61 characters |
| `license` | yes | one of: `MIT`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `MPL-2.0`, `GPL-2.0`, `GPL-3.0`, `LGPL-2.1`, `LGPL-3.0`, `AGPL-3.0` (the GPL, LGPL and AGPL ids also with `-only` or `-or-later`) |
| `archive_grant` | yes | must be `true`: you grant us the right to repack, sign and redistribute the theme at that pin and its builds (see above) |
| `homepage` | no | an `https://` link |

Give exactly one source: `repo` plus `commit`, or `archive` plus `sha256`, never
both. No other keys are allowed; in particular there is no `tier` for themes:
yours is listed as Community.

**Hosts.** The `repo` and `archive` hosts must be public DNS names reached over
`https`. IP addresses, `localhost`, single-label names, internal suffixes (`.local`,
`.internal`, `.lan`, `.home`, `.corp`, `.test`, `.invalid`), ports and credentials
in the URL are refused.

**Names.** `default`, `webhooks`, `hello`, `gtm` and `searchconsole` are
reserved. A name is unique across plugins and themes together: if
`manifests/<name>.json` (a plugin) exists, you cannot use `<name>` for a theme,
and the other way round.

**Archives.** For an archive source:

- It must be a zip file, at most 64 MiB, and its sha256 must match the pin. Get
  the hash with `sha256sum theme.zip` (on macOS `shasum -a 256 theme.zip`).
- `theme.json` sits at the top of the zip, or inside exactly one wrapper folder
  (what `zip -r` of a folder gives you); that one folder is stripped.
- It follows the same safety limits as any package: entry count, sizes,
  compression ratio, safe file names, no links or encrypted entries, see
  [PACKAGES.md](PACKAGES.md#safety-rules-for-the-archive).
- Host it somewhere that keeps serving the same bytes. Put the version in the
  file name and never overwrite a published zip; a changed file no longer matches
  its sha256.

Check your manifest before you push:

```sh
scripts/validate-theme-manifest.sh manifests/themes/kumo.json --remote   # run in a clone of hatamoto-plugins
```

`--remote` also fetches your source, confirms that `theme.json` there has the
same name, that it names a screenshot under `assets/`, and that the screenshot
exists and is at most 2 MiB. When the `HATAMOTO_BIN` environment variable points
at a `hatamoto` binary it also runs `hatamoto theme check ... --registry
--expect-name <name>` on the fetched theme. Run that yourself in any case, as in
Part 1.

### The pull request

Fork the repo, add your manifest, and open a PR. Fill in the template. One theme
per PR.

### Review criteria

A maintainer reads the theme at the pinned commit or in the pinned zip. We look
for:

- **Pinned source.** The commit exists in the public repo, or the zip downloads
  and matches its sha256. Branches and tags are not accepted, only commit ids.
- **Passes the checks.** `hatamoto theme check --registry` passes, and the theme
  installs and renders on a current Hatamoto.
- **No remote assets, no trackers.** CSS, fonts, images and scripts come from
  `assets/`. No `<link>`, `<script>`, `@import` or `url()` pointing at another
  host, no analytics, no beacons, no hidden calls home.
- **Readable templates.** No obfuscated, minified-only or encoded blobs in
  templates or scripts, and no hidden behaviour. Minified third-party libraries
  are fine when the plain source and licence ship with them.
- **Truthful screenshot.** It shows this theme, and what a site owner gets when
  they install it.
- **License.** The repo or zip has a license file, and it matches the manifest.
  Fonts and images you ship must be licensed for redistribution too.
- **Fit.** The theme works, and does not duplicate an existing one without a
  good reason.

Reviewers may ask for changes. Push fixes to your repo and update `commit` (or
`archive` and `sha256`) in the PR.

### After the merge

The release manager fetches the pin, repacks the theme, signs it with the Hatamoto key and publishes a
new registry index. Your own signature, if the package had one, is ignored: the
published package carries the first-party signature only. Every signed build is
kept in the signed-build archive, so an index can always be rebuilt from it. The
theme then shows up in Admin > Settings > Theme > Browse registry on every
Hatamoto server and as a card with your screenshot at
<https://hatamoto.dev/themes>, labelled "Community" and "by author". Publishing is
done by a person with the signing key, so it can lag the merge by a little.

**There is no source mirror.** The registry keeps the signed builds, not a copy
of your repository or zip. If your repo or URL disappears, versions already
published stay installable, but a new build of that pin fails until you restore
the source or submit a new pin.

### Updating a theme

Open a new PR that changes `commit` (or `archive` and `sha256`) in your existing
manifest. Bump `version` in `theme.json` first, so servers can tell the new build
from the old one. The review looks at what changed since the previous pin.

### Removing a theme

Open a PR that deletes your manifest. After it merges the release manager
publishes an index without the theme. Copies already installed keep working until
each site owner removes them, and the signed builds already made stay in the
archive. If a theme is found to be harmful, maintainers may remove its manifest
without notice and publish immediately.
