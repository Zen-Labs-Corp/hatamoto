# Admin JS: browser-only QA

Go tests cover the server side (modulepreload closure, import-map integrity,
single Tiptap/ProseMirror copy, htmx config, `injectCSS:false`, template lint).
What only a browser can show is listed here. Run against a real admin
(`/admin/entities/<id>/edit` of a blog post) with the browser console open. The harness pins `HATAMOTO_ADMIN_PATH=/admin`, so the URLs here are literal; real installs get a random address (`hatamoto admin-url`).

Smoke run for v1-plan-sync-adminjs (headless Chrome 154, real blog-editor module
stack, admin CSP): see the PR description. Zero CSP violations, slash menu
opens/filters/inserts/Escape, bubble menu appears on selection and applies Bold.

## Every browser (Chrome, Firefox, Safari macOS and iOS)

- [ ] Console shows **no CSP violations** and no `EvalError` on a list page, the
      page editor and the blog editor.
- [ ] Network panel: the head's `modulepreload` requests are served from the
      same URLs the import map resolves (no second fetch of a module), each with
      its `integrity`; a plain list page loads the admin entry and `components` only
      and no editor module (`pageeditor`, `blogeditor`, `editor/*`); each other page
      loads just the modules its `modules` block names (`menutree` with `menukeys`, `menudrag`, `menuinspector`, `menupreview` and `menuadd` on the menu editor,
      `charts` on the three analytics tabs, `pluginframe` on a plugin page,
      `searchselect`, `repeater` and `content` where the page uses them); only the page
      and blog editors add the Tiptap graph (62 files).
- [ ] A module that a page names loads once and its behaviour starts on that page
      (menu tree pointer drag, keyboard, inspector and preview on `/admin/theme/menus/{id}`, charts on
      `/admin/analytics/crawlers`, repeater rows on a form or entity type editor); the
      same page after an htmx swap (filter, reorder) still works.
- [ ] Reload with a warm cache: no revalidation round trip per module (all
      `immutable`).

## Slash menu (`@tiptap/suggestion`)

- [ ] `/` at the start of a line and after a space opens the list; `a/b` and
      `/` inside a code block do not.
- [ ] Typing filters (`/h2` -> Heading 2), no match closes it and Enter then
      behaves as a normal Enter.
- [ ] Up/Down move the selection, Enter/Tab insert, Escape closes and the menu
      stays closed for that `/` until it is deleted.
- [ ] Mouse click inserts and keeps the caret in the editor.
- [ ] The menu follows the caret when the editor scrolls or the window resizes.
- [ ] Screen reader (VoiceOver, NVDA): focus stays in the editor; arrow keys
      announce each option through `aria-activedescendant`; the list is a
      `listbox` named "Blocks".
- [ ] IME composition (Japanese/Chinese) does not open or select from the menu
      mid-composition.
- [ ] Image / Upload image / Video embed items open their file chooser or
      dialog (they call the page's `ctx`).

## Bubble menu (`@tiptap/extension-bubble-menu`)

- [ ] Selecting text with the mouse, with Shift+arrows and by double-click shows
      the toolbar above the selection within ~0.1 s; collapsing the selection or
      moving into a code block hides it.
- [ ] Bold, Italic, Inline code toggle and `aria-pressed` follows the caret;
      Link opens the editor's link row.
- [ ] Tab/Shift+Tab reaches the toolbar buttons; Escape returns focus to the
      text.
- [ ] Near the top of the viewport the toolbar flips below the selection.
- [ ] Touch (iOS Safari, Android Chrome): long-press selection shows the bubble
      without fighting the native selection handles.

## Page editor (v1-12)

- [ ] Rich-text regions in the page editor still mount (they share the kit and
      the single Tiptap core); undo/redo and the Fields view round trip.
