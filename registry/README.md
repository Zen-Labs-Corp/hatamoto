# Plugin registry

The default registry (`https://hatamoto.dev/registry.json`) lists the plugins
Hatamoto ships (`searchconsole` and `gtm`, first-party, each in its own folder of the
monorepo `Zen-Labs-Corp/hatamoto-first-party`) and the approved community plugins, all built from manifests in the
plugins repository and signed with the **hatamoto first-party** key.

```sh
git -C ~/src/hatamoto-plugins pull
COMMUNITY_DIR=~/src/hatamoto-plugins HATAMOTO_REGISTRY_KEY_FILE=/path/to/hatamoto-first-party.key make registry
# registry/packages/<name>.hmp  (one per manifest)
# registry/registry.json      (signed index, valid 90 days)
```

`make registry` builds each manifest with `registry/build-community.sh`, packs it with
`hatamoto plugin pack --key`, and signs the index with `hatamoto registry index`.
The outputs are git-ignored: they are build products signed with a key that is
**not in this repository**.

- The public half of the first-party key is `config.FirstPartyKey`
  (`backend/internal/config/packages.go`); every server trusts it, so the
  plugins install with one click.
- The private half lives with the release manager (a file, mode 0600). Never
  commit it. Create a replacement with `hatamoto keys generate --name
  hatamoto-first-party`, ship its public key in `config.FirstPartyKey` in a
  release and re-sign the registry; servers that predate the release keep
  trusting the old key until they update.
- CI runs `registry/test-build-community.sh`, the offline fixture test of the build script,
  with a throwaway key (`hatamoto keys generate`); no server trusts the result.
- The index expires after 90 days (`--valid-days`, 1 to 180): re-run the
  target (with `COMMUNITY_DIR` set to a freshly pulled plugins clone; it is
  required) and publish `registry.json` and `packages/` together before then.

To test a local registry with your own key, serve the outputs over HTTP and
start Hatamoto with `HATAMOTO_REGISTRY_URL=http://127.0.0.1:PORT/registry.json
HATAMOTO_REGISTRY_ALLOW_HTTP=true HATAMOTO_TRUSTED_KEYS=<your public key>`.

## Community plugins and the plugin directory

Outside authors submit plugins as manifests to the public plugins repository
([hatamoto-plugins](https://github.com/Zen-Labs-Corp/hatamoto-plugins), see
[docs/PLUGINS_WRITE_AND_SUBMIT.md](../docs/PLUGINS_WRITE_AND_SUBMIT.md)); a merge
is the approval. Point `COMMUNITY_DIR` at a checkout of it:

```sh
make registry COMMUNITY_DIR=~/src/hatamoto-plugins HATAMOTO_REGISTRY_KEY_FILE=/path/to/hatamoto-first-party.key
```

`registry/build-community.sh` validates every manifest, clones each pinned commit,
builds it with the `plugin-%` recipe, packs and signs it into `registry/packages/`
and merges `{title, about, features, author, source, commit, tier}` (`title`, `about` and `features` only when the manifest has them; no `author` for a first-party manifest) into `registry/meta.json`. Nothing is
published unless every manifest builds. It runs the authors' build, so do it in a
throwaway environment; the key file is only passed to `plugin pack`. Every run
rebuilds the whole index, so `make registry` refuses to run without `COMMUNITY_DIR`.
`registry/test-build-community.sh` is the offline test of the script.

The build also reads the listing rules in `<COMMUNITY_DIR>/policy/` (a missing or empty
list stops the build) and checks every manifest and its siblings, so a policy or theme
change in the plugins repository can stop it. The fix belongs there, not in `COMMUNITY_SKIP`.

A manifest may carry `"tier": "first-party"` (and then no `author`). The script accepts it
only when the repository owner is in `FIRST_PARTY_OWNERS` (space separated, case-insensitive;
the Makefile fixes it to `zenlabscorp Zen-Labs-Corp` (the first only until the plugin manifests move) with `override`, so only an edit of the Makefile changes it; the script's own default is nobody) and dies otherwise.
The names `gtm` and `searchconsole` are reserved for such manifests, `webhooks` and `hello`
for everyone. The tier is only the label on the page: trust is the signature.

The optional manifest keys `about` and `features` (plugins and themes) feed the detail
page of a package. Both are plain text, never HTML or Markdown. `about` is 1 to 1500
characters in at most 10 paragraphs separated by a single newline (no empty paragraph,
no leading or trailing space, no control character other than that newline); `features`
is a list of 1 to 8 distinct items of 1 to 120 characters each (no control character,
no leading or trailing space). Neither may contain `<`, `>`, a URL (`://`, `www.`),
emoji, symbols or invisible characters. `build-community.sh` and
`build-community-themes.sh` refuse a manifest that breaks a rule, and so do
`registry index` and `--meta`; a client clears an invalid value without dropping the
package. The same rules belong in the listing repository's manifest validator.

`meta.json` (a first-party entry is `{title, about, features, source, commit, tier: first-party}`, no author) is passed to `registry index --meta`, so the signed index carries
the descriptive fields. A site shows the directory with the core "Plugin directory"
section on a normal CMS page (see `docs/site/themes/sections.md`), which reads the
published index. An operator without a CMS page can run `make registry REGISTRY_PAGE=1`
to also render a static `registry/plugins/index.html` with `registry page`, verifying
the index first with the same trust a server has (the first-party key plus
`HATAMOTO_TRUSTED_KEYS`). For a throwaway key (CI) set
`REGISTRY_PAGE_TRUST_SIGNER=1` to also trust the `<key>.pub` that `keys generate`
wrote next to it. `make registry` needs `jq`; the community build also needs `git`
and Go. Plugins are built with our pinned toolchain (the `toolchain` line of
`backend/go.mod`, passed by the Makefile as `HATAMOTO_BUILD_GOTOOLCHAIN`, so the
system Go may be older and Go downloads the pinned one). The pin is exact, never
`+auto`: a plugin whose `go.mod` needs a newer Go fails the build.

### Source mirror and archive

Every approved plugin is kept in places we control, so an author deleting their
repo or force-pushing away the pinned commit cannot break a publish.

- **Source mirror.** When a manifest is merged, CI in the plugins repository copies the
  pinned commit into `github.com/Zen-Labs-Corp/<name>`: the author's
  commit plus one commit that adds `vendor/` (when the SDK is public) and
  `MIRROR.json`, under the annotated tags `src/<commit>` and `v<version>`.
  `make registry` (`MIRROR_BASE` defaults to `https://github.com/Zen-Labs-Corp`;
  an empty `MIRROR_BASE` prints a warning)
  fetches `refs/tags/src/<commit>` from there first and only then from the
  author's repo. A mirror tag that exists must sit directly on the
  pinned commit and touch nothing but `<subdir>/vendor/` and `MIRROR.json`;
  otherwise the build stops (possible tampering). A missing tag or repo falls back
  to the author's repo. A mirror is built
  exactly like upstream (`-mod=readonly`, `go.sum` and sumdb verified) and its
  `vendor/` is ignored, because Go never checks `vendor/` against `go.sum`. Only
  when the author's repo and the module proxy are both gone, set
  `TRUST_MIRROR_VENDOR=1` (env `HATAMOTO_TRUST_MIRROR_VENDOR=1`): the build then
  uses the mirror's `vendor/` with `-mod=vendor` and `GOPROXY=off`, trusting
  whoever can push to the mirror, and prints a loud warning. It is refused when
  the pinned commit itself contains `vendor/`. In the
  signed index `source` is the author's repo while it answers, else the mirror.
- **Signed build archive.** `ARCHIVE_DIR` (default `registry/archive`) keeps every
  signed build forever as `<name>/<name>-<version>-<commit12>-<keyid>.hmp` plus a
  sidecar `.json` with `{name, version, commit, key_id, sha256}`. A manifest whose
  full commit matches a sidecar written for the current signing key reuses that `.hmp` byte for byte: no fetch, no build.
  After a key rotation the old files stay and the new key builds into new files.
  Reuse trusts the sidecar sha256 (a mismatch aborts the run) and also requires the
  `.hmp`'s own `plugin.json` name and version to match the sidecar and the file
  name. Residual risk: the sidecar and `.hmp` come from the host, so someone who
  controls the host's `archive/` can still plant an older, genuinely signed build
  with a consistent sidecar and file name (a rollback); keep the host archive
  writable only by the publish account and compare `registry/archive` with the
  last published index before publishing. There is no
  hatamoto verify command, so the signature is checked at install time and the
  file is parsed by `registry index`. New builds are written to the archive only
  after every manifest succeeded. The archive is append-only: an existing file
  with different content is refused here and by `scripts/publish-registry.sh`.
  `make registry` never wipes it.
- **Grant and license.** `build-community.sh` refuses a manifest without
  `"archive_grant": true` or with a license outside the allowlist, even if PR CI
  was bypassed.

### When a pinned commit disappears

With the mirror and archive this no longer blocks a publish: the archived
`.hmp` is reused, or the build falls back to the mirror. Only a plugin approved
before the mirror existed, or merged while mirroring was down, can still fail with
`cannot fetch <commit>`. Fix it by running the mirror workflow for that manifest
(`workflow_dispatch` in the plugins repository) if the commit is still reachable.

As a last resort, pass `COMMUNITY_SKIP="name1 name2"` to `make registry` (the
skipped names are logged loudly and are left out of the index), then publish as
usual, and open a removal PR in the plugins repository so the next build
matches. Servers that installed the plugin keep it; it just leaves the Browse
list.

### One-time mirror setup

Done once by a maintainer, not by CI:

1. Create the GitHub org `Zen-Labs-Corp`. **Required before the first
   merge:** register it before anything else points at the name.
2. Create a fine-grained personal access token with resource owner
   `Zen-Labs-Corp`, Repository access: All repositories, and the
   repository permissions Administration (read and write, to create repos) and
   Contents (read and write). Set an expiry and renew it. If the org requires
   approval for fine-grained tokens, approve it in the org settings.
3. In the plugins repository create the environment `plugin-mirror`, with
   deployment branches limited to `main` only (optionally also required
   reviewers), and store the token as the **environment** secret `MIRROR_TOKEN`.
   It must not be a repository secret: a repository secret is readable by every
   workflow on every branch and bypasses the environment protection.
4. Nothing to do by hand: `mirror-plugin.sh` creates two repository rulesets on
   every mirror repo right after creating it (repository rulesets work on a free
   org; org rulesets do not). A tag ruleset on `refs/tags/src/*` and
   `refs/tags/v*` blocks update and deletion, and a branch ruleset on `main`
   blocks deletion and non-fast-forward updates. Neither has bypass actors. An
   Admin token can still remove a ruleset or delete a repo, so custody of
   `MIRROR_TOKEN` (environment secret, `main` only, expiry) is the real control.
5. Optionally set the variable `MIRROR_ORG` (default `Zen-Labs-Corp`)
   and, once the SDK is public, `SDK_PUBLIC=true` so new mirrors are vendored.
   Vendoring is a last resort only (see `TRUST_MIRROR_VENDOR` above): the
   build ignores `vendor/` by default. Mirrors created before `SDK_PUBLIC` flips
   stay unvendored; their source is still kept, and nothing needs to be redone.

## Community themes

Themes are submitted as manifests under `manifests/themes/` in the same plugins
repository (see [docs/THEMES_WRITE_AND_SUBMIT.md](../docs/THEMES_WRITE_AND_SUBMIT.md)),
and `make registry` builds them right after the plugins with
`registry/build-community-themes.sh`, from the same `COMMUNITY_DIR`. A theme is
data: nothing is compiled, no Go toolchain is needed and no author code runs.
The source is either a pinned git commit or a zip archive pinned by sha256
(`archive` and `sha256` in the manifest). For each manifest the script validates it
(`"archive_grant": true`, an allowlisted license, public https hosts), fetches the
source, runs `hatamoto theme pack ... --registry` (theme.json rules, the required
screenshot, package limits) and signs the result into `registry/packages/<name>.hmp`.
It copies the screenshot to `registry/screenshots/<name>.<png|webp|jpg>` and adds
`{title, about, features, author, source, commit (git only), tier: community, screenshot:
"screenshots/<name>.<ext>"}` to `registry/meta.json`, so the index entry carries the
`screenshot` path. The name may not already exist as a package, a meta entry or a
plugin manifest. As with plugins, nothing is published unless every manifest builds.
There is no source mirror: the signed build archive is what keeps a published
version installable, and a deleted upstream blocks only a new build of a pin that is
not archived yet.

- **Archive.** Theme builds go into the same `ARCHIVE_DIR` as
  `<name>/<name>-<version>-<id12>-<keyid>.hmp` plus a `.json` sidecar
  `{name, kind: "theme", version, id12, key_id, sha256}` and `commit` (git source) or
  `archive_sha256` (archive source); `id12` is the first 12 characters of the commit or of
  the archive sha256. A manifest whose pin matches a sidecar for the current key reuses
  that `.hmp` byte for byte, after `theme check --package` confirms the name and version.
  The archive host can be compromised, so the version is also checked against the pin: a
  git pin is shallow-fetched and `theme.json` name and version at `subdir` compared; an
  archive pin is re-downloaded, its sha256 verified and its `theme.json` compared. A
  mismatch (an older signed build renamed under a new pin) aborts the run as "rolled
  back". If the source cannot be reached the build is refused, unless
  `HATAMOTO_ARCHIVE_OFFLINE=1` (as for plugins), which reuses it unchecked with a loud
  warning. Residual risk: only name and version are compared, so a planted build with the
  pinned version but other content still passes; keep the host archive writable only by
  the publish account. The archive is append-only, as for plugins.
- **Skipping.** `make registry THEMES_SKIP="name1 name2"` (env `HATAMOTO_THEMES_SKIP`)
  leaves the named themes out of the index, logged loudly; each name must have a
  manifest. It is the theme counterpart of `COMMUNITY_SKIP`, for a last-resort publish.
- **Test.** `registry/test-build-community-themes.sh` is the offline test, run in CI with
  a throwaway key.
- **Screenshots on the host.** Caddy serves `/screenshots/<name>.(png|webp|jpg)` from the
  registry directory as static files (cached 5 minutes, `nosniff`, a restrictive CSP;
  `deploy/caddy/hatamoto.dev.caddy`). The Theme directory section links its images
  from the registry host, so a self-hosted registry must serve `screenshots/` next to
  `registry.json`.

## Key rotation, 2026-10-16

The previous first-party private key was not available, so the key was
rotated. The new public key is `HsfRxf3cApV2VddoUEfOM76UrOWbr2yGT2nD/xeesAo=`
(key_id `adea8e00f5ac74ca`) and is `config.FirstPartyKey`. The private half is
held by the release manager outside this repository. A server older than this
release trusts only the old key, so it must upgrade or add the new key to
`HATAMOTO_TRUSTED_KEYS` before it can install from the registry.

The retired key had key_id `a592a1276b3947b1`. A package showing Blocked with signer `a592a1276b3947b1` was signed by the
retired first-party key: re-download it from the registry (it is re-signed with
the new key), or add the old public key to `HATAMOTO_TRUSTED_KEYS` temporarily.

## Publishing

1. `git -C ~/src/hatamoto-plugins pull`
2. `make registry-pull-archive` (fetches the archive from the host; needs `DEPLOY_HOST` or `.deploy-host`, resolved the same way as in `scripts/publish-registry.sh`; the first time, when the host has no archive yet, it starts empty)
3. `COMMUNITY_DIR=~/src/hatamoto-plugins MIRROR_BASE=https://github.com/Zen-Labs-Corp HATAMOTO_REGISTRY_KEY_FILE=/path/to/hatamoto-first-party.key make registry`
4. `scripts/publish-registry.sh` (add `--dry-run` first). It uploads `registry/archive/`
   append-only, then `registry/packages/`, `registry/screenshots/` (when it exists),
   `registry/plugins/` (when it was built) and `registry/registry.json` last.
5. Caddy serves `/registry.json`, `/packages/*`, `/screenshots/*` statically and
   `/archive/*` as immutable files from `hatamoto.dev` (`deploy/caddy/hatamoto.dev.caddy`).
