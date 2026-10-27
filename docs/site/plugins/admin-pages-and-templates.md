---
title: Admin pages and templates
nav_title: Admin and templates
description: Everything a plugin can add to the admin and to your theme: its own admin page, a settings form, template functions, sections and files.
---

A plugin can contribute to the admin and to the public site without running code inside a template or the admin's own page. This page covers the admin page, settings, template functions, partials, sections and assets. To build an admin page with a settings form step by step, follow [Add an admin page](add-an-admin-page.md).

## The admin page

A manifest may declare a page of its own:

```json
"ui": {"title": "Leads", "entry": "index.html", "roles": ["owner", "editor"]}
```

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

`ui/<entry>` (default `ui/index.html`) is shown on the plugin's Settings tab at `<admin>/plugins/<name>/settings` for the listed roles (owner only by default), once the plugin is loaded. The old `<admin>/plugins/<name>/ui` address redirects there.

### The sandbox

The page runs in an `<iframe sandbox="allow-scripts">` **without** `allow-same-origin`. It has an opaque origin, so it has no admin cookies, no admin DOM and no storage. It is served from `/plugin-ui/<name>/`, outside `<admin>`, with this policy:

```
Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src 'self';
  style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none';
  form-action 'none'; base-uri 'none'; frame-ancestors 'self'
```

That means no inline script or style, no `eval` and no network of its own. The HTML document needs a signed-in user of an allowed role, and scripts, styles and images are static files from the package's `ui/` directory.

### The bridge

Everything the page does goes through a small bridge. Load it, then use the `hatamoto` object:

```html
<script src="/plugin-ui/_sdk/bridge.js"></script>
<script src="app.js"></script>
```

```js
await hatamoto.ready;                                   // {plugin, functions}
const r = await hatamoto.route("GET", "/leads", null, {page: 1});
//   -> {status, content_type, body, encoding?}  one of the plugin's own routes
const h = await hatamoto.host("kv.get", {key: "last"});
//   -> {ok, result} | {ok:false, error:{code, message}}  a granted host function
```

The bridge is `postMessage` to the admin page, which relays to `POST <admin>/plugins/<name>/bridge` with the session and CSRF protection. The two calls differ in whose authority they use:

- `route` runs one of the plugin's own routes through the same registry as `/api/v1/p/<name>/...`, as the **signed-in user**. The route's `roles` apply, so a route that lists only `owner` refuses an editor who opened the page.
- `host` is **owner only**. The owner granted those permissions to the plugin's code, not to every editor, so an editor's `host` call is refused with 403 and the code `permission_denied`. For the owner, a host function runs only if the plugin holds its permission (checked in the admin script and again on the server). A function the plugin was not granted answers `{ok: false}` with the code `permission_denied` and the message `<fn> is not available to this plugin's page`.

A route body that is not valid UTF-8 comes back base64-encoded, with `encoding: "base64"`. The bridge is rate limited per user and plugin (30 calls per second, burst 60, then HTTP 429). The page cannot read the user, the session, the CSRF token or other plugins. It serves only the first document loaded in the frame, and every request carries a per-load nonce. If the page navigates the frame elsewhere, or reloads itself, the host stops relaying and blanks the frame (the document URL carries a one-time ticket that the admin minted for your session, so it loads once). Scripts, styles and images need no ticket.

> **Residual risk.** A sandboxed frame can still navigate itself once, so a plugin page can leak what it can read (what the bridge returned to it) through that navigation. The policy cannot stop it. Install only plugins you trust with what their page can see.

The Search Console plugin ships a `ui/` page built this way: see its [`ui/app.js`](https://github.com/Zen-Labs-Corp/hatamoto-plugin-searchconsole/blob/main/ui/app.js) for working bridge code.

## Settings

A plugin with a `settings_schema` gets a schema-driven settings form in the admin and the `GET` and `PUT /api/v1/plugins/{name}/settings` endpoints. The guest reads values with `settings.get` (permission `settings`). Mark secrets `"writeOnly": true`: the API and form never return them. Use the manifest's `config` for fixed values that are not meant to be edited.

## Template functions

Plugin code never runs inside a template, but a theme can call functions the plugin declares in `funcs`:

```
{{ plugin_leads_badge "arg" 1 }}
```

The name is `plugin_<name>_<func>`, with a `-` in the plugin name written as `_`. The host calls the export `func_<func>` with the arguments as a JSON array and uses the output as **plain text**, escaped by `html/template` like any string, so it can never inject markup. In Go:

```go
guest.OnFunc("badge", func(args []json.RawMessage) (string, error) { return "x", nil })

//go:wasmexport func_badge
func funcBadge() int32 { return guest.Run(guest.FuncExport("badge")) }
```

Each call runs for the function's own timeout: 250 ms by default, or `limits.hook_timeouts_ms["func_<name>"]` up to 10 seconds. Output is capped at 64 KiB. An error, a timeout or an oversized answer renders as an empty string and is logged, never a failed page. After three consecutive failures of the same function, that function (not the plugin's others) is skipped for 30 seconds.

A page rendered while a function was blank or skipped, or while a `render.head`, `render.page` or `jsonld.enrich` filter failed, timed out or was skipped by the circuit breaker, is served `Cache-Control: private, no-store` and is not stored in the page cache. A bad moment is never frozen into cached pages. The signal is process-wide, so while a plugin is failing, pages rendered at the same time may also stay uncached.

See [Template functions](../themes/template-funcs.md) for how a theme author uses them.

## Partials and sections

Two directories in the package add templates:

- **Partials.** `partials/<name>.html` is available as `{{ template "plugin/<name>/<partial>" . }}` (again `-` is `_` in the plugin name).
- **Section packs.** `sections/<type>.html` defines a section type matching `[a-z][a-z0-9_]*`. The file must define `"section-<type>"`, like a theme's own sections. A section type the active theme defines wins, and a type two plugins both ship is refused for the second. See [Sections](../themes/sections.md).

A plugin template may only define names of its own: `plugin/<name>/...` for partials and `section-<type>` or `plugin/<name>/...` for sections. The host parses each plugin template in a scratch set first. A `{{define "head"}}` (or any other theme template), a syntax error, an unknown function or a section that does not define its own entry rejects **all** of that plugin's templates, which is logged. The theme is never touched, so a broken plugin template can neither replace a theme template nor stop the theme from compiling. Template text is trusted markup like a theme's own, so install only template packs you would put in a theme.

Partials, sections and functions are removed again when the plugin is unloaded or reloaded.

## Public assets

Files in `assets/` are served publicly at `/p/<plugin>/assets/<file>` (GET and HEAD, `nosniff`, sandboxing CSP) and take priority over your own `/p/<plugin>/...` routes. If `assets/plugin.css` exists, every public page links it, and `assets/plugin.js` is linked only while the plugin has the `site.scripts` permission (`plugin.css` needs no grant), through the `plugin-head` and `plugin-body` slots of the theme. The default theme includes those slots. A custom theme's base layout must include them for the files to load. The gtm plugin works this way, with the grant: a `render.head` filter adds a meta tag and `assets/plugin.js` reads it.
