---
title: Keep data and add a route
nav_title: Data and routes
description: Write a plugin that stores a number, answers its own web address with it and reports it on a schedule.
next: add-an-admin-page.md, data-routes-cron.md
---

In this guide you write a plugin called `counter`. It counts how many times something is published, answers `/p/counter/count` with the number as JSON, and writes the number to the server log every hour. You learn the three things most plugins need besides reacting to events: stored data, a web address of their own and a schedule.

## Before you start

- You have finished [React to events](react-to-events.md), so you can create, build and load a plugin with `HATAMOTO_PLUGIN_DIR`.
- You can sign in to your local server as an owner, because you allow the plugin to store data.
- `curl`, or a browser, to call the route.

Three terms are new here. The **key-value store** (`kv`) is the plugin's own storage of named values. A **route** is a web address the plugin answers. A **cron** job is a schedule that runs a job at fixed times.

## Steps

### Create the plugin

1. Make the module, as before:

   ```bash
   mkdir counter
   cd counter
   go mod init example.com/counter
   go get hatamoto.dev/sdk@v0.12.0
   ```

   SDK 0.12.0 ships with Hatamoto `v0.31.0`; until your users run it, declare `^0.11`. The `site.scripts` permission belongs to SDK 0.12.0, but the range you declare does not gate it: hosts from v0.30.0 accept it under any range and enforce it, older hosts reject it as unknown.

2. Create `plugin.json`. It asks for the `kv` **permission**, a right the owner grants, and declares one public route and one cron job:

   ```json
   {
     "name": "counter",
     "version": "0.1.0",
     "sdk": "^0.11",
     "description": "Counts publishes and shows the number.",
     "permissions": ["kv"],
     "routes": [
       { "method": "GET", "path": "/count", "public": true }
     ],
     "cron": [
       { "name": "report", "every": "1h" }
     ]
   }
   ```

   A public route answers anyone at `/p/counter/<path>`. Without `"public": true` it would live at `/api/v1/p/counter/<path>` for signed-in owners only.

3. Create `main.go`:

   ```go
   package main

   import (
   	"encoding/json"
   	"fmt"

   	"hatamoto.dev/sdk"
   	"hatamoto.dev/sdk/guest"
   )

   // count reads the stored number of publishes (0 when nothing is stored yet).
   func count() (int, error) {
   	var got struct {
   		Found bool `json:"found"`
   		Value int  `json:"value"`
   	}
   	err := guest.Call(sdk.FnKVGet, map[string]string{"key": "published"}, &got)
   	return got.Value, err
   }

   func init() {
   	// Store: add one each time something is published.
   	guest.OnAction(sdk.EntityPublished, func(sdk.EntityEvent) error {
   		n, err := count()
   		if err != nil {
   			return err
   		}
   		return guest.Call(sdk.FnKVSet, map[string]any{"key": "published", "value": n + 1}, nil)
   	})

   	// Route: answer GET /p/counter/count with the number as JSON.
   	guest.OnHTTP(func(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
   		n, err := count()
   		if err != nil {
   			return sdk.HTTPResponse{Status: 503, Body: err.Error()}, nil
   		}
   		body, _ := json.Marshal(map[string]int{"published": n})
   		return sdk.HTTPResponse{
   			Headers: map[string]string{"Content-Type": "application/json"},
   			Body:    string(body),
   		}, nil
   	})

   	// Cron: write the number to the server log once an hour.
   	guest.OnCron("report", func(sdk.CronEvent) error {
   		n, err := count()
   		if err != nil {
   			return err
   		}
   		guest.Log(guest.LogInfo, fmt.Sprintf("counter: %d publishes so far", n))
   		return nil
   	})
   }

   //go:wasmexport on_entity_published
   func onEntityPublished() int32 { return guest.Run(sdk.EntityPublished.Export()) }

   //go:wasmexport http_handle
   func httpHandle() int32 { return guest.Run(guest.HTTPExport) }

   //go:wasmexport on_cron_report
   func onCronReport() int32 { return guest.Run(guest.CronExport("report")) }

   func main() {}
   ```

   `guest.Call` calls a function of the **host API**, the set of functions the server offers a plugin. Every route of the plugin goes to the one `http_handle` export, and each cron job has an export named `on_cron_<name>`.

4. Run `go mod tidy`.

### Build, load and grant

1. Build and copy it into your plugin directory, as in the last guide:

   ```bash
   GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
   cp plugin.json build/
   mkdir -p <plugins>/counter
   cp build/* <plugins>/counter/
   ```

2. Restart the server with `HATAMOTO_PLUGIN_DIR` set.

3. In the admin, open **Plugins**, then **counter**, then the **Permissions** tab. Tick `kv` and save. A plugin gets nothing until the owner grants it, and a grant applies at once.

## Check it worked

1. Publish a page in the admin, then call the route. Replace `<site>` with your server's address, such as `http://localhost:8090`:

   ```bash
   curl <site>/p/counter/count
   ```

   ```text
   {"published":1}
   ```

2. To see the cron job without waiting an hour, set `"every": "1m"` (one minute is the shortest), rebuild, copy and restart. A minute after startup the log shows `counter: 1 publishes so far`.

If the route answers `503` with `capability_denied: kv.get needs the "kv" permission, which the owner has not granted`, the grant in step 3 of "Build, load and grant" is missing. If it answers `404`, check the path in `plugin.json` and that the log shows `plugin loaded` for `counter`.

Two publishes at the same moment can both read the same number, so this counter may miss one. For exact counts and tables of your own, use the `db` permission and SQL, described with every limit in [Data, routes and cron](data-routes-cron.md).
