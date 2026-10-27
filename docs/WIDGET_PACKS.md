# Widget packs and builder data sources

A package can add widgets to the page builder and supply live data to it
without a change to Hatamoto itself.

- A **widget pack** is a plugin package (`plugin.json`) that ships `widgets/`.
  Each widget is validated at install, shows up in the palette under **Plugin
  widgets**, renders on the canvas, the public page and the no-JS outline, and
  degrades to a placeholder when the package goes away.
- A **data source** is a plugin that answers the `builder.data` filter. It
  supplies values for bindings and item lists for query loops, in one batched,
  time-boxed, capped call per page render.

Reference code: [`examples/plugins/widgetpack-demo`](../examples/plugins/widgetpack-demo)
(no code, two widgets) and [`examples/plugins/datasource-demo`](../examples/plugins/datasource-demo)
(a data source). Background: [BUILDER.md](BUILDER.md), [PLUGIN_API.md](PLUGIN_API.md),
[PACKAGES.md](PACKAGES.md).

## Layout

```
plugin.json                          the manifest (kind is "plugin", as for any plugin)
plugin.wasm                          optional when widgets/ is present
widgets/<name>/schema.json           props and palette metadata
widgets/<name>/widget.html           the template
widgets/<name>/widget.css            the styles
widgets/<name>/island.js             optional enhancement script
```

`<name>` matches `^[a-z][a-z0-9-]{0,31}$` (no underscore, no capitals). Nothing
else may live under `widgets/` and it is two levels deep at most. A **theme**
package cannot hold `widgets/` (theme-shipped widgets are not supported). A
package holds at most 16 widgets; a site registers at most 64 widget kinds in
total (the rest are skipped and logged, by plugin name).

A widget-only pack has no `plugin.wasm`: it lists as a plugin with no hooks,
and disabling or removing it removes its widgets. A pack may also ship wasm,
for example a data source for its own widgets.

### The kind name

The widget `<name>` of plugin `<plugin>` is the builder kind **`<plugin>_<name>`**
(`widgetpack-demo_promo`). No core kind contains an underscore, and plugin and
widget names cannot, so a pack can never shadow a core widget and two packs
cannot collide. The kind is what a saved page stores in `t`, what the CSS root
class is built from (`hm-<plugin>_<name>`) and what `data-hm-t` shows on the
canvas.

## schema.json

A JSON Schema (2020-12) for the widget's props, plus an `x-hm` object for the
palette:

```json
{
  "type": "object",
  "additionalProperties": false,
  "x-hm": {
    "label": "Promo banner",
    "hint": "A dismissible strip with a call to action",
    "children": false,
    "default": {"title": "Spring sale"}
  },
  "properties": {
    "title": {"type": "string", "maxLength": 80, "x-hm-bind": "text"},
    "href": {"type": "string", "maxLength": 300, "x-hm-bind": "url"},
    "tone": {"enum": ["info", "warn"]}
  }
}
```

Rules, all checked at install (and again when the site loads the package):

- `additionalProperties: false` at the top; `x-hm.label` is required.
- `x-hm.group` is not yours to choose: the widget always sits in **Plugin widgets**.
- No array props (the same rule as core widgets); at most 24 props; the file is at most 16 KiB.
- `x-hm-bind` is `text`, `url` or `image`: the prop then offers the editor's "Bind" control for sources of that class.
- `x-hm-widget` hints the inspector control (`textarea`, `datalist:<options>` and so on, as in core).
- `x-hm.children: true` lets the widget hold child nodes (`{{.Children}}`).
- `x-hm.seed` and `x-hm.only_in` may name only this package's own widgets, native kinds or core widgets.
- `$ref` to anything outside the file is refused.

## widget.html

A Go `html/template` with one root element. The template's dot has four fields:

| Field | Meaning |
|---|---|
| `.Attrs` | the root element's class and (canvas only) editor markers: **put `{{.Attrs}}` on the single root element**; a template without it is refused |
| `.P` | the node's props, a map keyed as in `schema.json` |
| `.Children` | the rendered child nodes |
| `.Canvas` | true in the builder canvas |

Only an allowlist of functions is available: `eq`, `ne`, `lt`, `le`, `gt`,
`ge`, `and`, `or`, `not`, `len` and `index` (plus the `if`, `range` and `with`
actions). There is no function map, and anything that builds a string (`print`,
`printf`, `println`, `slice`, `html`, `js`, `urlquery`) or `call` is refused:
a template cannot be interrupted, so such a function could burn CPU and memory
without writing a byte. Print values directly; `html/template` escapes them.
`template`, `define` and `block` are refused too. Values are escaped by `html/template` for their context
(text, attribute, URL), so `{{.P.title}}` is safe and `<a href="{{.P.href}}">`
cannot carry `javascript:`. Guard optional props with `{{if .P.x}}`, because the
sample render at install uses typical values only for common prop names. An
`{{if .P.x}}` branch the sample never takes is still validated.

The template may not contain `script`, `style`, `iframe`, `object`, `embed`, `link`,
`meta`, `base`, `form`, `svg`, `math` (and a few other raw-text or foreign
elements), an attribute starting with `on`, a `style=`, `srcdoc`, `formaction`
or `xlink:href` attribute, or a `javascript:` / `data:` / `vbscript:` URL. A link's `target` is dropped from the output (no new tabs, no `window.opener`). The
template is at most 16 KiB.

Install checks the template's **parse tree**, not just a sample render:

- An action may print in **text**, or inside a **quoted attribute value** of
  `class`, `title`, `alt`, `lang`, `role`, `aria-*`, `data-*` and similar
  plain attributes, or be the **entire quoted value** of `href` or `src`
  (so `html/template` filters the scheme). An action inside a tag name, an
  attribute name, an unquoted value, a comment, `id`, `name`, `srcset` or any
  other attribute is refused. Only `{{.Attrs}}` may sit between attributes.
- An `{{if}}`, `{{range}}` or `{{with}}` must open and close its markup in the
  same branch.
- The markup is scanned for the banned tokens with every action removed and
  again with every action replaced by a letter, so a split such as
  `<scr{{if x}}{{end}}ipt>` is caught; the sample render is one more layer.
- The markup tokenizer is kept simple on purpose: `<!` and `<?` (comments,
  doctype, processing instructions) and `</` followed by anything but a letter
  are refused. Each `{{if}}`/`{{else}}`/`{{range}}`/`{{with}}` body is also
  scanned on its own, not only joined with its neighbours.
- `{{range}}` may only iterate a prop (`.P` or `.P.name`), never a number or a
  function result, and a `{{range}}` may not sit (lexically) inside another
  `{{range}}`. `$` and variables (`{{$x := ...}}`, `range $k, $v := ...`) are
  refused. `{{with}}` takes a field chain or `.` only (`{{with .P.title}}`), so
  the dot can narrow but never jump back to the root. The parse tree is capped
  at 2000 nodes. A validator panic on a hostile tree is recovered and turns into
  a refusal of that widget (logged), never a crash.

- The markup must be **balanced**: an element left open or an end tag that
  closes nothing (`<a>` without `</a>`, `</main>`) is refused. Only elements
  with an optional end tag (`li`, `p`, `td`, `tr`, `option`, ...) may stay open.
  The `main` element is not allowed in a pack's output, and neither are `header` and
  `footer`, which the theme owns.

At run time a pack widget renders into a scratch buffer capped at 256 KiB (its
own markup, without its children). The output is then sanitised and re-parsed
as the content of a `<div>`, which closes any element still open and drops stray
end tags, so a widget can never change how the markup after it is parsed. A template error or an overflow replaces the
widget with a placeholder (a comment publicly, a box in the canvas) and the
page is not cached. The error names the file and the rule.

### The output is sanitised

Validation is the first wall. As a second, **every pack widget's own output is
sanitised** after the template runs, with a strict allowlist policy: common
structural and text elements (no `script`, `style`, `iframe`, `object`,
`embed`, `meta`, `link`, `base`, `form`, `svg`); the attributes `class`, `id`,
`role`, `title`, `lang`, `dir`, `tabindex`, `aria-*`, `data-*` (not `data-hm-*`,
which belong to the core), and a few element-specific ones (`alt`, `width`,
`height`, `loading` on `img`, `colspan`, `for`, ...); no `on*`, `style` or
`srcdoc`; URLs only `http`, `https`, `mailto`, `tel`, relative and `#fragment`.
The core's own parts, the root `{{.Attrs}}` and the rendered children, are
opaque to the sanitiser and come back unchanged. Children may therefore be
printed **once** (`{{.Children}}` twice replaces the widget with the failure
placeholder). If the sanitiser changes anything, the change is served and a
warning is logged (once per kind and minute): treat it as a bug in the pack.
The cost is about 6 µs per widget; core widgets are not sanitised.

## widget.css

At most 16 KiB. **Every selector must start with the widget's root class**
`.hm-<plugin>_<name>`, on a class boundary, so a pack can never restyle the
page: `.hm-acme_card .title` is fine, `.hm-acme_card__title`, `body`, `:root`,
`.other` and `.hm-acme_cards` are refused. Only `@media` and `@supports`
at-rules are allowed (their rules are checked the same way). The CSS is
minified first and the **minified text, the one that is served**, is validated.
`url(`, `image-set(`, `image(`, `cross-fade(`, `element(`, `src(`, `@import`,
`@font-face`, `@charset`, `@namespace`, `@keyframes`, `@property`, `expression`,
`-moz-binding`, `:has(`, any `position` value other than exactly `static` or `relative` (functions such as `env()`, `if()`, `attr()` and `var()` included)
(`absolute`, `fixed`, `sticky`, the keywords `inherit`/`initial`/`unset`/`revert`
and `var(...)` are refused, so a widget cannot be taken out of the flow or fixed
to the viewport; it is **not** confined to its box: negative `top`/margins or a
`transform` can still draw it over neighbouring content, and the box's own
`overflow` is the only clip), strings
with a line break and script constructs are refused. Use the theme's tokens, `var(--hm-space-3, .75rem)`,
`var(--hm-color-text, ...)`, with a fallback.

The CSS is minified and sent on public pages only when the widget is present;
in the canvas it is always present.

**Natural display.** A pack cannot declare its root's `display`. When the builder
un-hides a pack widget under a wider "Hide on" (mobile-only layouts), it writes
`display: block`, so a root that is `flex` or `grid` loses that layout on the
device it was un-hidden for. Keep the root a block and put flex or grid on an
inner element.

## island.js

An optional script, at most 32 KiB, served as `<script type="module" src>` from
`/theme/assets/builder/<kind>.<hash8>.js` (content-hash name, immutable cache,
`nosniff`), only on public pages that use the widget, never in the canvas and
never inline. The tag carries `integrity="sha256-..."`.

**An island is first-party JavaScript.** It runs on your public pages with the
visitor's privileges (an administrator who views the page included); the public
CSP (`script-src 'self'` for themes that opt in to `csp.strictScripts`, such as
the default theme, otherwise `script-src 'self' 'unsafe-inline'`; possibly
report-only) allows it (a first-party `'self'` script) and does not contain it. So it is a permission: a package that ships any `island.js` must
list **`builder.island`** in `plugin.json`, the install and update prompt shows
it as "runs JavaScript on your site's pages", and an update that newly asks for
it needs the owner's confirmation and clears the grants. Without the grant the
package installs and its widgets render, but the island is not served. The
install scan (`eval(`, `new Function`, `document.write`, dynamic `import(`,
`importScripts`, absolute `http(s)://` URLs, protocol-relative `//host`) is a
lint, not a sandbox. Install only packs you trust (signature). Write it as a
module without globals and make the server output usable without it.

For the same reason the **install-time validation is the boundary for templates
and CSS** (the public CSP allows `https:` images and inline styles), and the
`builder.island` grant is the boundary for JavaScript. Tightening the public CSP
is a follow-up.

## Try a pack locally

```sh
hatamoto plugin pack path/to/pack --out pack.hmp
hatamoto package install pack.hmp --source dev --dev --confirm
```

The widgets appear in the builder without a restart. A refused install prints
`widgets/<name>/<file>: <rule>: <detail>`. See [PACKAGES.md](PACKAGES.md) for
signing and the unsigned opt-in.

## Uninstall, disable and update

Removing or disabling the package unregisters its kinds at once. Saved pages
keep their nodes (**orphans**):

- public page: the comment `<!-- hm-widget: <kind> not installed -->` in place of the widget (its children are dropped);
- canvas: a selectable box "Widget "<kind>" is not installed"; the outline form shows the same notice;
- the page still validates and saves; move, hide and delete work; edits of the orphan's props are refused ("widget not installed");
- reinstalling restores the widget with all props intact.

A kind that is not shaped like a pack kind is still `unknown kind`.

## Data sources

A plugin that answers `builder.data` declares its sources in `plugin.json`,
asks for the `builder.data` permission and exports `filter_builder_data`:

```json
{
  "name": "weather-demo",
  "sdk": "^0.11",
  "permissions": ["builder.data"],
  "builder_sources": [
    {"name": "temp", "label": "Temperature", "kind": "value", "class": "text"},
    {"name": "forecast", "label": "Forecast", "kind": "items"}
  ],
  "limits": {"hook_timeouts_ms": {"builder.data": 300}}
}
```

| Field | Rule |
|---|---|
| `name` | `^[a-z][a-z0-9_-]{0,31}$`, unique in the plugin; the full source id is `ext.<plugin>.<name>` |
| `label` | 1 to 60 characters, shown in the editor |
| `kind` | `value` (one string for a prop) or `items` (a list for a loop) |
| `class` | `text`, `url` or `image`; only for `value`; decides which props may bind it |

At most 8 sources. Declaring sources requires the `builder.data` permission,
and the filter is registered **only while the owner has granted it**: without
the grant the sources are not offered, the plugin is never called, and saved
bindings resolve empty.

### Using a source

- **Binding**: a value source is offered in a prop's **Bind** list (grouped by plugin name) and stored as `b: {"title": "ext.weather-demo.temp"}`. A text prop takes any source; a url prop takes url sources; an image prop takes image sources.
- **Loop**: an items source is offered in the query loop's **Source** field: `from: "source"`, `source: "ext.weather-demo.forecast"`. Items feed the usual item scope: `loop.item.title`, `.url`, `.excerpt`, `.image`, `.category`, `.author`, `.author_url`, `.published_at` and `loop.item.attributes.<k>` (from `attrs`). The loop's `limit` applies; there is no paging.

A binding or loop naming a source that is not registered (plugin disabled or
removed) stays valid: it renders empty (a loop shows its empty state), and the
canvas shows `‹ext.plugin.name›`.

### The hook

Input and output are the same JSON object (`BuilderDataRequest` in `hatamoto.dev/sdk`):

```json
{ "path": "/", "language": "en", "schema_slug": "page", "entity_id": "",
  "requests": [ {"id": "v:ext.weather-demo.temp", "source": "ext.weather-demo.temp"},
                {"id": "i:ext.weather-demo.forecast:5", "source": "ext.weather-demo.forecast", "limit": 5} ],
  "results":  [ {"id": "v:ext.weather-demo.temp", "value": "21C"},
                {"id": "i:ext.weather-demo.forecast:5", "items": [ {"title": "Day 1", "url": "/f/1", "attrs": {"temp": "16C"}} ]} ] }
```

Answer every request in `results`, matched by `id`; a non-empty `error` marks one
request failed. An item has `title`, `url`, `excerpt`, `image`, `category`,
`author`, `author_url`, `published_at` (RFC 3339 or `YYYY-MM-DD`) and `attrs`.

- **Ownership**: the host sends a plugin only the requests naming its own declared `ext.<plugin>.*` sources, and takes back only results for those ids. A plugin cannot answer for another.
- **Batching**: one call per page render carries every request of the page, theme parts and synced blocks included. Theme parts (header, footer, content wrapper) share one more batch, so at most two calls happen per render; there is never a call per node or per item. Requests are deduplicated; at most 16 distinct requests per render.
- **Caps**: at most 48 items per request (the request's `limit` is at most 48), strings at most 2048 bytes of valid UTF-8, at most 16 `attrs` keys per item, output JSON bounded (64 results/items per array, depth 6) and by `max_output_bytes`. A result that breaks a cap is dropped **whole**, never partially.
- **Time**: the manifest's `limits.hook_timeouts_ms["builder.data"]` (default 250 ms, at most 10 s), inside a 500 ms budget for the whole batch.
- **Safety**: values and item fields are ordinary props to the renderer: escaped by context, URLs through the same safe-URL check (a `javascript:` value renders an empty `href`), images through the usual variant path.
- **Failure**: an error, trap, timeout, circuit-breaker skip or malformed output gives empty props and the loop's empty state. The page is served, marked degraded, and **not stored in the page cache**.
- **Freshness**: a successful result is cached with the page and is as fresh as the page cache (cleared by any content write or package change). There is no per-source TTL.

`examples/plugins/datasource-demo` shows all three sources and a `mode` config
value (`ok`, `slow`, `trap`, `big`, `liar`) to watch each failure behaviour.

## Limits at a glance

| What | Limit |
|---|---|
| widgets per package / on the site | 16 / 64 |
| props per widget, schema file | 24, 16 KiB |
| `widget.html`, `widget.css` | 16 KiB each |
| `island.js` | 32 KiB |
| `builder_sources` per plugin | 8 |
| requests per render, calls per render | 16, 2 |
| items per request, string, attrs per item | 48, 2048 bytes, 16 |
| batch budget, default source timeout | 500 ms, 250 ms |
