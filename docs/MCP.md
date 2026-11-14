# MCP server

Hatamoto serves the [Model Context Protocol](https://modelcontextprotocol.io) at `/mcp` (streamable HTTP, official Go SDK), so an AI agent can draft, publish and inspect the site.

## Connect

1. Create an API key in the admin (or `POST /api/v1/api-keys`) with the scopes the agent needs.
2. Point the client at `https://your-site/mcp` and send `Authorization: Bearer nxk_...`.

```json
{ "mcpServers": { "hatamoto": { "url": "https://example.com/mcp", "headers": { "Authorization": "Bearer nxk_..." } } } }
```

No key, or a revoked/expired key, gets `401`. The key's rate limit applies to every HTTP request to `/mcp` (initialize, list, ping and tool calls alike; over the limit: `429`). Idle sessions are closed after 30 minutes.

**Origin.** A request with an `Origin` header (a browser) must come from the site's own origin (`PUBLIC_BASE_URL`) or one listed in `HATAMOTO_MCP_ALLOWED_ORIGINS` (comma-separated); anything else gets `403`. Clients without an `Origin` header (agents, CLIs) are not affected. Behind a reverse proxy on the same host (Caddy, nginx) nothing else is needed: the SDK's loopback `Host` check is off because every request already needs a secret bearer key.

## Scopes

Each tool needs one scope. A `read:*` scope cannot call a write tool; `write:entities` implies `read:entities`; `*` allows all. Media, menus, redirects and terms have their own write scopes: `write:entities` does **not** grant them, and existing keys do not gain them. `write:menus`, `write:redirects` and `write:terms` are MCP-only. `read:media` and `write:media` also unlock two REST routes: `GET /api/v1/media/` (list) and `POST /api/v1/media/upload`; `write:media` implies `read:media`. `read:seo` and `write:seo` also unlock one REST route, `GET|PATCH /api/v1/settings/seo`; otherwise REST API keys reach nothing but entities, linking and those media, SEO and forms routes. `write:seo` implies `read:seo`. `read:forms` has no MCP tool; it unlocks `GET` on `/api/v1/forms`, `/api/v1/forms/{id}` and `/api/v1/forms/{id}/submissions`.

| Scope | Tools |
|---|---|
| `read:entities` | `entities_list`, `entities_get`, `search`, `schemas_list`, `schemas_get`, `terms_list`, `menus_list`, `menus_get`, `redirects_list`, `sitemap_preview`, `llms_txt_preview`, `jsonld_validate`, and the builder read tools `builder_kinds`, `builder_stylespec`, `builder_design`, `builder_get`, `builder_validate`, `builder_render`, `builder_templates_list`, `builder_template_nodes` |
| `write:entities` | `entities_create` (always a draft), `entities_duplicate` (always a draft), `entities_reorder`, `entities_update`, `entities_publish`, `entities_unpublish`, `builder_apply_ops` |
| `write:media` | `media_upload` |
| `write:terms` | `terms_create` |
| `write:menus` | `menus_update` |
| `write:redirects` | `redirects_create`, `redirects_delete` |
| `read:seo` | `seo_get`, `seo_preview`, `seo_analyze`, `seo_404_list` |
| `write:seo` | `seo_update` |
| `read:reports` | `analytics_crawler_hits`, `analytics_first_crawls`, `analytics_google`, `analytics_ai_referrals`, `analytics_traffic`, `analytics_conversions` |

`entities_update`, `entities_unpublish`, `builder_apply_ops`, `menus_update` and `redirects_delete` are marked destructive in the tool annotations, so clients can ask before running them.

A term in `terms_list` carries its `seo` when one is saved. It is read-only here: `terms_create` takes no `seo`, and a term's search appearance is edited on its admin page.

Entity writes are attributed to the key's creator and tagged `[mcp:<key name>]` in the version history.

## Behaviour

- Tools call the same services as the HTTP API (no HTTP self-calls), so validation, slug rules, sanitising of rich-text HTML (and of the inline `html` prop of `heading` and `text` nodes in `page_doc`: `strong em b i u s code br a` only, safe links, `rel="noopener noreferrer"` on `target="_blank"`), plugin `before_save` hooks and cache invalidation are identical. A heading or text node renders its `html` where it has one: changing only `p.text` of such a node changes nothing on the page, so change `html` too or send the node without `p.html`. Sanitised `html` over 20,000 characters is a 422 `invalid_page_doc`.
- `attributes.page_doc` (a node tree, see [BUILDER.md](BUILDER.md)) can be sent to `entities_create` and `entities_update`. It is validated and its component content sanitised; the stored `page_sections` is a frozen legacy key and is never rewritten. An invalid doc fails with `invalid page_doc: ...` (REST: 422 `invalid_page_doc`). Through `entities_update` a `page_doc` also meets the builder's reuse rules, as in the admin builder save: a `slot` node belongs in a layout part only, and a block must not embed itself (directly or through other blocks); a doc that breaks them is refused with `invalid page_doc: ...` (before this was stored; the REST API does not check these two rules yet). Sending a changed `page_sections` for a page that has a `page_doc` fails with `page_sections can no longer change a page that has a page_doc; send attributes.page_doc` (REST: 422 `page_sections_removed`); sending the stored or derived value back is accepted. The valid style keys and values of a node's `s` map are listed by the `builder_stylespec` tool (and by `GET <admin>/builder/stylespec` in an admin session, where `<admin>` is the install's private admin address from `hatamoto admin-url`; the same table the builder uses): read it to see every key, its tokens, enum values, bounds and patterns before writing styles. To edit a page's layout step by step, prefer the [builder tools](#builder-tools) over sending the whole `page_doc`. Values outside it fail the whole write. A doc that uses the newest keys cannot be read by an older binary (see the rollback floor in [BUILDER.md](BUILDER.md#stylespec)). A page without a doc that is created or updated with `page_sections` is converted to a `page_doc` once (deprecated). Omitting `page_doc` on update keeps the stored one, but `attributes` still replaces the rest as a whole.
- `entities_duplicate` takes the `id` of a page or post and creates a draft copy in the same language and under the same parent, named `Copy of <name>` with the slug `<slug>-copy` (`-copy-2` and so on when taken). It copies attributes (without `canonical_url`), metadata, byline and terms, never the status, dates or translation group, and returns `id`, `name`, `slug` and `status`. People and reusable blocks are refused. The copy is owned by the site even when a plugin created the original.
- `entities_reorder` takes `id`, `position` (1-based among the siblings of the same type, parent and language; 0 or less means last) and optionally `parent_id` or `clear_parent`. A pure reorder stores no version, keeps `updated_at` and leaves a plugin's entity with the plugin; a move to another parent changes the address (the old one redirects) and counts as an edit. `terms_reorder` does the same for a term (write:terms scope). Loops and `query` show the result with `order: "manual"`.
- `media_upload` takes base64. The encoded size is checked against the media size limit (`MEDIA_MAX_SIZE_MB`, default 10) before decoding; the type is sniffed from the bytes and must match the file extension (jpg, png, gif, webp, pdf, mp4, webm; never SVG).
- `entities_update` replaces `attributes` as a whole: read, change, send back.
- An `entities_update` that changes the body, slug, parent or an SEO field of an entity a plugin imported takes it over: the plugin's later changeset items for it get `not_owned` until an owner or editor hands it back from the editor. Sending the stored values back changes nothing. The takeover appears in the audit as an `entities_take_over` call. See [Ownership](PLUGIN_API.md#ownership).
- Every entity has a `locale` (a BCP 47 tag such as `en` or `fr`). `entities_create` and `entities_update` take an optional `locale`; empty on create stores the site language (`und`, all languages, when the entity type is marked "Shared across languages" and the site has more than one language), omitted on update keeps the stored one. Entity results include `locale`. A slug is unique per schema and locale, and the public site serves only entities in the site language.
- The byline is separate from `attributes`: `entities_update` takes `byline_person_id` (the id of an entity of the `person` schema), `byline_org` (`true` credits the site organisation) or `clear_byline`. A non-person id is refused. Entity results include `byline_person_id` and `byline_org` when set. REST `PATCH /api/v1/entities/{id}` accepts the same `byline_person_id` and `byline_org`, plus `clear_author` to remove the byline.
- Once an entity is credited (a person or the organisation), `attributes.author` and `attributes.author_name` are **not an input** on update: they are overwritten with the credited name, so an attributes object copied earlier cannot revert the byline. Change the credit only with `byline_person_id`, `byline_org` or `clear_byline` (REST: `clear_author`). Plain author text is an input only on create, import, and updates of an entity with no credit.
- `menus_update` with `items` replaces every item. Items are a flat list (`key`, `parent_key`, ...); a parent must come before its children. The list is validated first, then the menu fields and items are replaced in one transaction: a bad item (including an `entity_id` that does not exist) leaves the live menu untouched.
- Menu items have a `link_type` of `entity`, `custom`, `external`, `archive` or `none`. `none` is a text-only item (no link, for example a heading above a group of links); existing `#` items now read back as `none`. `menus_get` and `menus_list` always include an `attrs` object per item, `{}` when empty. Its fields are `style`, `color`, `badge` and `icon` (ids of presets the active theme declares; an unknown id is kept and not shown), `show_on` (`desktop` or `mobile`; empty means all devices), `id`, `title`, `aria_label` and `rel` (a list of `nofollow`, `sponsored`, `ugc`, `me`). `css_classes` is at most 200 characters and must not contain control characters, quotes, backticks or angle brackets. Invalid values are rejected before anything is saved.
- `menus_update` returns the menu fields at the top level (`id`, `name`, `items`, ...) plus a `warnings` array that is always present, `[]` when there is nothing to report. An item that is accepted but has no target (an `entity` item without `entity_id`, a `custom` item without `url`, an `archive` item without `schema_slug` or `url`) renders as plain text, and the save succeeds with one string per such item, for example `items[2] "Docs": no target, it renders as plain text` (`2` is the position in the `items` you sent). `none` items are never reported. Read `warnings` after a write to catch items that lost their link.
- `jsonld_validate` reads the JSON-LD from the live page when the entity is published (otherwise it generates it from the draft) and checks JSON validity, `@context`, `@type` and the required fields of common types (Article, BlogPosting, WebPage, Organization, LocalBusiness, Product, Service, Event, FAQPage, HowTo, BreadcrumbList ...). Unknown types only get their `@type` checked.
- `sitemap_preview` and `llms_txt_preview` serve the real route in process, so they return exactly what crawlers get.
- AI referrals are counted per assistant and day only (the site keeps no visitor or per-page referral data); crawler hits are per page and per agent.

## SEO tools

Five tools read and change the site's search settings (Settings > SEO in the admin). They call the same service as `GET|PATCH /api/v1/settings/seo` (owner session or API key; `read:seo` for `GET`, `write:seo` for `PATCH`; no other REST route takes these scopes).

| Tool | Scope | What it does |
|---|---|---|
| `seo_get` | `read:seo` | The whole SEO document: the 16 sections (`separator`, `home`, `types`, `taxonomy`, `archives`, `variables`, `social`, `index`, `robots`, `verification`, `sitemap`, `breadcrumbs`, `indexnow`, `monitor404`, `ads_txt`, `app_ads_txt`), `type_settings` (per content type, by slug) and `ai_crawlers`. |
| `seo_update` | `write:seo` | Merges a partial document into the stored settings and returns the whole updated document, or the field errors (a refused patch changes nothing). |
| `seo_preview` | `read:seo` | (input: `entity_id`) The SEO part of a published entry's public head (title, description, robots, canonical, Open Graph and Twitter tags), built as a visitor would get it. A draft is not found. |
| `seo_analyze` | `read:seo` | The entry's SEO panel checks, counts and score, from the stored entity (input: `entity_id`), the title and description length checks included. No keyword or `llms.txt` checks. |
| `seo_404_list` | `read:seo` | The 404 monitor. `visitor` is `""` (all), `people` or `bots`. |

`seo_get` returns the document in the shape `seo_update` takes, so read, edit a copy and send it back. Fields keep their **stored polarity**: a switch that the admin shows as "enabled" may be stored as `disabled` (for example `monitor404.disabled`), and the document uses the stored name and meaning, not the admin label.

How a patch merges:

- Only the keys you send change; a section you send is merged into the stored one. Decoding is strict: an unknown key or field is refused.
- Top-level keys are the 16 sections, `type_settings` and `ai_crawlers`. A `{}` patch writes nothing.
- `null` on an entry of a map deletes it. This covers `types`, `taxonomy`, `variables`, `social.types`, `social.taxonomy` and the `index.noindex_*` maps.
- `type_settings.<slug>` takes `archive_title`, `archive_description`, `schema_org_type`, `article_type`, `faq_from_content` and `sitemap` (`exclude`, `priority` or `null`, `change_freq`). An empty string clears a text field. `schema_org_type` is refused for `person` and for a type whose schema fixes the JSON-LD type.
- `ai_crawlers.denied_purposes` replaces the whole list.
- The IndexNow key is read-only: it is never returned and a patch that carries it is refused (`indexnow` error). Enabling IndexNow with no stored key generates one.
- The whole patch is validated before anything is written, and the settings and per-type rows are saved in one transaction. A failure changes nothing.
- Invalid input is a REST `422` `{"error": "...", "fields": {"<field>": "<message>"}}`, a tool error elsewhere. Field names look like `type[doc][title]`, `type_settings[<slug>][<field>]`, `ai_crawlers[denied_purposes]`; a body that does not decode is named by its section.
- Each change writes an audit row with the way in (`api` or `mcp`), the key and the sections changed; values and the IndexNow key are not logged.

## Builder tools

Nine tools read and edit a page's layout (the `page_doc` node tree described in [BUILDER.md](BUILDER.md)) the way the admin builder does: through ops, with a version check, and through the same validation, sanitising and reuse rules as the builder's save. The tools work on pages, [blocks and layout parts](BUILDER.md); a post, a person or a page that still has only the legacy `page_sections` layout is refused with `not_builder:` (open such a page once in the admin builder). A page with no layout yet starts as an empty doc.

| Tool | Scope | What it does |
|---|---|---|
| `builder_kinds` | `read:entities` | Node kinds: props with types and limits, whether the kind takes children, `onlyIn` (the one parent kind it must sit in), `interactive` (not allowed inside a linked container), `universal` (the element props the kind takes) with their limits in the top-level `universalProps`, bindable props. Optional `kind` filter. |
| `builder_stylespec` | `read:entities` | Every style key of a node's `s` map with its value rules, plus `breakpoints` (`base`, `md`, `sm`) and `states` (`hover`, `focus`, `active`, `parent-hover`); a style map key is a breakpoint or `breakpoint:state`. |
| `builder_design` | `read:entities` | The site's tokens (the names a style value takes: `space-4`, `primary`, `lg`), palette (resolved values of colours, spacing steps and font sizes) and named presets. Read only. |
| `builder_get` | `read:entities` | `{version, doc}` of a page, block or part. Pass `version` back to `builder_apply_ops`. |
| `builder_validate` | `read:entities` | A dry run: checks a whole `doc`, or `ops` tried on the stored layout, or the stored layout alone. Returns `{ok, errors:[{code, message}], ids}`; `ids` lists the ids generated for the ops, as in `builder_apply_ops`. Stores nothing. |
| `builder_render` | `read:entities` | `{html, css, ids}` of the page, or of the nodes in `ids`, optionally after `ops` (a dry run; the reply's `ids` lists the ids generated for the ops, so you can name them in the request). Stores nothing. |
| `builder_templates_list` | `read:entities` | The saved tree templates (up to 100). |
| `builder_template_nodes` | `read:entities` | A template's nodes with fresh ids on every call, to use as `node` of an `insert` op. |
| `builder_apply_ops` | `write:entities` | Applies ops and stores one new version. |

### Ops

`builder_apply_ops` takes `{entity_id, version, ops, publish?}`. The ops apply in order to a copy of the stored layout; the first one that fails stops the call and nothing is stored (`invalid_page_doc: op 3 (move): ...`). At most 500 ops and 1 MiB of ops per call (`limit:`).

| `op` | Fields | Effect |
|---|---|---|
| `insert` | `node`, `parent` (empty: top level), `index` (omitted: append) | Inserts the node. Nodes (and their children) without an `id` get a generated one. |
| `remove` | `id` | Removes the node and its subtree. |
| `move` | `id`, `parent`, `index` (omitted: append) | Moves the node. |
| `setProp` | `id`, `key`, `value` | Sets a prop to any JSON value; `null` or no value deletes it. |
| `setStyle` | `id`, `bp`, `key`, `value` | Sets a style entry; `bp` is `base`, `md`, `sm` or with a state (`md:hover`). `null` deletes it. |
| `setBind` | `id`, `key`, `value` | Binds a prop to a data source (a string such as `entity.title`); `null` unbinds. |
| `wrap` | `ids`, `node` (default a container) | Puts adjacent siblings into a new wrapper placed where the first one was. A ref, a component and a legacy (v1) section are never wrapped. |
| `unwrap` | `id` | Replaces a container by its children. Refused for a legacy section (or its component) and when it would leave a component at the top level. |
| `duplicate` | `id` | Inserts a copy with new ids right after the node. Naming the component of a legacy section duplicates the whole section. |

`wrap`, `unwrap` and `duplicate` are expanded here into the core verbs as the builder's own helpers do, with the same legacy-section guards. Generated ids come back in `ids` as `{op, path, id}`: the op's index, the node's path inside the op's `node` (`node`, `node.c.0`, ...) and the new id. The reply is `{version, changed, ids, doc_summary, missingBlocks, stored_differs, published, note?}`. `missingBlocks` lists referenced blocks that cannot be read; `stored_differs` is true when the sanitiser changed what you sent. The new version's change summary is `[mcp:<key name>] Edited by <key name> via MCP`.

With `publish: true` a page that is not yet published (a draft, but also a scheduled or archived one) is published immediately after the save, through the same status change as `entities_publish`; a block or part is never published. If the save worked but the publish did not, the call still succeeds with `published: false` and a `note` that says so (the edit is stored under the returned `version`); publish with `entities_publish`.

### Conflicts and errors

`version` is required. It must be the version `builder_get` returned: a stale one is refused with `conflict: stale version 2; current version is 3 (re-read with builder_get)` and nothing is stored. Read again, redo the edit on the new layout, retry. The version only moves when the layout changes: an `apply` that changes nothing returns the same version with `changed: false`. The check and the write are not one atomic step, as in the admin builder: two writers within the same few milliseconds can both pass.

Builder errors start with a stable code, then a colon:

| Prefix | Meaning |
|---|---|
| `conflict:` | Stale `version`; the text names the current one. |
| `invalid_page_doc:` | An op or the resulting layout was refused: the message names the cause. Rules from the admin save show their code first, for example `invalid_page_doc: part_slot: ...` (a slot outside a layout part) or `ref_cycle` (a block that would embed itself). |
| `not_builder:` | The entity is not edited with the page builder (post, person) or has the legacy layout. |
| `limit:` | More than 500 ops, ops over 1 MiB, more than 50 `ids`, or a render over 2 MiB. |
| `unavailable:` | The site cannot render or has no template store. |
| `forbidden:` | The key lacks the scope (a read-only key calling `builder_apply_ops`). |

Every call is audited with its arguments shortened (see [Audit log](#audit-log)); a whole layout is never logged.

### Rendering

`builder_render` without `ids` renders the whole page as the public site would, as a full HTML document; `css` is its builder stylesheet, which is also inside the document. With `ids` it returns only those nodes' markup as the builder canvas draws it, and their CSS (this includes the state rules the editor forces, so use it to inspect, not to publish). Ops given to `builder_render` are applied to a copy and never stored. The layout you send takes the place of the stored one in the render.

### Templates

`builder_templates_list` lists tree templates saved from the admin builder. Older section templates need the theme's section types and are not offered through MCP. `builder_template_nodes` returns one template's nodes with fresh ids; send each as the `node` of an `insert` op.

### Example session

An agent builds a hero and a two-column section, renders it, hits a conflict and publishes. Each call is shown with the result it returns (`"*"` stands for any value; generated ids differ every time). The test suite replays these blocks against a real database, so the example stays true. `<page-id>` is the `id` that `entities_create` returns.

Create the draft page:

```json mcp-step
{"tool": "entities_create", "arguments": {"schema": "page", "name": "Launch", "slug": "launch"}}
```

```json mcp-result
{"status": "draft", "schema": "page", "id": "*"}
```

Read its layout; a new page is empty and at version 1:

```json mcp-step
{"tool": "builder_get", "arguments": {"entity_id": "<page-id>"}}
```

```json mcp-result
{"version": 1, "role": "page", "doc": {"v": 2, "nodes": []}}
```

Check what a container takes before building:

```json mcp-step
{"tool": "builder_kinds", "arguments": {"kind": "container"}}
```

```json mcp-result
{"kinds": [{"kind": "container", "children": true}]}
```

Build the hero and the two columns in one call. The columns have no `id`, so the server generates them:

```json mcp-step
{"tool": "builder_apply_ops", "arguments": {"entity_id": "<page-id>", "version": 1, "ops": [
  {"op": "insert", "parent": "", "index": 0, "node": {"id": "hero", "t": "container", "p": {"tag": "section"},
    "s": {"base": {"pt": "space-8", "pb": "space-8", "ta": "center"}},
    "c": [
      {"id": "hero-title", "t": "heading", "p": {"text": "Launch week", "level": 1}},
      {"id": "hero-sub", "t": "text", "p": {"text": "Everything ships on Friday."}},
      {"id": "hero-cta", "t": "button", "p": {"text": "Read more"}}
    ]}},
  {"op": "insert", "parent": "", "index": 1, "node": {"id": "cols", "t": "container", "p": {"tag": "section"},
    "s": {"base": {"cols": "2", "gap": "space-6"}, "sm": {"cols": "1"}},
    "c": [
      {"t": "container", "c": [{"id": "left-text", "t": "text", "p": {"text": "What is new"}}]},
      {"t": "container", "c": [{"id": "right-text", "t": "text", "p": {"text": "What is fixed"}}]}
    ]}}
]}}
```

```json mcp-result
{"version": 2, "changed": true, "published": false,
 "ids": [{"op": 1, "path": "node.c.0", "id": "*"}, {"op": 1, "path": "node.c.1", "id": "*"}],
 "doc_summary": {"nodes": 9, "top_level": 2}}
```

Edit the hero: change the title, loosen the spacing and wrap the sub-title and button in one container:

```json mcp-step
{"tool": "builder_apply_ops", "arguments": {"entity_id": "<page-id>", "version": 2, "ops": [
  {"op": "setProp", "id": "hero-title", "key": "text", "value": "Launch week is here"},
  {"op": "setStyle", "id": "hero", "bp": "base", "key": "pt", "value": "space-10"},
  {"op": "wrap", "ids": ["hero-sub", "hero-cta"], "node": {"id": "hero-actions", "t": "container"}}
]}}
```

```json mcp-result
{"version": 3, "changed": true, "doc_summary": {"nodes": 10, "top_level": 2}}
```

A second agent (or a retry) still holding version 2 is refused, and nothing is stored:

```json mcp-step
{"tool": "builder_apply_ops", "arguments": {"entity_id": "<page-id>", "version": 2, "ops": [
  {"op": "setProp", "id": "hero-title", "key": "text", "value": "Stale edit"}
]}}
```

```text mcp-error
conflict: stale version 2; current version is 3
```

Try an edit without storing it. An op that names a node that is not there fails the whole batch:

```json mcp-step
{"tool": "builder_validate", "arguments": {"entity_id": "<page-id>", "ops": [
  {"op": "remove", "id": "no-such-node"}
]}}
```

```json mcp-result
{"ok": false, "errors": [{"code": "invalid_page_doc", "message": "*"}]}
```

Render the hero without storing anything:

```json mcp-step
{"tool": "builder_render", "arguments": {"entity_id": "<page-id>", "ids": ["hero"]}}
```

```json mcp-result
{"html": "*", "css": "*"}
```

Save the final tweak and publish in one call:

```json mcp-step
{"tool": "builder_apply_ops", "arguments": {"entity_id": "<page-id>", "version": 3, "publish": true, "ops": [
  {"op": "setStyle", "id": "hero", "bp": "base", "key": "bg", "value": "surface"}
]}}
```

```json mcp-result
{"version": 4, "changed": true, "published": true, "status": "published"}
```

## Audit log

Every call (allowed or refused) writes a row to `hatamoto_mcp_audit`: time, key id and name, tool, success, error and the arguments (long strings shortened, capped at 4 KB). Rows older than 90 days are pruned by the worker.
