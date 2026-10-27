# datasource-demo - reference builder data source

A WASM plugin (own Go module, standard Go toolchain) that answers the
`builder.data` filter. Its `plugin.json` declares three sources and asks for
the `builder.data` permission:

| Source | Kind | Use |
|---|---|---|
| `ext.datasource-demo.temp` | value, text | bind a text prop |
| `ext.datasource-demo.link` | value, url | bind a link prop |
| `ext.datasource-demo.forecast` | items | loop `from: "source"`; `loop.item.title/url/excerpt/category`, `loop.item.attributes.temp` |

The host sends one batched call per page render and gives the plugin only the
requests that name its own sources. The `mode` config value drives failure
behaviour: `ok`, `slow` (hits the 300 ms limit), `trap`, `big` (100 items,
over the cap of 48: the whole result is dropped) and `liar` (answers the id in
`steal`, which is ignored).

## Build and install

```sh
make plugin-datasource-demo
hatamoto plugin pack examples/plugins/datasource-demo/build --out datasource-demo.hmp
hatamoto package install datasource-demo.hmp --grant builder.data
```

The contract is documented in `docs/PLUGIN_API.md` and `docs/WIDGET_PACKS.md`.

## Test

`go test ./internal/pluginhost` in `backend/` builds this plugin and runs it
through the real host.
