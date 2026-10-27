# hello - reference Hatamoto plugin

A WASM plugin in its own Go module, built with the standard Go toolchain
(no TinyGo). It imports `hatamoto.dev/sdk` and its `guest`
package, and shows:

- **action** `entity.published`: logs the entity through the host logger;
- **filter** `jsonld.enrich`: adds `x-hello` (manifest `config.greeting`) to the JSON-LD;
- **manifest**: `plugin.json` with config, limits and a settings schema.

## Build and run

```sh
make plugin-hello              # examples/plugins/hello/build/{plugin.wasm,plugin.json}
mkdir -p data/plugins/hello && cp examples/plugins/hello/build/* data/plugins/hello/
hatamoto plugins list          # hello 0.2.0 ^0.8
hatamoto serve                 # loads ./data/plugins/*, or HATAMOTO_PLUGIN_DIR
```

Plugins load at boot; restart to pick up a change. The `replace` in `go.mod`
is only for building inside this checkout.

## Test

`go test ./internal/pluginhost` in `backend/` builds this plugin and runs it
through the real host.

Writing a plugin in another language: see `docs/PLUGIN_API.md`.
