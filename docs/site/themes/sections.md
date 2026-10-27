---
title: Sections and schemas
nav_title: Sections
description: How section templates and their field lists work, the supported schema rules, every field kind, and the sections the built-in theme ships.
---

A section is a reusable block that an editor adds to a page: a hero, a list of features, a contact form. A theme defines which sections exist by shipping two files per type, a template in `sections/<type>.html` and a JSON Schema in `schemas/<type>.json`. There is no central registry: a type is available when its template exists, and the schema drives the admin form and tells the renderer which strings to sanitise. To build your first section step by step, follow [Add a section](add-a-section.md).

## The template

A section template must define `section-<type>`, where `<type>` matches `[a-z][a-z0-9_]*`. It receives the prepared section as dot:

| Field | Meaning |
| --- | --- |
| `.Content` | the section's `content` map, sanitised, with `x-hm-alias` names mapped to canonical names |
| `.ID`, `.Type`, `.Title` | stored section identity |
| `.Page` | page context: `.Forms` (form definitions by slug), `.Path`, `.Submitted` |
| `.Settings` | the active theme's settings, the same map layouts read |

The engine wraps the output in `<section>` for you (through the `section` partial), so a template starts directly with its content. This is the call to action section from the default theme:

```gotemplate
{{define "section-cta"}}{{$c := .Content}}
<div class="hm-container hm-cta hm-cta--{{default "plain" $c.backgroundStyle}}">
  <h2 class="hm-cta__headline" {{hmf "headline"}}>{{$c.headline}}</h2>
  {{with $c.subheadline}}<div class="hm-cta__subheadline" {{hmf "subheadline"}} data-hm-kind="richtext">{{.}}</div>{{end}}
  {{with $c.buttonText}}<p class="hm-cta__actions"><a class="hm-btn" href="{{cta $c.buttonUrl}}" {{hmf "buttonText"}}>{{.}}</a></p>{{end}}
</div>
{{end}}
```

Three habits make a section editable on the canvas. Put `{{hmf "path"}}` on every element whose text is exactly one stored field. Pass every link through `cta` so only safe schemes are emitted. Print rich text that the editor stores as `richtext` with `data-hm-kind="richtext"` on its wrapper. The functions are described in [Template functions](template-funcs.md).

Sections with `isVisible: false` are skipped, and the rest are ordered by `sort_order`. An unknown section type renders as the HTML comment `<!-- unknown section type: name -->`, with no marker. A section whose template fails to execute leaves `<!-- section render failed: type -->`, and the page is then served with `Cache-Control: private, no-cache, no-store` and not stored in the HTML cache, since the failure may be transient. Each section runs twice per uncached render, so template functions called from a section must be free of side effects.

## The schema

`schemas/<type>.json` describes the section's `content`. The supported JSON Schema subset is plain structural keywords: `type`, `properties`, `items`, `enum`, `default`, `title`, `description`, `required` and numeric or length bounds. The admin form cannot render `$ref`, `oneOf`, `anyOf`, `allOf` or `patternProperties`, so a schema using any of them is rejected when the theme loads, as is an unknown `x-hm-field` kind. The checks walk nested properties and array items.

Hatamoto adds three extensions:

| Keyword | Where | Meaning |
| --- | --- | --- |
| `x-hm-section` | top level | `{ "title", "description", "icon" }` shown in the section picker |
| `x-hm-field` | string property | the field kind, listed below |
| `x-hm-alias` | top-level property | alternative stored names, for example `form_slug` with alias `formSlug`; the renderer maps the alias to the canonical name before the template runs |

A complete schema:

```json
{
  "x-hm-section": { "title": "Call to action", "description": "Heading and one button" },
  "type": "object",
  "properties": {
    "heading":    { "type": "string", "title": "Heading", "x-hm-field": "plaintext" },
    "buttonText": { "type": "string", "title": "Button text", "x-hm-field": "plaintext" },
    "buttonUrl":  { "type": "string", "title": "Button link", "x-hm-field": "url" }
  }
}
```

### Field kinds

The legal values of `x-hm-field` are `plaintext`, `richtext`, `html`, `url`, `image`, `code`, `css` and `lines`.

- `lines` goes on an array of strings and edits it as a textarea with one item per line (the default is a comma separated tag list). Use it when an item can contain a comma, such as `htmx admin, no Node`.
- `plaintext` and `code` are escaped by the template. `code` is edited in a monospace textarea and rendered with `{{hl "go" .code}}`.
- `richtext` and `html` strings are sanitised at render time, and `data-hm-*` is stripped from them. They are sanitised again when content is saved, which reads the active theme's schemas.
- `url` and `image` hold addresses; print them through `cta` and `imgsrc`.
- `css` is edited like `code` and is for a theme's own `<style>` element. At render time every `<` that could start a tag (one followed by `/`, `!`, `?` or an ASCII letter, or the last character of the value) becomes the CSS escape `\3c `, so values joined in one `<style>` can never form `</style>`. A value containing `@import`, `expression(`, `javascript:`, `behavior:` or `-moz-binding` (case-insensitive) becomes empty. That deny-list is best-effort and can be bypassed with CSS escapes, so do not rely on it. The one guaranteed property is that the value cannot leave the `<style>` element. Emit it only inside a top-level `<style>` element, as `<style>{{.Content.css}}</style>`, never inside `<svg>` or an attribute.

Tenant content is untrusted HTML, which is why the sanitiser strips every `data-hm-*` attribute from `richtext` and `html` fields. See [Builder contract](builder-contract.md) for the trust rule.

`theme.json` may carry a `settings_schema` in the same subset for theme-level settings. The theme endpoint returns it as is.

### Code fields and shared partials

`{{hl "go" .code}}` HTML-escapes the source and wraps strings, comments and a small keyword set in `<span class="hm-tok hm-tok--k|s|c">`. The default theme ships two shared partials for this: `section-head` (eyebrow, heading and subheading) and `code-panel` (a framed `<pre>` plus an optional output strip), with `.hm-frame`, `.hm-code` and `.hm-outs` in `base.css`.

## Section library of the default theme

The default theme ships 25 section types: `card_columns`, `client_logos`, `comparison`, `contact_form`, `contact_info`, `content`, `cta`, `faq`, `features`, `hero`, `html`, `link_directory`, `logo_strip`, `overview`, `plugin_directory`, `pricing`, `process_steps`, `split`, `stats`, `sticky_sidebar`, `tag_strip`, `team`, `testimonials`, `theme_directory` and `value_proposition`. Each type has `sections/<type>.html`, `schemas/<type>.json` and `assets/css/sections/<type>.css`. Fields use camelCase.

The `contact_form` section renders a form chosen in the editor. When that form has the spam check switched on and a captcha provider is set up, the section adds a `hm-form__captcha` element (with `data-hm-captcha` and `data-sitekey` attributes) right above the submit button, and `hm.js` fills it with the provider's widget. A theme's own contact form needs the same element. See "Spam protection" in [Settings reference](../admin/settings-users-email.md).

| Type | Fields | Notes |
| --- | --- | --- |
| `features` | `eyebrow, heading, subheading, variant (cards\|list\|bento), columns, features[{title, description, number, category, tags, badge}]` | `bento` is the framed grid with numbered cells |
| `content` | `layout (stack\|essay), eyebrow, asideGlyph, asideText, headline, body` | `essay` adds a sticky aside |
| `process_steps` | `eyebrow, heading, subheading, layout (list\|cards), steps[{number, title, description, code}]` | `cards` shows a code box per step; the cards stay inside the container width |
| `tag_strip` | `label, items[]` | a label plus a row of short items |
| `split` | `eyebrow, heading, lede, footnote, layout, ratio, rowsStyle (definitions\|chips-grid), rows[{term, description}], panel{title, meta, code, language}, outputs[{label, value}], panelHeight (auto\|match)` | text column plus code panel; `match` stretches the panel to the height of the text column above 960px (the code panel grows, outputs sit at the bottom) |
| `card_columns` | `eyebrow, heading, subheading, columnCount, columns[{label, title, description, items[]}], notes[{title, text}]` | markers such as `columns.N.items.M` |
| `comparison` | `eyebrow, heading, subheading, sides[{tag, title, tone (negative\|positive), items[]}]` | glyphs are CSS pseudo-elements, never content |
| `plugin_directory` | `eyebrow, heading, subheading (alias intro), writeUrl, submitUrl, installHint, otherUrl, otherLabel, showSearch` | cards come from the server's signed registry, not from the fields; see below |
| `theme_directory` | `eyebrow, heading, subheading (alias intro), writeUrl, submitUrl, installHint, otherUrl, otherLabel, showSearch` | cards come from the server's signed registry, not from the fields; see below |
| `hero` | includes optional `panel`, `commandChip`, `stripLabel` and `stripItems[]` | the code panel shows with `layout: split` and no image; `stripLabel` plus `stripItems` show a row of short items between the actions and the media (the label needs items to render) |

### Plugin directory

`plugin_directory` lists the plugins of the registry the server is configured for (`HATAMOTO_REGISTRY_URL`), with the same trust and verification as Admin > Plugins > Browse registry. Put it on a normal page to get a plugin directory inside your header, footer and light or dark theme. Each card shows the title (else the name), `name vX`, a First-party or Community badge, `by @author` for community entries only, the description, the permissions as "Needs: ...", and a Source link when the entry has an `https` source. Entries are sorted by name and only plugins are listed.

`writeUrl` and `submitUrl` add "Write a plugin" and "Submit a plugin" buttons (https only; leave empty to omit them). `installHint` is a line under the cards. `otherUrl` and `otherLabel` add a cross-link in the header (for example to the theme directory); the URL is https or a site path starting with `/`, and the label defaults to "More". `showSearch` (default on) reveals a search box that filters the cards in the browser; without JavaScript every card stays visible.

The registry is read on the server, never by the visitor's browser, and cached for 3 minutes, so a newly published registry shows within about 5 minutes. A page with this section is cached for at most 60 seconds whatever the page cache setting is. When the registry is unreachable, unsigned by a trusted key or expired, the section shows "The plugin directory is unavailable right now." and the page still answers 200; that page is not cached. With no registry configured (`HATAMOTO_REGISTRY_URL=off`) it shows "No plugin registry is configured." and is cached normally. A valid registry with no plugins shows "No plugins are listed yet."

### Theme directory

`theme_directory` is the same section for themes: it lists the themes of the registry the server is configured for, with the same trust, verification, sorting, search box, `writeUrl`, `submitUrl`, `installHint`, `otherUrl` and `otherLabel` behaviour as the plugin directory (the buttons read "Write a theme" and "Submit a theme"). A card shows the screenshot, the title (else the name), `name vX`, a First-party or Community badge, `by author` for community entries only, the description and a Source link for an `https` source. Only themes are listed. A page may hold both sections; the registry is fetched once per render.

The screenshots are not stored on your site: the registry host serves them (`screenshots/<name>.<ext>` next to `registry.json`), and the card links the image from there. Without a screenshot the card simply has no image. The cache and failure behaviour is that of the plugin directory: the page is cached for at most 60 seconds, an unreachable, unsigned or expired registry shows "The theme directory is unavailable right now." (200, not cached), `HATAMOTO_REGISTRY_URL=off` shows "No theme registry is configured." and a registry with no themes shows "No themes are listed yet."

Read the files under `backend/internal/render/themes/default/schemas/` for the remaining types; each schema is the authoritative field list.

## Checking coverage

Before you activate a theme, the admin lists the section types your content uses that the theme does not render, with a count per type. A package replacing the active theme asks for confirmation when it would drop section types in use. From the CLI, `hatamoto theme activate <name>` refuses while sections are missing unless you pass `--force`. Because a theme may be partial, you can also leave a section out on purpose and let the default theme render it: only the files you ship override the chain.

## Theme endpoint

The admin builds its section forms from `GET /api/v1/render/theme`, which requires an authenticated `owner` or `editor`:

```json
{
  "name": "default",
  "version": "d316c2f6629b",
  "sections": [
    { "type": "hero", "title": "Hero", "description": "Headline and buttons", "schema": { "type": "object", "properties": {} } }
  ],
  "settings_schema": { "type": "object", "properties": {} }
}
```

`sections` is sorted by `type`, and `version` changes whenever any theme file does. The other render endpoint, which draws unsaved pages, is described in [Preview API](preview-api.md).
