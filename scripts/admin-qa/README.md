# Admin QA harness

Browser checks for the Go admin (v1-23a). Dev-only tooling: not part of the
build, nothing here is imported by `backend/`. Uses `playwright-core` plus the
system Chrome.

## Run a local instance

```sh
docker compose up -d db                                  # Postgres 16, hatamoto/hatamoto on :5432
(cd backend && go build -o /tmp/hm-qa-bin ./cmd/hatamoto)
export DATABASE_URL='postgres://hatamoto:hatamoto@localhost:5432/hatamoto?sslmode=disable'
export API_HOST=127.0.0.1 API_PORT=8191
/tmp/hm-qa-bin serve &                                   # migrates, creates ./data/secrets
QA_USE_DATABASE_URL=1 HATAMOTO_BIN=/tmp/hm-qa-bin scripts/admin-qa/seed.sh     # owner, editor, 57 posts
```

Any free port works; avoid 8090 if something else runs there. For a throwaway
database, `createdb hatamoto_qa` and point `DATABASE_URL` at it. Forms and
submissions have no CLI: run the setup wizard with "install starter content"
(or create a form by hand) if you want those screens exercised; screens whose
data is missing are reported as `skipped` (a failure under `QA_STRICT=1`).

## One command

```sh
scripts/admin-qa/run-local.sh [consistency.mjs a11y.mjs ...]
```

With `DATABASE_URL` unset it runs `initdb` from `PG_BIN` (default:
`initdb` or `postgres` on `PATH`, else `/usr/lib/postgresql/*/bin`) in a
temp cluster on a free port, builds the backend into `/tmp`, serves it from a
temp directory (`EMAIL_DISABLED=true`; the binary embeds this checkout's admin
files, and `HATAMOTO_ADMIN_DIR` is left unset because it re-parses every
template on every request, about 1s each), waits for `/admin/login`, runs `seed.sh`, then
the scripts. The server and cluster are removed on exit (trap), and the exit
code is the first failing script's. With `DATABASE_URL` set (CI service) no
cluster is created. Missing Postgres binaries or Chrome exit 2 with a message.
`PW` defaults to `scripts/admin-qa/node_modules/playwright-core` (`npm ci`
there; `package-lock.json` is committed) when the old dev path does not exist.

`scripts/ship.sh` runs `make design-check` and this script (strict) on the PR head commit
in a temporary worktree when the GitHub `ci` workflow is not active; with `ci` active
it waits for the PR's status checks (`admin-ui` must pass) instead. In the
local mode missing Postgres or Chrome abort the ship rather than skip.

## Install and run

```sh
cd scripts/admin-qa && npm install      # or: PW=/path/to/playwright-core/in/another/node_modules
export BASE=http://127.0.0.1:8191
node scripts/admin-qa/a11y.mjs [screen ...]
```

Environment: `BASE` (default `http://127.0.0.1:8090`), `PW` (playwright-core
path, default `scripts/admin-qa/node_modules/playwright-core`), `CHROME`
(default `/usr/bin/google-chrome`), `QA_EMAIL` / `QA_PASSWORD` (owner, default
`owner@qa.test` / `qa-password-123`, as seeded), `QA_EDITOR_EMAIL`, `QA_ROLE`
(`owner` or `editor`: runs only the screens that role may open), `OUT`
(results dir, default `scripts/admin-qa/results/`, gitignored), `QA_STRICT=1`,
`QA_LEGACY=1` (use the pre-v1-23a paths, for smoke runs on an old build).
`consistency.mjs` and `consistency-families.mjs` run their width x theme sessions
concurrently: `QA_JOBS` (default 4; `QA_JOBS=1` is the old serial run).

## Files

| File | Purpose |
|---|---|
| `urls.mjs` | URL inventory on the final URL map: name, path, mockup screen it pairs with, roles, extra a11y states (editors: `entity-edit-post` with slash, bubble, more menu and SEO tab states; `entity-edit-page` with section menu and add-section states; `builder-shell` and `builder-outline` for the page builder, see below; a state with `press` clicks, then presses keys) |
| `lib.mjs` | login, theme cookie, path tokens (`{post}`...), result files |
| `a11y.mjs` | axe WCAG 2.0/2.1/2.2 A+AA per screen at 1440 light, 1440 dark, 390, plus open popover states. Exit 1 on any violation or non-200 |
| `keyboard.mjs [chromium\|firefox\|webkit] [screen ...]` | Tab walk on every screen (1440 light, 1440 dark, 390: visible, focus indicator, not obscured, skip link first) and widget scripts: skip link, theme toggle, row menu popover, confirm dialog, tabs, and the editor widgets: slash menu, bubble toolbar, blog sidebar tabs, page section menu, add-section dialog, and the builder scripts `builder-tree`, `builder-palette`, `builder-tabs`, `builder-sheet`, and the Style tab controls (colour popover Tab wrap and Esc, token commit, number arrows, one undo per slider drag, box-model tab order and Link) |
| `perf.mjs [screen ...]` | Chrome, no throttling, `--disable-features=Prerender2`, median of `RUNS` (5) loads per primary screen: CLS < 0.05, INP < 200 ms after scripted interactions, navigation (click to DOMContentLoaded and responseEnd) < 300 ms |
| `nav.mjs` | Navigation mode: sidebar click is a document navigation with no fetch/xhr (ISC-19), `speculationrules` present (ISC-21), scroll restored after row click and back (ISC-22), no editor modules on the entities list (ISC-23) |
| `consistency.mjs [screen ...]` | Admin UI rules UI-D1..UI-D13 (`docs/ADMIN_UI_RULES.md`) as computed-style checks on every screen, owner and editor, 1440 and 390, light and dark: footer geometry and order, shadows, button size, switches, one primary per footer, footer inside card, one breadcrumb trail, selected segmented items (paper pill with a ring), tabs vs pills, list first, calm inputs, Delete far-left on editor savebars (UI-D12), `data-hm-open` dialogs titled with focus in and back (UI-D13). Screenshots `<name>-<width>-<theme>.png` (`editor-` prefix for the editor role) go to `QA_OUT` (default `results/consistency/`, gitignored). Prints `RULE role:screen width theme selector message (value)` per violation and exits 1 on any |
| `consistency-families.mjs [screen ...]` | One look per control family: measures computed styles of the selected and unselected segmented option, track and height (the builder phone tabs included at 390), table header cells, stat value and label, current sidebar item, selected media tile, tab underline and removable chips on every owner screen (1440 and 390, light and dark), groups them per family, theme and width, and prints `family | distinct sets | screens`. Exits 1 when a family has more than one value set, or when a family was never found (`ALLOW_EMPTY` is empty: the media picker's selected tile and the filter chip are reached by script probes). Writes `families.json` to the results dir. `--self-test` needs no server: two differing segmented controls must be reported. Part of the `run-local.sh` default list and of `make ui-check`; run it alone with `QA_SEED_ANALYTICS=1 bash scripts/admin-qa/run-local.sh consistency-families.mjs` |
| `run-local.sh [script.mjs ...]` | Boots a throwaway Postgres 16 and the server, seeds, runs the scripts (default `consistency.mjs consistency-families.mjs`), tears everything down. See below |
| `crawl.mjs` | Unknown-route crawl: every `/admin` page reachable from the sidebar, sub navs and tab rows (owner and editor, 1440) must be in `urls.mjs` or its `crawlIgnore` list with a reason. `crawlFrom` follows links on every scanned page with no depth limit, capped by `MAX_PAGES` (200). `--self-test` needs no server. Run by `run-local.sh crawl.mjs` |
| `select-manual.mjs` | Scratch check of the styled Select (keyboard pick fires htmx, light and dark). Not part of the suite; `SHOTS` sets the screenshot directory |
| `mobile-table.mjs` | Standalone dev check (not in the gate): at 390px the bulk checkbox is the hit target at its own centre, and hand-written tables keep their column headers |
| `aia-states.mjs` | The AI analytics charts (js/aia) on a seeded instance: every figure ends with an SVG or a state, legend toggle `aria-pressed` and the last series cannot be hidden, ArrowRight moves the live region, a failing data request shows an alert with a working Retry, no infinite animation under reduced motion, no CSP violation. `QA_SEED_ANALYTICS=1 run-local.sh aia-states.mjs` |
| `shots-address.mjs` | Screenshots of the admin address flow from a fresh install (no `seed.sh`): the wizard step and its typed confirm, Done, Security, the change confirm, then a reboot on `/admin` for the upgrade banner and the Keep confirm. Boots its own server: `HATAMOTO_BIN=<built hatamoto> DATABASE_URL=<EMPTY scratch database> [PSQL=...] node scripts/admin-qa/shots-address.mjs`. Writes to `scripts/admin-qa/shots-out/address/` (or `SHOTS`), 1440 light |
| `busy-guard.mjs` | Standalone dev check (not in the gate, takes ~45 s): a plain POST form locks while pending and unlocks after 10 s; a multipart upload form stays locked |
| `select-manual.mjs` | Standalone dev check (not in the gate): the styled Select opens, picks and fires htmx by keyboard and mouse, light and dark; `SHOTS=dir` sets the screenshot directory |
| `segmented-click.mjs` | A real mouse click (page.mouse) on the Allow/Block segmented control checks the radio and shows the save bar; arrow keys move it. Guards the radio input covering its label (a click on label text focused `main[tabindex=-1]` and scrolled the page between mousedown and mouseup) |
| `main-focus.mjs` | A mousedown on plain text in `<main>` does not focus it or scroll the page; the skip link moves focus to main and Tab or the next click drops its tabindex |
| `parent-picker.mjs` | The entity parent picker: closed it shows the chip and Change; open it shows Done only (creates a "QA guide" type with a `{parent}` pattern when the site has none) |
| `tooltip-clip.mjs` | Hovering the agents table's "Ignores robots.txt" tooltip (`.hm-table__scroll [data-tooltip]`) at 390, 700 and 1440 leaves the scroll box and page scroll size unchanged (a clipped tip grew the scroll area) |
| `frame-tickets.mjs` | Frame tickets (A3): builder canvas, blog preview and a plugin page load with their one-time ticket (screenshots in `SHOTS`); an iframe without a ticket or with a reused one gets the admin 404, from an admin page and from a public page. `run-local.sh` points `HATAMOTO_PLUGIN_DIR` at `fixtures/frame-plugin` (a UI-only plugin with an empty wasm module) when this script is requested |
| `tabs.mjs` | No tab or pill row (`.hm-tabs__list`) has scrollHeight > clientHeight on analytics crawlers, settings types/redirects, plugins, 1440 and 390, light and dark |
| `seo-panel-editors.mjs` | One SEO panel everywhere (A2): the post editor, the page entity form, a term edit page (created as "QA SEO term" when missing) and the builder SEO drawer all show the Search, Social, Schema and Advanced tabs and one `hm-seopanel` root; the term panel has no body checks; Escape closes the drawer and returns focus to the SEO button; a title edited in the drawer and saved with the page persists after a reload (B1) |

Every script exits non-zero when a bar is missed and writes JSON to
`results/` (gitignored); paste the numbers into the QA table of the docs todo.
Primary screens: entities, analytics overview/crawlers/content/referrals/health, plugins,
settings general/media/types/forms (flagged `primary` in `urls.mjs`).
Firefox and WebKit come from `~/.cache/ms-playwright` for `keyboard.mjs`.

## Screenshots

```sh
node scripts/admin-qa/shots.mjs [screen ...]   # SHOTS=dir MOCK_BASE=http://127.0.0.1:8097 FULL=1
node scripts/admin-qa/shots.mjs --table        # markdown comparison table (screen x width x theme)
```

Writes `<name>-<1440|390>-<light|dark>.png` per screen to `shots-out/`
(gitignored), the mockup shot to `mock/`, and for paired screens a composite
`...-sbs.png` (mockup left, admin right). The mockup server is
an out-of-repo static server for the admin mockups (`MOCK_BASE`, default port 8097).
Fails when any inventory screen is not 200 or a paired mockup is unreachable.

Editor screens only: `node a11y.mjs entity-edit-post entity-edit-page`,
`node keyboard.mjs chromium entity-edit-post entity-edit-page`, and
`MOCK_SURFACE=planner node shots.mjs entity-edit-post entity-edit-page`
(needs the seeded pages; run `seed.sh` with `BASE` set).

## Builder screens

`builder-shell` (`/admin/entities/{page}/edit`) and `builder-outline`
(`?outline=1`, the no-JS form shown as the page) open the page whose name is
`QA_PAGE_NAME`; it must carry a `page_doc` (a page without one opens the v1
editor, which `entity-edit-page` covers). `builder-shell` states run in order
on one page: `navigator-focus` (click a tree row), `inspector-style` (the Style
tab of the selected element), `palette-open` (`/` from the tree), `shortcuts-open`
(the Shortcuts dialog).

```sh
export QA_PAGE_NAME='QA Builder Only'
node scripts/admin-qa/a11y.mjs builder-shell builder-outline
node scripts/admin-qa/keyboard.mjs chromium builder-shell
```

`keyboard.mjs` runs four builder widget scripts whenever it runs (they need that
page; each undoes what it edits):

| Script | Checks |
|---|---|
| `builder-tree` | one roving tab stop; Up/Down/Home/End select; Left/Right fold and move to parent or first child; Alt+arrows move, indent and outdent keeping focus; Delete removes; Ctrl+Z restores the structure |
| `builder-palette` | `/` focuses the search; the filter narrows; ArrowDown/Up move between search and list; Enter inserts and returns focus to the tree; undo removes the inserts |
| `builder-tabs` | Enter moves focus into the inspector; Left/Right/Home/End move between Content, Style and Advanced with one tab stop and a matching panel |
| `builder-sheet` | `?` opens the shortcut dialog, Tab stays in it, Esc closes it and returns focus to where it came from |

`node scripts/admin-qa/builder-states.mjs [chromium]` (same `QA_PAGE_NAME` page; it inserts a divider widget per check, because the seeded v1-converted rows are not canvas nodes, and undoes it) checks the State switch by keyboard (one tab stop, arrows select), a hover-lift (`ty:-4px` on Hover moves the forced canvas element, Normal restores it), forced hover at phone width, State and force cleared on a selection change, reduced motion, and axe on the Style tab (every group opened), the open colour popover and the box model. Style groups are disclosure buttons (Transform and Transition start closed): the script opens them first. `urls.mjs` `builder-shell` also has the states `box-model` and `colour-popover-open` for consistency, a11y and shots.

`scripts/admin-qa/run-local.sh builder-ux-tasks.mjs` (builder UX shell, UX-1) runs the ten tasks of the builder-ux-shell plan (`ref/tasks.md`) (T1 hero on a new page, T2 feature grid and the Phone view, T3 brand colour and corners from Site styles, T4 hover shadow as a class on two cards (State Hover, Effects, Save as class..., the second card takes the class; the class name is four random letters and is deleted afterwards), T5 hide on phone, T6 copy a section between pages, T7 section to block, T8 typo, bold and link on the canvas, T9 undo three steps and then restore one element from an earlier saved version, T10 padding of a nested element by one canvas click on the deepest element and the box model). Each task runs by mouse and by keyboard at 1440 px (3 timing runs each, `QA_RUNS`) and once at 390 px, and T1, T2, T5 and T8 also run a first-use path with visible buttons only (no shortcut, not even outside a field). T1 and T2 also run a "plus" path by mouse only (the layout picker of "Add a section", no search and no key; T2 starts with the Navigator showing, since an empty page opens on the Add tab and opening the Navigator is one more click than its baseline of 3 allows). T5 hides the heading with the Show on switches (the keyboard run keeps the H key). T8 by keyboard edits the selected text with Enter, bolds a word with Ctrl+B and makes the link with Ctrl+K (the page list is checked to offer About; the browser's own suggestion popup takes no synthetic keys, so the address is typed). T9 counts only its undo part against the baseline (restoring an element had none: it was impossible), and prints the whole run next to it. The counting actor refuses what a mode forbids (a mouse click in keyboard mode, a shortcut in mouse or phone mode) and counts clicks, keys (typed characters and shortcuts) and nav presses (Tab, Enter, Escape, arrows). The baselines are the UX-0 scripted expert paths (mixed mouse and keyboard), so a task passes when the mouse run or the keyboard run is `<=` the baseline in every column it has, the first-use run is too, and the median wall time is `<=` 1.5 x the baseline; phone runs must complete and are the first phone measurements. It also checks the status chip, the device announce and chip, the More menu order, the Add tab, Ctrl+K, the conflict and recovery banners (the canvas does not move), the one-time hints, the phone dock, Site styles for owner and editor (the editor is logged in as `QA_EDITOR_EMAIL`, default `editor@qa.test`), and checks the hit-test (a click selects the deepest element, the breadcrumbs name it), the element toolbar (outside the element, built once, arrow keys), the zoom floor, the Tablet and Phone band, Ctrl+K from the canvas frame and the element restore dialog, and runs axe (zero violations) on the start panel, the Add tab, the builder with a selection, the Style tab, the layout picker, the link panel, the command palette, the Site styles drawer, the phone dock, the restore dialog and dark mode (Style tab and palette). Output: `results/builder-ux-tasks.json` (per task and mode: clicks, keys, nav, seconds, pass; checks; axe). `QA_ONLY=<regex>` runs only matching task ids or check names, `QA_VERBOSE=1` prints errors in full. Scratch pages are made through the Create menu route and deleted after each run. `builder-ux-shots.mjs` takes the plan's screenshots of the shell and `builder-ux2-shots.mjs` those of the canvas and inspector (`SHOTS_OUT`). Regression pass for the builder shell, in one invocation:

```sh
scripts/admin-qa/run-local.sh builder-ux-tasks.mjs builder-direct.mjs builder-inline.mjs builder-settings.mjs builder-states.mjs keyboard.mjs a11y.mjs consistency.mjs consistency-families.mjs
```

`node scripts/admin-qa/builder-direct.mjs [chromium]` (E4 direct manipulation, by mouse; the browser context is granted the clipboard permissions) drives the "+" picker (3 columns), copies the new section from the first page and pastes it on a second one through the system clipboard and `POST .../builder/paste`, wraps it, renames rows by F2 and by double-click, Ctrl+click selects two rows and deletes them (one undo restores both), opens the element menu with a canvas right-click and Shift+F10 in the tree, jumps back in the History panel, runs axe on the open picker, menu and history, and drags the move handle straight after load with the `drag` chunk delayed (so a press before the chunk arrives is covered). It needs `QA_PAGE_NAME` (default `About`) and a second page, `QA_PAGE2_NAME` (default `Contact`); no scratch page is created: every check undoes its edits and the last one reloads both pages and compares their documents. `QA_VERBOSE=1` prints the browser console and the full Playwright error. `keyboard.mjs` adds `builder-menu`, `builder-rename`, `builder-isolation` (shortcuts typed in the inspector field and the rename input leave the page alone), `builder-multiselect` and `builder-history`; `urls.mjs` `builder-shell` adds the states `context-menu`, `history-open` and `structure-picker`.
`node scripts/admin-qa/builder-inline.mjs [chromium]` (E5 inline rich text) pastes a generated 60-node section (a heading, a text and 58 more text nodes) onto `QA_PAGE_NAME` (default `About`) through the clipboard, then: bold and a link on the heading and on a text node by mouse (a dragged selection, the bar's B and Link buttons, the dialog with "Open in new tab") and by keyboard (Ctrl+Shift+Left, Ctrl+B, Ctrl+K); `javascript:` refused in the dialog; Update, a reload, and the stored `html` holds only allowed tags with `rel="noopener noreferrer"` on new-tab links; hostile `html` staged through `POST .../builder/stage` renders clean (no script, img, on*, `javascript:` or `//host` link, input `rel` dropped); a Word-like rich paste and a plain paste; Esc restores; Shift+Enter makes a `<br>` in a text and nothing in a heading; 200 typed characters send text posts and no `patch` to the frame; a component's body click selects it and focuses its inspector editor and typing in its headline puts the stored text back (Q7), with no page error; keystroke cost (Chromium only): the time between the window's capture and bubble phase of `keydown`, `beforeinput` and `input` on the 60-node page, p95 <= 2 ms, next to a bare contenteditable in the same frame (Event Timing only lists events slower than 16 ms, so it is used only to catch a slow one); axe with the bar and the link dialog open; the inspector editor by keyboard at desktop and phone width. The last check deletes the pasted section, saves the page as it was and compares documents (the page is published, so the harness uses Update). `QA_VERBOSE=1` prints the messages between frame and host.

`node scripts/admin-qa/builder-settings.mjs [chromium]` (E6 element settings) pastes a generated section (a lightbox gallery, images with lightbox and caption, anchor, new-tab and rel-only buttons, linked containers, a container with a button inside, 30 fillers and an anchored target) onto `QA_PAGE_NAME` (default `About`), saves with Update and checks the public page as a signed-out visitor under the real CSP: clicking the `#qa-target` link scrolls there (scrollY, `:target`); `target`, `rel`, `aria-label` and `title` on the links; a linked container is one `<a>` named by its aria-label or its text, with its anchor id; CLS is exactly 0 for the library images (CDP-throttled, cache disabled, at least one image still loading at first layout, else the run is invalid) and the same page with `width`/`height` stripped must measure above 0 (so the check can fail; a failing run prints the shifting elements); the lightbox opens by Enter on the link, focuses Close, traps Tab (single image and gallery), ArrowRight/ArrowLeft wrap through the gallery, Esc closes and returns focus; axe on the page and on the open dialog; no CSP violation. In the builder: the Link field's datalist holds pages and `#anchors`, Open in new tab and the rel boxes write `target`/`rel` and clearing the link removes them, the Advanced Anchor lowercases and refuses a duplicate or bad id, a reserved anchor and a link on a container with a button are refused by the save (422, message shown), the Advanced tab fits at 390 px, axe on the Content and Advanced panels. A section left by a failed run is removed first; the last check deletes the section, saves and compares documents. `seed.sh` adds the 1200x675 media image (`make-png.mjs` writes it, no dependencies) the CLS check needs.

The scripts that need a seeded post or the v1 page editor (slash menu, bubble
toolbar, section menu...) report `no post seeded` or a timeout when
`QA_PAGE_NAME` names a builder page and there are no posts; run those against
the seeded instance. Browser-level builder checks (consistency, performance,
no-JS, conflicts) live in a harness kept outside the repository (`builder-shell.mjs`).

`settings-updates` is in the inventory; `a11y.mjs` and `keyboard.mjs` run 390 px in light and dark. `QA_VERSION=v1.4.1` makes `run-local.sh` build a release-stamped binary (the default build is a dev build, which shows the dev-build state), and `updates-shots.sh` shoots every Updates status and the owner banner (see below).

### Updates screenshots

```sh
scripts/admin-qa/updates-shots.sh      # writes scripts/admin-qa/shots-out/updates (or $SHOTS)
```

It boots four throwaway instances through `run-local.sh` (release-stamped build, update
URL pointing at a closed local port, so nothing leaves the machine): one for every status
and banner (the status row is written with `psql`; banners wait out the 30 s cache), one
with `HATAMOTO_UPDATES=auto` (locked mode), one with `HATAMOTO_UPDATE_CHANNEL=canary`
(`env_locked_canary`, the channel is locked to canary) and one with an invalid update URL
(unavailable). `QA_VARIANTS="up_to_date banner_blocked"` limits `updates-shots.mjs` to some
variants; `run-local.sh` treats arguments as scripts, so variants go in that variable.

### Opaque-origin sandbox (`opaque-origin.mjs`)

Proves in a real browser that a page served with the enforced sandbox CSP runs in an opaque
origin: `self.origin === "null"`, a credentialed `fetch('/admin...')` reaches the server with no
`Cookie` header (recorded server-side, with a same-origin control fetch that does carry it),
`document.cookie` and `localStorage` are refused, and a public form submits through hm.js with
`Origin: null` and its `hm_ts` token. It needs the test-only `hatamoto_qa` build, which adds the
fixture page `/__qa/opaque`, the capture `/__qa/seen` and `HATAMOTO_QA_SANDBOX_ALL=1`:

```sh
QA_TAGS=hatamoto_qa HATAMOTO_QA_SANDBOX_ALL=1 scripts/admin-qa/run-local.sh opaque-origin.mjs
```

The tag must never ship. Check a production binary:
`go version -m <binary> | grep -q hatamoto_qa && echo LEAK`. A Go test
(`TestQASeamAbsentInDefaultBuild`) pins that the default build has no such route.
This script is not in the CI script list; run it locally.

Settings > Locales (`settings-locales`, `settings-locale-addresses`, `settings-locale-business` in `urls.mjs`): the default seed is single-locale, which is the check for the add-only state of the Locales tab and the empty Business details tab. Seed several locales through the Add locale dialog to check the list, the Public switch and reorder.
