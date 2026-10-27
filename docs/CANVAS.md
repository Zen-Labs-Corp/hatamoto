# Canvas protocols and node rendering

`<admin>` stands for the install's private admin address, the path that `hatamoto admin-url` prints. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address.

The page editor shows the page in a sandboxed frame (`allow-scripts`, never `allow-same-origin`). The frame runs the
embedded bridge (`backend/internal/render/canvas_bridge.js`); the admin parent runs `js/canvas/host.js`. They talk
with `postMessage`. Two message families exist, told apart by the `hm` field:

| Family | `hm` | Scope | Used on |
|---|---|---|---|
| v1 | `1` | sections and their fields | every page editor |
| v2 | `2` | any node, in-place patching | pages whose `page_doc` the form can carry (see "Gating") |

`data-hm-contract` on `<html>` stays `"1"`: it is the marker contract, not the message protocol.

## Negotiation

```
host                                   bridge (in the frame)
  | -- frame loads, bridge starts in v1 mode -->
  | <-- {hm:1,type:"hm:ready",contract:"1"} ---|
  | --- {hm:1,type:"hm:init",...} ------------>|   v1 hosts stop here
  | --- {hm:2,type:"init",editable,selectedKey}>|   v2 hosts only
  | <-- {hm:2,type:"ready",contract:"2"} -------|   bridge now speaks v2
  | <-- {hm:2,type:"layout",...} ---------------|   replaces hm:layout / hm:select
  | --- {hm:2,type:"move"|"insert"|...} ------->|   patches, last one with a seq
  | <-- {hm:2,type:"painted",seq} --------------|
```

A v1 host never sends `init`, so an older cached admin keeps working against the new bridge. In v2 mode the bridge stops
sending `hm:layout` and `hm:select`; every other v1 message (`hm:edit`, `hm:edit-text`, `hm:rich-open`,
`hm:details-open`, `hm:chrome-click`, `hm:save`, `hm:scroll`) is unchanged in both modes.

## Trust model

Every inbound payload is untrusted. The host accepts a message only when `event.origin === "null"` (the sandboxed frame),
`event.source` is the active frame, the frame is still the one it loaded, and `parseFrameMessage` returns a typed
message. Extra fields are dropped. The bridge validates what it receives with the same rules (`validToBridge`), ignores
anything without `hm` 1 or 2, never navigates, and uses no `eval`. Node ids match `^[A-Za-z0-9_-]{1,64}$` and nothing
else reaches a selector.

## Protocol v1 (`hm:1`)

Frame to parent:

| Type | Fields |
|---|---|
| `hm:ready` | `contract` |
| `hm:layout` | `docHeight`, `sections:[{id,type,top,height}]`, optional `header`, `footer` boxes |
| `hm:select` | `sectionId`, optional `path` |
| `hm:edit` | `sectionId`, `path`, `value`, `kind:"plaintext"` |
| `hm:edit-text` | `sectionId`, `path`, `index`, `value`, `expect` |
| `hm:rich-open` | `sectionId`, `path`, `rect` |
| `hm:chrome-click` | `area:"header"|"footer"` |
| `hm:scroll` | `y` |
| `hm:save` | none |
| `hm:details-open` | `fields:[{sectionId,path}]` |

Parent to frame: `hm:init` (selection, editable flag, open details), `hm:select` and `hm:scroll-to`, as sent by `host.js`;
the frame ignores a v1 message that is not well formed.

## Protocol v2 (`hm:2`)

Parent to bridge (all accept an optional `seq`, a non-negative integer echoed in `painted`; `host.apply` puts one on the
last message of a batch). A node reference is `{sel:"section"|"node", key:id}`; a placement is exactly one of
`before`, `after`, `parent`, each a node reference (`parent` may also be `{sel:"main"}`).

| Type | Fields | Effect |
|---|---|---|
| `init` | `editable`, `selectedKey` (id or null) | switches the bridge to v2 |
| `patch` | node ref, `html`, optional `css` | replaces a node |
| `insert` | `html`, optional `css`, one placement | adds a node |
| `move` | node ref, one placement | moves a node in the DOM |
| `remove` | node ref | removes a node and its css |
| `setVar` | node ref, `decls:[[prop, value|null]]` | inline declarations (the style preview) |
| `css` | node ref, `css:{id:rules}` | swaps the node's `<style data-hm-css>` and drops the inline values |
| `edit` | node ref | starts inline editing of a node's text |
| `select` | node ref, `scrollIntoView` | selects a node |
| `scrollTo` | `y` | scrolls the frame |
| `format` | node ref, `cmd` (`bold`, `italic`, `underline`, `strike`, `code`, `link`, `unlink`, `clear`, `done`), `href` (required for `link`), optional `newTab` | applies an inline mark to the selection of the heading or text being edited; `done` ends the edit |
| `force` | node ref, `state` (`hover`, `focus`, `active`, `parent-hover` or `null`) | shows a state on the node by setting `data-hm-force` on it (on its parent node for `parent-hover`); `null` clears it |

Bridge to parent:

| Type | Fields |
|---|---|
| `ready` | `contract:"2"` |
| `layout` | `nodes:[{id,k,t,p,x,y,w,h,c?,dir?}]`, `main:{x,y,w,h}`, `docH` |
| `hover` | `id` (or `null`), `k` |
| `select` | `id`, `k`, `shift`, `ctrl` (Ctrl or Cmd held: toggle in a multi-selection) |
| `contextmenu` | `id`, `k`, `x`, `y` (right-click on a node; `x`,`y` are frame-viewport px) |
| `text` | `id`, `k`, `prop`, and `value` (plain text) or, for `prop` `html`, `html` (canonical inline markup) |
| `selection` | `id`, `k`, `prop`, `rect:{x,y,w,h}` (document px, the space of `layout` rects), `marks` (the marks active at the selection), optional `el` (the edited element's box), `link` (the link under the selection), `kind` (`heading` or `text`: the bar offers only B, I, Link and Clear on a heading) |
| `textend` | `id`, `k`, `prop` (the text edit on that prop ended: blur) |
| `textlimit` | `id`, `k`, `prop` (typing or paste was refused: the text is at the 20,000-character bound; the host shows a message) |
| `painted` | `seq` |
| `key` | `key`, `shift` (see the key list below) |

`k` is `s` when the element is a section (`data-hm-section`), else `n`. `t` is the section type or widget kind
(`data-hm-t`), `p` the parent node id or `""`, `c` is `1` for a container, `dir` is `row`, `column` or `grid`.

### Validation rules

These are what `protocol.js` enforces and `testdata/canvas_protocol_cases.json` pins (the Go mirror test reads them).

* A message is an object with a string `type`; `hm` must be exactly the number 1 or 2 (not `"2"`, not 3).
* All numbers are finite. Ids match `^[A-Za-z0-9_-]{1,64}$`. `k` is `"s"` or `"n"`. Booleans are real booleans.
* `layout`: at most 5,000 nodes; `t` is at most 100 characters; `p` is `""` or an id; `c`, when present, is `1`; `dir`,
  when present, is `row`, `column` or `grid`; `docH` and the `main` rect are finite numbers.
* `force`: a node ref and `state`, which is one of `hover`, `focus`, `active`, `parent-hover` or `null` (exact case; anything else or a missing `state` is refused; the validators do not look at extra fields). `data-hm-force` lives only in the bridge's DOM: it is never serialised, saved or present in public output, and the force-aware selectors (`SEL[data-hm-force="hover"]`) are compiled only for canvas renders. A `select` of another node, `force null` or a tab change clears it; the bridge re-applies it after `patch`, `insert`, `move` and `css`.
* `hover`: `id` is `null`, or an id with a `k`.
* `text`: `prop` matches `^[A-Za-z0-9_-]{1,40}$`. Exactly one of `value` or `html` is present, each a string of at most 200,000 characters: `html` only with `prop` `html`, `value` only with any other prop (both, neither, `html` on another prop and `value` on `html` are refused; other fields are dropped). The parent never trusts `html`: the server sanitises it on save and render.
* `selection`: `id`, `k`, `prop` as for `text`; `rect` has four finite numbers; `marks` has at most 6 entries, each one of `bold`, `italic`, `underline`, `strike`, `code`, `link`, without repeats. `kind`, when present, is `heading` or `text` (anything else drops the whole message). Sent while a heading or text is being edited, when the selection moves.
* `format`: a node ref and `cmd`, one of `bold`, `italic`, `underline`, `strike`, `code`, `link`, `unlink`, `clear`, `done` (exact case). `href` is a string of 1 to 2,000 characters and is required for `link` only; `newTab` is an optional boolean. The bridge re-checks the link target before it writes it, and the server sanitises the result.
* `textend`: `id`, `k` and `prop` as for `text`, no value. The host re-renders a loop whose item text was typed in (inline `text` sends no patch under a loop).
* `select`: `shift` and `ctrl` are both required booleans.
* Editing style: the bridge's injected style floors an empty `[data-hm-node]` at `min-height: 48px` with a dashed outline (editor only), so a fresh layout (empty columns) can be selected and dropped into. The public page never carries it.
* `contextmenu`: `id`, `k` as for `hover`; `x` and `y` are finite numbers (frame-viewport pixels; the host adds the frame offset and scale). The bridge cancels the browser menu only for an editable v2 frame, and only on a node.
* `painted`: `seq` is a non-negative integer. `key`: 1 to 20 characters. The bridge forwards Delete and, with Ctrl or Cmd (no Alt), `z Z y d` and the node clipboard and wrap keys `c C x X v V g G` (`shift` carries Shift); never while a widget text is being edited (the one exception: Ctrl or Cmd+K while a heading or text is edited forwards `k` so the host can open its link dialog; Ctrl/Cmd+B, I and U apply in the frame). Outside a text edit Ctrl or Cmd+K (no Alt, no Shift) forwards `k` too (the frame cancels the browser search) and the host opens the command palette: the host tells the two apart by its own edit state. Enter on the selected element, outside a text edit, starts editing its own text (no message: the same as the `edit` message the host sends from the Navigator or the toolbar; an element whose text belongs to a descendant is not edited), and not `c`/`x` while page text is selected. F2, Shift+F10, Ctrl+H and Ctrl+A are not forwarded: they act in the parent only.
* `html` at most 1,000,000 characters. `css` is a map of at most 200 entries, each key an id and each rule at most 100,000
  characters. `decls` has at most 50 entries, each `[prop, value]` with `prop` matching `^[a-z-]{1,40}$` and a value of
  at most 200 characters or `null`.
* A placement has exactly one of `before`, `after`, `parent`.
* Extra fields are dropped from accepted messages; any other violation makes the whole message invalid.

## Render endpoint

`POST <admin>/entities/{id}/edit/render` (session and CSRF, like every admin write) takes the JSON doc mode body described
below. Nothing is stored: the draft lives for the request. The v1 form body (`_hm_ops`, `_hm_render`, `_hm_css`, `_hm_seq`)
was removed with the classic editor.

Reply `200 {fragments, css, order, seq}`:

* `fragments:[{sel,key,html,css}]`: `sel` is `"section"` (key = section id) for top-level v1-shaped nodes and `"node"`
  for native nodes. `html` is exactly the node's slice of a full canvas render; `css` is the compiled style of its subtree.
* `css`: compiled rules for the ids in `_hm_css`.
* `order`: the top-level ids the draft renders, in order. The client compares it with what it expects.

Errors: `400` (bad JSON field, too many ops or ids), `403` (no CSRF), `404` (unknown entity, or no render dependency),
`409 {"error":"no_doc"}` (no usable `page_doc`), `413` (body over 4 MiB), `422 {error}` (an invalid op, or a draft that
does not validate; all or nothing). The endpoint never renders a full page.

`GET <admin>/builder/stylespec` returns `builder.StyleSpecTable()` as JSON (`Cache-Control: private, max-age=3600`, ETag).
The host fetches it once, the first time `setStyle()` is called, and previews a change with `setVar` before the compiled
`css` from the render endpoint replaces it. Keys the table marks `settle` (`hide display cols gtc grad bgimg`) send no `setVar`: the compiled `css` alone shows them. The compiler stays in Go.

### JSON doc mode (the builder)

The same URL takes `Content-Type: application/json` from the builder editor. The body is the doc itself, so builder-only
pages work and no form is involved:

```json
{"doc": {"v": 2, "nodes": []}, "ops": [], "render": ["id"], "css": ["id"], "seq": 7}
```

`doc` is required and must parse (`422 {error, code:"invalid_page_doc"}`). The admin forwards it to the node render
endpoint with the stored attributes minus `page_doc` and `page_sections` (chrome and meta only) and the entity of the
URL, never one from the body. The render endpoint applies `ops` to `doc` in memory, so `doc` is the doc **before** the ops.
The reply is the same `{fragments, css, order, seq}`. The body is limited to 4 MiB (`413`). The v1 form mode and its `nodeops` gate were removed with the classic editor (B10).

`POST <admin>/entities/{id}/builder/stage` takes `{doc}` and answers `{url}`: the doc staged as a canvas page under the
same ticket store as the preview (`GET .../edit/preview/{ticket}`): the ticket is one-time, valid 60 seconds, and bound to
the signed-in session and the entity, so every canvas load stages a fresh URL. Anything else gets the admin 404. The
builder uses it for the first load and to reload the canvas after a drift.

### Loops and bindings in the canvas

The canvas renders the same code as the public page, with real data, bounded: a `loop` shows at most 3 items. Item 1
carries the `data-hm-*` markers (real ids, selectable); items 2 and 3 are plain copies without markers. A loop with
no result (or a failed query) shows a dashed "No items match" box plus its empty state. A bound prop has no
`data-hm-prop` (no inline text editing) and shows its value, or `‹source›` when the value is empty. `entity.*` reads
the draft's attributes and name; the canvas has no entity URL or author.

## Self-heal

The builder editor sends JSON doc mode for every committed op, patches the frame with the
fragments, checks `order` and the frame's next `layout` the same way, and on any mismatch or failed call restages the
doc (`builder/stage`) and reloads the frame.

## Inline rich text

A double-click on a heading or text node (`data-hm-prop` `text` or `html`) makes the element `contenteditable="true"`;
every other prop stays `plaintext-only`. The bridge reads the element through a pure canonicaliser (`clean`, `ser`:
allowed marks only, `b/i/strike` mapped to `strong/em/s`, every attribute and other tag dropped), never `innerHTML` of
live DOM, and posts `text` with `prop:"text"` and `value` when the content has no marks, else `prop:"html"` and `html`.
No `patch` is sent while typing; `textend` makes the parent re-render the node from the server.

```
frame: input -> clean/ser -> text{html|value} ----> parent: inline ops (html + text projection), no patch
frame: selectionchange (rAF) -> selection{rect,marks,kind} -> parent: lazy formatting bar above the node (a link panel of at most 360 px with a page and anchor list)
parent: bar button / link dialog -> format{cmd,href,newTab} -> frame: Range edit, input event, selection report
frame: Ctrl/Cmd+K -> key{key:"k"} -> parent: link dialog (in a text edit) or command palette (otherwise); Enter / Esc / format done / click elsewhere -> textend
```

Undo in a rich edit: a format (the bar, Ctrl/Cmd+B, I, U, the link dialog, Clear) rewrites the editor's DOM, which breaks the browser's own undo, so the bridge keeps a snapshot per format (markup before it, and the selection; at most 50, per edit session). Ctrl/Cmd+Z, or the Edit menu's undo, restores the last snapshot while the text still is what that format produced; once something was typed after it, the browser's undo takes the typing back first and the format is next. There is no redo for a format. Nothing crosses the wire: the restore is an ordinary `text` report.

Keys in the frame while editing: Ctrl/Cmd+B, I, U apply locally (no round trip, no `<b>`/`<i>`); Ctrl/Cmd+K is the one
key forwarded out; Enter ends the edit; Shift+Enter inserts `<br>` in a text only; Esc restores the serialised markup
it started with. Focus: the bar keeps focus in the frame (its pointer events are cancelled); the link field takes it,
and the frame keeps the edit and its selection (kept as plain-text offsets) until Apply, Esc or `done`. Chromium
reports the editor's blur before the frame loses focus, so the bridge decides a tick later.

Component fields (v1 messages in a v2 canvas): `hm:select` with a `path` only selects; `hm:rich-open` and `hm:edit`
select the component, open the inspector and focus that field (the parent re-renders the node from stored content on
`hm:edit`, since a component's content has no path setter on the client).

## Host chrome (not in the frame)

The parent page draws the editing chrome over the frame, so the bridge stays small and the public markup never carries
it: one hover outline (dashed, neutral) and one selection outline from the `layout` rects, the element toolbar, the
breadcrumbs and zoom strip, the Tablet or Phone band and the round "+" handles. They are built by the lazy
`canvas/chrome.js` (geometry in `canvas/points.js`) from `layout` (`id`, `k`, `p`, `c`, `dir`, boxes), `hover` and
`select`. A v1 frame's own outlines are drawn only on a v1 canvas; a v1 `hm:select` never replaces a deeper v2
selection. The toolbar and handles are outside the element, so the bridge needs no extra protocol message for them:
they act by the existing parent operations (select, duplicate, wrap, hide, delete, insert) and by `edit` for text.

Keys from the frame that matter here: **Enter** on the selected element starts editing its own text (handled in the
bridge, no message), **Esc** in an edit restores the text and **Enter** in an edit ends it, **Ctrl/Cmd+K** is forwarded as
`key` and the host opens the link dialog during a text edit and the command palette otherwise, and **Ctrl/Cmd+Z** in a
text edit undoes the last format from the bridge's snapshot stack (see [Inline rich text](#inline-rich-text)).

## Canvas markers

`data-hm-node` (id) and `data-hm-t` (section type or widget kind) exist only in canvas renders; public output and the
goldens never carry them.

## Design system in the canvas

The canvas base style (`hm-builder-base`) carries the `:root` token block (opted-in themes) and the rules of **every**
style preset, because a patch carries only per-node rules: a node given a preset by a patch is styled at once. Per-node
tags use the same compile mode (the `fs` token form) as a full render, so patches and fresh renders agree. A change
to a preset made with "Save as preset" reloads the canvas once so the new rules appear.

## Reuse in the canvas

A `ref` is one unit: the wrapper carries `data-hm-node` (the ref node's id, which is in the client doc) and the block's
nodes inside it are rendered without markers, so a click selects the ref and the client and the canvas agree on ids.
A missing block, a cycle or a limit shows a dashed "Missing block" box (its CSS is added to the canvas head only when
the doc has a ref, so other canvases are unchanged). Patches (`RenderTop`, `RenderNode`, `NodeCSS`) expand refs the
same way as a full render. A block saved elsewhere shows after the canvas is reloaded. The canvas of a `theme_part`
sets `NoParts`, so the assigned header or footer is not drawn twice; a `slot` is a dashed "Content slot" box in the
canvas and `<!--hm-slot-->` in public output.

## Widgets in the canvas

The canvas base style (`hm-builder-base`) carries the CSS of every widget kind, because a patch carries only the
per-node rules; a widget inserted by a patch is styled at once. Islands (tabs, video) are never loaded in a canvas
render: tabs show every panel stacked, a video shows its link, so each part stays selectable. Widget markup carries
the same `data-hm-node` and `data-hm-t` markers as the base kinds. A form, menu or breadcrumbs widget with nothing to
show prints a placeholder in the canvas only.
