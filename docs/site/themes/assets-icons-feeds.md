---
title: Assets, icons and feeds
nav_title: Assets, icons, feeds
description: How a theme's stylesheets, scripts, fonts and icons are served and cached, plus feeds, internal links and the places plugins add files.
---

This page covers everything a theme serves besides HTML: stylesheets, scripts, fonts and icons, the feeds the site publishes, and the slots through which plugins add their own assets. For a first stylesheet change, follow [Change colours and fonts](change-colours-and-fonts.md).

## CSS bundling

Stylesheets are bundled by the engine. Files are concatenated in this order:

1. `assets/css/base.css`
2. every other `assets/css/*.css`, sorted by name (the default theme ships `blog.css`, `changelog.css`, `docs.css` and `wp-compat.css` here)
3. every `assets/css/sections/*.css`, sorted by name

The result is one fingerprinted file, `/theme/assets/theme.<hash>.css`, where the hash is the first 12 hex characters of the SHA-256 of the bundle. It is served with `Cache-Control: public, max-age=31536000, immutable`, so a changed stylesheet gets a new URL and visitors never see a stale one. `layouts/base.html` links it through `{{.CSSHref}}`:

```html
<link rel="stylesheet" href="{{.CSSHref}}">
```

Because the **lookup chain** (your theme first, then the built-in default theme) applies to every file, a theme that ships only `assets/css/base.css` replaces the default's base styles, while the default's `blog.css`, `changelog.css`, `docs.css`, `wp-compat.css` and section stylesheets are still bundled after it, since they are different files. If you do not want them, ship an empty file of the same name.

The default theme's `blog.css` styles cards, articles and pagination. `wp-compat.css` styles core block classes in rich-text bodies that were imported from WordPress: `alignwide` and `alignfull`, image captions, quotes, tables, columns, buttons, responsive embeds, and colour and size classes. It also follows `prefers-color-scheme` for dark mode: the inline token style carries only values the owner changed from the stock defaults, so an untouched site follows the operating system setting, while colours the owner set win. Navigation is accessible by default, with a skip link, a labelled `nav`, `aria-current` on the current link and `:focus-visible` outlines.

## Scripts and other files

`assets/js/hm.js` is served at `/theme/assets/hm.js` with a five-minute cache. Any other file under `assets/` is served from `/theme/assets/<path>`, also with a five-minute cache (`public, max-age=300`) and an `ETag`. Unknown paths and directories return 404. Every response carries `X-Content-Type-Options: nosniff` and `Access-Control-Allow-Origin: *`, because the sandboxed editor canvas has an opaque origin and loads fonts in CORS mode. The default theme's fonts (Geist and Geist Mono, under the SIL Open Font Licence) live in `assets/fonts/`.

Under the public content security policy, scripts must come from the site's own origin (`script-src 'self'`). Do not rely on inline scripts or third-party script hosts in a theme.

### The default `hm.js`

The default theme works with JavaScript switched off, and `hm.js` only upgrades it. It has no dependencies and does two things:

- **Forms.** Without JavaScript a form posts `application/x-www-form-urlencoded` to `POST /forms/{slug}/submit`, and the server answers `303` back to the page with `?submitted=1` for the success banner or `?form_error=1` for a failure. A hidden honeypot field, `hm_website`, silently drops bots, and the endpoint is rate limited per IP. When a form has the captcha switched on, `hm.js` loads the provider's script and renders its widget in the `hm-form__captcha` element (the default theme's `contact_form` section and the `form` widget both emit it, only while a provider is set up and its secret key is readable); a theme that builds its own form markup needs the same element with `data-hm-captcha` and `data-sitekey` attributes. `hm.js` also fetches a timing token from `GET /api/v1/public/forms/{slug}/token` at page load and sends it in a hidden `hm_ts` field. Both are described under "Spam protection" in [Settings reference](../admin/settings-users-email.md). Without JavaScript a captcha form cannot be submitted. With JavaScript, `hm.js` submits the form as JSON to `/api/v1/public/forms/{slug}/submit` and shows the success message in place, falling back to the native post on any failure.
- **Navigation.** The menu is a `<details>` element. On wide screens `hm.js` keeps it open; below that, Escape or a click outside closes an open menu.

A theme may replace `hm.js` or drop it. If you ship your own, keep the progressive enhancement: the page must work first without it.

## Site icons

`assets/icons/` holds the site icons. Go serves them at the site root, with a 30-day cache (`public, max-age=2592000`):

| Path at the site root | File in the theme |
| --- | --- |
| `/favicon.ico` | `assets/icons/favicon.ico` |
| `/favicon-16.png` | `assets/icons/favicon-16.png` |
| `/favicon-32.png` | `assets/icons/favicon-32.png` |
| `/apple-icon.png` | `assets/icons/apple-icon.png` |
| `/icon-192.png` | `assets/icons/icon-192.png` |
| `/icon-512.png` | `assets/icons/icon-512.png` |

The default theme ships a full set. A theme overrides any of them by shipping a file of the same name, and the rest still come from the default. The default `head` partial links `/favicon.ico`, `/favicon-32.png` and `/apple-icon.png`; if your theme has its own `head.html`, link whichever you ship. These are fixed names, so there is no manifest to edit.

## Feeds

When the site has a `post` schema, `/feed.xml` (RSS 2.0) and `/atom.xml` (Atom 1.0) list the 20 newest published posts, with sanitised bodies, absolute links and the author attribute. A site with no `post` schema answers 404. Feeds are cached with the other rendered pages and are cleared by the same content changes.

The default `head` partial advertises both feeds with `<link rel="alternate">`, built from `.Feeds` in the head view. A theme with its own `head.html` should range over `.Feeds` to keep that:

```gotemplate
{{range .Feeds}}<link rel="alternate" type="{{.Type}}" title="{{.Title}}" href="{{.Href}}">
{{end}}
```

The sitemap's browser stylesheet, `/sitemap.xsl`, is served from the embedded default theme. A theme cannot replace it. The sitemaps, `robots.txt` and `llms.txt` are part of the platform and not templates; see [Sitemaps](../ai-seo/sitemaps.md) and [robots.txt and llms.txt](../ai-seo/robots-llms.md).

## Internal links

The editor stores links to other entities as `<a data-entity-id="UUID" href="...">`. At render time, every such link in section rich text, `html` fields and the post body (`.Body`) gets the target's current public path, resolved in one query per page, so a slug or URL pattern change cannot leave a stale link. Targets that are not published keep their stored href. A theme needs to do nothing to get this.

## Plugin asset slots

`layouts/base.html` calls `{{template "plugin-head" .}}` right before `</head>` and `{{template "plugin-body" .}}` right before `</body>`. Both are defined in `partials/plugin-assets.html`:

```gotemplate
{{define "plugin-head"}}{{with .PluginHead}}{{.}}
{{end}}{{end}}
{{define "plugin-body"}}{{with .PluginBody}}{{.}}
{{end}}{{end}}
```

The host fills `.PluginHead` with `<link rel="stylesheet">` tags and `.PluginBody` with `<script defer>` tags for every loaded plugin whose package ships `assets/plugin.css` or `assets/plugin.js`. Plugin files are served under `/p/<plugin>/assets/...` in plugin-name order. The slots render nothing while no plugin ships assets. The builder's per-page style and its widget islands use the same slots. A theme with its own `base.html` must call both partials to receive plugin assets and builder output.

## Plugin head tags

Plugins can also add structured tags to the head with the `render.head` filter. They arrive through the `Ext` list that the default `head` partial ranges over, so a theme with its own `head.html` should keep that loop. A plugin never supplies raw HTML. The allowed kinds are:

| Kind | Allowed attributes |
| --- | --- |
| `meta` | `name`, `property`, `content` |
| `link` | `rel`, `href`, `type`, `hreflang`, `crossorigin` |
| `script-src` | `src`, `crossorigin` |
| `jsonld` | a JSON-LD document, emitted as `application/ld+json` |

Other attributes and unsafe URL schemes (`javascript:`, `data:`) are dropped, and values are attribute-escaped. The filter has a 2-second deadline and is fail-open: a panic, an error or a timeout leaves the head without the plugin's tags.
