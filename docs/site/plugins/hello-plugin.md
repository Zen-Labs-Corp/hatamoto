---
title: Build the hello plugin
nav_title: Hello plugin
description: Build the smallest complete plugin, load it into your local server and see it log each publish and add data to every page.
next: react-to-events.md, keep-data-and-routes.md, package-and-install.md
---

In this guide you build `hello`, the example plugin in the Hatamoto repository, and load it into a server on your own machine. When you finish, the server logs a line each time you publish a page, and every page carries one extra value the plugin added.

## Before you start

- A checkout of the Hatamoto repository.
- A Go toolchain that satisfies the `go` line in `backend/pkg/sdk/go.mod` (Go 1.26 or newer).
- A Hatamoto server you can start on your machine, with `DATABASE_URL` set and the `hatamoto` program on your `PATH`. See [Getting started](../getting-started/index.md).
- An idea of what a plugin is. [Understand how plugins work](overview.md) explains it in a few minutes.

## Steps

### Look at the files

1. Open `examples/plugins/hello`. The plugin is a `plugin.json`, a `main.go`, a `README.md` and a `go.mod`. `plugin.json` is the **manifest**, the file that describes the plugin: its name, its version, the SDK it was built against, a config value and its limits. The **SDK** is the Go module `hatamoto.dev/sdk`, which defines what a plugin and the server exchange.

   ```json
   {
     "name": "hello",
     "version": "0.2.0",
     "sdk": "^0.8",
     "description": "Reference plugin: logs published entities and enriches JSON-LD.",
     "config": { "greeting": "konnichiwa" },
     "limits": { "memory_pages": 256, "hook_timeouts_ms": { "entity.published": 2000, "jsonld.enrich": 250 }, "max_instances": 4 }
   }
   ```

   JSON-LD is the structured data for search engines that each page carries. `hook_timeouts_ms` is how many milliseconds the server waits for each **hook** (explained in step 3) before it gives up on it. `"sdk": "^0.8"` loads because the server still serves plugins built for SDK 0.8.

2. Open `go.mod`. Its `replace hatamoto.dev/sdk => ../../../backend/pkg/sdk` line only lets the example build inside the checkout. Your own plugin depends on the published module instead:

   ```bash
   go get hatamoto.dev/sdk@v0.12.0
   ```

   SDK 0.12.0 ships with Hatamoto `v0.31.0`; until your users run it, declare `^0.11`. The `site.scripts` permission belongs to SDK 0.12.0, but the range you declare does not gate it: hosts from v0.30.0 accept it under any range and enforce it, older hosts reject it as unknown ([The sdk range](manifest.md#the-sdk-range)).

3. Open `main.go`. It registers two handlers and exports one function per **hook**, a point where a plugin joins in: an action runs when something happens, a filter changes a value before it is used.

   ```go
   package main

   import (
   	"fmt"

   	"hatamoto.dev/sdk"
   	"hatamoto.dev/sdk/guest"
   )

   func init() {
   	guest.OnAction(sdk.EntityPublished, func(ev sdk.EntityEvent) error {
   		guest.Log(guest.LogInfo, fmt.Sprintf("published %s/%s (%s)", ev.SchemaSlug, ev.Slug, ev.ID))
   		return nil
   	})

   	guest.OnFilter(sdk.JSONLDEnrich, func(d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
   		greeting, ok := guest.Config("greeting")
   		if !ok {
   			greeting = "hello"
   		}
   		if d.Doc == nil {
   			d.Doc = map[string]any{}
   		}
   		d.Doc["x-hello"] = greeting
   		return d, nil
   	})
   }

   //go:wasmexport on_entity_published
   func onEntityPublished() int32 { return guest.Run(sdk.EntityPublished.Export()) }

   //go:wasmexport filter_jsonld_enrich
   func filterJSONLDEnrich() int32 { return guest.Run(sdk.JSONLDEnrich.Export()) }

   // main is never called: a c-shared wasm module is a reactor.
   func main() {}
   ```

   The action logs each published page. The filter adds `x-hello` to the page's structured data for search engines, using the manifest's `greeting` value. Each handled hook needs its one-line `//go:wasmexport` function, and `Export()` turns the hook name into the export name.

### Build and load it

1. From the repository root, build the plugin:

   ```bash
   make plugin-hello
   ```

   ```text
   Built examples/plugins/hello/build/
   ```

   The plugin compiles to **WebAssembly**, a standard format for small programs that the server runs in a closed space with no access to its files or network. The standard Go toolchain does this; you do not need TinyGo, the separate Go compiler some WebAssembly projects use. The result is `build/plugin.wasm` next to a copy of `plugin.json`.

2. Copy the build into a plugin directory and check that the server can read it:

   ```bash
   mkdir -p "$PWD/plugins/hello"
   cp examples/plugins/hello/build/* "$PWD/plugins/hello/"
   export HATAMOTO_PLUGIN_DIR="$PWD/plugins"
   hatamoto plugins list
   ```

   ```text
   hello 0.2.0 ^0.8
   ```

   `HATAMOTO_PLUGIN_DIR` is a development setting: it loads each subdirectory as an unsigned plugin, kept in memory only. Never use it in production.

3. In the same terminal, so `HATAMOTO_PLUGIN_DIR` is still set, go to the folder your server normally starts in and start it there:

   ```bash
   cd /path/to/your/site
   hatamoto serve
   ```

   Start it from that folder, not from the repository. The server keeps its **server secret**, a long random value it uses to sign links and protect stored credentials, in `./data/secrets` of the folder it starts in, unless `HATAMOTO_SECRETS_DIR` says otherwise. If you start it anywhere else, it creates a new secret, and preview links and stored import credentials made before stop working. After you rebuild the plugin, copy it again and restart the server.

### Run the tests

1. From `backend`, run the tests that load the compiled plugin into the real server code:

   ```bash
   go test ./internal/pluginhost -run TestHelloLoadsAndHandlesHooks
   ```

   For your own plugin, `guest.Handle(export, input)` runs a handler without the runtime, so ordinary Go unit tests work.

## Check it worked

1. The server log shows `msg="plugin loaded" plugin=hello` at startup.
2. Publish a page in the admin. The log shows `published page/<slug> (<id>)` with `plugin=hello`.
3. Open the page and view its source. The `<script type="application/ld+json">` block contains `"x-hello":"konnichiwa"`. Remove `config` from the manifest, rebuild and restart, and the value becomes `hello`.

If `hatamoto plugins list` prints the plugin name followed by an error in parentheses, the manifest is not valid, and the message names the field to fix. If the list looks right but the log shows `packages: plugin dir` with `a plugin package needs plugin.wasm`, the module is missing: copy the whole `build` directory again.
