# Write and submit a theme

How to build a Hatamoto theme in your own repo and publish it through a
developer account. Part 1 covers writing and testing, Part 2 covers publishing.
The full theme reference (layouts, template funcs, sections, markers) is
[THEMES.md](THEMES.md); packaging and trust are in
[PACKAGES.md](PACKAGES.md). Writing a plugin instead? See
[PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md).

> **Status:** developer accounts are open at <https://hatamoto.dev/developers>.
> Themes published there are listed as Developer packages. Everything in Part 1
> works today with the `hatamoto` binary.

Developer packages are listed at <https://hatamoto.dev/themes> and in Admin >
Settings > Theme > Browse registry, once a site owner has allowed them (see
Part 2). Themes in the first-party registry are listed another way, see
[First-party listing](#first-party-listing).


## Part 1: Write a theme

A theme is data: Go `html/template` files, CSS, JavaScript and JSON schemas,
plus a `theme.json`. There is no SDK, no Go code and no build step. It lives in
your own repo.

### Prerequisites

- The `hatamoto` binary, for `theme check` and `theme pack` (and `theme dev`,
  which also needs a running development server, see below).
- A public GitHub repo for your theme. The packaged `.hmp` file goes on a
  GitHub release of that repo.

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

A theme you publish must be self-contained. Ship your CSS, fonts,
images and scripts inside `assets/`; do not load anything from another host.

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
| `name` | `^[a-z][a-z0-9-]{1,39}$`; `default` is reserved |
| `version` | your theme's semver; bump it for every release |
| `description` | shown on the directory card |
| `screenshot` | **required for the registry**: path to a preview image under `assets/` |
| `settings_schema` | optional, a JSON Schema that becomes a settings form, see [THEMES.md](THEMES.md#installing-and-activating-themes) |
| `menus` | optional, the menu locations the theme renders and their default items, see [THEMES.md](THEMES.md#menu-locations-menus). `theme check` refuses a malformed block |

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
rules above) and the `menus` block when there is one, and, with `--registry`, also
requires a valid screenshot and refuses the name `default`. On success it prints
`ok: theme kumo v0.1.0`.

`theme check` does not render your templates; step 4 does. Run both.

### 6. Pack and sign it

```sh
hatamoto keys generate --name mykey      # mykey.key (secret) + mykey.pub
hatamoto theme pack ./hatamoto-kumo --key mykey.key --out kumo.hmp
```

Keep `mykey.key` out of git and never share it. Hatamoto does not re-sign your
theme: the `.hmp` you build carries your signature, and that is what site owners
install. The command prints `signed by <key id>`. To try the package on your own
development server, trust your key with `HATAMOTO_TRUSTED_KEYS` or allow
unsigned packages, as described in
[THEMES.md](THEMES.md#installing-and-activating-themes) and
[PACKAGES.md](PACKAGES.md).

### 7. Publish a release and submit

Push your theme to GitHub, create a release and attach `kumo.hmp` to it. Then
follow [Part 2](#part-2-publish-your-theme).

## Part 2: Publish your theme

Anyone can publish. Developer accounts are free for now. We may introduce a fee later and will announce it in advance. You need an account to submit, and Hatamoto never signs your code: your own key signs it.

1. Sign in at <https://hatamoto.dev/developers> with GitHub, add a display name
   and accept the developer terms. You need a verified email on your GitHub
   account.
2. Make a key with `hatamoto keys generate --name mykey`. Keep `mykey.key`
   private. Paste the contents of `mykey.pub` under "Add key". An account has one
   key.
3. Pack and sign the theme with `hatamoto theme pack . --key mykey.key` and
   attach the `.hmp` to a GitHub release.
4. On the same page fill in the submit form and press "Check and submit":
   - **Kind:** Theme.
   - **Release file:** the link to the `.hmp`, in the form
     `https://github.com/<owner>/<repo>/releases/download/<tag>/<file>.hmp`.
     Other hosts and release page links are refused.
   - **Source repository** and **Title:** optional.
   - **What it does:** up to 600 characters.
   - **Site type:** blog, business, docs, landing, portfolio or restaurant.
   - **Screenshot link:** optional, an https link to a picture of the theme as
     it looks on sample content. It must show this theme, not a mock-up.

The checks run at once and are shown as a list: signed by your key, the file is a
theme, the name is free, the version is newer than the listed one, and the
package passes the same rules as `theme check`. If one fails, fix it and press
"Resubmit". If all pass, the status is "Waiting for approval". The site owner of
hatamoto.dev approves it or says why not, and you get an email either way. No
review time is promised.

**Names.** Use lower case letters, digits and `-`, starting with a letter
(`^[a-z][a-z0-9-]{1,39}$`). Reserved and first-party names are refused. A name
becomes yours when your first submission is approved; there is no separate claim
step.

### After approval

The theme is listed as a Developer package, "by @handle". A site owner who wants
to use it first allows developer packages in Admin > Plugins > Package security
> Allow developer packages. This takes three steps: switch on "I understand",
type the site address, and enter the owner password. Hatamoto checks who signed a
developer package, but does not review its code. After that the owner installs it
from Browse registry or with `hatamoto package install <name>`.

### Updating a theme

Raise `version` in `theme.json`, pack with the same key, attach the new `.hmp` to
a new release and submit the new link. The new version replaces the old listing
once approved. An update signed by a different key is refused.

### Removing a theme

Contact us and we will remove the listing. We may refuse or remove listings under
the developer terms. A listing also drops out by itself when the release file or
its signer changes. Copies already installed stay on sites until their owners
remove them.

## First-party listing

This section is for first-party themes and for themes already listed in the main
registry as Community. Everyone else publishes through a developer account, see
[Part 2](#part-2-publish-your-theme).

Those themes are listed with a manifest pull request to
<https://github.com/Zen-Labs-Corp/hatamoto-plugins>, in the folder
`manifests/themes/`. The manifest pins a git commit or a zip archive with its
sha256. A maintainer reviews the theme at that pin. After the merge the release
manager fetches the pin, repacks it and signs it with the Hatamoto key. The
registry never accepts binaries or pre-signed packages, and by submitting you
grant the project the right to repack, sign and redistribute the theme and its
builds (`archive_grant`).

### The manifest

Add `manifests/themes/<name>.json`, where `<name>` is the `name` in `theme.json`.
Give exactly one source, `repo` plus `commit` or `archive` plus `sha256`:

```json
{
  "name": "kumo",
  "repo": "https://codeberg.org/tanaka/hatamoto-kumo",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "title": "Kumo",
  "author": "tanaka",
  "license": "MIT",
  "archive_grant": true,
  "homepage": "https://example.com/kumo"
}
```

| Field | Required | Rule |
|---|---|---|
| `name` | yes | `^[a-z][a-z0-9-]{1,39}$`; equals the file name and the `name` in `theme.json`; not reserved, unique across plugins and themes |
| `repo`, `commit` | git source | an `https://` URL on a public host, and a full 40 character lowercase hex commit id; optional `subdir` for a folder inside the repo |
| `archive`, `sha256` | archive source | an `https://` URL of a `.zip` (at most 64 MiB) and the 64 character hex hash of exactly that file |
| `title` | no | card name, 1 to 60 characters |
| `author` | yes | shown as "by author" |
| `license` | yes | an SPDX id from the list in the manifest validator, for example `MIT` or `Apache-2.0` |
| `archive_grant` | yes | must be `true` |
| `homepage` | no | an `https://` link |

No other keys are allowed. The hosts must be public DNS names over `https`: no IP
addresses, `localhost`, internal suffixes, ports or credentials. Check the file
in a clone of hatamoto-plugins before you push:

```sh
scripts/validate-theme-manifest.sh manifests/themes/kumo.json --remote
```

### Review criteria

- **Pinned source.** A commit id that exists, or a zip that matches its sha256.
  Branches and tags are not accepted.
- **Passes the checks.** `hatamoto theme check --registry` passes and the theme
  installs and renders on a current Hatamoto.
- **No remote assets, no trackers.** Everything comes from `assets/`; no analytics,
  beacons or hidden calls home.
- **Readable templates.** No obfuscated or encoded blobs. Minified third-party
  libraries are fine when the plain source and licence ship with them.
- **Truthful screenshot.** It shows this theme as a site owner gets it.
- **License.** A license file that matches the manifest, and redistributable
  fonts and images.
- **Fit.** The theme works and does not duplicate an existing one without reason.

### Updating and removing

To update, open a new pull request that changes the pin, after you raise
`version` in `theme.json`. To remove a theme, open a pull request that deletes
its manifest. Installed copies keep working until each site owner removes them.
