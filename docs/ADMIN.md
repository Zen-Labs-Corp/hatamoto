# The Go admin

The admin is server-rendered Go: `html/template` pages from `backend/web/admin`,
[htmx](https://htmx.org) for interactivity, no Node build. The binary serves all of
`<admin>/*` itself; the Next.js admin it replaced is deleted (v1-15).

This page writes the admin address as `<admin>`. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address (`hatamoto admin-url`).

```
backend/web/admin/        layouts/ partials/ pages/ css/ js/   (embedded in the binary)
backend/web/vendor/       htmx, SortableJS, Tiptap, pinned by vendor.json
backend/internal/admin/   handlers (never imports internal/server)
  ui/                     typed shapes of the partials
  schemaform/             JSON Schema -> HTML form -> JSON
  starter/                the setup wizard's starter content
  a11y/                   the no-browser accessibility checker used by tests
backend/internal/server/admin_mount.go   the only server -> admin edge
```

## Navigation and URL scheme

The sidebar has five primary items: **Entities** (`<admin>/entities`), **Theme**
(`<admin>/theme`), **AI analytics** (`<admin>/analytics`), **Plugins**
(`<admin>/plugins`; it never has children) and **Settings** (`<admin>/settings`). Theme and
Settings are shown when the signed-in role can reach at least one of their tabs. The footer holds an avatar link to `<admin>/profile`
and two icon buttons, theme (`POST <admin>/theme`) and sign out (`POST <admin>/logout`), each
a real form with an `aria-label` and a tooltip. Sign-in lands on `<admin>/entities`. Below 640
pixels the sidebar becomes a top bar and the Settings section list becomes a select with a Go
button (plain HTML, no script). Every nav and template icon name must exist in the icon map
(`icons.go`); an unknown name is a render error.

**People** (`<admin>/people`) sits under Entities (the Entities list has a People button, and the
Entities item stays active there). It lists every `person` entity (search, `?team=yes|no`,
sort), has **Add person** (a dialog, or `<admin>/people/new` without JS) and a **Possible
duplicates (N)** button to `<admin>/people/duplicates`, where each group's Merge form posts to
`POST <admin>/people/merge` (owner and editor). A person is edited in the normal
entity form, so fields a site adds to the type show there. The profile page
(`<admin>/profile`) edits the signed-in user's own person: name, photo, job title, short bio,
LinkedIn and the **Public profile** switch.

Merging keeps the kept person's `public` and `on_team` choices. The dropped person's profile
fields fill the gaps in the kept person, except when the kept person is public and the dropped
one was hidden: then nothing is copied, so a hidden person's details never become public.

**Complete your profile.** `RequireProfile` runs after `RequireSession` on every admin
route. A user whose person has no name or no photo is sent to `<admin>/profile/complete`
(a GET carries `next=`, an htmx request gets `HX-Redirect`, a POST is redirected without
`next`). Exempt: `<admin>/profile*`, `logout`, `assets`, `media/upload`, `media/picker`,
`theme` (that exact path only: it is the colour-scheme switch, and the Theme pages under
`<admin>/theme/...` are gated). "Complete" is derived from the person (`person.Complete`), nothing is stored.

Settings and Theme each have a secondary sidebar, built by one component (`settings_nav.go`:
an `adminArea` with `For(role)`; `SettingsFor` and `ThemeFor` are the two areas). A section
URL (`<admin>/settings/{entity-models,import-export}`) answers 302 to the first tab
the role can reach (the pre-v1-26 IDs `content-model`, `seo` and `team` still
answer, as does `?section=` with them), and `GET <admin>/settings` redirects to the first
reachable section. `GET <admin>/theme` does the same for Theme (`?section=` picks one). The old
`appearance`, `menus` and `menus-forms` sections moved to Theme: `<admin>/settings?section=` with
them lands on the Theme section that replaced them. Each tab is its own URL:

| Section | Tabs (path under `<admin>/settings/`) | Who |
| --- | --- | --- |
| General | `general` (identity, homepage, language, contact, email badge on one page) | owner |
| Email | `email` (SMTP settings, test, reset) | owner |
| Media | `media` (the library) | owner, editor |
| Entity models | `types`, `taxonomies` | owner |
| Forms | `forms`, `submissions` | owner, editor |
| Redirects | `redirects` | owner, editor |
| Internal linking | `linking` | owner, editor |
| Users | `users` | owner |
| API | `api-keys` | owner |
| Import & export | `import` (WordPress), `export` (informational, see EXPORT.md) | owner |
| Performance | `performance` (page cache: on/off, lifetime, size limit, clear, preload) | owner |

Theme has eight sections, one tab each (paths under `<admin>/theme/`):

| Section | Path | Who |
| --- | --- | --- |
| Themes | `themes` (install, preview, activate, settings, uninstall) | owner |
| Colours and fonts | `colours` (the brand colour, fonts, corners) | owner |
| Design system | `design-system` (B5 tokens) | owner |
| Style presets | `presets` (B5) | owner |
| Layout parts | `parts` (B6 slot assignment) | owner |
| Blocks | `blocks` (B6 list: name, used by, edit, delete, **New block**) | owner, editor |
| Templates | `templates` | owner, editor |
| Menus | `menus` | owner, editor |

Entities does not list the built-in `block` and `theme_part` types (rows, counts, type tabs,
Create menu, New chooser, parent and link pickers); they are managed under Theme, and their
`<admin>/entities/{id}/edit` URLs still open the builder. A site's own non-built-in type
named `block` stays in Entities.

Roles are the gates the screens had before the move; the move changed no access. AI analytics
is an area with its own sub-sidebar (like Settings and Theme): Overview (`<admin>/analytics`),
Crawlers, Content, Referrals and Health, plus a page per agent (`<admin>/analytics/agents/{id}`,
Crawlers stays current). Owners and editors reach all of it; only owners change the robots policy.
The old `?tab=` URLs and `/pageviews` redirect to the new sections.

Every page has a width, `narrow`, `compact`, `wide` or `full` (`data-width` on the layout): forms are
narrow, lists wide, `compact` is narrow that stays narrow inside Settings (other Settings pages use the wide column), the page and blog editors full (they use their own layout, without the
sidebar).

## Routes

| Path | Served by |
| --- | --- |
| `<admin>/login`, `logout`, `forgot`, `reset`, `accept`, `setup/*`, `profile` | Go (v1-10) |
| `<admin>/entities*`, `media/{upload,picker,{id}}`, `pick/{entities,terms}` | Go (v1-11, v1-14) |
| `<admin>/theme/*`: `themes*` (activate, settings, uninstall, install), `colours`, `design-system*`, `presets*`, `parts`, `blocks`, `templates*`, `menus*` | Go (the Theme area; the screens date from v1-10 to B6) |
| `<admin>/settings/*`: `general` and `api-keys*`, `media`, `types*`, `taxonomies*`, `forms*`, `submissions*` (list, `{sid}`, bulk, CSV export), `redirects*` (rules, 404 log, explain), `linking*` (rules, suggestions, equity, import), `users*`, `import*` (WordPress wizard, `docs/IMPORT-WORDPRESS.md`), `export`, `performance*` (page cache) | Go (v1-23a; the screens date from v1-10 to v1-22b) |
| `<admin>/analytics` (Overview), `/crawlers`, `/content`, `/referrals`, `/health`, `/agents/{id}`; `POST /crawlers/robots`; `data/{overview,agents,agent,pages,first-crawls,referrals,errors,robots}.json` (all take `from`, `to` and `compare=1`) | Go (v1-14, v1-23a, AI analytics redesign) |
| `<admin>/plugins*` (list, install, update, enable, uninstall, upload), `plugins/{name}/settings` and `/permissions` (plugin pages), `/plugin-ui/*` (their sandboxed documents), `preview/theme/{name}` | Go (v1-18) |
| every old admin path (`schemas*`, `taxonomies*`, `menus*`, `forms*`, `redirects*`, `sections*`, `linking*`, `users*`, `import*`, `media`, `settings/{site,contact,email,api,themes*,sections*,menus*,appearance,...}`, `analytics/agents`) | Go: 301 (GET, HEAD) or 308 (other methods) to the new path (`docs/REDIRECTS.md`) |
| `<admin>/entities/{id}/edit` | the builder for pages and every entity with Page Builder sections; Go blog editor (v1-13) for the post types (`post`, `article`, `blog_post`, `blog`) |
| `<admin>/entities/{id}/edit` (a page with a parseable `page_doc`) | Go builder editor (B3), see "Builder editor (B3)"; `POST` is its no-JS outline form |
| `<admin>/entities/{id}/builder/{stage,component,save,paste}`, `GET <admin>/builder/stylespec` | Go: builder JSON endpoints (B3; `paste` validates clipboard nodes, [BUILDER.md](BUILDER.md#copy-paste-and-the-paste-endpoint)) |
| `<admin>/assets/*` | Go: fingerprinted CSS/JS, vendored modules, `tokens.css` |
| every other `<admin>/*` | Go: a 404 page for a signed-in user, the login page otherwise |
| `GET /auth/*` (the old Next sign-in at `/auth/login`, `/auth/register`) | Go: 302 to `<admin>/login` |

The mux picks the most specific pattern, so a screen only registers its own
patterns. The legacy redirect table runs in the `<admin>` fallback, after the real routes
(which win) and before the session check, so an old URL with no real route never
reaches it. `GET <admin>/analytics` is a real route (the Overview; `?section=` is the no-JS section select, 303). Caddy (or any proxy) forwards everything to the one port; see
`docs/deploy/caddy-hatamoto.md`.

## Content screens (v1-11)

`entities`, `schemas`, `taxonomies` and `media` are served by `internal/admin`
from the services in `Deps.Content` (`ContentDeps`: interfaces for
`entity.Service`, `taxonomy.Service`, `media.Service` and the author store, so
handler tests run on in-memory fakes; the real services are exercised by
`content_integration_test.go`, build tag `integration`, `DATABASE_URL`).

* **Entities**: a secondary sidebar (like Settings and Plugins) lists "All
  entities" and one entry per entity type with its entry count, then, for
  owners, "Manage entity types"; below 640px it is a select + Go (a GET form with
  `type`, no JS). `?type=<slug>` shows that type with its plural as the title,
  one "New <type>" button and no Type column; an unknown slug shows All. The
  list keeps its filters (`q`, `type`, `status`, `author`,
  `term` with descendants, `from`/`to` updated dates, `sort`, `dir`, `page`) in
  the URL. Bulk actions post the checked ids to `<admin>/entities/bulk`; delete
  is answered with a confirmation page first (`confirmed=1` completes it; with
  JavaScript the bulk button opens `#hm-confirm`). The edit form is one POST:
  name, slug, status, schedule (typed in the site time zone), parent (only when
  the schema's URL pattern uses `{path}` or `{parent}`), author, terms, the
  SEO panel and the type's own fields through schemaform with the prefix
  `attr.`. SEO values live in the attributes (`meta_title`, `meta_description`,
  `canonical_url`, `og_image`, `no_index`), the keys the public site reads, and
  are removed from the schemaform output so a field is not shown twice.
  Structural buttons (`_add.`, `_move.`, `_remove.`) re-render without saving.
  Scheduling is `entity.Service.ScheduleEntity` (status `scheduled`,
  `published_at` set; `worker.PublishWorker` publishes it when due).
* **Schemas** (owners): the editor builds the JSON Schema from field rows
  (`field` carries each row's key in document order; a drag only moves the row,
  so nothing is renumbered). Properties the builder cannot express are kept
  as they are (`custom` rows). Postgres `jsonb` does not keep key order, so the
  chosen order is stored in a root `x-hm-order` list that `schemaform` and the
  editor honour. A property may carry `x-hm-group` (a heading string): the
  entity form lists ungrouped properties first, then each group under its
  heading in order of first appearance, keeping `x-hm-order` inside a group.
  Settings keep keys the form does not edit (overrides). The
  address pattern preview is `GET <admin>/settings/types/preview-pattern`.
  Besides the address and archive settings (`url_pattern`, `archive_path`,
  `archive_title`, `archive_description`, `archive_crumb_label`, sitemap and JSON-LD
  type, and an Editor select: Form, or Post editor to edit the type like a blog post), `archive_crumb_label` is the section label in an entry's breadcrumb
  (falls back to `archive_title`, then the type name; the BreadcrumbList JSON-LD uses it).
  Built-in types are read-only **except `person`**: its editor can add fields and
  change every setting above, but the core fields (`job_title`, `short_bio`, `bio`,
  `photo`, `public`, `public_email`, `website`, `linkedin`, `x`, `github`,
  `expertise`, `on_team`) show locked (no remove, name and kind fixed), the name and
  description stay, and the type cannot be deleted. The same rule is enforced in
  `entity.Service.UpdateSchema` (and so for the REST schema API); a refusal is
  `ErrCannotModifyBuiltin`. **Changing the address pattern of a type** (for example person
  from `/author/{slug}` to `/team/{slug}`) writes a 301 from each published entity's old path to
  the new one. A redirect that already exists at an old path is never changed or removed,
  a move that would loop is skipped, and a person who is not public gets none (no page, so
  nothing to redirect).
* **Taxonomies** (owners): kinds are tabs, terms a tree (`terms_tree.go`);
  merge moves entities and reports the old archive path to the move observer.
* **Media**: the library is `<admin>/settings/media`; the detail page, upload and picker stay under `<admin>/media/`. Uploads are `multipart/form-data` to `POST <admin>/media/upload`.
  The CSRF check never parses a whole multipart body (it runs before the
  session check), so an upload carries the token in the `X-CSRF-Token` header
  (htmx, fetch) or as the form's FIRST field, a hidden `_csrf` input placed
  before every file input (the no-JavaScript path; the check reads a bounded
  peek of the body). The token is never accepted from the query string. The
  WordPress import's multipart forms do the same; `TestUILintMultipartFormsPutCSRFFirst`
  fails when a form puts `_csrf` anywhere else. The picker
  (`<admin>/media/picker`, a fragment, unchanged by the move) is opened by `content.js` for every
  `[data-hm-media-picker]` button, which schemaform's image field and the
  SEO social image render hidden until the script runs.
* **Roles**: entities and media are for owners and editors; schemas and
  taxonomies are owner-only, like the API. Editors cannot create terms from the
  entity form.

## Page editing and the shared editors

Every page-editor entity (the page type and every entity that has sections, except posts and persons) opens the
builder at `GET <admin>/entities/{id}/edit` ([Builder editor](#builder-editor-b3)). The v1 section form editor (B1 to
B9: `page_editor.go` handlers, `pagesections.go`, `pageedit.html`, `js/pageeditor.js`) is gone, and so are its
section-form save and the save-as-template buttons. `POST <admin>/entities/{id}/edit` is now only the builder's
no-JavaScript outline form: a classic section-form post carries no `doc`, is redirected and writes nothing. A
form-mode `POST /edit/render` is `415` (JSON only) and `POST /edit/preview` of a page is `404`.

* **No write on GET.** A page that only has `page_sections` opens on its **in-memory** conversion
  (`entity.LegacyPageDoc`); nothing is stored and no version is added. The first builder save persists the doc
  through the normal write path.
* **A stored `page_doc` that cannot be parsed** shows a recovery screen (what happened, a link to Versions to
  restore an earlier version, a link to the entity details form) instead of a form.
* **Kept.** `POST <admin>/entities/{id}/edit/render` (JSON node fragments) and `GET <admin>/builder/stylespec`
  ([CANVAS.md](CANVAS.md)), and `POST <admin>/entities/{id}/edit/preview`, which the blog editor still uses.
* **Rich-text editors.** `js/editors.js` (`@hm/editors`) is the loader that every screen with a `data-hm-editor`
  textarea gets through the markup hooks (`internal/admin/render.go`): it calls `enhanceEditors` from
  `js/editor/mount.js` and loads the Tiptap kit on demand. `js/editor/` is the shared kit (`kit.js`: `mountRichText`,
  `mountHtmlSection`, `mountProseRegion`; `extensions.js`, `entitylink.js`, `links.js`, `htmlblocks.js`,
  `htmlfields.js`/`fieldsview.js`, `prose.js`) and `js/canvas/` the canvas host (`host.js`, `protocol.js`, `layout.js`).
  The blog editor reuses `mountRichText` (see below), which takes `opts.extensions` and `opts.toolbar(specs)`. Every
  editor enhances a textarea and writes back to it (`input` events), so the no-JavaScript form is the same form.
* **Tests.** `go test ./internal/admin` (handlers, goldens, a11y checker, asset closure); `node --test
  scripts/admin-qa/unit` (including `editors-mount.test.mjs`); by-hand lists in [QA.md](QA.md).

## Builder editor (B3)

Every page opens the builder at `/edit` (the choice is in `handleEntityEditor`); a page without a doc opens on its
in-memory conversion. User-facing behaviour, saves and shortcuts are in [BUILDER.md](BUILDER.md#editor).

Bindable props show a **Bind** button beside the control. It opens a "Bind to" select of the sources valid for that
prop (`loop.item.*` only inside a loop; the list is boot data from `builder.BindSources`, the server stays the
authority). Picking one is a single undo step and the field shows a chip with the source and an **Unbind** button; an
attribute source asks for its path. A loop's props use the generic controls (`datalist:schemas`, `datalist:terms`,
enums, numbers), and a loop without one gets **Add empty state**. Outline rows of bound nodes read "(bound)". The
no-JS outline has a source select per bindable prop (`n.<id>.b.<key>`, path in `n.<id>.bp.<key>`), decoded to
`setBind` ops by the same decoder.

Routes (all in the session and CSRF group; each answers 404 for an entity the builder does not own):

| Route | Purpose |
| --- | --- |
| `POST /entities/{id}/edit/render` with `Content-Type: application/json` | node fragments for a posted doc and ops ([CANVAS.md](CANVAS.md#json-doc-mode-the-builder)); form posts keep the B2 mode |
| `POST /entities/{id}/builder/stage` | `{doc}` -> `{url}` of the staged canvas page |
| `POST /entities/{id}/builder/component` | `mode=form` -> `{html}`; `mode=decode` -> `{content, errors, structural, html?}` through `decodeComponent` |
| `POST /entities/{id}/builder/save` | version-checked autosave, save and publish |
| `POST /entities/{id}/builder/paste` | clipboard envelope -> `{nodes, missingBlocks}`: validated, sanitised, fresh ids; writes nothing |
| `GET /builder/stylespec` | the StyleSpec table (shared with B2) |
| `POST /entities/{id}/edit` | no-JS outline form (`_op`: `up down after into add del save publish`) |

Server files are `internal/admin/builder_{editor,render,component,save,outline,stylespec}.go`. The page ships a boot
block, `<script type="application/json" id="hm-builder-boot">` with `{doc, version, status, name, kinds, sections, urls}`
built by `json.Marshal` (data, so the CSP does not apply). The outline sits in `<noscript>`; `?outline=1` makes it the
page. The builder page opts out of the markup hooks, so it does not load `@hm/editors`.

First paint and lazy chunks. **`builder.html`'s modules block lists first-paint modules only; lazy modules are reached by
`import()`, never `modulepreload`.** The page loads `builder`, `builder/screen` and `canvas/host` with their static
closure. The section-form widgets (`repeater`, `content`, plus `editor/mount` and the Tiptap kit) load
when a component form is adopted (`builder/fields.js`) and are then started explicitly with `init(form)` (their
`init` is idempotent: it marks what it has bound). The Style tab body (`builder/style`) loads through `builder/lazy.js`
(`lazy()` memoises; browsers cache a failed module fetch, so the panel tells the user to reload) and is prefetched with `import()` when the browser
is idle; its busy text shows only after 150 ms. The helpers the shell needs at first paint (`BP_NAME`, `NAMES`, the
hide helpers) live in `builder/bp.js`. A core module must never import a lazy one statically: the
`editor_js_first_gzip` budget test fails if it does. New files go at most one directory deep (`js/builder/<name>.js`), a
deeper path is not served. Dynamic specifiers are literal strings, or the budget walk cannot see them.

JS modules (`web/admin/js/builder/`, imported as `@hm/builder/<name>`; one directory level, lowercase names):

| Module | Role |
| --- | --- |
| `screen` | entry: boot, wiring, top bar, selection |
| `store` | immutable doc tree, ops, inverse log, undo/redo (pure) |
| `bridge` | op -> canvas bridge messages and render ids (pure) |
| `sync` | render queue, stage, save, autosave, recovery |
| `navigator` | the ARIA tree: multi-select, inline rename, fold caret (first paint; `pickSelection` is pure) |
| `keymap` | key -> action table (first paint, pure) |
| `keys` | the shortcut sheet and its rows (a lazy chunk, prefetched when idle) |
| `inspector` | tabs and native props (first paint) |
| `fields` | component form fetch and decode post (a lazy chunk, prefetched when idle) |
| `style` | StyleSpec controls (a lazy chunk, loaded through `lazy`) |
| `controls`, `controlsui`, `boxmodel`, `presetsrc` | the Style tab's control model (unit probing from the table pattern, split/join of values, box-model rings, row state, palette; pure, node-tested), the row chrome (set-here dot, source label, reset), and the value a preset gives a key (lazy chunks, imported by `style`) |
| `presetpanel`, `designops` | the Style tab's Preset fieldset, and the preset and copy / paste op builders (lazy chunks; `design` keeps drift, the cascade and the clipboard at first paint) |
| `reusecore`, `reuse` | palette items for templates and blocks (first paint, pure); the save-as-template UI and the ref panel (a lazy chunk, loaded at start when the site has reuse) |
| `bp` | breakpoint names and hide helpers (first paint, pure) |
| `lazy` | memoised `import()` loader (a rejection clears the memo), idle prefetch |
| `palette` | the inline add panel |
| `drag` | drop-target maths and the pointer shield for drag and drop (a lazy chunk, imported by `screen` right after first paint; a handle press before it arrives is replayed) |
| `banners` | the save-conflict and unsaved-copy banners (a lazy chunk; `screen` falls back to a plain line) |
| `ctxmenu` | the element actions menu: `menuItems` availability model and `openMenu` popover (a lazy chunk) |
| `clipops` | multi-selection ops (wrap, delete, duplicate), the clipboard envelope, copy, cut and paste through the paste endpoint, select siblings (a lazy chunk; the first half is pure and node-tested) |
| `picker` | the "+" layout picker: `LAYOUTS` and `openPicker` (a lazy chunk) |
| `history` | the history panel: `historyRows` and `openHistory` (a lazy chunk) |
| `dom` | the `h()` element helper |

`js/editor/mount.js` holds `enhanceEditors` and the Tiptap kit loader, shared by the blog editor and `js/editors.js`. `canvas/host.js` gained `setSelectedNode`, `nodeRect`, `nodes` and
`onLayout`. The styles are `css/pages/builder.css`.

Budget: `TestBudgetBuilderScreenJS` measures `gzip -9` per file over the screen's first-party modules (Tiptap kit
included; vendored libraries excluded) as two metrics: `editor_js_first_gzip` (the static closure, the modulepreload
set; cap is the baseline + 2 KiB and never above 117 KiB) and `editor_js_total_gzip` (static + `import()`; cap
220 KiB). Baselines are in docs/BUILDER.md. Pure modules have
`node --test` suites in `scripts/admin-qa/unit/builder-*.test.mjs`.

### Inspector controls for widgets

The palette groups kinds by Layout, Content, Media, Interactive and Data and inserts structural kinds with their
seeded children as one undo step. The Content tab builds a control per prop from the schema hint: rich text (the
Tiptap kit), a media field (text input and the media picker button), a textarea for lines, code and plain long text,
a datalist for form slugs and menu locations. An image has an alt field with a "Decorative" checkbox and a warning
while neither is set. The Style tab shows enum keys as button groups (direction, wrap, justify, align, align-self),
grid columns, rows and span as presets 1 to 4 plus a bounded number input, a "Reset <name>" button where the node has its own
value at the active device, a set-here dot, a source label ("Local", "Preset <name>" or "Inherited from ..."), and a Visibility group with "Hide on"
Desktop, Tablet and Phone plus "Display". The groups are Layout, In its parent, Spacing, Size, Position, Typography,
Background, Border, Effects, Transform, Filter, Transition and Visibility. Each is a disclosure button with a set-here dot; the open state is kept in `localStorage["hm.builder.style.groups"]`. Lengths are a number, a unit select probed from the table pattern, and a slider; a colour key is a swatch opening a `popover` palette (Tab wraps inside it, Escape returns to the swatch) fed by `boot.design.palette`; Spacing is a linked box model. A slider or colour drag commits through one explicit `setStyle` session, so it is one undo step. Hidden nodes show a badge in the structure tree; the `h` key toggles
hide at the active device (under a wider hide it writes the element's natural `display` to show it).

### Design system (B5)

Theme has two more tabs (owner only): **Design system** (`<admin>/theme/design-system`: colours,
spacing, type scale and shadows, plus "Reset to defaults"; a notice shows when the active theme has not turned on
`builder.tokens`) and **Style presets** (`<admin>/theme/presets`: one section per preset with a grid of
style keys by Desktop, Tablet and Phone, add, and a delete confirmation). Both are plain forms, validated by the
`builder` package, and purge the page cache on save. `POST <admin>/builder/presets` (owner or editor, CSRF) creates a
preset from the editor. The editor boot JSON carries `design` (`tokens`, `presets`, `canCreate`) and `urls.presets`.

The Style tab gets a Preset panel (select, drift list with Reset, Detach to custom, Save as preset) and the
inspector header has Copy style and Paste style buttons (`aria-disabled` until something is copied). Shortcuts
`Ctrl/Cmd+Alt+C` and `Ctrl/Cmd+Alt+V` work from the structure tree and the page, never inside a text control. The tree
row text ends with the preset note. Code: `web/admin/js/builder/design.js` (pure helpers, node-tested).
See [BUILDER.md](BUILDER.md#design-system).

### Reuse (B6)

Theme tab **Templates** (was Section templates, `<admin>/theme/templates`) lists both kinds: "Layout"
templates saved from the builder and the older section templates with their section type. Tab **Layout parts**
(`<admin>/theme/parts`, owner only): one row per slot (header, footer, single, archive) with a select of the
site's parts of that slot ("None" uses the theme's own), **Create <slot> part** (opens the builder on a new part) and
**Edit**; slots the active theme does not support show "Your theme does not use this part". Saving purges the page
cache.

The builder Add tab has groups **Templates** and **Blocks** (boot data, searched with the rest) and a foot with
**Save as template…**, **Save page as template…** and **Save as block…** (each opens a small name dialog, no browser dialogs). A selected `ref` shows the block name or "Missing block", **Edit block** (new tab), **Detach** and **Used on
N pages**; the Navigator row reads "Block: <name>". A block or part opens the same builder with **Save block** / **Save
part**, no autosave, no Publish, and (blocks) a "Used on N pages" disclosure. Routes (session, CSRF, owner or editor):
`POST <admin>/entities/drafts` (`type=block` or `type=theme_part&slot=...`), `POST <admin>/entities/{id}/builder/block`,
`POST <admin>/entities/{id}/builder/template`, `GET <admin>/builder/blocks[/{id}[/usage]]`,
`GET <admin>/builder/templates/{id}`. Code: `web/admin/js/builder/reuse.js` and `reuseops.js` (pure, node-tested).
See [BUILDER.md](BUILDER.md#reuse).

## Blog editor (v1-13)

`GET|POST <admin>/entities/{id}/edit` for the post types (`blog_editor.go`, `blogseo.go`,
`js/blogeditor.js`). One form: inline title (`name`), body (`content` textarea, rich text
with JavaScript), and a sidebar: status and schedule, slug, featured image and excerpt,
categories and tags (owners can create a term inline), author, search and social (focus
keyword, SEO title and description, canonical, social image, noindex, include in
`/llms.txt`). Without JavaScript the textarea is the HTML body and every field saves.

* **What it owns.** `attributes.content`, `excerpt`, `featured_image`, the SEO keys of v1-11,
  `seo.focus_keyword` and `seo.include_in_llms_txt` (stored only when switched off). Every
  other attribute is kept as it is. Title and slug are entity fields. Status, schedule,
  terms and author go through the v1-11 helpers (`afterSave`, `resolveAuthor`).
* **Author.** The sidebar picker lists the people and the site organisation. Picking one
  sets `byline_person_id` (or `byline_org`); typing a new name makes a person that is not
  public and has no login (the same rule as a plain `attributes.author` text), and text equal
  to the site name credits the organisation. The post's old `author_id` is kept in step for
  rollback. A person never opens in the Page Builder: its edit link goes to the entity form,
  whatever is stored under `page_sections` or `page_doc`.
* **Buttons.** A draft has *Save draft* (`_op=save`, status from the select, so *Scheduled*
  plus a time schedules it) and *Publish* (`_op=publish`); a published post has *Update*.
  Saves are version-checked like the page editor: a stale form is 409 with the draft
  re-rendered at the current version, validation errors are 422 and keep the posted version.
* **Autosave.** `POST .../edit/autosave` (urlencoded, `X-CSRF-Token`) writes only name, body,
  excerpt and featured image of a **draft**, checked against `version`: 200
  `{ok, version}`, 409 `{conflict:true, version, error}` when somebody saved since, 409
  without `conflict` for a post that is no longer a draft (the browser says the changes are
  not saved and keeps the leave-page warning), 422 for a rejected value. A request that
  omits excerpt or featured image keeps the stored value; `slug_auto=1` (a hidden field
  until the slug is edited by hand) keeps the slug following the title. A run of one
  person's autosaves within 5 minutes keeps a single revision (summary "Autosaved in the
  blog editor"), so history holds checkpoints; Save, Publish and Update always add one, and
  the nightly prune drops autosave revisions before any explicit one. The browser
  (`editor/autosave.js`) is a true debounce: a save 3 s after the last keystroke, at least
  10 s between saves, at most 30 s of continuous typing without one; one request in flight
  and one follow-up for what was typed meanwhile. The form's `version` follows every
  response, and Save draft / Publish / Update wait for a save in flight. It retries with
  backoff and, on a conflict, waits for *Overwrite with my version* or *Reload*. Published
  and scheduled posts are never autosaved; unsaved work on a published or scheduled post is kept
  as a recovery copy on the server (`internal/recovery`), one per user and post, written by
  `POST /entities/{id}/edit/recovery` (the builder uses `/builder/recovery`; each has a
  `/discard` twin, so four POST routes in `recovery_handlers.go`). Drafts rely on autosave
  instead. The editor page carries the copy (`#hm-be-recovery`; the builder boot JSON's
  `recovery`), compared with the page after normalising the HTML (the sanitiser escapes
  quotes) and offered only when it really differs. A copy survives sign-out and follows the
  user to another device. It is deleted by that user's own save, publish or update, by
  Discard, after 30 days (a prune job), and by cascade when the post or user is deleted.
  Copies that older versions kept in `localStorage` (`hm:blog:` keys) are removed when the
  admin loads and are never restored. The SEO panel's refresh runs
  the checks only; the AI-crawler card and author/term lists load with the page.
* **Slash menu, bubble menu, shortcuts.** `editor/slash.js` (commands: text, headings 2-4,
  lists, quote, code block, table, library image, upload image, video embed, divider,
  callout, FAQ block, html block), `editor/bubble.js` (bold, italic, link, inline code),
  Markdown input rules from StarterKit, `Ctrl/Cmd+S` (flush autosave), `K` (link), `Shift+F`
  (focus mode). They are Tiptap extensions passed in `opts.extensions`; the toolbar gets
  Callout, FAQ, Video and Upload through `opts.toolbar`; `opts.image` swaps in the captioned
  image (`<figure><img><figcaption>`, plain `<img>` without a caption).
* **Blocks and what the server keeps.** Callout `div.callout`, FAQ
  `div.faq > div.faq-item > h3.faq-q + div.faq-a`, embed `div.embed > iframe` and the html
  block `div.html-block` all survive the rich-text sanitiser (classes on div, an allowlisted
  iframe). Embeds accept only YouTube (watch, youtu.be, embed, shorts, nocookie) and Vimeo
  addresses, rewritten to `youtube-nocookie.com/embed/ID` or `player.vimeo.com/video/ID`
  (`editor/embed.js`; the server's `iframeSrcAllowlist` is the authority). The html block's
  source is cleaned to the sanitiser's tag set and shown as source, never rendered in the
  admin.
* **Images.** Paste or drop an image: `editor/upload.js` posts it to
  `<admin>/media/upload?json=1` (the media service; the CSRF token in the header; the
  answer is `{files:[{id,url,name}], errors}`), shows a progress bar in the text, inserts
  the image and focuses its description field. Alt text and caption are inputs under the
  picture.
* **Paste clean-up.** `editor/paste.js` (`transformPastedHTML`): Google Docs wrapper and
  style-based bold/italic/underline/strike, Word `Mso` list paragraphs to real lists,
  comments, `o:*` tags, classes, styles and ids removed, `h1` to `h2`, only the sanitiser's
  tags and URL schemes kept. A convenience: the server sanitiser still runs on every save.
* **Search and answer panel.** `blogseo.go` is pure: search-result preview with title (50 to 60)
  and description (120 to 160) checks; focus keyword in title, first paragraph, description,
  slug, a subheading and its density; heading order, image descriptions, internal and
  external links, length, readability; answer checks (question headings, short direct
  answers under them, a short opening summary, FAQ block, `llms.txt` inclusion); and the
  JSON-LD the public page will emit, built by `entity.GenerateJSONLDWithFAQ`. The panel is
  server-rendered on load and refreshed by htmx (`POST .../edit/analyze`, 1.5 s after the
  last input, answers the `hm/blogpanel` fragment). FAQ blocks reach the public page as
  `FAQPage` (`entity.ExtractFAQItems`, used by `public_pages_loader.go`). Once published, the
  *AI crawlers* card reads `agents.EntityActivity` (`ContentDeps.Crawlers`).
* **Preview.** `POST .../edit/preview` with `X-HM-Stage: 1` stages the posted draft under a frame
  ticket and answers `{url}`; the iframe (`sandbox="allow-scripts"`, never
  `allow-same-origin`) loads it at 1100 px or 390 px. The URL carries a one-time ticket valid
  60 seconds, bound to the signed-in session and to the entity (`frameticket.go`); a request
  without one, with a reused or expired one, or from another session gets the admin 404. Tickets and staged previews live in the serving process's memory, so with more than one replica admin traffic needs sticky sessions. Without JavaScript the button posts the
  form and the server answers the rendered page (`formtarget=_blank`).
* **Tests.** `go test ./internal/admin` (render goldens `blogedit`, saves, conflicts, publish,
  schedule, autosave, panel checks, preview, JSON upload), `go test -tags integration
  ./internal/admin -run BlogEditor` (write, autosave, schedule, publish, update and restore on
  Postgres). The slash menu, paste clean-up, autosave, blocks and upload scripts are checked
  by hand: `docs/QA.md` "Blog editor".

## Security model

* **JSON API origin**: the session-authenticated JSON API is same-origin only
  (no CORS headers, `OPTIONS` is not answered). Use API keys from servers;
  browser JavaScript on another origin cannot call it. The one exception is the
  public delivery API (form token, form submit and page-view track): it answers
  `Origin: null` (a sandboxed page) with `Access-Control-Allow-Origin: null`,
  never with credentials, and never any other origin.
* **Session**: the session cookie, `__Host-s` (`s` when `ADMIN_COOKIE_SECURE` is off; formerly `hatamoto_session`) (HttpOnly, SameSite=Strict) and the
  session store the JSON API uses; independent of `ENABLE_COOKIE_AUTH`. 30 days
  absolute, 7 days idle: an idle session is revoked, not just refused. The Go
  admin never rotates a session (parallel htmx requests would race). `Secure`
  is on when the connection is TLS, when a trusted proxy says so with
  `X-Forwarded-Proto: https`, or when `ADMIN_COOKIE_SECURE` (default on in
  production) asks for it: the setting is a minimum, never switched off by a
  missing header. Signing in revokes the session the browser presented.
* **Trusted proxies**: `X-Forwarded-*` is believed only from
  `HATAMOTO_TRUSTED_PROXIES` (comma-separated CIDRs; default loopback only;
  Docker deployments set the bridge gateway). From anyone else it is just a client-chosen string.
* **Emailed links** (password reset, invitation) use only the configured site
  address (`PUBLIC_BASE_URL`, else Settings > General), never request headers.
  Without one nothing is emailed: the owner sees a banner asking for the address
  and the one-time link on screen, and the server logs why.
* **CSRF**: a token bound to the session (the admin sets no CSRF cookie; a pre-session `hm_pre` cookie covers the
  sign-in forms). A form carries
  `_csrf` (partial `hm/csrf`); htmx requests send `X-CSRF-Token` from
  `<meta name="csrf-token">`. Cross-site `Origin` / `Sec-Fetch-Site` is refused.
* **CSP**, on every Go admin response, replacing the global one: no
  `unsafe-inline`, nonce on every inline `<script>`/`<style>`,
  `frame-ancestors 'self'`. So: no `style=""`, no `onclick=`, no `hx-on:*`, no
  `hx-vals='js:'`, `allowEval:false`. The a11y checker fails the build on them.
* Login failures read the same for a wrong password, an unknown address and a
  locked account; `next` is a path under `<admin>` only. Reset and invite tokens
  are single use, stored hashed, sent in the query string with
  `Referrer-Policy: no-referrer` and never logged.
* First run: `<admin>/setup` needs the setup code from the server log (or
  `HATAMOTO_SETUP_CODE`), and the owner is created under an advisory lock.

## Navigation and htmx conventions

1. **Real page loads.** Links and forms are ordinary navigation. The admin does not use
   `hx-boost` or `hx-history` anywhere (a test fails on either). Every mutation is a real
   `<form method="post">`, answered with 303 and a flash, or 422 and the re-rendered page,
   so the whole admin works without JS and with CSRF and cookies the simple way.
2. **Cross-document View Transitions.** `partials/sidebar.css` opts in with
   `@view-transition { navigation: auto; }`. The sidebar and the Settings subnav carry a
   `view-transition-name`, so only the main area animates (120 ms); `prefers-reduced-motion`
   turns it off. It is CSS only: no script, no CSP change.
3. **Speculation Rules.** Shell pages (not the sign-in and setup pages) carry an inline
   `<script type="speculationrules" nonce>` built in Go (`speculation.go`) that prerenders
   same-site `<admin>/*` links on hover (`eagerness: moderate`). It excludes logout, theme,
   assets, login and setup, previews, plugin `settings`, `ui` and `bridge`, every `.../delete` and
   `export.csv` URL. The tag uses the request nonce like the import map, so `script-src` is
   unchanged. Admin HTML stays `Cache-Control: no-store`: that blocks the back/forward cache
   (back re-fetches the page; the browser still restores scroll) but not prerender.
4. **htmx is for in-page updates only**: the entities filter form, media upload, the parent
   search, SEO analysis in the blog editor, schema field reordering and the import progress
   poll. Each long-running request names an `hx-indicator` element (`.hm-indicator`, shown
   while the request runs; htmx's own indicator CSS is off because of the CSP). A template
   test fails on an `hx-get` or `hx-post` without one unless it is on its allow-list.
5. **Fragments.** `IsHTMX(r)`; `Render` returns the whole document normally and only the
   `main` block for an htmx request with an `HX-Target`; responses carry
   `Vary: HX-Request, HX-Target`.
6. Validation errors re-render in place with **422** (`admin.js` makes htmx swap
   it). Other failures: an error page, or for fragment requests an
   `HX-Trigger` toast plus `HX-Reswap: none`.
7. Success: 303 for plain requests, `HX-Redirect` for htmx (`admin.Redirect`).
   Messages across a redirect use the signed `hm_flash` cookie (`SetFlash`) and
   render as an inline `role=status` banner, which `components.js` turns into an
   auto-dismissing toast (errors stay until closed); without JS the banner stays.
   Toasts from htmx use `HX-Trigger: {"hm:toast": {...}}` (`admin.Toast`).
8. Tables keep their state in the URL (`?q&sort&dir&page`); a table is
   `hx-target`/`hx-select` on its own id, and the filter form is a real GET form that
   works without JS.
9. Destructive actions never fire on one click and never use `hx-confirm`: a
   table `ui.Action` with `Confirm` is a link to a confirmation page (the no-JS
   path) that `components.js` upgrades to the native `<dialog>` `#hm-confirm`.
10. Pages that show a secret once (API key, invite link, reset link) are
    `no-store` and the secret is never in a later response. A new API key is
    shown by POST, 303, GET: the secret waits two minutes in memory for that one
    GET, so reloading the page cannot mint or rotate another key.
11. **Per-page modules.** `admin.js` is the core only: htmx CSRF and config, the theme and
    sign-out submit hooks, and `components`. A page that needs more declares it,
    `{{define "modules"}}@hm/menutree{{end}}`, and `layouts/head` emits a
    `<script type="module">` (with SRI) and a preload for each. A plain list page loads no
    editor code. An unknown module name is a render error. Module boot code runs on
    `DOMContentLoaded` (or at once when the document is already loaded) and again on
    `htmx:afterSettle`.

## Components

Partials are `{{define "hm/<name>"}}` files in `web/admin/partials`, called with a
dict (`{{template "hm/field" dict "ID" "email" ...}}`) or a typed struct from
`admin/ui`. Main partials: `button card-start/-end card-foot field select dialog-start/-end
callout-start/-end flash empty stat steps table tabs badge searchselect parentselect`
(the rest are in the directory). `hm/select` wraps a native select that `@hm/select`
upgrades; the native element stays the source of truth. `hm/callout` is a standing notice, `hm/flash`
with `Inline` an action result, toasts are transient (rules in `ADMIN_UI_RULES.md`). One CSS file per partial in `web/admin/css/partials`, class prefix
`hm-`. Extra attributes go through `attrs`, a whitelist (`hx-*` minus inline
code, `data-*`, `aria-*`, `form*`, a few plain ones): anything else is a render
error. `hm/button` with `IconOnly` requires `AriaLabel`; `hm/field` requires `ID`
and `Name`. Each partial state has a golden in `internal/admin/testdata/golden`
(`go test ./internal/admin -update` to refresh).

To add a screen: a page template in `web/admin/pages/<area>/<name>.html`
(`{{template "layouts/shell" .}}` plus `{{define "main"}}`), a handler that builds
a view with `a.NewView` and renders with `a.Render`, route registration in a
`mountXxx` that `mountScreens` calls, a line in
`internal/server/testdata/admin_routes.golden`, and an a11y test (the guard
`TestEveryPageTemplateIsExercised` fails for a page without one).

## Design tokens (the plugin contract)

`<admin>/assets/tokens.css` serves `web/admin/css/tokens.css` alone, with CORS and
a short cache, so a plugin page can `<link>` it and inherit light and dark.
Names are a contract (`TestTokensContract`): add freely, never rename.

* Neutrals `--color-shiro --color-paper --color-kasumi --color-kinu
  --color-nezumi --color-hai --color-sumi-soft --color-sumi`; accent and status
  `--color-accent(-soft) --color-success|warning|danger(-soft) --admin-accent`;
  `--border-control` (a 3:1 border for checkboxes, switches and secondary
  buttons, unlike the hairline `--color-nezumi`); `--border-field` (the soft
  resting border of text inputs, selects and textareas; hover uses `--color-hai`
  and focus the blue ring, both keep 3:1).
* Radii `--radius-sm|md|lg|xl|full` (6, 8, 12, 24 px and a pill) and
  `--radius-nav`, a kept alias of `--radius-md`; sizes `--control-h
  --control-h-sm --sidebar-w --content-max --page-pad-x|y` (kept for plugins); spacing `--space-1..8` plus the
  half steps `--space-0-5|1-5|2-5`; stacking `--z-raised|sticky|header|popup|tooltip|
  skip|toast|shield`; type `--text-xs..2xl` plus `--text-title` (18 px) and
  `--text-metric` (30 px); fonts `--font-ui --font-mono --font-serif`.
* Elevation `--shadow-1|2|3`, with their own dark values (a 1px light ring plus a
  deeper drop); chart palette `--chart-1..6` (light and dark) with `--chart-bar-soft`
  and `--chart-stack-*`. Partial CSS uses these tokens, not literals.
* Aliases for the design handoff vocabulary: `--bg --surface --text --text-muted
  --border`. Dark values apply under `:root[data-admin-theme="dark"]`, set from
  the `hm_theme` cookie on `<html>` (no flash, no inline script).

Values are the live admin's, so the Go admin looks like the one it replaces; the
handoff's dimensions and rules (36 px controls, 232 px sidebar, one primary
action) are adopted. `go test ./internal/admin/a11y` checks WCAG contrast of the
declared pairs in both themes.

## Assets and vendoring

`make vendor` (network, Go only) refreshes `web/vendor`; `make vendor-verify`
(offline, part of `make check`) proves the committed bytes match `vendor.json`.
URLs must be pinned to `@x.y.z`; changed upstream bytes fail unless `--update`.
Tiptap is a crawled, deduplicated ES module graph (`backend/tools/vendor/README.md`
has the spike outcome). The import map maps `@tiptap/*` and `@hm/<name>` (the
fingerprinted `web/admin/js/*.js`). Dev loop: `HATAMOTO_ADMIN_DIR=backend/web/admin`
reloads templates, CSS and JS per request.

## Site and growth screens (v1-14)

Their services reach the admin through one struct, `Deps.Growth` (`growth.go`), whose
interfaces are filled by `server/admin_growth_mount.go`; a nil member answers a 503 page.

* **Editors that are plain forms.** The menu tree and the form builder post every row as
  `i.N.*` / `f.N.*` fields plus an `_op` button (`up.N`, `down.N`, `indent.N`, `outdent.N`,
  `remove.N`, `add`, `save`). The server applies the edit and re-renders (200, not saved);
  `save` validates and stores. `menutree.js` upgrades the same buttons in place and adds
  SortableJS drag and drop (sub-items travel with their parent). Structural buttons carry
  `formnovalidate`, so an unfinished row can still be moved.
* **Menus** save through `menu.ReplaceMenu`, one transaction: all items or none.
* **`hm/searchselect`** is the minimal combobox behind menu items, linking rules and the
  homepage (`<admin>/pick/*` returns JSON). Typed text without JS is resolved on save by
  exact name or slug. v1-11's entity and media pickers replace it.
* **Dashboards.** One builder per view feeds the page's server-rendered tables and the JSON under
  `data/*.json` that the SVG charts (`js/aia/*`, entry `@hm/aia`) draw from, so a chart cannot disagree
  with its table. The chart stays blank without JS and the table does not. With `compare=1` the JSON adds a
  `previous` series for the preceding period of the same length. The range is `?from&to` (UTC, at most
  366 days, default 30). There is no charting library: Chart.js is no longer shipped.
* **CSV export** streams a page of 500 submissions at a time, with a BOM, and prefixes cells
  that start with `= + - @` so a spreadsheet does not run them.
* **Destructive actions** (menu, form, redirect, rule, section deletes and every bulk
  delete) go through the confirmation page; a bulk delete posts twice (`confirm=1`).

## WordPress import (v1-22b)

`Deps.Import` is `importjob.Manager`: scan and import are background jobs on the server process, their state in
`hatamoto_import_runs`. A page that shows a running job carries `View.Refresh` (a `<noscript>` meta refresh in
`layouts/shell`) and polls itself with htmx (`hx-get ...?poll=<state>`, `hx-select` on its own id); a poll that
finds the run in another state answers `HX-Redirect`, so the page moves on by itself. `limitImportBody` caps an
import form's body before the CSRF check reads it. The private-host override is not reachable from here.

## Packages and themes (v1-18)

Services reach the admin through `Deps.Growth.Packages` (`PackageAdmin`, `packages.go`), filled by
`server/admin_packages.go` over `internal/packages`; a nil member answers a 503 page.

* **Install flow.** A plugin goes fetch/upload, then a **permission prompt** page (the package is
  held in memory for ten minutes, per user, at most eight at a time), then confirm with the ticked
  permissions. Themes install straight away and stay inactive. Refusals the owner should read
  (`*admin.Refusal`: bad signature, untrusted signer, unsigned while not allowed, a theme that does
  not render) come back as a 422 page with the reason.
* **Activation** is a GET confirmation page listing missing section types, then a POST. Uninstalling
  a plugin is a page with the keep-or-delete-data choice; a theme uses the generic confirm page.
* **Uploads** are multipart posts, so the CSRF token travels in the `X-CSRF-Token` header or as the form's first `_csrf` field (the admin never parses a whole multipart body before the session check, and never reads the token from the query string).
* **Unsigned opt-in** is a form with an acknowledgement checkbox and a standing warning on the page.
* **Plugins subnav.** Every `<admin>/plugins*` page sits in a secondary sidebar built like the Settings
  one (`plugins_nav.go`, `partials/pluginsnav.html`): "All plugins", then one entry per installed plugin
  (`<admin>/plugins/{name}/settings`), read from the plugin registry so a new install adds an entry.
  A plugin that is off or uninstalling is muted with an "Off" marker; a failed or blocked one with
  "Failed". At 640px and below it becomes the labelled "Plugin" select plus Go, a plain GET form to
  `<admin>/plugins/go?plugin=<name>` (303 to that plugin; an empty value is the list, an unknown name
  lands on the role's home), so a plugin named `go` has no bare `<admin>/plugins/go` shortcut; its `/go/settings` still works. An editor sees only the plugins whose UI admits editors, and no "All plugins".
* **Plugin list.** `<admin>/plugins` (owner) is one line per plugin: name, description, status badge
  (Active, Disabled, Failed, Blocked, Uninstalling) and a "Settings" link. The header has Upload
  package and Browse registry; the Unsigned, Available and Upload cards follow the list.
* **Plugin pages.** `<admin>/plugins/{name}/settings` renders for every installed plugin. The owner gets
  the header (status, "Update to X", Turn on/off) and two link tabs. The **Settings** tab holds the
  plugin's settings form and its admin UI, an `<iframe sandbox="allow-scripts">`; `pluginframe.js`
  relays `postMessage` to `POST <admin>/plugins/{name}/bridge`. The **Permissions** tab
  (`<admin>/plugins/{name}/permissions`) holds the grants, hosts, routes, cron, cookies, functions, the
  signature and Uninstall. An editor may open `/settings` only for a plugin whose UI admits editors and
  sees just the frame. `<admin>/plugins/{name}` redirects (302) to `/settings`, and the old
  `<admin>/plugins/{name}/ui` redirects (301) there. The sandboxed document is served
  outside `<admin>` (`/plugin-ui/{plugin}/{file}`) with its own strict CSP; see PLUGIN_API.md "Admin page".
  The document URL carries a one-time ticket (`?t=`, 60 seconds, bound to the session and the
  file) minted when the page renders (held in the serving process's memory: with more than one replica admin traffic needs sticky sessions); scripts, styles and images stay public. A page that
  navigates itself or reloads in its frame is blanked and shows the "Plugin page disconnected" notice.
* **Theme preview** is `GET <admin>/preview/theme/{name}` (owner or editor), which redirects to the
  public site with a signed token; see THEMES.md.

## Overview and Audit log

`GET <admin>` is the Overview: KPI row, then for owners a Site health card, the pages AI
crawlers, missing pages and recent submissions cards, and an Activity card (layout rules in
`ADMIN_UI_RULES.md`, "Dashboard"). **Activity** merges the newest audit rows with update
history (installs, rollbacks), newest first, and links to the Audit log. If one source
fails the card shows the other; it only errors when both do. Editors see content cards only.

**Audit log** (`GET <admin>/audit`, owner only, read-only, 25 rows a page) lists the
`hatamoto_admin_audit` table: when, who, what and the target. It exists only when the host
wires `Deps.AuditLog`; without it the route is a 404. There is no sidebar link: the way in is the
Activity card's "Audit log" link. Actions are
named by `auditLabel`, falling back to the raw action. Recorded: `entity.publish`,
`entity.unpublish`, `entity.archive`, `entity.update`, `entity.schedule`, `entity.delete`,
`entity.restore_version` (explicit saves and status changes, never autosaves or builder
saves), plus backups, email, cache, spam and update settings actions. Automatic updates
and CLI actions appear under their own actor ("Auto update").

Every shell page ends with a colophon at the bottom right of the content area: the running
version ("Hatamoto v1.4.2", or "dev build" for an unstamped or between-tags build) linking to
hatamoto.dev. Update status lives on Settings > Updates and in the update banner; see
`ADMIN_UI_RULES.md`.

## Settings, users and email

Users and settings are owner-only (editors have `<admin>/profile`). The last owner
cannot be deleted or demoted (enforced in the repository, race-safe). Email is
set up in Settings > Email (`<admin>/settings/email`, with `/test` and `/reset`) or by the
server's `SMTP_*` environment; saved settings replace the environment as a whole, Save checks
connect, TLS, sign-in and MAIL FROM first (10s) and stores nothing on failure, and the password
is sealed with `HATAMOTO_SECRET`. Settings > General only shows a badge. The setup wizard has an
Email step after Your site, with Skip. Without email, invitations
and resets show a one-time link for the owner to pass on, and "forgot password"
points to `hatamoto user reset-password`.

### SEO

Settings > SEO (owner-only) has two tabs. General (`<admin>/settings/seo/general`) lists the
built-in variables (`%title%`, `%sitename%`, `%sep%` and so on) and holds up to 20 custom
variables, used as `%cv_name%`. Titles & meta (`<admin>/settings/seo/titles`) sets the separator,
the homepage, archive, author, date, search and not-found titles, a title and description
template per content type and a title template per term kind. A title or description written on
an entry always wins over a template; with the defaults, pages render exactly as before. The
archive title and description of a type are the same values as in the type's settings. Saving
merges only the keys a tab owns, writes one `seo.settings` audit entry and clears the page
cache. Update preview resolves a sample page of each kind through the same function the public
head uses, without saving.

### Performance

Settings > Performance (`<admin>/settings/performance`, owner only) controls the
in-process page cache without a restart. The page has two cards, Page cache and
Preload, each with a row per setting (name and help on the left, the control on
the right; On or Off is a segmented control):

* **Page cache** on or off. Off bypasses every read and write, clears the
  rendered pages and sitemaps once, and pauses preload; the other controls are
  disabled and keep their stored values. Turning it on again starts cold.
* **Keep pages for** (30 seconds to 30 days; presets 5 minutes, 15, 30 minutes,
  1, 2, 6, 12, 24, 48 hours, 5 days and 30 days). Edits made in this
  admin clear the cache at once. Changes made elsewhere (another app instance,
  the CLI or direct database writes, plugin data that changes without an edit)
  only show after the lifetime ends or after Clear cache, so long lifetimes suit
  mostly static sites with one instance.
  The value saved here wins over `HATAMOTO_PAGE_CACHE_TTL`; the page shows
  whether the lifetime is "set here" or the server default (environment or
  built-in 5 minutes).
* **Max cache size** in MB (1 to 102400, empty means no limit). Over the limit
  the oldest pages are evicted first; a single page larger than the limit is
  not stored. Applied at boot and on save; lowering the limit evicts on the
  next cache write, not at once.
* **Cached right now**: page count and approximate size, with **Clear cache**
  (every page is rebuilt on its next visit; Clear never starts a preload).

The Preload card builds pages ahead of visitors:

* **Preload** on or off (scheduled and after-change runs).
* **Pages to build**: **Only missing** (the default) skips pages that are
  already cached and unexpired, so it does not refresh them; **Refresh all**
  re-renders every page, overwrites the cached copies and keeps plugin data
  fresh.
* **After content changes** (on by default): when an edit, menu or settings
  change empties the cache, a preload starts about 2 minutes after the last
  change (never later than 10 minutes after the first one in a burst), using
  the chosen mode. Clear cache and switching the cache off do not trigger it.
* **Preload every** (15 minutes to 30 days; 15, 30 minutes, 1, 2, 6, 12, 24,
  48 hours, 5 days, 30 days; the page pre-selects the shortest one that is not
  below the page lifetime). The first scheduled run is one interval after
  saving.
* **Preload now** re-renders the sitemap URLs under `PUBLIC_BASE_URL` in the
  background (2 at a time, one run at a time). A run stops early when the size
  limit starts evicting pages. The last result (time, pages, pages already
  cached, errors, duration) is kept in memory only. Failed pages are named
  ("Failed: /a (500) · /b (panic)", up to 10, then "and N more").

The cache lives in the server process; each app instance has its own. Without
`PUBLIC_BASE_URL` nothing is cached and preload is refused. With
`HATAMOTO_WORKER=off` there is no scheduler, so only **Preload now** works.
Saves, clears and manual preloads are written to the admin audit log as
`cache.settings`, `cache.clear` and `cache.preload`.
