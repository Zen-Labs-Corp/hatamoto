# Admin UI rules

The admin looks the same on every screen because every screen is built from the
same partials. These rules say which partial to use and which values are fixed.
They are enforced, not advisory:

This page writes the admin address as `<admin>`. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address (`hatamoto admin-url`).

- `make design-check` runs every static design check in a few seconds, with no
  database: the templates, JS strings and CSS (rule ids UI-01 to UI-11), the
  component contract in `components.json` (DC-01 to DC-04), flash copy, literal
  colours, tokens and contrast, retired primitives, classes without a rule, and the
  single-purpose CSS guards. The checks are the tests of
  `backend/internal/admin/designcheck` plus `backend/internal/admin/a11y`, so a new
  static check goes in that package and runs everywhere with no list to update;
  `TestDesignChecksLiveHere` fails for a test in `internal/admin` that reads admin CSS.
  It runs after each edit (`scripts/check-changed.sh`, the pipeline `todo` gate), in
  `make check`, at push (the affected Go tests) and in the ship gate.
- `scripts/admin-qa/consistency.mjs` loads every admin screen in Chrome, light and
  dark, at 1440 and 390 pixels wide, and checks computed styles (rule ids UI-D1 to
  UI-D14). `scripts/admin-qa/run-local.sh consistency.mjs` boots a seeded instance and
  runs it, strict by default (`QA_STRICT=1`): a screen without seed data, an unknown
  screen name or a run that scans nothing fails. `consistency.mjs --self-test` plants
  one violation per UI-D rule in a static page and fails unless every rule fires; it
  runs before the real check in `run-local.sh`.
- `scripts/admin-qa/crawl.mjs` walks the sidebar, sub navs and tab rows
  (owner and editor): `crawlFrom` collects links on every page it scans, with no
  depth limit, only a runaway cap of `MAX_PAGES` (200). It fails on any `<admin>` page that is not in `urls.mjs` or its
  `crawlIgnore` list, so a new page cannot escape the checks above.
- Standalone dev checks, not part of the gate: `mobile-table.mjs` (390px table
  cards), `busy-guard.mjs` (plain POST lock and its 10 s release, multipart
  uploads never released) and `select-manual.mjs` (styled Select by keyboard and
  mouse). Run them through `run-local.sh <script>`.
- The seeded database: `run-local.sh` creates a throwaway cluster (needs `PG_BIN` or
  Postgres on `PATH`). It refuses an ambient `DATABASE_URL`; set
  `QA_USE_DATABASE_URL=1` to allow a local one. A non-local host is always
  refused, because seeding adds an owner with a public password.
- Shipping: the local git hooks (`make hooks`, CONTRIBUTING.md "Local gates") run `make check` (with `make design-check`) and the affected tests before every push. `scripts/ship.sh` picks
  its gate from the workflow state (`gh workflow list --all --json name,state`).
  `ci` active: ship waits for the PR checks; every one must be green, and `backend` and
  `admin-ui` must be SUCCESS (no local re-run). `ci` not
  active: ship runs the local UI gate on the PR head commit in a temporary worktree
  (`make design-check`, then `scripts/admin-qa/run-local.sh`,
  strict) and aborts on failure. There it never skips: missing Postgres or Chrome
  abort the ship, and there is no override. `--dry-run` prints
  `DRY: would run local UI gate on <sha>` instead of running it. The merge is pinned
  to the checked head (`--match-head-commit`), so a push during the gate is refused.
  The local gate executes the PR's own code (tests, `qa-env.sh`, `run-local.sh`,
  `npm ci`) with your credentials: do not ship fork PRs without reading them.
- Not inventoried in Chrome: Import options (needs a finished WordPress scan) and
  plugin settings (needs an installed plugin); the seed cannot create either, so
  only the static lint covers them.

The CSS in `backend/web/admin/css/` is the source of truth; values in older design
handoff material are stale, do not copy them.

## House rules

1. **Card footer is right-aligned.** Every submit lives in a footer at the bottom
   of its card, aligned to the right edge.
2. **Cancel or secondary before primary, everywhere.** In card footers, plain
   footers, sticky footers and dialogs the secondary action (Cancel, Back,
   Preview, Send a test email) comes first and the primary or danger action is
   the last, right-most button. There is at most one primary per footer. Enter
   presses the first submit button in source order, so a form with more than one
   submit starts with a hidden default submit (see UI-07). Cancel is the default
   secondary `hm/button` (no `Variant`): never ghost, never a bare link style.
3. **Delete sits apart, at the far left, on editors.** On the sticky editor footer
   Delete is the first item and is pushed to the left edge with
   `hm-savebar__aside` (or `hm-card__foot-note`); Cancel and Save stay on the right.
4. **One switch for every on/off setting.** A lone boolean is an `hm/field` with
   Control `switch`. Pass `OffValue` when the handler needs an explicit "off" in
   the POST, because an unchecked switch posts nothing. Exceptions that stay
   checkboxes: multi-pick lists (API scopes, permissions, content types, terms, spam script blocks,
   table row and select-all, media tile select, the schemaform "clear the stored
   value" box) and consent gates ("I understand ..."). 
5. **Flat sticky footer only on four editor screens and the menu editor:** entity edit, entity model edit,
   form editor and plugin settings, plus the menu editor, whose save bar stays in reach
   beside a long tree. They use `.hm-savebar` (flat, hairline top
   border, no radius, no shadow). Every other form uses a card footer.
6. **No shadows except popups and focus or selected rings.** Allowed:
   `none`, ring shadows such as `0 0 0 2px`, `inset` shadows (selected marker,
   hairline), and `--shadow-2` or `--shadow-3` on popups only: dialogs, menus,
   popover panels, the slash menu and bubble, toasts, search-select lists, styled-select lists (`.hm-select__list`), the
   token list and the page editor rich popup. The selected
   segment of every segmented control (`hm/segmented`, and the pill row `hm/tabs` `Variant "segmented"`) is a
   paper pill on the grey track with a hairline ring (`0 0 0 1px`), an inset bottom bevel and a semibold label, and no drop shadow. One named exception: the sticky blog editor
   toolbar (`.hm-ed__bar`) carries `--shadow-1`. Nothing else may use `--shadow-1`.
   Tooltips share one type and radius (`--text-xs`, `--weight-medium`, `--radius-sm`); the analytics chart
   tooltip (`.hm-aia-tt`) keeps its paper fill and 1px border, with no shadow, because it holds rows of data.
7. **Always use the partials.** Never hand-write the markup a partial produces:

   | Need | Partial |
   |---|---|
   | Button or link button | `hm/button` |
   | Card with a title | `hm/card-start` ... `hm/card-end` |
   | Card footer | `hm/card-foot` ... `hm/card-foot-end` |
   | Footer outside a card (auth, setup) | `hm/foot` ... `hm/foot-end` |
   | Form control with label, help, error | `hm/field` |
   | Dialog | `hm/dialog-start` ... `hm/dialog-end` (its `Primary` slot renders the primary button after Cancel). The footer is its own `<form method="dialog">`, so a primary that submits a page form needs `PrimaryAttrs` with `form="<form id>"`; without it the button only closes the dialog |

   | Notice that stays on a page | `hm/callout-start` ... `hm/callout-end`, or `hm/callout` |
   | Result of an action in a swapped fragment | `hm/flash` with `Inline` |
   | Select (one-off page, filter, field, parent) | `hm/select`, or `hm/field` Control `select` |

   `hm/button` accepts extra attributes through `Attrs` (built with `attrmap`).
   The whitelist lives in `backend/internal/admin/partialfuncs.go`: htmx
   attributes, `data-*`, `aria-*`, form association (`form`, `formaction`, ...),
   `popovertarget`, `popovertargetaction`, `hidden`, `download` and a few plain
   ones. Event handlers (`on*`, `hx-on`) are rejected and must stay that way.


## Components

Components are declared once in `backend/web/admin/components.json`: the selector, the owner
CSS file and the contract values. It is the only registry of who owns a component: UI-06 and
DC-02 both read their owners from it (every class block in the base selector belongs to the
owner file). A component with empty props and no variants only names its owner; it needs no
styleguide sample. Values are checked by `TestDesignContract` (DC-01 to DC-04) and by UI-D14 in
Chrome. The table is generated from the JSON: after editing it run
`cd backend && go test ./internal/admin/designcheck -run TestDesignContractDocsTable -update`.
A change to one component edits only its own subsection below and its entry in the JSON.

<!-- components:begin -->
| Component | Selector | Owner | Partial | Props |
|---|---|---|---|---|
| badge | `.hm-badge` | `css/partials/badge.css` | `hm/badge` | `border-top-left-radius: var(--radius-full)`, `font-size: var(--text-xs)`, `font-weight: var(--weight-medium)`, `height: var(--badge-h)`, `padding-left: var(--space-2)`, `padding-right: var(--space-2)`; `.hm-badge.hm-badge--sm`: `font-size: var(--text-2xs)`, `height: var(--badge-h-sm)`, `padding-left: var(--space-1)`, `padding-right: var(--space-1)` |
| button | `.hm-btn` | `css/partials/button.css` | `hm/button` | `border-top-left-radius: var(--radius-sm)`, `border-top-width: 0`, `box-shadow: inset 0 0 0 1px var(--segment-ring), inset 0 -2px 0 var(--segment-bevel)`, `font-size: var(--text-sm)`, `font-weight: var(--weight-medium)`, `padding-left: calc(var(--space-3) + var(--space-px))`, `padding-right: calc(var(--space-3) + var(--space-px))`; `.hm-btn--danger`: `box-shadow: inset 0 -1px 0 var(--btn-edge-danger)`; `.hm-btn--ghost`: `box-shadow: none`; `.hm-btn--icon, .hm-btn[data-icon-only]`: `padding-left: 0`, `padding-right: 0`; `.hm-btn[aria-pressed="true"]`: `box-shadow: inset 0 0 0 1px var(--color-sumi)`; `.hm-btn--primary`: `box-shadow: inset 0 -1px 0 var(--btn-edge-primary)`; `.hm-btn--sm`: `padding-left: calc(var(--space-2) + var(--space-px))`, `padding-right: calc(var(--space-2) + var(--space-px))` |
| card | `.hm-card` | `css/partials/card.css` | none | `border-top-color: var(--color-kinu)`, `border-top-left-radius: var(--radius-lg)` |
| chip | `.hm-msel__chip, .hm-msel__more, .hm-filter-chip` | `css/partials/multiselect.css` | none | `border-top-left-radius: var(--radius-full)`, `font-size: var(--text-xs)`, `font-weight: var(--weight-medium)`, `height: var(--chip-h)`, `padding-left: var(--space-2)`, `padding-right: var(--space-2)` |
| colophon | `.hm-colophon` | `css/partials/colophon.css` | none |  |
| confirmcard | `.hm-confirmcard` | `css/partials/confirmcard.css` | `hm/confirmcard` | `display: flex`, `flex-direction: column`, `gap: var(--space-4)` |
| danger-confirm | `.hm-danger-confirm` | `css/partials/danger-confirm.css` | `hm/danger-confirm` | `border-top-left-radius: 0`, `font-size: var(--text-sm)`, `padding-left: 0`, `padding-right: 0` |
| dialog | `.hm-dialog` | `css/partials/dialog.css` | none | `border-top-color: var(--color-kinu)`, `border-top-left-radius: var(--radius-lg)`, `box-shadow: var(--shadow-3)`; `.hm-dialog[data-side="end"]`: `border-top-left-radius: 0` |
| dropzone | `.hm-dropzone` | `css/partials/dropzone.css` | `hm/dropzone` | `border-top-left-radius: var(--radius-md)`, `font-size: var(--text-sm)`, `padding-left: var(--space-4)`, `padding-right: var(--space-4)` |
| input | `.hm-field__control` | `css/partials/field.css` | `hm/field` | `border-top-color: var(--border-field)`, `border-top-left-radius: var(--radius-sm)`, `font-size: var(--text-md)`, `padding-left: var(--space-3)`, `padding-right: var(--space-3)`; `select.hm-field__control`: `padding-right: var(--space-8)` |
| menu | `.hm-menu` | `css/partials/menu.css` | none | `border-top-color: var(--color-kinu)`, `border-top-left-radius: var(--radius-md)`, `box-shadow: var(--shadow-3)` |
| popover | `.hm-popover-panel` | `css/partials/popover.css` | none | `border-top-color: var(--color-kinu)`, `border-top-left-radius: var(--radius-md)`, `box-shadow: var(--shadow-3)` |
| radiocard | `.hm-radiocard` | `css/partials/radiocard.css` | `hm/radiocard` | `border-top-left-radius: var(--radius-md)`, `font-size: var(--text-sm)`, `padding-left: var(--space-4)`, `padding-right: var(--space-4)` |
| savebar | `.hm-savebar` | `css/partials/savebar.css` | none |  |
| savestate | `.hm-savestate` | `css/partials/savestate.css` | `hm/savestate` | `border-top-left-radius: var(--radius-md)`, `font-size: var(--text-sm)`, `padding-left: var(--space-3)`, `padding-right: var(--space-3)` |
| searchselect-list | `.hm-searchselect__list` | `css/partials/searchselect.css` | `hm/searchselect` | `border-top-color: var(--color-kinu)`, `border-top-left-radius: var(--radius-md)`, `box-shadow: var(--shadow-2)`, `z-index: var(--z-popup)` |
| segmented | `.hm-segmented__btn` | `css/partials/segmented.css` | `hm/segmented` | `border-top-left-radius: var(--radius-sm)`, `font-size: var(--text-sm)`, `font-weight: var(--weight-medium)`, `padding-left: var(--space-3)`, `padding-right: var(--space-3)`; `.hm-segmented__input:checked + .hm-segmented__btn`: `background-color: var(--color-paper)`, `box-shadow: 0 0 0 1px var(--segment-ring), inset 0 -1px 0 var(--segment-bevel)`, `color: var(--color-sumi)`, `font-weight: var(--weight-semibold)`; `.hm-segmented__btn:disabled`: `cursor: not-allowed`, `opacity: 0.55`; `.hm-segmented__btn[aria-pressed="true"]`: `background-color: var(--color-paper)`, `box-shadow: 0 0 0 1px var(--segment-ring), inset 0 -1px 0 var(--segment-bevel)`, `color: var(--color-sumi)`, `font-weight: var(--weight-semibold)` |
| select-list | `.hm-select__list` | `css/partials/select.css` | `hm/select` | `border-top-color: var(--border-control)`, `border-top-left-radius: var(--radius-md)`, `box-shadow: var(--shadow-2)`, `z-index: var(--z-popup)` |
| statcard | `.hm-statcard` | `css/partials/chart.css` | `hm/stat` |  |
| statgrid | `.hm-statgrid` | `css/partials/chart.css` | `hm/stats` |  |
| swatches | `.hm-swatches__opt` | `css/partials/swatches.css` | `hm/swatches` | `border-top-left-radius: var(--radius-sm)`, `font-size: var(--text-sm)`, `font-weight: var(--weight-medium)`, `padding-left: var(--space-2)` |
| switch | `.hm-switch .hm-field__box` | `css/partials/field.css` | `hm/field` |  |
| table | `.hm-table` | `css/partials/table.css` | `hm/table` |  |
| tabs | `.hm-tabs__tab, .hm-tablist__tab` | `css/partials/tabs.css` | `hm/tabs` | `font-size: var(--text-sm)`, `font-weight: var(--weight-medium)`, `padding-left: var(--space-2)`, `padding-right: var(--space-2)`; `.hm-tabs[data-variant="segmented"] .hm-tabs__tab`: `padding-left: var(--space-3)`, `padding-right: var(--space-3)`; `.hm-tabs[data-variant="segmented"] .hm-tabs__tab[aria-current="page"]`: `background-color: var(--color-paper)`, `box-shadow: 0 0 0 1px var(--segment-ring), inset 0 -1px 0 var(--segment-bevel)`, `color: var(--color-sumi)`, `font-weight: var(--weight-semibold)`; `.hm-tabs[data-variant="sub"] .hm-tabs__tab`: `font-size: var(--text-xs)` |
<!-- components:end -->

### Button

Partial: `hm/button`  
Contract: `button` in components.json  
Rules: house rules 1 to 3, UI-01, UI-05, UI-06, DC-02

Height 36px (`--sm` 32px), radius `--radius-sm`, 13px text, weight 500. Variants: secondary (default),
`primary` (ink fill), `ghost`, `danger`, `danger-text`. Secondary is a paper fill with sumi text and no
border: an inset ring `--segment-ring` in the pixel the old border used, plus a 2px inset bevel
`--segment-bevel` (the selected-segment pill's look). Hover is a shiro fill with the ring `--segment-bevel`;
active is a kasumi fill with the ring only; pressed (`aria-pressed`) is kasumi with a 1px sumi ring.
`primary` keeps the ink fill with an inset bottom edge `--btn-edge-primary` (hover `--color-sumi-soft`),
`danger` is solid with an inset bottom edge `--btn-edge-danger`, `ghost` has no ring, and `danger-text` is
the secondary frame with a red label. Inline padding adds `--space-px`, so the text stays where the 1px
border had it. Forced colors: every button, ghost and danger-text included, gets a 1px system-colour
outline as its edge (`GrayText` when disabled; the ring shadows are dropped there) and a `Highlight` focus ring.
Only `css/partials/button.css` may set these values;
other files may set layout (width, margin) but not radius, padding, type or borders on `.hm-btn`.

Like the Input relaxation below, the resting secondary edge is a deliberate relaxation of WCAG 1.4.11:
`--segment-ring` is a hairline well below the 3:1 that `--border-control` met, chosen so a button matches the
selected segmented pill. The label, the fill and the position identify the button; hover and focus keep a
stronger ring. Do not move it back to `--border-control`.

### Input

Partial: `hm/field`  
Contract: `input` in components.json  
Rules: UI-11, DC-02

Text inputs, selects and textareas are calm at rest: a soft hairline border from
`--border-field`. Hover darkens the border to `--color-hai` (or `--border-control`). Focus draws the blue
ring. Checkboxes and switches keep `--border-control`.

This is a deliberate relaxation of WCAG 1.4.11 (non-text contrast) for the resting
border of a field: `--border-field` is lighter than the 3:1 that `--border-control`
meets. It was chosen because a dark outline on every field pulls the eye away from
the content. The field is still identified by its label, its fill and its position,
and the hover and focus states keep the 3:1 border and the ring. axe still has to
pass. The lint (UI-11) checks the resting token and that the hover rule sets the
border to `var(--color-hai)` or `var(--border-control)`; Chrome (UI-D11) checks
the resting colour on every screen and the hover colour on `settings-general`
(first visible control only).

### Card

Partial: `hm/card-start` ... `hm/card-end`  
Contract: `card` in components.json  
Rules: house rules 1 and 5, UI-02, UI-04

The paper panel: `--radius-lg`, 1px `--color-kinu` border, no shadow. The footer follows house rule 1.

### Badge

Partial: `hm/badge`  
Contract: `badge` in components.json  
Rules: DC-01, UI-05

A status pill: `--badge-h` high, `--radius-full`, `--text-xs`, weight 500, a tinted fill per tone (neutral is bordered soft grey) and a leading dot so tone is not colour alone.

- **`Size "sm"`** is a compact secondary label (`hm-badge--sm`: `--badge-h-sm`, `--text-2xs`, muted ink; an `Icon` stays accent coloured). The Browse cards use it for First-party; other badges keep the default size.
- **The dot is the default.** The partial renders it for every badge; callers do not ask for it.
- **Only exception: a badge with an `Icon`** (stat and KPI delta arrows, env-lock badges). The icon carries the meaning, so it replaces the dot. Any other exception is a named entry in `badgeExceptions` in `internal/admin/badge_dot_test.go` with a reason; there are none today. `TestBadgeDotEverywhere` fails on a dotless badge in the page and partial goldens.
- **JS never writes badge markup** (UI-05). Clone a server-rendered `<template>` of `hm/badge`, as the builder navigator does for its "Hidden" pill (`data-hm-bs-hidden-badge`).
- **Read-only tags and identifiers are badges too**: hooks, hosts, routes, shortcodes, the bound-data chip and the entity parent label use `hm/badge` with `Mono` (monospace, no dot, wraps) or `Plain` (no dot). Status badges keep the dot (UI badge-dot test).
- **Not badges** (controls or tokens that only look like pills): the tab count (`.hm-tabs__count`), the Filters button count and removable filter chips, input tokens (`.hm-chip`, `.hm-msel__chip`), editor canvas labels, `.hm-savestate` and the Allow/Block segmented control.

### Popup family

Partial: none (menus, popovers and lists are part of their owner partials)  
Contract: `menu`, `popover`, `select-list`, `searchselect-list` in components.json  
Rules: house rule 6, DC-02, DC-03

Popups carry a shadow and a hairline border. Today the family is not uniform, and the JSON records
the real values per selector: `.hm-menu`, `.hm-popover-panel` and `.hm-dialog` use `--shadow-3` with a
`--color-kinu` border; `.hm-select__list` and `.hm-searchselect__list` use `--shadow-2` and `--z-popup`.
Menus and popovers live in the top layer and set no z-index. Unifying them is a design change for the
PR that owns popups, not for a token sweep.

### Dialog

Partial: `hm/dialog-start` ... `hm/dialog-end`  
Contract: `dialog` in components.json  
Rules: UI-D13, DC-02

`hm/dialog-start` takes `Title`, an optional `Description` (one sentence, wired to
`aria-describedby`), `Danger`, `Static`, `Side` and `Width`. `Side: "end"` makes the
drawer variant (`data-side="end"`): a full-height panel pinned to the inline end
that otherwise behaves as a dialog (480px; `Width "wide"` makes it `min(640px, 100vw)`, used by the Browse plugin panel only). A static dialog holds input, so a
click on its backdrop does not close it; Esc and Cancel still do. The panel scrolls
under a pinned footer. On open, focus moves to the first control in the body (the
title when there is none) and returns to the opener on close; UI-D13 checks both.
Row and bulk confirmations all go through `openConfirm` in `components.js`, which
fills the shared `#hm-confirm` dialog; do not build a second confirm.

A page may also host a slide-over drawer that is not modal and leaves the page usable beside it:
the builder's SEO drawer (`.hm-bs__seo`, page-local in `builder.css`, content loaded on first
open). It follows the same keyboard contract as a dialog: focus moves into it on open, Esc and
its Close button hide it and focus returns to the button that opened it (`aria-expanded` on the
opener). Its controls are the shared `hm/*` partials, never bespoke ones.

### Tabs

Partial: `hm/tabs`  
Contract: `tabs` in components.json  
Rules: UI-D12, DC-02

One question decides which a row of choices is: does picking an item change what
the page is, or only how the same data is shown?

- **Tabs navigate.** Picking an item loads another page or another set of data:
  a different path, `?tab`, `?type`, `?kind`, or a different dataset. Tabs are an
  underline row (`hm/tabs` with no `Variant`). Redirects and its tools, the
  Internal linking sections, and the entity types and taxonomy kinds are tabs.
  The AI analytics sections are not: they are a sub-sidebar area (below).
- **Pills rearrange in place.** Picking an item changes the order, grouping, period
  or view of data that stays on the page: `group`, `from` and `to`, `range`,
  `view`. Pills are `hm/tabs` with `"Variant" "segmented"`: the same look as
  `hm/segmented`: a hairline `--border` track and a selected chip that is a paper pill with a hairline ring, a bottom bevel and a semibold label. They sit inside the
  content next to what they change. The period is not a pill row: it is the date
  picker (`hm/daterange` inside `hm/pageheader`), because it applies to the whole
  page and its preset list already covers the common periods.
- **At most two tab levels.** Level 1 is the underline row. Level 2 is a smaller
  underline row directly under it, `"Variant" "sub"`. A third level is a sign the
  page should be split. The one exception is a JS tablist that sits inside a panel
  of another JS tablist: the shared SEO panel's tabs inside the blog editor's
  SEO and AEO tab. They are different kinds of row (the outer one is the page's
  tablist bar, the inner one a section of the panel), and the inner one never navigates.
- **JS tablists** switch panels in the same document and are not `hm/tabs`
  rows (they are buttons with `aria-selected`, not links). Three of them, the blog
  editor's Post / SEO and AEO tabs, the builder inspector's Content / Style / Advanced
  tabs and the shared SEO panel's Search / Social / Schema / Advanced tabs (`hm/seopanel`,
  module `@hm/seopanel`, shown stacked under their headings without JavaScript), use `.hm-tablist` / `.hm-tablist__tab`: the exact `hm/tabs` underline recipe (same type,
  accent underline, hover pill, hairline), declared in `tabs.css` as selector lists on the link-tab
  rules. The builder's phone tabs (Navigator, Add, Inspector, below 960px) are not segmented: they are `.hm-bs__dock-btn` buttons in a fixed bottom bar, and the selected tab has a kasumi fill with an ink top line (see Selected state).

| Choice | Row | Why |
|---|---|---|
| AI analytics: Overview, Crawlers, Content, Referrals, Health | sub-sidebar (`hm/areanav`), not tabs | five sections of one area, like Settings and Theme |
| Group the chart: By operator, By purpose | pills | the same chart regrouped |
| Date range | date picker in the page header; `ui.DateRange.Compare` adds a "Compare to previous period" switch inside it | one control for presets, custom ranges and the comparison |
| Media: Grid, List | pills | the same files, another layout |
| Redirects: Redirect rules, 404 log, Explain a URL | tabs | different pages |
| Taxonomies: one tab per kind | tabs, level 2 under Entity models | a different set of terms |

**AI analytics is an area, not a tab row.** Like Settings and Theme it has its own secondary sidebar
(`hm/areanav`, a select on phones) with five sections: Overview, Crawlers, Content, Referrals and Health.
The agent page (`<admin>/analytics/agents/{id}`) keeps Crawlers current. Add a section by registering it in
`analyticsRegistry` (`settings_nav.go`), never by putting a tab row in the page.

The lint and the Chrome check enforce this. A segmented row outside the declared in-place rows
(Group the chart, View) fails the lint, and a segmented row whose items
change the path or a navigating param fails in Chrome.

### Chip

Partial: none (rendered by `js/multiselect.js`)  
Contract: `chip` in components.json  
Rules: DC-02

The filter bar (`hm/filterbar`, used through `hm/table`) has opt-in keys, each of which leaves the markup unchanged when absent: `Segmented` (a radio `hm/segmented` that applies live), `Live` (popover fields apply live, no Apply button, `<noscript>` Apply stays), `Target` (id of the results the swap replaces, so an open popover stays put), `FilterCount` (the Filters badge, default the number of chips), `Bare` (no card; segmented + Filters at the start, search + selects at the end, stacked below 40rem) and `ChipsOutside` (the page renders `hm/filterchips` itself). Popover `ui.Filter`s may be `Control "checkbox"` (a group over `Options`) or `"switch"` (`Value`, `OffValue`, `Checked`). Plugins Browse is the user of all of them.

The removable chips: the multi-select value chip, its "+N more" sibling and the active-filter chip (`.hm-filter-chip`, remove button in `popover.css`) share one look: `--chip-h` high, `--radius-full`, `--text-xs`, weight 500. Read-only tags are `hm/badge`, not chips.

### Select

Partial: `hm/select`  
Contract: `select-list` in components.json (the list popup)  
Rules: UI-D3, DC-02

`hm/select` (and `hm/field` Control `select`, filter bars and `hm/parentselect`)
renders the native `<select>` inside `[data-hm-select]`. `@hm/select` upgrades it to a combobox
trigger and a listbox (`.hm-select__list`, a popup, so `--shadow-2` is allowed).
The native select stays the source of truth: its `id`, `name`, value, validation and
`change` event are what forms, htmx triggers and the server see, and without script
it renders as it always did. A fieldset-disabled select is disabled. Not styled, on
purpose, and they stay native: the settings sub-navigation select and the selects the
builder creates in JS. Name every select with a `<label for>` or `AriaLabel`.

### Callout, flash and toast

Partial: `hm/callout`, `hm/flash`  
Contract: not in components.json yet  
Rules: UI-01

Three ways to tell the user something; pick by how long it lives and where it is.

- **Callout (`hm/callout-start` / `-end`, or `hm/callout` with a `Body`).** A
  standing notice that belongs to the page: a warning, a limit, a state the user must
  act on. Dict: `Kind` (`info`, `success`, `warning`, `danger`; `error` maps to
  `danger`), optional `Title`, `Compact`, `DismissKey` (makes it dismissible; the
  choice is kept for the browser session), `ID` and `Attrs`. It needs a `Title` or a
  `Body`. The shell banners are compact, dismissible callouts. Never hand-write
  `hm-callout` markup.
- **Flash (`hm/flash` with `Inline`).** The result of an action inside a fragment
  that htmx swaps: it renders in place and stays. Without `Inline` the flash is
  the server flash of a redirect (`SetFlash`), and with JS it becomes a toast.
- **Toast.** A transient, polite message: `HX-Trigger` `hm:toast` with `kind`
  (`error`, `warning`, `success`, `info`; each has an icon), `message` and
  `timeout`. The layout holds two persistent, sibling live regions: `#hm-toasts`
  (`role=status`, polite) and `#hm-toasts-alert` (`role=alert`, errors, which stay until
  dismissed). Toast nodes carry no role of their own (nested or freshly inserted live
  regions are announced unreliably). At most four show per region, and Esc dismisses the
  newest (an error first) unless a dialog or popover owns the key.

- **Failed load and empty.** A section that fails to load is a `hm/callout` (`Kind` danger) with a Retry
  `hm/button`, on every page (the dashboard and the analytics charts alike); an empty list or chart inside a
  card is `hm/empty` with `Compact`, with no dashed frame of its own.

A message the user must still see after a reload is a callout. A confirmation of
something that just happened is a toast or an inline flash, never a callout.

### Smaller component notes

Partial: `hm/stat`, `hm/empty`, `hm/steps`, `hm/table`, `hm/rowmenu`  
Contract: not in components.json yet  
Rules: UI-D3

- **Stat** (`hm/stat`): `Change` renders as a delta badge with an arrow, a tone and an
  optional `Caption`; direction is also in hidden text. A ticker card also takes an optional
  `Note` (a muted line under the value and trend) and `Link` with `LinkLabel` (a plain link under
  the note, default "View more"); the card owns their look, a page never restyles it.
  A value is always a metric (30px, semibold). A status phrase or a date (anything that is not a number) sets
  `Badge` and renders as an `hm/badge`, never as a smaller value. `hm/stats` `Variant "strip"`
  joins the cards in one bordered frame (the analytics KPI row). Every stat value is 30px semibold with a
  `--color-hai` label, and its trend line is `hm/spark`: server geometry, or live (`Stat.Spark` with `ui.Spark{Live: &ui.SparkLive{...}}`), where
  `js/aia/page.js` fills the paths of the server-rendered SVG from a data endpoint.
- **Empty** (`hm/empty`): a solid hairline border; `Compact` for use inside a card.
- **Stepper** (`hm/steps`): done steps show a check, the current step the accent,
  and a "Step n of N" line follows; `Label` names the list (default "Progress").
- **Copy buttons**: `data-hm-copy-field="<input id>"` copies a field's value and
  `data-hm-copy-url="<path>"` the absolute address. One handler serves both.
- **Pending state**: a plain POST form shows busy on its submitter (`aria-busy`,
  `aria-disabled`, never `disabled`, so the button still posts) and a second submit is ignored.
- **Tables** (`hm/table`, `hm/datatable`, the analytics tables): every header cell is 12px, medium,
  uppercase with `.08em` tracking, `--text-muted`, set once in `table.css` on `.hm-table thead th`; no page restyles it.
  Group header rows are `tr.hm-table__group`. Under 640px rows stack as cards and each cell shows its
  column label (`data-label`); the header strip stays only when it holds select-all or a sort link. A cell can carry an inline `Bar` or `Spark`.
- **Menus** track `aria-expanded` on their opener and Tab leaves an open menu.
  `hm/searchselect` supports Home and End and announces the result count.

### Charts and data viz

Partials: `hm/aia-chart`, `hm/spark`, `hm/barlist`, `hm/categorybar`, `hm/tracker`, `hm/progress`
Owner: `js/aia/*` (entry `@hm/aia`), `css/partials/aia-chart.css`, `css/partials/viz.css`

The AI analytics charts are SVG drawn by the admin's own modules (`js/aia/`: `page`, `timechart`,
`tracker`, `model`, `scale`, `paths`, `series`, `fmt`, `load`). The server renders every chart as a figure with
its data table; the script draws the SVG over it. No charting library is shipped.

- **Series order.** Series are drawn in the order c1, c2, c3, c5, c4, c6 (`aia/series.js`), the order of the
  approved design, and carry the class `hm-viz--series-<n>` of the token number, so the tokens keep their
  numbers (`--chart-1..6`). "Other" is always last and always `--chart-6`. A series also has a dash and a
  marker shape, so colour is never the only cue (WCAG 1.4.1).
- **Tokens only.** Colour comes from CSS classes (`hm-viz--*` set `--viz`), never from script, so a theme change
  needs no redraw. No hex literal in `aia*.css` or `js/aia`.
- **Legend.** One `<button aria-pressed>` per series above the plot. Space and Enter toggle it; the last visible
  series cannot be hidden (`aria-disabled`). With compare on, a non-interactive "Previous period" item is added.
- **Show data.** The server-rendered table is the fallback. With JS it is collapsed behind a "Show data" button
  (`aria-expanded`, `aria-controls`).
- **States.** The plot keeps its height in every state: a pulse skeleton while loading (static under reduced
  motion), "No data for this period." when every value is zero, and an alert with a Retry button when the fetch
  fails (one alert per page, the rest are status). Retry keeps focus inside the chart. The empty and error
  markup are `<template>`s rendered by Go; never build `hm-btn` markup in JS.
- **Keyboard and tooltip.** The plot is a focusable group: arrows, Home and End move a cursor, Escape closes the
  tooltip, and a live region announces the date and values.
- **Values.** `chartSpec.Format` (`number`, `compact` or `percent`) sets how a value prints. Day ticks never rotate.
- **Sparks.** `ui.SparkOf(vals, unit)` carries `Summary` ("Trend over 30 days: ending higher", no percentage, so it
  cannot contradict the KPI badge), the accessible name and tooltip of a focusable wrapper. `Kind` picks the hue.
- **Bars.** `hm/barlist` takes `Quiet` (class `hm-barlist--quiet`, fill `--chart-bar-quiet`) for dense lists; other
  barlists keep `--chart-bar-soft`. `hm/categorybar` takes `Columns` (legend with Value and Share columns) and
  `hm/tracker` takes `States` (hatched danger, shorter warning, hollow no-data blocks); both are opt-in.
- **Date picker.** The trigger shows the preset and its span ("Last 30 days · 20 Sep to 19 Oct"), and a second line
  "vs <span>" with compare on. Popover and input ids derive from `DateRange.ID` (default `range`).
- **Progress and tracker.** `hm/progress` is a `role=progressbar` with value attributes. `hm/tracker` takes
  `Start`, `End` and a `Legend`; `hm/categorybar` rounds only its outer segments.
- **Adding a series.** Add a column to the `matrix` the page builds; the next entry of the series table in
  `aia/series.js` gives it its class, dash and marker, and the legend and tooltip pick it up with no template change.
  A new chart form goes in `aia/`, with a `data-hm-aia-chart` kind in `page.js`.

### Dashboard (overview)

`GET <admin>` is the Overview, built in `internal/admin/dashboard.go` from existing
stores and shipped partials only: no new table; the
Site health and Activity cards and the owner-only Audit log page are the additions. A KPI row
of five `hm/stat` ticker cards (Published, Drafts, New submissions, Missing pages,
AI-referred visits) is followed by the card grid (below). Each KPI shows its `Note`
and `Link` inside the stat card. `css/pages/dashboard.css` is layout plus the plain row styling of
its own lists: grid, gaps, the 390px column rules, a token-coloured divider between Activity
rows and the muted group heading in Site health. Never a card background, radius or a
colour that is not a token; anything a second page needs becomes a partial.

- **Card states.** Every card is `ok`, `empty` or `error`, independent of the others.
  Empty names what will appear there (`hm/empty`, compact). A store that fails, panics
  or misses the deadline turns only its own card into an error card (`hm/callout` plus
  a ghost "Reload the page" button) and its KPI cards into "n/a" with an error note and Reload under each; the cause is logged,
  never shown.
- **Role split.** Owners see everything. Editors see Published, Drafts, New submissions
  and Recent submissions; the 404 log, analytics and update stores are not called for
  them. A store that is not wired omits its cards.
- **Bounds.** One context with a 4 s timeout covers the page; the cards run in parallel.

- **Grid.** Owners get four columns: Site health (`#dash-health`) in column 1 spanning two
  rows, the pages AI crawlers, missing pages and recent submissions cards in row 1, and
  Activity (`#dash-activity`) across columns 2 to 4 in row 2. Below 1280px it is two
  columns (health and Activity lose their spans; Activity is full width), below 1100px one,
  and below 640px the KPI row is two across. DOM order is health, pages, missing,
  submissions, activity. A lone card (the editor's Recent submissions) spans the whole
  row. Activity rows are one line each: who, kind badge, target, when, laid out in the
  page CSS from tokens, with the existing `hm/badge`; no new partial.
- **Split spark.** A KPI with a comparison draws the previous period as a grey
  `.hm-spark__prev` line left of a `.hm-spark__divider`, the current period in the accent
  line; the note says what is compared. Only Submissions and AI-referred visits have one.
- **Update status.** The overview has no updates card and the top bar no update chip. The
  top banner is narrowed to what needs an action: `rolled_back`, `blocked`, `backup_failed`.
  A major release and updates off for 90+ days are shown on Settings › Updates only.

| Read | Bound |
|---|---|
| Published, Drafts | one grouped count |
| Submissions | at most 20 forms, 200 rows each (spam counts), last 14 days, spam left out; a cut-off scan shows "N+" |
| Missing pages | one read-only query: open entries seen in the last 7 days, 51 rows at most ("50+" when full) |
| AI-referred visits | one 14-day window |
| Pages AI crawlers fetch most | 8 rows, last 30 days |
| Site health | the existing health checks, grouped; read-only |
| Activity | newest 6 of the audit log merged with update history; one source failing leaves the other |

Labels say what is counted: "Missing pages" is the exact number of open addresses seen
in the last 7 days ("50+" past 50), while the bar values, headed "All-time hits", are
lifetime hit counts. The per-page analytics are AI crawler fetches, never "visits". Dates
are absolute.

### Dropzone

Partial: `hm/dropzone`. Contract: `components.json` key `dropzone`.

Rules: every file upload uses `hm/dropzone` (media dialog, `/media/new`, picker with `Compact`). It wraps a real file input, so it works without JS. The border is neutral at rest; accent is only for `--over` and focus. Choosing or dropping files only lists them in `data-hm-dropzone-files`; it never submits. A rejected file shows an inline error and is left out. Never hand-write a drop area.

### Save state

Partial: `hm/savestate`. Contract: `components.json` key `savestate`.

Rules: one dot plus text in the bar of every editor (entity form, post editor, page builder);
never a second indicator. `data-state` is `saved` (green), `unsaved` (amber), `saving` (spinner),
`error` (red) or `idle` (muted, text empty: the partial hides itself). Scripts only change
`data-state` and the text, never the markup. The text wraps and never truncates.

### Confirmcard

Partial: `hm/confirmcard`. Contract: `components.json` key `confirmcard`.

One form around one card, for every page that asks "are you sure?" and has no dialog: the generic
confirm, theme replace, theme activate and plugin uninstall. The page title is the pageheader above
it; the card holds the sentence (or a table, or one field) and its footer holds Cancel first and the
action last. Open with `hm/confirmcard-start`, close with `hm/confirmcard-end`; `hm/confirmcard` is
the plain form with a message.

Rules: the Confirm label names the action ("Delete theme", never "OK"); Danger only for data loss;
a page never adds a second form or a second primary button inside the card. The dialog
(`data-hm-confirm`) is still preferred where JS is available; this card is its no-JS page.

### Shell, auth and error pages

- **One flash slot per layout.** Shell: the first child of `.hm-page`. Bare (sign in, setup, profile step): under the brand, inside the panel. Editor: under the editor bar. A page never renders a second slot; the sign-in form's one status line (`#login-status`, wired to both inputs with `aria-describedby`) is that page's use of it.
- **Standing notices** (setup pending, site address, update) render in `.hm-notices`, a full-width strip above the page column, so they are the same width on narrow and wide pages. They stay dismissible `hm/callout` compact callouts.
- **`hm/pageheader` `Badge`.** An optional `ui.Badge` renders after the title, in the same row; actions are top-aligned with the title.
- **`hm/empty` `Code`, `Plain`, `H1`.** `Code` is a muted status ("404") above the title, `Plain` drops the frame to centre it inside a panel, `H1` makes the title the page heading. Full-page errors and `hm/errorpage` are built on it; an error page names its status with an icon (search, lock, alert).
- **`hm/tabs` `ShowZero`.** A row opts in to showing a count of 0 (taxonomy kinds); by default 0 stays hidden.
- **`#hm-confirm` is the one hand-written dialog.** `components.js` fills a single shared node, so it is not built with `hm/dialog-start`. A link or menu item with `data-hm-confirm` opens it; so does a submit `button[data-hm-confirm]` inside a form, which then posts that form's fields (without `_csrf`). Without script the button submits directly, so its card must already say what happens.
- **`data-hm-carry="<input id>"`** on a link appends that input's value as `?email=` when clicked (the invite form's "Add with a password instead"); without script it is a plain link.
- **Colophon.** Every shell page (never the bare or editor layouts) ends with `.hm-colophon` after `</main>`: one muted `--text-xs` link, "Hatamoto <version> · Stand close. Serve well.", to https://hatamoto.dev (`View.AppVersion`: `app.Version` with one leading v, or "dev build"). `.hm-shell__main` is a flex column with `.hm-content` growing, so the line sits at the foot of a short page and after the content of a long one; centred under 768px (`css/partials/colophon.css`).
- **The sidebar** is sticky at `100dvh`; its collapsed state persists in `localStorage` (`hm-sidebar`) on desktop. Under 768px it is a top bar and has no toggle. When a subnav is shown, only the subnav item carries the active chip.

### Segmented

Partial: `hm/segmented`. Contract: `components.json` key `segmented`.

Rules: use it for one value out of a short list. Three modes, one look: `Mode ""` (post: each button is its own submit and no script is needed, the submission status), `"radio"` (a fieldset of radios a surrounding form posts, the Allow/Block policy) and `"buttons"` (`aria-pressed` buttons a script reads, the builder and blog editor width switches). The selected option is a paper pill (`--color-paper` fill, `--color-sumi` semibold text) with a hairline ring and a bottom bevel (ring and inset shadows only), no border, in every mode. Forced-colors mode adds a `Highlight` outline. It is not navigation (use `hm/tabs`) and never holds more than five choices. Never hand-build a row of `aria-pressed` buttons (the parked exceptions under Selected state aside).

A choice that is not available yet sets `SegmentedItem.Disabled`: the button (or radio input) renders `disabled` and `aria-disabled="true"`, is dimmed (`opacity` 0.55, `cursor: not-allowed`), takes no hover and cannot be focused or chosen in any mode. Use it for a layout that stays the same when a later phase lights the choice up (First touch / Last touch, Visits | Leads | Conv. rate); keep at least one live item, and say why in a muted note next to the control. Never use it for a permission.

**`--` placeholder.** A stat or table cell whose data arrives in a later phase renders `--` (with a Note or a nearby help line saying when). `n/a` means the metric does not apply to that row; `--` means "not available yet".

How it differs from the pill row: `hm/tabs` `Variant "segmented"` rearranges a view in place (links); `hm/segmented` sets a value. They differ only in what they do, never in look: hairline `--border` track, 28px tall (`--control-h-xs`), paper pill with a hairline ring and bottom bevel on the selected option, no drop shadow. The track is `inline-flex` and aligns to the start of its container; it never stretches across a card.

### Selected state

One rule for what "selected" looks like, so no screen invents its own:

- **A choice among siblings** (one value out of a short row) is a **paper pill with a hairline ring and a bottom bevel** and a semibold label: `hm/segmented` and the pill row `hm/tabs` `Variant "segmented"`. The standalone builder toggles (the phone dock tabs, the inspector value and link buttons) use a kasumi fill with a 1px ink ring; a hovered unselected inspector option gets the shiro fill and a darker ring. In forced-colors mode every selected control also gets a `Highlight` outline.
- **A tab that switches a panel or page** is an **accent underline**: `hm/tabs` and the JS `.hm-tablist` rows (blog editor, builder inspector, SEO panel).
- **An item in a list or grid** (media tiles, the media picker tile, radio cards; the builder Navigator excepted, see below) is an **ink ring** (2px, `--text`); a check badge on the tile is the non-colour cue.

Accent is for tabs, links and focus; ink is for selection (the builder Navigator is the one exception below). Both inks invert in dark mode through the tokens.

**Deliberate exceptions** (parked, each with its reason; do not copy them into new screens):

- `.hm-bs-opt[aria-pressed="true"]` (the builder inspector's small value buttons, such as spacing and size steps) and the phone dock tabs: a kasumi fill with an ink ring, not a paper pill. They are standalone buttons in a dense inspector, not a track of siblings.
- `.hm-bs-row[aria-selected="true"]` (the builder Navigator): a solid accent fill with on-accent text (`--color-paper`), and the section that contains the selection gets an accent-soft band (`.hm-bs-row--band`). The Navigator is a dense two-level tree where the selection has to read at a glance, and the canvas marks the same node in the same accent. Non-colour cues stay: `aria-selected`, a `Highlight` outline in forced-colors mode and the focus ring (drawn inward in the on-accent colour, since an accent ring would vanish on the accent fill; `Highlight` in forced-colors mode).
- `.hm-theme--active` (Themes page): an accent 2px ring on the active theme card. It marks "in use on the site", not a pending choice, so it follows the accent of the Active badge it carries.
- `.hm-btn[aria-pressed="true"]` (blog editor Focus and Toolbar, date range presets): a toggle button, not a choice among siblings. It keeps the 1px ink ring and kasumi fill of a pressed button.
- `.hm-aia-lg` legend toggles (chart series): the toggle shows a series on or off, and its swatch carries the series colour, so the pressed state is dimming, not a fill.

### Radiocard

Partial: `hm/radiocard`. Contract: `components.json` key `radiocard`.

Rules: use it when each choice needs a title and a sentence (the setup theme and content steps, the people duplicate "Keep" pick); a plain short list stays a `hm/field` select or radio group. One fieldset with a `Legend`, one native radio per card, the selected card has an ink ring. `Cols` 2 gives a two-column grid that stacks on phones; `IDPrefix` keeps ids unique when several groups on a page share a `Name`. `Tag` takes a `ui.Badge` after the title. Never hand-build a bordered radio.

### DangerConfirm

Partial: `hm/danger-confirm`. Contract: `components.json` key `danger-confirm`.

Rules: use it for an action that is hard to undo or weakens a security setting, where a stray click must not be enough (the admin address "use it anyway" step, later key rotation or deleting site content). It is an inline card, never a modal: a self-contained `<form method="post">` with a title, body paragraphs, one phrase input named `confirm_phrase`, and a footer with Cancel first and the danger submit last. The server always checks the posted phrase with `CheckDangerPhrase` and the screen re-renders with status 422 and `Error` set on a mismatch or an empty value. The submit state in the browser is only a hint: server HTML never carries `disabled` on the submit, so the form works without JS, and `components.js` disables it until the typed text equals the phrase exactly. The comparison is exact in Go and in JS: no trim, no case folding. Body text is built from `ui.DangerPart` segments (plain, `Code`, `Strong`), never `template.HTML`. Pass a unique `ID` per card on a page. The card border stays the normal grey; danger shows as a red title (`--color-danger`, as in the danger dialog) and the red submit.

<!-- new-components-above -->

## Breadcrumbs

The trail lives in the top bar and nowhere else. A page never repeats it in its own
panel, so `hm/pageheader` takes no `Crumbs`. Handlers that open a page below a list
set `View.Crumbs` (`settingsCrumbs` for Settings pages, `entityCrumbs` for entity
sub-pages); a page with no crumbs falls back to Home, its section and its title.
The last crumb is the current page and is not a link. The one exception is the
editor bar of the page and blog editors, which have no top bar and show their own
trail. The lint and the Chrome check enforce it: exactly one visible `Breadcrumb`
navigation per screen and viewport.

On phones (768px and below) the top bar stays and shows a compact trail: a back link to
the parent crumb (for example "‹ Taxonomies"). The editor bar's trail is hidden on
phones; its back link (`.hm-editor-bar__back`) is the way up there, and UI-D7 requires
it to be visible instead.

## Create pattern

The list comes first. A list page shows its table or cards right after the header.
Creating something is the header action "New X", the only primary in that header.
No create form sits above or below the list.

- **Small create** (up to about four simple fields, no preview, no rich content):
  the header action is a link to the `/new` page, and with JS it opens a dialog
  over the list that holds the same fields. Without JS the link goes to the `/new`
  page, so nothing needs script. A failed POST re-renders the `/new` page with a 422
  and the form values kept.
- **Big create** (a builder, a preview or rich content): the header action goes to
  the page and stays there. Creating continues into the editor of the new item.

| Surface | Class | Create UI |
|---|---|---|
| API keys | small | dialog, `/settings/api-keys/new` |
| Webhooks | small | dialog, `/settings/webhooks/new`; edit has its own page (`/settings/webhooks/{id}/edit`, no dialog); the signing secret shows once in the secret banner |
| Users: invite | small | dialog, `/settings/users/invite`; "Add with a password instead" links to `/settings/users/new` |
| People: Add person | small | dialog, `/people/new`; the person editor follows (photo and bio). Possible duplicates (merge) are their own view, `/people/duplicates`, linked from the list header |
| Menus | small | dialog, `/theme/menus/new` |
| Taxonomy terms | small | dialog, `/settings/taxonomies/new?kind=`; a new kind and the description sit behind "More options" |
| Redirects | small | dialog, `/settings/redirects/new` (the 404 log links there with a prefill) |
| Media upload | small | dialog with the dropzone, `/settings/media/new` |
| Plugin and theme upload | small | dialog, `/plugins/upload`; the permission review that follows stays its own page |
| Entities | big | page, then the editor |
| Entity types | big | page with the field builder, then the editor |
| Forms | big | page, then the form editor |
| Linking rules | big | page with the preview |
| Import (WordPress) | wizard | its own pages |

Dialog markup: the field group is a partial (`hm/form-apikey`, `hm/form-webhook`, `hm/form-menu`, `hm/form-person`,
`hm/form-term`, `hm/form-redirect`, `hm/form-upload-media`, `hm/form-invite`,
`hm/form-upload-pkg`) used by both the dialog and the `/new` page, so the two cannot
drift; a Go test compares their field names. The dialog is `hm/dialog-start` with an
ID and Title, a form with its own `id` in the body, and `hm/dialog-end` with
`Primary` and `PrimaryAttrs` of `form="<form id>"`. Never pass `type` in
`PrimaryAttrs`: `hm/dialog-end` sets it. The opener is the header link with
`data-hm-open="<dialog id>"`. The lint (UI-10) and Chrome (UI-D10) both enforce the absence of page-level
create forms, on pages with a table and on the theme and plugin lists; only Chrome
checks that the list is the first block and that the header has one primary.
Row actions (the registry Install and Update forms) and settings (the unsigned
packages form) are exempt by name in both checks.

## After create

- **Small create** redirects with 303 to the list as `?created=<id>`, after a
  `SetFlash("success", ...)` that becomes a toast ("API key created"). The handler
  validates the id; the created row is pinned first on page 1 and marked with
  `hm-row--new`, a soft background and a hidden "(new)" label so colour is not the
  only cue. A reload keeps the highlight and never creates a second item.
- **A one-time secret** (an API key, a webhook signing secret, an invite link) never travels in the flash. The
  handler parks it in the server side secret store and redirects to
  `?created=<id>&secret=<token>`. The list renders the `hm/secretbanner` above the
  table: `role="status"`, a heading such as "Copy this key now, it is shown once", the
  value in a read only field, a Copy button and a Dismiss link. The token is single
  use, tied to the signed in user and expires after two minutes, so a reload or a
  second user sees no banner.
- **Big create** redirects to the editor of the new item with a "Created" toast.
- **Errors** re-render the `/new` page with status 422, a summary and the entered
  values. They never redirect and lose the input.

## Copy

Words are part of the design. One verb means one thing on every screen.

| Rule | Use | Not |
|---|---|---|
| Delete or Remove | **Delete** destroys a record (a page, a form, a key). **Remove** detaches something from a container and destroys nothing else (a field from a form, an item from a menu, a chip from a list). | Remove for a record, Delete for a detached item |
| Create or Add | **Create** makes a new top-level object (`Create menu`, `Create form`, `Create key`). **Add** puts a new item inside another thing (`Add field`, `Add item`, `Add term`, `Add person`). A button reads `Add <noun>`; its dialog title reads `Add a <noun>`. | `New secret` as an action, `Add a section` on a button |
| Save | A bare **Save** on a page with one save form and on every editor savebar. `Save <noun>` only when a page has several save forms (`Save identity`, `Save homepage`). `Save draft` and `Save and continue` are their own verbs. | `Save settings` on a page with one form |
| Sentences | Flashes and toasts are sentences that end with a full stop (or `!` or `?`). `Menu created.`, not `Menu created`. A message built from a value that was written for logs goes through `fullStop(...)` (or `sentence(...)`, which also capitalises). `TestFlashCopy` checks every `SetFlash` literal. | `Saved`, `Added "X"`, `Update not started: ` + a raw error |
| Quotes | Curly quotes around a name: `Added “Zebra”.` | Straight quotes |
| Shortcuts | Static help text says `Ctrl/Cmd + X`. A string built in JS is platform aware: `Press ⌘C to copy.` on Apple platforms, `Press Ctrl+C to copy.` elsewhere (`components.js`). | A bare `Ctrl+C` on a Mac |
| Dates | `2 Jan 2006`, `2 Jan 2006 15:04` and `15:04` (`fmtDate`, `fmtDateTime`, `fmtTime` in Go templates and handlers, `hmDate`, `hmDateTime`, `hmTime` imported from `@hm/dates` in JS, not read from `window`). Day first, no comma. | `Jan 2, 2006`, `2006-01-02 15:04` for display |

### Fix list

Label renames that touch template wording are not done here. Each is owned by the PR that already edits those screens.

| Where | Today | Should be | Owner |
|---|---|---|---|
| 404 log row action and its confirm (`redirects.go`) | Remove | Delete (the entry is destroyed) | PR-G |
| Section row menu in the page builder outline (`builderoutline.html`) | Delete | Remove (the section leaves the page) | PR-C (done) |
| Settings pages that save one form (`settings/spam.html`, `plugins/plugin.html`, `settings/updates.html`) | Save settings | Save | PR-D |

| Section row menu in the page builder outline (`builderoutline.html`) | Delete | Remove (the section leaves the page) | PR-C |
| Settings pages that save one form (`settings/spam.html`, `plugins/plugin.html`, `settings/updates.html`) | Save settings | Save | PR-D (done) |
| `Save` on multi-form pages (`general.html`, `designpresets`) | mixed | `Save <noun>` everywhere | PR-D |
| `Delete menu` (`menus/edit.html`) | Delete menu | Delete | PR-D |

## Fixed values

| Thing | Value |
|---|---|
| Card footer (`.hm-card__foot`) | flex, `justify-content: flex-end`, gap `--space-2`, padding `--space-3` `--space-5`, 1px top border, no shadow, no radius |
| Plain footer (`.hm-card__foot--plain`) | same, no border, no inline padding |
| Sticky footer (`.hm-savebar`) | the same geometry, `position: sticky; bottom: 0`, flat |
| Radius, spacing, stacking, type | tokens only: `--radius-sm\|md\|lg` (6, 8, 12px), `--radius-full`, `--space-*` (half steps `--space-0-5`, `-1-5`, `-2-5`), `--z-*`, `--text-*`; no literal radii, z-index or font sizes in partial CSS (see `docs/ADMIN.md`) |
| Button | height 36px, radius 6px, 13px text, weight 500; `--sm` is 32px |
| Switch (`.hm-switch`) | 32px by 18px track, radius 999px, colours from tokens |
| Dialog footer (`.hm-dialog__foot`) | same values as the card footer |

## Static rules (UI-01 to UI-11, DC-01 to DC-04)

| ID | Rule |
|---|---|
| UI-01 | Every submit button in a `<form>` sits inside a footer region (`hm/card-foot`, `hm/foot`, `.hm-savebar`, `.hm-dialog__foot` or `hm/dialog-end`). `.hm-row` and `.hm-stack` submit groups are not footers. A primary or danger submit must not come before a Cancel or secondary in the same footer. Only two kinds of form skip the rule, and both skip it because they change nothing: `method="get"` (or no method, which is GET) and `method="dialog"`. A class (`hm-inline`) or `role="search"` does not skip it. A form with an htmx verb or a templated method is checked. Partials are checked too. Every other exception, such as one-button POST forms in rows, menus and headers, is a named allowlist entry with a reason. |
| UI-02 | The class strings `hm-savebar` and `hm-card__foot` appear only in their partials, their CSS, the four sticky editor templates, the menu editor (`pages/menus/edit.html`, whose sticky save bar keeps Save and Discard in reach beside a long tree) and the analytics dirty bar (`partials/aia-policy.html`: the robots policy form's `hm-savebar`, hidden until a choice changes, shared by Crawlers, Health and the agent page). |
| UI-03 | No raw `type="checkbox"` and no `hm/field` Control `checkbox`, except allowlisted multi-pick lists and consent gates. |
| UI-04 | `box-shadow` is `none`, a ring, an `inset` shadow, `--shadow-2` or `--shadow-3` inside the popup selector list, or `--shadow-1` on the blog editor toolbar by allowlist. A popup is matched by whole class token, with an optional `__element` or `--modifier`, so `.hm-menu` does not match `.hm-menutree`. `drop-shadow(` and `text-shadow` are banned. A button's inset ring plus inset bevel (and the primary/danger inset bottom edge) is allowed; there is still no drop shadow. |
| UI-05 | No `hm-btn`, `hm-card` or `hm-badge` class token in markup written by hand (`partials/badge.html` is the component): `class="..."`, `class='...'` or an unquoted class in pages, layouts and schemaform templates; and any `hm-btn`, `hm-card` or `hm-badge` token in JS strings and template literals (`class="hm-btn"`, `` `${base} hm-btn` ``, `className = "hm-badge"`); badges come from a server-rendered `<template>` of `hm/badge`. A leading dot (`querySelector(".hm-btn")`) is a selector and is not flagged. Allowlisted entries are the exception. |
| UI-06 | `css/pages/*.css` and other partial CSS may not set visual properties (colour, background, border, shadow, font, padding, height, outline, opacity, text-transform) on a shared component (any class block in the selector of a `components.json` entry, with its `__element` and `--modifier` classes: `.hm-btn*`, `.hm-card*`, `.hm-field*`, `.hm-switch*`, `.hm-table*`, `.hm-menu*`, `.hm-dialog*`, ...) unless the file is that component's `owner`. A page that needs another look adds a modifier in the owner's CSS (`.hm-tablist--bar`); the few layout-plus-padding uses are allowlisted with a reason. This covers descendant selectors (`.page .hm-btn`), compound selectors (`.hm-btn.page-x`, `.hm-card.is-x`, `.hm-btn[data-x]`, `.hm-btn:not(.x)`) and functional forms (`:is(.hm-btn, .x)`, `:where(.hm-card)`). A lone component class with only simple pseudos (`.hm-btn:hover`, `.hm-btn::after`) is the component's own state and is allowed. Layout-only declarations (width, margin, flex, gap) are allowed. |
| UI-07 | In a `<form>` with more than one submit button, the first submit in source order is the primary action or a hidden default submit (`hm-visually-hidden`), never a secondary such as Preview, because Enter presses the first submit. Buttons that belong to another form through `form="..."` are not counted. A bare `<input type="submit">` is never counted as primary. |
| UI-08 | A `hm/tabs` row is an underline row (no `Variant`) or the level-2 `sub` row, because its items navigate. `Variant "segmented"` is only for rows that declare what their items change: `"Changes" "view"`, one or more of `group`, `from`, `to`, `range`, `view`. `Variant` must be a literal in the template, and `Variant "tablist"` is the JS role=tablist row (buttons that switch panels in place, see JS tablists below); any value other than empty, `segmented`, `sub` or `tablist` is rejected. Chrome (UI-D9) checks the path and that the rendered links change only the declared params; the lint does not see the items. See Tabs below. |
| UI-09 | The top bar is the only place a breadcrumb trail renders. `partials/pageheader.html` mentions no `Crumbs`, and no page or layout passes `"Crumbs"` to `hm/pageheader` or writes `class="hm-crumbs"`, except the editor bar of `layouts/editor` and the editor template `blogedit.html`. Handlers set `View.Crumbs`. |
| UI-10 | A page template that renders `hm/table`, `hm/datatable`, `hm-themes` or `hm-pluginlist` has no post form outside the span between `hm/dialog-start` and `hm/dialog-end`, and no `hm-reveal` card. Create forms live in a dialog, with a `/new` page that renders the same fields. Named allowlist entries cover edit or destructive forms (the robots policy editor, deleting rules by source), per-row registry actions (Install, Update on the theme and plugin pages) the unsigned packages setting, and the Settings > Updates forms (the update policy form, the Install stats switch form, and the Check now, Update now, Unpin and Reset ID row actions beside the read-only history table). |
| UI-11 | `css/partials/field.css` sets `.hm-field__control` to `var(--border-field)` at rest and has a `.hm-field__control:hover` rule whose border is `var(--color-hai)` or `var(--border-control)`. No other CSS puts `--border-control` on `.hm-field__control` or `.hm-input-affix`. |
| DC-01 | In admin CSS (`tokens.css` and `.prose` rules excepted) `font-size`, `font-weight`, `line-height`, `border-radius`, `padding*`, `margin*`, `gap` and `z-index` use design tokens (`--text-*`, `--weight-*`, `--leading-*`, `--radius-*`, `--space-*` or `--page-pad*` for padding, `--z-*`). Allowed non-token values: `0`, `auto`, `inherit`, `1px` and `-1px` hairlines, `50%` radius, and `calc()` of tokens and plain numbers. The `font` shorthand is parsed. |
| DC-02 | Only the owner file named in `components.json` may set a contract property of a component, on a selector whose subject (last compound) carries the component class. Named allowlist entries cover the known exceptions. |
| DC-03 | The owner's base rule (and each variant rule) declares exactly the values in `components.json`; shorthands are expanded to longhands. |
| DC-04 | The table between `<!-- components:begin -->` and `<!-- components:end -->` equals the table generated from `components.json`. |

## Rendered rules (UI-D1 to UI-D14)

| ID | Check |
|---|---|
| UI-D1 | `.hm-card__foot` and `.hm-savebar`: flex, right-aligned, no shadow, no radius, 1px top border. The last visible button is primary or danger when there is one, and its siblings sit to its left. The right edge matches the parent's content edge within 1px. Sticky is allowed only on the four editor screens and the menu editor (UI-02). |
| UI-D2 | Every visible element has no shadow, a ring or inset shadow, or is inside a popup. Only the sticky blog editor toolbar is a named exception. |
| UI-D3 | `.hm-btn` sizes are 36px or 32px with radius 6px, plus the named exceptions (filter chip remove button, editor bubble and editor bar buttons). |
| UI-D4 | Every checkbox outside the multi-pick allowlist is a 32px by 18px switch with `role="switch"`. Every segmented radiogroup (`.hm-segmented` with radios) has two or more radios. |
| UI-D5 | At most one primary button per footer, per dialog footer and per page header. |
| UI-D6 | A card footer is never inside the card body, and is as wide as the card. |
| UI-D7 | Exactly one visible `nav[aria-label="Breadcrumb"]` per screen at 1440 and 390: the top bar trail (compact on phones), or the editor bar trail on the editor screens, whose back link stands in for it on phones. A second visible trail in the panel, or a trail hidden by CSS, is the violation. |
| UI-D8 | The selected item of a segmented control (pill row, radio or button) is a paper pill (`--color-paper` fill, `--color-sumi` text) with no border and a hairline ring (ring and inset shadow parts only, no drop shadow), compared against the resolved tokens, light and dark. |
| UI-D9 | Tabs navigate, pills rearrange in place. A segmented row stays on one path and its items differ only in the params it declares with `Changes` (`group`, `from`, `to`, `range`, `view`); a segmented row with no `Changes` fails. A page has at most two tab rows, and a `sub` row sits under an underline row. |
| UI-D10 | On list screens (`list: true` in `urls.mjs`) the first content block after the header is the list, no post form sits beside it outside a dialog (the bulk action form of the table, `id="bulk-..."`, and forms inside rows are not beside it), and the header has exactly one primary that opens a create page (`/new`, `/upload` or `/invite`). |
| UI-D11 | Text inputs, selects and textareas at rest have the computed border colour of `--border-field`, not `--border-control`. On `hover: true` screens the border darkens to `--color-hai` or `--border-control` under the pointer. |
| UI-D12 | On an editor savebar Delete is the far-left control, inside `.hm-savebar__aside`, at the savebar's content-box left edge. No Delete (a new item) is fine. |
| UI-D13 | Every `[data-hm-open]` resolves to a `<dialog>` that has a title (`aria-labelledby` text or `aria-label`). Live: opening it puts focus on a control inside the dialog, and closing returns focus to the opener. |
| UI-D14 | Every visible element of each component in `components.json` (and of each variant) computes to the contract values, on every screen and on the development styleguide (`<admin>/_styleguide`, `HATAMOTO_DEV=1`). Exempt: filter chips and bubbles (buttons), the editor side card and `.hm-tablecard` (cards), colour and file inputs, inputs inside an affix (`.hm-input-affix`) or an invalid field, and disabled, invalid or focused inputs. The enhanced select trigger is checked as the `select` variant of the input. |

## Allowlist

Some markup cannot be expressed through a partial. These stay raw and are listed
by name in the allowlist in `backend/internal/admin/designcheck/uilint_test.go`, each with a
reason:

| Entry | Reason |
|---|---|
| Theme toggle in `partials/sidebar.html` | One button with two icons swapped by CSS; `hm/button` has one icon slot. |
| Filters button in `partials/filterbar.html` | Label carries a live count badge made of markup. |
| Filter chip remove link in `partials/filterbar.html` | Chip pill with its own compact sizing. |
| Date range button in `partials/daterange.html` | Needs the `hm-daterange__button` class. |
| Menu trigger in `partials/pageheader.html` | Icon, label and chevron in one primary button. |
| Menu tree row buttons in `pages/menus/edit.html` | Icon buttons whose hidden text is rewritten by JS. |
| Page and blog editor toolbars, canvas, section ops, bubble, slash menu (`pages/entities/pageedit.html`, `blogedit.html`, `js/editor/*`, `js/canvas/*`) | Editor toolbar and canvas controls, not form actions. |
| Schema field and form field row buttons (`pages/schemas/edit.html`, `pages/forms/edit.html`, `schemaform/field.html`) | Small row operations (move, remove, add item) inside repeaters. |
| Consent gates: `pages/settings/plugins.html`, `packageconfirm.html`, `pluginprompt.html` | Acknowledgements, not settings, so they stay checkboxes. |
| Multi-pick lists: API scopes, webhook events, permissions, content types, terms, table select, media tile select, schemaform clear box | Pick several, so checkboxes are correct. |
| One-button POST forms in rows, menus and headers (`forms/submission.html`, `linking/suggestions.html`, `partials/pageheader.html`, `pluginhead.html`, `rowmenu.html`, `sidebar.html`, the bulk toolbar in `partials/table.html`) | Not a form footer. Each is one named entry. The webhook row actions (Pause, Resume, Send test, Retry) are `ui.Action{Post}` items rendered by `rowmenu.html`, so they are covered by its entry and need none of their own. |
| Editor CSS overrides (`blogeditor.css`, `pageeditor.css`, `listbox.css`, `popover.css`) | Editor scope; `editor_polish_test.go` asserts `blogeditor.css`. |

### How to add an allowlist entry

1. Try the partial first. If `hm/button` or `hm/field` cannot express it, extend
   the partial (and its whitelist, if the attribute is plain HTML) rather than
   bypass it.
2. Otherwise add one entry to the allowlist in
   `backend/internal/admin/designcheck/uilint_test.go`: rule id, file, a needle that matches
   the offending line, and a one sentence reason. The needle must identify the
   element (a data hook, an id, a label, a unique class), never the rule's own
   trigger such as `hm-btn` or `class="hm-btn`.
3. An entry without a reason fails the test. An entry that no longer matches
   anything is stale and also fails, so remove entries when you fix the markup.
   An entry whose needle is a bare trigger fails, and so does an entry that
   matches more than 3 lines, so it cannot exempt a whole file. A small group of
   look-alike elements may declare `Max` (never above 10) in the entry; a new
   violation beyond that count fails.
4. Reviewers reject entries whose reason is "it was easier".
