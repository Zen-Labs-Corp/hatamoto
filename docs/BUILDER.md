# Builder: the page_doc model

`<admin>` stands for the install's private admin address, the path that `hatamoto admin-url` prints. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address.

`attributes.page_doc` is a tree of layout nodes. It is the document of a page; Go is its only renderer.
`attributes.page_sections` (the flat v1 list) is a **frozen legacy key**: nothing writes or rewrites it any more, a
page that has a doc ignores it, and readers that want section-shaped data (the public pages JSON, plugins, hooks) get
it derived from `page_doc` at read time. There is no DDL: the key stays in stored rows. Rollback floor: v0.27.0 (a
binary older than that cannot read the doc).

Code: `internal/builder` (model, validator, ops, StyleSpec, conversion; it imports neither `render` nor `entity`),
`internal/render/node.go` (node renderer), `internal/entity/page_doc.go` (write rules).

## The doc

```json
{"v": 2, "nodes": [
  {"id": "row", "t": "container", "p": {"tag": "div"}, "s": {"base": {"cols": "2", "gap": "space-4"}, "sm": {"cols": "1"}},
   "c": [
     {"id": "left",  "t": "container", "p": {"tag": "div"}, "c": [{"id": "h", "t": "heading", "p": {"text": "Hello", "level": 2}}]},
     {"id": "right", "t": "component", "p": {"type": "contact_form", "content": {"form_slug": "quote"}}}
   ]}
]}
```

A node is `id`, `t` (kind), `p` (props), `b` (bindings, see [Dynamic data](#dynamic-data)), `s` (styles by breakpoint), `c` (children, containers only).

| Kind | Props |
| --- | --- |
| `container` | `tag` (`section div header footer main article aside nav`), `layout` (`flex`, `grid`), `hidden`, `v1`, `href`, `target`, `rel` (a linked container, see [Element settings](#element-settings-builder-e6)) |
| `component` | `type` (a theme section type), `title`, `content` (the section's content object), `hidden` |
| `heading` | `text`, `level` 1-6, `hidden` |
| `text` | `text`, `hidden` |
| `button` | `text`, `href`, `target`, `rel`, `hidden` |
| `image` | `src`, `alt`, `hidden`, `href`, `target`, `rel`, `caption`, `lightbox` |
| `spacer`, `divider` | `hidden` |
| `ref` | `block` (a block id, lowercase uuid), `hidden`; no children; renders the block in place ([Reuse](#reuse)) |
| `slot` | `hidden`, `replace`; only in a `single` or `archive` layout part: where the page content goes (`replace` is archive only) |
| `loop` | query loop, see [Dynamic data](#dynamic-data); children are the item template |
| `loopempty` | the loop's empty state; only directly inside a `loop`, at most one |

The `html` kind is rejected. Adding a kind is one entry in the registry in `internal/builder/schema.go`.

Limits: depth 8, 1000 nodes, 1 MB per doc. Ids match `^[A-Za-z0-9_-]{1,64}$` and are unique in the doc.
`heading` and `text` also take `html` (at most 20,000 characters): the same text with inline marks, written by the
canvas and inspector editors. Where `html` is set and `text` is not bound, `html` is what renders, after the inline
sanitiser (see [Inline rich text](#inline-rich-text-builder-e5)); a bound `text` wins over `html`. `html` is never
bindable and never in a kind's defaults. Writers (REST, MCP, scripts) take note: while `html` is set, `html` is what
renders and a changed `text` is ignored (it only feeds search, the outline and hints). To change the text of a node that
has `html`, write `html` too, or remove `html` (REST/MCP: send the node without `p.html`); the API does not clear it for
you. If the sanitised `html` is longer than 20,000 characters the write fails with 422 `invalid_page_doc`.
Every node also takes universal props that are not kind props: `preset` (a style preset name) and `navlabel`
(the author's name for the node in the Navigator: a string of at most 80 characters, no control characters; it is
never rendered and never reaches the public page). Every kind except `component`, `ref`, `slot` and `spacer` also takes
the element settings `anchor`, `aria` and `tip` ([Element settings](#element-settings-builder-e6)). An empty name removes the prop. A hidden node (`p.hidden`) is not rendered. Text props are HTML-escaped; `href` and `src` accept `http`, `https`,
`mailto`, `tel`, relative paths, `/` and `#` only.

## StyleSpec

`s` maps a breakpoint (`base`, `md` up to 1024px, `sm` up to 640px) to style keys. A key may add a state:
`base:hover`, `md:focus`, `sm:parent-hover` (see [States](#states)). Values are tokens, enum members
or bounded literals; raw CSS is never accepted. The table is data: `builder.StyleSpecTable()` (served at
`GET <admin>/builder/stylespec`) returns every key with its allowed tokens, enums, patterns and bounds, so a client
can preview a change without a second mapping. The compiler exists only in Go (`builder.NodeCSS`). Writers over
REST, MCP and the admin all go through the same validation (`setStyle` ops and `page_doc` writes).

Lengths are `0` or a number (up to 4 digits, 2 decimals) with `px rem em % vh vw`. Space tokens are `space-0`..`space-12`.
Margins and the position offsets also accept `auto` and negative lengths; paddings and gaps do not.
Wherever a key says "tokens or length", the token wins when a value matches both.

| Group | Keys (value) |
|---|---|
| Layout (containers) | `dir` (`row column row-reverse column-reverse`), `wrap`, `ai`, `jc`, `ac` (align-content), `gap`, `rgap`, `cgap` (space), `cols` and `rows` (1 to 12), `gtc` (up to 6 tracks of `auto` or `1fr`..`12fr`, for example `1fr 2fr auto`) |
| In its parent | `span` (1 to 12), `cs` (column start, 1 to 13), `rs` (row span, 1 to 12), `as` (align self), `js` (justify self), `order` (-1 to 12) |
| Spacing | `pt pb pl pr` (space token or length), `mt mb ml mr` (space token, length, negative length, `auto`) |
| Size | `w minw maxw h minh maxh` (tokens `container`, `full`, or a length), `aspect` (`auto 1/1 4/3 3/2 16/9 21/9 3/4 2/3 9/16`), `fit`, `objpos`, `overflow` |
| Position | `pos` (`static relative absolute sticky`; **not** `fixed`), `top right bottom left` (like margins), `z` (0 to 100), `scrollmt` (a length in `px rem em vh vw`, no `%`) |
| Typography | `ff` (`body heading mono`), `fs` (type scale or length), `fw` (`100`..`900 normal bold`), `lh` (`tight snug normal relaxed loose`, or a number 0.8 to 3), `ls` (`normal` or ±99.99 `px rem em`), `ta` (`left center right justify`), `tt`, `td`, `fst`, `color` |
| Background | `bg` (colour), `bgimg`, `grad`, `bgsize bgpos bgrep bgatt` |
| Border | `radius` and the corners `rtl rtr rbr rbl` (token or length), `bw` and the sides `bwt bwr bwb bwl` (`0 1 2 4` or 0 to 20px), `bs` (`none solid dashed dotted double`), `bc` (colour) |
| Effects | `op` (opacity, 0 to 1 in steps of 0.05), `shadow` (`none sm md lg`) |
| Transform | `tx ty` (±400px or ±100%), `sc` (0.5 to 2), `rot` (±360deg), `skx sky` (±45deg), `torigin` (nine positions) |
| Filter | `blur` (0 to 20px), `bright contrast sat` (0 to 200%), `gray` (0 to 100%), `hue` (0 to 360deg), `bblur` (backdrop blur, 0 to 40px), `blend` (`normal multiply screen darken lighten color-dodge color-burn difference luminosity`) |
| Transition | `trans` (`none all colors opacity shadow transform transform-shadow`), `tdur`, `tease`, `tdelay` |
| Visibility | `hide` (`1`), `display` (`block flex grid inline inline-block table none`) |

Colours (`bg color bc`) are a token (`primary bg text surface muted accent`) or hex (`#rgb`, `#rrggbb`, `#rrggbbaa`).
`ff` compiles to the theme font variables with fallbacks (see [THEMES.md](THEMES.md)).

**Background image.** `bgimg` is a media-library path of the form `/media/<dir>/<name>.<ext>` (up to two directory
segments; `png jpg jpeg webp avif gif`, lower case). `http:`, `data:`, quotes, brackets, `..`, `//` and svg are
refused. It compiles to `url("…")`. Known limit: the prefix is fixed at `/media`, so a site that serves media from
another `MEDIA_BASE_URL` cannot use it (same limit as the video poster).

**Gradient.** `grad` is `linear-<angle>-<c1>-<c2>[-<c3>]` (angle 0 to 360, a whole number) or
`radial-<c1>-<c2>[-<c3>]`. A colour is a token or 3 or 6 hex digits **without** `#` (`linear-180-000-accent`
compiles to `linear-gradient(180deg,#000,var(--hm-accent,#c8102e))`).

**Gradient plus image.** `grad` and `bgimg` compile to ONE `background-image` declaration, computed from the values
in effect at each breakpoint (desktop-first), gradient layer on top of the image. A breakpoint that sets either key
gets the combined declaration, so a phone-only gradient keeps the desktop image. Known limit: a style preset and a
node compose separately: a node's `background-image` replaces the preset's.

**Order of declarations.** Keys are emitted alphabetically within a breakpoint, so the winner of two keys that write
the same property is fixed: `display` < `hide` (hide wins), `cols` < `gtc` (gtc wins), `radius` < the corner keys,
`bw` < the side keys. The longhands `cgap` (`column-gap`) and `cs` (`grid-column-start`) are emitted after everything
else so they follow the `gap` and `span` shorthands. A narrower breakpoint always wins over a wider one.

**Showing and hiding.** `hide: 1` is `display:none`. To show an element on one device only, hide it on a wider
breakpoint and set `display` on the narrow one: mobile-only is `hide: 1` at `base` and `display: flex` at `sm`;
desktop-only is `hide: 1` at `md` (it cascades to `sm`). When you unhide under a wider hide the builder
writes the nearest wider `display` you set, else the kind's own (`flex` or `grid` for containers, `grid` for columns and
gallery, `flex` for the buttons widget, stat, loops and synced blocks, `inline-block` for the button widget and icons, `table`, `block` otherwise).

**Known limits.** `position: sticky` does nothing inside an ancestor with `overflow` other than `visible`. A border
width shows nothing until a border style `bs` is set (there is no implied style). Offsets (`top`…) only matter once
`pos` is not `static`. `pos: fixed` is not offered. A widget pack's widget always un-hides as `block`: a pack cannot declare
a natural display, so a pack widget whose root is `flex` or `grid` loses that layout when it is un-hidden under a wider hide.

**Client preview.** Each key previews live through `setVar` unless the table marks it `settle`: `hide display cols
gtc grad bgimg` are shown only after the server's compiled `css` message (composition and cascade order cannot be
previewed per key). The Go table, the Go validator and the JS preview are tested against one fixture
(`backend/testdata/stylespec/`).

### States

A style key can carry a state: `base:hover`, `base:focus`, `base:active`, `base:parent-hover`, and the same on `md`
and `sm`. The grammar is closed (one `:`, only these four states); `xl`, `base:` and `base:visited` are refused.
A state holds only the keys the table marks `states` (colours, background, border colour and corners, opacity,
shadow, type colour and weight, and every transform and filter key); layout, size, position, display and the
transition keys are Normal only. `parent-hover` styles the node while its **parent** is hovered, so the node needs a
parent that is not a `ref` or `component`; style presets cannot hold it. Known limit: a direct child of a `loop` uses the loop wrapper as its parent, so it
lifts for every repeated item when any one of them is hovered. `focus` compiles to `:focus-visible`,
`active` to `:active`.

```json
"s": {"base": {"shadow":"sm","trans":"transform-shadow","tdur":"200"},
      "base:hover": {"ty":"-4px","shadow":"lg"}}
```

**Emission order.** Per breakpoint (`base`, `md`, `sm`, each inside its own `@media (max-width)` except base) the
rules are: plain, then `hover`, `focus`, `active`, `parent-hover`, each its own rule. The transition rule follows the
plain rule of its breakpoint.

**Cascade.** These follow from CSS specificity and are pinned by golden tests:

- A state selector carries an extra pseudo-class, so `base:hover` beats a narrower **plain** value (`sm`) while the
  element is hovered. A phone value for Normal does not silence a desktop hover value: set `sm:hover` to change it.
- `md:hover` after `base:hover` wins by order at equal specificity.
- A state value builds on Normal: a transform, filter, backdrop or background-image group keeps the Normal values of
  its other keys, so hover adding `ty` keeps a base `rot`. This holds at every breakpoint: when `md` sets `rot`, the
  compiler also emits the hover rule at `md`, composed from the Normal and hover values that apply there. (This is
  only for the composed groups; a plain key such as `op` is not re-emitted, see the first bullet.)
- Preset state selectors are doubled (`.hm-p-n.hm-p-n:hover`), so a preset's hover beats the node's own **base**
  value: a node's local Normal value never silences the preset's hover. The node's own hover ties with it and wins by
  order ("local wins within the same state").
- `parent-hover` wins over the node's own `hover` when both set the same key (deliberate, the ancestor drives), and a
  `base:parent-hover` value beats `sm:hover`.
- A node's composition ignores preset sub-key values (the same limit as gradient plus image).

**Transform, filter, backdrop.** Each group compiles to one declaration (`transform`, `filter`,
`backdrop-filter` with the `-webkit-` form) from the values in effect: the plain chain up to the breakpoint, then the
state chain. Order inside `transform` is translate, rotate, skew, scale; inside `filter` blur, brightness, contrast,
saturate, grayscale, hue-rotate. `torigin` and `blend` are plain declarations. `blend` is the closed list above; one common
blend mode is left out on purpose because the repository's neutral-content guard rejects the word.
Composition keys are never previewed inline; the canvas shows them after the server's compiled `css` message.

**Transitions.** `trans tdur tease tdelay` compile to `transition-property/-duration/-timing-function/-delay`
longhands (defaults `all`, `200ms`, `ease`, `0ms`; `none` is `transition-property:none`), set on the Normal rule only,
and **only inside** `@media (prefers-reduced-motion:no-preference)`, combined with the breakpoint as one query
(`@media (max-width:1024px) and (prefers-reduced-motion:no-preference)`). Visitors who ask for reduced motion get the
state change without the animation. `transform-shadow` (`transform,box-shadow`) means a hover lift needs no `all`.

**Force preview (canvas only).** The editor's State switch shows a state on the canvas by setting
`data-hm-force="hover"` (or `focus`, `active`; for `parent-hover` on the parent node). In canvas rendering every
state rule's selector list also holds the forced form (`.hm-n-x:hover,.hm-n-x[data-hm-force="hover"]`); the attribute
has the same specificity as the pseudo-class, so the cascade is identical. The public render never emits the forced
selectors and never carries the attribute (it exists only in the bridge's live DOM). See [CANVAS.md](CANVAS.md).


**Rollback floor.** This release is the floor for any page, section template or style preset that uses a new key,
or a new value of an old key (`ta: justify`, `dir: row-reverse`, `column-reverse`), and for any **state key**
(`base:hover` and the rest) or any transform, filter or transition key. An older binary validates
`page_doc` styles on every parse, not only on save, with no unknown-key tolerance:

- Public site: the stored doc fails to parse, so the page renders an **empty body** and logs
  `stored page_doc is invalid, rendering an empty body`.
- Admin: the page opens as a broken doc (no builder), and save, render and stage requests are refused with 422
  `invalid_page_doc`; the version diff reports the doc as unreadable.
- State keys: the older binary only knows `base`, `md` and `sm`, so `base:hover` fails with `unknown breakpoint`
  (checked against the `origin/main` parse path, `doc.go` walk, which rejects any `s` key outside the three on a native
  node; an orphan pack node was only read through the `base`, `md` and `sm` maps, so a state key on it parses there).
  The consequences are those above. In the other direction, this version refuses a whole doc whose orphan node carries
  an unknown `s` key such as `xl`, which the older one accepted; only a hand-written doc can have one.
- Style presets: the older binary drops only the entries with a new key (or a new value), not the whole preset. A
  preset that mixes old and new keys still applies, without the new ones; a preset with only new keys is ignored but
  stays stored. Saving the Style presets page on the older binary writes back what it kept, so it permanently deletes
  the new keys from mixed presets; rolling forward does not bring them back.

**Rollback floor for `navlabel` (builder-e4).** A node that carries `p.navlabel` cannot be read by an older binary,
which knows no such prop and rejects the doc: the public page renders an **empty body**, and the admin shows the page
as a broken doc (`docBroken`, saves refused with 422 `invalid_page_doc`). Naming a node in the Navigator is
therefore a one-way change for that page until the binary is current again. Pages with no `navlabel` are
byte-identical in output and storage. The paste endpoint, the history panel and the other editor changes are
client-side or write nothing, so they have no floor of their own. Before a rollback, clear the names (Rename, empty
value) on pages that must keep rendering.

**Rollback floor for `html` (builder-e5).** A heading or text node that carries `p.html` cannot be read by an older
binary (the schema has `additionalProperties:false`): the public page renders an **empty body** and the admin shows the
page as a broken doc (`docBroken`, saves refused with 422 `invalid_page_doc`). Formatting a heading or text on the
canvas or in the inspector is therefore a one-way change for that page until the binary is current again. The editors
only write `html` when the text has marks: plain text still goes to `text`, and a page with no formatted text is
byte-identical in output and storage. Before a rollback, remove `html` from every formatted heading and text on pages
that must keep rendering. In the builder: select all the text and press **Clear** in the bar, or clear the inspector
editor, which stores plain text again; but a **line break** (Shift+Enter) is markup, so a text with a line break keeps
`html` after Clear until the break is deleted. The no-JS outline cannot remove `html` (an empty `html` field there is a
no-op). Through the API, send the page_doc with `p.html` removed from those nodes (`text` already holds the plain
projection; `entities_update` / `PATCH /api/v1/entities/{id}` replace `attributes` as a whole, so read, delete
`p.html`, send back).

Nothing is rewritten by the upgrade itself, so rolling back and forward again restores the pages (presets saved in between excepted, see above). Before a rollback,
remove the new keys from pages that must keep rendering (older documents are byte-identical in output and are not
affected).

Public output carries one `<style id="hm-builder-css">` with a `.hm-n-<id>` rule per styled node, appended to the
head. A page with no styled or native node gets no `<style>` and no extra bytes. The CSP already allows inline
style (`style-src 'self' 'unsafe-inline'`); there is no nonce.

## Widgets

Besides the eight base kinds (`container`, `component`, `heading`, `text`, `button`, `image`, `spacer`, `divider`),
the builder ships widgets. A widget is a pair of embedded directories:

```
internal/builder/widgets/<kind>/schema.json       data model + palette metadata
internal/render/widgets/<kind>/widget.html        template (one element per node)
internal/render/widgets/<kind>/widget.css         kind CSS (minified at init)
internal/render/widgets/<kind>/island.js          optional enhancement (tabs, video)
internal/render/widget_<kind>.go                  init(): registerWidget(kind, prep)
internal/render/testdata/widgets/<kind>.*         doc, public and canvas goldens
```

`schema.json` is a JSON Schema for the props plus `x-hm` (`label`, `hint`, `group` = Layout, Content, Media,
Interactive or Data, `order`, `children`, `seed`, `default`); a property may carry `x-hm-widget` (`richtext`,
`media`, `textarea`, `lines`, `code`, `datalist:<source>`). The palette and the inspector read this through
`KindInfo` and `PropInfo`, so no JS table lists kinds. `internal/builder` stays a leaf (no HTML, no sanitiser); a
render test asserts both directories hold the same kind set. Array props are refused by the loader: repeating
structures are child nodes, flat data is text (`list.items` one item per line, `table.rows` tab-separated text).
Structural kinds are inserted with seeded children: the editor seeds on the client (`palette.js`), the no-JS outline
through `builder.NewTree`. Both nest by the kinds table `children` flag, not by kind name.

| Kind | Group | Props | Output |
| --- | --- | --- | --- |
| `columns` | Layout | `count` 2 to 6 | `div.hm-cols.hm-cols--N`; four or more columns drop to two at md, one at sm |
| `stat` | Content | `value label prefix suffix` | value and label spans |
| `richtext` | Content | `html` | sanitised rich text, every render and on save |
| `html` | Content | `html` | section-body sanitiser, every render and on save; may keep author `style=` |
| `list` | Content | `items` (lines), `ordered`, `icon` | `ul` or `ol`, optional inline icon |
| `table` | Content | `rows` (TSV), `header`, `caption` | `table` with `caption`, `th scope=col`; ragged rows padded, 200 rows by 20 columns at most |
| `quote` | Content | `text cite role` | `blockquote` and `figcaption` |
| `buttons` | Content | children `button` | flex container of buttons |
| `icon` | Media | `name` (closed set), `label` | inline `svg`, `aria-hidden` unless `label` is set |
| `image` | base | adds `decorative` | `alt=""` when decorative; `srcset` and `sizes` when media variants exist |
| `gallery` | Media | children `image` | `div` of images |
| `video` | Media | `url` (YouTube or Vimeo), `title`, `poster` | link-first facade, see Islands |
| `map` | Media | `query image href alt` | `a > img` with a same-origin static image; the link defaults to a map search for `query` |
| `accordion` | Interactive | `single` | container; `single` shares a `name` across its items |
| `accorditem` | Interactive | `title open` | native `details > summary` |
| `tabs` | Interactive | children `tabpanel` | stacked sections; enhanced by the island |
| `tabpanel` | Interactive | `label` | `section` |
| `form` | Data | `slug` | the theme contact form markup; nothing publicly for an unknown slug |
| `menu` | Data | `location`, `label` | `nav` with nested lists of the menu at that location |
| `breadcrumbs` | Data | none | `nav > ol` of the page trail; nothing when empty |

Per-kind CSS: the head holds `BaseCSS`, then the CSS of the kinds present on the page (registry order), then the
per-node rules. A page that uses none of the widgets is byte-identical to before, with one exception: a native `image` whose src has
media variants now gets `srcset` and a fixed `sizes` (`(max-width:1024px) 100vw, 1024px`).
Widget CSS must not out-specify a per-node `.hm-n-<id>` rule (one class): a selector whose subject is an element that
can be a child node uses `:where()` (`.hm-gallery > :where(img)`), or hide and width set on that node would lose.
A test enforces it for kinds with children. Disabled (`hidden`) nodes and their subtrees add no kind CSS or island. The canvas base carries the CSS of
every kind, so a widget added by a patch is styled. Budget: the `hm-builder-css` block of an all-widgets page is at
most 8 KiB gzip (`widgets_page_test.go`).

### StyleSpec additions

`rows` (`grid-template-rows`), `span` (`grid-column: span N`), `as` (`align-self`), `jc` gains `around` and `evenly`,
`ai` gains `baseline`. Counts are bounded; there is no free-form grid template.

`hide` is one value (`display:none`) and **cascades desktop first**: hidden at md is hidden at sm. A narrower
breakpoint can show the element again with `display` (see [Showing and hiding](#stylespec)). The inspector's Visibility
group offers "Hide on" for Desktop, Tablet and Phone and a "Display" value. The node prop `hidden` is
different: that node is not rendered at all ("Disabled (not rendered)").

### Islands and the CSP

`tabs` and `video` have an `island.js` (module, no globals, no `eval`). It is served from
`/theme/assets/builder/<kind>.<hash8>.js` (content hash, immutable cache) and a `<script type="module" src>` is added
to the plugin body slot only for kinds present on a public page, never in the canvas, never inline. The public CSP is
`script-src 'self'` for themes that opt in (`csp.strictScripts`, the default theme; see [THEMES.md](THEMES.md#strict-script-policy-cspstrictscripts))
and `script-src 'self' 'unsafe-inline'` for the others, so for plugin widgets the boundary is the owner's `builder.island` grant (see
[WIDGET_PACKS.md](WIDGET_PACKS.md)), not the CSP. The theme's own `hm.js` is unaffected: a page with no tabs and no video has no
builder script.

* **Tabs**: server output is every panel visible with its label as a `p.hm-tp__t` (not a heading, so the heading outline stays the
  author's), so it works without JS. The island
  builds the tablist (roving tabindex, arrows, Home, End, `aria-selected`, `aria-controls`) and hides inactive panels.
* **Video**: server output is a `div.hm-vid` with `data-embed` and a link to the watch URL (plus the optional
  same-origin poster). No third-party request is made at load; no remote thumbnail is ever used. A click replaces
  the link by an `iframe` on the allowlisted embed host (`youtube-nocookie.com`, `player.vimeo.com`). A URL that is not
  one of those renders a plain link.

### Accessibility lint

`internal/admin/a11y` has `CheckPublicWidgets(html)` (fragment mode): an image without `alt`, an unnamed link or
button, a table without `th`, `details` without `summary`, an inconsistent tab ARIA structure and a `style=` attribute
(except in the html widget) are errors; a skipped heading level is a warning. It runs over every widget golden in the
render tests; it is not a production dependency.

### Known limits

* The video poster and the map image only take root-relative paths (no third-party request before a click); with an
  absolute `MEDIA_BASE_URL` (a CDN) they are omitted, as the engine does not know the media base.
* Images have no width and height (the picker returns a URL only), so layout can shift while they load.
* `list` and `table` data are text props, less structured than a repeater.
* `hide` cannot un-hide at a narrower width.
* `menu` selects by location only; the `html` widget may carry author `style=`.
* A form widget replicates the theme's contact form markup; a theme override of the section is not honoured.

## Design system

A site-wide set of **design tokens** and **style presets**. Both are data: nothing in a page changes when they are
edited, and no page is re-saved.

**Storage.** One key `builder` in the site `theme_extra` JSON (the blob behind Theme > Colours and fonts), no migration:

```json
{"builder":{"tokens":{"color":{"accent":"#c8102e"},"space":{"4":"1.25rem"},"fs":{"lg":"1.25rem"},"shadow":{"md":"0 4px 12px rgba(0,0,0,.12)"}},
            "presets":{"card":{"label":"Card","s":{"base":{"pt":"space-4","bg":"surface","radius":"md","shadow":"sm"},"sm":{"pt":"space-2"}}}}}}
```

The Colours and fonts form and the theme PUT API keep every key they do not own, so `builder` survives them. Every
field is optional; a missing token is the built-in default. Per-theme package settings are not used, so presets
survive a theme switch.

**Tokens.** The names are fixed, the values editable (Theme > Design system, owner only):

| Class | Names | CSS variable | Validation |
| --- | --- | --- | --- |
| colour | `surface muted accent` | `--hm-surface`, `--hm-muted`, `--hm-accent` | hex colour |
| space | `0` to `12` | `--hm-space-N` | length (`rem em px % vh vw`) |
| type | `xs sm md lg xl 2xl 3xl 4xl` | `--hm-fs-<name>` | length |
| shadow | `sm md lg` | `--hm-shadow-<name>` | at most two layers of `x y blur [spread] #hex or rgba()` |

The style colours `primary bg text`, radii and fonts stay in Colours and fonts. A new style key `shadow` takes
`none sm md lg`. Values are checked on save and again at render; an invalid stored value is dropped and never
emitted.

**Opt-in.** A theme turns tokens on with `"builder": {"tokens": true}` in `theme.json`. Only then the page emits one
`:root{--hm-...}` block (only the tokens that differ from the defaults, sorted) at the start of `hm-builder-css`, and
the `fs` style compiles to `var(--hm-fs-<name>,<literal>)` instead of the literal. A theme without it renders
byte-identically to before. Space, colour and radius styles already read their variables with a fallback.

**Presets.** A preset is a named style map (the same keys and breakpoints as `s`). A native node references it with
the prop `preset` (`^[a-z][a-z0-9-]{0,31}$`, accepted on every kind); the element gets the class `hm-p-<name>`.
A page emits the rules of the presets it uses once each (after the kind CSS, before node rules); the canvas base
carries all presets, like the kind CSS. `component` nodes ignore presets. Up to 48 presets; a preset needs at least
one valid key. Presets are edited in Theme > Style presets (owner). Editors can create a preset from
a node ("Save as preset" in the Style tab, `POST <admin>/builder/presets`; 409 if the name exists) but not change or
delete one.

**Cascade.** A node that references a preset compiles its own rules with the selector `.hm-n-<id>.hm-n-<id>`
(two classes), so a value set on the node beats the preset's value at every breakpoint. A node that names a preset
that does not exist renders a dead class and no preset rules.

**Cache.** Every write (tokens page, presets page, create endpoint) purges the rendered-page cache, so the next
request shows the change. The canvas is uncached and reads the same data. Browsers and any CDN in front keep a public page for its `Cache-Control` lifetime (60 s in the browser, 300 s shared by default), so a visitor may see the old look for that long.

**Editor.** The Style tab has a Preset panel: apply, the list of values that differ from the preset ("Differs from
preset") with Reset per value and Reset to preset, Detach to custom (copies the preset's values onto the node, drops
the reference), and Save as preset. Each action is one undo step. With tokens on, values that are not a token are
marked "Custom value". The Navigator shows "preset card", "preset card, differs" or "preset removed" after the
row text. **Copy style** and **Paste style** (buttons, `Ctrl/Cmd+Alt+C` and `Ctrl/Cmd+Alt+V`) copy a node's whole
style map and preset in memory; paste replaces the target's styles and preset in one undo step. The client derives
all of this from the doc and the boot data (`boot.design`); it never writes CSS.

**Limits.** No live preview on the settings pages (the editor canvas shows saved changes); the blob is written whole
(last write wins between two admins); presets do not nest; components ignore presets; deleting a preset shows no
usage count (pages that use it lose its styles); a theme must opt in to see token effects.

## Reuse

Three ways to use one piece of layout more than once. None needs a migration.

**Templates.** "Save selection as template" and "Save page as template" (palette footer) store a copy of the nodes in
the section-template store (`hatamoto_section_templates`, `section_type = "hm-tree"`, `content = {"v":1,"nodes":[...]}`).
The content is validated like a doc (kinds, props, depth, 1000 nodes) and is at most 512 KB. The palette group
**Templates** lists them next to the older section templates; inserting fetches the nodes with **fresh ids** on every
insert (one undo step; fetching does not change any counter). An older section template inserts as a `component` node, and is refused with a message when
the active theme lacks its section type. A template is a copy: later
edits to it or to the page do not link them.

**Blocks** (shared, synced across pages). A block is an entity of the built-in type `block` whose `attributes.page_doc` is a doc (the schema
rows `block` and `theme_part` are inserted at boot by `entity.EnsureReuseSchemas`, data only, idempotent; if a site
type already owns either slug, reuse is switched off for that type and logged once: the rules below apply only to
the built-in rows, so the site's own type publishes, edits and schedules as before and a `ref` never resolves to it). Blocks are listed, created and deleted under Theme > Blocks (`<admin>/theme/blocks`), not in Entities (the built-in `block` and `theme_part` types are hidden from the Entities list, New menu and pickers; their `<admin>/entities/{id}/edit` URLs still open the builder). A page uses a block with a `ref` node
(`{"t":"ref","p":{"block":"<uuid>"}}`). The renderer expands each `ref` into a wrapper `div` (id and styles of the ref
node, class `hm-n-<ref id>`) holding a copy of the block's nodes; their ids are rewritten with the prefix
`b<hash8 of the block id>_` so two uses, or a page and a block with equal ids, never collide. A page with a ref is builder-only (it has no derived `page_sections`).

Editing: **Save as block…** (Add tab foot, element menu or the `Ctrl+K` palette; a small name dialog) turns the selected node into a new block and puts a `ref` in its
place in one undo step. Palette group **Blocks** inserts a ref. The ref's inspector shows the block name,
**Edit block** (opens the block's builder in a new tab, so unsaved page work is never lost), **Detach** and
**Used on N pages**. A block builder shows **Save block**: there is no autosave and no Publish; a save is live for
every page that uses the block (the rendered-page cache is purged), a canvas that is already open shows the old block
until it is reloaded. A block is **never published**: every write path (create, update, import, restore, status,
schedule) forces `draft`, so a block has no public URL and is not in the sitemap, feeds or search. Two editors saving
one block get the usual version conflict.

**Safety.** At most 3 blocks nested (`RefMaxDepth`) and 1000 expanded nodes per page (`RefMaxNodes`). A ref to a block
that is already being expanded (itself, A to B to A) is a cycle. Public output for cycle, depth, limit or a missing
or non-block target is only a comment, `<!-- hm-ref: cycle|depth|limit|missing -->`; the rest of the page renders. The
canvas shows a "Missing block" box instead. Saving a block that would make a cycle is refused (`422 ref_cycle`, the
message names the path); the render guard also covers writes through the API. A block may not contain a `slot` and a
page may not either.

**Usage and detach.** `GET <admin>/builder/blocks/{id}/usage` lists the pages, blocks and parts whose doc references the
block (a text scan of `attributes` verified by parsing the doc; capped at 200, shown as "200+"; it never runs when a
page is rendered). The block's builder shell shows the same list as "Used on N pages". Deleting a block is allowed;
its refs then degrade to the missing comment. **Detach** fetches the block's nodes with fresh ids and replaces the ref
by them in one undo step; the visible output is unchanged.

**Theme parts.** See [THEMES.md](THEMES.md#layout-parts-builderparts). Parts are entities of type `theme_part`
(slot `header`, `footer`, `single` or `archive`), also always draft, built in the same builder (**Save part**).
`single` and `archive` parts must hold exactly one `slot` node; `header` and `footer` none (`422 part_slot`). Theme >
Layout parts (owner only) assigns them. Assigning is owner-only because it restyles the whole site, but an
editor can still open and save a part that is already assigned (editors edit any content), which has the same site-wide
effect.

**Cache.** Any block or part save is an entity event, which purges every rendered page; assigning parts in Settings
purges too. Browsers and CDNs keep a public page for its `Cache-Control` lifetime.

**Limits.** Saves are explicit; the whole cache is purged on a block save (not only the pages that use it); parts are
per site, not per schema (no per-type single part, no page or home part, no conditions); a part is previewed inside
the theme's own header and footer, not in place; the usage scan is a sequential scan capped at 200; blocks have no
versions UI of their own beyond the entity's. **Rollback:** a page that uses `ref` is builder-only and an older binary
cannot read the kind, so use refs only after the new binary is live; Detach turns a page back into plain nodes. The
boot-time schema rows are harmless to an older binary, but an older binary does not lock blocks and parts to draft:
one published there is never served by this release (public reads skip the built-in types) and is put back to draft at
the next boot.

## Dynamic data

Two features show live site data without code. Neither changes the schema: bindings and loops live in
`attributes.page_doc`.

**Bindings.** A node's `b` maps a prop key to a source: `{"id":"t1","t":"heading","b":{"text":"loop.item.title"}}`.
While `b[key]` exists `p[key]` is ignored (the editor drops the literal when it binds; Unbind leaves the prop unset).
The value is resolved when the page renders (public and canvas) and then goes through the same escaping, URL check
and `srcset` code as a literal, so a bound `javascript:` link renders an empty `href` and a bound image gets its
variants. A missing value renders empty; the canvas shows `‹source›` instead. The `setBind` op
(`{"op":"setBind","id","key","value":"<source>"|null}`) sets and clears it.

Only props marked bindable (`x-hm-bind` in the kind's schema; `KindInfo.Props[].Bind`) take a source:

| Class | Props |
| --- | --- |
| text | `heading.text`, `text.text`, `button.text`, `image.alt`, `quote.text/cite/role`, `stat.value/label`, `table.caption`, `icon.label`, `video.title`, `map.query`, `tabpanel.label`, `accorditem.title` |
| url | `button.href` |
| image | `image.src` |

Never bindable: rich text, html, code and lines props, enums, numbers, booleans, `container`, `component`, `ref`.
A text prop takes any source; a url prop takes url sources and attribute sources; an image prop takes image sources
and attribute sources. Anything else is `422 invalid_page_doc` at save. Page bodies (`page_sections`, `page_doc`,
`content`) are never readable as a source.

| Source | Class | Value |
| --- | --- | --- |
| `entity.name`, `.title`, `.excerpt`, `.published_at`, `.updated_at`, `.author.name` | text | the page or entity shown (dates as `2 January 2006`) |
| `entity.image` | image | its featured image |
| `entity.url`, `entity.author.url` | url | |
| `entity.attributes.<path>` | text | an attribute; `<path>` is 1 to 4 segments of `[A-Za-z0-9_-]{1,64}` |
| `site.name`, `.tagline`, `.phone`, `.hours`, `.address.street`, `.city`, `.state`, `.postal_code`, `.country` | text | site settings |
| `site.logo` | image | |
| `site.settings.<key>` | text | a site setting |
| `term.name`, `.slug`, `.description`, `.kind` / `term.url` | text / url | only where a term view exists |
| `loop.item.title`, `.name`, `.excerpt`, `.category`, `.author`, `.published_at`, `.reading_time` | text | the current loop item |
| `loop.item.image` / `.url`, `.author_url` | image / url | |
| `loop.item.attributes.<path>` | text | an attribute of the item |
| `ext.<plugin>.<name>` | the source's class | a value source of a plugin (see [Plugin extensions](#plugin-extensions)); an unregistered id is accepted and renders empty |

`loop.item.*` is valid only inside a `loop` template (`422` elsewhere). In the canvas, `entity.*` reads the draft's
own attributes and name; `entity.url` and the author are empty there.

**Query loop.** A `loop` lists published entities and renders its children once per item (the item template is
every direct child except a `loopempty`). Markup: `<div class="hm-n hm-loop hm-n-<id>">` holding one
`<div class="hm-loop__item">` per item; the loop's own style (`cols`, `gap`) lays them out.

| Prop | Values | Default |
| --- | --- | --- |
| `from` | `query`, `archive` (the items and pager of the archive being shown, no extra query), `source` (items of a plugin data source, see [Plugin extensions](#plugin-extensions)) | `query` |
| `source` | `ext.<plugin>.<name>`, an items source; required when `from` is `source` (the loop's `limit` applies, no paging) | none |
| `schema` | schema slug | none |
| `context` | boolean: use the page's own schema | off |
| `term` | `<kind>:<slug>` | none |
| `author` | person slug | none |
| `order` | `newest`, `oldest`, `updated`, `name`, `name_desc` | `newest` |
| `limit` | 1 to 48 | 12 |
| `offset` | 0 to 1000 | 0 |
| `paging` | `none`, `pages` | `none` |
| `exclude_current` | boolean | off |

Data rules: only published entities are listed; the built-in `block` and `theme_part` rows, hidden people and
scheduled or draft rows never are. A failed or empty query renders the `loopempty` children, if any (a failed query
also keeps the page out of the cache). A loop inside a loop is refused at save; a `ref` to a block that holds a loop,
placed inside a loop template, renders `<!-- hm-loop: nested -->`. Inside a block, bindings resolve against the
host page.

**Loop budget.** A render runs at most 8 loops, counting those in header, footer and archive parts and in
referenced blocks. Later loops print `<!-- hm-loop: limit -->` publicly and a "Loop limit reached" notice in the
editor canvas. Bound values show whatever the source holds, so a `site.settings.*` binding prints that theme setting
to the public page.

**Paging.** With `paging: "pages"` the loop shows a `nav.hm-pager[aria-label="Pagination"]` and `?page=N` selects
the window. One paged loop per page owns the parameter (the first in document order, parts before the page); later
ones render page 1 without a pager. On archive and listing routes the route owns `?page=`, so a paged loop in a
part shows page 1 there (use `from: "archive"`). The pager stops at page 1000. A failed query serves the degraded,
uncached page, not a 404. `?page=N` past the last page is a quiet 404; `?page=abc` or `0` is page 1.
The head of page N has a self canonical (`?page=N` for N of 2 or more), `rel=prev` and `rel=next`. See
[URL_PATTERNS.md](URL_PATTERNS.md#paged-builder-loops).

**Archive in the builder.** `slot.replace` (archive part only, else `422 part_slot`) drops the theme's own listing:
the part's markup before and after the slot is all the archive prints. With a `loop` of `from: "archive"` the blog
archive is built entirely from nodes. Taxonomy and author layouts are not wrapped by parts, so a category archive
cannot be built this way yet; a page entity with a `query` loop covers region-style pages.

**Limits.** No expression language, no nested loops, no bound rich text or component content, no filters on
attributes and no "menu order" (entities have no such column), no scheduled items, no search-result loops. The
canvas shows at most 3 items; only the first carries selection markers. A doc with `b` or `loop` is
builder-only, so it has no derived `page_sections`. **Rollback:** an older binary cannot parse `b`, `loop` or
`loopempty`; use them only after the new binary is live.

## Plugin extensions

A plugin package can add widgets and data without a core change; the authoring guide is
[WIDGET_PACKS.md](WIDGET_PACKS.md).

**Registry.** The builder keeps its node kinds and compiled prop schemas in one immutable snapshot that is swapped
atomically (`SetPackKinds`), so a call that reads it never sees a mix. Core kinds are always present and a plugin
kind is never allowed to replace one: a plugin widget `<name>` of plugin `<plugin>` is the kind `<plugin>_<name>`
(exactly one underscore; no core kind has one). The packages manager rebuilds the snapshot after every install,
enable, disable, uninstall and reconcile tick, so widgets appear and disappear without a restart. Pack kinds list in
the palette under **Plugin widgets**, after the core groups; their props, defaults and `x-hm-bind` classes are data
for the inspector and the outline form like any widget's.

**Orphans.** A saved node whose pack kind is not registered (package removed or disabled) is accepted by `Validate`
with any JSON-object props up to 8 KiB, children, and bindings checked for syntax only. It can be saved, moved, hidden
and deleted; a prop edit is refused with "widget not installed". The public page prints
`<!-- hm-widget: <kind> not installed -->`, the canvas a marked box, the outline a notice. A name that is not
pack-shaped is still `unknown kind`.

**Data sources.** `ext.<plugin>.<name>` is a binding source for a `value` source and the loop `source` for an `items`
source, both declared in a plugin manifest (`builder_sources`) and registered only while the owner has granted the
plugin `builder.data`. The renderer collects every `ext.*` binding and source loop of the page (blocks expanded) and
asks the plugins in one batched call; theme parts add at most one more call, so at most two calls per render, 16
distinct requests, 500 ms in total. Values go through the same escaping and URL checks as literals. A failure leaves
the props empty, shows the loop's empty state and keeps the page out of the page cache; a success is cached with the
page. The editor offers value sources in **Bind** (grouped by plugin) and item sources in the loop's **Source** field.

## Rendering

- A **v1-shaped** top-level node (an unstyled `section` container with one `component`, `v1: true`) renders through
  the theme `section` partial, so its HTML is byte-identical to the v1 section it came from.
- Every **other node** renders natively. `component` nodes inside it still use the theme's section templates, without
  the `<section>` wrapper.
- A failing native node leaves `<!-- section render failed ... -->`, the rest of the page is served, and the page is
  not cached (same rule as a failing v1 section).
- Public HTML has no `data-hm-node` marker. The canvas (editor preview) adds them.
- A doc that v1 can express is handed to the server as sections, so forms, entity links and the `render.page` plugin
  filter behave as before. A builder-only doc is rendered from the doc: forms and entity links are found in its
  components, and the `render.page` plugin filter is **skipped** (its contract is sections).

## Canvas

On a page whose doc the v1 form can carry, the editor canvas speaks protocol v2: any node can be hovered and selected,
and structural edits patch only the affected section in the live frame (scroll, open panels and selection survive).
The server renders the fragments (`Engine.RenderTop`, the same output as a full canvas render) and applies draft ops
in memory; nothing is stored. Pages without a doc keep the full reload. A page still drawn from v1 sections has no builder base sheet or widget CSS, so the first native node added to it re-renders the frame whole; after that it patches. In the editor, an empty node is floored at 48 px with a dashed outline so a fresh layout can be selected and dropped into. See [CANVAS.md](CANVAS.md).

## Editor

`<admin>/entities/{id}/edit` picks its screen from the stored `page_doc`:

| Page | Screen |
| --- | --- |
| page-editor entity with a parseable `page_doc` (v1-expressible or builder-only) | the builder |
| no `page_doc`, but `page_sections` | the builder, on the **in-memory** conversion (`entity.LegacyPageDoc`); opening stores nothing and adds no version, the first save stores the doc |
| a `page_doc` that does not parse | a recovery screen: links to Versions (restore an earlier version) and to the entity details form |

The v1 section form editor no longer exists; there is no classic screen. A new page draft is created with
`{"page_doc":{"v":2,"nodes":[]}}`. Posts keep the blog editor. Pages get a stored doc on their first builder save, on
any API/MCP/import write ([Write rules](#write-rules)) or in bulk through `hatamoto builder convert`.

### Panes

The shell is `web/admin/pages/entities/builder.html` (server-rendered, so the no-JS outline still works) driven by `screen.js`.

* **Top bar**: back link, page name, labelled **Navigator** and **Inspector** toggles (`Ctrl/Cmd+Alt+[` and `]`),
  Shortcuts, Undo, Redo, History, the device switch, one status chip (`role=status`: "Draft · Saved 14:02", "Saving…",
  "Not saved", "Changed somewhere else"), Save draft and Publish (draft) or Update page (published), and a **More**
  menu: Versions, Page settings, Site styles (owners; `boot.canStyle`), Keyboard shortcuts, a separator and Delete page
  (Delete block or Delete part on those screens; always behind `data-hm-confirm`). On a phone Save draft, Undo, Redo
  and History also appear in More. The slug rule is the blog editor's; the slug and the address fields stay on the details page.
* **SEO drawer** (pages only; blocks and parts have no SEO button): the **SEO** button in the top bar (also in More on a phone)
  opens a slide-over on the right of the canvas (`seodrawer.js`, a lazy module that is not part of first paint). The first
  open fetches `GET <admin>/entities/{id}/builder/seo` (a bare fragment for a request with `HX-Request` and `HX-Target`) and
  loads `@hm/seopanel`; later opens reuse it. Escape or Close hides it and returns focus to the SEO button. The live checks
  post the draft doc with the panel's fields to `POST <admin>/entities/{id}/builder/analyze`, which renders the draft
  through the preview and analyses its `<main>` (the `<body>` when the theme has none); nothing is written. Editing a field
  marks the page changed, and from then on every save carries a `seo` object; before the first edit no save sends one, so a
  value changed elsewhere is never overwritten. SEO edits are not part of the recovery copy (doc and name only).
* **Left pane** has two WAI-ARIA tabs with roving arrows, **Navigator** and **Add**, and a permanent footer with
  **Add element** (the `/` action) and **Ctrl K** (the command palette).
  * **Navigator**: an ARIA tree (`role=tree`, `treeitem`, `aria-level`, `aria-expanded`, `aria-selected`, one roving
    tab stop). Rows can be dragged. A toolbar has a Filter box, **Collapse all** and a visible **Rename** button (F2 and
    double-click still rename). Row names come from one map (`kindnames.js`): "Rich text", "Accordion item",
    "Tab panel", "Block: Promo"; the separator is " · ".
  * **Add** (lazy `addtab.js`, mounted on first open): a search field (focused), Recent (last inserts per user,
    `recent.js`), Patterns (one short row of four with "Show all patterns (N)", drawn thumbnails from `patterns.js`),
    then Layout, Elements, Content, Media, Interactive, Data and Blocks. Search ranks by name first (`search.js`). It is
    inline, not a modal, so tiles can be dragged. Enter inserts the first tile, Shift+Enter inserts and keeps the
    search open. The foot has Save as block… and Save as template… (`reuse.js`, with the name dialog `savedialog.js`).
  * Insert position (`insertPoint`): the "+" position through `insertAt`, else after the selection (inside a
    selected container), else the end of the page.
* **Command palette** (lazy `commandk.js`): `Ctrl/Cmd+K` anywhere except a canvas text edit, where it stays
  "insert link" (`resolveKey` returns `null` when `ctx.canvasText`; the key from inside the canvas frame is not
  forwarded by the bridge yet, because `canvas_bridge.js` is untouched in this change). A combobox over a listbox with
  `aria-activedescendant`: Recent, Patterns, Elements, Blocks and Commands (Undo, Redo, Desktop, Tablet, Phone, Show or
  hide Navigator, Show or hide Inspector, History, Keyboard shortcuts, Save as block…, Save as template…, Site styles
  for owners, Preview when the page has an address).
* **Canvas** (middle): the sandboxed frame (`allow-scripts`, no `allow-same-origin`) with a parent-side layer for
  hover and selection boxes and the drop indicator. The device switch reads Desktop, Tablet ≤1024 and Phone ≤640
  (`BREAKPOINT_MAX` in `bp.js` mirrors Go `breakpointMaxWidth`, a test keeps them equal), sizes the frame and sets the
  breakpoint the Style tab edits (`base`, `md`, `sm`). A device chip "Editing Tablet" and a polite live region
  ("Editing Tablet styles") say which width the edits go to; `data-hm-device` is on `.hm-bs`. The shared
  `hm/segmented` control is unchanged.
* **Start panel** (lazy `startpanel.js`): on a page with no nodes, tiles for hero, features, call to action and FAQ
  first, then the theme's other sections, and "Start blank". It is removed on the first insert by any path.
* **Coachmarks** (lazy `coachmarks.js`): two notes, "Click to select. Double-click text to type." and "Press / or
  use Add to insert anything.", each shown until dismissed or the first insert, kept per user in `localStorage`
  (`hm-builder-hints:<user>`); they do not take focus and honour reduced motion.
* **Inspector** (right): tabs Content, Style and Advanced. Content shows the props of a native node as plain inputs, or
  the server-rendered form of a component. Style shows one control per StyleSpec key for the active breakpoint; a
  value inherited from a wider breakpoint is shown greyed. Advanced shows the id (read-only), `hidden` and, for
  containers, `tag`.
* **Banners** (lazy `banners.js`): the conflict and recovery banners float over the top of the canvas, so nothing
  below them moves. Conflict copy: "Someone else saved this page while you were editing. Your changes are still here
  and are not saved yet." with Keep my version (saves again at the current version), Load their version (reload) and
  Compare in Versions (opens the versions page in a new tab).
* **History labels** (lazy `historylabel.js`, derived from the ops and the document before the commit): "Add Heading",
  "Delete Container", "Move Button", "Change Text colour (Tablet)", "Paste 3 elements"; the first row is "Original".
  A caller's own label wins over a bare verb.
* **Site styles** (lazy `sitestyles.js`, owners only): a right-hand `<dialog>` that edits the Theme colours (Brand,
  Secondary, Background, Text) and corners (`POST .../theme/colours`) and, when the theme uses builder colours,
  Accent, Surface and Muted (`POST .../theme/design-system`), both in fetch mode (`X-Hm-Fetch: 1`, 204 or 422 JSON, so
  the page does not reload and validation is unchanged). Only changed fields are posted. The default theme renders
  only the builder `tokens.color.accent`; Theme accent and secondary are relabelled in the drawer and are not read by
  the default theme (making them live is a render change, a follow-up). The colour names are one map
  (`colournames.js`, mirrored by Go `builderColourNames`).
* **Phone** (≤959.98px, `phone.js`, `panes.js`): the panes stack and a bottom dock Navigator / Add / Inspector
  chooses one; the canvas is the base. Selecting an element opens the Inspector sheet. The device switch and Undo stay
  in the top bar at 640px and below.

### Editing

Every edit is a command on an immutable client store with a log of inverse ops, so undo and redo cover insert, move,
remove, duplicate, props, content and style. Consecutive edits of one prop within a second merge into one undo step.
The canvas follows each command through the render endpoint (CANVAS.md); undo and redo replay the same way and do not
reload. A move that would nest a node in itself, exceed depth 8 or 1000 nodes, or put children on a leaf is refused
before it is committed. The server validates again.

Component content is never read by JavaScript: the inspector posts the node's fields to `builder/component` and
gets back `content`, `errors` and, when the form must change, new `html`.

### Saving

`POST <admin>/entities/{id}/builder/save` takes `{version, doc, op, name, seo?}`; `op` is `autosave`, `save` or `publish`.
`seo` is `{title, description, canonical, og_image, no_index}`, honoured for pages only and written into the page's
attributes (`meta_title`, `meta_description`, `canonical_url`, `og_image`, `no_index`; an empty value removes the key)
by the same single update as the doc, so one version is created and a stale `version` still writes nothing. A missing
`seo` leaves the stored values alone; a value the public head could not use is `422 {code:"invalid"}`.

| Case | Answer |
| --- | --- |
| ok | `200 {ok, version, savedAt, status}`; `doc` is added when the server stored a different doc (its sanitiser changed content), and the client adopts it and reloads the canvas |
| stale `version` (or none) | `409 {conflict:true, version:<current>, error}`; nothing is written |
| invalid doc | `422 {code:"invalid_page_doc", error}`; nothing is written |
| `autosave` on a published page | `409 {code:"not_draft", error}` |
| `publish` of an untitled page, or a draft that cannot be published | refused (`422 untitled`; or saved with a `note`) |
| missing CSRF / no session | 403 / the session middleware |

Other fields: `note` carries something to tell the person although the save worked (a slug in use, a draft that was
saved but not published).

* **Drafts** autosave a few seconds after a change. The version is written with `entity.AutosaveSummary`, so autosaves
  coalesce and expire like blog autosaves (the version list reads "Autosaved in the blog editor"). Save draft and
  Publish are explicit saves with the summary "Updated in the builder".
* **Published pages** never autosave. A recovery copy is kept on the server, one per user and page, and the Update page button
  saves. The builder sends the copy about 1.5 seconds after a change to `POST /entities/{id}/builder/recovery`. The next
  time that user opens the page, on any device, the boot JSON carries it as `recovery` and the person is offered it. The
  copy survives sign-out. It is deleted by that user's own Save or Update, by Discard
  (`.../builder/recovery/discard`), after 30 days, and with the page or the user. Copies in the browser that older
  versions left behind are removed when the admin loads and are never restored.
* **Conflict** (somebody saved a newer version): the status chip shows "Changed somewhere else" and a banner offers
  Keep my version (saves again at the current version), Load their version (also clears the recovery copy) and Compare in Versions.
* A save that fails keeps the server copy (it is only deleted after a successful save), and a draft retries.
* The save sends `page_doc` only; the stored `page_sections` is not touched.

### Saving from an AI agent (MCP)

An MCP agent edits a page with the same ops the editor sends: `builder_get` returns `{version, doc}`,
`builder_apply_ops` applies the ops and stores one version, and a stale `version` is refused with
`conflict: ... current version is N` (the MCP form of the `409` above). The agent's save runs the same checks as the
editor's save: the doc and element-setting rules, the slot and block-reference rules (`part_slot`, `ref_cycle`) and the
sanitiser. The tools, the op list and an example session are in [MCP.md](MCP.md#builder-tools).

### Without JavaScript

The server also renders the page as one nested form (the outline), inside `<noscript>`. `?outline=1` shows it as the
page itself. Each node is a fieldset with its props as inputs (names `n.<id>.p.<key>`) and, for a component, the
schema form (prefix `n.<id>.c.`). The form carries the working doc in a hidden `doc` field and the `version`.

| `_op` | Effect |
| --- | --- |
| `up.<id>`, `down.<id>` | move among siblings |
| `after.<id>`, `into.<id>` | insert the kind chosen in `kind.<id>` (a native kind or `component:<type>`) after the node, or as the last child of a container |
| `add` | insert the kind in `add_kind` at the end |
| `del.<id>` | remove |
| `save`, `publish` | store (stale version is a 409 that shows the form again) |

Every operation but save and publish shows the form again with 200 and stores nothing. Re-parenting needs the
JavaScript editor.

### One decoder

Component content is decoded by one function, `decodeComponent` (`internal/admin/builder_component.go`): the outline
post and the inspector endpoint both use it. Native props are coerced by `builder.CoerceProp` and applied as `setProp`
ops through `Doc.Apply`, the validator the JavaScript path reaches. The same edit made either way gives the same doc.
A bad doc is answered 422 `invalid_page_doc`; these paths never send `page_sections`.

### Style controls

The Style tab's controls are built from the StyleSpec entry, never from a second table: the units a length accepts are probed by testing the entry's `pattern` (`controls.js`, pure, node-tested), so a key's unit menu, slider range and step follow the server's validation. Each row shows a set-here dot, a source label (Local, Preset, Inherited) and a reset button; Spacing is a linked box model (`boxmodel.js`); groups are disclosures. The colour palette (`boot.design.palette`: colours, space steps and font sizes) is resolved on the server from the site settings and the design tokens, so the swatch text shows the colour the theme paints; the preview uses the token (`var(--hm-…)`) wherever the table gives one. The colour popover reuses `.hm-popover-panel` and the Popover API: Tab wraps inside it, Escape closes it and returns focus to the swatch.

**Drag coalescing.** A slider or colour drag commits on every `input` with one explicit session key (`commit(..., {coalesce: "<session>"})`), so the whole drag is one undo step even with a pause in the middle. `coalesce: true` is unchanged and still merges only a lone `setProp`, so each arrow-key press on a number field is its own step. This changes no document or rollback behaviour.

**First paint and lazy chunks.** The new controls (`controls`, `controlsui`, `boxmodel`, `presetsrc`) are imported by the lazy `style` chunk. To make room, the reuse UI, shortcut sheet, component fields, preset panel and preset op builders became lazy too, with small first-paint cores left behind (`reusecore`, `keymap`, `design`). See [ADMIN.md](ADMIN.md#builder-editor-b3).

### Copy, paste and the paste endpoint

Copy and Cut put the selected elements (the topmost of a multi-selection, in document order) on the **system
clipboard** as one JSON envelope, `{"hm":"nodes","v":2,"nodes":[...],"site":"<origin>"}`, and keep a copy in memory
for when the browser denies clipboard access. Paste reads the clipboard text, and sends the envelope to the server,
which is the only place that validates it, so a paste from another page, another site or a hand-written text is as
safe as a save.

`POST <admin>/entities/{id}/builder/paste` (owner or editor, CSRF header, same gate as the other builder endpoints;
body at most the tree template limit):

| Case | Response |
| --- | --- |
| Valid envelope | 200 `{"nodes": [...], "missingBlocks": ["<id>", ...]}`: the nodes sanitised like a save, every id fresh; `missingBlocks` lists `ref` nodes whose block does not exist on this site (they stay, and render as the missing-block placeholder) |
| Not JSON, or a body over the limit | 400 `bad_request`; 413 `too_large` |
| `hm` is not `nodes`, `v` is not 2, or no nodes | 422 `not_nodes` |
| The nodes do not form a valid doc (unknown kind or prop, depth, bad ids, a forbidden style key) | 422 `invalid_nodes`, the message names the problem; nothing is dropped silently |
| The nodes hold a `slot` (the Content slot of a layout part) | 422 `part_slot` |

The endpoint writes nothing. The client inserts the returned nodes with a normal `insert` batch (**Paste after** the
selection, **Paste inside** a container), so a paste is one undo step, and the context checks that need the target
(depth after insertion, `loop.item` binds outside a loop, kinds that only fit one parent) are enforced by the store
ops and again by the save validation.

### Shortcuts

Shortcut keys act on the page, never inside a text control (those keep their own undo, copy and paste, and the rename input); `Ctrl/Cmd+S` and `Ctrl/Cmd+K` work everywhere (`commandk` is resolved before the text early return; `screen.js` sends it to the link dialog in a canvas text edit and to the palette otherwise).
`?` opens the same list as a grouped, searchable dialog.

| Keys | Action |
| --- | --- |
| Up / Down | previous / next element in the Navigator |
| Left / Right | collapse or go to the parent / expand or go to the first child |
| Home / End | first / last element |
| Alt+Up / Alt+Down | move among siblings |
| Alt+Left / Alt+Right | outdent (after the parent) / indent (into the previous container) |
| Delete | remove |
| Ctrl/Cmd+D | duplicate |
| Ctrl/Cmd+C / Ctrl/Cmd+X / Ctrl/Cmd+V | copy, cut, paste after the selection (the system clipboard, so it also pastes on another page) |
| Ctrl/Cmd+G / Ctrl/Cmd+Shift+G | wrap the selection in a container / unwrap a container |
| Ctrl/Cmd+A | select the siblings of the selected element |
| Ctrl/Cmd+click, Shift+click | add or remove a row or canvas element from the selection / select the range among siblings |
| F2 | rename the selected row in the Navigator (Enter saves, Esc cancels) |
| Shift+F10, the menu key | the element actions menu at the row |
| Ctrl/Cmd+H | history panel |
| Ctrl/Cmd+Alt+C / Ctrl/Cmd+Alt+V | copy the element's style / paste it onto the selected element |
| Ctrl/Cmd+Z | undo |
| Ctrl/Cmd+Shift+Z, Ctrl+Y | redo |
| Ctrl/Cmd+S | save |
| Enter | on the page or canvas: edit the selected element's own text (a text or heading); in the Navigator: open its settings in the inspector |
| Ctrl/Cmd+K | command palette, also from the canvas frame (in a canvas text edit: insert link) |
| Ctrl/Cmd+Alt+[ / Ctrl/Cmd+Alt+] | show or hide the Navigator / the Inspector |
| `/`, `A` | open the Add tab with its search focused (Shift+Enter in the search inserts and keeps it open) |
| `?` | shortcut sheet |
| Escape | cancel a drag |

From inside the canvas frame only Ctrl/Cmd+Z, Shift+Z, Y, D, C, X, V, G, Shift+G, K and Delete reach the shell (Enter on a selected text starts the edit inside the bridge), through the bridge's `key` message (with the page's own text selected, C and X stay the browser's). F2, Shift+F10, Ctrl/Cmd+A and Ctrl/Cmd+H need focus outside the frame; a right-click in the frame opens the menu. The other keys work once focus is outside the frame.

### Direct manipulation (builder-e4)

* **Menu.** Right-click on the canvas or a Navigator row, Shift+F10 or the menu key opens one `role=menu` popover at the
  point: Copy, Cut, Paste after, Paste inside, Duplicate, Rename, Wrap in container, Unwrap, Copy style, Paste style,
  Save as template… and Save as block… (when the site has reuse), and Delete. Rows that cannot apply are disabled.
  An element outside the selection becomes the selection first. Esc closes it and focus returns to the tree.
* **Multi-select.** Ctrl/Cmd+click toggles and Shift+click selects the range among siblings, in the tree and on the
  canvas. Delete, Duplicate, Hide, Wrap and Copy act on all selected elements in one undo step; the inspector shows
  "N elements selected" and edits nothing. Dragging, rename, style paste and Paste inside need exactly one element. The
  tree is `aria-multiselectable`.
* **Rename.** F2 or a double-click turns a tree row into an input; the name is the `navlabel` prop (one `Rename` undo
  step, empty clears it). The caret before a row folds it.
* **"+" picker.** The round "+" buttons between elements and inside empty containers (drawn by the canvas host's lazy
  `canvas/chrome.js` from `canvas/points.js`; only for the selected and the hovered container, in gaps and never over text,
  none within 26 screen px of another, hidden while text is edited) open a popover of layouts: 1, 2, 3 and 4 columns
  (the `columns` widget), 33/67 and 67/33 and a 2×2 grid (a `container` in grid layout with a column template), and
  **Choose an element…** and **Insert a ready-made section…** for the Add tab (at the same position). Each layout is one undo step ("Add section: 3 columns" at the page level, "Add 3 columns" nested).
  The buttons are pointer only: the keyboard path is the toolbar's **Add after** (the same picker after the selection) or the Add tab.
* **Move handle.** The selected element's toolbar carries a handle; dragging it reorders with the same drop targets as the
  tree. A press before the lazy `drag` chunk has arrived starts the drag as soon as it does if the pointer is still
  down. A v1 section's handle moves the whole section (its top-level container), not the component inside it.
* **History.** Ctrl/Cmd+H or the clock button lists every change of the session with its label and age; picking a row
  jumps there in one step (`store.jump`), undoable like any change.

### Inline rich text (builder-e5)

Double-click a heading or text on the canvas to edit it in place; a formatting bar appears above it.

* **Marks.** Bold, italic, underline, strike, code, link and line break. Shortcuts: Ctrl/Cmd+B, I, U; Ctrl/Cmd+K opens
  the link dialog (address and **Open in new tab**; only `http`, `https`, `mailto`, `tel`, `sms` and relative
  addresses). Enter ends the edit, Shift+Enter inserts a line break in a text (a heading is one line), Esc puts the
  text back as it was. Paste keeps only these marks; drops are blocked.
* **Storage.** Text without marks is stored in `text`, as before. Text with marks is stored in `html` and `text`
  keeps the plain projection, so search and the outline still work. One edit session is one undo step.
* **Sanitiser.** `html` is cleaned on save and again at render (`entity.SanitizeInlineHTML`): `strong em b i u s code
  br a` only; links keep `href` (the same rule as `render.safeURL`: `javascript:`, `data:` and `//host` are dropped and
  the text kept) and `target="_blank"`, which always gets `rel="noopener noreferrer"`; every other attribute is removed;
  unclosed tags are balanced. The bridge never sends raw markup: it posts a canonical form and the server sanitises it
  again. The canonical form is one serialisation shared by the sanitiser's output, the canvas bridge (`ser`) and the
  inspector editor (`canonHTML`): text escapes only `&`, `<`, `>` (`'` and `"` stay literal), `"` is `&quot;` inside
  attributes only, a line break is `<br>`, a link is `<a href=… target="_blank" rel="noopener noreferrer">`, and an href
  is lower-case scheme with spaces, non-ASCII and `"<>`\^{|}` percent-encoded. Sanitising canonical html changes nothing,
  so a save never differs from what the editor sent and the page is not reloaded under the author's hands.
  `backend/testdata/inline/canonical.json` pins all three in Go and node tests.
* **No patch per key.** Typing posts `text` and sends no `patch` to the frame, so the caret never moves; when the edit
  ends the node is re-rendered from the server-sanitised output.
* **Inspector.** The **Text** of an unbound heading or text is a small editor (B, I, U, S, code, Link) that stays in
  step with the canvas. It works by keyboard and at phone width. A bound text keeps the Bind control and no editor.
* **History and diff.** The version diff shows tag-stripped text with a "formatting changed" note when only marks
  differ; names and hints in the tree use the plain text of `html`.
* **Components.** A click on a theme component's field on the canvas selects the component and moves focus to that
  field in the inspector (the rich editor for rich text); typing in a plain field there puts the stored text back and
  focuses the field.

### Element settings (builder-e6)

Settings that change the element's public markup, not its style. All are optional; a page that uses none is
byte-identical in output and storage.

| Prop | Kinds | Output |
| --- | --- | --- |
| `anchor` | every kind except `component`, `ref`, `slot`, `spacer` | the public `id`. Pattern `^[a-z][a-z0-9-]{0,63}$`, stored as typed (upper case is a 422, the inspector lowercases while typing) |
| `aria` | same | `aria-label` (at most 200 characters, no control characters). Start it with the visible text, so voice-control users can say what they read. Not offered (the schema refuses it) where `aria-label` is prohibited or already written by the widget: `text`, `icon`, `menu`, `breadcrumbs`, `tabpanel`, and every kind whose root is a role-less `div` (`gallery`, `columns`, `buttons`, `stat`, `accordion`, `tabs`, `loop`, `loopempty`, `html`, `richtext`, `form`, `video`). A `container` takes it only with a link or a landmark tag; a stale value is ignored at render and the editor clears it when the link or tag goes |
| `tip` | same | the `title` attribute (same limits) |
| `href`, `target`, `rel` | `button`; `container` and `image` for `href`, `target`, `rel` | the link. `target` is `_blank` only. `rel` holds `nofollow`, `sponsored` and `ugc` (at most three, any order in storage) |
| `caption` | `image` | a `figure` with a `figcaption` |
| `lightbox` | `image`, `gallery` | the image links to its full file and opens in a dialog; a gallery makes every image one group |

* **Links.** `target="_blank"` always gets `noopener noreferrer`; `rel` is written in the fixed order `nofollow sponsored
  ugc noopener noreferrer`, duplicates dropped. Without `_blank`, `rel` carries only the chosen tokens. `target` and `rel`
  on a node without a link are ignored at render, and the inspector removes them when the link is cleared. URLs follow
  the usual rule (`http`, `https`, `mailto`, `tel`, relative, `#`).
* **Image markup.** An image with no caption, link or lightbox renders exactly the old `img`, plus `id`, `title`,
  `width` and `height` when set. Otherwise the outermost element (`figure`, or the link when there is no caption)
  carries the node class, `id`, `aria-label` and `title`. On a bare image `aria` is not rendered (the alt text is its
  name; the inspector hides the field); on a link it is the link's accessible name. A lightbox link points at the
  original file, so it works without script; its name is `aria`, then the caption, then the alt text.
* **Image size (CLS).** When an image's `src` is a library file, the page writes its intrinsic `width` and `height`
  (read from the media row; none for SVG or external images). The stylesheet keeps `height:auto`, so the box is reserved
  before the file arrives and layout does not shift. This is the one intended change to existing live pages: images
  from the library gain the two attributes. **Limit:** the size is that of the stored original, whose EXIF
  orientation is kept. A photo taken sideways on a phone is displayed rotated, so its reserved box has the two sides
  swapped and the page still shifts once for it (the "CLS 0" figure holds for upright images).
* **Container link.** A `container` with `href` renders as a link instead of a `div` (`tag` must be absent or `div`),
  with `hm-c--link`. It may not hold interactive content, because a link inside a link is invalid: `button`, `buttons`,
  `menu`, `breadcrumbs`, `map`, `video`, `accordion`, `tabs`, `form`, `html`, `richtext`, `loop`, `component`, `ref`,
  `slot`, any plugin widget, an image with a link or lightbox, a gallery with a lightbox, a container with its own
  `href`, and a heading or text whose `html` holds a link. Plain heading, text, icon, list, quote, stat, table, divider,
  plain image and gallery and nested containers are fine. The save is refused with the child named. At render the rule
  is checked again on the expanded tree (a `ref` is resolved); a failing container degrades to a `div` without a link.
  A new widget template that contains a link, button or form control must be added to the interactive set (a test scans
  the widget templates).
* **Write rules.** Relational rules run on writes only (`Doc.CheckSettings`: the editor save, builder ops, REST and MCP writes), never in `Parse`, so a stored page is never refused at render. Refused with a 422 that names the cause:
  an anchor used twice in the doc (hidden nodes count), a reserved anchor (`main-content`, `toc`, `top`, `main`,
  `content`, anything starting `hm-` or ending `-strip-label`, or the id of a node of the same doc), `href` and
  `lightbox` on one image ("choose a link or a lightbox"), `href` on a container with interactive content or with a
  `tag` other than `div`.
* **Render-time duplicates.** Anchors are never prefixed. When blocks, parts or the page repeat an anchor, the first
  one rendered (page body, then parts, in render order) keeps the `id`; later ones drop it silently (the canvas marks the element
  with `data-hm-anchor-dropped`). A reserved anchor is dropped at render too.
* **v1 sections.** `anchor`, `aria` and `tip` are not part of `page_sections`. A top-level section that carries one is
  not v1-shaped: the page takes the native path, like any other non-whitelisted prop. The inspector says so under
  the Anchor field. Without them a v1 page round-trips unchanged.
* **Plugin widgets.** A pack widget may not declare `preset` or `navlabel`, and installing a new one that declares
  `anchor`, `aria` or `tip` fails with "property name reserved". A package installed before these settings existed
  keeps working: for a widget that declares one of the three, the builder skips that element setting (the prop is the
  plugin's own, validated by its schema and passed to its template) and logs a warning at startup. Otherwise the
  renderer strips the three element settings from the props a pack widget receives.
* **Lightbox script.** Pages with a lightbox load one extra script, a core island served as
  `/builder/lightbox.<sha8>.js` (hashed, strict-CSP safe, no inline code); other pages load none, and
  `public_builder_js_scripts` stays 0. A click without modifier keys opens a `dialog` (focus on Close, Esc closes, Tab
  stays inside, Left and Right move through a gallery, focus returns to the link); any other click, or no script, follows
  the link to the file. The page behind the open dialog does not scroll, and a screen reader hears the position, the
  alt text and the caption when the dialog opens and on each Left or Right. The canvas does not build the dialog.
* **CSS.** The extra rules (`figure`, `figcaption`, linked images and containers, the dialog) are written only on pages
  that use the feature, so the base stylesheet and other pages stay byte-identical.
* **Lint.** The widget lint fails a `target=_blank` link without `noopener` and a link inside a link, and an
  empty-alt lightbox image without a name has no accessible text. The inspector warns on such an image (a decorative
  one needs a caption) and on a gallery that has some; an explicit save adds a note with the count
  (`Doc.LightboxUnnamed`).
* **Inspector.** Link options and Anchor, Accessible name and Tooltip are a lazy chunk. The Link field offers published
  pages and `#anchors` of this page (anchors inside blocks and parts are not listed).

**Rollback floor for element settings (builder-e6).** An older binary has no such props and rejects the doc
(`additionalProperties:false`): the public page renders an **empty body** and the admin shows the page as broken (saves
refused with 422 `invalid_page_doc`). Before a rollback, remove `anchor`, `aria` and `tip` (any kind), `target` and
`rel`, `href` on a container or image, `caption` and `lightbox` from every page, block, part and saved version that must
keep rendering. To find them, search the stored docs for the keys, for example in SQL
`attributes::text ~ '"(anchor|aria|tip|caption|lightbox|target|rel|href)":'` over `hatamoto_entities` and
`hatamoto_entity_versions` (a hit on `href`, `target` or `rel` needs a look at the node kind: buttons and menus used
them before, so expect false positives from them). Pages without a hit are unaffected.

### UX canvas and inspector (builder-ux-canvas-inspector)

A presentation pass over the canvas and the inspector. The document model, the ops and the stored keys are unchanged: no
migration, and **no rollback floor** (an older binary reads every page this work saves). Nothing new
is stored.

* **Canvas chrome** (lazy `canvas/chrome.js`, pure geometry in `canvas/points.js`, loaded after first paint, not in
  the canvas bridge). Built once and moved, never rebuilt by the marks redraw (the picker anchor survives). A sticky
  strip: breadcrumbs from the layout parent ids (a crumb selects that ancestor, "Page" selects nothing), zoom
  **Fit / 100 / 75 / 50** (an explicit zoom survives `fit()`; never below 0.5, except Fit in the phone shell), and a 4 px
  Tablet or Phone **band** with its label. One outline layer: with a v2 parent the parent draws hover (dashed,
  neutral) and selection; the frame's own outlines are v1-only, and the host ignores a v1 `hm:select` that would
  replace a deeper v2 selection. The empty-node floor is 64 px.
* **Toolbar.** A `role=toolbar` (roving tabindex) outside the selected element: handle, name, **Parent**,
  **Duplicate**, **Wrap** (accessible name "Wrap in container"), **Hide** ("Hide on this device" or "Show on this
  device"), **Delete**, **More** ("More element actions", the element menu). It sits above, below at the top edge,
  clamped, and shrinks while editing text so the format bar stays clear. The handle press is delegated on the stage.
* **"+" handles.** From `canvas/points.js`: page level, the selection's container and its parent, the last hovered
  container, and always inside an empty container; a keyed pool of 32 px buttons with an insertion line on hover. The
  picker's **Choose an element…** and **Insert a ready-made section…** open the Add tab (patterns shown) and insert at
  the same position (`screen.addAt`, `docPos`, which maps frame keys, v1 section keys included, to a doc position).
* **Text.** Enter edits the selected element's own text; the format bar shows B, I, Link, Clear on a heading (the
  `selection` message carries `kind`); the link panel is at most 360 px with a page and anchor list (`linkChoices`);
  Ctrl/Cmd+Z in a text edit undoes the last format from the bridge's snapshot stack (no redo). See
  [CANVAS.md](CANVAS.md#inline-rich-text).
* **Inspector** (first paint split: `inspector.attach(el)` keeps the chrome across `draw()`, and the Content and
  Advanced bodies, Copy/Paste style and the rest are lazy). Header (`inspheader`): name, device chip, **Element
  options** menu (Copy style, Paste style, **Restore from an earlier version…** once `api.restore` exists). The
  **Class** row is the preset panel (`presetpanel`) with the word "class" on screen; the stored key stays `preset`,
  and so do the save labels' underlying ops. **Style** opens with the State switch, then **Essentials** per kind
  (`essentials`: four to six keys, a key listed there is hidden in the groups, padding for every kind), then one
  **More style options** disclosure (the 13 groups, with Effects = shadow, opacity, move X/Y, scale). Each control has a
  source chip (**Set here**, **Inherited from …**, **From class .name**, **Custom value**) and one Reset that clears the
  key at the current width and state. **Show on** (`showon`, pure): three independent switches that compute `hide` and
  `display` writes for every width so one never changes another; the tree eye, the `H` key, the toolbar and the
  context menu use the same writer (`commitMany` takes a `bp` per write). The box model (`boxmodel`) nests padding in
  margin, with 0 and auto buttons and link toggles that write the linked sides in one batch. The colour control
  (`controlsui`, `colournames`) names site colours, shows **None**, keeps the last six custom colours per user
  (`hm.builder.recent-colours.<user>` in `localStorage`) and gives owners **Edit site colours**. Content tab
  (`inspcontent`): fixed field order, H1 to H6 buttons, a collapsed **Use data** toggle, **Number of columns**
  (`columnsops`: adds empty containers or removes empty trailing ones with the count, in one commit), the **Items** panel
  (`items`: Tabs, Accordion, Gallery children and List lines), a Form select, Video labels, plain icon names. History
  labels come from the inspector ("Change Font size (Tablet)", "Change Link", "Bind Text").
* **Element restore.** `GET {entity}/builder/node-versions?node=<id>` (owner or editor, the builder gate; boot key
  `urls.nodeVersions`; `handleBuilderNodeVersions`) looks through the newest 100 saved versions (the most one read gives) and returns up to 50
  of those that hold the element, newest first: `version`, `saved`, `by`, `summary`, the node without children (`t`, `p`, `s`, `b`),
  and its `parent` and `index`. It only reads. The lazy `noderestore` dialog and `restoreops` build the restore from
  ordinary ops in one commit, "Restore <name> from version N": the same kind sets only the props, styles and bindings
  that differ; another kind is removed and inserted again at the same place with the children the page has now.
  The dialog is titled with the element's outline name, chooses the newest version that differs from the page, writes
  times and dates with `@hm/dates`, says "This element cannot be restored here." when the store refuses a kind change,
  and puts focus on the element's outline row afterwards. Reachable from the inspector Element options and the element menu.
* **Icon size.** An Icon inserted in the builder measures 24 px in the canvas without any seed (`.hm-icon` is
  `1.5em`, and the canvas sheet carries every kind's CSS), so it grows with its text like an old one. An earlier seed
  (`x-hm.style`, `w: 1.5rem`) was removed for that reason; `builder-direct.mjs` checks the size.
* **Smaller items.** The media picker opens on a `role=status` "Loading images…" skeleton grid; the outline
  (`builder_outline.go`, `builderKindNames`, pinned to `kindnames.js`) names kinds with the widget label, else a plain
  name ("Rich text", "Accordion item", "Tab panel", "Empty state").
* **Lazy chunks.** Each of these stays out of first paint (`lazyOnly` in `builder_budget_test.go` enforces it, and that
  the total graph still reaches it): `canvas/fmtbar`, `canvas/chrome`, `canvas/points`, `builder/inspcontent`,
  `builder/inspheader`, `builder/essentials`, `builder/showon`, `builder/columnsops`, `builder/items`,
  `builder/restoreops` and `builder/noderestore`, besides the shell chunks listed under Budgets.

### Code

Server: `internal/admin/builder_*.go` (`editor` routing and boot data, `render`, `component`, `save`, `outline`,
`stylespec`), `builder.Kinds`, `NewNode`, `CoerceProp` in `internal/builder/kinds.go`. Client: `web/admin/js/builder/`
(see [ADMIN.md](ADMIN.md#builder-editor-b3)).

## Version history: layout changes

Compare of two versions of a builder page (a page whose `page_doc` parses in both versions) shows a **Layout
changes** table, rendered on the server (no JavaScript): one row per node with its label and hint, an operation
(Added, Removed, Moved, Changed), what changed (props, styles per breakpoint, bindings, kind) and where it sits. The
field table below it no longer lists the `page_doc.*` and `page_sections.*` rows (the node table replaces them; the
sections are a derived mirror). Any other entity, a version without a layout, or a layout that cannot be parsed
keeps the flat field table (with a muted line in the last case).

`builder.DiffDocs` (`internal/builder/diff.go`) is a pure function. Nodes are matched **by id**. A node is *moved*
when its parent differs or its position among the siblings present in both versions differs (so inserting a sibling
does not mark its neighbours moved); *changed* when kind, props, styles or bindings differ. An added or removed node
is reported once with its descendant count. The table lists the first 200 changes and says how many more there are.

**Restore this node from vN** (a form POST with CSRF, works without JavaScript) is offered on removed, moved and
changed rows, from the older version of the comparison (when that is not the current version). It re-checks that the
page is still at the version the comparison was built from (otherwise 409 and nothing is written), then:

* the node exists in the current page (a changed or moved row): its own fields (kind, props, styles, bindings) are
  put back and it **keeps the children it has now**, including ones added since; a moved node also goes back to its
  old parent and index. Changes inside the children have rows of their own and are restored one by one;
* it is gone (a removed row): the whole old subtree is inserted at its old parent and index when that parent exists,
  else at the end of the top level (the flash says so);
* an id of the restored subtree is already used by another node: refused, nothing is written.

The result is saved through the normal builder save (same validation, reuse checks and sanitiser) as one new version,
`Restored node "<label>" from version N`. Restoring a published page is live like any builder save. Added nodes have
no restore.

## Budgets

`make budgets` (part of `make check`) runs the `TestBudget*` tests of `internal/admin`, `internal/render`,
`internal/server` and `internal/budgetfile` and prints one line per metric:
`BUDGET render/page_css_allwidgets_gzip got=2378 baseline=2378 limit=3402 unit=bytes status=ok`. It exits non-zero if
any metric is over its limit or missing from its baseline. Only bytes and allocation counts decide; **timing is not a
gate** (the wall-clock tests `TestRenderBudgets`, `TestWidgetRenderBudget`, `TestLoop12RendersWithinBudget` and
`TestRenderNodesBudget` are backstops with ceilings of at least 10x the measurement, skipped under `-short` and `-race`).

Baselines are committed in `backend/testdata/budgets/{admin,render,server}.json` (value, unit, tolerance, optional
hard cap, update history). A plain run never writes them. When growth is intended:

```
make budgets-update REASON="B10 adds the gallery widget"
```

rewrites the values (it refuses without `REASON` and writes nothing), appends the reason to the history, and the
diff shows the new numbers. A retired metric is removed from the JSON by hand (an update never deletes a key), and its
history reason says what replaced it. Current baselines:

| Metric | Baseline | Limit |
|---|---|---|
| `admin/editor_js_first_gzip` (builder first-paint JS: static closure of `admin` + the builder.html module tags + preloads, gzip -9 per file) | 91746 B | +1 KiB, cap 93351 B (the cap in `admin.json` is the enforced one: 89255 + 4 KiB, set by builder-ux-shell for the tabbed shell, status chip, device labels and phone dock; builder-ux-canvas-inspector moved the inspector bodies to lazy chunks and stayed under it). Lazy chunks, not counted here: Style tab and its controls (`style`, `controls`, `controlsui`, `boxmodel`, `presetsrc`), preset panel, reuse UI, shortcut sheet, component fields, element menu, clipboard ops, layout picker, history panel and `historylabel`, drag, banners, `fmtbar`, `canvas/chrome` and `canvas/points`, the inspector chunks (`inspcontent`, `inspheader`, `essentials`, `showon`, `columnsops`, `items`), element restore (`restoreops`, `noderestore`), the inspector text editor, the component field jump, `elementsettings`, and the UX shell chunks `addtab`, `search`, `recent`, `patterns`, `commandk`, `savedialog`, `startpanel`, `coachmarks`, `sitestyles` and `colournames` |
| `admin/editor_js_total_gzip` (first paint plus every dynamic `import()` target: section-form widgets, Tiptap kit, Style tab, the shell chunks above) | 238906 B | +1 KiB, cap 241978 B (current + 3 KiB; builder-ux-canvas-inspector raised it from 220 KiB to 240 KiB for the lazy inspector, canvas and restore chunks, forge 1B lowered it to what is used) |
| `admin/canvas_host_gzip` | 7794 B | +512 B |
| `admin/admin_css_gzip` (admin CSS bundle) | 45143 B | +1 KiB |
| `admin/vendor_{htmx,sortablejs,tiptap}_bytes` and `vendor_total_bytes` | 52182 / 45478 / 560546 / 658206 B | exact, also equals `vendor.json` |
| `render/page_css_allwidgets_gzip`, `..._tokens_presets_gzip`, `..._parts_blocks_gzip` (`hm-builder-css`) | 2378 / 2676 / 2425 B (also `page_css_allstylekeys_gzip` 1861 B and `page_css_states_gzip` 885 B, same cap) | +1 KiB, cap 8 KiB |
| `render/public_builder_js_scripts` (all-widgets page without tabs/video) | 0 | exact |
| `render/theme_default_css_gzip`, `theme_default_js_gzip`, `canvas_bridge_gzip` | 13259 / 4965 / 15249 B | +1 KiB / +512 B / +512 B |
| `render/golden_<name>_bytes` (page-shell, hero, features, faq, pricing, cta, tpl-home, tpl-single, tpl-archive), `page_allwidgets_html_bytes` | see the file; 37085 B for the page | +5 % |
| `render/alloc_{RenderNode10,RenderTop10,RenderPage72,RenderPageStat72,Loop12}_{allocs,bytes}` | 11 / 42 / 528 / 2143 / 14 allocs | +10 % (+2 allocs, +64 B) |
| `server/alloc_RenderNodesEndpoint_{allocs,bytes}` | 10190 allocs, 682905 B | +10 % |

Allocation metrics are the minimum of three `testing.Benchmark` runs and are skipped under `-short` and `-race`.

The `render.head` hook (and `HeadFilter`) runs once per render, including on pages with a paged loop: the head is built
after the page body, so the paged-loop head replaces it instead of being built first.

## Write rules

Every write path (create, update, restore, import) goes through one function, `normalizeDoc`, in `internal/entity`. The reuse rules (a `slot` only in a layout part, block references without a loop) run in the admin builder save and in the MCP tools (`builder_apply_ops`, and `entities_update` when it carries a `page_doc`); the REST API does not check them yet.
`W` is the incoming `attributes`, `S` the stored ones. The rules are the same for REST, MCP, the admin entity form and
plugins. Nothing writes `page_sections`: in the stored attributes it is the stored value carried forward unchanged, or
absent when there was none.

| Write | Result |
| --- | --- |
| `W.page_doc` present and different from `S.page_doc`, or `S` has no doc | **doc write**: validated, component content sanitised, stored. Any `W.page_sections` is ignored (exports and old tooling may send both) |
| `W.page_doc` absent or equal, `S` has a doc, `W.page_sections` absent, or equal to the stored or the derived sections | no-op round trip (read-modify-write clients keep working; no new version) |
| same, but `W.page_sections` is anything else | **rejected: 422 `page_sections_removed`**, nothing written |
| `S` has no doc, `W` (else `S`) has a `page_sections` array | **convert once**: the doc is built from the sections (the deterministic conversion the CLI uses), stored; when the sections came from `W` and differ from `S` a deprecation warning is logged. This window exists for creates and unconverted pages and goes away in a later release |
| restore of a version blob | a blob with `page_doc` is a doc write, a blob with only sections converts to a doc; a blob whose sections do not convert (see ids below) fails with `invalid_page_doc` (422) |
| no doc and no `page_sections` anywhere | untouched (non-page entities) |

Posts (the post-editor types) and persons are not converted: their `page_sections` is ordinary data and is never
rejected. A name-only update of an unconverted page converts it (one version). A `before_save` plugin hook sees derived
sections; a change it makes to `page_sections` is discarded with a warning, a change to `page_doc` is kept and
validated.

Conversion is deterministic and never repairs ids: section ids become node ids, and the public HTML carries them. Sections
whose ids are duplicate, empty, longer than 64 characters or use characters other than letters, digits, `-` and `_` do
not convert. A write that carries them (API, MCP, plugin, restore) fails with 422 `invalid_page_doc`; a page already
stored that way is left as it is by unrelated writes, and the builder shows a recovery screen (no write route accepts
it). `hatamoto builder convert` cannot fix such a page either: it does not repair ids, so its render check reports a difference and writes nothing. The fix is to make the ids valid (in the stored sections, or by sending a corrected `page_doc` through the API or MCP), which changes the section `id` attributes in the public HTML. Repeating a save creates no phantom version.

### Reading `page_sections`

The key of a stored row is frozen, so readers do not use it. For a page with a valid doc, the public pages JSON, the
plugin host (`entities.get`/`entities.list`) and the `before_save` hook input carry `page_sections` **derived** from
`page_doc` (`entity.DerivedAttributes`): the sections of a doc v1 can express, and no key for a builder-only doc. Admin
REST and MCP responses return the stored attributes. Pages without a doc still render from `page_sections`; a stored doc
that fails validation renders an empty body and logs a warning (it does not fall back to the frozen key).

## Errors

| Error | HTTP | `code` |
| --- | --- | --- |
| Invalid doc (`ErrInvalidDoc`) | 422 | `invalid_page_doc` |
| Changed `page_sections` on a page that has a doc (`ErrPageSectionsRemoved`) | 422 | `page_sections_removed` |

Neither is a 400 `validation failed`. MCP returns the message ("page_sections can no longer change a page that has a
page_doc; send attributes.page_doc"); the admin entity form shows it as a 422 notice. The former 409 `builder_layout`
no longer exists.

## Converting existing pages

Pages convert lazily (see [Write rules](#write-rules), and in memory when opened in the builder) or in bulk through
`hatamoto builder convert` ([CLI.md](CLI.md#builder-convert)). The CLI renders each page as sections and as a doc
through the same view and compares the bytes before anything is written; a difference is reported, that page is skipped
and the command exits 1. Writes go through `entity.Service.UpdateEntity` with the summary "Converted to builder doc":
one version is added, but the page keeps its modified date (`updated_at`, so JSON-LD `dateModified` and the sitemap
`lastmod` do not move). The stored `page_sections` is left alone, the public HTML is unchanged, so a page the running
server has cached stays correct either way (it clears its cache within seconds of the write): the CLI is a separate
process and does not fire plugin events or reindex links (see [CLI.md](CLI.md)). There is no `--revert` (it exits with "unsupported": a revert
would resurrect a frozen, stale `page_sections`). Operator runbook: `--dry-run --verify-render`, then the real run, then
check the pages. At boot `serve` logs `pages without page_doc` with a count and this hint.

The CLI verifies with the built-in theme; the active theme package is not loaded by the CLI. Re-run the dry run on the
host that serves the site for the final check.

## Theme requirements

- The `section` partial must honour `.Comment` (the default partial prints it with no wrapper). A theme that drops
  that branch wraps native nodes in a section and is unsupported for builder-only pages.
- The head must call the `plugin-head` partial. If it does not, the engine splices the `<style>` before the last
  `</head>`.

## Known gaps (for the editor work)

- `render.page` plugins do not see builder-only pages.
- A page with a stored (frozen) `page_sections` and a doc can approach 2 MB in the row; the API body cap is 1 MB.
- Posts are out of scope: only pages and types with sections.
