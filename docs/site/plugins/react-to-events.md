---
title: React to events
nav_title: React to events
description: Write a plugin that runs code each time a page is published and adds a tag to the head of every page on your site.
next: keep-data-and-routes.md, hooks-and-events.md
---

In this guide you write a small plugin called `notice` from scratch. It writes a line to the server log each time something is published, and it adds an author tag to the head of every public page. You end with a working plugin you can extend with any other event.

## Before you start

- You have built and loaded the example plugin in [Build the hello plugin](hello-plugin.md), so Go 1.26 and a local server are ready.
- You know where your server's log appears (the terminal that runs `hatamoto serve`).

A **hook** is a point where a plugin joins in. There are two kinds: an **action** runs when something happens and returns nothing, and a **filter** changes a value before the server uses it. This plugin uses one of each.

## Steps

### Create the plugin

1. Make a directory for the plugin outside the Hatamoto checkout, and start a Go module in it:

   ```bash
   mkdir notice
   cd notice
   go mod init example.com/notice
   go get hatamoto.dev/sdk@v0.12.0
   ```

   SDK 0.12.0 ships with Hatamoto `v0.31.0`; until your users run it, declare `^0.11`. The `site.scripts` permission belongs to SDK 0.12.0, but the range you declare does not gate it: hosts from v0.30.0 accept it under any range and enforce it, older hosts reject it as unknown.

2. Create `plugin.json`, the **manifest** that describes the plugin. `config` holds fixed values the plugin reads at run time:

   ```json
   {
     "name": "notice",
     "version": "0.1.0",
     "sdk": "^0.11",
     "description": "Logs every publish and adds an author tag to each page.",
     "config": { "author": "Example Studio" }
   }
   ```

   The plugin asks for no **permissions**, the rights an owner grants a plugin, because writing to the log with `guest.Log` and reading `config` need none.

3. Create `main.go`:

   ```go
   package main

   import (
   	"fmt"

   	"hatamoto.dev/sdk"
   	"hatamoto.dev/sdk/guest"
   )

   func init() {
   	// Action: runs after a page or post is published.
   	guest.OnAction(sdk.EntityPublished, func(ev sdk.EntityEvent) error {
   		guest.Log(guest.LogInfo, fmt.Sprintf("notice: published %s %q", ev.SchemaSlug, ev.Name))
   		return nil
   	})

   	// Filter: adds one meta tag to the head of every public page.
   	guest.OnFilter(sdk.RenderHead, func(c sdk.HeadContext) (sdk.HeadContext, error) {
   		author, ok := guest.Config("author")
   		if !ok || author == "" {
   			return c, nil
   		}
   		c.Tags = append(c.Tags, sdk.HeadTag{
   			Kind:  "meta",
   			Attrs: map[string]string{"name": "author", "content": author},
   		})
   		return c, nil
   	})
   }

   //go:wasmexport on_entity_published
   func onEntityPublished() int32 { return guest.Run(sdk.EntityPublished.Export()) }

   //go:wasmexport filter_render_head
   func filterRenderHead() int32 { return guest.Run(sdk.RenderHead.Export()) }

   func main() {}
   ```

   Each hook has a handler, registered in `init`, and an exported function the server calls by name. The name is the hook name with `.` replaced by `_`, after `on_` for an action or `filter_` for a filter: `entity.published` becomes `on_entity_published`. The filter returns the value it received, with one tag added. The server builds and escapes the HTML for the tag, so a plugin cannot inject markup.

4. Fetch the remaining dependencies:

   ```bash
   go mod tidy
   ```

### Build and load it

1. Build the plugin and put the manifest next to it:

   ```bash
   GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
   cp plugin.json build/
   ```

2. Copy the build into the plugin directory your server reads, next to `hello`:

   ```bash
   mkdir -p <plugins>/notice
   cp build/* <plugins>/notice/
   ```

   Replace `<plugins>` with the directory you set in `HATAMOTO_PLUGIN_DIR` in the hello guide.

3. Restart the server with `HATAMOTO_PLUGIN_DIR` set, as before.

## Check it worked

1. At startup the log shows `msg="plugin loaded" plugin=notice` with `hooks=[entity.published render.head]`. The list names every hook the plugin exports.
2. Publish any page in the admin. The log shows a line like this one:

   ```text
   level=INFO msg="notice: published page \"About us\"" plugin=notice
   ```

3. Open any public page and view its source. With the default theme, the head contains `<meta name="author" content="Example Studio">`.

If the tag is missing but the log line appears, the page came from the page cache. Publishing a page clears the cache, so reload the page after step 2.

If the filter returns an error or takes longer than its time limit (250 ms by default), the page still loads, without the tag, and the server logs the failure. This is true for every filter.

To react to other events, such as a form being sent or an import finishing, or to refuse a save with `entity.before_save`, pick the hook from [Hooks and events](hooks-and-events.md) and add a handler and an export the same way.
