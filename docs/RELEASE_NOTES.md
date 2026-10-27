# Release notes

## Unreleased: Builder canvas and inspector, redrawn

The page builder's canvas and inspector are easier to read and to use. On the canvas: a toolbar beside the selected element (Parent, Duplicate, Wrap, Hide on this device, Delete, More), breadcrumbs and Fit, 100, 75 and 50 zoom above the page, a coloured band on Tablet and Phone, round **+** handles between elements (with **Choose an element…** and **Insert a ready-made section…**), **Enter** to edit a selected text and **Esc** to cancel, `Ctrl+K` for the command palette from the page, a shorter format bar on headings, a link panel that lists your pages and anchors, and `Ctrl+Z` to undo the last format while typing. In the inspector: a header with an **Element options** menu, a **Class** row (the word "preset" is gone from the screen), a Style tab that opens with the State switch and a few Essentials per element, source labels with one **Reset** per control, **Show on** switches for Desktop, Tablet and Phone, padding nested in margin, colours with site names, **None** and recent colours, a reordered Content tab, **Number of columns**, an **Items** panel for Tabs, Accordion and Gallery, and **Restore from an earlier version…** for a single element (new read-only route `GET /entities/{id}/builder/node-versions`).

No migration; the stored document and its keys are unchanged, and nothing new is stored. **Rollback floor: none.**

## Unreleased: Public fingerprint scrub and opaque-origin page scripts

Public responses no longer say what serves them. **Everyone is signed out once after this upgrade**: the session cookie is renamed (`__Host-s` over HTTPS, `s` on plain-HTTP dev), so each person signs in again once. The old `hatamoto_session` cookie is ignored. The admin sets no CSRF cookie; the only cookie like it is the JSON API session cookie, sent at login when `ENABLE_COOKIE_AUTH=true`. It is now its own session kind named `__Host-a` (or `a`), and the admin cookie no longer authenticates `/api/v1`. People who use the API with a cookie sign in again once; the login response carries the `csrf_token` to send in `X-CSRF-Token`.

- `/health` and `/ready` answer `ok` (and `503 not ready`) to anyone; the JSON detail needs an admin session, an `X-API-Key` or a Bearer token. Status codes are unchanged, so container and load balancer checks, `hatamoto healthcheck` and `scripts/ship.sh` keep working.
- `X-Hatamoto-Renderer` is gone, and the shipped Caddy config strips `Server`, including on Caddy's own error pages. Check a deployed site with `deploy/test-caddy-headers.sh`. A Caddy file you copied earlier needs `header -Server` added by hand.
- New `PUBLIC_API_PREFIX` renames the public forms and page-view endpoints (at least two segments, for example `/x/v1`); the default `/api/v1/public` stays mounted. The theme script follows the prefix. See the [configuration reference](site/operate/configuration-reference.md).
- Plugins are now classed first-party, community or self-built. A community plugin is refused the `site.scripts` grant, and a stored grant is ignored at render. A page that carries a live community script is served in a CSP sandbox (opaque origin), so that script cannot read cookies or call the admin API; the page's own forms and page-view beacon still work (they are answered for `Origin: null` with a page token). No community package can be granted `site.scripts` today.
- Browser acceptance for the sandbox is `scripts/admin-qa/opaque-origin.mjs`, which needs the test-only `hatamoto_qa` build tag (never in a release binary; see `scripts/admin-qa/README.md`).
- `PUBLIC_API_PREFIX` adds a path; it does not hide the default. `/api/v1/public` stays mounted, so a scanner still finds it.
- A CDN or cache rule keyed on the `hatamoto_session` cookie name must be updated to `__Host-s` (or `s` on plain HTTP).
- A browser that still holds a pre-upgrade `hatamoto_session` cookie for a live admin session has that session revoked server-side and both legacy cookies (`hatamoto_session`, `hatamoto_csrf`) expired the first time it visits after the upgrade. The legacy cookie never signs anyone in. Any other legacy value, or a `hatamoto_csrf` alone, gets no response cookie, so the product name is not sent to clients that do not hold a live legacy session. Until that first visit the old session stays valid, up to the 7-day idle limit (or 30 days of age).
- `builder.island` is refused for community packages (grant and render; an island of a community package is dropped even with a stored grant), for the same reason as `site.scripts`: an island is a same-origin script.
- Themes are trusted and outside this boundary: a theme controls every public template and its JavaScript, so install community themes only from sources you trust.
- A no-JS form post from a sandboxed page without a valid token is answered with the usual redirect and `form_error=1` (nothing is stored); the JSON endpoint still answers 403.
- No migration; pages and stored content are unchanged.

**Rollback floor: unchanged.** An older binary reads the database as before. Sessions created under the new cookie names are not recognised by an older binary (it reads `hatamoto_session`), so everyone signs in once more after a rollback. A legacy session is revoked the first time its browser visits after the upgrade, so a rollback does not sign that browser back in; one that has not visited since the upgrade still holds a valid legacy session, and a rollback inside the idle window signs it back in. An older binary answers JSON on `/health` and `/ready`, which `scripts/ship.sh` accepts.

## Unreleased: Recovery copies live on the server

Unsaved work on a published post or page is now kept on the server, one copy per user and entity, instead of in the browser. It is written by four POST routes (`/entities/{id}/edit/recovery`, `/builder/recovery` and a `/discard` twin of each) and delivered inside the editor page. A copy survives sign-out and follows the user to another device. It is deleted by the user's own save, publish or update, by Discard, after 30 days (prune job), and with the entity or user. Drafts rely on draft autosave. Copies that older versions kept in `localStorage` are removed when the admin loads and are never restored, so a copy that was only in a browser is lost at the upgrade.

## Unreleased: MCP builder tools

An MCP agent can now read the builder's vocabulary and build or edit a page by ops, with the same checks as the admin builder. New tools: `builder_kinds`, `builder_stylespec`, `builder_design`, `builder_get`, `builder_validate`, `builder_render`, `builder_templates_list`, `builder_template_nodes` (all `read:entities`) and `builder_apply_ops` (`write:entities`). `builder_apply_ops` takes the `version` `builder_get` returned: a stale version is refused with `conflict: ... current version is N`, and a batch with one bad op stores nothing. It runs the validator, the element-setting rules, the slot and block-reference rules and the sanitiser, stores one version ("Edited by <key> via MCP") and can publish. `wrap`, `unwrap` and `duplicate` are available beside the editor's six verbs; omitted node ids are generated and returned. Limits: 500 ops and 1 MiB of ops per call, 2 MiB of rendered output. See [MCP.md](MCP.md#builder-tools) for the tools and an example session that the test suite replays.

- **Fix:** `entities_update` with a `page_doc` now meets the builder's reuse rules too. Before, a `slot` node on a page or a block that embeds itself was stored through MCP although the admin builder refused it; it is now refused with `invalid page_doc: ...`. A client that wrote such a doc through MCP fails from this release (the REST API does not check these two rules yet).
- The admin builder is unchanged: its save, messages and boot data are the same bytes. The shared rules moved into `internal/entity`; the kinds boot JSON and the template node reader moved into `internal/builder`. No migration, no new dependency.
- Known limits: the version check and the write are not one atomic step (two writers in the same few milliseconds can both pass, as in the admin builder); pages that still have only the legacy `page_sections` layout, posts and people are refused by the builder tools (`not_builder:`); only tree templates are offered.

## Unreleased: Element settings in the builder

Buttons, images, containers and galleries get the settings Elementor users expect. **Anchor** gives an element a public `id` (link to it with `#anchor`; the Link field suggests your pages and the page's anchors). **Accessible name** and **Tooltip** set `aria-label` and `title`. Links have **Open in new tab** (always `noopener noreferrer`) and **Nofollow**, **Sponsored** and **User generated content**. Images have a link, a **Caption** (a `figure`) and **Lightbox**, which a gallery can switch on for all its images; a container can be one big link. Duplicate or reserved anchors, a link plus a lightbox on one image, and a linked container with a button, form, menu or other link inside are refused on save with the reason.

- Images from the media library now carry `width` and `height`, so layout is reserved before they load (measured CLS 0 under a throttled cold load; the same page without the attributes shifts 0.41). This is the one change to existing live pages: library images gain the two attributes. Photos that carry an EXIF rotation (phone photos taken sideways) still reserve the unrotated box and shift once.
- A page with a lightbox loads one hashed script, `/builder/lightbox.<sha8>.js` (no inline code; the dialog opens on a plain click, Esc closes, Tab stays inside, arrows move through a gallery, focus returns; without script the image links to the file). Pages without a lightbox load no extra script; `public_builder_js_scripts` stays 0.
- The widget lint now fails a `target=_blank` link without `noopener` and a link inside a link.
- A plugin widget that declares `preset` or `navlabel` is refused at install ("property name reserved") instead of panicking. A new install that declares `anchor`, `aria` or `tip` is refused too; a package already installed keeps working: the builder skips that element setting for the widget, so its own prop is untouched, and logs a warning (`plugin widget declares props the builder now uses`) at startup.
- Fix found on the way: the default theme's hidden light/dark toggle took no space and pushed the header nav 58 px sideways when the script showed it, a layout shift on every page; it now keeps its box while hidden (CSS only).
- Pages that use none of this are byte-identical in output and storage; goldens unchanged except the new `settings` page. No migration.
- Admin JS: the inspector's element settings are one lazy chunk; builder first paint is 85,152 B gz (cap 89,255; target 85,300), total 187,271 B gz (cap 220 KiB). Budgets re-baselined with reason "builder-e6-element-settings".

**Rollback floor: this release, for pages that use any element setting.** An older binary rejects the new props: the public page renders an empty body and the admin shows the page as broken (saves refused with 422 `invalid_page_doc`). Before a rollback, remove `anchor`, `aria` and `tip` (any kind), `target` and `rel`, `href` on a container or image, `caption` and `lightbox` from every page, block, part and saved version that must keep rendering (BUILDER.md, "Rollback floor for element settings").

Deferred: custom `data-*` attributes and `lang`, art-direction image sources, lightbox for video, links on list items and icons, popups, anchors on theme sections and blocks, a smooth-scroll script (use `scroll-behavior` in the theme CSS).

## Unreleased: Inline rich text in the builder

Headings and text can be formatted where they sit on the page. Double-click one: a bar offers bold, italic, underline, strike, code, link (with an Open in new tab option) and Clear; Ctrl/Cmd+B, I, U and K work too, Enter finishes, Shift+Enter adds a line break in a text, Esc restores. Pasted text keeps only those marks. The inspector's Text field for a heading or text is a small editor with the same marks, in step with the canvas, usable by keyboard and at phone width.

- Storage: formatted text is the new `html` prop of `heading` and `text` (at most 20,000 characters); the plain text stays in `text`. It is sanitised on save and again at render: `strong em b i u s code br a` only, `javascript:`, `data:` and `//host` links dropped, `rel="noopener noreferrer"` on every new-tab link. A bound text wins over `html`. The version diff shows tag-stripped text with a "formatting changed" note.
- Typing sends no `patch` to the preview, so the caret does not move; keystroke handling measured at p95 under 2 ms on a 60-node page.
- Fixes found on the way: clicking a theme component's field on the v2 canvas threw an error and did nothing; it now selects the component and focuses the field in the inspector.
- Pages without formatted text are byte-identical in output and storage; goldens unchanged. No migration.
- Admin JS: the bar, link dialog, inspector editor and component field jump are lazy chunks; builder first paint is 85,617 B gz (re-baselined from the 85,000 B target by the review fixes). The canvas bridge is 14,078 B gz.

**Rollback floor: this release, for pages with formatted heading or text.** A node that carries `html` cannot be read by an older binary: the public page renders an empty body and the admin shows the page as broken. Before a rollback, remove `html` on pages that must keep rendering: press Clear in the bar (a text with a line break keeps `html` until the break is deleted) or send the page_doc through the API with `p.html` removed; the no-JS outline cannot remove it.

## Unreleased: Builder direct manipulation

Work on the page itself. A right-click on the canvas or a structure row, Shift+F10 or the menu key opens an element menu (copy, cut, paste after or inside, duplicate, rename, wrap, unwrap, copy and paste style, delete). Copy and paste go through the system clipboard, so an element copied on one page pastes into another; the server validates and re-ids what is pasted (`POST /admin/entities/{id}/builder/paste`, writes nothing). Ctrl or Cmd plus click and Shift plus click select several elements for delete, duplicate, hide, wrap and copy in one undo step. F2 or a double-click renames a row (shown in the structure and canvas tags, never on the public page). The **Add section here** bars open a picker of layouts (1 to 4 columns, 33/67, 67/33, 2×2 grid). The selected element has a move handle on the canvas, and **History** (Ctrl+H) jumps to any earlier step of the session. See [builder-reference](site/admin/builder-reference.md) and [BUILDER.md](BUILDER.md#direct-manipulation-builder-e4).

- Fixes found on the way: empty layout cells are now visible and clickable in the editor; the first native element added to a page drawn from v1 sections re-renders the frame so it gets its layout CSS; the move handle on a v1 section moves the whole section and works when pressed before the page has finished loading; the add-section button's accessible name now contains its visible text.
- Pages without a renamed element are byte-identical in output and storage; goldens unchanged. No migration.
- Admin JS: the menu, clipboard ops, layout picker, history panel, drag and banners are lazy chunks, so builder first paint stays at 82,725 B gz (cap 85,000 B) and the total is 170,376 B gz (cap 220 KiB). Budgets re-baselined with reason "builder-e4"; no cap raised.

**Rollback floor: this release, for pages with a renamed element.** A node that carries the new `navlabel` prop cannot be read by an older binary: the public page renders an empty body and the admin shows the page as a broken doc (saves refused with 422 `invalid_page_doc`). Nothing is rewritten by the upgrade; clear the names (Rename, empty value) on pages that must keep rendering before a rollback. Everything else here is client-side or writes nothing.

Deferred: dragging several elements at once, editing the styles of several elements together, and history that survives a reload.

## Unreleased: Builder inspector controls

The Style tab's fields are now real controls: lengths have a number field, a unit menu and a slider; colours are a swatch with a palette popover (site colours, custom colour, hex); Spacing is a box model with a link toggle per ring; every row shows a set-here dot, a source label (Local, Preset, Inherited) and a Reset; groups are collapsible and show a dot when something inside is set. A slider drag is one undo step. Keyboard: Tab wraps inside the colour popover and Esc returns to the swatch. See [builder-reference](site/admin/builder-reference.md) and [BUILDER.md](BUILDER.md#style-controls).

- Style keys, validation, the document and the compiled CSS are unchanged; goldens are byte-identical. No migration.
- Admin JS: the reuse UI, shortcut sheet, component fields and preset panel moved to lazy chunks, so builder first paint is smaller (81,658 B gz, cap 89,255 B) while the total grows with the new controls (155,644 B gz, cap 220 KiB).

**Rollback floor: unchanged.** No document-model change; an older binary reads every page this release saves.

## Unreleased: Builder states (hover, focus, pressed, parent hover)

The Style tab gets a State switch: Normal, Hover, Focus, Pressed and, for an element with a parent, Parent hover. Every state has its own values per device, shown on the canvas while selected (editor only). New style keys: Transform (`tx ty sc rot skx sky torigin`), Filter (`blur bright contrast sat gray hue bblur blend`) and Transition (`trans tdur tease tdelay`). Transitions apply only for visitors who have not asked for reduced motion. A hover-lift card is Shadow Small, Transition Transform and shadow, and on Hover Move Y `-4px` and Shadow Large.

- Documents that use none of the new keys compile to byte-identical CSS. No migration.
- Cascade: a Hover value set on Desktop still applies on a phone while hovering (set the Phone Hover value to change it); a preset's Hover beats the element's own Normal value; Parent hover beats the element's own Hover. See [BUILDER.md](BUILDER.md#states).
- The style presets page lists only Normal keys and notes a preset's state styles; saving it keeps them.
- Versions diff names states ("style Shadow (Hover, Tablet)").
- Budgets re-baselined with reason "builder-e2-states" (see the budget history); no cap raised.

**Rollback floor: this release.** A page, section template or style preset with a state key (`base:hover`...) or a transform, filter or transition key cannot be read by an older binary: it rejects the key (`unknown breakpoint` for a state key, verified against the previous parse path) and the page renders an empty body publicly and is refused with 422 `invalid_page_doc` in the admin, as in the Style depth rollback note below. Nothing is rewritten by the upgrade; remove the new keys from pages that must keep rendering before a rollback.

## Unreleased: Builder style depth (StyleSpec v2)

The builder's Style tab goes from 27 to 77 keys: margins with `auto` and negative values, size limits, aspect ratio and image fit, `position` including `sticky` with offsets and stack order, typography (font, weight, line height, letter spacing, case, decoration), borders (width, per side, style, colour, per-corner radius), a background image and gradient, opacity, `display`, grid placement and gaps. Raw CSS is still never accepted: every key is a token, a listed option or a bounded pattern, and the same validation applies over REST, MCP and the admin (`GET /admin/builder/stylespec` lists them).

- Show on one device: `hide` plus `display` on a narrower breakpoint. Phone-only is hide at Desktop and show at Phone.
- Documents that use none of the new keys compile to byte-identical CSS. No migration; nothing changes until a page uses a new key.
- The style presets page now lists only the keys a preset sets, with an Add style row.
- Admin JS: first paint +381 B gz (cap unchanged), total +1955 B (the labels are in the lazily loaded Style chunk). Budgets re-baselined with reason "builder-e1-style-depth". New metric `page_css_allstylekeys_gzip` (cap 8 KiB).

**Rollback floor: this release.** A page, section template or style preset that uses a new key, or a new value of an old key (`ta: justify`, `dir: row-reverse|column-reverse`), cannot be read by an older binary. The older binary validates styles whenever it parses a doc: the public page renders an empty body (and logs it), the admin shows the page as a broken doc and refuses saves with 422 `invalid_page_doc`, and a preset loses only its entries with new keys (a preset with only new keys is ignored but stays stored). Nothing is rewritten by the upgrade, so going forward again restores the pages, but saving the Style presets page on the older binary permanently deletes the new keys from presets that mix old and new keys. Remove the new keys from a page before rolling back if it must keep rendering. See [BUILDER.md](BUILDER.md#stylespec).

## Unreleased: AI analytics in the admin sub-sidebar

AI analytics is now five sections (Overview, Crawlers, Content, Referrals, Health) plus a page per agent, in the admin sub-sidebar like Settings and Theme. Old `?tab=` and `/pageviews` links redirect. The date picker can compare with the previous period. Chart.js is no longer shipped: the charts are drawn by the admin's own SVG modules, which makes the admin download about 208 KB smaller.

## Unreleased: Default theme layout options

- Hero: optional `stripLabel` and `stripItems[]` show a labelled row of short items between the buttons and the media. Nothing changes for heroes that do not set them.
- Split: optional `panelHeight: match` stretches the code panel to the height of the text column above 960px. The default (`auto`) is unchanged.
- Process steps: `layout: cards` now stays inside the container width on every site. Before, the cards ran wider than the rest of the page. Below about 1248px the only difference is the side padding.
- Admin: the items of a tag strip (and the hero strip) are now edited one per line in a textarea, so an item can contain a comma. Before, the comma separated field split `htmx admin, no Node` into two items on save. Existing content is unchanged. A theme can use this for its own string arrays with `"x-hm-field": "lines"`.
- The strip label is now linked to its list (`aria-labelledby`) in the hero and the tag strip.

## Unreleased: Pages published from the CLI show up without a restart

A page or menu written by another process (`hatamoto entity create --publish`, an import, the scheduled-publish worker, another replica) now clears the running server's page cache. Previously the server kept serving the old cached pages (for example a header without the new page) until the cache entry expired.

How it works: a migration adds `hatamoto_content_changes`, an append-only log. Triggers on `hatamoto_entities`, `hatamoto_menus` and `hatamoto_menu_items` add one row per writing transaction, only when a statement changed rows. Each server polls the log every 3 seconds (every second when `ENVIRONMENT` is not `production`) and clears its page cache when a new committed row appeared. An isolated change shows within about one poll interval (3 seconds). While changes keep arriving (an import) it clears at most once per five polls, so a change can take up to about 15 seconds to show, and once more when they stop. There is no setting; nothing to run after the update.

- Cost: one insert per writing transaction (no row lock shared between writers; rows older than a day are deleted) and one small read per server every few seconds. A transaction that stays open longer than a day and then commits is not noticed.
- A server that has not had the migration applied logs one warning and does not poll.
- Not covered: redirects (read live, as before), settings, terms, authors, media, forms, schemas, themes and plugins changed in another process. With several replicas those still reach the other replicas when the cache entry expires (`HATAMOTO_PAGE_CACHE_TTL`).
- The cache lifetime and cache headers are unchanged.

## Unreleased: Theme directory section

A new core section type, "Theme directory", shows the themes of your signed registry
(`HATAMOTO_REGISTRY_URL`) as cards with a screenshot, inside the normal site theme.
It is served at `/themes` from the same cached registry read as the plugin directory,
with the same unavailable message when the registry cannot be reached.

Themes are submitted by pull request: add `manifests/themes/<name>.json` with
`archive_grant: true`, an allowed license, and a source that is either a repository on
any https git host (pinned to a 40 character commit) or an https archive pinned by
`sha256`. A screenshot (`"screenshot": "assets/<file>"` in `theme.json`) is required
for registry themes. Authors check a theme with `hatamoto theme check ./mytheme --registry`
and repack and sign a pinned author zip with `hatamoto theme pack --archive F --sha256 H`
(`theme pack` reads the zip; it does not build one).

## Unreleased: Plugin scripts need the site.scripts permission

Plugins' browser JavaScript (`assets/plugin.js`, `render.head` script tags) and plugin sections/partials with scripts now need the `site.scripts` permission. After updating, a plugin that ships a script stops running it until its author lists `site.scripts` and you grant it under Plugins > Permissions. Nothing is granted automatically. Stylesheets are unaffected. The permission is part of SDK 0.12.0 (Hatamoto v0.31.0). Hosts from v0.30.0 already enforce it, whatever `sdk` range the plugin declares; this is a behaviour change that should have been announced with a notice, see [STABILITY.md](../STABILITY.md).

## Unreleased: Security hardening for self-hosters

- **Trusted proxies default to loopback only.** `X-Forwarded-*` headers are believed only from `127.0.0.0/8` and `::1`; private ranges are no longer trusted. A proxy on another address, and every Docker deployment (a host proxy reaches the container from the bridge gateway), must set `HATAMOTO_TRUSTED_PROXIES`. Until then every visitor looks like one client and the log shows a one-time warning. See [Trusted proxies](site/operate/configuration-reference.md#trusted-proxies).
- **`HATAMOTO_AGENT_RANGES=off`** stops the daily download of AI-crawler IP ranges. Crawler hits are then verified from stored ranges and reverse DNS. See [Outbound connections](site/operate/outbound-connections.md).
- **Setup code rule.** `HATAMOTO_SETUP_CODE` must be at least 12 characters and not a trivially repeated pattern. A fresh install with a weaker code refuses to start; an install that already has users only logs a warning.
- **CSRF token never in a URL.** Multipart admin forms send it in the `X-CSRF-Token` header or as the first form field; a token in the query string is never accepted.
- **Archive import and `/media`.** An imported file must match the media allowlist (files that do not are skipped with a warning), and `/media` serves types outside the allowlist as downloads in a sandbox.

## Unreleased: Plugin versions and updates in Admin > Plugins

Every plugin in the list now shows its version. When the registry has a newer one
the row gets **Update to vX**, one click, and your granted permissions stay as they
are. An update that adds permissions shows **Review update to vX** and the names it
adds, and opens the usual permission prompt. **Update all (N)** at the top updates
every plugin that needs no review, one after the other, skips the ones that do
(it names them), and stops at the first error. An update that asks for more
reach (other signer, new allowed hosts, any-address fetching, site scripts, an
older version) is never applied without your confirmation, from any button. With no
registry set up, or one that cannot be reached, the list shows no update controls.

## Unreleased: Plugin directory section

A new core section type, "Plugin directory", shows the plugins of your signed
registry (`HATAMOTO_REGISTRY_URL`) as cards inside the normal site theme: header,
footer, light and dark. Add it to any page in the builder. The registry is read on
the server with the same trust and checks as Admin > Plugins > Browse registry and
cached for 3 minutes; pages with the section are cached for at most 60 seconds, so a
publish shows within about 5 minutes. If the registry is unreachable or expired the
section shows a short unavailable message and the page still answers 200.

`hatamoto registry page` stays for operators without a CMS page, and
`make registry` builds the static page only with `REGISTRY_PAGE=1`.
`scripts/publish-registry.sh` uploads `registry/plugins/` only when it exists.
The Caddy example no longer serves `/plugins` statically: it is proxied to the app.

## Unreleased: one page model (breaking changes)

`attributes.page_doc` is now the only page body model. The v1 `page_sections` list is a frozen, read-only legacy key.
No migration; public HTML and the public pages JSON are unchanged.

* **API and MCP: 422 `page_sections_removed`.** Sending a *changed* `page_sections` for a page that has a `page_doc`
  is refused (REST `422 {"code":"page_sections_removed"}`, MCP the same sentence). Send `attributes.page_doc`. Sending
  the stored or derived `page_sections` back (read-modify-write) is accepted and writes nothing. The former 409
  `builder_layout` is gone. `page_doc` together with a different `page_sections` stores the doc and ignores the sections.
* **Convert once.** A create or update that carries `page_sections` for a page without a doc converts it to a
  `page_doc` (a deprecation warning is logged). This window ends in a later release; move integrations to `page_doc`.
* **Nothing rewrites `page_sections`.** The stored key is carried forward unchanged. The public pages JSON, plugin
  host entities and `before_save` hook input show `page_sections` derived from `page_doc` at read time. A hook that
  changes `page_sections` has the change discarded (with a warning); changes to `page_doc` are kept.
* **The classic section editor is removed.** Every page opens the builder; a page that only has `page_sections` opens
  on its in-memory conversion and is stored on first save. The `POST /admin/entities/{id}/edit` section form, its
  preview variants and `pageeditor.js` are gone. A damaged `page_doc` shows a recovery screen.
* **Section ids are not repaired.** Creating or updating a page with `page_sections` whose ids are duplicate or contain
  anything but letters, digits, `-` and `_` (max 64) now fails with 422 `invalid_page_doc` instead of being stored as
  is; send a `page_doc`. A stored page like that opens a recovery screen in the admin (`hatamoto builder convert
  --entity ID` names the problem) and is never blanked.
* **CLI.** `hatamoto builder convert --revert` is removed (exits 1). `serve` logs how many pages still lack a
  `page_doc`; run `hatamoto builder convert --dry-run --verify-render`, then `hatamoto builder convert`.
* **Seeds.** The setup wizard's home page and the starter pages are stored as `page_doc`.
* **Rollback floor: v0.27.0.** An older binary cannot read `page_doc`. Because `page_sections` is no longer updated,
  a rollback binary shows the last mirror, which can be older than the doc. Convert (`builder convert`) before relying
  on the new behaviour, and keep a backup.

## Unreleased: Webhooks are built in

Outgoing webhooks are a core feature again, reversing the webhooks half of
v1-19. Search Console stays a plugin. Receiver contract and setup:
`docs/WEBHOOKS.md`.

**What you get**

- Settings > Webhooks (owner only) to add, pause, test and delete webhooks, replace
  a secret and read the delivery log. No plugin to install or grant permissions to.
- Delivery can be faster: the job runs every 10 seconds, where the plugin's cron
  ran every minute. Body, headers, signatures and secrets are unchanged for
  receivers; delivery is still at least once.

**Upgrade steps**

1. Run the migration with `hatamoto migrate` (`202610251000_webhooks_core`). It
   creates the core tables, copies subscriptions still waiting in
   `hatamoto_plugin_handover` and deletes those rows. The table stays: v0.27.0
   still deletes from it when a plugin is uninstalled.
2. On the first start the server copies the subscriptions (ids and secrets
   included) of an installed webhooks plugin into core, then uninstalls the plugin
   with its data. If the copy fails the plugin is kept and the copy retried at
   the next start. Pending deliveries and old delivery history are not copied.
3. Rolling deploy: stop the old instances first. An event handled by an old
   instance while the new one retires the plugin may be lost: its queued
   plugin deliveries are dropped with the plugin's schema, and once the old
   instance unloads the plugin nothing listens there. Queued plugin
   deliveries are never carried over.
4. SDK surface change: the `handover.list` and `handover.ack` host functions are
   removed, and with them the exported constants `sdk.FnHandoverList` and
   `sdk.FnHandoverAck` in `hatamoto.dev/sdk`; an out-of-tree plugin that
   references them stops compiling. Only the old webhooks plugin used them.
5. Subscriptions are imported **paused** when the plugin was not really sending
   (disabled, signer no longer trusted, or not granted all of `db`, `http.fetch`
   and `http.fetch.any`), and for rows from the old
   hand-over table unless they were created within the hour before the migration
   (a direct jump from the pre-plugin core keeps them live). Resume them in
   Settings > Webhooks.
6. Only a first-party `webhooks` plugin is moved. One that is unsigned,
   third-party or signed by your own registry key stays installed and
   untouched; Settings > Webhooks says so. Recreate its subscriptions there,
   then uninstall the plugin (the name is reserved for installs only).
7. `Down` refuses to roll back while any webhook exists. A binary-only rollback
   to v0.27 stops all webhooks: subscriptions now live in core and the plugin is
   gone. Reinstalling the plugin gives an empty one; re-create the subscriptions.

**Pending contract:** the next minor drops `hatamoto_plugin_handover`
(`-- hatamoto:contract unused-since v0.27`). Nothing in this release reads it.

## Unreleased: Plugin directory

**Upgrade steps**

None. The registry index stays at version 1; the new fields are optional and
older servers ignore them.

**What changed**

- `hatamoto registry index` can add optional `title`, `author`, `source`, `commit` and
  `tier` to each entry (`--meta F`, a JSON file keyed by package name) and refuses
  duplicate names, non-https sources and commits that are not 40 hex characters.
- New `hatamoto registry page <registry.json> [--out F]` renders the signed index
  as a static, self-contained plugin directory page. It refuses an unsigned,
  altered, expired or untrusted index.
- `make registry COMMUNITY_DIR=...` also builds approved community plugins from
  pinned commits, and writes `registry/plugins/index.html`. See
  `docs/PLUGINS_WRITE_AND_SUBMIT.md` and `registry/README.md`. Registry
  operators: a plain `make registry` now refuses to run unless `COMMUNITY_DIR`
  or `FIRST_PARTY_ONLY=1` is set, so a publish cannot silently drop the
  community plugins.

## Unreleased: People replace authors

**Upgrade steps**

1. Three migrations run with `hatamoto migrate` (`202610231000`, `202610231005`,
   `202610231010`). They add columns and a table, make a person for every author
   and re-point the posts. Nothing is dropped: `hatamoto_authors` and
   `author_id` stay, so rolling back to the previous binary keeps every byline
   (`docs/UPGRADES.md`, People).
2. A site with its own content type slugged `person` must rename it first; the
   backfill refuses to run against it and says so.
3. Each user is sent once to "Complete your profile" (name and photo) at the next
   sign-in.

**What changed**

* Bylines, feeds, JSON-LD (`Person`, Article `author`), the author box and the
  author page come from people. A person is hidden everywhere public until they
  switch **Public profile** on. The site organisation can be credited instead.
* **People** screen under Entities: add, search, filter by team, merge duplicates.
* The built-in `person` type can gain fields and have its address, archive title
  and breadcrumb label changed in Settings > Entity types; its core fields are
  locked. Changing a type's address pattern writes 301s from the old paths.
* Themes: `.Entity.Author` has `.JobTitle`, `.IsOrg`, `.SameAs`; the person page
  is the `author` layout; `query` can list the team with `attr on_team`.
* REST `PATCH /api/v1/entities/{id}` and MCP `entities_update` set the byline
  (`byline_person_id`, `byline_org`).
* WordPress import creates people (not public); export/import carries
  `hatamoto_user_people`, and old archives get people on import.
* `/api/v1/authors` is deprecated and only edits the rollback mirror.

## Unreleased: Page cache controls in the admin

**Upgrade steps**

1. Migration `202610211000_page_cache_settings` runs with `hatamoto migrate`. It
   adds five columns to `hatamoto_site_config`. Defaults keep today's behaviour:
   cache on, lifetime from `HATAMOTO_PAGE_CACHE_TTL` (or 5 minutes), no size limit,
   preload off.

**What changed**

- New owner-only Settings > Performance screen: page cache on or off, lifetime, max
  size in MB, Clear cache, Preload now and a scheduled preload, with live page count
  and approximate size. Changes apply immediately. See `docs/ADMIN.md`.
- A lifetime saved in the admin wins over `HATAMOTO_PAGE_CACHE_TTL`.
- Saves, clears and manual preloads are recorded in the admin audit log.
- With `HATAMOTO_WORKER=off` scheduled preload is unavailable; Preload now works.

## v1-19: Search Console and webhooks became plugins

**Upgrade steps**

1. Migration `202610201100_webhooks_to_plugin` runs with the usual `hatamoto
   migrate`. It moves your webhook subscriptions (secrets included) to the core-only
   table `hatamoto_plugin_handover` and drops `hatamoto_webhooks` and
   `hatamoto_webhook_deliveries`. API keys lose the `read:webhooks` /
   `write:webhooks` scopes. Delivery history is not carried over.
2. Install the **webhooks** plugin (Admin > Settings > Plugins; it is in the default
   registry, signed by the built-in first-party key) and grant its permissions,
   including `http.fetch.any`. The plugin imports the subscriptions on its first
   cron run (within a minute). Until it is installed nothing is delivered, and
   events before the import are not delivered to the old subscriptions.
3. Install **searchconsole** the same way if you used Search Console, enter the
   Google client ID and secret in its settings and connect again (tokens were not
   migrated).
4. The migration's `Down` refuses to run while subscriptions are still waiting in
   `hatamoto_plugin_handover`; installing the plugin empties the table.

**For webhook receivers** (details in `examples/plugins/webhooks/README.md`)

- `X-Webhook-Signature` is unchanged, so existing verification keeps working.
  New: `X-Webhook-Signature-V2` (`t=<unix>,v1=<hex>`, HMAC of `<unix>.<body>`) for
  replay protection.
- `tenant_id` is gone from the payload; `X-Webhook-Timestamp` is now the attempt
  time (unsigned), not the scheduled time.
- Custom headers cannot override `Content-Type`, `User-Agent` or hop-by-hop headers.
- New subscriptions need `https://` (plain `http://` is a per-subscription opt-in).
- Delivery happens within a minute of the event (cron), requests time out after at
  most 5 seconds, and delivery is at-least-once: de-duplicate by `X-Webhook-Delivery`.

**For operators and plugin authors**

- New permission `http.fetch.any` for `allowed_hosts: ["*"]`; `*` only reaches ports
  80 and 443 (list `*:<port>` for others). A plugin update that adds hosts or
  `http.fetch.any` needs confirmation and clears its grants, like a signer change.
  Install and update audit entries record `allowed_hosts`.
- `http.fetch` is capped per plugin (default 600 requests a minute,
  `HATAMOTO_PLUGIN_FETCH_PER_MINUTE`).
- `HATAMOTO_TRUST_FIRST_PARTY=false` drops the built-in first-party signing key.
- `hatamoto registry index --valid-days` accepts 1 to 180 only.
- Write-only plugin settings render as password inputs that never show the stored
  value; a "clear" box (or `null` in the JSON API) removes it.
- New host functions `handover.list` / `handover.ack` (permission `kv`).
- Search Console's token encryption protects the plugin's schema, not a full
  database dump or backup (the key material lives in the same database).

## v1-15: Next admin removed (breaking API changes)

* **`POST /api/v1/auth/login`** no longer returns `tokens` (`access_token`,
  `refresh_token`). The body is `{ user, session_token, tenants }`;
  `session_token` is an opaque server-side session (30 days absolute, 7 days
  idle) sent as `Authorization: Bearer <session_token>`. With
  `ENABLE_COOKIE_AUTH=true` the HttpOnly session cookie and the CSRF cookie are
  set as well. JWTs are no longer accepted anywhere.
* **Removed**: `POST /api/v1/auth/refresh` and `POST /api/v1/auth/register`.
  Create users with `hatamoto user create` or the admin; use API keys (`nxk_` prefixed, scoped) for programmatic access.
* **`POST /api/v1/auth/logout`** revokes every session the request presents
  (bearer and cookie).
* **No CORS**: the JSON API is same-origin only. Cross-origin integrations call
  it from a server with an API key. `CORS_ALLOWED_ORIGINS` is ignored.
* **`JWT_SECRET`** is now documented as the server secret and keeps working;
  `HATAMOTO_SECRET` is the preferred name and wins when both are set. The
  derived keys are unchanged for the same value.
* **Deploy**: one Go-only image and one process (`hatamoto-api`); `hatamoto-web`,
  `frontend/` and Node are gone. The image has a `HEALTHCHECK`
  (`hatamoto healthcheck`).
