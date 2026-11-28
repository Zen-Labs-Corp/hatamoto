# Hatamoto plugin template

A minimal [Hatamoto](https://github.com/Zen-Labs-Corp/hatamoto) plugin: one action, one filter, a
manifest, a Makefile and a CI workflow. It builds with the standard Go toolchain (Go 1.27 or newer),
no TinyGo.

## Use it

1. Copy this directory into a new repository.
2. Change the module path in `go.mod`, and `name`, `version` and `description` in `plugin.json`.
3. `make build` writes `build/plugin.wasm` and `build/plugin.json`.
4. Try it locally: start a server with `HATAMOTO_PLUGIN_DIR=./plugins` and copy `build/*` to
   `./plugins/<name>/`. This is a development override: the plugin is unsigned and not stored.
5. Package it: `hatamoto keys generate --name mykey`, then `hatamoto plugin pack ./build --key mykey.key`.
   Keep the `.key` file out of version control.

`hatamoto.dev/sdk` is fetched from the Go module proxy (the public repository `Zen-Labs-Corp/hatamoto-sdk`). `go.mod` and
`go.sum` pin the SDK `v0.29.0`; a server with a newer SDK still loads it (`sdk.CompatibleWith`). To use a newer SDK,
`go get hatamoto.dev/sdk@vX.Y.Z`, `go mod tidy` and update `"sdk"` in `plugin.json`. The SDK is `v0.x`: minor versions can
still break, so pin the version you built against and declare the matching range in `plugin.json` (`"sdk": "^0.29"`).

SDK 0.29 ships with Hatamoto v1.4.6 (first tag with `Version = "0.29.0"`), and an older host refuses a plugin that declares `^0.29`. If your users run older servers, build against an older SDK and declare its range instead (see [STABILITY.md](../../STABILITY.md)).

## Learn more

- Plugin guide: https://hatamoto.dev/plugins
- Hook payloads, manifest and host functions: `docs/PLUGIN_API.md` in the Hatamoto repository
- Stability promises: [STABILITY.md](https://github.com/Zen-Labs-Corp/hatamoto-sdk/blob/main/STABILITY.md)
