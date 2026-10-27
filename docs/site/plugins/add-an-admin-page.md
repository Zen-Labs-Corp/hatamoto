---
title: Add an admin page
nav_title: Add an admin page
description: Give your plugin a settings form and a page of its own in the admin, where owners and editors see what it is doing.
next: package-and-install.md, admin-pages-and-templates.md
---

In this guide you extend the `counter` plugin with two things in the admin: a settings form where the owner sets a goal, and a small page that shows the count next to that goal. Both appear on the plugin's **Settings** tab, and editors can open the page too.

## Before you start

- You have the `counter` plugin from [Keep data and add a route](keep-data-and-routes.md), loaded and granted `kv`, its key-value store.
- You can sign in as an owner, and ideally also as an editor to check what they see.

The page runs in the **sandbox**, a closed space where it can do only what its permissions allow. It cannot reach the network, the admin's cookies or other plugins. It talks to your plugin through a small script the server provides, called the bridge.

## Steps

### Declare the page and the settings

1. Replace `plugin.json` with version `0.2.0`. It adds the `settings` permission, an admin-only **route** (a web address the plugin answers), the page (`ui`) and a `settings_schema`, a JSON Schema that the admin turns into a form:

   ```json
   {
     "name": "counter",
     "version": "0.2.0",
     "sdk": "^0.11",
     "description": "Counts publishes and shows the number.",
     "permissions": ["kv", "settings"],
     "routes": [
       { "method": "GET", "path": "/count", "public": true },
       { "method": "GET", "path": "/stats", "roles": ["owner", "editor"] }
     ],
     "cron": [
       { "name": "report", "every": "1h" }
     ],
     "ui": { "title": "Counter", "roles": ["owner", "editor"] },
     "settings_schema": {
       "type": "object",
       "properties": {
         "goal": {
           "type": "integer",
           "title": "Goal",
           "description": "How many publishes you aim for.",
           "minimum": 1,
           "default": 10
         }
       }
     }
   }
   ```

   `/stats` has no `public` flag, so only signed-in owners and editors can call it. `ui` without `entry` shows `ui/index.html`.

2. In `main.go`, replace the `body, _ := json.Marshal(...)` line of the route handler with this block, so `/stats` also returns the goal:

   ```go
   out := map[string]int{"published": n}
   if r.Pattern == "/stats" {
   	var s struct {
   		Goal int `json:"goal"`
   	}
   	if err := guest.Call(sdk.FnSettingsGet, struct{}{}, &s); err != nil {
   		return sdk.HTTPResponse{Status: 503, Body: err.Error()}, nil
   	}
   	out["goal"] = s.Goal
   }
   body, _ := json.Marshal(out)
   ```

   `r.Pattern` is the path from `plugin.json` that matched. `settings.get` returns the saved settings, with the schema's `default` filled in for anything not saved yet.

### Write the page

1. Create `ui/index.html`. The page may not contain inline script or style, so both live in their own files:

   ```html
   <!doctype html>
   <html lang="en">
   <head>
     <meta charset="utf-8">
     <title>Counter</title>
     <link rel="stylesheet" href="app.css">
   </head>
   <body>
     <h1>Counter</h1>
     <p id="total">Loading...</p>
     <script src="/plugin-ui/_sdk/bridge.js"></script>
     <script src="app.js"></script>
   </body>
   </html>
   ```

2. Create `ui/app.js`. `hatamoto.route` calls one of the plugin's own routes, as the person who opened the page:

   ```js
   (async () => {
     await hatamoto.ready;
     const res = await hatamoto.route("GET", "/stats");
     const out = document.getElementById("total");
     if (res.status !== 200) {
       out.textContent = "Could not load the count (" + res.status + ").";
       return;
     }
     const s = JSON.parse(res.body);
     out.textContent = s.published + " published. Goal: " + s.goal + ".";
   })();
   ```

3. Create `ui/app.css`:

   ```css
   body { font-family: system-ui, sans-serif; margin: 1rem; }
   ```

### Build, load and grant

1. Build, and copy the `ui` directory into the build as well:

   ```bash
   GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o build/plugin.wasm .
   cp plugin.json build/
   cp -r ui build/
   rm -rf <plugins>/counter
   cp -r build <plugins>/counter
   ```

2. Restart the server. Open **Plugins**, then **counter**, then **Permissions**, tick `settings` as well as `kv`, and save.

## Check it worked

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. As the owner, open **Plugins > counter > Settings** (`<admin>/plugins/counter/settings`). You see a **Goal** field and, below it, the page with "1 published. Goal: 10." (your count may differ). The goal is 10 because nothing is saved yet and the schema's default applies.
2. Change **Goal** to 20 and click **Save settings**. The page now ends with "Goal: 20."
3. Sign in as an editor and open the same address. The page shows the same line, without the settings form, because only owners change settings.

If the page says "Could not load the count (503).", the plugin lacks a permission: grant both `kv` and `settings`. If the page stays on "Loading...", open the browser console. A message about the Content Security Policy means a script or style is inline in `index.html`; move it to its own file.

The bridge has one more call, `hatamoto.host`, for host functions, but only the owner may use it. Its rules, the full page policy, template functions and public files are on [Admin pages and templates](admin-pages-and-templates.md).
