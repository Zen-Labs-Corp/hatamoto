---
title: Understand how plugins work
nav_title: Overview
description: Learn what a plugin can add to your site, what it can never touch, and how your site stays up when a plugin breaks.
next: hello-plugin.md, react-to-events.md
---

A plugin adds a feature to your site without changing the server: a tracking tag on every page, extra data for search engines, a page of its own in the admin, or a job that runs every hour. In this guide you look at where plugins live in the admin and learn the few ideas you need before you write one.

## Before you start

- A running Hatamoto site you can sign in to as an owner. See [Getting started](../getting-started/index.md).
- Some experience with one programming language, HTTP and JSON.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Open **Plugins** in the admin (`<admin>/plugins`). This is where an owner installs, updates and removes plugins. Each installed plugin has a **Settings** tab and a **Permissions** tab. A **permission** is a right the owner grants a plugin. The **Available plugins** card lists the plugins in the **registry**, a place to publish and download plugins, and **Upload package** installs a file you made yourself.

2. Learn what a plugin is made of. A plugin is a **WebAssembly** module: a small compiled program in a standard format that runs inside the server in a closed space, the **sandbox**, with no access to the server's files, memory or network. You write it in Go or another language and ship it as a **package**, one `.hmp` file that holds the whole plugin.

3. Learn the ways a plugin joins in. A **hook** is a point where a plugin joins in: an action runs when something happens, a filter changes a value before it is used. Through hooks and a few other entry points, a plugin can:

   - React to events, such as a page being published, a form being sent or an import finishing ([Hooks and events](hooks-and-events.md)).
   - Change values: add structured data for search engines, add tags to the page head, add sitemap entries, hide page sections or refuse a save.
   - Call the **host API**, the set of functions the server offers a plugin: read and write content, menus, redirects and media, send mail, fetch a web address, keep its own data ([The host API](host-api.md)).
   - Answer its own web addresses, signed in or public, and run jobs on a schedule ([Data, routes and cron](data-routes-cron.md)).
   - Give a theme extra template functions, reusable template pieces and new block types for pages, and show its own page in the admin ([Admin pages and templates](admin-pages-and-templates.md)).
   - Serve public files under `/p/<plugin>/assets/`. The files `assets/plugin.css` and `assets/plugin.js` load on every public page.

4. Learn what a plugin cannot do. Nothing is open unless the owner allows it:

   - It sees no files. Nothing is mounted, and anything it prints to standard output or error is discarded.
   - It has no network of its own. The only way out is the `http.fetch` host function, limited to the hosts its **manifest** lists and to public addresses. The manifest is `plugin.json`, the file that describes the plugin: its name, its version and what it needs.
   - It can do nothing without a permission. A plugin always loads, but nothing is granted by default. A call without its permission returns the error `capability_denied` and the plugin carries on.
   - It cannot read Hatamoto's own tables. A plugin that may store data gets its own database schema and database user, which cannot read users, sessions or any other core table.
   - It cannot take over a core address. Plugin routes live under two fixed prefixes, `/api/v1/p/<plugin>/` and `/p/<plugin>/`.

5. Learn what happens when a plugin breaks. The server treats every plugin as untrusted, so a failure never takes your site down:

   - If a filter fails, crashes, runs out of memory or takes too long, the value stays as it was and the page still loads without that plugin's change. The other plugins still run.
   - An action runs in the background after the event. If it fails, the server only logs it.
   - A hook that crashes or times out three calls in a row is switched off for 30 seconds for that plugin.
   - A plugin route that fails answers `502` (or `503` when the plugin is busy or switched off) with `{"error":"plugin error"}`, and the details go to the server log.

   The one deliberate exception is `entity.before_save`, a filter that may refuse a save on purpose.

6. Learn how installing works. When an owner installs a plugin, the admin lists every permission its manifest asks for. The owner unticks anything they do not want, and only what stays ticked is granted. Packages are **signed** with ed25519 keys, which proves who made them and that nobody changed them. Your server accepts the Hatamoto first-party key and the keys you list in `HATAMOTO_TRUSTED_KEYS`. The details are on [Packaging and signing](packaging-signing.md).

## Check it worked

You can now answer these questions:

- Where does an owner install a plugin and grant its permissions? On **Plugins**, then the plugin's **Permissions** tab.
- What happens to a page when a plugin's filter fails? The page loads without the plugin's change.
- What can a plugin reach without any permission? The data each hook hands it, its own fixed config values and the server log, nothing else.

If the **Plugins** page says "Plugins is not available on this server.", the plugin runtime did not start. Look in the server log from startup for the line `plugins: runtime unavailable; no plugins loaded` and fix the error it names.
