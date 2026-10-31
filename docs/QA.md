# Manual QA checklist

What the automated tests cannot see. Run it before a release that changes the
admin, and for the editors (v1-12/13) once they exist. The no-browser a11y
checker (`internal/admin/a11y`) covers names, landmarks, headings, ids, tables,
dialogs, CSP-hostile markup and contrast; this covers behaviour.

Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address (`hatamoto admin-url`). The URLs in this checklist and in `qa/` are the pinned ones, so they stay literal `/admin`.

`test/admin-attack/run.sh` runs the admin attack suite against a real server (browser, state and discovery attacks, plus the plugin sandbox), and exits non-zero if any attack succeeds; run it from the repo root with Chrome and Postgres available and `PW` pointing at `playwright-core` (`test/admin-attack/README.md` has the exact command).

## Keyboard (no mouse)

* Tab through **login -> setup wizard -> users -> a table -> a form**: the order
  follows the visual order, every control gets a visible focus ring, nothing is
  skipped or trapped. The skip link is the first stop and lands on the main region.
* Open and close the invite card, a `<dialog>`, a popover menu (Esc closes, focus
  returns to the opener).
* Table: sort links, the search box, bulk select with the keyboard (Space), the
  row action buttons announce which row they act on.
* Repeaters (schema forms): move up/down and remove work without a mouse; drag is
  an extra.

## Screen reader (one pass on a table and one form)

* The table is announced with its caption, column headers and sort state; row
  headers (the first cell) are read with each cell.
* The form announces label, required state, help text and, on error, the error
  summary (focus moves to it) and each field's message.
* Toasts and flash messages are announced once (`role=status` / `alert`).

## Zoom, reflow and motion

* 200% zoom and a 320 px wide window: no horizontal scroll except inside tables;
  the sidebar collapses above the content.
* Dark mode (Sign-out area, "Dark theme"): text, borders and focus rings stay
  readable; status colours keep their text label.
* "Reduce motion" in the OS: no spinners spin faster than a blink, no animation.

## Flows

* Fresh install: `serve` with only `DATABASE_URL`; open `/admin`, enter the setup
  code, finish the wizard with starter content; the site, `/blog` and the contact
  form work.
* Sign in with a wrong password five times: the message never says "locked".
* Settings > Email: save with good and bad credentials (bad shows the SMTP error and saves nothing), send a test email, switch to the server's settings and back, check Security None is refused for a non-loopback host; the setup wizard Email step with Skip and with Save.
* Forgot password with and without SMTP; invite an address; accept the invite in a
  private window; the link works once.
* Session expiry: delete the session cookie (`__Host-s`, or `s` over plain HTTP) while on a page and press a
  link: you land on the login page and are sent back afterwards (a lost session on
  a form POST lands on the login page without a target).
* Idle session: set `last_used_at` of a session 8 days back and load a page: you
  are signed out, and the session row is gone (the login page does not bounce you
  back in).
* Narrow window (390 px): the sidebar is a "Menu" button, the users table scrolls
  inside its card (no sideways page scroll), flashes appear as toasts.
* API keys: Delete, Revoke and New secret each ask first (a dialog; with JS off, a
  confirmation page). Reloading the page that shows a new secret creates nothing.
* Create an API key: the secret appears once, the list shows only its prefix.

## Plugin admin pages and theme preview (v1-18)

Run before a release, in a real browser (the iframe sandbox and CSP are browser behaviour):

1. Install a plugin that ships `ui/` (for example `examples/` reference plugin packed with `hatamoto plugin pack`). Open its page from the sidebar. It loads, `hatamoto.route(...)` returns data, `hatamoto.host(...)` works for a granted function and answers `permission_denied` for one that was not granted (the admin bridge keeps that code; plugin code itself sees `capability_denied`).
2. In the page's console (frame context): `document.cookie` and `localStorage` throw `SecurityError`; `parent.document` throws; `fetch('/')` fails on `connect-src`; `eval('1')` is refused.
3. Install a theme, click Preview: the public site shows it, the admin theme card does not say "Live", and a private window on the same URL shows the active theme. `/?hm_theme_preview=off` ends the preview.
4. Activate the theme with a section type missing: the page lists it; after activating, the pages that use it render the HTML comment for unknown types.

## Rich-text editors and the removed page editor (B10)

Source: `docs/ADMIN.md` "Page editing and the shared editors". The v1 section form editor no longer exists; the
builder checklists below cover page editing.

* Open a page that only has `page_sections` (create one with `hatamoto entity create --attributes-file`): the builder
  opens with the page's content as components, and Versions shows no new version until you save. Save once: one new
  version, and the public page is unchanged.
* Open a page whose `page_doc` is damaged (edit the row by hand): a recovery screen with links to Versions and to the
  entity details form, not a form.
* On a schema-form screen with an html or rich-text field (a custom entity type, or the blog editor) the editor
  toolbar mounts; with JavaScript off the plain textarea stays and saves.
* `/admin/entities/<id>/edit/preview` still serves the blog editor's preview; a classic section-form `POST /edit` is
  redirected and writes nothing; form-mode `POST /edit/render` is 415.

## Builder editor (B3)

Source: `docs/BUILDER.md` "Editor". Use a draft page with a builder doc (a container with a heading and a theme
section) and a published one. Automated: every save and render shape, the outline form and the one-decoder rule
(`go test ./internal/admin ./internal/server ./internal/builder`), the pure client modules
(`node --test scripts/admin-qa/unit/`) and the JS budget. The checks below are what they cannot see.

* Open a page with no `page_doc`: the builder on its in-memory conversion, no notice. Open one whose layout is
  unreadable: the recovery screen.
* New page: Create > Page opens the builder in one step, with the start panel on an empty page.
* Consistency: edit text, a prop, a style at base, md and sm, insert, move, duplicate and remove; Save; reload. The page
  is identical to what you left, and the public page shows it.
* Undo and redo each of those edits, with the keyboard and the buttons; the canvas does not reload.
* Keyboard only: build a three-section page with the Navigator, the Add tab (`/`) and the inspector (Enter). Tab
  never gets trapped; focus is visible; the tree announces level, expanded and selected.
* Shortcut sheet: `?` opens it, every row matches the key's behaviour, Escape closes it with focus back.
* Shortcut keys do nothing while typing in an input; `Ctrl/Cmd+S` still saves.
* Drag from the palette and from the structure onto the canvas: the indicator shows before, after or inside; Escape
  cancels; a drop into the dragged node itself is not offered.
* Draft: edit and wait; the bar says Saved and the version list shows an autosave.
* Published page: edit; the bar says the change is kept for you (on the server); nothing is autosaved; Update page saves.
  Close the tab before Update, reopen, also after signing out or in another browser: the recovery copy is offered.
* Conflict: open the page in two tabs, save in one, then edit in the other. The banner offers Keep my version and
  Load their version (and Compare in Versions); Load their version shows the other tab's version; Keep my version keeps yours.
* Publish an untitled new page: refused with a message. Give it a title and publish.
* Component content: edit a repeater, a media and an html field in the Content tab; a validation error shows on the
  field.
* Without JavaScript (or `?outline=1`): edit props and component content; move up and down, add after and inside,
  delete, save. Reload shows the same doc the JavaScript builder would have written.
* A large converted page (six html sections, about 150 KB) opens and accepts a content edit in under a second.
* First open of each lazy panel (Style tab, component form with repeater/media picker, rich text/html
  fields): no empty flash, keyboard-only works, console clean; retry after a blocked chunk.
* Screen reader pass on the tree, the tabs and the outline form.
* Style depth (E1): a header container with Position Sticky, Top 0, Stack order 10 and a background stays at the top
  while the page scrolls (public page and canvas); setting Position reveals the offsets without losing focus. A banner
  hidden on Desktop and Tablet and visible on Phone shows only at 390 px; a desktop-only one hides at Tablet and below.
  A hero with a media-library image plus `linear-180-000-accent` shows both layers. The Style tab is operable by keyboard
  alone (groups, selects, the number field of Stack order and Order) with axe at 0.
* States (E2): a card with Shadow Small, Transition "Transform and shadow" (200 ms) and, on Hover, Move Y `-4px` and Shadow
  Large. In the editor, the State switch is one tab stop; arrows select Hover and the canvas shows the lift (force
  preview); Normal puts it back; picking another element returns to Normal. Publish: the public page lifts on a real
  hover and on keyboard Focus when a Focus value is set. With the OS set to reduced motion the card still changes but does
  not animate, and the public CSS holds no `transition` outside `prefers-reduced-motion:no-preference`. At 390 px, a
  Desktop Hover value still applies on hover, and a Phone Hover value replaces it. The Style tab is operable by keyboard
  alone with axe at 0 (`scripts/admin-qa/builder-states.mjs`).
* Light, dark and 390 px wide: panes stack and nothing overflows.

**Harness record (2026-10-18).** The out-of-repo builder harness (`builder-shell.mjs`) against a local server: Chrome 154 133/133 (300 s soak on a 60-node page: heap 3.84 MB at load, 5.28 MB after 1305 ops;
keystroke p50 1 ms, style paint p95 13 ms, structural paint p95 21 ms; 150 KB html-section page opens in 636 ms, edit 442 ms), Firefox 157 133/133, WebKit 27.2 129 to 130/133 (the `select` CSP console noise, plan note F7).
`a11y.mjs builder-shell builder-outline`: 0 violations; `keyboard.mjs`: every builder script passes (the v1 page-editor and blog scripts fail on a builder-only page with no seeded post, expected). JS budget 90.3 KiB of 120.


## Builder widgets (B4)

Source: `docs/BUILDER.md` "Widgets". Automated: schema, golden, lint, budget and page tests
(`go test ./internal/builder ./internal/render ./internal/admin/a11y ./internal/server`), the pure client tests and the
JS budget. The checks below need a person.

* Palette: every widget is listed under its group; inserting `tabs`, `columns`, `accordion` and `buttons` adds the
  seeded children, and one undo removes them.
* Edit one prop of each widget; the canvas matches the public page after Save.
* Rich text: type, bold, a link; the caret stays put while typing. Paste a script tag: it is gone after Save.
* Image: pick a file from the media picker; the alt warning shows until alt or Decorative is set.
* Layout: direction, justify, align, grid columns 1 to 4 and a custom number, rows, and span on a grid child change the
  canvas.
* Device toggle: a value set at Tablet shows greyed "Inherited from" at Phone; Reset removes only the active device's
  value; "Hide on" Tablet hides the node in the canvas at that width and the structure tree shows the badge; `h`
  toggles it.
* Public page with tabs: Tab reaches the tab list, arrows, Home and End move and activate, the panel follows. With JS
  off all panels show.
* Public page with a video: the network panel shows no request to the provider before the click; the click starts the
  player.
* Light, dark and 390 px wide: palette groups and the style buttons wrap and nothing overflows.

## Builder design system (B5)

Source: `docs/BUILDER.md` "Design system". Automated: model, render, cache, admin and page tests, the pure client
tests and the JS budget. The checks below need a person.

* Theme > Design system: change `accent` and the `space-4` step, save; with an opted-in theme reload a
  public page and the editor canvas: both follow without re-saving the page. With a theme that has not opted in the
  notice shows and nothing changes.
* Style presets: add `card` (padding, background, radius, shadow), save; in the editor apply it to a container: the
  look changes, the structure tree row says "preset card".
* Change one padding on that node: "Differs from preset" lists it and the row says "differs"; Reset to preset clears
  it with one undo step; Detach to custom keeps the look and drops the reference.
* Edit the preset in Settings: reload two pages that use it; both change.
* Copy style from one block (`Ctrl/Alt+C` from the structure tree), select another, `Ctrl/Alt+V`: it takes the look;
  one undo restores it. Plain Ctrl+C in a text field still copies text.
* Save as preset from a styled node: the canvas reloads and shows it; saving the same name again shows the message.
* Screen reader: the Preset group, the drift list and the Reset buttons are announced with their names; the tree row
  reads "container, preset card, differs".
* Light, dark and 390 px wide: the Preset panel and the settings grids wrap and nothing overflows.

## Builder reuse (B6)

Source: `docs/BUILDER.md` "Reuse". Automated: model, render, entity, admin and integration tests (real cache), pure
client tests, the JS budget. The checks below need a person.

* Select a section, **Save as block…**, name it: the canvas looks the same and the Navigator row reads "Block: <name>";
  one undo restores the section.
* Add the block to a second page (Add tab, Blocks), publish both. Open **Edit block**, change the headline,
  **Save block**: reload both public pages (logged out): both changed. Reload both canvases: both changed.
* The block builder has no Publish and no autosave; the public URL of the block is a 404.
* Delete a block in use: pages still render, the ref shows "Missing block"; Detach is disabled.
* Detach a ref: the page looks identical, the tree has plain nodes; one undo brings the ref back. Used on N pages
  matches the pages that use it.
* Try to make block A contain block B and B contain A: the save is refused with the path.
* Save a selection as a template, insert it twice on one page: both appear, and a section template from before still
  inserts.
* Owner: Layout parts, create a header part, **Save part**, assign it. With a theme that opted in the header changes on
  the next load; with the default theme nothing changes. An editor cannot open the Layout parts page.
* Screen reader: palette groups, the ref inspector buttons and the Layout parts selects are announced with names.
* Light, dark and 390 px wide: the palette footer, ref panel and Layout parts page wrap and nothing overflows.

## Builder dynamic data (B7)

Source: `docs/BUILDER.md` "Dynamic data". Automated: model, render, server and admin tests, integration tests with the
real cache, the loop benchmark, pure client tests, the JS budget. The checks below need a person.

* Select a heading, **Bind**, pick "This page: Name": a chip shows and the canvas shows the page name. **Unbind** and
  Ctrl+Z each undo one step. The Bind select never offers "Loop item" outside a loop.
* Insert **Query loop** (Data): seeded title, excerpt, button and "Nothing here yet." Set schema `post`, limit 6: the
  canvas shows at most 3 items, only the first selectable.
* Publish, open the page logged out: 6 items and a pager when `paging` is `pages`; `?page=2` has a self canonical and
  prev/next; a page past the end is a 404. Edit a post: the paged page changes.
* A draft post, a block and a hidden person never appear. A loop over an empty type shows its empty state.
* Theme > Layout parts: an archive part with a `slot` (Replace on) and a loop `from: archive`: the blog archive shows
  only builder markup and its pager works; the default theme's archive is unchanged.
* Screen reader: the Bind button names its prop, the chip and Unbind are announced; keyboard only works end to end.

## Blog editor (v1-13): the writing checklist

Source: `docs/ADMIN.md` "Blog editor". Use a post type (`post`), one draft and one published
post, a site with a theme, the media library with at least one image, and a user who is an
owner (editors cannot create terms). Automated so far (`go test ./internal/admin`,
`go test -tags integration ./internal/admin -run BlogEditor`): the form
renders and saves without JavaScript; saves are version-checked (409, nothing saved); drafts
autosave and refuse a stale version; a published post is never autosaved; the server
sanitiser strips unsafe markup; schedule, publish, Update and restore-a-revision on a real
database; the SEO and AEO checks. Slash commands, paste clean-up, the autosave queue and its
conflicts, blocks and the upload flow ran as node:test suites until v1-15 removed Node; they
are the "Browser modules" section at the end of this file now.

### Write

1. **Posts -> New**, type *Post*, name it. You land in the writing view. Tab order: title,
   toolbar, body, then the sidebar.
2. Type a title; the slug follows it while the post is a draft (stop when you edit the slug
   field yourself). Press Enter in the title: the caret moves into the body.
3. Type `## Heading`, `- item`, `1. one`, `> quote`, `**bold**` and `` `code` ``: each becomes
   its block as you type (Markdown shortcuts). `Ctrl/Cmd+B`, `I`, `K` (link) and `Z` work.
4. Select a word: the bubble menu appears over it (Bold, Italic, Link, Inline code) and is
   reachable with Tab; Escape returns to the text. Make a link: an unsafe address
   (`javascript:`) is refused with a message.
5. Type `/` on an empty line: the menu lists Text, Headings, lists, Quote, Code, Table, Image,
   Upload image, Video embed, Divider, Callout, FAQ block, HTML block. Typing narrows it,
   arrow keys and Enter choose, Escape closes without inserting. Insert each once.
6. Word count and reading time update under the toolbar. **Focus mode** (button or
   `Ctrl/Cmd+Shift+F`) hides the sidebar and toolbar and dims the other blocks; Escape leaves it.
   The formatting toolbar is hidden by default: the **Toolbar** button (pressed state) or `Alt+Shift+T`
   shows it, in the post and in the page editor (Ctrl+Shift+T is reserved by Chrome and Firefox).
7. Paste from **Google Docs** and from **Word** (headings, bold, a bulleted list, a link): the
   text arrives as plain headings, lists and marks, with no fonts, colours or `Mso` markup.
   Word's typed bullets become a real list.

### Illustrate

8. **Image from the library** (slash menu): pick one; give it a description. Edit the
   description and the caption under the picture; the caption shows on the public page.
9. **Paste a screenshot** and **drop an image file** onto the text: a progress bar appears where
   the image will go, then the picture, and the cursor is in its description field. The file
   is now in Media. Drop a PDF: it is ignored. Drop an image over the size limit: a message
   says why, and the text is untouched.
10. **Video embed**: a YouTube watch link and a Vimeo link work; `https://example.com/x` is
    refused with an explanation.
11. Set a **featured image** and an excerpt in the sidebar.

### Schedule

12. Add categories and tags; create a new tag inline (as an owner). Choose an author.
13. Status *Scheduled*, a time in the future (the sidebar names the site time zone), **Save
    draft**. The post is listed as Scheduled; a past time is refused. When the time passes the
    post is published by the worker.

### Publish

14. Switch to a draft. Type, then wait: the status line shows "Unsaved changes", then "Draft
    saved 12:03". Reload: the text is there. Open the same draft in a second tab, type in
    both: the older tab shows a conflict with *Overwrite with my version* and *Reload their
    version*, and nothing is overwritten until you choose.
15. Go offline (devtools) and type: the status line says it keeps retrying and that a copy is in
    this browser. Reload while offline: after reconnecting, the banner offers *Restore it*.
16. **Preview**: the toggle opens the post in your real theme, at Desktop and Mobile widths,
    with unsaved text. The frame is sandboxed: it cannot reach the admin.
17. **Search and answer checks** update about a second after you stop typing: the
    search-result preview shows your SEO title and description, the length checks, the focus
    keyword checks, the question-heading, short-answer, FAQ and `llms.txt` checks and the
    structured data (a FAQ block makes `FAQPage` appear). Untick *Include in /llms.txt*: the
    check warns.
18. **Publish**. The public page shows the post and (with an FAQ block) a `FAQPage` JSON-LD
    block. A published post shows **Update**, never autosaves, and keeps a local copy of what
    you type until you press Update. Within a day of traffic the **AI crawlers** card lists
    assistants that fetched the post.

### Restore

19. Update the published post twice. **Versions** lists a version for every save (autosaves
    say "Autosaved in the blog editor"); *Compare* shows the changes; *Restore* on an older
    one brings that text back, keeps the current text as a new version, and the writing view
    shows the restored text.

### Without JavaScript

20. Disable JavaScript: the body is a plain textarea of HTML, the sidebar fields are all
    present, **Save draft**, **Publish** and **Update** work, a stale form shows the conflict
    message with your text, and *Preview in a new tab* opens the rendered post.

### Accessibility pass

* The toolbar, bubble menu and slash menu are announced as toolbars/listbox; the slash menu
  keeps the caret in the text (`aria-activedescendant`). Upload progress and autosave
  status are announced politely. The title and body have names ("Title", "Body").
* The check list marks each result with words (Good, Could be better, Needs work), not only
  colour; the structured data block can be reached with the keyboard.

## Browser modules (v1-15)

Run before each release, in a real browser, on a draft page and a draft post. These replace
the node:test and jsdom suites that were deleted with Node (no JavaScript engine with a DOM
runs under `go test`). Each item names the module it covers.

* **Tiptap kit** (`kit`): bold, italic, underline, strike, code, link, headings, lists,
  quote, rule, table, and clear formatting each toggle from the toolbar and from their
  shortcuts; the textarea underneath always holds the same HTML (reload to check).
* **Link policy** (`links`): a link to `javascript:` or `data:` is refused; an off-site link
  gets `rel="noopener"`; a relative link and `mailto:` are kept.
* **Undo/redo** (`history`): Ctrl+Z and Ctrl+Shift+Z step through typing, moves, adds and
  deletes in the section list; a new edit after an undo drops the redo stack.
* **Slash menu** (`slash`): `/` at the start of a block opens the menu; typing filters it;
  arrows and Enter pick; Escape closes it; `/` mid-word does nothing.
* **Paste clean-up** (`paste`): paste from a word processor and from a web page: no inline
  styles, no `<span>` soup, headings and lists survive, a pasted image is not inlined.
* **Autosave** (`autosave`): type, wait: "Saved" appears; edit in a second tab, then in the
  first: the conflict notice appears and nothing is overwritten; go offline: "Not saved"
  stays until the connection returns and the retry succeeds.
* **Blocks and upload** (`blocks`, `upload`): insert an image from the library and by upload
  (drag, paste and button); a failed upload shows the error and leaves the text alone.
* **html section** (`htmlblocks`, `html-text`, `html-fields`): the Visual tab edits text in
  place without changing the markup around it; the Fields tab edits links and images and
  groups repeated cards; switching tabs and saving leaves the HTML otherwise byte-identical.
* **Canvas protocol** (`protocol`, `host`): a click in the preview frame selects
  the section in the list and the reverse; the
  device toggle (desktop, 390 px) resizes the frame.
* **Canvas frame trust** (`host`): in the preview frame follow a link or run
  `location = "/"` from its console so it navigates away from our document: the editor
  stops listening to it and shows the preview as unavailable (it never acts on a message
  from the new page). A message with a non-`null` origin (post one from the top window's
  console with `postMessage` from an `about:blank` popup, or from another tab through
  `window.open`) changes nothing.
* **Canvas messages** (`protocol`): from the frame's console post each of these to the
  parent and check that nothing happens (no selection, no edit, no error banner): an
  unknown `type`; a wrong protocol version; an `hm:edit-text` with an out-of-range index,
  an over-long `value` (above `MAX_TEXT_LEN`, 200,000, in `protocol.js`), a non-string value, and extra
  fields on any message (the extras are dropped, never applied).
* **v2 messages** (`protocol`): from the frame's console post each of these to the parent and check that nothing
  happens: an unknown `type` with `hm: 2`; `hm: 3`; a `select` with an id containing a quote or space; a `text`
  with a value above 200,000 characters; extra fields on a `hover` (dropped, never applied).
* **Link classification, lookalike hosts** (`links`): with the site at `example.com`, links
  to `example.com.evil.test`, `evilexample.com`, `example.com@evil.test` and
  `//evil.test` are treated as off-site (`rel="noopener"`, external marker); `example.com`
  itself and its `/path` are kept as internal.
* **Text-walk parity** (`html-text`): Visual-tab edits must hit the same text node the
  server walks. For each fixture in `backend/internal/render/testdata/canvas-text/*.html`
  load its HTML into an html section, edit the first, a middle and the last text in the
  Visual tab, save, and compare the saved HTML with the `edits[].nodesAfter` and
  `value` in the matching `*.golden.json` (a markup-like value such as `<b>&</b>` is stored
  escaped). `go test ./internal/render -run CanvasTextGolden` keeps the Go half honest.
* **Blog editor local copy** (`blogeditor`): type in a post, go offline, reload: the
  editor offers the local copy; change the post in a second tab and reload the first: the
  warning says the local copy is stale and does not overwrite the server's.
* **Blog editor, save in flight** (`blogeditor`): throttle the network to "Slow 3G", type,
  and keep typing while "Saving" shows: the later text is not lost and saves next; click
  Publish while an autosave is in flight: Publish waits for it and publishes the latest
  text.
* An optional local tool runs most of the above headlessly (Playwright, dev only, never
  shipped): `scripts/admin-qa/`. See `docs/ROADMAP.md`, v1-15, and the v1-23a section above.

## Admin IA, design and feel (v1-23a)

The admin has five primary items, Settings and Theme secondary sidebars, real page loads and a
local browser harness. Checks by hand are below; the numbers come from `scripts/admin-qa/`
(Playwright and axe, dev only, never shipped, not part of `go test`). Start an instance as
`scripts/admin-qa/README.md` describes (database, `hatamoto serve` with
`HATAMOTO_ADMIN_DIR=backend/web/admin`, `scripts/admin-qa/seed.sh` for 57 entities and an
owner and an editor), then run the commands under "Harness".

### Information architecture

* **Primary nav** (`nav.go`): as owner, the sidebar has exactly Entities, Theme, AI analytics,
  Plugins and Settings; plugin pages are nested under Plugins. As editor: Entities, Theme,
  AI analytics, Settings, and Plugins only when a plugin page is visible to editors.
* **Footer**: avatar link to `/admin/profile`, a theme button and a sign-out button. The
  two buttons are icon only, each with an `aria-label` and a tooltip; hover and keyboard
  focus show the tooltip. With JavaScript off, the theme button still switches theme
  (`POST /admin/theme`) and sign-out still signs out.
* **Landing**: signing in lands on `/admin/entities`; `/admin` and `/admin/` answer 303 to it.
* **Settings roles**: as owner, Settings lists General, Media, Entity models, Forms,
  Redirects, Internal linking, Users, API, Performance, Updates and Import & export (no
  Appearance and no Menus: they are under Theme). As editor it lists only Media, Forms
  (Forms, Submissions), Redirects and Internal linking; every link of every listed tab
  returns 200, and an owner-only URL typed by hand returns 403.
* **Theme roles**: as owner, Theme lists Themes, Colours and fonts, Design system, Style
  presets, Layout parts, Blocks, Templates and Menus. As editor it lists only Blocks,
  Templates and Menus; every listed link returns 200 and the five owner-only URLs
  (`themes`, `colours`, `design-system`, `presets`, `parts`) typed by hand return 403. Theme
  is hidden from a role with no tab.
* **Entities hides blocks and parts**: the list, its type tabs and counts, the Create menu,
  `/admin/entities/new`, the parent picker and the menu and linking pickers show no built-in
  `block` or `theme_part`; a block opened at `/admin/entities/{id}/edit` still works, and
  its back link goes to Theme > Blocks (a part's to Layout parts).
* **Blocks page**: `/admin/theme/blocks` lists name (opens the builder), used-by count and
  date, with search; **New block** posts to the draft route and opens the builder on an
  Untitled block; Delete asks first and returns to the page.
* **Section URLs**: `/admin/settings/{entity-models,import-export}` (and the old
  `content-model`, `seo`, `team`) answer 302 to the first tab the role can reach;
  `/admin/theme` answers 303 to the first Theme section (for an editor, Blocks), and
  `/admin/settings?section=appearance` (also `menus`, `menus-forms`) lands on Theme.
* **General page**: `/admin/settings/general` holds identity, homepage, contact and email
  (no brand colour: that is Theme → Colours and fonts). Saving one block with an error
  re-renders the page with the error next to that block; the other blocks are untouched.
* **API page**: `/admin/settings/api-keys` (owner only) holds the create form and the key
  table; a new API key is shown once. `/admin/settings/api` answers 301 to it.
* **Export tab**: `/admin/settings/export` (owner only) shows the `hatamoto export` and
  `hatamoto import` commands and a link to `docs/EXPORT.md`. It has no upload, download or
  form.
* **AI analytics tabs**: Crawlers, Referrals, Pageviews, each its own URL; the Crawlers
  segmented tabs (overview, agents, pages, first crawls, errors, spoofed, robots) keep the
  date range when you switch.
* **At 390 pixels**: the sidebar is a top bar (brand, avatar, theme, sign-out, then the five
  primary items with icons over labels); the Settings and Theme section lists are a "Settings
  section" / "Theme section" select with a Go button that works without JavaScript (it does not
  navigate on change).
* **Row menus**: every table row has a ⋯ menu named "Actions for <row>", opens with Enter or
  Space, closes with Escape, and has the destructive item last behind a separator.
* **Widths**: every page is `narrow`, `compact`, `wide` or `full` (`data-width`; `compact` is the narrow column kept narrow inside Settings, where other widths are lifted to the wide column); the page and blog
  editors are `full` and have no sidebar.

### Redirects

* **Old admin URLs**: every row of the table in [REDIRECTS.md](REDIRECTS.md#admin-legacy-urls)
  answers **301** (GET, HEAD) or **308** (POST, PUT, PATCH, DELETE) with the exact `Location`, keeps the
  query string (`/admin/analytics/agents?tab=errors&from=2026-10-01`), and the redirect is
  the first response even when signed out (the login page's `next` is never an old URL).
  The exception is `GET /admin/analytics`, a real route: 303 to `/admin/analytics/crawlers`
  signed in, the login page signed out.
  `go test ./internal/admin -run Legacy` covers the table; spot check with
  `curl -sI http://localhost:8090/admin/forms/7/submissions/3` (301 to
  `/admin/settings/submissions/3`) and `curl -sI -X POST http://localhost:8090/admin/users/new`.
* **Segment boundaries**: `/admin/settings/theme` redirects and `/admin/theme/themes`
  does not; `/admin/media` redirects and `/admin/media/picker`, `/admin/media/upload` and
  `/admin/media/{id}` do not.
* **Entry points are not redirects**: `/admin/settings` answers **303** to the first section
  the role can reach (`?section=` picks one); each multi-tab section URL answers 302. Only
  `/admin/analytics` (a real route: GET answers 303, other methods 301) and
  `/admin/analytics/agents...` (301) move, to
  `/admin/analytics/crawlers`; `/admin/analytics/referrals` and `/pageviews` stay.
* **Real routes win**: `/admin/settings/general` and `/admin/settings/redirects` return 200,
  not 303.
* **No old links inside the admin**: `go test ./internal/admin -run NoOldAdminLinks` greps the
  admin's Go source, templates and scripts (not tests) for a moved path; click through
  one page per section and confirm the address bar never shows a 301 on the way (the only
  allowed 301 is one you typed or bookmarked). The form-submission notification email links
  to `/admin/settings/submissions/{sid}`.
* **Accepted limit (POST)**: a form opened on an old URL before the upgrade and submitted
  after it arrives as a GET (a 301 never keeps the method) and saves nothing. Reload and
  submit again.

### Feel

* **No `hx-boost`**: `grep -rn 'hx-boost\|hx-history' backend/web/admin backend/internal`
  prints nothing (`TestNoHxBoost` enforces it). A sidebar click is a full document navigation:
  in the Network panel the click is a `document` request, not `fetch/xhr`, and the
  Performance navigation entry has `type` `navigate`.
* **View Transitions** (Chrome 126 and later, Safari 18.2 and later): moving between
  Entities, AI analytics and Settings keeps the sidebar and the Settings subnav still while
  the main area cross-fades for about 120 ms. With the OS "reduce motion" setting on, the
  main area changes at once. Browsers without support just navigate.
* **Speculation Rules**: every shell page has one `script[type=speculationrules]` carrying
  the page's CSP nonce (no CSP violation in the console); the sign-in, setup and error-bare
  pages have none. In DevTools, Application, Speculative loads, hovering a sidebar link
  shows it prerendered; logout, theme, delete confirm pages, `export.csv`, previews and
  plugin `ui` URLs are never listed.
* **Back and scroll**: on the Entities list with 57 entities, scroll down, open a row, press
  Back: the list is at the same scroll position and the focus is on the page, not lost.
  The same on a Settings list. `nav.mjs` asserts the scroll position.
* **Unsaved changes**: edit a page or a post and click a sidebar link: the browser's
  leave-page prompt appears (`beforeunload`); cancel keeps the edit.
* **Indicators**: throttle the network to Slow 3G and check that the entities filter, a media
  upload, the parent search, SEO analysis, a schema field move and the import progress poll
  each show a busy indicator while the request runs, and that nothing shifts when it appears.
* **Per-page modules**: on the Entities list the Network panel lists no `editors`,
  `blogeditor` or `editor/*` request; on a screen with a rich-text field it does.
* **Known limit, no bfcache**: admin HTML is sent `Cache-Control: no-store` (cookie
  authenticated pages, `csp.go`), which stops the browser's back/forward cache. Back is a
  normal history navigation: the page is fetched again and the browser restores scroll. It
  is not instant the way a bfcache restore is. Relaxing `no-store` for list pages was
  considered and not approved.

### Visual and tokens

* **Pastel overrides**: the "delete the pastel override classes" item is a **verified
  no-op**. `rg 'bg-(green|red)-' backend/web/admin` finds nothing, and no admin CSS holds
  such a rule; they left with the Next admin. A Go test keeps it empty.
* **No literals**: `go test ./internal/admin/designcheck -run CSSLiterals` fails on a hex, `rgb(` or
  `hsl(` colour outside `tokens.css`, except the white preview frames and the dialog scrim.
* **Charts**: each `--chart-1` to `--chart-6` (indigo, three greys, amber, and a muted teal for "Other") has at least 3:1 against the paper in light
  and dark (`TestTokenContrast`); line series differ by dash and point shape as well as
  colour, so a chart still reads with colour vision turned off. The data table under each
  chart is always visible.
* **Side by side**: `shots.mjs` writes 1440 and 390, light and dark PNGs for every screen,
  next to the mockup shot for the mocked ones; `shots.mjs --table` prints the table that
  records each screen with "matches: yes, no or accepted delta".

### Harness

Run from the repository root against a running instance (`BASE` is its origin, default
`http://localhost:8090`). Results are written as JSON to `scripts/admin-qa/results/` (not
committed). Every script exits non-zero when a bar is missed.

| Check | Command | Bar |
|---|---|---|
| axe, WCAG 2.0, 2.1 and 2.2 A and AA, every screen, light, dark and 390 | `node scripts/admin-qa/a11y.mjs` | 0 violations |
| Keyboard: menu, popover, dialog, tabs, sortable | `node scripts/admin-qa/keyboard.mjs chromium` (also `firefox`, `webkit`) | all pass in all three |
| Menu editor: inspector, pointer and touch drag, Move to, keyboard, undo and redo, preview, JS off | `scripts/admin-qa/run-local.sh menu-editor.mjs` | all pass |
| Layout shift, interaction latency and navigation time, median of 5, no throttling, prerender off | `node scripts/admin-qa/perf.mjs` | CLS < 0.05, INP < 200 ms, navigation < 300 ms |
| Navigation mode, speculation rules, back and scroll, no editor module on a list | `node scripts/admin-qa/nav.mjs` | all pass |
| Screenshots | `node scripts/admin-qa/shots.mjs` | 4 images per screen, no non-200 |

Method for `perf.mjs`: Chrome, a fresh context per load, `--disable-features=Prerender2`.
CLS sums `layout-shift` entries without recent input; INP is the longest `event` entry (16 ms
threshold) over a scripted run (open a ⋯ menu, open Filters, type in search, theme button,
switch a tab); navigation time is `responseEnd` of the navigation entry and click to
`DOMContentLoaded`. The primary screens are Entities, Crawlers, Referrals, Pageviews,
Plugins, General, Media, Entity types and Forms.

Results, run against the 57-entity seed on 2026-10-16 (Chrome 154.0.8037.57,
commit c79719f8):

| Check | Result |
|---|---|
| axe violations, all screens (light, dark, 390) | 0 violations in 113 scans (every screen at 1440 light, 1440 dark and 390), 0 skipped |
| Keyboard, Chromium, Firefox, WebKit | 0 problems in Chromium, Firefox and WebKit (Chromium and WebKit after the sticky save bar focus fix) |
| Nav mode, speculation rules, back and scroll, no editor module | all pass: navigate with 0 fetch or xhr, speculationrules prerender, scrollY 758 back to 758, entities loads 5 scripts and no editor |
| Screenshots | 148 admin shots and 88 side-by-side composites, 0 skipped, 0 failed |

Navigation is `responseEnd`; where a sidebar click reaches the screen in one step, click to
`DOMContentLoaded` is in brackets.

| Screen | CLS (median of 5) | INP (ms) | Navigation (ms, median of 5) |
|---|---|---|---|
| Entities | 0.0000 | 24 | 246 (284) |
| AI analytics, Crawlers | 0.0000 | 0 | 251 (273) |
| AI analytics, Referrals | 0.0000 | 0 | 237 |
| AI analytics, Pageviews | 0.0000 | 0 | 227 |
| Plugins | 0.0000 | 0 | 248 (279) |
| Settings, General | 0.0000 | 24 | 233 |
| Settings, Media | 0.0000 | 16 | 222 |
| Settings, Entity types | 0.0000 | 16 | 224 |
| Settings, Forms | 0.0000 | 16 | 228 |

## Browsers, devices and assistive technology (v1.0 release pass)

The editors and trees need one pass on real engines and devices before 1.0, and again
whenever a vendored editor or Sortable version changes. Browser-only checks of the admin
JavaScript (CSP, modulepreload, slash menu internals, bubble menu) are in
[qa/admin-js.md](qa/admin-js.md); this section is the device and assistive-technology
matrix. Use a draft post and a draft page with a table, an image and a FAQ block.

| Where | Run |
|---|---|
| Chrome and Firefox (desktop) | the writing checklist, the page editor checklist and the browser modules list above |
| **Safari on macOS** | the same three lists; also check the bubble menu position and the Tab order (Safari skips links unless "Press Tab to highlight each item" is on) |
| **Safari on iOS** | the writing checklist on a phone and a tablet: typing, the keyboard not hiding the caret, the toolbar reachable, paste, image upload from the photo library, the preview frame |
| Chrome on Android | the writing checklist on a phone, and the touch drag items below |

* **Paste from Google Docs and from Word** (each, in Chrome and in Safari): a heading, bold
  and italic, a bulleted and a numbered list, a link and a small table. The text arrives as
  headings, lists, marks and a table with no fonts, colours, `Mso` or `docs-internal-guid`
  markup, no empty paragraphs between list items, and the textarea HTML holds the same.
* **IME input** (Japanese or Chinese pinyin, and Korean, with the OS input method): in the
  title and in the body, composing a word shows the underline, **Enter confirms the
  composition and does not insert a new paragraph or choose a slash-menu item**, and the
  autosave status does not flip to "Unsaved changes" on every keystroke inside a
  composition. Type `/` with the IME on: the menu does not open while composing and works
  after the composition ends. Select composed text: the bubble menu appears and bold applies.
* **Touch drag, menus** (menu tree in Menus, iOS Safari and Android Chrome): press a
  row's grip and drag it above, below and into another row (nesting). The drop line names
  the place ("inside About", "top level"), the page does not scroll while dragging, the row
  lands where the line showed, and Undo puts it back. A plain scroll over the tree still
  scrolls the page. Automated by `scripts/admin-qa/menu-editor.mjs` (pointer and touch).
* **Touch drag, page section tree** (page editor, iOS Safari and Android Chrome): drag a
  section up and down the list, and into a repeater. Same expectations; the canvas frame
  follows the new order after the save.
* **Keyboard buttons** (menus and the page section tree, with the mouse
  unplugged): every row has Move up, Move down, Indent and Outdent buttons that name their
  row ("Move About up"); each works from the keyboard with JavaScript on and, through the
  form fallback, with it off; focus stays on the moved row's button after the move and the
  new position is announced. The first row's Move up and the last row's Move down are
  disabled, not hidden.
* **Slash menu with VoiceOver (Safari, macOS and iOS)**: typing `/` announces that a list of
  commands opened and how many there are; arrow keys read each command with its position
  ("Heading 2, 3 of 13"); Enter inserts and reads the new block; Escape closes and says so.
  The caret stays in the text (`aria-activedescendant`), VoiceOver does not jump to the list.
* **Slash menu with NVDA (Firefox and Chrome, Windows)**: the same, in browse mode off
  (focus mode). NVDA announces the listbox, each option and its selected state, and does
  not read the editor text again on every arrow key.

## Editors against the mockups (v1-23f)

Run the three editor harness commands in `scripts/admin-qa/README.md` and compare the
`-sbs.png` shots (mockup left, admin right) for `entity-edit-post` and `entity-edit-page`.

* New post: Create menu, Post makes one untitled draft and opens `/edit`; a second click
  reuses the untouched draft; `GET /admin/entities/new?type=post` creates nothing.
* Toolbar shortcut and slash: `/` opens the block menu, arrows move, Enter inserts, Esc closes
  with focus kept in the text.
* Bubble toolbar: selecting text shows it, Tab reaches Bold, arrows move, Esc returns to the text.
* Tabs: Post and SEO and AEO switch with arrow keys and the panel follows.
* More menus: blog `...` menu and page section menus open with Enter, arrows move, Esc returns.
* Add-section dialog: radio cards, Tab stays inside, Esc returns to Add section.
* Without JavaScript: both editors still save from the form, and the add-section select works.

Harness record (2026-10-16, chromium): a11y 0 violations on 12 editor scans (1440 light and
dark, 390, plus slash, bubble, more, SEO tab, section menu and add-section states); keyboard
tab walks 0 problems (1440 light, dark, 390), and all 5 editor widget scripts ok; 8 admin shots
plus mockup pairs written, 0 failed. Manual run: not done in this pass.

## Builder Style controls (builder-e3-inspector-controls)

Run `scripts/admin-qa/builder-states.mjs`, `keyboard.mjs`, `a11y.mjs` and `consistency.mjs` (see `scripts/admin-qa/README.md`), then check by hand in light and dark and at phone width:

* Colour popover by keyboard: Tab to a colour swatch (for example Text colour), Enter opens the palette and focus moves into it; Tab and Shift+Tab wrap inside it; picking a palette colour stores the token; Esc closes it and focus returns to the swatch.
* Slider: drag a length slider (for example Margin top) and the canvas follows; one Ctrl/Cmd+Z undoes the whole drag. ArrowRight on the slider changes the value by one step.
* Box model: Tab order is margin top, right, bottom, left, then padding top, right, bottom, left. With Link on, typing in one side sets all four, and one undo reverts all four. Vertical and Horizontal copy as labelled. Reset margin and Reset padding clear the ring.
* Rows: the set-here dot, source label (Local, Preset, Inherited) and Reset appear and disappear as a value is set and reset; a closed group keeps its dot after reload.
* Accessible names: Move Y is exactly one number field; its unit, slider and token controls are named "Move Y unit", "Move Y slider" and "Move Y token".

## Builder direct manipulation (builder-e4-direct-manipulation)

Source: `docs/BUILDER.md` "Direct manipulation" and "Copy, paste and the paste endpoint". Automated: the Go paste, schema and render tests, the pure client suites (`builder-store`, `builder-clipops`, `builder-ctxmenu`, `builder-picker`, `builder-history`, `builder-selection`, `builder-keys`, `builder-bridge`), and the browser scripts `scripts/admin-qa/builder-direct.mjs` (mouse only: picker, cross-page paste through the clipboard, wrap, both rename paths, Ctrl+click delete, canvas and tree menus, history jump, a move-handle drag with a delayed drag chunk, axe on the menu, picker and history), `keyboard.mjs` (menu, rename, text-control isolation, multi-select, history) and `a11y.mjs builder-shell` (states `context-menu`, `history-open`, `structure-picker`). `builder-direct.mjs` needs `QA_PAGE_NAME` and a second page (`QA_PAGE2_NAME`, default `Contact`) and leaves both as found.

By hand, in light, dark and at phone width, on a page you can throw away:

* Click a round **+** between two sections (or inside an empty cell), pick **3 columns**: a section with three dashed cells appears at that spot, selected. Esc on the picker returns focus to the button.
* Right-click a cell on the canvas: the menu opens at the pointer, Esc closes it. Right-click the section's row, **Copy**; open another page, right-click a row, **Paste after**: the same layout appears with new ids. Undo removes it in one step.
* **Wrap in container** (Ctrl+G) then **Rename** (F2, then double-click): the row shows the name; Esc cancels; an empty name restores the default.
* Ctrl+click two rows, Delete, one undo restores both. In the inspector field and in the rename box, Ctrl+C, V, G and Z do not touch the page.
* Open **History**: labelled steps ("Add section: 3 columns", "Paste", "Wrap", "Rename"); pick an earlier one and the page matches it; Esc returns to the button.
* Reload and press the move handle at once, before the page has finished loading: the drag still starts.
* Rollback check (a scratch install): a page with a renamed element renders an empty body on the previous binary and the admin shows it as broken; clearing the name fixes it.

## Builder inline rich text (builder-e5-inline-rich-text)

Source: `docs/BUILDER.md` "Inline rich text" and `docs/CANVAS.md`. Automated: the Go sanitiser, schema, render, diff, outline and protocol tests, the pure client suites (`bridge-inline`, `builder-inlinehtml`, `builder-inlinefield`, `fmtbar`, `canvas-host-v1`, `builder-bridge`), and `scripts/admin-qa/builder-inline.mjs` (bold and link by mouse and keyboard, saved and rendered markup, rich paste, Esc, Shift+Enter, the component field click, keystroke p95 <= 2 ms on a 60-node page, no `patch` while typing, axe with the bar open, the inspector editor at phone width).

By hand, in light, dark and at phone width, on a page you can throw away:

* Double-click a heading: it becomes editable and a bar appears above it. Select a word, press **B**: it turns bold and the **B** shows pressed. Ctrl+B, Ctrl+I and Ctrl+U do the same without the bar.
* **Link** (or Ctrl+K): type an address, tick **Open in new tab**, Enter. The word is a link. `javascript:alert(1)` is refused with a message. **Remove link** takes it off.
* Paste from a web page or a Word file into a text: only bold, italic, underline, strike, code, links and line breaks stay.
* Shift+Enter in a text adds a line break; in a heading it does nothing. Enter ends the edit. Esc puts the text back as it was.
* Click outside the page to end an edit: the bar goes. The public page shows the same marks; a new-tab link opens with no opener.
* Select a component on the canvas and click its body: its editor in the inspector takes focus. Type in its headline: the field takes focus and the canvas shows the stored text.
* In the inspector, the **Text** of a heading has its own small editor (B, I, U, S, code, Link): use it with the keyboard alone, then check the canvas follows.
* Rollback check (a scratch install): a page with a formatted heading or text renders an empty body on the previous binary and the admin shows it as broken; clearing the formatting (Clear) on every formatted node fixes it.

## Builder element settings (builder-e6-element-settings)

Source: `docs/BUILDER.md` "Element settings" and `docs/THEMES.md`. Automated: the Go schema, settings-validation, render, golden (`settings`), lint, boot and budget tests, the pure client suites (`builder-bridge`, `builder-elementsettings`) and `scripts/admin-qa/builder-settings.mjs` (anchor scroll, link attributes, linked container, CLS 0 with a failing control, lightbox keyboard and axe, inspector Link and Advanced fields, the 422 messages, phone width).

By hand, in light, dark and at phone width, on a page you can throw away:

* **Advanced**, **Anchor** `pricing` on a container, then a button whose **Link** is `#pricing` (the field offers `#pricing` and your pages): on the public page the button scrolls to it and the address shows `#pricing`. **Copy #anchor** puts `#pricing` on the clipboard.
* A second element with the same anchor, or `main-content` or `hm-x`: refused (in the field, or by the save with its message). Upper case is lowercased as you type.
* A button with **Open in new tab** and **Nofollow**: the public link has `target="_blank"` and `rel="nofollow noopener noreferrer"`. Clearing the link removes both settings.
* An image from the library: the public `img` has `width` and `height`. **Caption** makes a figure; **Lightbox** opens a dialog: Esc closes it and focus returns to the image, Tab stays inside, arrows move through a gallery. A page without a lightbox loads no extra script.
* A container with a **Link** and a heading inside: one link, named by its text or its **Accessible name**. A button inside it: the save is refused with the reason.
* Rollback check (a scratch install): a page using any of these settings is refused cleanly by the previous binary; remove them first (see the rollback floor in `docs/BUILDER.md`).

## Slice B9: budgets, layout diff and strict scripts

Automated: `make budgets` prints the budget table and exits 0; the out-of-repo browser harness covers the browser checks below and writes `results-b9/`.

By hand, on a builder page with at least three saved versions:

* Versions, pick two, Compare: a "Layout changes" table lists added, removed, moved and changed nodes; the field table
  has no `page_doc` or `page_sections` rows.
* On a removed row press "Restore this node from vN" **with JavaScript off**: the page shows the flash, a new version
  exists ("Restored node ... from version N") and the node is back in the editor outline. Open Compare in one tab, save
  in another, then restore from the first: the 409 page appears and nothing changes.
* Public default-theme page: devtools, Network, the document response has `script-src 'self'` without `'unsafe-inline'`
  and the console shows no CSP violation. The admin preview still shows the old policy.
* Footer text on the default theme is at least 4.5:1 in light and dark (axe reports 0 violations).
* Editor memory: after the soak in `memcheck.mjs` the parent and frame heap are under 40 MB.
* A deliberate +10 KiB of editor JS or +2 KiB of widget CSS makes `make budgets` fail; revert it.

## Builder UX shell (builder-ux-shell)

Source: `docs/BUILDER.md` "Panes". Automated: the pure client suites (`node --test scripts/admin-qa/unit/*.test.mjs`, the `builder-*` files for the shell: statusline, topbar, lefttabs, palette, commandk, keymap, kindnames, navigator-filter, historylabel, coachmarks, startpanel, phone, reuse, sitestyles), the Go boot, bundle, UILint and budget tests, and the browser script `scripts/admin-qa/builder-ux-tasks.mjs`, which runs tasks T1 to T10 by mouse, keyboard and at 390 px, the first-use path (visible buttons only) for T1, T2, T5 and T8, the shell checks and axe on seven surfaces:

```
scripts/admin-qa/run-local.sh builder-ux-tasks.mjs      # result: scripts/admin-qa/results/builder-ux-tasks.json
```

`QA_ONLY` (regex of task ids or check names), `QA_MODE` and `QA_RUNS` narrow it; exit 1 on any problem. `builder-ux-shots.mjs` takes the screenshots. By hand, light and dark, on a draft page:

* Top bar: one status chip, "Draft · Saved 14:02" after a save, "Saving…" while it runs, "Not saved" on failure.
* Device switch reads Desktop, Tablet ≤1024, Phone ≤640. Choosing Tablet shows an amber "Editing Tablet" chip and a screen reader hears "Editing Tablet styles". Selected button stays ink, not amber.
* The **Navigator** and **Inspector** buttons are labelled and hide their panes; Ctrl/Cmd+Alt+[ and ] do the same.
* More menu order: Versions, Page settings, Site styles (owner only; absent for an editor), Keyboard shortcuts, a rule, Delete page last and red; Delete asks first. On a block, it reads Delete block.
* Left pane: Navigator and Add tabs move with the arrow keys. Add has a focused search, Recent after a first insert, a Patterns row with Show all patterns, then the groups. Enter inserts, Shift+Enter inserts and keeps the search open, a drag still works.
* The footer has **Add element** and **Ctrl K** buttons at all times.
* Ctrl/Cmd+K opens the palette from the canvas pane, a field and the Navigator; the list shows Commands including Save as block…; Esc closes it. Double-click a heading, press Ctrl+K: the link dialog opens, not the palette. With keyboard focus inside the canvas frame (click the page, not a field) Ctrl+K opens the palette too.
* Navigator: Filter, Collapse all, Rename; names read "Rich text", "Accordion item", "Block: Promo".
* History rows read "Add Heading", "Delete Container", "Move Button", "Change Text colour (Tablet)", "Paste 3 elements", starting with "Original".
* Empty page: the Start this page panel with thumbnails and Start blank; Start blank focuses the Add search. The two hints show once and not again after Got it or a first insert.
* Conflict (two tabs): the banner floats over the canvas top with no layout shift, the chip says "Changed somewhere else", the three actions work.
* Save as block… and Save as template… open a small dialog with the name focused; Esc cancels and focus returns to the opener.
* Site styles (owner): the drawer opens at the right, saves without a reload, a bad hex or size is marked on its field, Accent shows only on a theme that uses builder colours.
* 390 px: the bottom dock Navigator, Add, Inspector; selecting an element opens the Inspector sheet; Undo and the device switch stay in the top bar.
* Shortcut sheet: grouped, searchable, never taller than the viewport.

## Builder UX canvas and inspector (builder-ux-canvas-inspector)

Source: `docs/BUILDER.md` "UX canvas and inspector" and `docs/CANVAS.md` "Host chrome". Automated: the pure client suites (`node --test scripts/admin-qa/unit/*.test.mjs`; for this work `builder-layout`, `builder-inspector-attach`, `builder-inspheader`, `builder-rows`, `builder-showon`, `builder-boxmodel`, `builder-content-order`, `builder-columnsops`, `builder-items`, `builder-restoreops`, `canvas-host-v2`, `content-skeleton`, `fmtbar`, `builder-keymap`), the Go outline, node-versions, widget meta and budget tests, and the browser scripts `builder-ux-tasks.mjs` (T1 to T10 plus the `restore-dialog` check), `builder-direct.mjs`, `builder-inline.mjs`, `builder-states.mjs` and `keyboard.mjs`:

```
scripts/admin-qa/run-local.sh builder-ux-tasks.mjs      # result: scripts/admin-qa/results/builder-ux-tasks.json
scripts/admin-qa/run-local.sh builder-inline.mjs        # Enter, Esc, Ctrl+K, format undo, heading bar, link panel
QA_ONLY=restore-dialog scripts/admin-qa/run-local.sh builder-ux-tasks.mjs
```

By hand, in light, dark and at phone width, on a page you can throw away:

* Hover an element: a dashed grey outline only. Click it: a solid outline and a toolbar outside it with Parent, Duplicate, Wrap, Hide, Delete and More; arrow keys move between them. On a text the hint reads "Double-click or Enter to edit text".
* Strip above the page: breadcrumbs select each parent, "Page" selects nothing. Zoom Fit, 100, 75 and 50 work; there is no zoom below 50 (Fit may go lower only at phone width); a resize keeps a chosen zoom. Tablet and Phone show a coloured band with the label.
* Round **+** between elements, at the top, and inside an empty box: the picker lists the six layouts, **Choose an element…** and **Insert a ready-made section…**; each inserts at the spot. The empty page offers one too.
* Select a text, press Enter: you are typing. Enter again keeps it; do it again, type, press Esc: the text goes back. Ctrl/Cmd+K while typing opens the link panel (no wider than 360 px, with a page and anchor list); with the page itself focused it opens the command palette.
* A heading's bar shows B, I, Link, Clear; a text's bar shows the full set. Make words bold, then press Ctrl/Cmd+Z: the bold goes. Type, then Ctrl/Cmd+Z: the typing goes first. There is no redo for a format.
* Inspector: the header names the element, "Editing Tablet" on Tablet, and **Element options** has Copy style, Paste style (greyed with a tip until a copy) and Restore from an earlier version…. The row under it says **Class**, not Preset or Overrides.
* Style tab: a context line (breakpoint, State) is first, then the groups Layout, Spacing, Fill, Typography, Corners & border and Effects, each collapsible; a group with a value set here starts open, one without starts closed with a +. Each control's chip reads Set here, Inherited from…, From class or Custom value, and Reset clears just that one.
* Show on: turn Tablet off, then Phone on: Desktop stays as it was. The toolbar's Hide on this device, the tree eye and the H key agree with the switches.
* Box model: padding sits inside margin; tab order is top, right, bottom, left; 0 and auto (margin only) work; a link toggle makes one edit change the linked sides in one undo step.
* Colour control: names read Brand, Background, Text (not raw tokens), an unset colour says None, a custom colour appears under Recent next time, and an owner sees **Edit site colours** (an editor does not).
* Content tab: a button's text is above its link; an image reads address, alt, caption, link, lightbox; a heading's level is H1 to H6; **Use data** opens the source picker and a bound field shows **Unbind**. Number of columns adds and removes empty columns and refuses a filled last one with a message. Tabs, Accordion and Gallery list their items with Add item, Move up, Move down, Delete; a Form is a select; a Video reads Video address, Title, Poster image.
* Insert an Icon at desktop and phone width: it is 24 px (1.5em), not full width. Open the media picker: a "Loading images…" placeholder shows before the grid.
* Restore one element: change a heading's text and save, change it again, open Element options, **Restore from an earlier version…**, pick the first version: only that heading changes, the history reads "Restore Heading from version N", one undo undoes it.
* Navigator and history use plain names (Rich text, Accordion item, Tab panel, Empty state), and the history reads "Change Font size (Tablet)", "Change Link", "Bind Text".
* Rollback check (a scratch install, older binary): a page edited with all of the above opens and renders as before; there is nothing to clear for this work.
