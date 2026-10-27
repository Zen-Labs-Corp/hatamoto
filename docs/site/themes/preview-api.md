---
title: Preview API
nav_title: Preview API
description: The addresses the admin uses to preview a theme and to draw unsaved pages, with their access rules, limits and safe embedding.
---

This page describes the preview features behind the admin: the theme preview on the live site, the theme endpoint the editor builds its forms from, and the endpoint that renders unsaved pages. To preview a theme as an owner, follow [Preview and develop a theme](preview-and-dev.md).

## Overview

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

| Feature | Address | Who |
| --- | --- | --- |
| Theme preview on the live site | `<admin>/preview/theme/<name>`, then `/?hm_theme_preview=<token>` | signed-in `owner` or `editor` starts it |
| Theme description for the editor | `GET /api/v1/render/theme` | `owner` or `editor` |
| Render unsaved attributes | `POST /api/v1/render/preview` | `owner` or `editor` |

## Theme preview on the live site

An installed theme can be previewed on the real site whatever theme is active:

- In **Theme > Themes** (`<admin>/theme/themes`), **Preview** on a theme card opens `<admin>/preview/theme/<name>` in a new tab. The admin mints a signed token that is valid for one hour and sends the browser to the public site with `?hm_theme_preview=<token>`.
- The public site turns the token into a cookie named `hm_theme_preview` and redirects to the same address without the parameter. The cookie is `HttpOnly`, `SameSite=Lax`, has the path `/` and lasts one hour. It is `Secure` when `ADMIN_COOKIE_SECURE` is on, over TLS, or behind a trusted proxy that reports HTTPS. An invalid or expired token clears the cookie.
- While the cookie is valid, pages render with the preview theme, and the theme's assets under `/theme/assets/` are served from it too. Preview responses are `private, no-cache, no-store` and never enter the HTML cache. A visitor without the cookie never sees them.
- `/?hm_theme_preview=off` ends the preview.

The preview shows published content only; it never exposes drafts. Treat the token link as a bearer link: anyone holding it can view the theme's design over published content for the hour, and the cookie outlives a logout.

## `GET /api/v1/render/theme`

This endpoint returns what the admin needs to build section forms: the theme name, its version, the sections with their schemas, and the settings schema. It requires an authenticated `owner` or `editor`. The response is shown in [Sections and schemas](sections.md), under "Theme endpoint".

## `POST /api/v1/render/preview`

This endpoint renders unsaved attributes as a full HTML document. It never writes to the database or the cache. The admin's page and blog editors call it on every change.

```json
{
  "entity_id": "optional uuid (puts data-hm-entity on <main>)",
  "schema": "page",
  "attributes": {
    "meta_title": "Draft",
    "page_doc": { "v": 2, "nodes": [] }
  },
  "path": "optional local path, default /",
  "canvas": false
}
```

The body is `application/json`, at most 4 MiB, otherwise the response is `413` with `preview body too large`. A successful call returns `200 text/html`.

The page body is `attributes.page_doc`, the builder's node tree. The older flat `page_sections` list is still accepted for a draft without a `page_doc` and renders as sections; when both are sent, `page_doc` wins. Stored pages use `page_doc` (see [Builder contract](builder-contract.md)).

Posted section content is sanitised exactly like stored content: scripts are removed and `data-hm-*` is stripped from rich text. The document is `noindex`, carries `data-hm-preview="true"`, and uses the live site chrome (name, navigation, forms). The response headers are:

| Header | Value |
| --- | --- |
| `Cache-Control` | `no-store` |
| `X-Frame-Options` | `SAMEORIGIN` |
| `Content-Security-Policy` | a policy that includes `frame-ancestors 'self'` |
| `X-Robots-Tag` | `noindex` |

Access is limited to `owner` and `editor`, with `401` without credentials and `403` for other roles. When cookie authentication is on (`ENABLE_COOKIE_AUTH`), unsafe methods need a CSRF token: send the `csrf_token` from the login response body in `X-CSRF-Token`. It is bound to the API session cookie, no CSRF cookie is set, and the request must be same-origin. Bearer-token callers are exempt.

This request fetches a preview with an API key sent in the `X-API-Key` header (for example a key with the scope `read:entities`, created by an owner) or with a session token you hold. A `Bearer` header is read as a session token, so an API key sent that way is refused with `401`:

```bash
curl -s -X POST https://example.com/api/v1/render/preview \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"schema":"page","attributes":{"page_doc":{"v":2,"nodes":[]}},"path":"/"}'
```

### The canvas flag

With `"canvas": true` the response also carries the editor bridge script (`<script id="hm-canvas-bridge">`, injected before the last `</body>` and embedded in the program). Nothing else differs from the same request without the flag. The admin's visual editor uses it to talk to the page over `postMessage`. Canvas output also includes canvas-only markers (`data-hm-node`, `data-hm-t`) that never appear on public pages.

Theme assets under `/theme/assets/*` carry `Access-Control-Allow-Origin: *`, because an opaque-origin (sandboxed) iframe loads fonts in CORS mode.

### Embedding a preview safely

The preview HTML contains tenant-authored content and the theme's inline scripts, so it must never run in the admin's origin. Do not use a bare `<iframe srcdoc>`: a `srcdoc` document inherits the parent's origin and could act with the editor's session. Fetch the HTML with the request above and embed it as:

```html
<iframe sandbox="allow-scripts" srcdoc="...the fetched HTML, attribute-escaped..."></iframe>
```

Leave out `allow-same-origin`, so that the document runs in an opaque origin with no access to the admin's cookies, storage or DOM. Its own `/theme/assets/hm.js` still loads and runs. Pointing an iframe at a same-origin URL is only acceptable when that response carries its own `Content-Security-Policy: sandbox allow-scripts`.

## Limits and errors

| Status | When |
| --- | --- |
| `401` | no credentials, or an API key sent as `Bearer` |
| `403` | signed in with a role other than `owner` or `editor`, or a missing CSRF token under cookie authentication |
| `413` | preview body larger than 4 MiB |
