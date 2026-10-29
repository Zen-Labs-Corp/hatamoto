---
title: Editor reference
nav_title: Editor reference
description: Every element, setting, shortcut, limit and save rule of the page builder and the blog editor.
---

This page lists everything the two content editors do: the page builder and the blog editor. To change a page step by step, follow [Edit a page](page-editor.md); to write a post, follow [Write a post and add people](blog-and-people.md). Theme authors who need the markup the builder produces should read [Builder contract](../themes/builder-contract.md).

## Overview

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

Pages, posts and any entity with page sections are edited at `<admin>/entities/{id}/edit`. Which screen opens depends on the type and on what the page stores:

| The entity | Screen |
|---|---|
| a post type (`post`, `article`, `blog_post`, `blog`, or a type whose Editor setting is Post editor) | The **blog editor** |
| a page with a `page_doc` that parses | The **page builder**, a visual tree editor |
| a page with only `page_sections` (the older flat list of sections, no `page_doc`) | The **page builder**, on the page's content converted in memory. Opening stores nothing and adds no version; the first save stores the document |
| a page with a `page_doc` that cannot be read | A recovery screen, "This page's layout cannot be read", with **Open the versions** to restore an earlier version; the page's other fields still edit under Details |
| a page with only `page_sections` whose section ids cannot convert (duplicate or empty ids, ids over 64 characters, or characters other than letters, digits, `-` and `_`) | A recovery screen, "This page has a legacy layout". The content and the public page are unchanged; an administrator fixes the ids in the stored sections or sends a corrected `page_doc` through the API or MCP |

There is no separate section editor any more: the older form editor was removed, and every page opens in the builder. A new page draft starts with an empty document. An older page gets a stored document on its first builder save, on any write through the API, MCP or an import, or in bulk when someone runs `hatamoto builder convert` on the server (see the [CLI reference](../operate/cli-reference.md)).

## The page builder

The builder edits `attributes.page_doc`, a tree of layout nodes. Hatamoto renders it on the server, so the public page is plain HTML and CSS.

### The screen

| Part | What it holds |
|---|---|
| Top bar | A back link, the page name, **Navigator** and **Inspector** buttons that show or hide each pane (`Ctrl/Cmd+Alt+[` and `]`), **Shortcuts**, **Undo**, **Redo**, **History**, the device switch, one status chip, **Save draft** and **Publish** (**Update page** once published), and a **More** menu. |
| **Navigator** (left tab) | A tree of nodes. Rows can be dragged, renamed (F2, double-click or the **Rename** button) and folded with the caret; Ctrl or Cmd plus click selects several. Above it, a **Filter elements** box and **Collapse all**. |
| **Add** (left tab) | A search box (focused when the tab opens) over the things you can insert, as tiles. Press Enter or click to insert after the selection (or into a selected container); Shift+Enter inserts and keeps the search open; press and drag to drop onto the canvas. Its foot holds **Save as block…** and **Save as template…**. |
| Left pane foot | Always visible: **Add element** (opens the Add tab, same as `/`) and **Ctrl K** (opens the command palette). |
| Canvas (middle) | The page in a sandboxed frame, with a strip above it (breadcrumbs, zoom), a hover outline, a selection outline and a toolbar beside the selected element. The device switch sizes the frame and selects the breakpoint the Style tab edits. |
| **Inspector** (right) | The settings panel, with the tabs **Content**, **Style** and **Advanced**. |

On a screen 959 pixels wide or less the panes stack and a dock at the bottom switches between **Navigator**, **Add** and **Inspector**; selecting an element opens the Inspector sheet. At 640 pixels and below the device switch and **Undo** stay in the top bar, and **Save draft**, **Redo** and **History** move into **More**.

**Names.** The Navigator, the history and the messages use plain names for kinds: "Rich text" (`richtext`), "Accordion item", "Tab panel", "Columns", "Query loop", "Block: Promo" for a block reference, and so on. A name you gave an element wins over the kind name. A row reads "Name · Kind" when both differ.

**Status chip.** One chip in the top bar shows the save state: "Draft · Saved 14:02", "Saving…", "Not saved" or "Changed somewhere else".

**Device switch.** **Desktop**, **Tablet ≤1024** and **Phone ≤640**: the widths are the breakpoints the public CSS uses. On Tablet or Phone an amber chip reads "Editing Tablet" or "Editing Phone", and a polite announcement says "Editing Tablet styles", so it is clear that Style edits apply to that width only.

**Start panel.** A page with no elements shows **Start this page** over the canvas: hero, features, call to action and FAQ first, then other theme sections, with **Start blank**. It goes away on the first insert.

**Hints.** Two one-time notes ("Click to select. Double-click text to type." and "Press / or use Add to insert anything.") show until you press **Got it** or make your first insert, and are remembered per user in this browser.

**More menu.** **Versions**, **Page settings** (slug and SEO, on the entity form), **Site styles** (owners only), **Keyboard shortcuts**, then in red **Delete page** (**Delete block** or **Delete part** on those screens), always behind a confirmation.

**Command palette.** `Ctrl/Cmd+K` opens a centred search from anywhere, even from a text box. It lists Recent, Patterns, Elements, Blocks and Commands: Undo, Redo, Desktop, Tablet, Phone, Show or hide Navigator, Show or hide Inspector, History, Keyboard shortcuts, Save as block…, Save as template…, Site styles (owners) and Preview (pages with a public address). Enter runs the chosen row; Shift+Enter on an element or pattern inserts it and leaves the palette open. Typing ranks matches by name first. `Ctrl/Cmd+K` also works while focus is on the page itself, inside the canvas frame. In a text edit on the canvas it is the link shortcut instead.

**Patterns.** With no search the Add tab shows one short row of patterns and **Show all patterns (N)**. A search lists every match.

### Node kinds

The base kinds are `container` (tags `section`, `div`, `header`, `footer`, `main`, `article`, `aside`, `nav`; flex or grid layout), `component` (a theme section), `heading`, `text`, `button`, `image`, `spacer` and `divider`. Raw HTML nodes are rejected.

| Limit | Value |
|---|---|
| Depth | 8 |
| Nodes per document | 1000 |
| Document size | 1 MB |
| Link targets | `http`, `https`, `mailto`, `tel`, relative paths and `#` only |
| Text | always escaped |

On top of these the builder ships widgets, grouped in the palette:

| Group | Widgets |
|---|---|
| Layout | `columns` (2 to 6) |
| Content | `stat`, `richtext`, `html`, `list`, `table`, `quote`, `buttons` |
| Media | `icon`, `gallery`, `video`, `map` (plus the base `image`) |
| Interactive | `accordion`, `tabs` (with panels) |
| Data | `form`, `menu`, `breadcrumbs`, `loop` (query loop) |

Structural widgets are inserted with their seeded children as one undo step. Accordions and tabs work without JavaScript, and a small script enhances tabs and video only on pages that use them. A video renders as a link first and loads the player after a click, from YouTube no-cookie or Vimeo, so loading the page makes no third-party request.

### Formatting text on the page

Double-click a heading or text on the canvas to type in it, or select it and press **Enter**. A bar appears above the text. On a text it holds **B**, **I**, **U**, **S**, **Code** (`</>`), **Link** and **Clear**; on a heading it holds only **B**, **I**, **Link** and **Clear**. Select some words and press one. From the keyboard: Ctrl/Cmd+B, I and U, and Ctrl/Cmd+K for a link.

**Link panel.** **Link** opens a small panel (at most 360 pixels wide) with an address field, **Open in new tab** and **Apply**. Start typing in the address field and it suggests your published pages and the anchors on this page; pick one or type any address.

**Undo while typing.** `Ctrl+Z` (`Cmd+Z` on a Mac) in a text edit takes back the last format (bold, a link, Clear) while the text is still what that format produced. If you typed after it, the first undo takes the typing back and the next one the format. There is no redo for a format.

A link accepts `http`, `https`, `mailto`, `tel` and addresses on your site; anything else is refused. **Enter** finishes the edit, **Shift+Enter** adds a line break in a text (not in a heading), and **Esc** puts the text back as it was. Pasted text keeps only these marks. Links that open in a new tab get `rel="noopener noreferrer"`.

Formatted text is stored beside the plain text, so a page without formatting is unchanged. After you format a heading or text, the page can no longer be opened by an older Hatamoto version: update before you roll back, or clear the formatting first.

### Links, images and anchors

An image from the media library is published with its width and height, so the page does not jump while pictures load. A lightbox opens the full image in a dialog: **Esc** closes it, **Tab** stays inside, the arrow keys move through a gallery and focus returns to the image; without script the image is a plain link to the file. A page with a lightbox loads one small extra script; other pages load none.

Settings added here are stored on the page, so after you use any of them (anchor, accessible name, tooltip, new tab, nofollow, caption, lightbox, or a link on a container or image) an older Hatamoto version can no longer open the page: update before you roll back, or remove them first.

### The inspector

The top of the inspector names the selected element. On Tablet or Phone it also carries the "Editing Tablet" chip. Beside it is the **Element options** menu (a button marked "…") with **Copy style**, **Paste style** (greyed, with a tip, until you have copied a style) and **Restore from an earlier version…**. Under the header is a **Class** row: a menu that applies one of your site's classes (a named set of styles) to the element, and **Save as class…**, which turns the element's own styles into a new class. If an element differs from its class, a list names each difference with **Reset** for it, plus **Reset to class** and **Detach to custom**. Older notes and stored data call a class a "preset"; only the word on screen changed.

- **Content** builds one control per property, in the order people read them: a button's text before its link, and an image's address, then alt text, caption, link (with its options right under it) and lightbox. Rich text (a heading's or text's **Text** is a small editor with bold, italic, underline, strike, code and link, the same marks as on the canvas), a media picker (it shows a **Loading images…** placeholder while the library loads), text areas for lines and code, and lists of existing form slugs and menu locations. A heading's level is six buttons, **H1** to **H6**. A property that can show live data has a **Use data** button that opens the source picker (see [Live data](#live-data)); a field that uses data shows its source and an **Unbind** button. An image needs alt text or the **Decorative** box ticked, and warns until one is set. A few elements have their own controls: **Number of columns** on Columns adds empty columns or removes empty trailing ones in one step (a last column that still holds elements is refused, with a message); Tabs, Accordion and Gallery show an **Items** panel with **Add item**, **Move up**, **Move down** and **Delete** for each item, and a List edits its lines there; a Form picks from the site's forms; a Video asks for **Video address**, **Title** and **Poster image**; an Icon's name is plain text, without a preview.
- **Style** opens with the **State** switch (Normal, Hover, Focus, Pressed, Parent hover; see below), then **Essentials**: the four to six controls most edits need for that kind of element (for example a heading shows Font size, Weight, Colour, Alignment and Spacing (margin around padding); a button shows Background, Colour, Radius, Spacing, Font size and Width). Spacing, the box model with margin around padding, is an Essential for every kind, and margin is set only there. Every other style key sits under one **More style options** section, in groups, and edits the active breakpoint. A key shown in Essentials is not repeated in the groups. **Effects** holds shadow, opacity, move X and Y and scale. Values are design tokens, listed options or bounded numbers; raw CSS is never accepted, and a refused value says what was expected. Reading a row:
  - **Where a value comes from.** Each control has a **source chip**: **Set here** (the element has its own value at the active device and state), **Inherited from** Desktop, Tablet or Normal (shown greyed), **From class** followed by the class name, or **Custom value**. A control with a value of its own has one **Reset** button, which removes just that value at the active device and state and leaves the others alone.
  - **Groups** are collapsible sections under **More style options**. Layout, Spacing and Typography start open; the rest start closed, and the browser remembers your choice. A closed group shows a dot when anything inside it is set here.
  - **Lengths** (Spacing, Size, offsets, font size, radius and similar) have a number field, a unit menu (the units that key accepts, such as px, rem, %) and a slider. Where the key has design tokens, a menu above the number picks one (for example a space step) and "Custom" switches back to a number. The slider moves in steps and previews on the canvas as you drag; the whole drag is one undo step, while each arrow-key press on the slider or number field is its own step. A value the field does not accept is refused with the expected form, and the previous value stays.
  - **Colours** show a swatch and the colour's plain name, for example "Brand", "Background" or "Text" (hex for a custom colour); a colour with no value reads **None**. Select the swatch to open the palette: the site's colours, **None**, a **Custom** colour picker with a hex field, and your last six custom colours under **Recent** (kept per person in this browser). Owners also see **Edit site colours**, which opens the site styles panel. Tab stays inside the palette, **Esc** closes it and returns to the swatch, and clicking outside closes it. Choosing a site colour stores its name, so the colour follows the theme; a custom colour stores the hex.
  - **Spacing** (an Essential for every kind, so it is not repeated under More style options) is a box model: padding drawn inside margin, four inputs each in the order top, right, bottom, left. Each side has a **0** button, and margins also have **auto**. Three link buttons per ring (all sides, top and bottom, left and right) keep the linked sides equal, as one undo step; all sides start linked when all four are equal. Margins accept `auto`; padding does not. **Reset margin** and **Reset padding** clear the ring.
  - **Position**: the offsets (top, right, bottom, left), stack order and scroll margin appear once Position is not Static. A sticky header is Position Sticky, Top 0, Stack order 10 and a background.
  - **Background**: an image is a path from the media library (`/media/…`), a gradient is written like `linear-180-000-accent` (angle, then colours as tokens or hex without `#`). Image and gradient combine into one background. These, Columns, Column tracks, Display and Hide do not preview while you type; the canvas shows them a moment later, once the server has compiled the change.
  - **Border**: a width shows only once a border style is set.
  - **State**: the switch at the top of the Style tab chooses Normal, Hover, Focus, Pressed or Parent hover (the last only when the element has a parent). Pick a state and every field edits that state's value; the canvas shows the state while it is selected. Fields that cannot change with a state (layout, size, position, transitions) are hidden. A greyed value is inherited, from Normal or a wider device. Picking another element or tab returns to Normal. The preview is only in the editor; the public page shows the state when a visitor hovers, focuses or presses.
  - **Hover lift** (a card that rises): select the card, set Shadow "Small" and Transition "Transform and shadow" (duration "200 ms") in Normal, switch to Hover, set Move Y `-4px` and Shadow "Large". Visitors who prefer reduced motion get the lift without the animation.
  - On Phone, a Hover value set on Desktop still applies while hovering; set the Phone's Hover value to change it.
  - **Show on**: three independent switches, **Desktop**, **Tablet** and **Phone**. Turning one off hides the element at that width; turning one on shows it there. Changing one never changes another, so "Phone only" is Desktop off, Tablet off, Phone on. The tree's eye button, the **H** key, the canvas toolbar's **Hide on this device** and the right-click menu use the same rules. **Show as** (the display value) stays in the Visibility group.
- **Link** (buttons, images and containers) has an address field that suggests your published pages and the anchors on this page, an **Open in new tab** box, and **Nofollow**, **Sponsored** and **User generated content** boxes. A new-tab link always gets `noopener noreferrer`. Clearing the address clears these too. A container with a link becomes one clickable block: it cannot hold a button, a form, a menu, a video, another link or text with a link in it, and the save says which child is in the way. An image has **Caption** and **Lightbox**; a gallery has **Lightbox** too, which applies to all its images. An image needs either a link or a lightbox, not both.
- **Advanced** shows the node id (read only), the `hidden` switch (the node is not rendered at all) and, for containers, the tag. It also has **Anchor** (the element's address on the page: link to it with `#anchor`; lowercase letters, digits and hyphens, unique on the page, and not `main-content`, `top`, `toc` or a name starting `hm-`), **Copy #anchor**, **Accessible name** (what a screen reader says instead of the text; begin it with the visible words) (not offered on text, icons, menus, breadcrumbs, tab panels or a plain box without a link; give a box a link or a section/nav tag first) and **Tooltip**. Spacers, theme sections and blocks have no Anchor; wrap them in a container to give them one. Setting an anchor on a section of a page made from older sections switches that page to the free-form layout.

### Saving

Every edit (insert, move, remove, duplicate, property and style changes) can be undone and redone, and quick consecutive edits of one property merge into one step. The canvas follows each edit by asking the server to render only the affected node.

| Case | Behaviour |
|---|---|
| Drafts | Autosave a few seconds after a change, the same way blog drafts do. |
| Published pages | Never autosave. A recovery copy is kept on the server and offered on your next visit, on any device (**Restore my unsaved changes** or **Discard them**); **Update page** saves. |
| Conflict | If somebody saved a newer version the chip reads "Changed somewhere else" and a banner floats over the top of the canvas (nothing moves): "Someone else saved this page while you were editing. Your changes are still here and are not saved yet." with **Keep my version**, **Load their version** and **Compare in Versions** (opens Versions in a new tab). |
| Failed save | Keeps the server copy (sent at once), and drafts retry. |
| Invalid document | Refused with HTTP 422; nothing is written. |
| Page sections | A save sends only the document; the stored `page_sections` is never touched. |

### Without JavaScript

The server also renders the document as one nested form, the outline, inside `<noscript>`. Add `?outline=1` to the edit address to see it as the page. Each node is a fieldset; buttons move, insert, delete, save and publish. Moving a node into another container needs the JavaScript editor.

### Working directly on the page

- **Hover and select.** Moving over an element draws a dashed grey outline and nothing else. Clicking selects it: a solid outline and the element toolbar. Clicking a heading inside a column selects the heading, not the column.
- **Canvas strip.** Above the page: the **breadcrumbs** (Page, then each parent down to the selected element; pick one to select that parent) and the **zoom** buttons **Fit**, **100**, **75** and **50**. Zoom never goes below 50 percent, except that **Fit** may go lower on a phone-sized editor screen. A chosen zoom stays when the window resizes. On Tablet or Phone a thin coloured band along the top of the page, with the device's name, shows which width you are editing.
- **Element toolbar.** A small bar sits just outside the selected element (above it, or below it at the top edge): a drag handle, the element's name, then **Parent**, **Duplicate**, **Wrap**, **Add after**, **Hide** (its full name is "Hide on this device", or "Show on this device" when hidden), **Delete** and **More** (the same menu as a right-click). Move between the buttons with the arrow keys. On text the bar adds the hint "Double-click or Enter to edit text". While you type it shrinks to "Editing text · Enter to finish · Esc to cancel" and stays clear of the text formatting bar.
- **Edit text.** Select a heading or text and press **Enter** (or double-click). **Enter** finishes and keeps your text; **Esc** cancels and puts the text back as it was. See [Formatting text on the page](#formatting-text-on-the-page). `Ctrl+K` (`Cmd+K`) opens the command palette from the page, and the link panel while you type.
- **Add a section.** The round **+** buttons between elements, inside empty containers and at the top of the page (and on the empty page) open a picker of layouts: 1, 2, 3 or 4 columns, 33/67, 67/33 and a 2×2 grid. Pick one and an empty section with that layout is added at that spot and selected. The **+** buttons sit in the gaps between elements (never over their text) for the selected element's container and the one you point at, and an insertion line shows where the new section goes; **Choose an element…** and **Insert a ready-made section…** open the Add tab, which then inserts at the same spot. Empty cells are drawn with a dashed outline in the editor so you can click them. The **+** buttons are for the pointer; with the keyboard select an element and use **Add after** in its toolbar, which opens the same picker right after it, or the **Add** tab (`/`).
- **Element menu.** Right-click an element on the canvas or a row in the Navigator, or press **Shift+F10** (or the menu key) on a row. The menu offers Copy, Cut, Paste after, Paste inside, Duplicate, Rename, Wrap in container, Unwrap, Copy style, Paste style, Save as template…, Save as block… and Delete; what cannot apply here is greyed out. Arrow keys move, **Esc** closes it and focus goes back to the row.
- **Copy and paste.** Copy and Cut put the elements on your system clipboard, so you can paste them into another page of the same site (or another browser tab). Paste adds them after the selection with new ids, and the server checks them like a save: anything that does not fit is refused with a message and nothing is pasted. An element with a block that does not exist on this site is pasted with the missing-block placeholder.
- **Select several.** **Ctrl** or **Cmd** plus click adds or removes an element, **Shift** plus click selects the range among siblings, in the Navigator or on the canvas; **Ctrl+A** selects the siblings. Delete, Duplicate, Hide, Wrap and Copy act on all of them in one undo step. The inspector says how many are selected and edits none; drag, rename, Paste inside and style paste need exactly one.
- **Rename.** Press **F2** on a row, or double-click it, type a name and press Enter (Esc cancels). The name appears in the Navigator, the breadcrumbs and the toolbar and is never shown on the public page. An empty name goes back to the default. Pages with a named element need this version or newer to open; see the rollback note in the release notes.
- **Move handle.** The selected element's toolbar on the canvas has a handle: drag it to move the element; a line shows where it will land. Esc cancels.
- **Restore one element.** Open **Element options** in the inspector (or right-click the element) and choose **Restore from an earlier version…**. A window lists the earlier saved versions of the page that held this element, newest first, with who saved them and when. Pick one to put just this element back as it was, with the elements inside it that the page has now; the rest of the page does not change. If the element was a different kind then, it is replaced at the same place. It is one step in the history ("Restore Heading from version 4") that you can undo. It only reads saved versions; nothing is stored until you save the page.
- **History.** **Ctrl+H** or the clock button lists the changes made since the page was opened, starting with "Original". Rows say what happened, in plain words: "Add Heading", "Delete Container", "Move Button", "Change Text colour (Tablet)", "Paste 3 elements". Pick one to go back (or forward) to that point in a single step; you can undo that jump like any change.

### Shortcuts

Shortcuts act on the page, never inside a text field, except `Ctrl` or `Cmd` plus `S` and `Ctrl` or `Cmd` plus `K`, which work everywhere. Press `?` for the sheet, which is grouped (Add and insert, Select and move, Edit, View and device, History) and searchable.

| Keys | Action |
|---|---|
| Up, Down | previous, next element in the Navigator |
| Left, Right | collapse or go to parent, expand or go to first child |
| Home, End | first, last element |
| Alt+Up, Alt+Down | move among siblings |
| Alt+Left, Alt+Right | outdent, indent |
| Delete | remove |
| Ctrl/Cmd+D | duplicate |
| Ctrl/Cmd+C, Ctrl/Cmd+X, Ctrl/Cmd+V | copy, cut, paste after the selection |
| Ctrl/Cmd+G, Ctrl/Cmd+Shift+G | wrap in a container, unwrap |
| Ctrl/Cmd+A | select the siblings |
| F2 | rename the row |
| Shift+F10, menu key | element menu |
| Ctrl/Cmd+H | history |
| Ctrl/Cmd+Alt+C, Ctrl/Cmd+Alt+V | copy, paste the element's style |
| Ctrl/Cmd+Z | undo |
| Ctrl/Cmd+Shift+Z, Ctrl+Y | redo |
| Ctrl/Cmd+S | save |
| Enter | on the page: edit the selected text. In the Navigator: open the element's settings in the inspector |
| `/`, `A` | open the Add tab with its search focused |
| Ctrl/Cmd+K | command palette (in a canvas text edit: insert link) |
| Shift+Enter (in the Add search) | insert and keep the search open |
| Ctrl/Cmd+Alt+[ , Ctrl/Cmd+Alt+] | show or hide the Navigator, the Inspector |
| `?` | shortcut sheet |
| Escape | cancel a drag |

Inside the canvas frame undo, redo, duplicate, delete, copy, cut, paste and wrap work, and a right-click opens the menu; the rest (F2, Ctrl+A, Ctrl+H, Ctrl+K, the arrow keys) need focus outside it. Typing in a field, the rename box or the page's own text never triggers them.

## Live data

Two features show live site data without code. Both are stored in the page's own document, with no schema change.

### Bindings

Some properties show a **Bind** button beside the control: the text of a heading, text or button, a button's address, an image's source and alt text, and a few more (quote, stat, table caption, icon label, video title, map query, tab and accordion titles). Bind opens a "Bind to" list of the sources valid for that property, and the field then shows a chip with the source and an **Unbind** button. Picking a source is one undo step.

| Source | Value |
|---|---|
| `entity.*` | the page or entity being shown: name, title, excerpt, dates, featured image, address, author name and address, or an attribute (`entity.attributes.<path>`) |
| `site.*` | site settings: name, tagline, phone, hours, address parts, logo, or any setting (`site.settings.<key>`) |
| `term.*` | a term's name, slug, description, kind and address, only where a term view exists |
| `loop.item.*` | the current item of a loop, valid only inside a loop |

The value is looked up when the page renders and then escaped like typed text, so a bound `javascript:` link renders `#`. A missing value renders empty, and the canvas shows the source name in angle quotes instead. A bound property has no inline text editing, and a bound heading or text ignores any formatting stored for it. Page bodies are never a source, and rich text, enums, numbers and switches cannot be bound. The outline marks bound nodes "(bound)", and without JavaScript it has a source select per bindable property. A bound value shows whatever the source holds, so a `site.settings.*` binding prints that setting on the public page.

### Query loop

The `loop` widget lists published entities and repeats its children once per item. It is seeded with an item template (a heading, an excerpt and a "Read more" button, all bound to `loop.item.*`) and an **empty state**, the content shown when nothing matches (**Add empty state** restores it).

| Option | Values (default) |
|---|---|
| From | `query` (default) or `archive`, the items and pager of the archive being shown |
| Schema, or the page's own type | the entity type to list |
| Term, Author | filter by a `<kind>:<slug>` term or a person slug |
| Order | `newest` (default), `oldest`, `updated`, `name`, `name_desc` |
| Limit | 1 to 48 (12) |
| Offset | 0 to 1000 (0) |
| Paging | `none` (default) or `pages` |
| Exclude current | leave the page being shown out of the list |

| Rule | Behaviour |
|---|---|
| What is listed | Only published entities. |
| Nesting | A loop inside a loop is refused when you save. |
| Loops per render | At most 8, counting those in header, footer and archive parts and in blocks. Later loops print nothing publicly and show a "Loop limit reached" notice in the canvas. |
| Paging | With `pages` the loop shows a pagination nav and `?page=N` selects the window, only on a page that is not itself a listing and only for its first paged loop. See [URL patterns](../ai-seo/url-patterns.md). |
| Canvas | Shows at most 3 items. A loop with no result shows a dashed "No items match" box with its empty state. |
| Older releases | A page that uses a loop or a binding cannot be read by an older release, so use them only once the new version runs everywhere. |

### An archive built in the builder

In an archive layout part, the `slot` node has a **replace** option that drops the theme's own listing: only the part's markup before and after the slot prints. Pair it with a `loop` set to `archive` to build the blog archive from nodes. Replace is refused in any other part. Category and author layouts are not wrapped by parts, so they cannot be built this way.

## Design system

Owners shape the look of builder pages site-wide under **Theme**:

| Screen | Address | What it holds |
|---|---|---|
| Design system | `<admin>/theme/design-system` | Colours (`surface`, `muted`, `accent`), the spacing scale 0 to 12, the type scale `xs` to `4xl` and three shadows, with "Reset to defaults". A notice appears when the active theme has not opted in (`"builder": {"tokens": true}` in its `theme.json`). |
| Style presets | `<admin>/theme/presets` | Named style maps (up to 48) with values per breakpoint. The page lists the keys each preset sets, with an **Add a setting** row for any other key. A node references one by name. Editors can create a preset from the Style tab (**Save as class…** in the Class row) but cannot change or delete one. |

### Site styles in the builder

**More > Site styles** (owners only; editors never see it) opens the same colours and corners as **Theme** in a panel beside the canvas, so you can change them without leaving the page. It edits:

| Field | What it changes |
|---|---|
| Brand, Secondary, Background, Text | The theme colours (the **Theme** colours page). Each has a colour picker and a hex box. |
| Accent, Surface, Muted | The builder colours of the Design system. Shown only when the theme uses builder colours. |
| Small, Medium and Large corners | The corner sizes (a number and a unit such as `8px` or `0.5rem`). |

**Save site styles** stores only what you changed, through the same checks as the Theme pages, and refreshes the preview and the colour palette without reloading the page. A refused value is marked on its field. The default theme paints with Brand, Background and Text; it does not use the theme's **Secondary** colour, and Theme's own accent and secondary settings do not drive the public page. Only the builder **Accent** does. **Open full theme settings** goes to the Theme page.

On a node, the Style tab has a Preset panel (apply, see which values differ from the preset, reset them, **Detach to custom**) and a Copy style and Paste style pair. A value set on the node always wins over the preset. Saving tokens or presets purges the page cache.

## Reuse

Three ways to reuse layout, none needing a database change:

- **Templates.** **Save as template…** and **Save page as template…** at the foot of the Add tab (and in the element menu and the `Ctrl+K` palette) open a small name window and store a copy. Inserting always makes fresh ids. Templates are listed under **Theme > Templates** (`<admin>/theme/templates`) together with older section templates. A template is a copy: later edits do not propagate.
- **Blocks.** **Save as block…** turns the selection into a shared block and puts a reference in its place. Pages that use the block all show its latest saved content. Blocks are listed under **Theme > Blocks** (`<admin>/theme/blocks`, owners and editors). A block opens in the same builder with **Save block** (no autosave, no Publish) and a "Used on N pages" list. Blocks are always drafts and never public pages. They can nest three deep, and a cycle is refused. Deleting a block leaves a "Missing block" box in the editor and only a comment on the public page. **Detach** turns a reference back into ordinary nodes.
- **Layout parts.** **Theme > Layout parts** (`<admin>/theme/parts`, owners) assigns a header, footer, single or archive part to the site, or "None" to use the theme's own. A single or archive part must contain exactly one `slot` node, where the page content goes. Slots your theme does not support show "Your theme does not use this part".

Saving a block or a part purges the whole page cache. See [Performance and the page cache](performance-cache.md).

## The blog editor

Entity types that use the post editor open in the blog editor at `<admin>/entities/{id}/edit`. It is one form: an inline title, the body and a sidebar.

### Sidebar

| Card | Fields |
|---|---|
| Status | Status, and **Publish at** to schedule the post in the site time zone |
| Slug, Featured image, Excerpt | The address slug, the image shown with the post, and its short summary |
| Categories and tags | Owners can create a term inline. |
| Author | A picker listing people and the site organisation. **Or add a new author** creates a person who is not public and has no login. Choosing the site name credits the organisation. |
| SEO | Search tab: focus keyword, SEO title, SEO description. Social tab: social image. Advanced tab: canonical address, "Ask search engines not to list this post" and **Include in /llms.txt** |

A draft has **Save draft** and **Publish**; a published post has **Update**. Without JavaScript the body is a plain HTML text area and every field still saves.

### Autosave and recovery

| Rule | Value |
|---|---|
| When the browser saves | 3 seconds after the last keystroke, at least 10 seconds apart, and at most 30 seconds into continuous typing |
| What is autosaved | The title, body, excerpt and featured image of a **draft** only |
| Published and scheduled posts | Never autosaved; a live post changes only when you press **Update** |
| Versions | Autosaves from one person within 5 minutes share one version, labelled "Autosaved in the blog editor". Save, Publish and Update always add a version. |
| Conflict | If somebody saved in between, you see **Overwrite with my version** and **Reload their version**. |
| Recovery copy | Unsaved work on a published post is kept on the server, one copy per person and post, and survives sign-out and a change of device. It is offered on your next visit only when it differs from the stored post. It is deleted when you save, publish or update, when you choose Discard, after 30 days, or when the post or your account is deleted. Drafts rely on autosave instead. Copies an older version left in the browser are removed and never restored. |

### Writing

The editor uses a rich-text surface. Type `/` for the slash menu: text, headings 2 to 4, lists, quote, code block, table, library image, upload image, video embed, divider, callout, FAQ block and HTML block. Select text for a bubble menu (bold, italic, link, inline code). Markdown shortcuts such as `##` or `*` work as you type.

| Keys | Action |
|---|---|
| Ctrl/Cmd+S | Saves a draft now. On a published post it reminds you to press **Update**. |
| Ctrl/Cmd+K | Adds a link |
| Ctrl/Cmd+Shift+F | Toggles focus mode, which dims every block except the one you are in |
| Escape | Leaves focus mode |
| Alt+Shift+T | Shows or hides the formatting toolbar |

| Content | Behaviour |
|---|---|
| Images | Paste or drop an image and it uploads to the media library with a progress bar. Alt text and caption are inputs under the picture. A captioned image becomes `<figure>`. |
| Embeds | YouTube and Vimeo addresses only, rewritten to the privacy-friendly player addresses. |
| FAQ blocks | Reach the public page as FAQ structured data as well as visible markup. |
| Pasting | Content from Google Docs or Word is cleaned: wrapper elements, classes, styles and ids are dropped, Word lists become real lists, and `h1` becomes `h2`. |
| HTML block | Cleaned to the sanitiser's tag set and shown as source, never rendered inside the admin. |

Whatever the browser sends, the server sanitises the body again on every save.

### Search and answer panel

The panel checks the post as you write, refreshed 1.5 seconds after you stop typing:

- a search-result preview with title (50 to 60 characters) and description (120 to 160) length checks;
- the focus keyword in the title, first paragraph, description, slug and a subheading, and its density;
- heading order, image descriptions, internal and external links, length and readability;
- answer checks for AI assistants: question headings, short direct answers beneath them, a short opening summary, a FAQ block and `llms.txt` inclusion;
- the JSON-LD the public page will emit.

Once published, an **AI crawlers** card shows which crawlers fetched the post.

### Preview

Preview stages your current draft and loads it in a sandboxed frame at **Desktop** (1100 pixels) or **Mobile** (390 pixels) width. Without JavaScript the button posts the form and opens the rendered page in a new tab.
