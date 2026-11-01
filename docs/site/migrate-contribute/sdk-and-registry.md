---
title: SDK and registry
nav_title: SDK and registry
description: How the plugin SDK is versioned, how plugin packages are signed and packed, and how to build and host your own signed plugin registry.
---

Two pieces of infrastructure let other people extend Hatamoto without touching its repository: the **plugin SDK**, which fixes what a plugin can do, and the **registry**, which lets a server find, verify and install signed packages. This page lists the maintainer and contributor view of both. To write a plugin, start with [Plugins](../plugins/overview.md); to change the SDK itself, follow [Make your first contribution](contributing.md) and the rules below.

## The SDK module

The SDK is the Go module `hatamoto.dev/sdk` in `backend/pkg/sdk`, published as the public repository [Zen-Labs-Corp/hatamoto-sdk](https://github.com/Zen-Labs-Corp/hatamoto-sdk). It has its own `go.mod`, its root package depends on nothing but the standard library (the `guest` subpackage uses the Extism Go PDK), and it never imports the server's internal packages, so hosts and plugins share exactly the same types. It contains:

- Hook names and payload types (`events.go`, `hooks.go`).
- The `plugin.json` manifest type (`manifest.go`), permissions (`permissions.go`) and host function types (`hostapi.go`).
- Semantic version range matching (`semver.go`).
- The subpackage `hatamoto.dev/sdk/guest`, a thin Go layer over the Extism Go PDK that provides `OnAction` and `OnFilter` registration and the JSON plumbing. It only builds the plugin side for `GOOS=wasip1`.

Plugins are sandboxed WASM modules. Go plugins are built with the standard toolchain, with no TinyGo, as a `c-shared` reactor module:

```bash
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
```

`make plugin-hello` does this for the reference plugin, stages `plugin.json` and any `migrations`, `ui` and `assets` directories into `examples/plugins/hello/build/`, and leaves it ready to pack. Any language with an Extism plugin development kit can produce a plugin, because the contract is JSON payloads and named exports.

The import path is a vanity path: the project's web host answers a `go-get` request with the location of the code repository, so the path never changes when the repository moves. The SDK repository is a root module with plain `vX.Y.Z` tags, for example `v0.12.0`. Core stays the source of truth: `scripts/publish-sdk.sh` exports `backend/pkg/sdk` to the SDK repository and tags it with `Version`, and SDK changes are sent to core.

## SDK versioning rules

The SDK follows semantic versioning, with the usual convention for 0.x that the minor acts as the major.

- **Minor releases are additive only.** New hooks, new optional payload fields and new manifest fields are fine.
- **Removing or renaming** a hook, an export name or a field is a breaking change and bumps the major version, or the minor while the SDK is below 1.0.
- `Version` in `backend/pkg/sdk/version.go` is the current version. `CompatibleWith` lists earlier versions whose plugins this SDK still serves. At the time of writing the SDK is `0.15.0` and also serves plugins built for 0.5 through 0.14.

A plugin declares the range it was built against in its manifest, for example `"sdk": "^0.8"`. Caret ranges below 1.0 stop at the next minor, so a purely additive new minor must list its predecessors in `CompatibleWith`. The host refuses a plugin whose range does not contain a version it serves.

When you change the SDK:

1. Update `Version` and extend `CompatibleWith` if the change is additive.
2. Add a row to the version table in `backend/pkg/sdk/README.md` and update the contract text in `docs/PLUGIN_API.md`.
3. Run the SDK tests and the WASM vet:

```bash
cd backend/pkg/sdk
go vet ./... && go test ./...
GOOS=wasip1 GOARCH=wasm go vet ./guest
```

CI runs these in a separate `sdk` job, because the SDK is a separate module and `go test ./...` in `backend/` does not reach it. The example plugins are modules of their own too, and use a `replace` directive to build against the checkout.

## Packages and signing

Themes and plugins are installed as `.hmp` packages, stored in Postgres, never as directories in the install. Every package is signed with an ed25519 key, and a server trusts a package only if its signer is in the trust set. Keys come from the CLI:

```bash
hatamoto keys generate --name my-plugins --dir ./keys
# ./keys/my-plugins.key   secret, keep it private (the repository ignores *.key)
# ./keys/my-plugins.pub   public half, share it with whoever installs your packages
# it also prints the value to set as HATAMOTO_TRUSTED_KEYS on the server

hatamoto plugin pack examples/plugins/hello/build --key ./keys/my-plugins.key --out hello.hmp
hatamoto theme pack ./my-theme --key ./keys/my-plugins.key
# on the server, set HATAMOTO_TRUSTED_KEYS to that value first, or the install is refused
hatamoto package install hello.hmp
```

`--key` falls back to the environment variable `HATAMOTO_SIGNING_KEY`. Without a key the package is unsigned. An unsigned plugin must be allowed explicitly in the admin before it installs.

The trust set is controlled by these environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `HATAMOTO_TRUSTED_KEYS` | empty | Base64 ed25519 public keys of extra signers, separated by commas or spaces. |
| `HATAMOTO_TRUST_FIRST_PARTY` | trusted | Set to `false` to stop trusting the built-in first-party key, so only `HATAMOTO_TRUSTED_KEYS` count. |
| `HATAMOTO_REGISTRY_URL` | `https://hatamoto.dev/registry.json` | The signed registry index. `off` disables browsing, while upload and the CLI still work. |
| `HATAMOTO_REGISTRY_ALLOW_HTTP` | `false` | Accept an `http://` registry. For development and tests only. |

See [Packaging and signing](../plugins/packaging-signing.md) for the plugin author's view and [Configuration reference](../operate/configuration-reference.md) for every variable.

## The registry format

A registry is a static `registry.json`: an envelope holding an index and an ed25519 signature over its compact JSON.

```json
{
  "index": {
    "version": 1,
    "issued_at": "2026-10-19T00:00:00Z",
    "expires_at": "2026-11-19T00:00:00Z",
    "packages": [
      {"kind": "plugin", "name": "seo-tools", "version": "1.2.0",
       "description": "Example listing", "url": "seo-tools-1.2.0.hmp",
       "sha256": "<hex of the .hmp>", "size": 123456,
       "permissions": ["entities.read", "kv"], "homepage": "https://example.com"}
    ]
  },
  "signer": "3f9a1c2b7d4e5f60",
  "algorithm": "ed25519",
  "signature": "<base64>"
}
```

The server refuses an index that is unsigned, signed by an untrusted key, altered, expired, issued in the future or missing either time. This stops an old copy being replayed to hide newer versions. It checks each downloaded file against `sha256`, verifies the package inside like any other, and refuses a registry that serves a different package under a listed name. `url` may be relative to the index. Registry fetches go through the server's SSRF guard and are cached for 30 seconds.

## Building and hosting a registry

Anyone can run a registry. Pack and sign each package into one directory, then build the signed index:

```bash
hatamoto registry index ./packages --key ./keys/my-plugins.key \
  --url-prefix packages/ --out registry.json --valid-days 90
```

The index lists every `.hmp` file in the directory.

| Flag | Default | Meaning |
|---|---|---|
| `--key` | `HATAMOTO_SIGNING_KEY` | The signing key file. |
| `--out` | `registry.json` | The index file to write. |
| `--url-prefix` | `packages/` | Prefix of each package URL, relative to the index or an `https` URL. |
| `--valid-days` | `90` | Days the signed index stays valid, 1 to 180. Run the command on a schedule before the previous index expires. |
| `--meta` | none | A JSON file of package name to author, source, commit and tier, carried into the signed index. |

Serve `registry.json` and the `packages/` directory as static files over HTTPS. Then, on each server that should use it:

```bash
HATAMOTO_REGISTRY_URL=https://plugins.example.com/registry.json
HATAMOTO_TRUSTED_KEYS=<contents of my-plugins.pub>
```

To test locally, serve the outputs over plain HTTP and add `HATAMOTO_REGISTRY_ALLOW_HTTP=true`.

## The first-party registry

The default registry lists the plugins the project ships, `searchconsole` and `gtm` (first-party, each in its own folder of the monorepo `Zen-Labs-Corp/hatamoto-first-party`), plus the approved community plugins. All of them are built from manifests in the plugins repository. They are signed with the first-party key, whose **public** half is a constant in the server (`FirstPartyKey` in `backend/internal/config/packages.go`), so every server trusts it and the plugins install with one click. The **private** half is held by the release manager outside the repository and must never be committed.

```bash
git -C ~/src/hatamoto-plugins pull
COMMUNITY_DIR=~/src/hatamoto-plugins HATAMOTO_REGISTRY_KEY_FILE=/path/to/first-party.key make registry
# registry/packages/<name>.hmp  (one per manifest)
# registry/registry.json        (signed index, valid 90 days)
# registry/plugins/index.html   (optional static directory page, REGISTRY_PAGE=1)
```

`make registry` builds every manifest with `registry/build-community.sh`, packs it with `hatamoto plugin pack --key`, signs the index with `hatamoto registry index --meta`, and, with `REGISTRY_PAGE=1`, renders a static directory page with `hatamoto registry page`. Sites normally show the registry with the "Plugin directory" section on a CMS page. The outputs are git-ignored build products.

| Variable | Meaning |
|---|---|
| `HATAMOTO_REGISTRY_KEY_FILE` | The signing key file. Required: the target stops with an error when it is unset. |
| `COMMUNITY_DIR` | A freshly pulled checkout of the public plugins repository. Every plugin is built from its manifest there and every run rebuilds the whole index, so the target refuses to run without it. |
| `FIRST_PARTY_OWNERS` | GitHub owners whose manifests may carry `"tier": "first-party"` (fixed to `zenlabscorp Zen-Labs-Corp` with `override` in the Makefile, the first only until the plugin manifests move; an environment variable or command-line value does not change it, edit the Makefile to do so). It only labels the plugin in the directory; trust is the signature. |
| `COMMUNITY_SKIP="a b"` | Leave the named community plugins out, for example when an author deleted the pinned commit and the build fails with `cannot fetch <commit>`. |
| `REGISTRY_PAGE_TRUST_SIGNER=1` | Also trust the `<key>.pub` next to a throwaway key when the page step verifies the index. CI uses it. |

The target needs `jq`; the plugin build also needs `git` and Go. Plugins are built from their pinned commits with the toolchain pinned in `backend/go.mod`, and nothing is published unless every manifest builds. The build runs the plugin authors' code, so run it in a throwaway environment. The full procedure is in `registry/README.md`.

CI runs the build script's offline fixture test (`registry/test-build-community.sh`) with a throwaway key from `hatamoto keys generate`; no server trusts that key.

### Publishing and rotating

1. Run `make registry` with the real key file and a freshly pulled `COMMUNITY_DIR`.
2. Upload `registry/registry.json` and `registry/packages/` together to the host that serves the default registry address, as static files.
3. Repeat before the index expires, which is 90 days after it was built.

If the private key is lost or exposed, generate a replacement, ship its public half as the new `FirstPartyKey` in a server release and re-sign the registry. Servers older than that release keep trusting the old key until they update, so they must upgrade or add the new public key to `HATAMOTO_TRUSTED_KEYS` before they can install from the registry. A package showing as Blocked with an unknown signer was signed by a retired key: download it again from the registry, which is re-signed.
