---
title: The builder contract
nav_title: Builder contract
description: What a theme must keep so the visual editor can show and edit its pages, plus the optional design token and layout part settings.
---

The visual page editor shows the real rendered page in a canvas (the preview of the page in the editor) and edits it in place. For that to work, a theme keeps a small contract: every rendered page is a complete HTML document with a small, stable set of `data-hm-*` markers, so the canvas can map every visible piece of text back to the JSON field it came from. The contract also covers a few partials that must behave in a certain way, and optional opt-ins for design tokens and layout parts. This page lists all of it. To put markers on your own section, follow [Add a section](add-a-section.md); to set colours and fonts, follow [Change colours and fonts](change-colours-and-fonts.md).

## Marker contract v1

The markers are the interface between the rendered page and an editor that embeds it. They are isolated in `backend/internal/render/markers.go` and versioned.

| Attribute | On | Value |
| --- | --- | --- |
| `data-hm-contract` | `<html>` | contract version, currently `1`. Bumped on an incompatible change |
| `data-hm-entity` | `<main>` | id of the entity being rendered |
| `data-hm-section` | `<section>` | the stored section `id` |
| `data-hm-type` | `<section>` | the section `type` |
| `data-hm-field` | any element whose text is one stored field | canonical JSON path inside the section's `content` |
| `data-hm-kind` | with `data-hm-field` | the field's kind when it is not plain text: `richtext` or `html` |
| `data-hm-preview` | `<html>` | `true` only in preview output |

A section whose id is a short lower-case slug (`[a-z][a-z0-9_-]*`, up to 64 characters, not `main`, `main-content`, `content`, `nav`, `top`, `header`, `footer` or starting with `hm-`) is also rendered with `id="<section id>"`, so `/#quickstart` links to it. Other ids, such as UUIDs and upper-case ids, get no `id` attribute.

The `data-hm-field` path grammar is segments separated by `.`, where a segment is a property name or an array index: `headline`, `faqs.2.answer`, `address.postal_code`, `ctas.0.text`. Paths are always canonical, with alias names resolved, so they work as keys into the content of the matching section: `p.content` of the `component` node in `page_doc` whose `id` is the `data-hm-section` value, or `attributes.page_sections[i].content` for a page that still has only the legacy section list.

```html
<section data-hm-section="s3" data-hm-type="faq" class="hm-section hm-section--faq">
  <h3 data-hm-field="faqs.0.question">Why?</h3>
</section>
```

In a theme you never write these by hand. The engine's `section` partial emits `<section {{hmsec .}}>`, and inside section templates you write `<h2 {{hmf "heading"}}>` or `{{hmf "faqs" $i "answer"}}`. `base.html` puts `data-hm-contract` on `<html>` and `{{hment .}}` on `<main>`.

### The trust rule

Tenant content is untrusted HTML. Everything the sanitiser lets through from `richtext` and `html` fields has all `data-hm-*` attributes removed, so content cannot forge a section, field or entity marker. Markers on the body wrapper itself (`.hm-html-body` for `html` fields, `.hm-prose` for `richtext` fields) come from the theme, and markers on its descendants never do. A consumer can therefore trust `data-hm-section` and `data-hm-field` on any element that is not inside a `richtext` or `html` body. Text inside such a body is edited as a whole through its wrapper's `data-hm-field` and `data-hm-kind`.

Section elements are omitted for hidden sections, unknown types, blank `content` sections and sections that fail to render, so the number of `data-hm-section` elements is at most the number of visible known sections.

`richtext` and `html` fields are also sanitised when content is saved, and that pass reads the active theme's schemas. The result is the same either way, because the content is sanitised again at render.

## Requirements on a theme

- The `section` partial must print `.Comment` when it is set. The default partial does (`{{if .Comment}}{{.CommentHTML}}{{else}}<section ...>`). Native builder nodes are passed through the partial that way, and a partial that ignores `.Comment` wraps native nodes in a section. Such a theme is unsupported for builder-only pages.
- The head must call `plugin-head`. The per-page `<style id="hm-builder-css">` is appended to it. If the theme never prints it, the engine splices the style before the last `</head>`.
- `layouts/base.html` should call `{{template "plugin-head" .}}` before `</head>` and `{{template "plugin-body" .}}` before `</body>`, which also delivers plugin assets and builder islands.

## Builder pages

A page whose `attributes.page_doc` is a builder tree renders its layout nodes natively. The theme's `section` partial renders section-shaped nodes and is the only wrapper a `component` node gets. Native nodes carry `class="hm-n-<id>"`. The `data-hm-node` marker exists only in canvas output (`canvas: true` previews) and never on public pages, and so does `data-hm-t`.

### Loops and bindings

A `loop` node renders `<div class="hm-n hm-loop hm-n-<id>">` holding one `<div class="hm-loop__item">` per item, and an empty state when nothing matches. With paging on and more than one page it adds a `nav.hm-pager` with `aria-label="Pagination"`, and the page head gets a self canonical (`?page=N` for N of 2 or more) with `rel=prev` and `rel=next`. A bound property is resolved on the server before the usual escaping. None of this needs anything in the theme. See [Live data](../admin/builder-reference.md#live-data).

### Islands and the content security policy

The `tabs` and `video` widgets have an optional `island.js` (a module with no globals and no `eval`). It is served from `/theme/assets/builder/<kind>.<hash8>.js` with an immutable cache, and a `<script type="module" src>` is added to the plugin body slot only for kinds present on a public page, never in the canvas and never inline. The public CSP (`script-src 'self'`) covers it. A page with no tabs and no video has no builder script, and the theme's own `hm.js` is unaffected. The server output works without JavaScript: tabs render every panel visible with its label as a paragraph, and a video renders as a link-first facade that makes no third-party request at load.

## Design tokens (`builder.tokens`)

`theme.json` may carry:

```json
{ "name": "my-theme", "version": "1.0.0", "builder": { "tokens": true } }
```

A theme that sets it gets the site's design tokens as CSS custom properties in the builder style block (`:root{--hm-space-4:1.25rem;...}`, only values that differ from the defaults) and the `fs` style compiles to `var(--hm-fs-<name>,<literal>)`. Your CSS may define or override the same variables as defaults: `--hm-surface`, `--hm-muted`, `--hm-accent`, `--hm-space-0` to `--hm-space-12`, `--hm-fs-xs` to `--hm-fs-4xl` and `--hm-shadow-sm`, `--hm-shadow-md` and `--hm-shadow-lg`. A value the owner sets in Settings wins when the block comes later in the head. Without the flag, nothing changes.

Separately from this opt-in, the site's colours, fonts and radii (primary colour, background, text, font, heading font and three radii) reach every theme through a small inline `<style>` in the default `base.html`, which emits only values the owner changed from the stock defaults. A theme with its own `base.html` can read them from `.Tokens`.

## Layout parts (`builder.parts`)

`theme.json` may list the slots the theme lets the site build in the builder:

```json
{ "builder": { "parts": ["header", "footer", "single", "archive"] } }
```

Unknown names are ignored. A slot is active when the theme opted in, the owner assigned a part (Theme > Layout parts, stored under `theme_extra.builder.parts`), and the part is a `theme_part` entity of that slot with a valid document. A theme that did not opt in renders exactly as before, whatever is assigned.

The wrapper contract needs nothing in the theme; authors never use a `.Part` field:

- `header` and `footer`: the part replaces the theme's `header` or `footer` partial.
- `single` and `archive`: the part replaces the `content` block of the layouts `single`, `single-<schema>`, `archive` and `archive-<schema>`. The part holds one `slot`, and the theme's own content renders at the slot, so the part adds markup before and after it. The `home`, `taxonomy`, `author` and `search` layouts are never replaced.
- An `archive` part whose `slot` has `replace` set prints only its own markup before and after the slot, so the theme's `content` block (its listing) is not drawn. Pair it with a `loop` of `from: "archive"`. `replace` in any other part is `422 part_slot`, and a `single` part is never replaced.
- The theme's original partial or block stays as the fallback. A part that is missing, belongs to another slot, does not parse, has no slot (or two), or fails to render is ignored for that request, and the theme's own markup is used.
- The part's CSS joins the page's `hm-builder-css`, so the head must call `plugin-head` as above.

## Using the contract well

Keep the marker functions on the elements that hold one stored field each. Do not nest a second `data-hm-field` inside a rich text wrapper, because the whole body is one field. Prefer entity data (`.Attrs`) in single-schema layouts and keep `.Blocks` ranged inside them, so builder sections added to a post are not lost. See [Layout and template hierarchy](layout.md) for a worked layout and [Preview API](preview-api.md) for how the canvas loads your theme.
